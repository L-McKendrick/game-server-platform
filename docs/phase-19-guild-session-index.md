# Phase 19.4 Guild-Session Discovery

Phase 19.4 replaces bounded scans of the mixed metadata table with one
storage-independent guild-and-lifecycle-state query. DynamoDB session metadata
alone populates the sparse `gsi2` index:

```text
gsi2pk = GUILD#<guild-id>
gsi2sk = STATE#<lifecycle-state>#UPDATED#<sortable-utc>#SESSION#<session-id>
```

Session create and versioned update transactions write both keys with the
authoritative metadata item. A lifecycle transition therefore removes the old
index entry and adds the new state-range entry as one DynamoDB item update.
Events, claims, policies, workflow records, and other entity types omit the
keys. There is no shared mutable collection of active session IDs.

## Query and authority

`ports.GuildSessionRepository` requires a guild, one or more explicit states,
and an opaque page cursor. The DynamoDB adapter queries every requested state
range, merges results by descending `UpdatedAt` and immutable session ID, and
advances only state streams consumed by the returned page. Cursors are bound to
the guild and normalized state set.

The GSI is eventually consistent and discovers candidates only. Application
selectors strongly reread every candidate from the primary session key before
authorization, lifecycle eligibility, search filtering, display limits, or a
mutation. Missing, cross-guild, duplicate, and state-drifted candidates are
dropped. Exact immutable-ID and guild-slug resolution remain strongly
consistent point lookups.

Autocomplete, `/rb list`, timeout selection, card repair, and the legacy
public-card control fallback use this contract. Global maintenance inventories
for inactivity, workflow reconciliation, reset, and orphan detection are not
guild queries and retain their separate exhaustive or operational contracts.

## Deployment and cutover

Indexed reads remain disabled until the strongly read marker
`SYSTEM#GUILD_SESSION_INDEX / CUTOVER` has status `READY`. Before that marker,
new code uses an explicit strongly consistent, exhaustive table-scan
compatibility path with deterministic pagination and no arbitrary record
ceiling. It never consults or accepts an empty or partially populated GSI.
Compatibility cursors remain on that path even if cutover occurs mid-request;
new requests use the verified index after cutover.

After deploying the GSI and session writers, wait for `gsi2` to become
`ACTIVE`, then run:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"

go run ./cmd/guild-session-index -mode backfill -table game-server-platform-dev-metadata -region us-west-2
go run ./cmd/guild-session-index -mode verify -table game-server-platform-dev-metadata -region us-west-2
go run ./cmd/guild-session-index -mode cutover -table game-server-platform-dev-metadata -region us-west-2
```

Backfill conditionally writes keys only when the scanned version, lifecycle
state, and update timestamp still match. Concurrent authoritative writes win;
any reported conflict requires rerunning backfill. Verification exhaustively
scans both the authoritative table and sparse index, comparing total and
per-guild/state counts and checking every source key. Cutover reruns verification
and writes `READY` only after an exact match.

Do not run these commands against live data as routine validation. They are
deployment mutations requiring a separately reviewed Terraform plan and
operator approval. No Discord command registration is required.

## Failure and rollback

- Before `READY`, selectors use the exhaustive compatibility path. Sustained
  use is more expensive than the GSI, so complete verification and cutover
  promptly after a successful deployment.
- A backfill conflict is non-destructive; rerun backfill and verification.
- A verification mismatch leaves indexed reads disabled. Investigate the
  reported guild/state ranges and do not write the marker manually.
- After cutover, removing or changing the marker disables new indexed reads.
  Roll application code back together with its compatible infrastructure; do
  not delete the GSI while deployed readers still query it.
