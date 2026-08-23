# Current Work

## State and Objective

Phases 1-10 are complete. Phase 11 remains pending under the approved Phase 12
reorder. Phase 12 is complete on `codex/phase-12-discord-experience`, including
the final cross-step review, and is ready for a pull request.

## Final Review Hardening

- Merged the bootstrap handoff/input fixes already on `main`, preserving
  renewable Steam authorization leases, recoverable continuation delivery,
  mission/config escaping, explicit game selection, and deployment checks.
- Reset cleanup now validates every configured cleanup scope, walks bounded
  EC2/DynamoDB/S3/Step Functions/log pagination, preserves guild access,
  guild `server.cfg`, Steam authorization state, and the current reset audit,
  and fails closed on unknown metadata instead of deleting it.
- An unacknowledged reset attempt goes to the reset DLQ without automatic
  destructive replay. Discord receives reset queue permission only when the
  disabled-by-default reset gate is enabled.
- Reset confirmation replays validate environment, guild, actor, expiry, use,
  and the phrase derived from the immutable confirmation ID.
- Guild `server.cfg` snapshots require an exact guild/revision/SHA-256 object
  path. Transport errors cannot log signed Discord attachment URLs, deterministic
  invalid requests are not retried, transient failures retain bounded retries,
  and definitively superseded upload objects receive compensating deletion.
- Artifact-worker delete permission is limited to superseded guild
  configuration objects and does not permit deletion of session inputs.
- Archive/terminate confirmation retains current Discord role context while
  remaining optionless, atomic, state-bound, single-use, and replay-safe.
- Reset workflow scope is now an explicit flattened list of ARN strings,
  including every `for_each` lifecycle workflow plus provisioning and
  bootstrap. This fixes the Terraform plan-time unsupported-attribute error.
- Reset-worker deployment no longer attempts to set Lambda's reserved
  `AWS_REGION` environment key. The worker continues to read the region that
  Lambda supplies automatically at runtime.

## Validation

- `go test ./...`, `go test -cover ./...`, `go vet ./...`, and
  `go build ./cmd/...` pass with workspace-local Go caches.
- Focused reset, server-config, confirmation, session/workflow, S3, bootstrap,
  Discord interaction, and registration suites pass.
- `terraform fmt -check -recursive infra/terraform` and
  `terraform -chdir=infra/terraform/environments/dev validate` pass.
- Focused `go test ./internal/config` passes with the workspace-local Go cache,
  confirming the worker can continue using the runtime-provided region.
- The `/rb` command JSON parses, `git diff --check` passes, the bootstrap
  artifact passes its Bash syntax test, and all 13 Lambda archives package.
- Race coverage was not run locally because this Windows host has no C
  compiler; CI remains authoritative for `go test -race -cover ./...`.

## Deployment Disposition and Attention

- No deployment, Discord registration, live reset, billable acceptance, or
  deferred Phase 10 retry was run during this review.
- Never reuse `phase-12-8-6-scoped.tfplan` or another older saved plan.
- The failed apply may have created other resources before Lambda creation
  stopped. Do not reuse its saved plan; create and review the fresh plan below,
  which will reconcile the partial apply and omit the reserved environment key.
- Reset remains disabled by default. The default action is to keep
  `reset_enabled = false`; enabling it and executing a live reset are separate
  explicit decisions.
- Re-register `/rb` after deployment so Discord removes the former confirmation
  `code` options. Pre-deployment code confirmations expire and must be requested
  again after release.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=phase-12-reset-lambda-region-fix.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-12-reset-lambda-region-fix.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply phase-12-reset-lambda-region-fix.tfplan

./scripts/verify-bootstrap-worker-deployment.ps1

./scripts/register-discord-command.ps1 `
  -ApplicationId "1533676701354299402" `
  -GuildId "1192304488351019008"
```
