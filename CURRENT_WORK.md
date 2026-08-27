# Current Work

## State and Objective

Workshop content-source development through Phase 17.8 is complete on
`codex/workshop-content-sources`. Public Arma 3 scenarios and client mods can
now use individual Workshop items or collections without replacing the existing
upload workflows. No deployment or pull request has been performed.

## Completed Development

- One bounded resolver validates canonical public Steam links, Arma 3 app ID,
  item/collection type, tags, one-level collection membership, ordering,
  deduplication, availability, and retry disposition.
- Scenario sources require Data Type `Scenario` plus `Multiplayer` or `Coop`.
  Accepted `.pbo` files enter the existing immutable mission manager without
  changing the configured/current mission.
- Client-mod sources generate sanitized content-addressed internal presets,
  public modlists, and immutable source manifests. Server-only children are
  explicitly excluded from the client preset and mixed collections retain
  eligible client items.
- Workshop mod results use the existing active/pending revision lifecycle.
  Requests bind to the active revision seen at submission; start, wake, restore,
  promotion, rollback, card/status, archive/restore, and termination reuse the
  established paths. Publisher changes require explicit resubmission.
- Steam child metadata is fetched in batches of 100. A maximum-size collection
  now needs at most seven metadata calls instead of approximately 502, reducing
  Lambda duration, rate-limit exposure, and request cost. The worker timeout is
  90 seconds and FIFO visibility is 540 seconds.
- The hot DynamoDB projection omits untrusted titles and caps aggregate Workshop
  mod history at 1,000 classified items, avoiding per-item size growth that
  could disrupt ordinary session reads and writes. Full generated provenance
  remains in the immutable S3 manifest.
- Per-session FIFO serialization prevents Workshop and upload mutations from
  racing while leaving other sessions independent. Ordinary uploads do not call
  Steam and retain their previous processing path.
- Permanent errors explain the exact correction: public visibility, canonical
  link, scenario tags, client/server mod type, current lifecycle operation, or
  stale revision. Exhausted transient retries send a final actionable notice
  instead of silently ending in the DLQ. Active content remains unchanged.
- Existing artifact-worker S3 scope already covers the three generated objects;
  no IAM expansion, new worker, service, cache, database, NAT Gateway, or
  schedule was added. Session-prefix cleanup covers safe retry orphans.
- User and operator behavior, cost/performance bounds, and recovery steps are
  documented in `docs/workshop-content-sources.md`.

## Validation

- Changed Go files pass `gofmt -l`; unrelated pre-existing formatting findings
  were not rewritten.
- `go test -cover ./...`, `go vet ./...`, and `go build ./cmd/...` pass.
- All Lambda archives package successfully.
- Bootstrap Bash syntax and focused SteamCMD/bootstrap tests pass.
- Discord command registration and interaction contract tests pass; command
  definitions did not change, so re-registration is unnecessary.
- Terraform recursive format and development-environment validation pass.
- `git diff --check` passes. The Windows checkout reports only expected LF/CRLF
  conversion warnings.

## Proposed Pull Request

Title: `feat: add Steam Workshop content sources`

Summary: add bounded item/collection resolution for Arma 3 scenarios and client
mods, immutable provenance and generated artifacts, existing mission/mod
lifecycle integration, per-item authenticated SteamCMD safeguards, actionable
Discord recovery messages, metadata batching, persistence bounds, and the
minimal artifact-worker timeout/queue visibility changes. No new persistent
service or IAM grant is introduced.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"

./scripts/package-discord-lambda.ps1
$PlanFile = "workshop-content-sources-$((Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')).tfplan"
terraform -chdir=infra/terraform/environments/dev plan -out $PlanFile
terraform -chdir=infra/terraform/environments/dev show $PlanFile
# Apply only after confirming the reviewed plan updates the affected Lambda
# packages, artifact-worker timeout, artifact FIFO visibility, and bootstrap
# script object without unrelated infrastructure changes.
terraform -chdir=infra/terraform/environments/dev apply $PlanFile
```

No Discord command registration is required.
