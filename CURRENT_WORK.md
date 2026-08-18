# Current Work

## State and Objective

Phases 1-9 are complete. Phases 10 and 11 remain pending under the explicit
user-approved deferral; Phase 12 proceeding first does not mark either deferred
phase complete.

Work is on `codex/phase-12-discord-experience`. Steps 12.1 through 12.7 are
complete. Step 12.7 is delivered as the scoped commit
`feat(discord): add workflow step progress` on the phase branch.

## Step 12.7 Outcome

- Provision, bootstrap, sleep, wake, archive, restore, termination, and
  reconciliation have stable ordered checkpoint sets. Session progress stores
  the workflow-bound current, completed, and explicitly skipped checkpoints,
  qualitative state, operation start, and last-progress time.
- DynamoDB persistence is additive. The prior `progress_updated_at` projection
  remains write-through compatible; legacy clocks, coarse bootstrap/failure
  stages, and completed-step facts normalize safely on read and migrate on the
  next write.
- The managed bootstrap script emits allowlisted host, installation, mod,
  configuration, service, and health checkpoints. The SSM adapter returns only
  ordered sanitized facts, never raw command output, and polling folds multiple
  observations into one optimistic-concurrency-guarded session revision.
- Progress is monotonic across replay, retry state, rollback, failure,
  cancellation, explicit skips, and backward clocks. Authoritative workflow
  success closes proven remaining checkpoints; skipped work never fills the
  completed bar. Terminal states cannot advance.
- Public cards, detailed status, and repeated-operation responses reuse the
  existing projection/rendering path. They show a compact completed-only bar,
  `Step X/Y`, sanitized current stage, clamped elapsed time, qualitative state,
  and bounded guidance. They do not show ETA, percentages, or the internal
  milestone vocabulary.
- Waiting/retrying state-only edits are suppressed. Durable checkpoints and
  rollback/action-required/cancelled states use bounded idempotency keys so
  polling does not spam Discord and terminal information cannot be hidden by a
  prior checkpoint delivery.
- The complete-step audit covered authorization boundaries, persistence
  compatibility, atomicity, idempotency, replay protection, workflow and clock
  drift, misleading progress, redaction, billing/retry wording, unknown-state
  fallback, notification noise, and unnecessary complexity. No authorization
  or billing policy was broadened.

## Validation

- Focused validation passed after each task 12.7.1 through 12.7.6, including
  every workflow order, bootstrap checkpoint allowlisting, multi-checkpoint
  polling, explicit skips, replay, retry state, rollback, failures,
  cancellation, clock anomalies, legacy persistence, rendering bounds, and
  notification rate limiting.
- Full `go test ./...`, `go test -cover ./...`, `go vet ./...`, and
  `go build ./cmd/...` pass.
- Lambda packaging, Discord command JSON parsing, bootstrap Bash syntax,
  tracked Terraform formatting, `terraform validate`, and `git diff --check`
  pass.
- Race validation is unavailable on this Windows host: `go test -race ./...`
  requires CGO and no C compiler is installed.
- Validation used Go 1.26.6 (one patch newer than the repository's 1.26.5 CI
  target) and Terraform 1.15.8.

## Operator Attention

- Phase 10 retry policy and Phase 11 work remain deferred. No automatic retry
  is scheduled for current failures or an unverified rollback; operators must
  follow the persisted card/status guidance and support reference.
- Do not run Terraform apply without the separate plan, budget-recipient, and
  deployment approvals. The ignored local `terraform.tfvars` and untracked
  `infra/terraform/environments/dev/tfplan` were not modified.
- `/rb create` remains non-billable. The existing two-command start/bootstrap
  boundary is intentionally deferred to 12.8.7.
- Phase 13.5 remains responsible for adding a game selector and extracting
  game-specific creation/setup behavior before another game is exposed.

## Exact Next Step

Start task 12.8.1 only in a new development prompt. Do not combine it with
later 12.8 tasks unless the user explicitly requests that scope.
