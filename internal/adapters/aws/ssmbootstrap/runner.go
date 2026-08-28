package ssmbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const (
	documentName                = "AWS-RunShellScript"
	RuntimeConfigurationVersion = "steam-auth-cache-v1"
)

type API interface {
	SendCommand(context.Context, *ssm.SendCommandInput, ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}

type progressAPI interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type Config struct {
	Region             string
	AssetsBucket       string
	BootstrapScriptKey string
	MetadataTableName  string
	SteamAuthSecretID  string
	TeamSpeakVersion   string
	TimeoutSeconds     int32
}

type Runner struct {
	client   API
	progress progressAPI
	config   Config
}

var _ ports.BootstrapRunner = (*Runner)(nil)
var _ ports.PresetRevisionRunner = (*Runner)(nil)

func New(client API, config Config) (*Runner, error) {
	config.Region = strings.TrimSpace(config.Region)
	config.AssetsBucket = strings.TrimSpace(config.AssetsBucket)
	config.BootstrapScriptKey = strings.TrimSpace(config.BootstrapScriptKey)
	config.MetadataTableName = strings.TrimSpace(config.MetadataTableName)
	config.SteamAuthSecretID = strings.TrimSpace(config.SteamAuthSecretID)
	config.TeamSpeakVersion = strings.TrimSpace(config.TeamSpeakVersion)
	required := []struct {
		missing bool
		name    string
	}{
		{client == nil, "SSM client"},
		{config.Region == "", "AWS_REGION"},
		{config.AssetsBucket == "", "SESSION_ASSETS_BUCKET"},
		{config.BootstrapScriptKey == "", "BOOTSTRAP_SCRIPT_KEY"},
		{config.MetadataTableName == "", "METADATA_TABLE_NAME"},
		{config.SteamAuthSecretID == "", "STEAM_AUTH_SECRET_ID"},
		{config.TeamSpeakVersion == "", "TEAMSPEAK_VERSION"},
	}
	for _, value := range required {
		if value.missing {
			return nil, fmt.Errorf("bootstrap configuration is missing %s", value.name)
		}
	}
	if config.TimeoutSeconds < 900 || config.TimeoutSeconds > 172800 {
		return nil, fmt.Errorf("bootstrap timeout must be between 900 and 172800 seconds")
	}
	return &Runner{client: client, config: config}, nil
}

// WithProgressStore enables live progress snapshots from the existing session
// assets bucket. SSM command stdout remains the terminal/fallback source.
func (runner *Runner) WithProgressStore(client progressAPI) *Runner {
	runner.progress = client
	return runner
}

func (runner *Runner) Start(ctx context.Context, session domain.Session) (string, error) {
	applyingLifecycleRevision := session.HasApplyingPresetRevision(session.ActiveWorkflowID) && (session.LifecycleState == domain.StateWaking || session.LifecycleState == domain.StateRestoring)
	if !session.CanStartBootstrap() && session.LifecycleState != domain.StateInstalling && !applyingLifecycleRevision {
		return "", fmt.Errorf("%w: session is not bootstrap-ready", domain.ErrInvalidTransition)
	}
	return runner.start(ctx, session, false)
}

func (runner *Runner) StartRollback(ctx context.Context, session domain.Session) (string, error) {
	if !session.HasApplyingPresetRevision(session.ActiveWorkflowID) {
		return "", fmt.Errorf("%w: no applying preset revision to roll back", domain.ErrInvalidTransition)
	}
	return runner.start(ctx, session, true)
}

func (runner *Runner) start(ctx context.Context, session domain.Session, rollback bool) (string, error) {
	script, err := runner.commandMode(session, rollback)
	if err != nil {
		return "", err
	}
	output, err := runner.client.SendCommand(ctx, &ssm.SendCommandInput{
		DocumentName: aws.String(documentName),
		InstanceIds:  []string{session.Infrastructure.InstanceID},
		Comment:      aws.String("game-server-platform bootstrap " + session.ID),
		Parameters: map[string][]string{
			"commands":         {script},
			"executionTimeout": {fmt.Sprintf("%d", runner.config.TimeoutSeconds)},
		},
		TimeoutSeconds:     aws.Int32(60),
		OutputS3BucketName: aws.String(runner.config.AssetsBucket),
		OutputS3KeyPrefix:  aws.String("sessions/" + session.ID + "/logs/bootstrap"),
		OutputS3Region:     aws.String(runner.config.Region),
	})
	if err != nil {
		return "", fmt.Errorf("send Systems Manager bootstrap command: %w", err)
	}
	if output.Command == nil || strings.TrimSpace(aws.ToString(output.Command.CommandId)) == "" {
		return "", fmt.Errorf("Systems Manager returned no command ID")
	}
	return aws.ToString(output.Command.CommandId), nil
}

func (runner *Runner) Observe(ctx context.Context, instanceID string, commandID string) (ports.BootstrapCommandStatus, error) {
	instanceID, commandID = strings.TrimSpace(instanceID), strings.TrimSpace(commandID)
	if instanceID == "" || commandID == "" {
		return ports.BootstrapCommandStatus{}, fmt.Errorf("instance and command IDs are required")
	}
	output, err := runner.client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
		CommandId: aws.String(commandID), InstanceId: aws.String(instanceID),
	})
	if err != nil {
		var pending *types.InvocationDoesNotExist
		if errors.As(err, &pending) {
			return ports.BootstrapCommandStatus{Status: "Pending"}, nil
		}
		return ports.BootstrapCommandStatus{}, err
	}
	code, message := bootstrapFailure(aws.ToString(output.StandardErrorContent))
	return ports.BootstrapCommandStatus{
		Status: string(output.Status), ErrorCode: code, ErrorMessage: message,
		Activity:    parseActivity(aws.ToString(output.StandardOutputContent)),
		Checkpoints: parseCheckpoints(aws.ToString(output.StandardOutputContent)),
	}, nil
}

// ObserveProgress reads the workflow-scoped snapshot written by the running
// host command, avoiding SSM's buffered in-progress StandardOutputContent.
func (runner *Runner) ObserveProgress(ctx context.Context, instanceID, commandID, sessionID, workflowID string) (ports.BootstrapCommandStatus, error) {
	status, err := runner.Observe(ctx, instanceID, commandID)
	if err != nil || runner.progress == nil || strings.TrimSpace(workflowID) == "" {
		return status, err
	}
	output, getErr := runner.progress.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(runner.config.AssetsBucket),
		Key:    aws.String("sessions/" + sessionID + "/runtime/bootstrap-progress-" + workflowID + ".txt"),
	})
	if getErr != nil {
		return status, nil
	}
	defer output.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(output.Body, 16*1024))
	if readErr != nil {
		return status, nil
	}
	status.Activity = parseActivity(string(body))
	status.Checkpoints = parseCheckpoints(string(body))
	return status, nil
}

func parseActivity(output string) string {
	activity := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "GSP_CHECKPOINT:") {
			activity = ""
			continue
		}
		const prefix = "GSP_ACTIVITY:"
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		switch {
		case value == "ARMA_SERVER":
			activity = "Arma 3 server files"
		case strings.HasPrefix(value, "WORKSHOP_ITEMS:"):
			count := strings.TrimPrefix(value, "WORKSHOP_ITEMS:")
			parsed, err := strconv.Atoi(count)
			if err == nil && parsed >= 1 && parsed <= 9999 && strconv.Itoa(parsed) == count {
				unit := "items"
				if parsed == 1 {
					unit = "item"
				}
				activity = fmt.Sprintf("Workshop content (%d %s)", parsed, unit)
			}
		}
	}
	return activity
}

func bootstrapFailure(stderr string) (string, string) {
	if strings.Contains(stderr, "ERR_STEAM_REAUTH_REQUIRED") {
		return "ERR_STEAM_REAUTH_REQUIRED", "Steam authorization requires operator re-enrollment."
	}
	if strings.Contains(stderr, "ERR_WORKSHOP_SCENARIO_RESUBMIT") {
		return "ERR_WORKSHOP_SCENARIO_RESUBMIT", "The Workshop scenario changed after metadata resolution."
	}
	if strings.Contains(stderr, "ERR_WORKSHOP_SCENARIO_PAYLOAD") {
		return "ERR_WORKSHOP_SCENARIO_PAYLOAD", "The Workshop scenario download did not contain one safe deployable mission payload."
	}
	return "", domain.SanitizeDiagnostic(stderr)
}

func parseCheckpoints(output string) []domain.ProgressMilestone {
	allowed := map[domain.ProgressMilestone]bool{
		domain.ProgressHostPrepared: true, domain.ProgressGameServerInstalled: true,
		domain.ProgressModsApplied: true, domain.ProgressConfigurationReady: true,
		domain.ProgressServiceStarted: true, domain.ProgressHealthVerification: true,
	}
	seen := make(map[domain.ProgressMilestone]bool, len(allowed))
	for _, line := range strings.Split(output, "\n") {
		const prefix = "GSP_CHECKPOINT:"
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		checkpoint := domain.ProgressMilestone(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
		if allowed[checkpoint] && !seen[checkpoint] {
			seen[checkpoint] = true
		}
	}
	ordered, _ := domain.MilestonesForWorkflow(domain.BootstrapWorkflowType)
	checkpoints := make([]domain.ProgressMilestone, 0, len(seen))
	for _, checkpoint := range ordered {
		if seen[checkpoint] {
			checkpoints = append(checkpoints, checkpoint)
		}
	}
	return checkpoints
}

func (runner *Runner) command(session domain.Session) (string, error) {
	return runner.commandMode(session, false)
}

func (runner *Runner) commandMode(session domain.Session, rollback bool) (string, error) {
	creatorDLCFolders, err := domain.CreatorDLCModFolders(session.CreatorDLCs)
	if err != nil {
		return "", fmt.Errorf("creator DLC selection: %w", err)
	}
	presetObjectKey := session.PresetObjectKeyForApplication()
	presetRevision := session.PresetRevisionForApplication()
	serverPresetObjectKey := session.ServerPresetObjectKeyForApplication()
	serverPresetRevision := session.ServerPresetRevisionForApplication()
	mission := session.MissionForApplication()
	missionManifest, err := acceptedMissionManifest(session)
	if err != nil {
		return "", err
	}
	workshopMissionManifest, workshopMissionRevision, err := resolvedWorkshopMissionManifest(session)
	if err != nil {
		return "", err
	}
	if rollback {
		active := session.EffectiveActivePresetRevision()
		presetObjectKey, presetRevision = active.PresetObjectKey, active.Number
		serverActive := session.EffectiveActiveServerPresetRevision()
		serverPresetObjectKey, serverPresetRevision = serverActive.PresetObjectKey, serverActive.Number
	}
	if session.Infrastructure.InstanceID == "" || session.Infrastructure.DataVolumeID == "" || mission.Template == "" || (!session.Vanilla && presetObjectKey == "" && serverPresetObjectKey == "" && len(creatorDLCFolders) == 0) {
		return "", fmt.Errorf("instance, data volume, mission selection, and modded content are required")
	}
	values := map[string]string{
		"SESSION_ID_B64":                session.ID,
		"WORKFLOW_ID_B64":               session.ActiveWorkflowID,
		"DISPLAY_NAME_B64":              session.DisplayName,
		"DATA_VOLUME_ID_B64":            session.Infrastructure.DataVolumeID,
		"MISSION_KEY_B64":               mission.ObjectKey,
		"MISSION_TEMPLATE_B64":          mission.Template,
		"MISSION_MANIFEST_B64":          missionManifest,
		"CONTENT_REVISION_B64":          contentDeploymentRevision(session, missionManifest, runner.config.BootstrapScriptKey),
		"WORKSHOP_MISSION_MANIFEST_B64": workshopMissionManifest,
		"WORKSHOP_MISSION_REVISION_B64": workshopMissionRevision,
		"SERVER_CONFIG_KEY_B64":         session.ServerConfigObjectKey,
		"SERVER_CONFIG_SHA_B64":         session.ServerConfigSHA256,
		"SERVER_CONFIG_REV_B64":         fmt.Sprintf("%d", session.ServerConfigRevision),
		"PRESET_KEY_B64":                presetObjectKey,
		"PRESET_REVISION_B64":           fmt.Sprintf("%d", presetRevision),
		"PRESET_ROLLBACK_B64":           fmt.Sprintf("%t", rollback),
		"SERVER_PRESET_KEY_B64":         serverPresetObjectKey,
		"SERVER_PRESET_REVISION_B64":    fmt.Sprintf("%d", serverPresetRevision),
		"CREATOR_DLC_MODS_B64":          strings.Join(creatorDLCFolders, ";"),
		"MOD_CONFIG_REVISION_B64":       fmt.Sprintf("%d", session.ConfigurationRevision),
		"ASSETS_BUCKET_B64":             runner.config.AssetsBucket,
		"METADATA_TABLE_B64":            runner.config.MetadataTableName,
		"STEAM_AUTH_SECRET_B64":         runner.config.SteamAuthSecretID,
		"AWS_REGION_B64":                runner.config.Region,
		"TEAMSPEAK_VERSION_B64":         runner.config.TeamSpeakVersion,
	}
	var command strings.Builder
	command.WriteString("#!/usr/bin/env bash\nset -Eeuo pipefail\numask 077\n")
	for _, key := range []string{"SESSION_ID_B64", "WORKFLOW_ID_B64", "DISPLAY_NAME_B64", "DATA_VOLUME_ID_B64", "MISSION_KEY_B64", "MISSION_TEMPLATE_B64", "MISSION_MANIFEST_B64", "CONTENT_REVISION_B64", "WORKSHOP_MISSION_MANIFEST_B64", "WORKSHOP_MISSION_REVISION_B64", "SERVER_CONFIG_KEY_B64", "SERVER_CONFIG_SHA_B64", "SERVER_CONFIG_REV_B64", "PRESET_KEY_B64", "PRESET_REVISION_B64", "PRESET_ROLLBACK_B64", "SERVER_PRESET_KEY_B64", "SERVER_PRESET_REVISION_B64", "CREATOR_DLC_MODS_B64", "MOD_CONFIG_REVISION_B64", "ASSETS_BUCKET_B64", "METADATA_TABLE_B64", "STEAM_AUTH_SECRET_B64", "AWS_REGION_B64", "TEAMSPEAK_VERSION_B64"} {
		command.WriteString("export " + key + "='" + base64.StdEncoding.EncodeToString([]byte(values[key])) + "'\n")
	}
	if session.TeamSpeakEnabled {
		command.WriteString("export TEAMSPEAK_ENABLED=true\n")
	} else {
		command.WriteString("export TEAMSPEAK_ENABLED=false\n")
	}
	if session.Vanilla {
		command.WriteString("export VANILLA_MODE=true\n")
	} else {
		command.WriteString("export VANILLA_MODE=false\n")
	}
	command.WriteString("bootstrap_script=\"$(mktemp /run/gsp-bootstrap.XXXXXX)\"\n")
	command.WriteString("aws_cli_tmp=''\n")
	command.WriteString("trap 'rm -f \"$bootstrap_script\"; [ -z \"$aws_cli_tmp\" ] || rm -rf -- \"$aws_cli_tmp\"' EXIT\n")
	command.WriteString("if ! command -v aws >/dev/null 2>&1; then\n")
	command.WriteString("  command -v apt-get >/dev/null 2>&1 || { echo 'AWS CLI bootstrap requires apt-get' >&2; exit 1; }\n")
	command.WriteString("  apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl unzip\n")
	command.WriteString("  aws_cli_tmp=\"$(mktemp -d /run/gsp-awscli.XXXXXX)\"\n")
	command.WriteString("  curl --fail --location --silent --show-error 'https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip' -o \"$aws_cli_tmp/awscliv2.zip\"\n")
	command.WriteString("  unzip -q \"$aws_cli_tmp/awscliv2.zip\" -d \"$aws_cli_tmp\"\n")
	command.WriteString("  \"$aws_cli_tmp/aws/install\" --bin-dir /usr/local/bin --install-dir /usr/local/aws-cli\n")
	command.WriteString("fi\n")
	command.WriteString("download_bucket=\"$(printf '%s' '" + base64.StdEncoding.EncodeToString([]byte(runner.config.AssetsBucket)) + "' | base64 -d)\"\n")
	command.WriteString("download_key=\"$(printf '%s' '" + base64.StdEncoding.EncodeToString([]byte(runner.config.BootstrapScriptKey)) + "' | base64 -d)\"\n")
	command.WriteString("download_region=\"$(printf '%s' '" + base64.StdEncoding.EncodeToString([]byte(runner.config.Region)) + "' | base64 -d)\"\n")
	command.WriteString("aws s3 cp \"s3://$download_bucket/$download_key\" \"$bootstrap_script\" --region \"$download_region\" --only-show-errors\n")
	command.WriteString("chmod 700 \"$bootstrap_script\"\n")
	command.WriteString("\"$bootstrap_script\"\n")
	return command.String(), nil
}

func contentDeploymentRevision(session domain.Session, missionManifest, bootstrapScriptKey string) string {
	mission := session.MissionForApplication()
	digest := sha256.New()
	for _, value := range []string{
		bootstrapScriptKey,
		session.DisplayName,
		mission.Template,
		mission.ObjectKey,
		missionManifest,
		session.ServerConfigObjectKey,
		session.ServerConfigSHA256,
		strconv.FormatInt(session.ServerConfigRevision, 10),
	} {
		fmt.Fprintf(digest, "%d:", len(value))
		_, _ = io.WriteString(digest, value)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func acceptedMissionManifest(session domain.Session) (string, error) {
	var manifest strings.Builder
	for _, mission := range session.AcceptedMissionFiles() {
		base := path.Base(mission.ObjectKey)
		expectedPrefix := path.Join("sessions", session.ID, "input", "missions") + "/"
		separator := strings.IndexByte(base, '-')
		if !strings.HasPrefix(mission.ObjectKey, expectedPrefix) || separator != 64 || len(base) <= 65 || base[65:] != mission.Filename {
			// Legacy single-mission records predate content-addressed keys and
			// continue through MISSION_KEY until replaced by a current upload.
			if mission.ObjectKey == session.MissionForApplication().ObjectKey {
				continue
			}
			return "", fmt.Errorf("accepted mission object key is malformed")
		}
		checksum := base[:64]
		if _, err := hex.DecodeString(checksum); err != nil {
			return "", fmt.Errorf("accepted mission checksum is malformed")
		}
		manifest.WriteString(checksum + "\t" + mission.Filename + "\t" + mission.ObjectKey + "\n")
	}
	return manifest.String(), nil
}

func resolvedWorkshopMissionManifest(session domain.Session) (string, string, error) {
	type snapshot struct {
		id       uint64
		digest   string
		filename string
		fileSize int64
	}
	snapshots := make([]snapshot, 0, len(session.WorkshopMissionSources))
	for _, source := range session.WorkshopMissionSources {
		if err := source.Validate(); err != nil {
			return "", "", fmt.Errorf("Workshop mission source: %w", err)
		}
		if len(source.AcceptedItems) == 0 {
			return "", "", fmt.Errorf("Workshop mission source must be resubmitted to capture its canonical filename")
		}
		for _, item := range source.AcceptedItems {
			snapshots = append(snapshots, snapshot{id: item.PublishedFileID, digest: source.ResolutionSHA256, filename: item.Filename, fileSize: item.FileSize})
		}
	}
	slices.SortFunc(snapshots, func(a, b snapshot) int {
		if a.id < b.id {
			return -1
		}
		if a.id > b.id {
			return 1
		}
		if compared := strings.Compare(a.digest, b.digest); compared != 0 {
			return compared
		}
		return strings.Compare(a.filename, b.filename)
	})
	revision, err := session.WorkshopMissionRevision()
	if err != nil {
		return "", "", err
	}
	items := make([]snapshot, 0, len(snapshots))
	for _, item := range snapshots {
		if len(items) > 0 && items[len(items)-1].id == item.id {
			prior := items[len(items)-1]
			if prior.filename != item.filename || prior.fileSize != item.fileSize {
				return "", "", fmt.Errorf("Workshop scenario %d has conflicting immutable metadata; resubmit its sources", item.id)
			}
			continue
		}
		items = append(items, item)
	}
	var manifest strings.Builder
	for _, item := range items {
		fmt.Fprintf(&manifest, "%d\t%s\t%s\t%d\n", item.id, revision, item.filename, item.fileSize)
	}
	if len(items) > domain.MaximumWorkshopMissionItems {
		return "", "", fmt.Errorf("Workshop mission item limit exceeded")
	}
	return manifest.String(), revision, nil
}
