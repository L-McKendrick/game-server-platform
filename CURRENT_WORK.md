# Current Work

## State and Objective

Phases 1-10 and 12-14 are complete. Phase 18 is the approved out-of-order
miscellaneous-fixes and quality-of-life phase on `codex/misc-fixes`; its
mission-menu polish and lifecycle-visibility steps are complete.

## Completed Development

- `/rb edit` mission files now renders with Components V2 so each built-in or
  uploaded filename, status, and relevant controls share one action row.
- Redundant `Default` actions are omitted, `Add mission` is the final row, and
  five-file pagination is preserved.
- Focused coverage verifies the row association, action relevance, bottom add
  action, Components V2 flags, and bounded filename labels.
- Bootstrap progress now describes active host preparation, game-file
  installation, and Workshop installation instead of using
  completion-sounding labels for long-running work.
- The existing bootstrap output protocol now carries only allowlisted Arma 3
  server-file or bounded Workshop-count activity. The durable progress record
  and public card show the latest safe download target without raw SteamCMD
  output, item names, paths, or authentication data.
- `/rb status` now shows Discord-native automatic-sleep and automatic-archive
  timestamps only when authoritative idle or sleeping evidence applies.
- Phase 18.2 review made cumulative SSM output stage-aware so a completed
  download target cannot leak forward into a newer bootstrap stage. Coverage
  includes allowlisting, bounds, persistence, replay, stale output, unknown or
  interrupted evidence, clock anomalies, card idempotency, and Discord bounds.
- `AGENTS.md` now records that GitHub CI owns the CGO race check on Windows
  feature hosts without a local C compiler; normal tests and coverage remain
  required locally.

## Validation

- `go test ./...`, `go test -cover ./...`, `go vet ./...`, and
  `go build ./cmd/...` pass with workspace-local caches.
- All 13 Lambda archives package successfully; the Arma bootstrap script passes
  Git Bash syntax validation.
- `terraform fmt -check -recursive infra/terraform`, development-environment
  `terraform validate`, and `git diff --check` pass.
- `go test -race -coverprofile=coverage.out ./...` is delegated to the required
  GitHub CI job because this Windows host has no CGO C compiler, as documented
  in `AGENTS.md`.

## Next Development Task

- Phase 18.3 is next: add separately persisted Arma 3 server-only mods through the
  existing mod revision and SteamCMD bootstrap paths, including `-serverMod=`
  launch handling and exclusion from public/client modlists.
- Phase 18.4 then deploys accepted mission uploads to running servers through
  the existing artifact worker and bounded SSM copy, without restarting Arma
  or adding a separate synchronization worker.
- Phase 15 remains next in the primary delivery order after this approved
  out-of-order branch.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out misc-fixes-progress-timing.tfplan
terraform -chdir=infra/terraform/environments/dev show misc-fixes-progress-timing.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply misc-fixes-progress-timing.tfplan

$FunctionName = terraform -chdir=infra/terraform/environments/dev output -raw discord_interactions_function_name
$ExpectedHash = [Convert]::ToBase64String([Security.Cryptography.SHA256]::HashData([IO.File]::ReadAllBytes((Resolve-Path "dist/discord-interactions.zip"))))
$DeployedHash = aws lambda get-function --function-name $FunctionName --query Configuration.CodeSha256 --output text
if ($DeployedHash -ne $ExpectedHash) { throw "Discord interaction Lambda package hash does not match the local archive." }
```

No Discord command registration is required because the command definition did
not change. After deployment, start a development session and verify that its
public card advances through host, game-file, and Workshop work; then verify
the projected automatic-sleep or automatic-archive timestamp in `/rb status`
for a session with applicable inactivity evidence.
