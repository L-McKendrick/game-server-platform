package aws_test

import (
	"os"
	"path/filepath"
	"regexp"
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

func TestArchiveWorkflowCompletesSuccessfullyAfterDurableCompletion(t *testing.T) {
	t.Parallel()
	body := readTerraform(t, filepath.Join("..", "..", "..", "infra", "terraform", "environments", "dev", "phase9.tf"))
	if strings.Contains(body, `Next       = "ArchiveWorkflowFailed"`) || strings.Contains(body, `ArchiveWorkflowFailed = {`) {
		t.Fatal("archive Complete still routes successful durable completion to a Fail state")
	}
	if !strings.Contains(body, `Parameters = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "complete", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        End        = true`) {
		t.Fatal("archive Complete must terminate the state-machine execution successfully")
	}
}

func TestManagedGameHostHasNoStandingSteamAuthorizationAccess(t *testing.T) {
	t.Parallel()
	body := readTerraform(t, filepath.Join("..", "..", "..", "infra", "terraform", "environments", "dev", "phase6.tf"))
	if strings.Contains(body, `resource "aws_iam_role_policy" "game_instance_bootstrap"`) || strings.Contains(body, `data "aws_iam_policy_document" "game_instance_bootstrap"`) {
		t.Fatal("managed game host bootstrap inline policy must remain removed")
	}
	for _, required := range []string{
		`data "aws_iam_policy_document" "steam_authorization_broker"`,
		`"secretsmanager:GetSecretValue", "secretsmanager:PutSecretValue"`,
		`"${aws_s3_bucket.session_assets.arn}/platform/steam-exchanges/*"`,
		`resource "aws_s3_bucket_lifecycle_configuration" "steam_authorization_exchanges"`,
		`variable = "dynamodb:LeadingKeys"`,
		`values   = ["STEAM_AUTH#CACHE", "STEAM_EXCHANGE#*"]`,
	} {
		if !strings.Contains(body, required) {
			t.Errorf("broker policy does not contain %q", required)
		}
	}
}

func TestManagedHostAssetPoliciesAreRemovedAndSSMRemains(t *testing.T) {
	body := readTerraform(t, filepath.Join("..", "..", "..", "infra", "terraform", "environments", "dev", "phase5.tf"))
	if strings.Contains(body, `resource "aws_iam_role_policy" "game_instance"`) || strings.Contains(body, `data "aws_iam_policy_document" "game_instance"`) {
		t.Fatal("managed host standing asset policy remains")
	}
	for _, required := range []string{`resource "aws_iam_role_policy_attachment" "game_instance_ssm"`, `arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore`, `resource "aws_iam_instance_profile" "game"`} {
		if !strings.Contains(body, required) {
			t.Fatalf("reviewed host integration missing %s", required)
		}
	}
}

func TestHostRoleHasOnlyReviewedAttachmentAcrossEnvironment(t *testing.T) {
	root := filepath.Join("..", "..", "..", "infra", "terraform", "environments", "dev")
	files, err := filepath.Glob(filepath.Join(root, "*.tf"))
	if err != nil || len(files) == 0 {
		t.Fatal("environment policy inventory unavailable")
	}
	blocks := regexp.MustCompile(`(?ms)^resource "aws_iam_role_policy(?:_attachment)?" "[^"]+" \{.*?^}`)
	attachments := 0
	for _, file := range files {
		for _, block := range blocks.FindAllString(readTerraform(t, file), -1) {
			if !strings.Contains(block, "aws_iam_role.game_instance.") {
				continue
			}
			attachments++
			if !strings.Contains(block, `resource "aws_iam_role_policy_attachment" "game_instance_ssm"`) || !strings.Contains(block, "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore") {
				t.Errorf("unexpected host grant in %s: %s", filepath.Base(file), block)
			}
		}
	}
	if attachments != 1 {
		t.Fatalf("host attachment count = %d, want 1", attachments)
	}
}

func TestAllHostIssuersShareRuntimeAndNormalizedScriptDigest(t *testing.T) {
	root := filepath.Join("..", "..", "..", "infra", "terraform", "environments", "dev")
	blocks := regexp.MustCompile(`(?ms)^resource "aws_lambda_function" "([^"]+)" \{(.*?)^}`)
	wanted := map[string]bool{"artifact_worker": false, "bootstrap_worker": false, "sleepwake_worker": false, "archive_worker": false, "restore_worker": false, "reliability_worker": false}
	files, _ := filepath.Glob(filepath.Join(root, "*.tf"))
	for _, file := range files {
		for _, block := range blocks.FindAllStringSubmatch(readTerraform(t, file), -1) {
			if _, ok := wanted[block[1]]; !ok {
				continue
			}
			wanted[block[1]] = true
			if !regexp.MustCompile(`BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION\s*=\s*"scoped-host-access-v1"`).MatchString(block[2]) || !regexp.MustCompile(`BOOTSTRAP_SCRIPT_SHA256\s*=\s*local.bootstrap_script_hash`).MatchString(block[2]) || !strings.Contains(block[2], `aws_iam_role_policy.host_access_issuer["`+strings.TrimSuffix(block[1], "_worker")+`"]`) {
				t.Errorf("%s must use scoped runtime, normalized digest and explicit issuer policy dependency", block[1])
			}
		}
	}
	for worker, found := range wanted {
		if !found {
			t.Errorf("issuer missing: %s", worker)
		}
	}
}

func TestGuildSessionIndexInfrastructureContract(t *testing.T) {
	t.Parallel()
	body := readTerraform(t, filepath.Join("..", "..", "..", "infra", "terraform", "environments", "dev", "main.tf"))
	for _, required := range []string{
		`name = "gsi2pk"`,
		`name = "gsi2sk"`,
		`name = "gsi2"`,
		`attribute_name = "gsi2pk"`,
		`attribute_name = "gsi2sk"`,
		`projection_type = "ALL"`,
		`"${aws_dynamodb_table.metadata.arn}/index/gsi2"`,
	} {
		if !strings.Contains(body, required) {
			t.Errorf("guild-session index infrastructure does not contain %q", required)
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
