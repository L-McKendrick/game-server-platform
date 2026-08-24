# Phase 12: Discord Experience and Session UX

## Summary

Phase 12 turns the existing Discord command boundary into the primary polished
product experience. Users create and manage sessions through `/rb`, select
sessions by name or slug, receive private interaction responses, and share one
durable public status card per session. The card exposes safe connection and
progress information, while internal identifiers and raw cloud diagnostics
remain implementation details.

This phase also introduces durable follow-up confirmations, actionable failure
messages, safe post-creation modlist revisions, milestone-based progress, a
downloadable active modlist, and streamlined role-based administration. The
bot never sends direct messages.

`PROJECT_PLAN.md` is the authoritative task order. This document defines the
cross-task behavior and decisions that must remain stable during implementation.

## User Experience Contract

### Commands and privacy

- Replace `/session` with `/rb` immediately; do not maintain an alias.
- Keep administration behind the direct `/rb admin` subcommand, open an
  ephemeral component menu, and privilege-check every admin interaction on the
  server even when Discord command visibility is restricted.
- Register commands for guild contexts only. The bot has no DM workflow.
- Modal input, command acknowledgements, detailed status, lists, help, errors,
  confirmations, and cost output are ephemeral unless explicitly identified as
  public below.
- One public card is created in the invoking channel for each session. It is
  the only persistent public lifecycle message and is edited at useful
  milestones rather than replaced or spammed.

Registration uses Discord's guild-command bulk-overwrite endpoint, which is
inherently unavailable in DMs; `contexts` is not added because Discord defines
that field only for global commands. The HTTP boundary also rejects a missing
guild before authorization, autocomplete, or routing. Because Discord applies
`default_member_permissions` and command permission overrides to the root
command rather than an individual nested subcommand group, `/rb admin` cannot
be selectively hidden without also hiding normal `/rb` actions. The root stays
available to configured platform roles, all admin responses remain ephemeral,
and every nested admin interaction is reauthorized using the current signed
Administrator or Manage Server permission.

### Identity and display

- The immutable session ID remains authoritative for persistence, queues,
  idempotency, object prefixes, workflow correlation, tags, and destructive
  verification, but normal Discord output never displays it.
- Session inputs use authorized autocomplete labels in the form
  `Name — slug — state`; the hidden choice value carries the immutable ID.
- Exact slugs are accepted as a fallback. Display names alone must not resolve
  ambiguously.
- Generate stable lowercase slugs from names and add readable numeric suffixes
  on collision. A display-name edit must not silently change the slug.
- Descriptions are optional, single-line, whitespace-normalized, sanitized, and
  limited to 64 Unicode characters. Store creation and changes in immutable
  event history and preserve the final value in the deletion tombstone.
- New and restored EC2/EBS resources receive readable `Name`, `SessionName`,
  and `SessionSlug` tags. Immutable `Project`, `Environment`, and `SessionId`
  tags remain mandatory for resource discovery and deletion.

### Creation and setup

`/rb create` opens a private base-setup modal:

1. session name;
2. optional description;
3. combined mode/features control, with vanilla implied when modded is not
   selected, TeamSpeak independently selectable, and an optional durable
   `Begin server setup` intent;
4. optional mission upload; when omitted, Arma uses BI's
   `MP_ZGM_m12.Stratis` built-in mission.

After a modded base submission, an ephemeral `Continue to mod options` button
opens a second modal containing an optional Launcher preset upload and a
checkbox group for the seven supported Creator DLCs. Discord does not permit a
modal-submit response to open another modal, so the explicit button is the
native continuation boundary. Vanilla creation skips it. `/rb edit` with the
`mods` section opens this
same mod-options modal for an existing modded session and allows a preset
upload, a cDLC-only change, or both. Every submission is owner/guild/version
bound and stale forms fail closed. Discord modals do not support arbitrary
images or media galleries; cDLCs therefore use their official accessible names
without unsupported icon placeholders.

The artifact worker honors `Begin server setup` only after the authoritative
session reaches `NEW`, then calls the existing start use case with a stable
idempotency identity. Artifact replay can retry a failed queue handoff without
creating a parallel lifecycle path. Creator DLCs likewise reuse the revisioned
mod pipeline: bootstrap maps the supported catalog to the server directories
`gm`, `vn`, `csla`, `ws`, `spe`, `rf`, and `ef`, verifies each selected
directory, and writes them before Workshop links in the same `mods.txt` read by
the existing `-mod` argument. Archive manifests preserve the canonical
selection, restore rejects drift, and cards show the selected product names.
Only rows explicitly typed `ModContainer` are accepted from Launcher presets.
The original HTML is never used for server bootstrap: both the server input and
downloadable copy are rebuilt from those rows, so `DlcContainer` sections and
Workshop-looking IDs in footers or unrelated markup cannot override the form.
A preset that becomes Workshop-empty after filtering is accepted only when the
form selects at least one supported cDLC. cDLC-only sessions are therefore
valid, while a content-empty modded session remains a recoverable draft.

Combined submissions mark the initial preset pending in the same stale-bound
configuration write before automatic-start eligibility is evaluated. This
prevents the cDLC selection from starting provisioning ahead of its accompanying
preset validation. Bootstrap checkpoints include the configuration revision so
a cDLC-only change is applied on the next setup even when the preset revision
does not change.

Creation uses the platform sleep/archive defaults. Submission creates a draft
idempotently, publishes the public card, and queues file validation. It does
not claim that uploads are valid before workers accept them. A partial failure
retains the draft and successful artifact. `/rb setup` reuses the same
contracts to edit allowed draft fields or replace only missing/rejected input.

Creation does not allocate billable infrastructure before required uploads are
accepted. When `Begin server setup` is selected, the platform durably waits for
required validation and then performs the equivalent of one `/rb start`;
otherwise the owner starts manually. One start request owns the transition
through infrastructure provisioning, game/content bootstrap, health checks,
and playable readiness. Users must not issue a second start merely to advance
from provisioned infrastructure into bootstrap. A repeated start while that
operation is active returns the existing operation and progress.

### Public status card

The card projection is authoritative and derived from persisted state. Show
fields only when they are meaningful:

- game and name in the card title, plus the description;
- text-labelled lifecycle/health status with a matching status-colored embed
  sidebar;
- lifecycle, health, current operation, visual milestone bar, current stage,
  and Discord-native relative elapsed time when applicable;
- live mission, map, and player count from the bounded game-server query path;
- copy-friendly Arma DNS/IP and game port;
- copy-friendly TeamSpeak DNS/IP and voice port when enabled;
- the active preset name linked to its downloadable modlist below the game
  address, or `None` for vanilla;
- actionable failure summary and resource/cost impact.

Prefer DNS to public IPv4 and fall back to IPv4 when DNS is unavailable. Never
show private addresses, instance or volume IDs, SSM details, admin ports,
secrets, raw command output, or stack traces. Retained endpoints may remain on
sleeping or archived cards only when explicitly marked offline. Refresh a
changed endpoint before declaring wake or restore healthy.

Use Discord-native absolute/relative timestamps, mobile-safe stacked content,
and text labels in addition to color or icons. Use the shared vocabulary:
`Setting up`, `Ready`, `Starting`, `Running`, `Sleeping`, `Archived`,
`Action required`, and `Terminated`.

The card exposes `Show players` and `Refresh`. `Show players` returns the
bounded live roster ephemerally; the modlist link is part of the card, and help
remains available through `/rb help`. Reauthorize and revalidate session/card
revision on every click. Lifecycle and destructive controls remain slash
commands in this phase.

### Status, list, and help

- `/rb status` renders an ephemeral detailed form of the authoritative card to
  any user allowed to use the bot in that guild. Mutation permissions do not
  broaden.
- `/rb list` excludes `DELETED` by default, supports lifecycle filtering, and
  paginates ephemerally. Prefer stacked entries when a table would wrap on
  mobile. Never truncate in the middle of an identifier needed by a user.
- `/rb help` explains the lifecycle and returns state-aware next actions when a
  session is selected.
- Empty and success responses state what happened and the next useful action.
- A repeated request for an active operation returns its current progress
  rather than a generic conflict or a second workflow/card.

The optionless help path distinguishes a true first run from an existing
owner session and stays a short orientation rather than copying the runbook.
Its optional guild-visible session selector resolves opaque values
authoritatively, gives one lifecycle-specific next action, calls out the
billable start/restore boundaries, and never queues work. Failed, rejected,
deleting, and rollback guidance states that no automatic retry is scheduled.

### Modlist artifact and revisions

Do not repeatedly attach a preset to the frequently edited card. Maintain a
separate stable bot-authored channel message for the active modlist and link it
from the card. Generate a sanitized Arma Launcher-compatible file from the
validated preset, use a slug-based filename, and strip irrelevant local or
sensitive metadata. Recreate the message from the durable S3 object if it is
deleted.

Post-creation `/rb edit` with the `mods` section updates the desired Creator DLC set and optionally
uploads and validates a pending preset revision. A running session is not
interrupted. Apply pending mod options on the next start,
wake, or restore. The previous revision remains active until installation and
health verification pass; promote the public attachment only after success.
On failure, retain diagnostic history and attempt to return to the previous
known-good configuration. Archive and restore must preserve active and pending
intent deterministically.

### Failures and confirmations

Persist diagnostics separately from user presentation. User-facing failures
must state:

1. what happened;
2. the likely sanitized reason;
3. whether the platform is retrying or stopped;
4. the exact user action, if any;
5. whether resources remain and may incur cost;
6. a short opaque support reference when escalation is useful.

Never make a retry promise unless Phase 10 has scheduled that retry. Unknown
errors receive a safe fallback with resource impact and support reference.
Successful recovery clears the active visible failure but does not delete its
audit event.

Archive and terminate first create a durable confirmation and perform no
destructive work. Each user has one pending confirmation slot per guild;
`/rb confirm` atomically consumes its owner/guild/session/action/state-bound
record within ten minutes, and `/rb cancel-confirmation` cancels it. Replays,
stale state, mismatched users, and expired confirmations fail closed. Future
buttons may call the same application service but may not weaken this contract.

### Milestone progress

Persist stable ordered milestone sets for each workflow. The progress model
stores the current milestone, completed milestones, operation start time, and
last-progress time without raw command output. Bootstrap must expose meaningful
checkpoints instead of appearing as one opaque managed-node operation.

The public card and ephemeral status render a text-based visual bar together
with `Step X/Y`, the current stage, and elapsed time. The bar represents
completed checkpoints, not elapsed-time percentage or estimated remaining
duration. Do not collect historical duration samples or present an ETA.

Define deterministic rules for completed, skipped, replayed, retried, rolled
back, failed, and cancelled milestones so progress never falsely advances or
moves backward. Show concise qualitative guidance such as `Usually a few
minutes` or `Often the longest stage; large modlists may take considerably
longer`, without promising a completion time. Distinguish active, waiting,
stalled, retrying, rollback, completed, and action-required states. Card
delivery remains idempotent, milestone-only, rate-limited, and secondary to
authoritative state persistence.

### Administration

`/rb admin` is a permission-checked direct subcommand that opens a component
menu.
Normal platform access follows Discord's role model, while every admin command
and component rechecks the invoking member's current Administrator or Manage
Server permission. Only expose controls backed by implemented policies. Phase
12 includes access and card repair; the access view replaces the complete role
set and offers a separately confirmed remove-all action. An empty persisted set
disables normal-role access while Administrator or Manage Server remains the
recovery path. Contextual help remains under `/rb help`.
The wishlist cost command is omitted entirely, so Discord makes no Cost
Explorer requests and the Lambda role needs no Billing permissions. Existing
AWS budget alerts remain an operator concern outside Discord. Later scheduling
and duration features can attach to the same menu only after their policies
exist.

The danger area adds one full runtime reset visible only to members whose
current signed interaction carries Discord Administrator. Manage Server is
enough for access and card repair but never for reset. Reset uses an exact
ten-minute typed phrase, atomically consumes it while acquiring one
environment-wide lock, and freezes new session mutations until cleanup records
a terminal result. The deployment gate defaults off.

Reset deletes only discovered platform runtime state: active workflow
executions, exactly tagged game instances and disposable volumes, known
bot-authored session messages, every version below `sessions/`, runtime queue
contents, reset-scoped metadata, and eligible pre-reset application log
streams. It preserves the Terraform control plane and state, guild access,
secrets, guild configuration artifacts, budgets, CloudTrail, billing records,
AWS-retained service history, and the latest bounded reset result. Incomplete
cleanup fails closed, displays a possible-cost warning, and schedules no
automatic cleanup retry. An unacknowledged first worker attempt is isolated in
the reset DLQ rather than replaying destructive cleanup.

The Administrator-only configuration area accepts one guild-level Arma 3
`server.cfg`. Uploads use Discord's private modal file control, accept only a
non-empty UTF-8 `.cfg` file up to 64 KiB, and are downloaded by the artifact
worker into a private revisioned `guilds/<guild>/server-config/` S3 prefix.
Only filename, size, revision, and update time are rendered; contents and object
keys are never shown because the file may contain server passwords.

Start captures either the active object key/revision/SHA-256 or an explicit
generated-default selection in its internal command. Workflow lock acquisition
persists that snapshot atomically, and bootstrap downloads and verifies the
exact object before installing it as `server.cfg`. Replacement affects future
sessions only. Removal is revision-confirmed and returns future sessions to the
generated safe default; existing sessions retain their snapshot for replay,
wake, and restore determinism. Private prior artifacts remain available for
those existing snapshots and are preserved by platform reset.

Public cards retain stacked plain-text fallbacks and pair explicit state
labels/icons with embed color. Their only default buttons are `Show players`
and `Refresh`. Unknown, expired, revision-drifted, and deleted-state controls
return ephemeral reopen/refresh guidance without echoing custom IDs or raw
state.

## Design Guidelines

- Keep domain and application packages independent of Discord and AWS. Model
  cards, failures, selectors, confirmations, and revisions as application/domain
  concepts with adapter-specific rendering at the boundary.
- Persist authoritative state before attempting Discord delivery. Discord
  failure must not roll back a valid lifecycle transition or claim success that
  was not persisted.
- Reuse one renderer, one authorization path, one session resolver, one stage
  taxonomy, and one notification transport. Do not create workflow-specific
  versions of these mechanisms.
- Treat every interaction as replayable and every component as stale or
  attacker-controlled. Bind state server-side, use opaque custom values, check
  revisions, and preserve idempotency.
- Keep public messages safe for everyone who can view the channel. Disable
  mentions, escape user-controlled Markdown, bound every field, and avoid
  secrets or operational diagnostics.
- Prefer progressive disclosure: concise public card, ephemeral detailed view,
  and protected logs for operators.
- Prefer plain language and outcome-oriented controls. Do not expose internal
  lifecycle vocabulary or error codes when a user term exists.
- Maintain accessibility: do not rely on color, use text with icons, keep
  controls short, and test desktop and mobile layouts.
- Card and modlist repair must be automatic and idempotent. Store message
  references as replaceable delivery metadata, not domain identity.
- No direct messages, live mod hot-swap, public cost data, `/session` alias,
  visible internal IDs, or web UI are part of this phase.

## Codex Development Instructions

1. Complete Phases 10 and 11 prerequisites before starting Phase 12 unless the
   user explicitly reorders the roadmap.
2. Start Phase 12 on a new `codex/` phase branch. Before each step, confirm its
   numbered tasks remain accurate; modify the plan first if discovery changes
   the task boundary.
3. Implement exactly one numbered task per development prompt by default. Do
   not silently combine tasks, even when adjacent edits look convenient.
4. At the start of each task, read `CURRENT_WORK.md`, `PROJECT_PLAN.md`, this
   document, and the directly relevant existing adapter/domain files. Preserve
   unrelated worktree changes.
5. Prefer backward-compatible persistence reads and explicit migration-on-write
   behavior. Do not require a table replacement for additive metadata.
6. Test at the narrowest layer first, then run the proportional repository
   checks required by the touched surface. Discord protocol changes require raw
   payload/response tests; persistence changes require memory and DynamoDB
   coverage; Terraform changes require formatting and validation.
7. Never use live guild or AWS mutations as a substitute for unit/integration
   coverage. Reserve live acceptance for the end of a step or the phase and
   follow the deployment runbook.
8. After each state-changing task, update `CURRENT_WORK.md` with the immediate
   handoff and mark only verified task status in `PROJECT_PLAN.md`. Keep both
   concise and replace stale content.
9. Commit and push only when every task in a step is complete, using a scoped
   Conventional Commit. Merge the phase only through a reviewed pull request.
10. If Discord platform behavior differs from this plan, verify current official
    Discord documentation, record the constraint, and choose the smallest
    behavior-preserving adjustment before implementation.

## Phase Acceptance

Phase 12 is complete only when a live guild user can create a vanilla and a
modded session through `/rb create`, operate them without copying an internal
ID, follow one repairable public card, retrieve private detail, understand and
recover from representative failures, stage and apply a new modlist, download
the active sanitized preset, and complete destructive follow-up confirmation.
Automated verification must cover
authorization, idempotency, replay, redaction, bounds, stale components,
partial Discord failure, persistence compatibility, workflow regressions, and
Terraform/command registration.
