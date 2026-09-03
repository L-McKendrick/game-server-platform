# Current Work

## State and Objective

Phase 17.18 is complete on `codex/workshop-content-sources`. Workshop mod
resolution now preserves the repository's single-version transaction invariant,
and deterministic persistence failures cannot silently cycle through the
nine-minute artifact-queue visibility timeout.

## Completed Development

- `AttachWorkshopModSource` now applies the existing preset revision and
  immutable Workshop provenance operations to a copy of the session, validates
  their combined result, and publishes exactly one aggregate version change.
- Failed compound mutations leave the original in-memory session unchanged.
- Draft/new sessions still activate their first generated preset; established
  sessions still stage a pending revision. Existing lifecycle, active-revision,
  marker, collection, provenance, and refresh safeguards are unchanged.
- DynamoDB `SaveWithEvent` classifies invalid aggregate/version inputs as a
  persistence invariant without attempting a transaction.
- Artifact-worker logs every sanitized Workshop recorder failure with a terminal
  or retryable-persistence disposition. Internal invariant failures clear the
  exact pending marker immediately and direct the user to an operator through
  private status; transient external storage failures retain bounded retries.

## Live Finding and Recovery

- `test-39` metadata resolution completed in 315-627 ms. Its generated preset,
  modlist, and immutable source manifest exist under deterministic S3 keys.
- The old worker attempted to persist session version 9 from expected version 7;
  `SaveWithEvent` correctly required version 8. The message then followed the
  540-second SQS visibility retry cadence while the session retained its marker.
- Deploy this change before resubmitting the same collection. The existing
  test-39 delivery may clear itself on its fifth receive; after deployment,
  resubmitting the link safely reuses the deterministic objects and records one
  active draft revision. Check `/rb status` before resubmitting.

## Architecture Review

- No Lambda, queue, state machine, table, index, bucket, schedule, IAM permission,
  or polling transition was added.
- The fix stays within the domain aggregate and existing recorder/repository
  boundaries. Steam metadata, collection expansion, generated artifacts,
  content-sync dispatch, cards, and ephemeral status retain their original
  contracts.
- Deterministic S3 object keys keep retries idempotent and bounded; the change
  adds no per-session storage multiplication or standard-flow performance cost.

## Validation

- Added draft and established-revision single-version tests, marker cleanup,
  recorder persistence/replay coverage, repository invariant classification,
  and worker terminal-disposition/user-guidance coverage.
- `go test ./...`, `go vet ./...`, `go build ./cmd/...`, Lambda packaging,
  recursive Terraform formatting, and `git diff --check` pass. Windows reports
  only expected LF/CRLF warnings.
- Artifact-worker behavior requires deployment. Terraform and Discord command
  definitions did not change; command registration is unnecessary.

## Commands to Apply Current Changes

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=workshop-mod-atomic-persistence-20260903.tfplan
terraform -chdir=infra/terraform/environments/dev show workshop-mod-atomic-persistence-20260903.tfplan
terraform -chdir=infra/terraform/environments/dev apply workshop-mod-atomic-persistence-20260903.tfplan
aws lambda get-function-configuration --function-name game-server-platform-dev-artifact-worker --query '{State:State,LastModified:LastModified,CodeSha256:CodeSha256}' --output table
```

The reviewed plan must add and destroy no infrastructure; Lambda package updates
caused by shared Go dependencies are expected. After deployment, inspect `/rb status` for test-39;
if it is no longer resolving, submit the collection once and verify resolution
completes promptly with active mod revision 1.
