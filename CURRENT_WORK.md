# Current Work

## State and Objective

Phases 1-9 are complete. Phases 10 and 11 remain pending. Phase 12 is
proceeding before them by explicit user-approved roadmap reorder because
current limitations prevent completing those prerequisites; this does not
mark either prerequisite phase complete.

Work is on `codex/phase-12-discord-experience`. Steps 12.1 through 12.5 are
complete. The scoped Step 12.5 commit is the latest phase-branch handoff.

## Step 12.5 Verified Outcome

- Sessions persist a backward-compatible sanitized active failure projection
  with stable code, stage, retry disposition, resource/cost impact, bounded
  detail, timestamp, and opaque support reference. Raw provider and command
  diagnostics remain outside Discord presentation.
- One application catalog renders known and unknown failures with what
  happened, likely reason, platform action, exact user action, retry status,
  billing impact, and support reference. Failure content remains visible ahead
  of bounded player detail.
- Every current lifecycle worker records `NOT_SCHEDULED`; Phase 10 retry
  automation remains deferred and no retry is promised. Successful recovery
  clears only the active projection, preserving workflow/event audit history.
- Repeated requests during an unexpired operation return its safe operation,
  milestone, and start time without queueing a duplicate.
- Archive and termination now require a durable 12-character code bound to the
  owner, guild, session, action, lifecycle state, and exact version for ten
  minutes. Memory locking and DynamoDB transactions enforce atomic single-use
  consumption and state-drift rejection.
- `/rb archive` and `/rb terminate` only create the ephemeral confirmation.
  `/rb confirm code:<code>` consumes and revalidates it before queueing the
  exact action; `/rb cancel-confirmation code:<code>` permanently closes it.
  Direct application-layer destructive requests and inline booleans fail
  closed.
- The step review closed stale creation replays, authorization bypasses,
  ambiguous queue-delivery wording, response-limit ordering, and additional
  identifier, credential, address, and URL redaction gaps.

## Validation

- Focused Step 12.5 tests pass across domain, failure catalog/state, lifecycle
  workers, session-card rendering, session application service, memory and
  DynamoDB persistence, raw Discord interactions, and command registration.
- `go test ./...`, `go test -cover ./...`, `go vet ./...`, and
  `go build ./cmd/...` pass.
- `./scripts/package-discord-lambda.ps1` packages all Discord and worker
  Lambdas successfully.
- All 11 tracked Terraform `.tf` files pass `terraform fmt -check`, and
  `terraform -chdir=infra/terraform/environments/dev validate` passes.
- `git diff --check` passes.
- Race validation remains unavailable on this Windows host because
  `CGO_ENABLED=0` and no C compiler is installed. The pre-existing populated
  local `infra/terraform/environments/dev/tfplan` remains untracked and was not
  modified. Validation used Go 1.26.6 (one patch
  newer than the repository's 1.26.5 CI target) and Terraform 1.15.8.

## Operator Attention

The updated Discord handler, lifecycle workers, and `/rb` command definition
are not deployed. Live-guild use requires repackaging/deploying the affected
Lambdas and bulk guild command registration. Do not run Terraform apply without
the separately required plan, budget-recipient, and deployment approvals.

The current `/rb create` flow is intentionally Arma 3-specific. Phase 13.5 must
add a game field with autocomplete and route the selected supported game into
its game-specific creation/setup contract; it must not hard-code Arma 3 as the
only visible choice once another game is supported.

The current start boundary routes a ready session to provisioning and a later
start from provisioned state to bootstrap. This two-command UX is explicitly
scheduled for correction in task 12.8.7: creation remains non-billable, while
one `/rb start` owns provisioning through playable health acceptance and
repeated starts return the active operation's progress.

## Exact Next Step

Stop before Step 12.6. The exact next task, when explicitly requested, is
12.6.1: replace the single preset pointer with backward-compatible active and
pending preset revision metadata plus immutable change events. Do not treat
deferred Phases 10-11 as complete.
