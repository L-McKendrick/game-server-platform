# Current Work

## State and Objective

Phase 18 (Discord lifecycle UX and restart) is complete on
`codex/discord-lifecycle-ux`. Next: prepare and review the Phase 18 pull request,
then proceed to Phase 19 only after the branch is merged.

## Current Handoff

- Public cards show `Current mission` only for running or idle sessions.
- Successfully archived cards now use a light bluish-gray color and reduce to
  the game/session title, description, active modlist filename/link, and the
  native relative archive-completion timestamp when that timestamp exists.
- Archived legacy records safely show an unavailable or vanilla modlist
  fallback and do not invent an archive timestamp or use a pending revision.
- Archived cards retain `Refresh` for repair and lose `Show players`; terminated
  cards continue to clear all controls. Delivery revalidates this policy against
  current lifecycle state so queued legacy card updates cannot restore controls.
- Archived sessions with actionable failures retain diagnostic fields instead
  of using the reduced successful-archive presentation.
- Focused session-card, domain, session-service, Discord notification,
  interaction, and notification-worker tests passed with the installed Go
  toolchain. Full repository validation was intentionally deferred by request.
- No Terraform resources or Discord command definitions changed. Nothing was
  deployed or registered against Discord or AWS.

## Commands to Apply Current Changes

Run from the repository root. Package the affected Discord interaction and
notification worker Lambdas, then create and review a fresh saved Terraform
plan. Preserve existing plan files.

```powershell
$env:GOTOOLCHAIN = "go1.26.5"
$env:AWS_PROFILE = "game-server-dev"
./scripts/package-discord-lambda.ps1
$phase18Plan = "phase18-4-public-card-lifecycle-$(Get-Date -Format 'yyyyMMdd-HHmmss').tfplan"
terraform -chdir=infra/terraform/environments/dev plan "-out=$phase18Plan"
terraform -chdir=infra/terraform/environments/dev show $phase18Plan
```

After reviewing and approving that exact plan, in the same PowerShell session:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $phase18Plan
foreach ($component in @("discord-interactions", "notification-worker")) {
  aws lambda get-function-configuration --function-name "game-server-platform-dev-$component" --profile game-server-dev --region us-west-2 --query '{Status:LastUpdateStatus,CodeSHA256:CodeSha256}'
}
```

Discord command re-registration is not required. Verify setup and sleeping cards
omit `Current mission`; active cards retain it; archived cards use the reduced
bluish-gray presentation with an active modlist link and relative timestamp;
archived cards expose only `Refresh`; and terminated cards expose no controls.
