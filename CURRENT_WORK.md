# Current Work

## State and Objective

Phases 1-9 are complete. Phases 10 and 11 remain pending. Phase 12 is proceeding
before them by explicit user-approved roadmap reorder because current limitations
prevent completing those prerequisites; this does not mark either prerequisite
phase complete.

Work is on `codex/phase-12-discord-experience`. Steps 12.1 and 12.2 are complete.

## Step 12.2 Verification

- Sessions have optional normalized 64-character descriptions, generated stable
  guild-scoped slugs with readable collision suffixes, and immutable audit
  events for creation and description changes. DynamoDB reads remain compatible
  with legacy records and legacy slugs are protected before atomic new claims.
- Every session-targeting `/rb` command uses authorized autocomplete and exact
  slug fallback without displaying immutable IDs. Autocomplete excludes
  terminated sessions by default, with an explicit application-level opt-in
  reserved for contexts that intentionally need tombstones. `/rb list` has
  lifecycle filters and five-entry pages; `/rb status` is a guild-authorized
  private view. Mutation authorization remains owner-scoped except the existing
  sleep/wake guild-manager policy.
- New and restored EC2 instances and EBS volumes receive `Name`, `SessionName`,
  and `SessionSlug`; immutable `Project`, `Environment`, and `SessionId` remain
  authoritative for discovery and destructive verification.
- New archive manifests preserve and verify readable identity and descriptions;
  legacy schema-v1 manifests remain restorable. Deletion tombstones and terminal
  events retain final readable identity while resource and artifact pointers are
  cleared.
- Repository-wide `go test -cover ./...`, `go vet ./...`, `go build ./cmd/...`,
  Discord command parsing/registration tests, Lambda packaging, Terraform format
  and validation, and `git diff --check` pass. The Windows environment cannot run
  `go test -race` because its Go race detector requires CGO; CI remains the race
  check.
- Focused session-selection and Discord-interaction tests pass after excluding
  terminated sessions from normal autocomplete results.

## Operator Attention

The updated Discord handler (including the autocomplete filter), Lambda
packages, command definition, and Discord Lambda IAM change are not deployed.
Preview requires an approved Terraform plan/apply, configuring the Discord
interaction endpoint, and bulk guild command registration. Never store the bot
token in Terraform variables or repository files.

## Exact Next Step

Implement only task 12.3.1: replace `/rb create` with one private modal for name,
description, vanilla/modded and TeamSpeak options, mission upload, and optional
preset upload using platform policy defaults. Do not begin task 12.3.2.
