# Current Work

## State and Objective

Phases 1-10 are complete. Phase 11 remains pending under the approved Phase 12
reorder. Step 12.13 is complete on `main`. The focused automatic-start
authorization fix is complete on `codex/fix-auto-start-role-auth`.

## Automatic-Start Authorization Fix

- Live inspection found `test-11` ready and opted into automatic setup, but its
  start command was rejected as `forbidden` on each bounded queue delivery.
- The guild access policy requires an approved Discord role. Manual lifecycle
  commands carried the member's signed role IDs, while both immediate and
  artifact-delayed automatic-start commands discarded them.
- Create and mod-options submissions now preserve role IDs from the verified
  Discord interaction. Immediate cDLC-only starts and delayed artifact-complete
  starts carry those roles through the existing start use case and command
  envelope.
- Worker authorization is unchanged and remains fail-closed. The fix does not
  introduce an owner bypass, trust client-supplied role data, or weaken the
  configured guild role/channel policy.
- Automatic command IDs remain derived from session ID and configuration
  revision. Replays preserve the same ID and idempotency key while retaining
  the original interaction roles.
- Artifact requests reject empty/oversized role IDs and more than 250 roles.
  Older queued requests remain backward-compatible because the new field is
  optional.

## Validation

- Focused domain, artifact, session, and Discord interaction tests pass,
  including role preservation through immediate and delayed automatic starts.
- `go test ./...`, `go vet ./...`, and `go build ./cmd/...` pass with
  workspace-local Go caches.
- `terraform fmt -check -recursive infra/terraform` and
  `terraform -chdir=infra/terraform/environments/dev validate` pass.
- All 13 Lambda archives package successfully.
- `git diff --check` passes.

## Deployment and Retry Disposition

- No deployment, Discord registration, Terraform apply, queue redrive, or
  unscheduled retry was performed for this fix.
- The existing `test-11` command cannot be repaired by deployment because its
  queued payload was created without roles. Its configured SQS deliveries are
  bounded; do not imply another attempt after it reaches the DLQ.
- After deploying the fix, recover `test-11` by running `/rb start` once as a
  currently authorized member. Do not redrive the old role-less message.
- Discord command definitions did not change, so re-registration is not
  required.
- Never reuse an older saved Terraform plan. Create and review the fresh plan
  below; preserve any user-owned plan files.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=phase-12-auto-start-role-auth.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-12-auto-start-role-auth.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply phase-12-auto-start-role-auth.tfplan
```

After the apply completes, run `/rb start` once for `test-11` as a member with
an allowed role. No Discord command registration is required.
