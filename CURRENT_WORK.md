# Current Work

## State and Objective

Phase 18 (Discord lifecycle UX and restart) is ready for PR review on
`codex/discord-lifecycle-ux`. The final review correction is committed locally;
per user instruction, do not push or create a PR. Next: user reviews the proposed
PR title/description and chooses when to publish the branch.

## Current Handoff

- Reviewed the branch against merge base `53373c028ce20904f5f79eabc1fe2ba0b5e1bf27`.
  Corrected one confirmed defect: restart synchronized a new Workshop scenario
  and then overwrote it with an older accepted file sharing its filename.
  Accepted mission files/settings now deploy before pending Workshop content.
- Added a shell regression using the actual restart block and mission deployment
  code. Both manifest and legacy single-mission cases failed before the fix and
  passed afterward; restart replay and TeamSpeak-preservation checks passed.
- Go 1.26.5 validation passed: `go test -cover ./...`, `go vet ./...`,
  `go build ./cmd/...`, and Lambda packaging. Terraform 1.15.8 recursive formatting
  and development validation passed. No local C compiler was available; the
  required GitHub CI race check remains the release gate.
- Release behavior: `/rb start` handles ready drafts and sleeping sessions;
  `/rb restart` applies content/settings while leaving EC2 and TeamSpeak running;
  creation supports optional automatic setup and a best-effort initial ready ping.
  Public cards hide inapplicable mission/player/progress/control information.
  Direct card-token claims remove the normal guild-scan lookup dependency.
- Updated the restart runbook and roadmap. Nothing was deployed, registered,
  restarted, pushed, or opened as a PR during this review.

## Important Operator Attention

- Prior live evidence showed `test-47` needs manual `/rb start` after its content
  acceptance is confirmed. Default: do not redrive its old role-less message.
- Verify both controls on the existing `test-50` card after deployment to exercise
  legacy token backfill, then repeat a click to check the direct lookup path.
- Live restart acceptance interrupts players. Default: do not run it without
  explicit approval for a disposable session.

## Commands to Apply Current Changes

Run from the repository root. Lambda archives were already packaged with Go
1.26.5 during this review; no further packaging is needed for this checkout.
The restart script has a new content-addressed S3 key and Terraform updates the
workers' script references. Review the full release plan, preserving existing
plan files and provisioning/budget settings.

```powershell
$env:AWS_PROFILE = "game-server-dev"
$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$phase18Plan = "phase18-restart-mission-review-$timestamp.tfplan"
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

Registration remains required for the Phase 18 release, which removes `/rb wake`
and adds `/rb restart`; this review correction introduces no further command
changes. Verify optional automatic setup/ready ping, sleep followed by start,
lifecycle-specific cards and controls, and legacy card-token backfill. With live
restart approval, verify no-change and pending-content restarts and an updated
Workshop scenario retaining its new checksum when it shares the old filename.
Confirm unchanged EC2/TeamSpeak and promotion only after health verification.
