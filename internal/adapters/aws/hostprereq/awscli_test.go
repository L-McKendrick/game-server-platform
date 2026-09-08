package hostprereq

import (
	"strings"
	"testing"
)

func TestAWSCLIV2ShellInstallsAndVerifiesFailClosed(t *testing.T) {
	t.Parallel()
	command := AWSCLIV2Shell()
	for _, required := range []string{
		"aws_cli_ready=false",
		"https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip",
		"aws --version",
		"aws-cli/2.*",
		"ERR_AWS_CLI_PREREQUISITE",
	} {
		if !strings.Contains(command, required) {
			t.Errorf("AWS CLI prerequisite missing %q", required)
		}
	}
	if strings.Contains(command, "aws-cli/1.*") {
		t.Fatal("AWS CLI v1 was accepted")
	}
	if !strings.Contains(command, "if ! aws_cli_version=") {
		t.Fatal("a broken existing AWS CLI can exit without the stable prerequisite error")
	}
}
