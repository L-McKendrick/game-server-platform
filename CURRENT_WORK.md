# Current Work

## State and Objective

Phases 1-10, 12-14, and the approved out-of-order Phase 18 mission-menu polish
are complete on `codex/misc-fixes`.

## Completed Development

- `/rb edit` mission files now renders with Components V2 so each built-in or
  uploaded filename, status, and relevant controls share one action row.
- Redundant `Default` actions are omitted, `Add mission` is the final row, and
  five-file pagination is preserved.
- Focused coverage verifies the row association, action relevance, bottom add
  action, Components V2 flags, and bounded filename labels.

## Validation

- `go test ./...`, `go vet ./...`, and `go build ./cmd/...` pass with
  workspace-local caches.
- All 13 Lambda archives package successfully.
- `git diff --check` passes.

## Next Development Task

- Phase 15 maximum-duration cost guardrails remains next in the primary
  delivery order.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out misc-fixes-mission-menu.tfplan
terraform -chdir=infra/terraform/environments/dev show misc-fixes-mission-menu.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply misc-fixes-mission-menu.tfplan

$FunctionName = terraform -chdir=infra/terraform/environments/dev output -raw discord_interactions_function_name
$ExpectedHash = [Convert]::ToBase64String([Security.Cryptography.SHA256]::HashData([IO.File]::ReadAllBytes((Resolve-Path "dist/discord-interactions.zip"))))
$DeployedHash = aws lambda get-function --function-name $FunctionName --query Configuration.CodeSha256 --output text
if ($DeployedHash -ne $ExpectedHash) { throw "Discord interaction Lambda package hash does not match the local archive." }
```

No Discord command registration is required because the command definition did
not change. After deployment, reopen `/rb edit` for `mission-files` and verify
the private menu layout in the development guild.
