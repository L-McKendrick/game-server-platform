package hostprereq

// AWSCLIV2Shell returns a fail-closed shell fragment that makes the official
// AWS CLI v2 available on the supported x86_64 Ubuntu host and verifies the
// major version before callers use it. The caller owns aws_cli_tmp cleanup.
func AWSCLIV2Shell() string {
	return "aws_cli_ready=false\n" +
		"if command -v aws >/dev/null 2>&1 && aws_cli_version=\"$(aws --version 2>&1)\"; then\n" +
		"  case \"$aws_cli_version\" in aws-cli/2.*) aws_cli_ready=true;; esac\n" +
		"fi\n" +
		"if ! $aws_cli_ready; then\n" +
		"  command -v apt-get >/dev/null 2>&1 || { echo 'ERR_AWS_CLI_PREREQUISITE: apt-get is unavailable' >&2; exit 1; }\n" +
		"  apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl unzip || { echo 'ERR_AWS_CLI_PREREQUISITE: dependencies could not be installed' >&2; exit 1; }\n" +
		"  aws_cli_tmp=\"$(mktemp -d /run/gsp-awscli.XXXXXX)\"\n" +
		"  curl --fail --location --silent --show-error 'https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip' -o \"$aws_cli_tmp/awscliv2.zip\" || { echo 'ERR_AWS_CLI_PREREQUISITE: AWS CLI v2 download failed' >&2; exit 1; }\n" +
		"  unzip -q \"$aws_cli_tmp/awscliv2.zip\" -d \"$aws_cli_tmp\" || { echo 'ERR_AWS_CLI_PREREQUISITE: AWS CLI v2 archive is invalid' >&2; exit 1; }\n" +
		"  \"$aws_cli_tmp/aws/install\" --bin-dir /usr/local/bin --install-dir /usr/local/aws-cli || { echo 'ERR_AWS_CLI_PREREQUISITE: AWS CLI v2 installation failed' >&2; exit 1; }\n" +
		"fi\n" +
		"if ! aws_cli_version=\"$(aws --version 2>&1)\"; then echo 'ERR_AWS_CLI_PREREQUISITE: AWS CLI v2 verification failed' >&2; exit 1; fi\n" +
		"case \"$aws_cli_version\" in aws-cli/2.*) ;; *) echo 'ERR_AWS_CLI_PREREQUISITE: AWS CLI v2 is required' >&2; exit 1;; esac\n"
}
