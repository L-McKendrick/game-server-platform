# Current Work

## State and Objective

Phase 20 development remains limited to restore acceptance and completed step
20.2 on `codex/phase-20-production-hardening`. Test-52 and Test-54 exposed two
fresh-host restore defects; both focused corrections are implemented and
offline-validated. The live archive/restore acceptance gate in 20.1.7 remains
pending. Do not begin 20.3 OIDC work without new user direction.

## Current Handoff

- Added one shared fail-closed AWS CLI v2 prerequisite used by replacement-host
  restore and ordinary bootstrap. Restore checks the prerequisite before S3
  archive access and reports `ERR_AWS_CLI_PREREQUISITE` without raw diagnostics.
- Restore task results always carry terminal booleans. The restore state machine
  guards missing and contradictory terminal fields, records
  `ERR_RESTORE_RESULT_INVALID`, and retries the idempotent failure finalizer for
  transient Lambda failures.
- Archived `/rb start` now queues the existing owner-authorized restore command;
  `/rb restore` remains registered and supported. Help and lifecycle documents
  describe both paths.
- Test-52 restore `1546824696438464522` failed on replacement instance
  `i-0fcb93e5a1e34d129` because restore applied `steam:steam` ownership before
  bootstrap created that service account. Restore now creates the existing
  Steam and optional TeamSpeak accounts idempotently before extraction and
  ownership. No new service, IAM permission, or workflow state was introduced.
- Test-54 proved archive extraction now succeeds, then exposed that the shared
  bootstrap runner rejected an ordinary active `RestoreSession` unless a preset
  revision was pending. Bootstrap now accepts the existing `RESTORING` state
  only with a non-empty matching restore workflow lock; missing and mismatched
  locks remain rejected. Per user direction, no Test-54 recovery is planned.
- Added TLS-only S3 bucket policies for Terraform state and session assets.
  Sleep/wake EC2 start/stop permissions now require exact Project and
  Environment resource tags; wildcard access remains only for Describe APIs.
- Added `docs/threat-model/phase-20.md`, an IAM capability matrix, security
  contract tests, and explicit residual-risk ownership.
- Full Go coverage tests, vet, all command builds, Lambda packaging, Terraform
  recursive formatting, and bootstrap/development validation passed. The local
  race build remains unavailable without a working C compiler and is a CI gate.
  Nothing was deployed, registered, live-restored, or otherwise mutated in AWS
  or Discord.

## Important Operator Attention

- **Production release blocker SEC-20-01:** the shared managed-game instance
  profile can access other sessions' S3 inputs/archives and the shared Steam
  authorization cache. Default: do not approve production use until a
  session-scoped host-access design is implemented and verified.
- Phase 19 maximum-duration enforcement is still pending because Phase 20 was
  started out of order by explicit approval. Default: keep use non-production
  and manually supervised until Phase 19 is complete.
- The combined restore correction is not live-accepted. Test-52 and Test-54
  replacement resources may remain billable. Per user direction, do not recover
  Test-54; deploy through a fresh reviewed plan and validate on a future
  disposable archived session.

## Commands to Apply Current Changes

Run from the repository root. Lambda archives were packaged during validation.
The shared bootstrap adapter changed for the existing restore-worker path;
preserve all existing user-owned plan files.

```powershell
$env:AWS_PROFILE = "game-server-dev"
$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$futureRestorePlan = "future-restore-bootstrap-guard-$timestamp.tfplan"
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan "-out=$futureRestorePlan"
terraform -chdir=infra/terraform/environments/dev show $futureRestorePlan
```

After approving that exact plan, in the same PowerShell session:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $futureRestorePlan
aws lambda get-function-configuration --function-name game-server-platform-dev-restore-worker --region us-west-2 --query "{State:State,LastUpdateStatus:LastUpdateStatus,RevisionId:RevisionId}" --output table
$workflowArns = terraform -chdir=infra/terraform/environments/dev output -json workflow_state_machine_arns | ConvertFrom-Json
aws stepfunctions describe-state-machine --state-machine-arn $workflowArns.RestoreSession --region us-west-2 --query "{Status:status,RevisionId:revisionId}" --output table
```

No Discord command registration is required. Do not retry Test-54. On a future
disposable archived session, confirm restore extraction, ordinary bootstrap and
health completion without a pending preset, workflow-lock release, and truthful
retained-resource state. Do not proceed to 20.3 without new direction.
