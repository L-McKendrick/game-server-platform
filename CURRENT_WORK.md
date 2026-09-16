# Current Work

## State and Objective

Phase 19.4 is complete locally on `codex/phase-19-production-guardrails` and is
not deployed. Phase 19.3 managed-host S3 isolation remains required before
production or multi-tenant use. Do not claim a guaranteed AWS spending cap.

## Current Handoff

- Guild-scoped defaults persist separately and fall back to 30 minutes without
  players before sleep and 7 days sleeping before archive. New sessions snapshot
  the current defaults; legacy rows safely receive the same fallback values.
- `/rb admin` → **Session timeouts** lets Discord Administrators replace both
  future defaults or add positive time to both settings for a `RUNNING`/`IDLE`
  session. Current and resulting values are shown. Mutations enforce bounds,
  current state, optimistic versions, idempotent replay, and immutable reasons.
- The five-minute monitor calculates independent sleep and archive deadlines
  from the persisted timeout plus `idle_since` or `sleeping_since`. It records
  bounded one-hour/fifteen-minute owner warnings and queues the existing guarded
  workflows. Command identity binds the calculated deadline, so extensions,
  player return, state drift, or an active workflow make stale work fail closed.
- The former wall-clock maximum no longer sleeps/archives normal sessions or
  blocks start/wake/restore/restart. Its persisted clock remains only for guarded
  failed-initial-creation cleanup. Later-lifecycle failures continue to retain
  data and resources for operator action.
- Old maximum-duration Discord components now direct administrators to the new
  menu. A routing defect found during coverage was corrected so all new timeout
  buttons, selects, and modal submissions reach the admin handler.
- The test-62 bounded-scan defect is fixed locally. Autocomplete, `/rb list`,
  admin timeout and repair selectors, and legacy public-card claim backfill all
  use one paginated guild/state repository contract. The old `ListByGuild`
  operation and its bounded mixed-table scan are removed.
- Session metadata now writes sparse `gsi2pk`/`gsi2sk` guild, lifecycle-state,
  update-time, and immutable-ID keys through the same transactional session put
  used by create and every versioned update. State changes therefore move the
  DynamoDB index entry atomically with authoritative metadata. Other entity
  types omit the keys, and no mutable active-session aggregate was introduced.
- Terraform defines the `gsi2` all-attributes index and grants the Discord
  execution role access to its ARN.
- The storage-independent `GuildSessionRepository` accepts an explicit state
  set and opaque page cursor. DynamoDB queries every requested state range,
  merges candidates by descending update time and immutable ID, and advances
  only consumed streams. Cursors are bound to the guild and normalized state
  set. The in-memory adapter implements the same contract, including more than
  100 sessions and deterministic pagination.
- Before a strongly read `READY` marker exists, new readers use an explicit
  strongly consistent exhaustive-scan compatibility path with deterministic
  pagination and no arbitrary record ceiling. They never treat partial GSI
  results as complete. Compatibility cursors remain on that path through a
  mid-request cutover; new requests query `gsi2` after verification.
- `cmd/guild-session-index` performs paginated conditional backfill, exhaustive
  source-versus-index verification by guild and state, and explicit cutover.
  Backfill updates require the scanned version, lifecycle state, and update
  timestamp, so concurrent authoritative writes win and a replay projects the
  current row. Cutover reruns verification and refuses to write `READY` on any
  missing key, total mismatch, or per-range mismatch.
- Every indexed candidate is strongly reread from the primary session key
  before guild isolation, owner authorization, state eligibility, search, and
  display limits. Stale, moved, missing, cross-guild, or duplicate candidates
  are dropped. Timeout selection queries `RUNNING` and `IDLE` together so one
  state cannot consume the 25-option Discord limit before the other is
  considered. Exact immutable-ID and slug claims remain point lookups.
- Review coverage includes more than 1,000 mixed records, more than 100 guild
  sessions, single/multi-state pagination, deterministic ordering, state
  movement, backfill replay/conflicts, partial-cutover refusal, guild/owner
  isolation, post-authorization limits, stale-candidate revalidation,
  public-card legacy claims, and a Terraform GSI/IAM contract.
- Focused domain, service, DynamoDB atomic-write, Discord authorization/replay,
  workflow, monitoring, projection, migration, and stale-command tests pass.
  `go test -count=1 ./...`, `go test -cover ./...`, `go vet ./...`,
  `go build ./cmd/...`, Lambda packaging, and Terraform formatting/validation
  pass. The race detector was not run because this Windows host has CGO
  disabled and no C compiler; CI remains the required race check. The bootstrap progress
  sampler regression now waits for its slow uploader to be fully established,
  removing a load-sensitive test race without changing runtime behavior. No AWS
  mutation or live Discord registration occurred.

## Important Operator Attention

- Lifecycle timeouts reduce unattended cost but do not guarantee an AWS spending
  cap. Monitor, queue, workflow, or cleanup failures can retain billable resources;
  keep AWS Budget alarms and operator review active.
- Deploy only when no Steam-authenticated lifecycle operation or broker exchange
  is active. Wait for active operations before deployment.
- Test-58 may retain a billable instance and volume. Inspect it before retrying.
- Test-60 (`ref_f735121a5ef9`) completed host bootstrap but was marked failed by
  the previously corrected broker-output path. Reconcile or terminate retained
  resources through the guarded operator workflow after deployment.
- Test-61 (`01M2GKV5ME3MG97V0FY5894M0V`) retains its earlier deadline state. Use
  the new session-timeout controls only after this slice is deployed.
- Test-62 (`01M2KQZRR48XDKK0KZG6579EWB`) remains the live acceptance case after
  deployment and cutover: confirm it appears in the timeout menu while still
  `RUNNING`, then reopen the menu after any state change to verify authoritative
  eligibility.
- Do not run guild-session index cutover until Terraform reports `gsi2` as
  `ACTIVE`, the session writers containing the `gsi2` projection are deployed,
  backfill completes without conflicts, and standalone verification matches.
  A conflict is safe but requires rerunning backfill before verification.
- Cross-session managed-host S3 access remains an accepted supervised-development
  risk scheduled for Phase 19.3.

## Commands to Apply Current Changes

Run only after separately approving deployment and confirming no lifecycle or
Steam broker exchange is active:

```powershell
$Workspace = (Resolve-Path ".").Path
$env:GOCACHE = Join-Path $Workspace ".cache/go-build"
$env:GOMODCACHE = Join-Path $Workspace ".cache/go-mod"
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"

go test ./...
go vet ./...
go build ./cmd/...
terraform fmt -check -recursive infra/terraform
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev init -backend-config=backend.hcl -input=false
terraform -chdir=infra/terraform/environments/dev validate
terraform -chdir=infra/terraform/environments/dev plan -out=phase-19-4-authoritative-guild-discovery.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-19-4-authoritative-guild-discovery.tfplan
terraform -chdir=infra/terraform/environments/dev apply phase-19-4-authoritative-guild-discovery.tfplan

aws dynamodb describe-table --table-name game-server-platform-dev-metadata --query 'Table.{Status:TableStatus,GSI:GlobalSecondaryIndexes[?IndexName==`gsi2`].{Name:IndexName,Status:IndexStatus}}'
go run ./cmd/guild-session-index -mode backfill -table game-server-platform-dev-metadata -region us-west-2
go run ./cmd/guild-session-index -mode verify -table game-server-platform-dev-metadata -region us-west-2
go run ./cmd/guild-session-index -mode cutover -table game-server-platform-dev-metadata -region us-west-2
aws dynamodb get-item --table-name game-server-platform-dev-metadata --key '{"pk":{"S":"SYSTEM#GUILD_SESSION_INDEX"},"sk":{"S":"CUTOVER"}}' --consistent-read --projection-expression '#status,verified_at,source_count,indexed_count' --expression-attribute-names '{"#status":"status"}'
aws dynamodb get-item --table-name game-server-platform-dev-metadata --key '{"pk":{"S":"SESSION#01M2KQZRR48XDKK0KZG6579EWB"},"sk":{"S":"METADATA"}}' --consistent-read --projection-expression 'session_id,guild_id,lifecycle_state,gsi2pk,gsi2sk'
```

No Discord command registration is required because command definitions did
not change. Never reuse this saved plan after source, variables, credentials,
or remote state change; create and review a fresh plan instead.
