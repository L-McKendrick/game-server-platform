# Phase 10 Reliability Runbook

## Scope and safety model

Steps 10.1 and 10.2 add recovery controls around the existing lifecycle. They
do not change game behavior, create a new provisioning path, or authorize
routine destructive automation.

- Step Functions retry only Lambda service, SDK, and throttling failures. The
  policy is three attempts with exponential backoff, full jitter, and a
  30-second cap. Application and domain failures follow the existing terminal
  catch or rollback path.
- FIFO consumers retain partial-batch responses and the existing five-receive
  DLQ boundary.
- Cancellation is cooperative. The owner may request it, but a worker honors
  it only at the initial safe boundary before an external mutation. Archive,
  restore, provisioning, bootstrap, sleep, and wake support that boundary;
  termination remains intentionally non-cancellable.
- Scheduled reconciliation is non-destructive. It repairs expired workflow
  metadata when the matching Step Functions execution is failed, aborted,
  timed out, or missing. A succeeded execution with incomplete metadata and a
  missing workflow record are report-only findings.

## Operator actions

Invoke the reliability worker through an authenticated AWS operator session.
Every mutating action requires `requested_by` and persists an audit operation.
Replace placeholders before use.

```powershell
aws lambda invoke --profile game-server-dev --function-name game-server-platform-dev-reliability-worker --cli-binary-format raw-in-base64-out --payload '{"action":"inspect_dlq","queue":"COMMANDS","requested_by":"operator@example","correlation_id":"incident-123"}' response.json
aws lambda invoke --profile game-server-dev --function-name game-server-platform-dev-reliability-worker --cli-binary-format raw-in-base64-out --payload '{"action":"redrive_dlq","queue":"COMMANDS","requested_by":"operator@example","correlation_id":"incident-123","max_messages_per_second":10}' response.json
aws lambda invoke --profile game-server-dev --function-name game-server-platform-dev-reliability-worker --cli-binary-format raw-in-base64-out --payload '{"action":"inspect_orphans","limit":100}' response.json
aws lambda invoke --profile game-server-dev --function-name game-server-platform-dev-reliability-worker --cli-binary-format raw-in-base64-out --payload '{"action":"quarantine_orphan","finding_id":"FINDING_ID","requested_by":"operator@example"}' response.json
aws lambda invoke --profile game-server-dev --function-name game-server-platform-dev-reliability-worker --cli-binary-format raw-in-base64-out --payload '{"action":"cleanup_orphan","finding_id":"FINDING_ID","requested_by":"operator@example"}' response.json
```

Inspect the response and current queue depth before redrive. SQS starts a
managed move task back to the queue that owns the DLQ. Repeated operator
requests are recorded, and normal consumer idempotency remains the final replay
guard. Stop and investigate if the DLQ alarm immediately returns.

## Orphan handling

The scheduled scan correlates authoritative session metadata with
project/environment-tagged EC2 instances, EBS volumes, security groups, and
session S3 prefixes. The platform currently creates no per-session schedules;
schedule inventory must be added when that resource class is introduced.

An EC2 instance or detached EBS volume becomes actionable only when all of the
following are true:

1. `Project`, `Environment`, and immutable `SessionId` tags are present and
   match the configured environment.
2. The resource is absent from the session's authoritative references, or its
   session is absent or terminal.
3. The observation is at least 24 hours old.
4. The persisted finding is quarantined for another 24 hours.
5. Tags, attachment state, session state, references, and age are revalidated
   immediately before cleanup.

Malformed tags, attached volumes, shared security groups, S3 prefixes, and all
uncertain correlations are report-only. Automated cleanup never deletes S3
objects or security groups. Quarantine is reversible metadata; instance or
volume cleanup is destructive after the second operator action.

## Disaster recovery

### DynamoDB metadata

1. Stop workflow writers and record the incident time, table ARN, and latest
   known-good timestamp.
2. Restore point-in-time recovery into a new table; never overwrite the source.
3. Compare item counts by entity type and sample sessions, workflows, events,
   idempotency records, orphan findings, and DLQ operations.
4. Run reconciliation in inspect/report mode against the restored data.
5. Update configuration through a reviewed deployment, then verify reads before
   re-enabling writers. Roll back by restoring the prior table configuration.

### S3 assets and archives

1. Preserve bucket versioning and identify exact object versions from metadata
   and archive manifests.
2. Restore deleted or damaged objects by copying known-good versions to new
   versions. Do not permanently delete recovery evidence.
3. Verify object size, stored SHA-256 values, archive manifest checksums, and a
   test extraction before making an archive eligible for restore.

### Terraform state

1. Disable deployment automation and preserve the current state object/version.
2. Recover the selected version into a separate key or workspace and run
   `terraform plan` with the approved variables.
3. Review every proposed create, replace, and destroy. Do not run
   `terraform apply` as part of recovery validation.
4. Promote the recovered state only through the normal reviewed deployment
   process, retaining the prior version for rollback.

### Workflows and retained volumes

1. Compare active workflow metadata with `DescribeExecution` and run the
   reconciliation operator action. Do not infer success from a missing
   execution.
2. Re-run only the normal idempotent lifecycle command after reconciliation;
   never edit a workflow directly to claim a mutation completed.
3. For retained EBS data, attach a clone or snapshot-derived volume to a
   recovery instance, verify filesystem integrity and session identity, then
   use the normal restore and health acceptance path.

For every recovery, retain the incident timeline, selected recovery points,
AWS request IDs, before/after counts, checksums, Terraform plan, reconciliation
findings, health results, operator identity, and rollback decision.

## Validation boundary

Focused Go tests cover retry bounds, cancellation authorization and safe
boundaries, reconciliation decisions, DLQ audit state, orphan evidence,
quarantine, and cleanup gates. Terraform validation covers the scheduled
worker, least-privilege boundaries, redrive allow policies, and alarms. Live
redrive, quarantine, cleanup, restore, or Terraform apply requires a separate
operator-approved exercise.
