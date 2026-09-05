package ssmbootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type fakeSSM struct {
	sent       *ssm.SendCommandInput
	invocation *ssm.GetCommandInvocationOutput
	commands   *ssm.ListCommandsOutput
}

func (fake *fakeSSM) ListCommands(context.Context, *ssm.ListCommandsInput, ...func(*ssm.Options)) (*ssm.ListCommandsOutput, error) {
	return fake.commands, nil
}

type fakeProgress struct {
	input *s3.GetObjectInput
	body  string
}

func (fake *fakeProgress) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	fake.input = input
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewBufferString(fake.body))}, nil
}

func TestNewReportsExactMissingConfiguration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "asset bucket", edit: func(config *Config) { config.AssetsBucket = "" }, want: "SESSION_ASSETS_BUCKET"},
		{name: "script key", edit: func(config *Config) { config.BootstrapScriptKey = "" }, want: "BOOTSTRAP_SCRIPT_KEY"},
		{name: "metadata table", edit: func(config *Config) { config.MetadataTableName = "" }, want: "METADATA_TABLE_NAME"},
		{name: "Steam authorization cache", edit: func(config *Config) { config.SteamAuthSecretID = "" }, want: "STEAM_AUTH_SECRET_ID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := testConfig()
			test.edit(&config)
			_, err := New(&fakeSSM{}, config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %v; want missing %s", err, test.want)
			}
		})
	}
}

func (fake *fakeSSM) SendCommand(_ context.Context, input *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	fake.sent = input
	return &ssm.SendCommandOutput{Command: &types.Command{CommandId: aws.String("command-1")}}, nil
}
func (fake *fakeSSM) GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return fake.invocation, nil
}

func TestObserveProgressUsesWorkflowScopedLiveSnapshot(t *testing.T) {
	client := &fakeSSM{invocation: &ssm.GetCommandInvocationOutput{Status: types.CommandInvocationStatusInProgress}}
	progress := &fakeProgress{body: "GSP_CHECKPOINT:HOST_PREPARED\nGSP_CHECKPOINT:GAME_SERVER_INSTALLED\nGSP_ACTIVITY:ARMA_SERVER\n"}
	runner, err := New(client, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	runner.WithProgressStore(progress)
	status, err := runner.ObserveProgress(context.Background(), "i-1", "command-1", "session-1", "workflow-1")
	if err != nil || status.Activity != "Arma 3 server files" || !reflect.DeepEqual(status.Checkpoints, []domain.ProgressMilestone{domain.ProgressHostPrepared, domain.ProgressGameServerInstalled}) {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
	if got := aws.ToString(progress.input.Key); got != "sessions/session-1/runtime/bootstrap-progress-workflow-1.txt" {
		t.Fatalf("progress key = %q", got)
	}
}

func TestResolveContentCommandRequiresOwnedCommentAndSingleInstance(t *testing.T) {
	client := &fakeSSM{commands: &ssm.ListCommandsOutput{Commands: []types.Command{{Comment: aws.String("gsp:workshop-sync:session-1:workflow-1"), InstanceIds: []string{"i-1"}}}}}
	runner, err := New(client, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, workflowID, instanceID, err := runner.ResolveContentCommand(context.Background(), "command-1")
	if err != nil || sessionID != "session-1" || workflowID != "workflow-1" || instanceID != "i-1" {
		t.Fatalf("identity = %q %q %q, err=%v", sessionID, workflowID, instanceID, err)
	}
	client.commands.Commands[0].Comment = aws.String("unowned command")
	if _, _, _, err := runner.ResolveContentCommand(context.Background(), "command-1"); err == nil {
		t.Fatal("unowned command was accepted")
	}
}

func TestFindContentCommandRequiresExactWorkflowCommentAndInstance(t *testing.T) {
	client := &fakeSSM{commands: &ssm.ListCommandsOutput{Commands: []types.Command{
		{CommandId: aws.String("wrong-instance"), Comment: aws.String("gsp:workshop-sync:session-1:workflow-1"), InstanceIds: []string{"i-2"}},
		{CommandId: aws.String("command-1"), Comment: aws.String("gsp:workshop-sync:session-1:workflow-1"), InstanceIds: []string{"i-1"}},
	}}}
	runner, err := New(client, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := runner.FindContentCommand(context.Background(), "session-1", "workflow-1", "i-1")
	if err != nil || commandID != "command-1" {
		t.Fatalf("command = %q, err = %v", commandID, err)
	}
	if _, err := runner.FindContentCommand(context.Background(), "session-1", "workflow-other", "i-1"); err == nil {
		t.Fatal("mismatched workflow command was accepted")
	}
}

func TestStartContentRestartsOnlyWhenPromotingMods(t *testing.T) {
	client := &fakeSSM{}
	runner, err := New(client, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	session := domain.Session{ID: "session-1", DisplayName: "Test", Vanilla: true, ConfiguredMission: domain.DefaultMissionSelection(), CurrentMission: domain.DefaultMissionSelection(), ActiveWorkflowID: "workflow-1", Infrastructure: domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"}}
	if _, err := runner.StartContent(context.Background(), session, domain.WorkshopTargetMods, true); err != nil {
		t.Fatal(err)
	}
	script := client.sent.Parameters["commands"][0]
	if !strings.HasPrefix(script, bashShebang+"export GSP_OPERATION_MODE=workshop_sync\n") {
		t.Fatal("content command placed operation variables before its Bash shebang")
	}
	if !strings.Contains(script, "export WORKSHOP_PROMOTE_MODS=true") {
		t.Fatal("promoting content command did not request mod promotion")
	}
	if _, err := runner.StartContent(context.Background(), session, domain.WorkshopTargetMission, false); err != nil {
		t.Fatal(err)
	}
	missionScript := client.sent.Parameters["commands"][0]
	if !strings.Contains(missionScript, "export WORKSHOP_SYNC_TARGET=mission\n") {
		t.Fatal("mission-only content command did not use the canonical mission target")
	}
	if !strings.Contains(missionScript, "export WORKSHOP_PROMOTE_MODS=false") {
		t.Fatal("mission-only content command did not disable mod promotion")
	}
	if strings.Index(missionScript, bashShebang) != 0 {
		t.Fatal("mission-only content command did not retain Bash as its interpreter")
	}
}

func TestStartBuildsSecretSafeResumableCommand(t *testing.T) {
	t.Parallel()
	client := &fakeSSM{}
	runner, err := New(client, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	session := domain.Session{ID: "session-1", GuildID: "guild-1", DisplayName: "Test", MissionObjectKey: "sessions/session-1/input/mission.pbo", PresetObjectKey: "sessions/session-1/input/preset.html", ServerConfigRevision: 2, ServerConfigObjectKey: "guilds/guild-1/server-config/revisions/000002-a/server.cfg", ServerConfigSHA256: strings.Repeat("a", 64), LifecycleState: domain.StateInstalling, Infrastructure: domain.Infrastructure{CapacitySlotID: "slot-0", InstanceID: "i-1", DataVolumeID: "vol-1"}}
	commandID, err := runner.Start(context.Background(), session)
	if err != nil || commandID != "command-1" {
		t.Fatalf("command = %q, err = %v", commandID, err)
	}
	script := client.sent.Parameters["commands"][0]
	for _, required := range []string{"aws s3 cp", "gsp-bootstrap", base64.StdEncoding.EncodeToString([]byte(session.MissionObjectKey)), base64.StdEncoding.EncodeToString([]byte(session.ServerConfigObjectKey)), base64.StdEncoding.EncodeToString([]byte(session.ServerConfigSHA256))} {
		if !strings.Contains(script, required) {
			t.Errorf("command missing %q", required)
		}
	}
	if len(script) > 4096 {
		t.Fatalf("SSM command is unexpectedly large: %d bytes", len(script))
	}
	if strings.Contains(script, "get-secret-value") {
		t.Fatal("bootstrap implementation should be delivered through the private S3 artifact")
	}
}

func TestCommandSupportsBuiltInDefaultMissionWithoutS3Object(t *testing.T) {
	t.Parallel()
	runner, err := New(&fakeSSM{}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	session := domain.Session{ID: "session-default", DisplayName: "Default", Vanilla: true, ConfiguredMission: domain.DefaultMissionSelection(), CurrentMission: domain.DefaultMissionSelection(), LifecycleState: domain.StateInstalling, Infrastructure: domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"}}
	script, err := runner.command(session)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(domain.DefaultArma3MissionTemplate))) {
		t.Fatal("command omitted built-in mission template")
	}
	if !strings.Contains(script, "MISSION_KEY_B64=''") {
		t.Fatal("built-in mission unexpectedly required an object key")
	}
}

func TestCommandSynchronizesEveryAcceptedActiveMissionWithoutChangingSelection(t *testing.T) {
	runner, err := New(&fakeSSM{}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	firstKey := "sessions/session-1/input/missions/" + strings.Repeat("a", 64) + "-First.Altis.pbo"
	secondKey := "sessions/session-1/input/missions/" + strings.Repeat("b", 64) + "-Second.Stratis.pbo"
	session := domain.Session{ID: "session-1", DisplayName: "Test", Vanilla: true, ConfiguredMission: domain.UploadedMissionSelection(firstKey), CurrentMission: domain.UploadedMissionSelection(firstKey), MissionFiles: []domain.MissionRecord{
		{ObjectKey: firstKey, Filename: "First.Altis.pbo", Status: domain.ArtifactAccepted},
		{ObjectKey: secondKey, Filename: "Second.Stratis.pbo", Status: domain.ArtifactAccepted},
		{ObjectKey: "rejected", Filename: "Rejected.pbo", Status: domain.ArtifactRejected},
	}, LifecycleState: domain.StateInstalling, Infrastructure: domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"}}
	script, err := runner.command(session)
	if err != nil {
		t.Fatal(err)
	}
	manifest := strings.Repeat("a", 64) + "\tFirst.Altis.pbo\t" + firstKey + "\n" + strings.Repeat("b", 64) + "\tSecond.Stratis.pbo\t" + secondKey + "\n"
	if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(manifest))) {
		t.Fatal("command omitted accepted mission manifest")
	}
	revision := contentDeploymentRevision(session, manifest, testConfig().BootstrapScriptKey)
	if !strings.Contains(script, "CONTENT_REVISION_B64='"+base64.StdEncoding.EncodeToString([]byte(revision))+"'") {
		t.Fatal("command omitted content deployment revision")
	}
	if strings.Contains(script, base64.StdEncoding.EncodeToString([]byte("rejected"))) {
		t.Fatal("command included rejected mission")
	}
	if session.CurrentMission.ObjectKey != firstKey {
		t.Fatal("manifest construction changed current mission")
	}
}

func TestContentDeploymentRevisionChangesWhenSleepingSessionMissionChanges(t *testing.T) {
	firstKey := "sessions/session-1/input/missions/" + strings.Repeat("a", 64) + "-First.Altis.pbo"
	secondKey := "sessions/session-1/input/missions/" + strings.Repeat("b", 64) + "-Second.Stratis.pbo"
	session := domain.Session{
		ID: "session-1", DisplayName: "Sleeping", LifecycleState: domain.StateSleeping,
		ConfiguredMission: domain.UploadedMissionSelection(firstKey), CurrentMission: domain.UploadedMissionSelection(firstKey),
		MissionFiles: []domain.MissionRecord{{ObjectKey: firstKey, Filename: "First.Altis.pbo", Status: domain.ArtifactAccepted}},
	}
	firstManifest, err := acceptedMissionManifest(session)
	if err != nil {
		t.Fatal(err)
	}
	firstRevision := contentDeploymentRevision(session, firstManifest, testConfig().BootstrapScriptKey)
	if replayRevision := contentDeploymentRevision(session, firstManifest, testConfig().BootstrapScriptKey); replayRevision != firstRevision {
		t.Fatalf("unchanged content revision = %q; want %q", replayRevision, firstRevision)
	}

	session.MissionFiles = append(session.MissionFiles, domain.MissionRecord{ObjectKey: secondKey, Filename: "Second.Stratis.pbo", Status: domain.ArtifactAccepted})
	secondManifest, err := acceptedMissionManifest(session)
	if err != nil {
		t.Fatal(err)
	}
	if changedRevision := contentDeploymentRevision(session, secondManifest, testConfig().BootstrapScriptKey); changedRevision == firstRevision {
		t.Fatal("accepted sleeping-session mission did not change content deployment revision")
	}
}

func TestContentDeploymentRevisionChangesWithServerConfigurationAndBootstrapRevision(t *testing.T) {
	session := domain.Session{DisplayName: "Session", ConfiguredMission: domain.DefaultMissionSelection(), CurrentMission: domain.DefaultMissionSelection()}
	baseline := contentDeploymentRevision(session, "", "platform/bootstrap/arma3-old.sh")

	configured := session
	configured.ServerConfigObjectKey = "guilds/guild/server-config/revisions/000001/config.cfg"
	configured.ServerConfigSHA256 = strings.Repeat("c", 64)
	configured.ServerConfigRevision = 1
	if revision := contentDeploymentRevision(configured, "", "platform/bootstrap/arma3-old.sh"); revision == baseline {
		t.Fatal("server configuration did not change content deployment revision")
	}
	if revision := contentDeploymentRevision(session, "", "platform/bootstrap/arma3-new.sh"); revision == baseline {
		t.Fatal("bootstrap artifact did not change content deployment revision")
	}
}

func TestBootstrapArtifactAvoidsAWKBuiltinNamesForMissionRewriteLocals(t *testing.T) {
	path := filepath.Clean(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	artifact := string(script)
	if strings.Contains(artifact, "function structural(value, output, index,") {
		t.Fatal("mission rewrite uses awk's built-in index function name as a local parameter")
	}
	if !strings.Contains(artifact, "function structural(value, output, position,") {
		t.Fatal("mission rewrite does not declare its portable position local")
	}
	if strings.Contains(artifact, "function braces(value, open, close)") {
		t.Fatal("mission rewrite uses awk's built-in close function name as a local parameter")
	}
	if !strings.Contains(artifact, "function braces(value, opening_count, closing_count)") {
		t.Fatal("mission rewrite does not declare portable brace-count locals")
	}
}

func TestCommandPassesCreatorDLCsThroughExistingModPath(t *testing.T) {
	t.Parallel()
	runner, err := New(&fakeSSM{}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	session := domain.Session{ID: "session-1", DisplayName: "Test", MissionObjectKey: "sessions/session-1/input/mission.pbo", PresetObjectKey: "sessions/session-1/input/preset.html", CreatorDLCs: []string{domain.CreatorDLCGlobalMobilization, domain.CreatorDLCReactionForces}, LifecycleState: domain.StateInstalling, Infrastructure: domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"}}
	script, err := runner.command(session)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte("gm;rf"))) {
		t.Fatal("command omitted canonical Creator DLC mod directories")
	}
}

func TestCommandAllowsCreatorDLCOnlyModdedContent(t *testing.T) {
	t.Parallel()
	runner, err := New(&fakeSSM{}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	session := domain.Session{ID: "session-1", DisplayName: "Test", MissionObjectKey: "sessions/session-1/input/mission.pbo", CreatorDLCs: []string{domain.CreatorDLCWesternSahara}, ConfigurationRevision: 3, LifecycleState: domain.StateInstalling, Infrastructure: domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"}}
	script, err := runner.command(session)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"ws", "3"} {
		if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(value))) {
			t.Fatalf("command omitted %q", value)
		}
	}
}

func TestCommandInstallsApplyingPendingRevisionWithoutChangingActivePointer(t *testing.T) {
	t.Parallel()
	runner, err := New(&fakeSSM{}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 23, 0, 0, 0, time.UTC)
	resolutionDigest := strings.Repeat("a", 64)
	session := domain.Session{ID: "session-1", DisplayName: "Test", MissionObjectKey: "sessions/session-1/input/mission.pbo", PresetObjectKey: "sessions/session-1/input/presets/v1.html", PresetRevisionSequence: 2, LifecycleState: domain.StateWaking, ActiveWorkflowID: "wake-1", ActiveWorkflowType: domain.WakeWorkflowType, Infrastructure: domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"}, ActivePresetRevision: domain.PresetRevision{Number: 1, PresetObjectKey: "sessions/session-1/input/presets/v1.html", Status: domain.PresetRevisionActive, StagedAt: now, ActivatedAt: now}, PendingPresetRevision: domain.PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/presets/v2.html", Status: domain.PresetRevisionApplying, StagedAt: now, ApplyWorkflowID: "wake-1", ApplyStartedAt: now, WorkshopResolutionSHA256: resolutionDigest, WorkshopSourceID: 42}}
	script, err := runner.command(session)
	if err != nil {
		t.Fatal(err)
	}
	pendingEncoded := base64.StdEncoding.EncodeToString([]byte(session.PendingPresetRevision.PresetObjectKey))
	activeEncoded := base64.StdEncoding.EncodeToString([]byte(session.PresetObjectKey))
	if !strings.Contains(script, pendingEncoded) || strings.Contains(script, activeEncoded) || !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte("2"))) || !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(resolutionDigest))) {
		t.Fatalf("command did not select pending revision: %s", script)
	}
	if session.PresetObjectKey != session.ActivePresetRevision.PresetObjectKey {
		t.Fatal("command selection mutated active compatibility pointer")
	}
}

func TestStartRollbackSelectsPriorActiveRevision(t *testing.T) {
	t.Parallel()
	client := &fakeSSM{}
	runner, err := New(client, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	activeKey := "sessions/session-1/input/presets/v1.html"
	pendingKey := "sessions/session-1/input/presets/v2.html"
	session := domain.Session{
		ID: "session-1", DisplayName: "Test", MissionObjectKey: "sessions/session-1/input/mission.pbo", PresetObjectKey: activeKey,
		LifecycleState: domain.StateWaking, ActiveWorkflowID: "wake-1", ActiveWorkflowType: domain.WakeWorkflowType,
		Infrastructure:       domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"},
		ActivePresetRevision: domain.PresetRevision{Number: 1, PresetObjectKey: activeKey, Status: domain.PresetRevisionActive, StagedAt: now, ActivatedAt: now},
		PendingPresetRevision: domain.PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: pendingKey, Status: domain.PresetRevisionApplying,
			StagedAt: now, ApplyWorkflowID: "wake-1", ApplyStartedAt: now},
	}
	commandID, err := runner.StartRollback(context.Background(), session)
	if err != nil || commandID != "command-1" {
		t.Fatalf("rollback command=%q err=%v", commandID, err)
	}
	script := client.sent.Parameters["commands"][0]
	if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(activeKey))) || strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(pendingKey))) {
		t.Fatalf("rollback did not select active revision: %s", script)
	}
	if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte("true"))) || !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte("1"))) {
		t.Fatalf("rollback mode/revision missing: %s", script)
	}
	if session.PresetObjectKey != activeKey || session.ActivePresetRevision.Number != 1 || session.PendingPresetRevision.Number != 2 {
		t.Fatal("rollback command construction mutated revision authority")
	}
}

func TestCommandSelectsApplyingServerPresetIndependently(t *testing.T) {
	t.Parallel()
	runner, err := New(&fakeSSM{}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	activeServer := "sessions/session-1/input/server-presets/v1.html"
	pendingServer := "sessions/session-1/input/server-presets/v2.html"
	session := domain.Session{ID: "session-1", DisplayName: "Test", MissionObjectKey: "sessions/session-1/input/mission.pbo", PresetObjectKey: "sessions/session-1/input/presets/v1.html", LifecycleState: domain.StateWaking, ActiveWorkflowID: "wake-1", Infrastructure: domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"}, ServerPresetObjectKey: activeServer,
		ActiveServerPresetRevision:  domain.PresetRevision{Number: 1, PresetObjectKey: activeServer, Status: domain.PresetRevisionActive, StagedAt: now, ActivatedAt: now},
		PendingServerPresetRevision: domain.PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: pendingServer, Status: domain.PresetRevisionApplying, StagedAt: now, ApplyWorkflowID: "wake-1", ApplyStartedAt: now}}
	script, err := runner.command(session)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(pendingServer))) || strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(activeServer))) {
		t.Fatalf("server preset selection missing from command: %s", script)
	}
}

func TestGeneratedCommandPassesBashSyntaxCheck(t *testing.T) {
	runner, err := New(&fakeSSM{}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	script, err := runner.command(domain.Session{ID: "session-1", DisplayName: "Test", MissionObjectKey: "sessions/session-1/input/mission.pbo", PresetObjectKey: "sessions/session-1/input/preset.html", LifecycleState: domain.StateInstalling, Infrastructure: domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"}})
	if err != nil {
		t.Fatal(err)
	}
	assertBashSyntax(t, []byte(script))
}

func TestVanillaCommandUsesSteamAuthorizationWithoutPreset(t *testing.T) {
	t.Parallel()
	runner, err := New(&fakeSSM{}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	script, err := runner.command(domain.Session{
		ID: "session-vanilla", DisplayName: "Vanilla", Vanilla: true,
		MissionObjectKey: "sessions/session-vanilla/input/mission.pbo",
		LifecycleState:   domain.StateInstalling,
		Infrastructure:   domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "export VANILLA_MODE=true") {
		t.Fatal("vanilla command did not enable vanilla content mode")
	}
	if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(testConfig().SteamAuthSecretID))) {
		t.Fatal("vanilla command omitted the Steam authorization secret identifier")
	}
	if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(testConfig().MetadataTableName))) {
		t.Fatal("vanilla command omitted the Steam authorization-state table")
	}
	assertBashSyntax(t, []byte(script))
}

func TestBootstrapArtifactPassesBashSyntaxCheck(t *testing.T) {
	path := filepath.Clean(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"get-secret-value", "put-secret-value", "AWSCURRENT", "source_version_id", "config_sha256", "STEAM_AUTH#CACHE", "lease_expires_at < :now", "refresh_steam_auth_lock", "start_steam_auth_lock_heartbeat", "STEAM_AUTH_LOCK_LEASE_SECONDS=900", "STEAM_AUTH_LOCK_HEARTBEAT_SECONDS=300", "REAUTH_REQUIRED", "ERR_STEAM_REAUTH_REQUIRED", "login \"%s\"", "VANILLA_MODE", "PRESET_REVISION", "SERVER_PRESET_REVISION", "PRESET_ROLLBACK", "MOD_CONFIG_REVISION", "CONTENT_REVISION", "SERVER_CONFIG_KEY", "SERVER_CONFIG_SHA256", "server.cfg.pending", "sha256sum --check --status", "[ \"$PRESET_ROLLBACK\" = true ] && rm -f -- \"$marker\"", "$stage.missions-$WORKSHOP_MISSION_REVISION.client-$PRESET_REVISION.server-$SERVER_PRESET_REVISION.config-$MOD_CONFIG_REVISION.complete", "$stage.revision-$CONTENT_REVISION.complete", "rm -f -- \"$STATE_DIR/deploy_content.complete\" \"$STATE_DIR\"/deploy_content.revision-*.complete", "mod-revisions/revision-", "server-mod-revisions/revision-", "-serverMod=$server_mods", "active-preset-revision", "app_update 233780 validate", "bootstrap.lock", "for stage in install_steamcmd install_arma sync_workshop_content", "scrub_persistent_steam_auth", "trap steam_auth_exit EXIT", "trap 'exit 143' TERM", "STEAM_AUTH_ROOT", "safe_mission_template=\"$(sqf_escape \"$mission_template\")\"", "template = \"$safe_mission_template\";", "GSP_CHECKPOINT:%s", "checkpoint HOST_PREPARED", "checkpoint GAME_SERVER_INSTALLED", "checkpoint MODS_APPLIED", "checkpoint CONFIGURATION_READY", "checkpoint SERVICE_STARTED", "checkpoint HEALTH_VERIFICATION", "launch_and_verify", "systemctl restart arma3-server.service", "awk '{print $4}' | grep -Eq '(^|:)2302$'", "awk '{print $4}' | grep -Eq '(^|:)9987$'"} {
		if !strings.Contains(string(script), required) {
			t.Errorf("script missing %q", required)
		}
	}
	for _, forbidden := range []string{".password", "STEAM_SECRET_ID", "steam_guard_code", "login anonymous", "login \"%s\" \"%s\"", "now_epoch + 25200"} {
		if strings.Contains(string(script), forbidden) {
			t.Errorf("script contains legacy credential behavior %q", forbidden)
		}
	}
	if !strings.Contains(string(script), `^[0-9a-f]{64}-(.+\.[pP][bB][oO])$`) {
		t.Error("script does not normalize legacy digest-prefixed mission filenames")
	}
	assertBashSyntax(t, script)
}

func TestBootstrapArtifactCreatesWorkshopDirectoryForCreatorDLCOnlySessions(t *testing.T) {
	path := filepath.Clean(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	create := `mkdir -p "$ROOT/home/Steam/steamapps/workshop"`
	ownership := `chown -R steam:steam "$ROOT/config" "$ROOT/home/Steam/steamapps/workshop" "$ROOT/arma3"`
	createAt, ownershipAt := strings.Index(text, create), strings.Index(text, ownership)
	if createAt < 0 || ownershipAt < 0 || createAt > ownershipAt {
		t.Fatalf("bootstrap must create the optional Workshop path before applying ownership: create=%d chown=%d", createAt, ownershipAt)
	}
}

func TestOperatorEnrollmentScriptIsLocalMFAAndCacheOnly(t *testing.T) {
	path := filepath.Clean(filepath.Join("..", "..", "..", "..", "scripts", "steam-auth-cache.ps1"))
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{"steam-auth-enrollment", "900-second", "put-secret-value", "update-secret-version-stage", "AWSCURRENT", "AWSPREVIOUS", "STEAM_AUTH#CACHE", "REAUTH_REQUIRED", "ConfigVdfPath", "[Array]::Clear", "finally"} {
		if !strings.Contains(text, required) {
			t.Errorf("enrollment script missing %q", required)
		}
	}
	for _, forbidden := range []string{"SteamPassword", "GuardCode", "steam_guard_code", "SendCommand", "discord"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("enrollment script contains forbidden channel/credential field %q", forbidden)
		}
	}
}

func TestObserveMapsSteamGuardChallengeToStableReauthorizationFailure(t *testing.T) {
	t.Parallel()
	client := &fakeSSM{invocation: &ssm.GetCommandInvocationOutput{Status: types.CommandInvocationStatusFailed, StandardErrorContent: aws.String("internal noise\nERR_STEAM_REAUTH_REQUIRED: do not expose account data")}}
	runner, _ := New(client, testConfig())
	status, err := runner.Observe(context.Background(), "i-1", "command-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.ErrorCode != "ERR_STEAM_REAUTH_REQUIRED" || status.ErrorMessage != "Steam authorization requires operator re-enrollment." || strings.Contains(status.ErrorMessage, "internal noise") {
		t.Fatalf("reauthorization status = %#v", status)
	}
}

func TestObserveMapsWorkshopScenarioFailuresToActionableCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		stderr, code, message string
	}{
		{"package noise\nERR_WORKSHOP_SCENARIO_RESUBMIT: private detail", "ERR_WORKSHOP_SCENARIO_RESUBMIT", "The Workshop scenario changed after metadata resolution."},
		{"package noise\nERR_WORKSHOP_SCENARIO_PAYLOAD: private detail", "ERR_WORKSHOP_SCENARIO_PAYLOAD", "The Workshop scenario download did not contain one safe deployable mission payload."},
		{"package noise\nERR_WORKSHOP_DISK_SPACE: private detail", "ERR_WORKSHOP_DISK_SPACE", "The managed host does not have enough free disk space to stage the Workshop content."},
		{"package noise\nERR_WORKSHOP_RESULT_PUBLISH: private detail", "ERR_WORKSHOP_RESULT_PUBLISH", "The platform could not publish the Workshop synchronization result."},
	}
	for _, test := range tests {
		client := &fakeSSM{invocation: &ssm.GetCommandInvocationOutput{Status: types.CommandInvocationStatusFailed, StandardErrorContent: aws.String(test.stderr)}}
		runner, _ := New(client, testConfig())
		status, err := runner.Observe(context.Background(), "i-1", "command-1")
		if err != nil || status.ErrorCode != test.code || status.ErrorMessage != test.message || strings.Contains(status.ErrorMessage, "private detail") {
			t.Fatalf("status = %#v, err = %v", status, err)
		}
	}
}

func assertBashSyntax(t *testing.T, script []byte) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil && runtime.GOOS == "windows" {
		candidate := `C:\Program Files\Git\bin\bash.exe`
		if _, statErr := os.Stat(candidate); statErr == nil {
			bash = candidate
			err = nil
		}
	}
	if err != nil {
		t.Skip("bash is unavailable")
	}
	path := filepath.Join(t.TempDir(), "bootstrap.sh")
	if err := os.WriteFile(path, script, 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(bash, "-n", filepath.ToSlash(path)).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, output)
	}
}

func TestObserveReturnsBoundedError(t *testing.T) {
	t.Parallel()
	client := &fakeSSM{invocation: &ssm.GetCommandInvocationOutput{Status: types.CommandInvocationStatusFailed, StandardErrorContent: aws.String(strings.Repeat("x", 600))}}
	runner, _ := New(client, testConfig())
	status, err := runner.Observe(context.Background(), "i-1", "command-1")
	if err != nil || status.Status != "Failed" || len(status.ErrorMessage) != domain.MaximumFailureDetailRunes {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

func TestObserveReturnsOnlyOrderedAllowlistedCheckpoints(t *testing.T) {
	t.Parallel()
	output := strings.Join([]string{
		"Steam password=not-a-progress-fact",
		"GSP_CHECKPOINT:MODS_APPLIED",
		"GSP_CHECKPOINT:HOST_PREPARED",
		"GSP_CHECKPOINT:NOT_A_REAL_STAGE",
		"GSP_CHECKPOINT:MODS_APPLIED",
		"GSP_CHECKPOINT:GAME_SERVER_INSTALLED",
	}, "\n")
	client := &fakeSSM{invocation: &ssm.GetCommandInvocationOutput{
		Status: types.CommandInvocationStatusInProgress, StandardOutputContent: aws.String(output),
	}}
	runner, _ := New(client, testConfig())
	status, err := runner.Observe(context.Background(), "i-1", "command-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.ProgressMilestone{domain.ProgressHostPrepared, domain.ProgressGameServerInstalled, domain.ProgressModsApplied}
	if !reflect.DeepEqual(status.Checkpoints, want) || strings.Contains(fmt.Sprintf("%#v", status), "password") {
		t.Fatalf("sanitized status = %#v; want checkpoints %#v", status, want)
	}
}

func TestObserveReturnsOnlyLatestAllowlistedActivity(t *testing.T) {
	t.Parallel()
	output := strings.Join([]string{
		"GSP_ACTIVITY:ARMA_SERVER",
		"GSP_ACTIVITY:WORKSHOP_ITEMS:12",
		"GSP_ACTIVITY:WORKSHOP_ITEM:1234",
		"GSP_ACTIVITY:WORKSHOP_ITEMS:12 password=secret",
	}, "\n")
	client := &fakeSSM{invocation: &ssm.GetCommandInvocationOutput{
		Status: types.CommandInvocationStatusInProgress, StandardOutputContent: aws.String(output),
	}}
	runner, _ := New(client, testConfig())
	status, err := runner.Observe(context.Background(), "i-1", "command-1")
	if err != nil || status.Activity != "Workshop content (12 items)" || strings.Contains(fmt.Sprintf("%#v", status), "secret") {
		t.Fatalf("sanitized activity = %#v, err = %v", status, err)
	}
}

func TestObserveClearsActivityAfterNewerCheckpoint(t *testing.T) {
	t.Parallel()
	output := strings.Join([]string{
		"GSP_CHECKPOINT:GAME_SERVER_INSTALLED",
		"GSP_ACTIVITY:ARMA_SERVER",
		"GSP_CHECKPOINT:MODS_APPLIED",
	}, "\n")
	client := &fakeSSM{invocation: &ssm.GetCommandInvocationOutput{
		Status: types.CommandInvocationStatusInProgress, StandardOutputContent: aws.String(output),
	}}
	runner, _ := New(client, testConfig())
	status, err := runner.Observe(context.Background(), "i-1", "command-1")
	if err != nil || status.Activity != "" {
		t.Fatalf("stale activity = %#v, err = %v", status, err)
	}
}

func testConfig() Config {
	return Config{Region: "us-west-2", AssetsBucket: "assets", BootstrapScriptKey: "platform/bootstrap/arma3.sh", MetadataTableName: "metadata", SteamAuthSecretID: "/steam-auth", TeamSpeakVersion: "3.13.8", TimeoutSeconds: 21600}
}
