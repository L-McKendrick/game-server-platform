package aws_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerraformDeniesInsecureS3Transport(t *testing.T) {
	t.Parallel()
	for _, relative := range []string{
		filepath.Join("..", "..", "..", "infra", "terraform", "bootstrap", "main.tf"),
		filepath.Join("..", "..", "..", "infra", "terraform", "environments", "dev", "main.tf"),
	} {
		body := readTerraform(t, relative)
		for _, required := range []string{"DenyInsecureTransport", `variable = "aws:SecureTransport"`, `values   = ["false"]`} {
			if !strings.Contains(body, required) {
				t.Errorf("%s does not contain %q", relative, required)
			}
		}
	}
}

func TestSleepWakeCanMutateOnlyTaggedInstances(t *testing.T) {
	t.Parallel()
	body := readTerraform(t, filepath.Join("..", "..", "..", "infra", "terraform", "environments", "dev", "phase8.tf"))
	for _, required := range []string{
		`sid       = "StartStopOwnedInstances"`,
		`actions   = ["ec2:StartInstances", "ec2:StopInstances"]`,
		`variable = "ec2:ResourceTag/Project"`,
		`variable = "ec2:ResourceTag/Environment"`,
	} {
		if !strings.Contains(body, required) {
			t.Errorf("sleep/wake IAM policy does not contain %q", required)
		}
	}
	if strings.Contains(body, `actions   = ["ec2:DescribeInstances", "ec2:StartInstances", "ec2:StopInstances"]`) {
		t.Fatal("sleep/wake still grants mutating EC2 actions on all resources")
	}
}

func TestRestoreWorkflowGuardsMalformedTerminalResults(t *testing.T) {
	t.Parallel()
	body := readTerraform(t, filepath.Join("..", "..", "..", "infra", "terraform", "environments", "dev", "phase9.tf"))
	for _, required := range []string{
		`Default = "MalformedRestoreResult"`,
		`Default = "MalformedBootstrapResult"`,
		`Variable = "$.restore.result.error_code", IsPresent = true`,
		`Error = "ERR_RESTORE_RESULT_INVALID"`,
		`Error = "ERR_BOOTSTRAP_RESULT_INVALID"`,
		`Retry      = [local.lambda_transient_retry]`,
	} {
		if !strings.Contains(body, required) {
			t.Errorf("restore workflow does not contain %q", required)
		}
	}
}

func readTerraform(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
