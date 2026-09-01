# Current Work

## State and Objective

Phase 17.12 is complete and deployed from `codex/workshop-content-sources`.
Live investigation
of `test-33` support reference `ref_4ce8ff3ad476` found that wake started the
retained EC2 instance successfully, then its Workshop content command failed
before doing any work because operation exports were prepended ahead of the
generated Bash shebang. AWS Run Command therefore interpreted `set -o pipefail`
with Ubuntu `dash` and rejected it.

## Completed Development

- Workshop content commands now retain `#!/usr/bin/env bash` as their first
  line and inject wake/live operation variables immediately after it.
- Wake content observation preserves a stable actionable Workshop error code
  reported by the host. Unknown terminal host failures use
  `ERR_WORKSHOP_CONTENT_<STATUS>` instead of misleadingly reporting every
  mission or mixed-content failure as a mod-revision failure.
- The failure catalog includes safe guidance for the generic
  `ERR_WORKSHOP_CONTENT_FAILED` case.
- Focused tests cover Bash interpreter placement for promoted-mod and
  mission-only content commands and actionable wake failure propagation.

## Live Session Finding

`test-33` is recoverable. Its instance `i-0a474c54ba6b9ec6b` is running, both
EC2 status checks are OK, SSM is online, and the failed wake workflow released
its lock. The session is truthfully `FAILED` because wake crossed the compute
start boundary but did not synchronize content or restart and health-check
Arma. Do not mutate DynamoDB or mark that wake successful manually.

Use `/rb start` for `test-33`. Failed sessions with retained
managed infrastructure enter the resumable bootstrap path, which will reuse
the host, synchronize the immutable Workshop mission, start Arma, verify
health, and return the session to running state. The running instance and
100-GiB gp3 volume may incur cost until recovery, sleep, archive, or termination.

## Validation

- `go test ./...`, `go vet ./...`, and `go build ./cmd/...` pass with
  repository-local caches.
- Focused SSM bootstrap, sleep/wake, and failure-catalog tests pass.
- Lambda packaging completes successfully. A concurrent local packaging rerun
  briefly collided over its generated helper; the clean serial rerun passed.
- Recursive Terraform formatting, development Terraform validation, and
  `git diff --check` pass. Windows reports only expected LF/CRLF warnings.
- Discord command definitions did not change; registration is unnecessary.
- Terraform applied the reviewed saved plan successfully: 0 resources added,
  13 Lambda functions updated in place, and 0 resources destroyed. The
  sleep/wake and bootstrap workers are active with the planned code hashes.
- Post-deployment metadata verification confirms `test-33` remains `FAILED`
  with no active workflow-lock attribute. Operator recovery is not blocked.

## Commands to Apply Current Changes

No packaging, Terraform deployment, or Discord command registration remains.
The reviewed deployment is already applied. Run `/rb status` for `test-33`,
then `/rb start` once. Confirm
the resumable bootstrap completes, the Workshop scenario remains available,
and health returns to healthy.
