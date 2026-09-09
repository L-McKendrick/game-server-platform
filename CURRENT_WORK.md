# Current Work

## State and Objective

Phase 19 planning has started on `codex/phase-19-production-guardrails`, based
on the Phase 20 hardening branch. By explicit user direction, the next
development step is 19.1: remove standing managed-host access to the shared
Steam authorization cache through a workflow-scoped broker. Cross-session S3
isolation and maximum-duration enforcement are also folded into Phase 19.

## Current Handoff

- Restore and bootstrap share the fail-closed AWS CLI v2 prerequisite. Restore
  results carry terminal booleans, and the state machine rejects malformed or
  contradictory results through the idempotent failure finalizer.
- Test-52 led restore to create the existing Steam and optional TeamSpeak
  service accounts before extracted-file ownership is applied.
- Test-54 led bootstrap to accept an active matching `RestoreSession` lock
  without requiring a pending preset revision. Per user direction, do not
  recover Test-54.
- Test-55 proved both earlier corrections live, then showed restore extracting
  onto the root filesystem before bootstrap mounted the blank data volume.
  Restore now locates the exact recorded EBS device, prepares or reuses its XFS
  filesystem, rejects mismatched mounts, and mounts it before archive download
  or extraction. Temporary archive bytes remain on the data volume, and restore
  plus bootstrap recreate optional empty directories safely.
- EC2 launch now resizes the approved Ubuntu AMI root device `/dev/sda1` rather
  than creating an unused `/dev/xvda` volume. New instances retain the intended
  encrypted root and persistent data mappings.
- A failed restore with its verified archive, capacity slot, instance, and data
  volume retained can be retried through `/rb start` or `/rb restore`. The retry
  reuses those resources and the existing idempotent workflow; unrelated failed
  sessions remain ineligible. Failed pending revisions remain failed so retry
  uses the known-good active revision while manifest verification accepts the
  recorded pre-failure transition.
- Restore data-volume and bootstrap failures now have distinct codes, use the
  current progress milestone as their stage, and preserve the redacted terminal
  diagnostic instead of package-manager preamble.
- Test-56 proved the storage and root-device corrections live, then exposed an
  archived revision-qualified `deploy_content` marker suppressing creation of
  the replacement host's systemd unit. Restore now invalidates the current
  configuration-deployment and Workshop-synchronization markers as well as
  legacy install markers, so intentionally omitted host-local services and
  Workshop data are reconstructed through the existing bootstrap stages.
  `SERVICE_STARTED` is emitted only after Arma and optional TeamSpeak start.
- Reclassified SEC-20-01 from Critical to High. Standing Steam-cache access is
  the immediate credential risk. Cross-session S3 access is explicitly accepted
  for supervised development but remains a production and multi-tenant release
  blocker.
- Decomposed Phase 19 into brokered Steam authorization (19.1), maximum session
  duration (19.2), and exact-session host S3 access (19.3). Task 19.1.1 is
  complete in `docs/phase-19-steam-authorization-broker.md`; task 19.1.2 is next.
- Per user direction, do not rotate the Steam authorization cache without
  evidence of exposure; all work so far has remained internal.
- Full Go coverage tests, vet, all command builds, Lambda packaging, Terraform
  recursive formatting, and bootstrap/development validation passed. The local
  race build remains unavailable without a working C compiler and is a CI gate.
  Nothing was deployed, registered, or otherwise mutated in AWS or Discord.

## Important Operator Attention

- **SEC-20-01:** supervised development may continue. Default: remove standing
  Steam-cache access first, and do not approve production or multi-tenant use
  until Phase 19 also closes cross-session S3 access.
- The broker must not place Steam authorization material in SSM command text,
  logs, workflow payloads, Lambda configuration, or durable session artifacts.
- The combined restore correction is not live-accepted. Earlier replacement
  resources may remain billable; do not recover Test-54. Test-56 retains its
  verified archive, running instance, encrypted 100 GiB data volume, and
  capacity slot, and is eligible for explicit restore retry only after this
  correction is deployed. Its support reference is `ref_a48b8c088e0b`.

## Commands to Apply Current Changes

The Phase 19 design-only change adds no deployment or Discord registration.
The inherited Phase 20 restore correction is still undeployed. Run from the
repository root, package its affected Lambda sources, and create a fresh saved
plan while preserving every existing user-owned plan file:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$restoreReplayPlan = "restore-marker-replay-$timestamp.tfplan"
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan "-out=$restoreReplayPlan"
terraform -chdir=infra/terraform/environments/dev show $restoreReplayPlan
```

After approving that exact plan, in the same PowerShell session:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $restoreReplayPlan
aws lambda get-function-configuration --function-name game-server-platform-dev-bootstrap-worker --region us-west-2 --query "{State:State,LastUpdateStatus:LastUpdateStatus,RevisionId:RevisionId,BootstrapScript:Environment.Variables.BOOTSTRAP_SCRIPT_KEY}" --output table
aws lambda get-function-configuration --function-name game-server-platform-dev-restore-worker --region us-west-2 --query "{State:State,LastUpdateStatus:LastUpdateStatus,RevisionId:RevisionId,BootstrapScript:Environment.Variables.BOOTSTRAP_SCRIPT_KEY}" --output table
$workflowArns = terraform -chdir=infra/terraform/environments/dev output -json workflow_state_machine_arns | ConvertFrom-Json
aws stepfunctions describe-state-machine --state-machine-arn $workflowArns.RestoreSession --region us-west-2 --query "{Status:status,RevisionId:revisionId}" --output table
```

No Discord command registration is required. After the reviewed deployment,
retry Test-56 through `/rb start` and complete the documented restore checks.
Begin task 19.1.2 separately; do not alter live managed-host IAM permissions
until the brokered path and rollback behavior are implemented and validated.
