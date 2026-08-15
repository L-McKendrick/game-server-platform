# Current Work

## State and Objective

Phases 1-9 are complete. Phases 10 and 11 remain pending. Phase 12 is proceeding
before them by explicit user-approved roadmap reorder because current limitations
prevent completing those prerequisites; this does not mark either prerequisite
phase complete.

Work is on `codex/phase-12-discord-experience`. Step 12.1 is complete. Discord
session operations use only the guild-scoped `/rb` command, the interaction
boundary supports the protocol shapes required by Phase 12, one shared
authorized session selector supplies safe autocomplete choices, and one
renderer owns interaction presentation and safety.

## Task 12.1.1 Verification

- The guild bulk-registration command now loads `deploy/discord/rb-command.json`
  and `deploy/discord/admin-command.json`; bulk overwrite removes the former
  guild `/session` definition when deployed.
- Active Discord responses, worker notifications, deployment tooling, README,
  and interaction deployment runbook now point users to `/rb`.
- A focused interaction regression verifies that a signed legacy `/session`
  payload is rejected without creating a session.
- Full non-race Go tests, vet, and all command builds pass.
- Both Discord command definition files parse as JSON; stale active `/session`
  registration, routing, and guidance scans are clean; `git diff --check` passes.

## Task 12.1.2 Verification

- The protocol models autocomplete and modal-submit interactions, focused
  options, modal callback data, component IDs, buttons, all select variants,
  Components V2 layout/content types, file displays, and modal file uploads.
- Modal submissions preserve nested Label input and resolved attachment data;
  select interactions preserve selected values and lossless resolved entities.
- Components V2 responses support the required message flag without legacy
  top-level content, and autocomplete safely returns an explicit empty choice
  list until task 12.1.3 provides authorized session choices.
- Component custom IDs use a canonical `rb:v1` envelope with a bounded action,
  positive revision, opaque token, and Discord's 100-character limit.
- Focused tests for `internal/adapters/discord/interactions` pass, including wire
  encoding/decoding, handler acknowledgements, and malformed component-token
  rejection. The broader Step 12.1 regression suite and review remain deferred
  to task 12.1.5 as requested.

## Task 12.1.3 Verification

- The session application service selects only actor-owned sessions in the
  requested guild, supports case-insensitive name/slug/state filtering, sorts
  deterministically, and caps results at Discord's 25-choice limit.
- Authorized autocomplete uses the shared selector for both transitional
  `session-id` and future `session` option names. Labels contain only
  `Name — slug — state`; the immutable session ID is carried only as the
  hidden choice value.
- Labels preserve the readable slug and lifecycle state within Discord's
  100-character choice-name limit. Unsupported, malformed, unauthorized, and
  empty matches return a valid explicit empty choice list.
- Focused tests for `internal/app/sessions` and
  `internal/adapters/discord/interactions` pass. Registration-wide rollout and
  exact-slug command resolution remain task 12.2.3; the broader Step 12.1 suite
  and review remain deferred to task 12.1.5.

## Task 12.1.4 Verification

- All interaction messages now pass through one renderer that applies the
  ephemeral flag where appropriate, explicit allowed-mention suppression, and
  a Unicode-safe 1,900-character content bound.
- User-controlled inline text is single-line normalized, control/format
  characters are removed, and Discord Markdown is escaped. Code-style values
  are normalized and protected from backtick breakout.
- Session creation, configuration, list, status, artifact acknowledgement, and
  lifecycle acknowledgement output no longer displays immutable session IDs.
  Existing commands still accept IDs internally until task 12.2.3 rolls out
  session autocomplete and exact-slug resolution across command definitions.
- Lifecycle output and selector labels use the shared accessible vocabulary:
  `Setting up`, `Ready`, `Starting`, `Running`, `Sleeping`, `Archived`,
  `Action required`, and `Terminated`. Boolean state uses readable text.
- Creation, configuration, and status output use Discord-native absolute and
  relative timestamps. Focused interaction and notification adapter tests pass,
  including sanitization, Unicode bounds, timestamp rendering, vocabulary, ID
  hiding, and mention suppression. The broader Step 12.1 regression suite and
  review remain deferred to task 12.1.5.

## Task 12.1.5 and Step 12.1 Verification

- Registration tests verify guild bulk overwrite sends only `/rb` and `/admin`;
  the legacy `/session` definition is absent and both active definitions parse.
- Signed interaction tests cover malformed and oversized payloads, unauthorized
  autocomplete non-disclosure, owner/guild selection isolation, stale and
  malformed components, authorization before component handling, and Discord
  content, choice-count, label, component-ID, and mention-suppression limits.
- Step review moved ordinary components behind the shared authorization path,
  normalized bounded autocomplete labels, and corrected transitional deployment
  guidance. No Step 12.2 behavior was started.
- `go test ./...`, `go vet ./...`, `go build ./cmd/...`, active legacy-command
  scans, command JSON parsing, and `git diff --check` pass. Race testing is not
  available in this environment because CGO is disabled and no C compiler is
  installed; focused and repository-wide non-race tests pass.

## Operator Attention

Deploying the command definitions is intentionally deferred until task 12.2.3
adds session autocomplete and exact-slug resolution to targeting commands. When
deployment is approved after that task, use guild bulk registration so the old
`/session` command is removed rather than upserting `/rb` alongside it.

## Exact Next Step

Implement only task 12.2.1: add an optional normalized 64-character session
description and record creation and later changes in immutable event history.
Do not begin task 12.2.2.
