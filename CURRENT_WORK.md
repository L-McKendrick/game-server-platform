# Current Work

## Active Branch and Scope

- Branch: `codex/fix-bootstrap-worker-drift`.
- Phase 12.8 is complete through task 12.8.15. This branch contains the Discord
  admin/start orchestration, bootstrap drift correction, confirmation IAM and
  role-context fixes, shared Steam authorization, mission/card polish, required
  `/rb create arma-3` selection, and the final security/reliability review.
- The user-owned untracked `infra/terraform/environments/dev/tfplan` remains
  untouched and must not be reused for this release.

## Review Hardening

- Provisioning now validates that a replayed bootstrap continuation is still
  pending/running and still owns the session lock. A terminal or detached
  continuation fails closed instead of returning a stale command.
- If the provisioning state machine cannot enqueue its reserved bootstrap
  continuation after bounded retries, it invokes the bootstrap failure path.
  This records a stable failure and releases the session workflow lock instead
  of leaving `/rb status` stuck in startup for the eight-hour lease.
- Steam authorization now uses a renewable 15-minute DynamoDB lease with a
  five-minute heartbeat. Loss of lease ownership stops bootstrap; a forcibly
  killed host can block a retry for at most 15 minutes rather than seven hours.
- Mission uploads preserve conventional names such as `test.Stratis.pbo`,
  normalize unsafe characters before S3/host storage, enforce a 255-byte name
  bound, and escape the mission template when rendering `server.cfg`.
- Command-registration coverage binds the user-facing `Arma 3` choice to the
  internal `arma-3` value so future game expansion cannot silently drift.

## Validation

- `go test ./...`, `go vet ./...`, and `go build ./cmd/...` pass with
  workspace-local Go caches.
- Focused artifact, provisioning, bootstrap-script, domain, and command
  registration tests pass; the bootstrap artifact passes `bash -n`.
- `terraform fmt -check -recursive infra/terraform` and
  `terraform -chdir=infra/terraform/environments/dev validate` pass.
- The `/rb` command JSON parses and `git diff --check` passes.
- Race coverage was not run locally because this Windows host has no C
  compiler; CI remains authoritative for `go test -race -cover ./...`.

## Deployment Disposition

- These review changes are not deployed. They change the provisioning Step
  Functions definition/IAM, bootstrap script object, artifact worker,
  provisioning and bootstrap workers, Discord interaction package, and `/rb`
  command definition.
- Create and inspect a new Terraform plan after packaging. Do not apply if it
  contains unrelated deletions or changes outside the expected release scope.
- Known development targets are Discord application `1533676701354299402`,
  guild `1192304488351019008`, AWS profile `game-server-dev`, and Region
  `us-west-2`.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=phase-12-review-hardening.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-12-review-hardening.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply phase-12-review-hardening.tfplan

./scripts/verify-bootstrap-worker-deployment.ps1

./scripts/register-discord-command.ps1 `
  -ApplicationId "1533676701354299402" `
  -GuildId "1192304488351019008"
```
