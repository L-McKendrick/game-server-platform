# Current Work

## State and Objective

Phases 1-9 are complete. Phases 10 and 11 remain pending. Phase 12 is
proceeding before them by explicit user-approved roadmap reorder because
current limitations prevent completing those prerequisites; this does not
mark either prerequisite phase complete.

Work is on `codex/phase-12-discord-experience`. Steps 12.1 through 12.4 are
complete. The scoped Step 12.4 commit is the latest phase-branch handoff.

## Step 12.4 Verified Outcome

- One adapter-neutral projection now drives public cards and private details
  for readable identity, lifecycle, health, normalized progress, elapsed time,
  players, endpoints, mods, safe failures, artifacts, and source freshness.
  Public output omits player names and immutable infrastructure/workflow/session
  identifiers; raw workflow failures are not exposed.
- Bot-token notification delivery creates or edits one persistent public card,
  stores replaceable Discord delivery metadata independently of the session,
  rejects stale revisions and changed idempotency payloads, and uses stable
  delivery-specific nonces for ambiguous create retries.
- Sessions persist backward-compatible, workflow-bound Accepted,
  Infrastructure ready, Game/content setup, Health verification, Completed,
  and Failed milestones. Lifecycle workers publish only persisted major
  milestones after authoritative state writes.
- Healthy sessions show copy-ready Arma and optional TeamSpeak DNS/public-IP
  endpoints with ports. Retained sleeping or archived endpoints are labeled
  offline, while private or pre-health endpoints remain hidden.
- Accepted modded presets produce a deterministic sanitized Arma Launcher
  import file from bounded Workshop IDs and names. A separate stable Discord
  attachment message is backed by verified S3 content, linked from the card,
  and recreated on Discord `404`; vanilla sessions publish no active preset.
- Revision-bound `View details`, bounded `Refresh`, `Download modlist`, and
  `Help` controls use one-way session tokens and revalidate guild, channel,
  authorization, and current persisted state. Responses are ephemeral and
  mention-suppressed; the download action exposes only the stable Discord link
  and safe filename.
- Card delivery now recreates a deleted message only after Discord confirms
  `404` and persists the replacement reference. Rate limits and other partial
  outages remain retryable without fallback POST duplication. Manage Server or
  Administrator members can explicitly queue `/admin repair-card`; autocomplete
  is guild-scoped and excludes terminated sessions by default.
- Step review fixed attachment pointer aliasing, attachment-aware idempotency,
  delivery-specific card/modlist nonces, deterministic admin-repair replays,
  and changing live-player observations inside the one-minute refresh window.

## Validation

- Focused Step 12.4 tests and vet pass across domain, application, persistence,
  S3/SQS, Discord interaction/notification, command, and worker packages.
- `go test ./...`, `go test -cover ./...`, `go vet ./...`, and
  `go build ./cmd/...` pass.
- `./scripts/package-discord-lambda.ps1` packages all Discord and worker
  Lambdas successfully.
- All 11 tracked Terraform `.tf` files pass `terraform fmt -check`, and
  `terraform -chdir=infra/terraform/environments/dev validate` passes.
- `git diff --check` passes.
- Race validation is unavailable on this Windows host because `CGO_ENABLED=0`
  and no C compiler is installed. The recursive Terraform formatting command
  reports only the populated local `infra/terraform/environments/dev/terraform.tfvars`;
  it was intentionally not modified. Validation used Go 1.26.6 (one patch
  newer than the repository's 1.26.5 CI target) and Terraform 1.15.8.

## Operator Attention

The updated Discord handler, command worker, artifact worker, notification
worker, Terraform, `/rb`, and `/admin` command definitions are not deployed.
Live-guild use requires reviewing/applying the IAM and environment changes,
repackaging/deploying the affected Lambdas, and bulk guild command
registration. Do not run Terraform apply without the separately required plan,
budget-recipient, and deployment approvals.

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

Stop before Step 12.5. The exact next task, when explicitly requested, is
12.5.1: persist a sanitized failure projection containing stable internal code,
failed stage, retry disposition, resource/cost impact, bounded detail,
timestamp, and support reference. Do not begin 12.5.2 or revisit pending
Phases 10-11 as though they were complete.
