# Current Work

## State and Objective

Phases 1-10 are complete. Phase 11 remains pending under the approved Phase 12
reorder. Step 12.13 is complete on `main`. The focused automatic-start
authorization and cDLC-only bootstrap fixes are complete on
`codex/fix-auto-start-role-auth`.

## Focused Bug Fixes

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
- Live `test-11` provisioning succeeded and retained its EC2/EBS resources, but
  bootstrap failed after `MODS_APPLIED` because cDLC-only installation did not
  create Steam's optional `steamapps/workshop` directory before a recursive
  ownership operation.
- Bootstrap now creates that directory idempotently before applying ownership.
  Workshop-backed sessions are unchanged, and cDLC-only retries reuse the
  retained host and completed durable checkpoints.

## Validation

- Focused domain, artifact, session, and Discord interaction tests pass,
  including role preservation through immediate and delayed automatic starts.
- Focused SSM bootstrap, bootstrap application, and worker tests pass. Regression
  coverage requires the optional Workshop directory to be created before its
  ownership operation, and the bootstrap artifact passes Bash syntax validation.
- `go test ./...`, `go vet ./...`, and `go build ./cmd/...` pass with
  workspace-local Go caches.
- `terraform fmt -check -recursive infra/terraform` and
  `terraform -chdir=infra/terraform/environments/dev validate` pass.
- All 13 Lambda archives package successfully.
- `git diff --check` passes.

## Deployment and Retry Disposition

- No deployment, Discord registration, Terraform apply, queue redrive, or
  unscheduled retry was performed for this fix.
- The old role-less `test-11` command reached its bounded DLQ disposition. Do
  not redrive it and do not imply another attempt is scheduled.
- `test-11` is currently `FAILED`/`ACTION_REQUIRED`; its running EC2 instance
  remains billable. No automatic bootstrap retry is scheduled.
- After deploying both fixes and verifying the bootstrap worker, recover
  `test-11` by running `/rb start` once as a currently authorized member. This
  starts a new resumable bootstrap and reuses retained infrastructure; it does
  not redrive the old message or failed execution.
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
terraform -chdir=infra/terraform/environments/dev plan -out=phase-12-auto-start-cdlc-bootstrap-fixes.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-12-auto-start-cdlc-bootstrap-fixes.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply phase-12-auto-start-cdlc-bootstrap-fixes.tfplan

./scripts/verify-bootstrap-worker-deployment.ps1
```

Only after verification succeeds, run `/rb start` once for `test-11` as a
member with an allowed role. No Discord command registration is required.
