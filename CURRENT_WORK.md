# Current Work

## State and Objective

Phase 17.9 and the Steam Workshop content-source implementation are complete on
`codex/workshop-content-sources`. Resolved Workshop missions and mods now use
one bounded host synchronization path across live edits, initial bootstrap,
wake, and restore while reusing the existing workflow, SSM, reliability, and
status infrastructure. No deployment or pull request has been performed.

## Planned Next Work

- Review and apply a fresh Terraform plan, then open the prepared Phase 17 pull
  request when deployment approval is given.
- Use one target-aware content-sync command for individual items and
  collections. Scenarios may be added atomically to a running host without
  changing the current mission; mods may be downloaded to isolated staging but
  become active only through a controlled restart or wake.
- Reuse the session workflow lock and workflow record, the existing artifact
  and reliability workers, the wake content stage, S3, DynamoDB, and SSM. Add
  only a terminal SSM EventBridge callback and narrow permissions; do not add a
  polling Step Function, worker, queue, table, bucket, GSI, or schedule.
- Reject Workshop mutations while archived until an explicit archive-overlay
  contract exists, because the current restore manifest comparison treats such
  changes as drift.

## Completed Phase 17.9 Development

- **17.9.1:** Collections containing more than 50 direct children are rejected
  before child metadata retrieval. The same ceiling is enforced by finalized
  resolution and persisted mission/mod source validation while the generic
  metadata adapter retains its independent batching capacity.
- Workshop source changes now share one explicit lifecycle boundary: draft,
  new, ready, running, idle, sleeping, warning, and recoverable failed sessions
  are eligible only without an active workflow. Transitional, archived,
  destructive, and deleted states fail closed.
- Oversized collection errors tell the user to split the collection into at
  most 50 direct items. Architecture and recovery documentation now reflects
  the three-call maximum metadata path and the lifecycle matrix.
- **17.9.2:** Bootstrap, wake/restore, and the reserved live command mode now
  share one target-aware `sync_workshop_content` function. It accepts all,
  mission, or mod targets, retains bounded item retries and the existing host
  and Steam authorization locks, checks disk headroom, and writes one redacted
  per-item result manifest to the existing session S3 prefix.
- **17.9.3:** SteamCMD Workshop downloads use a workflow-scoped private Steam
  library. Scenario identity, canonical filename, expected size, safe path,
  payload shape, and checksum remain enforced before S3 snapshot and atomic
  `mpmissions` placement. Mods are validated for item-ID path, symlinks, and
  the 20-GiB item bound, then copied into client/server revision-owned trees.
  Bootstrap/wake/restore may promote those trees; live command mode is
  stage-only and cannot change active links, mod argument files, launch
  arguments, mission selection, or services.
- **17.9.4:** Stable running/idle resolutions can acquire a deterministic,
  replay-safe `WorkshopContentSync` lease and dispatch one stage-only SSM
  command. Its ID and deadline use the existing workflow record. One terminal
  SSM EventBridge rule invokes the artifact worker, while the existing
  reliability schedule observes missed terminal events. Callback identity is
  restricted to the exact platform comment, workflow, command, and instance.
- **17.9.5:** Wake's existing mod state is now `DispatchContent`, retaining its
  state count and 30-second observation cadence. It synchronizes Workshop
  missions without requiring a pending mod revision and promotes applying mods
  before health verification. Bootstrap and restore retain the shared host
  content-sync function.
- **17.9.6:** Create/setup keeps Workshop resolution private and defers host
  work to initial bootstrap. Stable running/idle edits dispatch live sync;
  sleeping and pre-runtime sessions remain queued for their next lifecycle
  content pass, while locked/transitional states reject safely. Detailed status
  now distinguishes queued, downloading/validating, available, awaiting
  restart, failed, and bounded excluded-child summaries without exposing titles.
- **17.9.7:** Content workflows persist the exact target, resolution digest,
  instance, command ID, and deadline. Replay identity binds the request and
  digest; terminal callbacks recheck workflow, instance, and current snapshot.
  Timed-out commands are cancelled before their lock is released, publisher
  timestamp drift is rejected, stale staging older than one day is removed
  through a constrained path, and all exit paths clean current staging and
  ephemeral result files. Stable redacted failures now cover disk capacity,
  visibility, removed items, publisher drift, Steam authorization, timeout,
  and individual item failures with specific recovery actions.
- Post-review recovery closes three interruption gaps before 17.9.8: queued
  metadata resolution now has a durable target/request marker cleared on
  success or terminal rejection; trusted callbacks and the scheduled scan can
  recover an SSM command accepted before its command-ID write; and wake-time
  mod promotion restarts and verifies Arma in the same host command before the
  existing health gate. Mission-only synchronization remains restart-free.
- **17.9.8:** The complete runtime and infrastructure diff was reviewed. One
  EventBridge rule, target, and Lambda permission were added; the wake state
  machine retains its state and polling count, and no worker, queue, table,
  bucket, GSI, schedule, NAT path, or polling Step Function was added. Because
  EventBridge cannot filter by SSM comment, terminal `AWS-RunShellScript`
  events cause one bounded artifact-worker invocation and ownership lookup;
  unrelated commands are rejected before session mutation. Scheduled recovery
  now has explicit missed-event and missing-command-ID coverage.
- Cost and performance remain bounded by the 50-child collection ceiling, one
  SSM command per requested synchronization, existing 15-minute recovery scan,
  six-hour command deadline, per-host file lock, disk headroom checks, and
  revision-owned staging. Mod staging temporarily duplicates downloaded data;
  mission copies and ordinary session management do not restart Arma.

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
- Live Steam verification confirmed `file_size` is encoded as a JSON string.
  The metadata adapter now accepts both string and numeric non-negative values;
  focused coverage uses the live response shape. This prevents a retrying
  mission resolution from blocking the same session's mod resolution in FIFO.
- Successful Workshop resolution no longer posts a standalone channel message.
  `/rb status` reports an accepted mission source as downloading on the next
  start, while mod revisions remain visible through their existing active or
  pending status. Asynchronous ephemeral follow-ups are intentionally not used
  because the worker does not retain short-lived interaction tokens. Rejection
  notices remain actionable so user-correctable failures are not silently lost.
- New scenario resolutions retain Steam's decoded, normalized canonical PBO
  filename and expected size in the immutable digest and source snapshot.
  Bootstrap accepts exactly one regular PBO or numeric `*_legacy.bin`, verifies
  its resolved size, and stages it under the canonical name without trusting a
  mutable Workshop title. Pre-fix records and publisher changes require an
  explicit link resubmission, which `/rb status` now explains. Stable scenario
  payload failure codes keep raw host output private while providing the exact
  user recovery action.
- Workshop mods need no equivalent rename: their existing path mounts the
  downloaded item directory, and resolver classification prevents scenario
  payloads from entering client-mod revisions.

## Validation

- Changed Go files pass `gofmt`; unrelated files were not rewritten.
- `go test ./...`, `go vet ./...`, and `go build ./cmd/...` pass with
  repository-local Go caches.
- All Lambda archives package successfully.
- Bootstrap Bash syntax through Git Bash and focused SteamCMD/bootstrap,
  lifecycle, collection-boundary, staging, result-manifest, and failure-mapping
  tests pass.
- Discord command registration and interaction contract tests pass; command
  definitions did not change, so re-registration is unnecessary.
- Terraform recursive format and development-environment validation pass.
- `git diff --check` passes. The Windows checkout reports only expected LF/CRLF
  conversion warnings.

## Proposed Pull Request

Title: `feat: add Steam Workshop content sources`

Summary: add bounded item/collection resolution for Arma 3 scenarios and client
mods, immutable provenance and generated artifacts, live and lifecycle-aware
SSM synchronization, existing mission/mod revision integration, per-item
authenticated SteamCMD safeguards, actionable Discord recovery messages,
metadata batching, persistence bounds, missed-event recovery, and a verified
wake-time mod restart. Infrastructure adds one terminal SSM EventBridge rule
and the SSM lookup/cancellation permissions required by the existing artifact
and reliability workers; no persistent service or polling state machine is
added.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"

./scripts/package-discord-lambda.ps1
$PlanFile = "workshop-content-sync-recovery-$((Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')).tfplan"
terraform -chdir=infra/terraform/environments/dev plan -out $PlanFile
terraform -chdir=infra/terraform/environments/dev show $PlanFile
# Apply only after confirming the reviewed plan updates the affected Lambda
# packages, bootstrap script object, wake definition, SSM cancellation IAM, and the
# single terminal SSM EventBridge rule, with no unrelated infrastructure.
terraform -chdir=infra/terraform/environments/dev apply $PlanFile
```

No Discord command registration is required because command definitions did
not change.
