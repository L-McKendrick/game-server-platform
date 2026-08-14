package ssmbootstrap

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type fakeSSM struct {
	sent       *ssm.SendCommandInput
	invocation *ssm.GetCommandInvocationOutput
}

func (fake *fakeSSM) SendCommand(_ context.Context, input *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	fake.sent = input
	return &ssm.SendCommandOutput{Command: &types.Command{CommandId: aws.String("command-1")}}, nil
}
func (fake *fakeSSM) GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return fake.invocation, nil
}

func TestStartBuildsSecretSafeResumableCommand(t *testing.T) {
	t.Parallel()
	client := &fakeSSM{}
	runner, err := New(client, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	session := domain.Session{ID: "session-1", DisplayName: "Test", MissionObjectKey: "sessions/session-1/input/mission.pbo", PresetObjectKey: "sessions/session-1/input/preset.html", LifecycleState: domain.StateInstalling, Infrastructure: domain.Infrastructure{CapacitySlotID: "slot-0", InstanceID: "i-1", DataVolumeID: "vol-1"}}
	commandID, err := runner.Start(context.Background(), session)
	if err != nil || commandID != "command-1" {
		t.Fatalf("command = %q, err = %v", commandID, err)
	}
	script := client.sent.Parameters["commands"][0]
	for _, required := range []string{"aws s3 cp", "gsp-bootstrap", base64.StdEncoding.EncodeToString([]byte(session.MissionObjectKey))} {
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

func TestVanillaCommandUsesAnonymousModeWithoutPresetOrSecretIdentifier(t *testing.T) {
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
		t.Fatal("vanilla command did not enable anonymous bootstrap mode")
	}
	if strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(testConfig().SteamSecretID))) {
		t.Fatal("vanilla command included the Steam secret identifier")
	}
	assertBashSyntax(t, []byte(script))
}

func TestBootstrapArtifactPassesBashSyntaxCheck(t *testing.T) {
	path := filepath.Clean(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"get-secret-value", "login anonymous", "VANILLA_MODE", "app_update 233780 validate", "bootstrap.lock", "for stage in install_steamcmd install_arma", "launch_and_verify", "systemctl restart arma3-server.service", "awk '{print $4}' | grep -Eq '(^|:)2302$'", "awk '{print $4}' | grep -Eq '(^|:)9987$'"} {
		if !strings.Contains(string(script), required) {
			t.Errorf("script missing %q", required)
		}
	}
	assertBashSyntax(t, script)
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
	if err != nil || status.Status != "Failed" || len(status.ErrorMessage) != 500 {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

func testConfig() Config {
	return Config{Region: "us-west-2", AssetsBucket: "assets", BootstrapScriptKey: "platform/bootstrap/arma3.sh", SteamSecretID: "/steam", TeamSpeakVersion: "3.13.8", TimeoutSeconds: 21600}
}
