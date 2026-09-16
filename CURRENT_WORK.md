# Current Work

## State and Objective

Phase 19.4 is complete on `codex/phase-19-production-guardrails`, now reconciled
with the Phase 20.1/20.2 work merged to `main`. Phase 19.3 managed-host access
isolation remains the production and multi-tenant release blocker. Do not begin
Phase 20.3 without new user direction or claim a guaranteed AWS spending cap.

## Current Handoff

- Guild-scoped defaults fall back to 30 minutes without players before sleep
  and 7 days sleeping before archive. New sessions snapshot current defaults;
  legacy rows receive the same safe fallback values.
- `/rb admin` lets Discord Administrators replace future timeout defaults or
  add positive time to both deadlines for a `RUNNING` or `IDLE` session.
  Mutations enforce bounds, current state, optimistic versions, idempotent
  replay, and immutable audit reasons.
- The five-minute monitor calculates independent sleep and archive deadlines,
  records bounded owner warnings, and queues existing guarded workflows.
  Player return, extensions, state drift, and active workflow locks make stale
  work fail closed. These timeouts reduce unattended cost but are not a
  guaranteed spending cap.
- Guild session discovery now uses one paginated guild/state repository
  contract for autocomplete, `/rb list`, timeout and repair selectors, and
  legacy public-card claim backfill. The bounded `ListByGuild` mixed-table scan
  is removed.
- Session metadata atomically writes sparse `gsi2pk`/`gsi2sk` guild, state,
  update-time, and immutable-ID keys. Terraform defines the all-attributes
  `gsi2` index and grants the Discord execution role access to its ARN.
- Before the strongly read `READY` marker exists, readers use a strongly
  consistent exhaustive compatibility scan with deterministic pagination and
  no arbitrary ceiling. Compatibility cursors stay on that path through a
  mid-request cutover.
- `cmd/guild-session-index` performs conditional paginated backfill,
  exhaustive source/index verification by guild and state, and explicit
  cutover. Concurrent session changes win; cutover refuses partial or
  mismatched results.
- Every discovery candidate is strongly reread before guild isolation, owner
  authorization, state eligibility, search, limits, display, or mutation.
  Missing, moved, stale, cross-guild, and duplicate candidates are dropped.
- Phase 20 restore hardening from `main` is retained, including explicit mount
  and exact-device verification before archive access, archived `/rb start`
  routing, retained-resource retry, diagnostic redaction, and host-stage replay.
- Test-56 restore execution `1547132738950406144` succeeded on 2026-09-09; its
  disposable session was subsequently terminated and has a `DELETED`
  tombstone. Modded and TeamSpeak-enabled variants retain offline coverage and
  remain Phase 20.4 staging cases.
- Review coverage includes more than 1,000 mixed records, more than 100 guild
  sessions, deterministic pagination and multi-state merging, state movement,
  backfill replay/conflicts, partial-cutover refusal, authorization after page
  boundaries, stale-candidate revalidation, legacy claims, and the Terraform
  GSI/IAM contract.
- After reconciling with `main`, `go vet ./...`, `go build ./cmd/...`,
  `go test -count=1 ./...`, Terraform recursive formatting and development
  validation, and Lambda packaging all pass. No AWS or Discord state changed.

## Important Operator Attention

- **Production release blocker SEC-20-01 / Phase 19.3:** the shared managed-game
  instance profile can access other sessions' S3 inputs/archives and the shared
  Steam authorization cache. Keep use non-production and supervised until
  session-scoped host access is implemented and verified.
- Monitor, queue, workflow, or cleanup failures can retain billable resources;
  keep AWS Budget alarms and operator review active.
- Deploy only when no Steam-authenticated lifecycle operation or broker
  exchange is active.
- Test-58 may retain a billable instance and volume. Inspect it before retrying.
- Test-60 (`ref_f735121a5ef9`) completed host bootstrap but was marked failed by
  the corrected broker-output path. Reconcile or terminate retained resources
  through the guarded operator workflow after deployment.
- Test-61 (`01M2GKV5ME3MG97V0FY5894M0V`) retains its earlier deadline state. Use
  the new timeout controls only after this slice is deployed.
- Test-62 (`01M2KQZRR48XDKK0KZG6579EWB`) remains the live discovery acceptance
  case after deployment and cutover: confirm it appears while `RUNNING`, then
  reopen the timeout menu after a state change to verify current eligibility.
- Do not cut over indexed reads until Terraform reports `gsi2` as `ACTIVE`, the
  new writers are deployed, backfill completes without conflicts, and
  standalone verification matches.
- SEC-20-07 records that replacement hosts fetch AWS CLI v2 over TLS and check
  its major version without pinning a checksum/signature. Close it through a
  versioned host image or verified installer before production staging.
- The local race build is unavailable without a working C compiler; GitHub CI
  remains the required race gate.

## Commands to Apply Current Changes

Run only after separately approving deployment and confirming no lifecycle or
Steam broker exchange is active. Create and review a fresh plan; preserve all
existing user-owned plan files.

```powershell
$Workspace = (Resolve-Path ".").Path
$env:GOCACHE = Join-Path $Workspace ".cache/go-build"
$env:GOMODCACHE = Join-Path $Workspace ".cache/go-mod"
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$phase19Plan = "phase19-guardrails-$timestamp.tfplan"

go test ./...
go vet ./...
go build ./cmd/...
terraform fmt -check -recursive infra/terraform
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev init -backend-config=backend.hcl -input=false
terraform -chdir=infra/terraform/environments/dev validate
terraform -chdir=infra/terraform/environments/dev plan "-out=$phase19Plan"
terraform -chdir=infra/terraform/environments/dev show $phase19Plan
```

After approving that exact plan, in the same PowerShell session:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $phase19Plan
aws lambda get-function-configuration --function-name game-server-platform-dev-restore-worker --region us-west-2 --query "{State:State,LastUpdateStatus:LastUpdateStatus,RevisionId:RevisionId,BootstrapScript:Environment.Variables.BOOTSTRAP_SCRIPT_KEY}" --output table
aws dynamodb describe-table --table-name game-server-platform-dev-metadata --query 'Table.{Status:TableStatus,GSI:GlobalSecondaryIndexes[?IndexName==`gsi2`].{Name:IndexName,Status:IndexStatus}}'
go run ./cmd/guild-session-index -mode backfill -table game-server-platform-dev-metadata -region us-west-2
go run ./cmd/guild-session-index -mode verify -table game-server-platform-dev-metadata -region us-west-2
go run ./cmd/guild-session-index -mode cutover -table game-server-platform-dev-metadata -region us-west-2
aws dynamodb get-item --table-name game-server-platform-dev-metadata --key '{"pk":{"S":"SYSTEM#GUILD_SESSION_INDEX"},"sk":{"S":"CUTOVER"}}' --consistent-read --projection-expression '#status,verified_at,source_count,indexed_count' --expression-attribute-names '{"#status":"status"}'
aws dynamodb get-item --table-name game-server-platform-dev-metadata --key '{"pk":{"S":"SESSION#01M2KQZRR48XDKK0KZG6579EWB"},"sk":{"S":"METADATA"}}' --consistent-read --projection-expression 'session_id,guild_id,lifecycle_state,gsi2pk,gsi2sk'
```

No Discord command registration is required. Never reuse the saved plan after
source, variables, credentials, or remote state change.
