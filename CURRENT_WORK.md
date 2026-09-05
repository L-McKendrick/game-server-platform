# Current Work

## State and Objective

Phase 17.22 is complete on `codex/workshop-content-sources`. Live diagnosis of
`test-43` found and corrected the target-contract mismatch that prevented a
running session from synchronizing an accepted Workshop mission.

## Live Finding and Correction

- Workshop item `3641132830` resolved successfully as an Arma 3 multiplayer
  scenario and was stored as an accepted mission source.
- Workflow `wsync-d8a52ec7a750aba5451c66c1` dispatched the canonical domain
  target `mission`, but the host script accepted only the unintended plural
  spelling `missions`. It failed before SteamCMD ran and was consequently
  presented as the generic `ERR_WORKSHOP_ITEM_DOWNLOAD` failure.
- The bootstrap contract now accepts and routes `mission`, matching the domain,
  workflow record, SSM dispatcher, and result-manifest target end to end.
- Wake/bootstrap target `all` and mod target `mods` are unchanged.
- The Tanoa source remains accepted but unresolved on `test-43`; after this
  bootstrap object is deployed, resubmitting the same link will create a new
  live synchronization attempt without changing the current mission.

## Validation

- Focused `ssmbootstrap` dispatch and bootstrap-artifact contract tests pass.
- The `workshopcontent` package tests pass.
- `git diff --check` passes.
- Bash is unavailable on this Windows host, so CI remains the native `bash -n`
  parser gate.

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
terraform -chdir=infra/terraform/environments/dev plan -out=workshop-mission-target-20260905.tfplan
terraform -chdir=infra/terraform/environments/dev show workshop-mission-target-20260905.tfplan
terraform -chdir=infra/terraform/environments/dev apply workshop-mission-target-20260905.tfplan
./scripts/verify-bootstrap-worker-deployment.ps1
```
