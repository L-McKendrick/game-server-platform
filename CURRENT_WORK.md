# Current Work

## State and Objective

Phases 1-10, 12-14 are complete.

## Completed Development

- Phase 14

## Validation

- `go test -cover ./...`, `go vet ./...`, and `go build ./cmd/...` pass with
  workspace-local caches (Go 1.26.6).
- `go test -race -cover ./...` cannot run in this Windows environment because
  the race detector requires CGO; CI remains the race-validation gate.
- All 13 Lambda archives package successfully.
- `terraform fmt -check -recursive infra/terraform` and
  `terraform -chdir=infra/terraform/environments/dev validate` pass with
  Terraform 1.15.8.
- Discord command JSON validation and `git diff --check` pass.
- Bootstrap Bash syntax could not be rerun because neither Bash nor an installed
  WSL distribution is available. The script is unchanged by Phase 14, and its
  existing Go regression suite passes.

## Next Development Task

- Remaining delivery order is Phase 15 maximum-duration cost guardrails, Phase
  16 production hardening and measured optimization, then Phase 17 potential
  enhancements.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out phase-14-capacity-preflight.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-14-capacity-preflight.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply phase-14-capacity-preflight.tfplan
```
