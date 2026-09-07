# Current Work

## State and Objective

Phase 18.2 (restart) is implemented on `codex/discord-lifecycle-ux`.
Next: 18.3 (creation readiness and notification). Complete all nested tasks,
then checks, review, and commit before ending the turn. At phase completion,
review the full branch and provide a PR title/description without publishing a PR.

## Current Handoff

- `/rb restart` is immediate, owner/admin-authorized, and restricted to stable
  running/idle sessions. It uses the existing workflow lock and wake state
  machine, bypassing EC2 control/waits. TeamSpeak stays running.
- The managed-host path applies pending mods/server mods and captured server
  settings, installs pending scenarios without new mission-selection behavior,
  and restarts Arma even when nothing is pending. Health gates mod promotion.
- SSM command lookup and a host completion marker suppress replay. Failures
  preserve resources and diagnostics without rollback; reconciliation clears
  locks and applying revision status. No new persistent infrastructure is added.
- Infrastructure changes: shared wake branching/completion catch, ListCommands
  read permission for the sleep/wake worker, bootstrap script and Lambda code.
- Validation: Go 1.26.5 coverage suite, vet, command builds, Lambda packaging,
  shell tests, Terraform 1.15.8 format/validate, command contracts and diff review.
  CGO remains disabled; race coverage is deferred to required CI.
- The broad test run encountered the existing bootstrap sampler timing test;
  sequential validation avoids concurrent compiler load. Nothing was deployed,
  registered, or tested against a live server.
- Archived `/rb start` routing remains deferred to 20.7.4. Do not attempt live
  restore before the test-44 prerequisites and failure handling are repaired.

## Commands to Apply Current Changes

Run from the repository root when deploying this item. Package before creating
and reviewing a fresh plan; preserve existing plan files. Keep provisioning and
budget configuration unchanged. Do not deploy as part of routine validation.

```powershell
$env:GOTOOLCHAIN = "go1.26.5"
$env:AWS_PROFILE = "game-server-dev"
./scripts/package-discord-lambda.ps1
$phase18Plan = "phase18-2-restart-$(Get-Date -Format 'yyyyMMdd-HHmmss').tfplan"
terraform -chdir=infra/terraform/environments/dev plan "-out=$phase18Plan"
terraform -chdir=infra/terraform/environments/dev show $phase18Plan
```

After reviewing and approving that exact plan, in the same PowerShell session:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $phase18Plan
foreach ($component in @("discord-interactions", "command-worker", "sleepwake-worker")) {
  aws lambda get-function-configuration --function-name "game-server-platform-dev-$component" --profile game-server-dev --region us-west-2 --query '{Status:LastUpdateStatus,CodeSHA256:CodeSha256}'
}
./scripts/verify-bootstrap-worker-deployment.ps1
```

Register the updated commands with the existing application/guild IDs and a
short-lived bot token in the process environment, as described in
`docs/runbooks/deploy-discord-interactions.md`:

```powershell
go run ./cmd/discord-register
Remove-Item Env:DISCORD_BOT_TOKEN
```

Verify `/rb restart` is registered. When a live restart is approved, verify
no-change and pending-content restart on a running/idle session, unchanged EC2
and TeamSpeak, private/card progress, final Arma health, and `/rb status` failure
guidance. See `docs/runbooks/restart-game-server.md`.
Do not attempt live restore before 20.7 repairs are validated.
