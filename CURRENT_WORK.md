# Current Work

## State and Objective

Phase 20 development is paused after 20.2 on
`codex/phase-20-production-hardening`, as requested. Restore correctness and
archived `/rb start` routing are implemented and offline-validated; the live
archive/restore acceptance gate in 20.1.7 remains pending. The Phase 20 security
review is complete. Do not begin 20.3 OIDC work without new user direction.

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
- The test-44 restore correction is not live-accepted. A live exercise creates
  billable replacement EC2/EBS resources and may affect a session. Default: do
  not deploy or run it without explicit approval for a disposable archived
  session.

## Commands to Apply Current Changes

Run from the repository root. Lambda archives were packaged during validation.
Create and review fresh plans for both state-bootstrap security and the
development platform; preserve all existing user-owned plan files.

```powershell
$env:AWS_PROFILE = "game-server-dev"
$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$phase20BootstrapPlan = "phase20-tls-state-$timestamp.tfplan"
$phase20DevPlan = "phase20-restore-security-$timestamp.tfplan"
terraform -chdir=infra/terraform/bootstrap plan "-out=$phase20BootstrapPlan"
terraform -chdir=infra/terraform/bootstrap show $phase20BootstrapPlan
terraform -chdir=infra/terraform/environments/dev plan "-out=$phase20DevPlan"
terraform -chdir=infra/terraform/environments/dev show $phase20DevPlan
```

After approving both exact plans, in the same PowerShell session:

```powershell
terraform -chdir=infra/terraform/bootstrap apply $phase20BootstrapPlan
terraform -chdir=infra/terraform/environments/dev apply $phase20DevPlan
aws lambda get-function-configuration --function-name game-server-platform-dev-restore-worker --region us-west-2 --query "{State:State,LastUpdateStatus:LastUpdateStatus,RevisionId:RevisionId}" --output table
$workflowArns = terraform -chdir=infra/terraform/environments/dev output -json workflow_state_machine_arns | ConvertFrom-Json
aws stepfunctions describe-state-machine --state-machine-arn $workflowArns.RestoreSession --region us-west-2 --query "{Status:status,RevisionId:revisionId}" --output table
```

No Discord command registration is required because command definitions did
not change. After separate approval, use a disposable archived session to run
`/rb start`, confirm AWS CLI prerequisite success, archive restoration,
bootstrap/health completion, workflow-lock release, and retained-resource truth
on a forced terminal failure. Do not proceed to 20.3 after verification without
new direction.
