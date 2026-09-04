# Current Work

## State and Objective

Phase 17 is complete on `codex/workshop-content-sources` and is ready for pull-request review. It adds Steam Workshop scenario and mod sources across setup, `/rb edit`, start, wake, restore, archive, status, and failure-recovery paths. No deployment or merge has been performed.

## Final Review

- Workshop URLs are canonicalized to one public numeric item ID. Collection expansion is limited to 50 direct children; nested collections are excluded rather than traversed.
- Metadata resolution uses at most two Steam API requests for either an item or collection and returns deterministic failures immediately. Result detail is ephemeral through the initiating interaction or `/rb status`; the public card receives only bounded state summaries and no standalone global or DM messages are sent.
- Scenario manifests now require exactly the pending immutable source snapshots. Partial collection refreshes work, intentionally removed scenarios stay removed, and a later explicit refresh becomes pending again.
- Live mod staging is revision-owned and atomically promoted. Wake reuses an unchanged, bounded, non-symlink staged tree with a matching publisher snapshot marker, avoiding a second SteamCMD download; missing or invalid staging is repaired through the normal bounded download path.
- Terminal SSM callbacks validate the platform command identity and session/workflow/digest binding. Duplicate or stale terminal events are ignored, while transient AWS failures retain bounded retry behavior.
- Archive restore validation now applies aggregate history limits and validates every persisted Workshop mission and mod source.

## Architecture and Impact

- The implementation reuses the artifact FIFO queue, artifact worker, SSM, one filtered EventBridge terminal-status rule, S3 session prefixes, and the existing wake state machine. It adds no Workshop Step Function, table, bucket, schedule, poller, or per-item AWS resource.
- Steady-state metadata cost is at most two Steam API calls per submitted source. AWS overhead is one queue delivery for resolution and, only when content is pending, one SSM command plus terminal EventBridge delivery. Unchanged wakes skip Workshop synchronization.
- Host inputs are numeric or bounded enums/revisions, files are staged outside the active tree, symbolic links and size violations are rejected, filenames are normalized, and promotion is atomic.
- The EC2 game instance still uses the shared environment instance role with session-prefix IAM scope rather than per-session temporary credentials. The control plane validates session/workflow bindings; per-session credentials remain a durable Phase 16 hardening item.

## Validation

- `go test ./...` passes using workspace-local Go caches.
- `go vet ./...` and `go build ./cmd/...` pass.
- Lambda packaging passes.
- `terraform fmt -check -recursive infra/terraform` and `terraform -chdir=infra/terraform/environments/dev validate` pass. A backend-disabled init attempted without AWS credentials reported the expected S3 credential error; the already initialized configuration still validated successfully.
- `git diff --check` passes with only expected Windows LF/CRLF conversion warnings.
- Focused tests cover collection request bounds, partial scenario refresh, removal/refresh semantics, stale callbacks, archive limits, editor projection, and staged-mod reuse. Bash is unavailable on this Windows host, so native `bash -n` was not run; the bootstrap contract tests pass and CI remains the shell-parser gate.

## Deployment Attention

- Review the fresh Terraform plan for the expected EventBridge rule/target/permission, scoped IAM additions, Lambda package/configuration updates, SQS visibility adjustment, bootstrap object revision, and wake state-machine definition update. Do not accept unrelated replacements or deletes.
- After deployment, canary both a single Workshop scenario and a direct-child mod collection through setup and `/rb edit`, then verify sleep/wake does not redownload unchanged content.
- Discord application command definitions did not change, so command re-registration is not required.

## Commands to Apply Current Changes

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=workshop-content-sources-release-20260903.tfplan
terraform -chdir=infra/terraform/environments/dev show workshop-content-sources-release-20260903.tfplan
terraform -chdir=infra/terraform/environments/dev apply workshop-content-sources-release-20260903.tfplan
./scripts/verify-bootstrap-worker-deployment.ps1
aws lambda get-function-configuration --function-name game-server-platform-dev-artifact-worker --query '{State:State,LastModified:LastModified,CodeSha256:CodeSha256}' --output table
aws lambda get-function-configuration --function-name game-server-platform-dev-sleepwake-worker --query '{State:State,LastModified:LastModified,CodeSha256:CodeSha256}' --output table
aws lambda get-function-configuration --function-name game-server-platform-dev-discord-interactions --query '{State:State,LastModified:LastModified,CodeSha256:CodeSha256}' --output table
```
