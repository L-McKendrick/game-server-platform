# Current Work

## State and Objective

Phase 17.11 is complete on `codex/workshop-content-sources`. Live verification
of `test-33` showed that its host and Workshop scenario setup succeeded, but
bootstrap finalization tried to persist several mission mutations through a
transaction that permits exactly one session-version increment. The atomic
repair and an end-to-end Workshop architecture hardening pass are implemented
locally. No deployment or live session retry was performed.

## Completed Development

- Bootstrap parses the immutable Workshop mission TSV without mutating session
  state, then attaches every item/collection scenario in the same single
  mutation that completes bootstrap. Partial collections fail atomically and
  configured/current mission selection remains unchanged.
- Live synchronization, terminal EventBridge callbacks, scheduled missed-event
  recovery, and wake now use the same parser and atomic mission attachment
  contract. A successfully downloaded scenario therefore appears in the
  mission manager for initial start, `/rb edit` on a running host, and edits
  queued while sleeping.
- The shared parser checks the exact session prefix, content-addressed key,
  normalized filename, complete item set, item identity, and full recorded
  provenance. Replays are idempotent. Ordinary wakes skip the S3 read when all
  current item/provenance records are already attached.
- A live manifest that cannot be safely imported releases the workflow as
  `ERR_WORKSHOP_RESULT_IMPORT` with operator-directed status instead of leaving
  the session locked or exposing provider diagnostics.
- The existing artifact, sleep/wake, and reliability workers received only
  `s3:GetObject` on `sessions/*/workshop-resolutions/*.tsv`. No worker, queue,
  state machine, table, bucket, schedule, polling path, or session-management
  request was added.

## Architecture Review Result

- Submission and metadata resolution remain FIFO-serialized, bounded to 50
  collection children, immutable-digest bound, and private at acceptance.
- Initial bootstrap, live sync, wake, restore/replacement bootstrap, and
  EventBridge-loss recovery use the same host download implementation and the
  same mission result authority. Mods remain isolated as pending revisions
  until a controlled restart; scenarios never select or restart a mission.
- Result publication, IAM, staging cleanup, retries, command identity, session
  locks, publisher drift checks, status projection, and archive/termination
  prefix cleanup were reviewed. No additional correctness or material
  cost/performance issue was found after the fixes above.

## Validation

- `go test ./...`, `go vet ./...`, and `go build ./cmd/...` pass using
  repository-local caches.
- Focused domain, bootstrap, live-sync/recovery, wake, failure-catalog, IAM,
  manifest, and DynamoDB adapter tests pass.
- All Lambda packages build successfully.
- Bootstrap Bash syntax, recursive Terraform formatting,
  `terraform -chdir=infra/terraform/environments/dev validate`, and
  `git diff --check` pass. Windows reports only expected LF/CRLF warnings.
- Discord command definitions did not change; registration is unnecessary.

## Operator Note

`test-33` retains running instance `i-0a474c54ba6b9ec6b` and attached 100-GiB
gp3 volume `vol-0c2bd01d6a9114720`, so it may continue to incur cost. Its SSM
bootstrap, Arma health check, mission upload, resolution TSV, and result JSON
all succeeded; only the metadata completion transaction failed. After deploying
this repair, inspect `/rb status` and retry `/rb start`. The resumable bootstrap
will reuse the managed host and attach the existing immutable mission result.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"

./scripts/package-discord-lambda.ps1
$PlanFile = "workshop-atomic-finalization-$((Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')).tfplan"
terraform -chdir=infra/terraform/environments/dev plan -out $PlanFile
terraform -chdir=infra/terraform/environments/dev show $PlanFile
# Confirm the reviewed plan updates the artifact, bootstrap, sleep/wake, and
# reliability worker packages and adds only the bounded Workshop TSV reads.
terraform -chdir=infra/terraform/environments/dev apply $PlanFile
```

After deployment, inspect `test-33` with `/rb status`, then retry `/rb start`
only when no workflow is active. No Discord command registration is required.
