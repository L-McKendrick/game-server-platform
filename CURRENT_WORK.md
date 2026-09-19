# Current Work

## State and Objective

Beta-release copy and README preparation are complete on
`codex/beta-release-prep`, based on `04aac08` (PR #29). Routine Discord handler
responses now use short, plain-language wording, and the README provides a
step-by-step Discord application setup path.

## Current Handoff

- Simplified routine access, expired-form, cancellation, confirmation,
  lifecycle, capacity, start-readiness, in-progress, and fallback error text.
- Archive and permanent-deletion confirmations remain explicit about their
  effect and ten-minute confirmation window.
- Preserved the pre-existing user edit intent while correcting confirmed
  archives so they are described as archived rather than deleted.
- Updated exact-copy tests without changing command behavior, authorization,
  lifecycle rules, or Discord command definitions.
- Added a ten-step README checklist covering Discord application creation,
  installation scopes and permissions, AWS deployment, secret storage,
  interaction-endpoint connection, `/rb` registration, access setup, and
  first-run verification. It links to the detailed deployment runbook.
- `go test ./internal/adapters/discord/interactions`, `go vet ./...`, and
  `git diff --check` pass.
- `go test ./...` has one unrelated failure:
  `TestArchiveWorkflowCompletesSuccessfullyAfterDurableCompletion` expects the
  archive state machine's `Complete` state to end the execution successfully.

## Important User Attention

- Review the unrelated archive workflow security-contract failure before using
  a full-suite pass as a beta release gate. This copy-only task does not change
  the archive state machine.

## Commands to Apply Current Changes

Package the updated Discord Lambda, create and review a fresh Terraform plan,
then apply that exact plan only after approval:

```powershell
./scripts/package-discord-lambda.ps1
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity
terraform -chdir=infra/terraform/environments/dev init -backend-config=backend.hcl -input=false
terraform -chdir=infra/terraform/environments/dev plan -out=beta-handler-copy.tfplan
terraform -chdir=infra/terraform/environments/dev show beta-handler-copy.tfplan
terraform -chdir=infra/terraform/environments/dev apply beta-handler-copy.tfplan
```

Discord command registration is not required because command definitions did
not change.
