# Current Work

## State and Objective

Phase 19.2 is complete on `codex/phase-19-production-guardrails` but is not
deployed. Phase 19.3 (managed-host S3 isolation) remains before production or
multi-tenant use. Do not claim a guaranteed AWS spending cap.

## Current Handoff

- Persisted 24-hour default, 1–168-hour configured bounds, immutable first
  provisioning clock, current deadline, and warning marker. Legacy rows remain
  unstarted on read.
- Administrator-only `/rb admin` duration modals configure a draft/new session
  or extend a started deadline (up to seven days from its original start).
  Mutations require a reason, use versioned idempotent writes, create immutable
  audit events, and notify only the owner by mention.
- The existing five-minute monitor pages through all candidates and warns at one hour and fifteen minutes;
  expired running/idle sessions queue sleep, then sleeping sessions queue
  archive. Warning intent is durably recorded before enqueue; failed enqueue
  retries on the next pass using a deterministic notification ID. An ambiguous
  enqueue success followed by failed acknowledgement can repeat a warning,
  but cannot silently lose it. The command worker revalidates deadline, state, and lock so stale
  queued actions fail closed. Expired sessions cannot start or wake.
- A failed initial provisioning/bootstrap session warns its owner before the
  deadline and queues the existing termination workflow at expiry. It uses
  exact deadline/state binding; stale commands, later lifecycle failures, and
  active workflow locks cannot trigger automatic deletion. Existing verified
  resource cleanup, capacity release, and retained failure truth are reused.
  Later-lifecycle failures with retained resources receive operator attention
  rather than automatic data loss. See `docs/phase-19-maximum-duration.md`.
- Focused policy, persistence, admin authorization, warning/retry, extension,
  stale-command, workflow, and state-matrix tests pass. `go test -count=1 ./...`,
  `go vet ./...`, and `go build ./cmd/...` completed successfully on this host.
  No AWS mutation, deployment, command registration, or live-session test was
  performed. See `docs/phase-19-maximum-duration.md` for operator checks.

## Important Operator Attention

- Maximum duration is **not a guaranteed cost cap**. Later-lifecycle failures
  or incomplete termination can retain billable infrastructure; default action
  is manual operator inspection and AWS billing alarms.
- Monitor scans now cover all pages, so very large metadata tables may increase
  scan work per pass. Default action is to review monitor runtime after deployment.
- Test-58's retained instance and volume may remain billable. Do not retry it
  until the Phase 19.1 Steam exchange correction is confirmed deployed.
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
terraform -chdir=infra/terraform/environments/dev init -backend-config=backend.hcl -input=false
terraform -chdir=infra/terraform/environments/dev plan -out=phase-19-2-maximum-duration.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-19-2-maximum-duration.tfplan
```

Review the fresh plan for intended Lambda/package changes, especially
Discord interactions, monitor, command, and notification workers; stop for
unexpected replacement, deletion, budget/provisioning drift, or sensitive
output. Apply **only that exact reviewed plan** after approval:

```powershell
terraform -chdir=infra/terraform/environments/dev apply phase-19-2-maximum-duration.tfplan
```

Then perform the operator checks in `docs/phase-19-maximum-duration.md`.
