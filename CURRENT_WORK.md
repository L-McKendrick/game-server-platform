# Current Work

## State and Objective

The branch `codex/public-card-channel` completes Phase 19.1. The implementation
and full review pass are complete and ready for deployment review. The branch
has not been pushed.

## Completed Development

- Added `PublicCardChannelID` to the existing guild access-policy record and
  preserved it when access roles change or are cleared.
- Added a **Public card channel** area to `/rb admin` for members with current
  Administrator or Manage Server permission. It uses Discord's text-channel
  selector and persists the selected channel.
- New session creation resolves the guild setting before creating the session.
  The selected channel becomes the session's existing durable channel, so card
  creation, linked modlist publishing, lifecycle edits, and repair continue
  through unchanged delivery paths.
- When no channel has been selected, creation continues to use the invoking
  channel. Persisting the first channel selection also preserves deployment
  fallback access roles and channels.
- Updated deployment verification, architecture, and Discord experience
  documentation.
- Review fixes reject crafted non-text channel selections and allow the trusted
  provisioning-to-bootstrap continuation to retain its guild, owner, workflow,
  correlation, and lock bindings when the command invocation channel differs
  from the configured card channel.

## Review and Validation

- `go test ./...` passes.
- `go test -cover ./...` passes.
- `go vet ./...` passes.
- `go build ./cmd/...` passes.
- `./scripts/package-discord-lambda.ps1` passes and rebuilds the Lambda set.
- `terraform fmt -check -recursive infra/terraform` passes.
- `terraform -chdir=infra/terraform/bootstrap validate` passes.
- `terraform -chdir=infra/terraform/environments/dev validate` passes.
- `git diff --check` passes with only expected LF-to-CRLF working-tree
  warnings.
- Focused coverage verifies authorization, guild-setting persistence, role
  preservation, admin channel selection, selected-channel session/card
  delivery, rejection of non-text selections, artifact routing, existing admin
  menu behavior, DynamoDB adapter compilation, and cross-channel bootstrap
  continuation.
- Local race coverage was not run because `CGO_ENABLED=0` and no C compiler is
  installed. The required GitHub CI job remains responsible for the race pass.

## Next Development Task

- Phase 15 maximum session duration guardrails are next unless another task is
  explicitly approved out of order.

## Commands to Apply Current Changes

From the repository root in PowerShell, after the deferred validation pass is
complete:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"

# Rebuild the Discord Lambda packages containing the interaction and shared
# guild-access changes.
./scripts/package-discord-lambda.ps1

# Create and review a fresh plan containing the rebuilt Lambda artifacts.
$PlanFile = "public-card-channel-$((Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')).tfplan"
terraform -chdir=infra/terraform/environments/dev plan -out $PlanFile
terraform -chdir=infra/terraform/environments/dev show $PlanFile
terraform -chdir=infra/terraform/environments/dev apply $PlanFile
```

No Discord command registration is required because the `/rb` command schema
did not change. After deployment, choose **Public card channel** in `/rb admin`,
create a non-billable draft from another allowed channel, and confirm its card
appears in the selected channel.
