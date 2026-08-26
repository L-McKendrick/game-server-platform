# Current Work

## State and Objective

Phase 17.6 Workshop backend development is complete on
`codex/workshop-content-sources`. The objective is one safe metadata and
resolution boundary for Arma 3 missions and mods supplied as either a public
Workshop item or collection.

## Completed Development

- Added consolidated roadmap steps 17.6-17.8 covering one shared Workshop
  item-or-collection metadata boundary, scenario-to-immutable-mission
  resolution, and mod-to-preset-revision resolution.
- The plan defaults to immutable snapshots: accepted scenarios become
  checksum-addressed S3 mission objects, and collections become deterministic
  generated preset revisions. Publisher changes require explicit new
  resolution rather than silently changing active sessions.
- Submission context supplies the requested target (`mission` or `mods`). A
  direct item takes the shared item path; a collection expands one level and
  sends every child through that same path. Mission-targeted collections add
  accepted scenarios as mission-manager choices without changing the selected
  mission, while mod-targeted collections generate one immutable mod revision.
- Mixed collections are classified child-by-child. Items matching the requested
  target are processed and other types receive explicit per-item feedback; no
  mismatched child silently changes session content.
- The plan reuses the existing Discord, SQS, DynamoDB, S3, Steam authorization,
  SteamCMD, bootstrap, mission, preset revision, rollback, archive, and restore
  paths; it does not assume a new persistent service.
- Added versioned Workshop request, target, source, item classification,
  immutable resolution snapshot, provenance, timestamp, and deterministic
  SHA-256 contracts under `internal/domain`.
- Added a strict canonical Steam Community URL parser, Arma 3 app ID and
  collection-size bounds, normalized tag classification, mixed-collection
  handling, deterministic child ordering, and duplicate rejection.
- Added a read-only Steam Workshop catalog adapter for Valve's public published
  file and collection metadata endpoints. HTTP responses are bounded and
  rate-limit, transient, rejected, unavailable, and malformed-response failures
  have stable retry metadata without exposing response bodies.
- Added an application resolver that treats a direct item as one child or
  expands a collection once, deduplicates child IDs, classifies every child for
  the requested target, and produces the immutable resolution digest. It does
  not download, subscribe to, or install Workshop content.
- Focused domain, resolver, adapter, mixed-collection, and rate-limit tests pass.
- The existing FIFO artifact queue now carries an explicitly typed Workshop
  resolution request without weakening the Discord-CDN-only attachment
  downloader. Session ownership, guild/channel context, lifecycle state,
  idempotency, and canonical URL validation run before enqueue.
- The artifact worker dispatches Workshop messages to the metadata resolver,
  retries only typed transient/rate-limit/malformed Steam failures, and sends a
  bounded completion or permanent-rejection notification. It still performs no
  Workshop download or session content mutation.
- `/rb create` accepts either an optional mission upload or mission Workshop
  link. The mission manager accepts either a `.pbo` or scenario item/collection
  link, and mod options accept either a Launcher preset or mod item/collection
  link. Existing modal submissions remain backward compatible.

## Next Development Task

- Implement **17.7.1** only: apply the shared resolver's `mission` policy and
  prepare accepted scenario items for the later authenticated download stage.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"

./scripts/package-discord-lambda.ps1
$PlanFile = "workshop-resolution-backend-$((Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')).tfplan"
terraform -chdir=infra/terraform/environments/dev plan -out $PlanFile
terraform -chdir=infra/terraform/environments/dev show $PlanFile
# Apply only after confirming the reviewed plan updates the Discord and
# artifact-worker Lambda packages without unrelated infrastructure mutations.
terraform -chdir=infra/terraform/environments/dev apply $PlanFile
```

No Discord command registration is required because command definitions did
not change. Workshop downloads and session content mutation remain disabled
until Phases 17.7 and 17.8 are implemented.
