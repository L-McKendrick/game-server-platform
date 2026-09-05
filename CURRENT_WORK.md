# Current Work

## State and Objective

Phase 17 Workshop content sources, including the live mission target and Steam
authorization-heartbeat corrections, is merged into `main` and verified in the
development environment. Phase 17.24 reconciles the durable documentation with
that final behavior on `codex/workshop-docs-sync`.

## Documentation Review

- The host sync target is documented with its canonical values: `all`,
  `mission`, and `mods`.
- The Workshop and Steam authorization guides document prompt heartbeat-worker
  shutdown without changing the five-minute renewal or 15-minute lease policy.
- The mission-management guide and README now include uploaded and
  Workshop-backed scenarios, live running-session availability, deferred
  lifecycle behavior, and pending mod activation.
- Existing documentation already accurately covers the 50-direct-child limit,
  unsupported nested collections, public/ephemeral response boundaries,
  immutable collection snapshots, ordinary-wake download skipping, and
  EventBridge plus reconciliation completion behavior.

## Validation

- Documentation references were checked against the merged implementation and
  Phase 17 roadmap.
- Markdown link targets resolve locally.
- `git diff --check` passes.

## Commands to Apply Current Changes

No deployment, Lambda packaging, Terraform plan, apply, verification, or
Discord command registration is required. These are documentation-only changes.
