# Current Work

## State and Objective

Phase 17.23 is complete on `codex/workshop-content-sources`. Live verification
of `test-43` found and corrected the fixed five-minute completion tail on
authenticated Steam operations.

## Live Finding and Correction

- Test-43 downloaded, validated, and promoted Workshop item `705986840`, but
  its SSM command remained in progress for `5m 2.215s` while the authorization
  heartbeat shell waited for its `sleep 300` child to finish.
- Bash deferred the heartbeat shell's termination trap while that foreground
  child was running. Cleanup therefore retained the workflow and shared Steam
  authorization lease until the whole interval elapsed.
- Heartbeat waits now use a tracked child. The worker's signal handler stops
  and reaps that child, including the five-second renewal-retry wait, before
  exiting. Repeated starts reuse the live worker, stale job identifiers are
  reaped without signaling a reused process ID, and repeated stops remain harmless.
- The existing owner-conditioned DynamoDB lease release, two-attempt renewal,
  parent termination on renewal loss, credential scrubbing, and 300/900-second
  heartbeat-to-lease ratio are unchanged.
- This removes the artificial completion tail for live mission and mod sync as
  well as authenticated bootstrap, wake, and restore paths without adding AWS
  resources or more frequent DynamoDB writes.

## Validation

- The behavioral heartbeat regression passed 20 consecutive runs, covering
  idempotent reuse of an existing worker, normal-wait interruption, retry-wait
  interruption, and repeated cleanup within a three-second bound.
- The complete `ssmbootstrap` package tests pass.
- `git diff --check` passes.
- The bootstrap artifact passes native Git Bash syntax validation on this host.

## Deployment Attention

- This change updates only the managed-host bootstrap script object; it adds no
  AWS resources and requires no Discord command registration or Lambda package.
- Review the Terraform plan and reject any resource replacement, deletion, or
  change beyond the bootstrap object caused by this correction.

## Commands to Apply Current Changes

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
terraform -chdir=infra/terraform/environments/dev plan -out workshop-heartbeat-stop-20260905.tfplan
terraform -chdir=infra/terraform/environments/dev show workshop-heartbeat-stop-20260905.tfplan
terraform -chdir=infra/terraform/environments/dev apply workshop-heartbeat-stop-20260905.tfplan
./scripts/verify-bootstrap-worker-deployment.ps1
```
