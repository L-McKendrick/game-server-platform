# Current Work

## State and Objective

Phase 17.21 is complete on `codex/workshop-content-sources`. Live inspection of
`test-42` proved that Workshop mod files were downloaded and passed to Arma's
`-mod` argument, but restrictive revision-parent permissions prevented the
unprivileged game service from reading them.

## Live Finding and Correction

- `test-42` stored CBA_A3 item `450814997` under the active client revision,
  with 71 files and the expected PBO content.
- Its active process included `-mod=@workshop_450814997`, while the Arma log
  reported that alias as `NOT FOUND (Empty)` and a `steam`-user filesystem
  check returned `Permission denied`.
- The bootstrap `umask 077` made `workshop/mod-revisions` and its revision
  directory `root:root` mode `0700`. Item ownership alone could not make the
  target reachable through the active symlink.
- Revision parents are now explicitly `root:steam` mode `0750`. Arma receives
  read/traverse access while the service remains unable to replace revision
  directories. Every parent is rejected when symbolic before permissions are
  applied.
- The correction is shared by client and server-only Workshop revision paths.

## Naming Decision

Workshop payloads and uploaded-preset payloads are already stored under their
numeric Steam published-file ID inside a revision. Duplicate IDs are removed
while parsing, and client IDs are removed from the server-only set. The
`@workshop_<id>` name is only a deterministic Arma-facing symlink and launch
alias, so removing that prefix would not improve deduplication and would add an
unnecessary active-path migration.

## Validation

- `go test ./...`, `go vet ./...`, and `go build ./cmd/...` pass.
- Lambda packaging, recursive Terraform formatting, Terraform validation, and
  `git diff --check` pass.
- Focused bootstrap contract coverage requires root-owned, group-traversable
  content, revisions, and per-revision parents.
- Bash is unavailable on this Windows host, so CI remains the native
  `bash -n` parser gate.

## Deployment Attention

- `test-42` returned to `SLEEPING` before the live repair could be applied. On
  its next wake, repair the existing parent modes and restart Arma, or deploy
  this bootstrap revision and submit an explicit Workshop refresh so the
  shared synchronization path repairs them.
- New and subsequently synchronized Workshop revisions receive the corrected
  permissions automatically after deployment.
- Discord command registration is not required.

## Commands to Apply Current Changes

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=workshop-mod-traversal-20260904.tfplan
terraform -chdir=infra/terraform/environments/dev show workshop-mod-traversal-20260904.tfplan
terraform -chdir=infra/terraform/environments/dev apply workshop-mod-traversal-20260904.tfplan
./scripts/verify-bootstrap-worker-deployment.ps1
```

The reviewed plan should contain only the bootstrap script object/key and
resulting bootstrap-worker configuration/package updates; reject unrelated
resource replacement or deletion.
