# Current Work

## State and Objective

Phases 1-9 are complete. Phases 10 and 11 remain pending under the explicit
user-approved deferral; Phase 12 proceeding first does not mark either deferred
phase complete.

Work is on `codex/phase-12-discord-experience`. Steps 12.1 through 12.7 are
complete. The follow-up public-card refinement in task 12.4.8 is complete on
the phase branch.

## Task 12.4.8 Outcome

- Public session cards use a Discord embed with `ARMA 3 | Session Name`, a
  text-labelled status, and a green/orange/red/gray sidebar for online, setup,
  error, and inactive states. Plain text remains as a delivery fallback.
- The upper card shows the description, live mission and map, player count, and
  a Discord-native relative session-start time. A2S_INFO provides the bounded,
  sanitized mission/map values through the same refresh path as player data.
- Connection details place the linked active preset below the game address with
  a separating blank line; vanilla shows `Modlist: None`. TeamSpeak is omitted
  when disabled.
- New cards expose only `Show players` and `Refresh`. The live roster is bounded
  and ephemeral. Old control IDs remain accepted for already-posted cards, but
  `View details`, card-only modlist download, and card-only help are no longer
  generated. `/rb help` remains the help entry point.
- Public cards no longer show Guidance or Last updated. Detailed private status
  retains diagnostic guidance and freshness information.

## Validation

- Full `go test ./...` and `go test -cover ./...` pass; session-card coverage is
  82.5%, Discord interaction coverage is 71.0%, and notification sender coverage
  is 80.2%.
- `go vet ./...`, `go build ./cmd/...`, Lambda packaging, and
  `git diff --check` pass.
- Validation used Go 1.26.6, one patch newer than the repository's 1.26.5 CI
  target.

## Operator Attention

- Phase 10 retry policy and Phase 11 work remain deferred. No automatic retry
  is scheduled for current failures or an unverified rollback.
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
