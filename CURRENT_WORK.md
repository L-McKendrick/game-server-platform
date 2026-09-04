# Current Work

## State and Objective

Phase 17.19 is complete on `codex/workshop-content-sources`. Ordinary wakes now
skip Workshop scenarios already represented by accepted immutable mission
records and skip unchanged active mod revisions.

## Live Finding

- `test-40` (`01M1MENEJCVQFZANVY3Z4BVSMG`) had one accepted Workshop mission
  and active Workshop preset revision 1 before wake.
- Wake still dispatched SSM command `1542eac4-df7f-40bc-9702-3c260af5092f`
  with the platform Workshop-sync comment. The previous predicate treated
  permanent Workshop source provenance as pending content on every wake.
- The redundant command was left unchanged and completed successfully. A final
  read-only check found `test-40` running and healthy with no active workflow.

## Completed Development

- Added a domain query that returns only Workshop scenario items without an
  accepted mission record produced at or after their latest immutable source
  resolution.
- Wake dispatch and observation now use that pending set together with the
  existing applying-preset predicate. No content command is sent when both are
  empty.
- Live/wake Workshop manifests contain only pending scenario items. Bootstrap
  and restore command generation retain the complete immutable manifest, while
  accepted mission objects continue through the existing S3 deployment path.
- Re-resolving an existing scenario remains functional: a newer source
  resolution is pending until its replacement mission snapshot is attached.
- Materialized legacy scenario sources do not block an unrelated pending mod
  revision merely because they predate canonical filename metadata.

## Architecture and Impact

- No schema migration or new persisted marker is required; the decision uses
  existing source `resolved_at` and mission `added_at` evidence.
- No Lambda, state machine, transition, queue, EventBridge rule, table, bucket,
  schedule, IAM permission, or polling behavior was added.
- Unchanged wakes avoid SteamCMD, SSM command runtime, network transfer, disk
  staging, and unnecessary wake delay. Pending edits and explicit refreshes
  retain the existing bounded synchronization and error paths.

## Validation

- Added focused domain, wake dispatch, refresh, partial-collection manifest,
  and legacy-record regressions.
- `go test ./...`, `go vet ./...`, `go build ./cmd/...`, Lambda packaging,
  recursive Terraform formatting, and `git diff --check` pass. Windows reports
  only expected LF/CRLF warnings.
- Runtime behavior requires Lambda deployment. Terraform resources and Discord
  command definitions did not change; command registration is unnecessary.

## Commands to Apply Current Changes

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=workshop-wake-pending-content-20260903.tfplan
terraform -chdir=infra/terraform/environments/dev show workshop-wake-pending-content-20260903.tfplan
terraform -chdir=infra/terraform/environments/dev apply workshop-wake-pending-content-20260903.tfplan
aws lambda get-function-configuration --function-name game-server-platform-dev-sleepwake-worker --query '{State:State,LastModified:LastModified,CodeSha256:CodeSha256}' --output table
```

The reviewed plan must add and destroy no infrastructure. Lambda package
updates caused by shared Go dependencies are expected. After deployment, let
`test-40` finish its current wake, sleep it again, then wake it without edits;
the wake should proceed from managed-node readiness to service start without a
`gsp:workshop-sync` SSM command.
