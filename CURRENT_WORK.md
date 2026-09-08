# Current Work

## State and Objective

Phase 18 (Discord lifecycle UX and restart) is release-ready on
`codex/discord-lifecycle-ux` after the full branch review. Next: review and merge
the Phase 18 pull request, then start Phase 19 on a new branch.

## Current Handoff

- `/rb start` provisions ready drafts and wakes sleeping sessions; `/rb wake`
  is retired. `/rb restart` applies pending content/settings and restarts only
  Arma while leaving EC2 and TeamSpeak running.
- Creation supports off-by-default automatic setup and one best-effort initial
  ready ping. Async Workshop requests now retain their bounded signed Discord
  role context so the command worker can perform its normal authorization after
  resolution instead of rejecting the internal start as `forbidden`.
- The real SSM health adapter now accepts `RESTARTING`; without this correction,
  deployed restarts would always have failed before their health probe.
- Public cards show mission/player data only while active. Completed archives
  use the compact bluish-gray title/description/active-modlist/time view, retain
  no controls, preserve legacy fallbacks, and keep actionable restore errors.
  `Show players` is now emitted only for `RUNNING` and `IDLE`; all setup,
  transition, sleeping, archived, failed, and terminated cards omit it.
  Successfully completed sleep cards omit the finished progress block while
  `/rb status` retains its diagnostic history; in-flight and failed sleep
  progress remains visible. Fully sleeping, archived, and terminated cards
  retain no controls; transition and failure cards retain `Refresh`.
- The review found no remaining authorization, lifecycle-lock, replay,
  idempotency, failure-resolution, backward-compatibility, or deployment-scope
  blocker. Automatic-start delivery remains bounded by normal queue retry/DLQ
  behavior; ready-message delivery remains deliberately best effort with no
  retry.
- Validation passed with the installed Go toolchain: `go test ./...`, `go vet
  ./...`, Lambda packaging, Terraform recursive formatting and development
  validation, Git Bash syntax validation for the bootstrap script, and diff
  checks. One initial timing-sensitive bootstrap sampler test failed during a
  parallel run and passed on the focused and subsequent full runs.
- Read-only live evidence from `test-47` confirmed Workshop resolution reached
  `NEW` and queued automatic start, but the deployed command worker rejected the
  old request as `forbidden` because it lacked roles. That already-queued legacy
  request cannot be repaired by this deployment; run `/rb start` manually for
  `test-47` after confirming its content remains accepted.
- Read-only live evidence from `test-49` confirmed install telemetry persisted
  and delivered `Arma 3 server files (42%)` on card revision 17. Percentage
  updates follow the bounded bootstrap observation cadence (about two minutes
  while installation is active); no telemetry defect was found. The same test
  exposed the setup-card `Show players` defect corrected above.
- Development command registration now rejects malformed snowflakes locally;
  the handoff discovers the deployed non-secret application/guild IDs and uses
  the secure prompting script instead of copyable placeholder values.
- Nothing was deployed, registered, restarted, or otherwise mutated in AWS or
  Discord during this review.

## Commands to Apply Current Changes

Run from the repository root. Package the changed Lambda archives, then create
and review a fresh saved Terraform plan. Preserve all existing plan files and do
not change provisioning or budget settings.

```powershell
$env:GOTOOLCHAIN = "go1.26.5"
$env:AWS_PROFILE = "game-server-dev"
./scripts/package-discord-lambda.ps1
$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$phase18Plan = "phase18-release-review-$timestamp.tfplan"
terraform -chdir=infra/terraform/environments/dev plan "-out=$phase18Plan"
terraform -chdir=infra/terraform/environments/dev show $phase18Plan
```

After approving that exact plan, in the same PowerShell session:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $phase18Plan
aws lambda list-functions --profile game-server-dev --region us-west-2 --query "Functions[?starts_with(FunctionName, 'game-server-platform-dev-')].[FunctionName,LastUpdateStatus,CodeSha256]" --output table
$discordSecretJson = aws secretsmanager get-secret-value --secret-id /game-server-platform/dev/discord-bot-token --profile game-server-dev --region us-west-2 --query SecretString --output text
$discordSecret = $discordSecretJson | ConvertFrom-Json
$env:DISCORD_BOT_TOKEN = $discordSecret.token
./scripts/register-discord-command.ps1 -ApplicationId "1533676701354299402" -GuildId "1192304488351019008"
Remove-Item Env:DISCORD_BOT_TOKEN
$discordSecret = $null
$discordSecretJson = $null
```

Registration is required because Phase 18 removes `/rb wake` and adds `/rb
restart`. Verify automatic setup through a new Workshop item/collection, one
opted-in ready ping, `/rb sleep` followed by `/rb start`, no-change and
pending-change restart health, download-percentage updates across at least two
bootstrap observations, lifecycle-specific cards/controls (including no `Show
players` before `RUNNING` or while sleeping), no completed progress block after
sleep, no `Refresh` after fully sleeping or archived, and manual `/rb start`
recovery for `test-47`.

## Important Operator Attention

- `test-47` needs a manual `/rb start` after content acceptance is confirmed.
  Default action: do not redrive or alter its old role-less queue message.
- Restart live acceptance mutates a running game service and may interrupt
  players. Default action: do not run it without explicit approval for a
  disposable session.
