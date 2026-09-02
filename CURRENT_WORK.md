# Current Work

## State and Objective

Phase 17.13 is complete on `codex/workshop-content-sources`. Accepted Workshop
scenarios now appear in `/rb edit` -> `Mission files` before their host download
and immutable mission-record import finishes.

## Completed Development

- The mission editor combines active `MissionFiles` with unresolved accepted
  Workshop scenario items in stable source order.
- Pending Workshop items are deduplicated across collections and finalized
  records, labeled `awaiting download`, and exposed without Default or Remove
  controls until a validated object key exists.
- Backward-compatible Workshop snapshots that predate canonical filenames are
  shown by Workshop item ID instead of being hidden.
- Empty pending object keys cannot be mistaken for the built-in configured or
  currently loaded mission.

## Live Finding

The deployed `test-34` record demonstrated the gap: its Workshop scenario
metadata was accepted while the session was `WAKING`, but `mission_files_json`
was intentionally absent until host synchronization completed. `test-33`
already contained the finalized accepted Workshop mission record before it was
terminated. No persistence or download mutation is required for this repair.

## Validation

- Focused Discord interaction tests pass, including finalized, pending,
  collection-deduplication, and legacy-snapshot presentation.
- `go test ./...`, `go vet ./...`, `go build ./cmd/...`, Lambda packaging, and
  `git diff --check` pass. Windows reports only expected LF/CRLF warnings.
- Infrastructure and Discord command definitions did not change. Only the
  Discord interactions Lambda requires deployment; command registration is
  unnecessary.

## Commands to Apply Current Changes

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=workshop-mission-editor-20260901.tfplan
terraform -chdir=infra/terraform/environments/dev show workshop-mission-editor-20260901.tfplan
terraform -chdir=infra/terraform/environments/dev apply workshop-mission-editor-20260901.tfplan
aws lambda get-function-configuration --function-name game-server-platform-dev-discord-interactions --query '{State:State,LastModified:LastModified,CodeSha256:CodeSha256}' --output table
```

The reviewed plan should update only the packaged Lambda functions whose ZIP
hashes changed; it must add and destroy no infrastructure. Discord command
registration is not required.
