# Current Work

## State and Objective

Phase 19.1 and 19.2, including their branch review corrections, are complete on
`codex/phase-19-production-guardrails` but are not deployed. Phase 19.3
(managed-host S3 isolation) remains before production or multi-tenant use. Do
not claim a guaranteed AWS spending cap.

## Current Handoff

- The trusted Steam authorization broker now uses exchange-specific lease
  ownership and replay-safe prepare, promotion, reauthorization, and cleanup.
  It reconstructs a missing exchange input from the authoritative secret,
  recognizes an ambiguously successful secret promotion, rejects changed
  authorization identity, validates object-deletion results, and permits safe
  reacquisition of failed exchanges. Successful SteamCMD authentication is
  propagated across isolated shell helpers with a root-owned ephemeral marker,
  so the parent returns the cache while a nested Guard failure still clears
  validity and the Steam user cannot forge promotion evidence.
  Broker DynamoDB access is restricted to the cache lease and exchange
  keyspaces.
- Persisted 24-hour default, 1–168-hour configured bounds, immutable first
  provisioning clock, current deadline, and warning marker. Legacy rows remain
  unstarted on read. Extensions accept whole hours and keep persisted duration
  and deadline consistent.
- Exact session slugs entered in `/rb admin` duration forms now resolve through
  the durable guild slug claim instead of the first 100 results from a bounded
  guild listing. This fixes test-61 in guilds with more than 100 records;
  genuine missing sessions now receive specific input guidance instead of a
  misleading stale-control response. Legacy sessions without slug claims keep
  the bounded compatibility fallback.
- Administrator-only `/rb admin` duration modals configure a draft/new session
  or extend a started deadline (up to seven days from its original start).
  Mutations require a reason, use versioned idempotent writes, create immutable
  audit events, and notify only the owner by mention.
- The existing five-minute monitor pages through all candidates, continues
  after per-session failures, and reports aggregated errors after processing.
  It warns at one hour and fifteen minutes; expired running/idle sessions queue
  sleep, then sleeping sessions queue archive. Warning intent is durably
  recorded before enqueue; failed enqueue retries on the next pass using a
  deterministic notification ID. The notification worker revalidates the
  current guild, channel, and owner before mentioning anyone. An ambiguous
  enqueue success followed by failed acknowledgement can repeat a warning, but
  cannot silently lose it. The command worker revalidates deadline, state, and
  lock so stale queued actions fail closed. Expired sessions cannot start or
  wake.
- A failed initial provisioning/bootstrap session warns its owner before the
  deadline and queues the existing termination workflow at expiry. It uses
  exact deadline/state binding; stale commands, later lifecycle failures, and
  active workflow locks cannot trigger automatic deletion. Existing verified
  resource cleanup, capacity release, and retained failure truth are reused.
  Later-lifecycle failures with retained resources receive operator attention
  rather than automatic data loss. See `docs/phase-19-maximum-duration.md`.
- Focused broker, policy, persistence, admin authorization, warning/retry,
  extension, stale-command, workflow, and state-matrix tests pass. The bootstrap
  progress sampler now interrupts an in-flight S3 uploader promptly during
  workflow shutdown, with a load-tolerant regression. `go test -count=1 ./...`,
  `go vet ./...`, `go build ./cmd/...`, Lambda packaging, Terraform
  formatting, and Terraform validation completed successfully on this host.
  No AWS mutation, deployment, command registration, or live-session test was
  performed. See the Phase 19 operator documents for deployment checks.

## Important Operator Attention

- Maximum duration is **not a guaranteed cost cap**. Later-lifecycle failures
  or incomplete termination can retain billable infrastructure; default action
  is manual operator inspection and AWS billing alarms.
- Monitor scans now cover all pages, so very large metadata tables may increase
  scan work per pass. Default action is to review monitor runtime after deployment.
- Deploy only when no Steam-authenticated lifecycle operation or broker exchange
  is active; the review tightened the exchange record and lease-owner contracts.
  Default action is to wait for active operations to finish before deployment.
- Test-58's retained instance and volume may remain billable. Do not retry it
  until the Phase 19.1 Steam exchange correction is confirmed deployed.
- Test-60 (`ref_f735121a5ef9`) completed its host bootstrap but was marked
  failed because the parent shell skipped the broker output upload. Its
  `c7i-flex.large` instance and two volumes were still running when inspected.
  Default action is to avoid another creation attempt, deploy this correction,
  then reconcile or terminate the retained test resources through the existing
  guarded operator workflow.
- Test-61 is running with its original 24-hour deadline at
  `2026-09-15T18:46:03Z`. Two failed duration submissions at 18:58 UTC resolved
  `test-61` as not found because it fell beyond the first 100 guild records;
  neither attempt changed its deadline. Default action is to deploy this
  correction before using the slug again. Until then, its immutable session ID
  `01M2GKV5ME3MG97V0FY5894M0V` bypasses the affected slug fallback.
- Cross-session managed-host S3 access is an accepted supervised-development
  risk scheduled for Phase 19.3 before production or multi-tenant use.

## Commands to Apply Current Changes

No deployment or Discord re-registration has been performed. Command
definitions did not change, so re-registration is not required. Only after
separate approval to deploy the reviewed development plan, run from the
repository root:

```powershell
./scripts/package-discord-lambda.ps1
aws login --profile game-server-dev --region us-west-2
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity
terraform -chdir=infra/terraform/environments/dev init -backend-config backend.hcl -input=false
$phase19Plan = "phase-19-test61-admin-slug-fix-$(Get-Date -Format 'yyyyMMdd-HHmmss').tfplan"
terraform -chdir=infra/terraform/environments/dev plan -out $phase19Plan
terraform -chdir=infra/terraform/environments/dev show $phase19Plan
```

Review the fresh plan for intended Lambda/package changes, especially
Discord interactions, bootstrap, restore, sleep/wake, reliability, artifact,
monitor, command, and notification workers plus the broker IAM restriction;
stop for unexpected replacement, deletion, budget/provisioning drift, or
sensitive output. Ensure no Steam-authenticated lifecycle operation or broker
exchange is active. Apply **only that exact reviewed plan** after approval:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $phase19Plan
```

Then perform the operator checks in
`docs/phase-19-steam-authorization-broker.md` and
`docs/phase-19-maximum-duration.md`.
