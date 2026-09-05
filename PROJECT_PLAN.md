# Project Plan

## Summary and Goal

Build a secure, cost-bounded AWS platform controlled through Discord that provisions, operates, sleeps, archives, restores, and destroys temporary dedicated game servers. Deliver the complete Arma 3 lifecycle first, then reuse the platform for other games.

## Remaining Delivery Order

1. Complete the core product with maximum-duration cost guardrails (Phase 15).
2. Harden production operations and optimize measured bottlenecks (Phase 16).
3. Future enhancements (Phase 17).

## Phases

1. **Foundation — Done**
   - **1.1** [x] Establish Go structure, configuration, logging, and CI.
   - **1.2** [x] Provision Terraform state, DynamoDB, S3, and secret boundaries.
   - **1.3** [x] Establish AWS authentication and baseline security.

2. **Metadata Layer — Done**
   - **2.1** [x] Define sessions, lifecycle states, events, and idempotency.
   - **2.2** [x] Implement CRUD, persistence, and object-storage boundaries.
   - **2.3** [x] Test metadata and artifact behavior.

3. **Discord Interface — Done**
   - **3.1** [x] Verify interactions and expose session management commands.
   - **3.2** [x] Add uploads, idempotency, and safe responses.
   - **3.3** [x] Configure guild access through role selection.

4. **Workflow Foundation — Done**
   - **4.1** [x] Add FIFO queues, workers, and DLQs.
   - **4.2** [x] Add workflow contracts, leases, and Step Functions boundaries.
   - **4.3** [x] Fail unimplemented workflows closed.

5. **Infrastructure Provisioning — Done**
   - **5.1** [x] Implement `/session start` and staged provisioning.
   - **5.2** [x] Add bounded EC2/EBS, networking, SSM, IAM, capacity, and budgets.
   - **5.3** [x] Validate code and review the non-destructive baseline plan.
   - **5.4** [x] Configure alerts, apply the approved plan, and register `/session start`.
   - **5.5** [x] Provision one session through Discord and verify EC2/EBS, SSM, and `BOOTSTRAPPING`.

6. **Arma Bootstrap — Done**
   - **6.1** [x] Install SteamCMD, Arma, DLC, and Workshop content resumably.
   - **6.2** [x] Deploy configuration, mission, and optional TeamSpeak.
   - **6.3** [x] Launch, verify health, and mark the session playable.

7. **Monitoring — Done**
   - **7.1** [x] Add heartbeat, player, service, and voice checks.
   - **7.2** [x] Publish metrics/alarms and defer automatic recovery to Phase 10.

8. **Sleep and Wake — Done**
   - **8.1** [x] Implement manual safe sleep and live `/session status` A2S player count; defer automatic idle detection until the server-side activity adapter exists.
   - **8.2** [x] Restart, verify health, refresh endpoints, and retain data.
   - **8.3** [x] Register sleep, wake, and archive Discord commands. Allow the session owner or a signed Discord Administrator/Manage Server member to invoke the implemented sleep/wake workflows; archive remains fail-closed until Phase 9 provides its destructive workflow safeguards.

9. **Archive, Restore, and Termination — Done**
   - **9.1** [x] Add owner-confirmed interruption warnings, portable bounded archives, versioned manifests, archive and manifest checksums, S3 verification, and non-destructive metadata completion.
   - **9.2** [x] Destroy tagged disposable infrastructure only after durable archive verification, then recreate infrastructure, safely restore validated data, and pass service health acceptance. Permit the approved AMI-backed gp3 root volume in provisioning and restore IAM while retaining encrypted-only authorization for blank data volumes.
   - **9.3** [x] Add an owner-confirmed `/session terminate` command that immediately stops and permanently deletes all tagged runtime infrastructure and session-owned stored artifacts for the selected session without creating an archive. Preserve only an auditable terminal metadata record, require an explicit irreversible-action confirmation, and fail closed on ownership or tag mismatches.
   - **9.4** [x] Support explicitly configured vanilla Arma sessions without a mod preset or Steam account.
     - **9.4.1** [x] Add a persisted, backward-compatible vanilla-session configuration flag and expose it through Discord configuration/status output.
     - **9.4.2** [x] Make a configured mission sufficient for vanilla-session readiness while retaining the preset requirement for modded sessions.
     - **9.4.3** [x] Bootstrap vanilla sessions with the shared cached Steam authorization while skipping all preset and Workshop processing.
     - **9.4.4** [x] Preserve vanilla intent through archive manifests and restore validation.
     - **9.4.5** [x] Add focused domain, application, Discord, persistence, bootstrap, and archive/restore regression coverage.

10. **Reliability — Done**
    - **10.1** [x] Add bounded retries, DLQ operations, reconciliation, and cancellation.
      - **10.1.1** [x] Define and persist bounded retry, cooperative cancellation, workflow reconciliation, and dead-letter operation state without weakening existing locks or idempotency.
      - **10.1.2** [x] Apply bounded transient retries and terminal-failure policies to queues, workers, and Step Functions while preserving partial-batch behavior and truthful retry status.
      - **10.1.3** [x] Add owner-authorized cancellation at the initial safe workflow boundary without interrupting destructive or consistency-critical mutations.
      - **10.1.4** [x] Reconcile stale workflow locks, missing or terminal executions, and incomplete workflow metadata through scheduled and operator-invoked non-destructive repairs.
      - **10.1.5** [x] Add operator CLI/runbook DLQ inspection and idempotent redrive, queue alarms, and focused retry, cancellation, reconciliation, and replay coverage.
    - **10.2** [x] Add orphan cleanup and disaster recovery.
      - **10.2.1** [x] Define orphan evidence and discover existing project-tagged EC2 instances, EBS volumes, security groups, and session S3 prefixes; reserve schedules as report-only until the platform introduces per-session schedules.
      - **10.2.2** [x] Persist orphan findings and implement retry-safe quarantine or cleanup only after immutable-tag, session-state, resource-reference, and minimum-age checks pass.
      - **10.2.3** [x] Add scheduled detection plus explicit operator inspect and cleanup commands; default every uncertain or malformed case to report-only behavior.
      - **10.2.4** [x] Document and focused-validate DynamoDB, S3/archive, Terraform-state, workflow, and retained-volume disaster recovery, including alarms and recovery evidence.
    - **10.3** [x] Replace routine Steam password login with a secure cached authorization flow.
      - **10.3.1** [x] Define and store the encrypted, versioned Steam authorization-cache contract with least-privilege access, serialized mutation, rollback, redaction, and explicit reauthorization state.
      - **10.3.2** [x] Add a short-lived operator enrollment and reauthorization procedure that never carries passwords or Guard codes through Discord, workflows, Lambda configuration, SSM command text, or persistent logs.
      - **10.3.3** [x] Inject cached `config.vdf` only during authenticated downloads, use username-only login, preserve valid updates, and remove authentication material before launch on every exit path.
      - **10.3.4** [x] Fail closed on renewed Steam Guard challenges, provide operator-safe guidance, and preserve Workshop-free vanilla behavior.
      - **10.3.5** [x] Add replacement-host reuse, invalidation, concurrency, cleanup, redaction, archive/restore, and vanilla regression coverage plus the operating runbook.

11. **Production Hardening — Resequenced**
    - moved -

12. **Discord Experience and Session UX — Done (approved reorder)**
    - Proceeded before Phases 10 and 11 by explicit user approval because of
      current limitations. Phase 10 is complete; the deferred Phase 11 scope is
      now ordered under Phase 16.
    - Detailed design and execution guidance: `docs/phase-12-discord-experience.md`.
    - **12.1** [x] Establish the Discord interaction and presentation foundation.
      - **12.1.1** [x] Replace the `/session` command definition and routing with guild-only `/rb`; do not ship a compatibility alias, and consolidate administration under the protected `/rb admin` group during release polish.
      - **12.1.2** [x] Extend the Discord protocol boundary for autocomplete, modal submission, Components V2, buttons, selects, file uploads, and safe component revisions.
      - **12.1.3** [x] Add a shared session selector that returns authorized name/slug/state labels while carrying the immutable ID only as the hidden value.
      - **12.1.4** [x] Centralize Discord rendering, sanitization, native timestamps, status vocabulary, accessibility text, response bounds, and allowed-mention suppression.
      - **12.1.5** [x] Add interaction, command-registration, authorization, malformed-payload, stale-component, and response-limit tests for the new foundation.
    - **12.2** [x] Make session identity, discovery, and AWS presentation human-readable.
      - **12.2.1** [x] Add an optional normalized 64-character session description and record creation and later changes in immutable event history.
      - **12.2.2** [x] Generate a stable slug from the display name, add readable collision suffixes, and preserve explicit legacy slugs without changing immutable identity.
      - **12.2.3** [x] Add authorized session autocomplete and exact-slug resolution to every session-targeting `/rb` command without exposing IDs; exclude terminated sessions from autocomplete by default while allowing explicit opt-in for applicable tombstone workflows.
      - **12.2.4** [x] Make `/rb list` exclude `DELETED` by default, support lifecycle filters including deleted sessions, and add bounded ephemeral pagination with readable headings or mobile-safe stacked rows.
      - **12.2.5** [x] Make `/rb status` an ephemeral detailed view available to approved users in the session guild while preserving mutation authorization.
      - **12.2.6** [x] Tag new and restored EC2/EBS resources with readable `Name`, `SessionName`, and `SessionSlug` values while retaining immutable `Project`, `Environment`, and `SessionId` safeguards.
      - **12.2.7** [x] Preserve descriptions and readable identity through persistence, archive/restore, deletion tombstones, and regression tests.
    - **12.3** [x] Replace multi-command setup with a resilient creation and setup wizard.
      - **12.3.1** [x] Implement `/rb create` as one private modal for name, description, vanilla/modded and TeamSpeak options, mission upload, and optional preset upload using platform policy defaults.
      - **12.3.2** [x] Validate modal fields and attachments, create the draft idempotently, and queue mission/preset ingestion without claiming validation success synchronously.
      - **12.3.3** [x] Create exactly one public session card in the invoking channel and persist its channel/message reference independently of artifact processing.
      - **12.3.4** [x] Update the card as each artifact is accepted or rejected and keep a partially configured draft recoverable.
      - **12.3.5** [x] Add `/rb setup` to edit allowed draft fields and replace only missing or rejected artifacts through the same modal contracts.
      - **12.3.6** [x] Add permission preflight and plain-text degradation for missing send, edit, component, embed, or attachment capabilities.
      - **12.3.7** [x] Test duplicate submissions, partial failures, modal limits, vanilla/modded requirements, card-delivery failure, and setup recovery.
    - **12.4** [x] Deliver the durable public session card and modlist artifact experience.
      - **12.4.1** [x] Define one authoritative card projection for name, slug, description, lifecycle, health, stage, elapsed time, players, endpoints, mods, failures, and data freshness.
      - **12.4.2** [x] Extend notification delivery to create and idempotently edit bot-authored card messages without relying on expiring interaction tokens.
      - **12.4.3** [x] Persist normalized major-stage progress and rate-limit card edits to accepted, infrastructure ready, game/content setup, health verification, and terminal milestones.
      - **12.4.4** [x] Show copy-friendly Arma and optional TeamSpeak DNS/IP plus ports when available, and clearly label retained endpoints offline while sleeping or archived.
      - **12.4.5** [x] Publish a separate stable channel message containing the sanitized Arma-compatible active modlist attachment and link it from the card; recreate it from S3 if deleted.
      - **12.4.6** [x] Add authorized `View details`, bounded `Refresh`, `Download modlist`, and `Help` controls with state/revision revalidation and concise Discord button labels.
      - **12.4.7** [x] Automatically repair missing cards and add an explicit admin repair action, with tests for deletion, duplicate delivery, stale controls, rate limits, and partial Discord outages.
      - **12.4.8** [x] Replace the public card's plain message with the approved status-colored embed, expose live mission/map and players through the bounded A2S path, group the linked active modlist with connection details, replace `View details` with `Show players`, and remove card-only help/download controls while retaining plain-text degradation and `/rb help`.
    - **12.5** [x] Replace opaque failures and inline booleans with actionable errors and durable confirmations.
      - **12.5.1** [x] Persist a sanitized failure projection containing stable internal code, failed stage, retry disposition, resource/cost impact, bounded detail, timestamp, and support reference.
      - **12.5.2** [x] Maintain a user-facing error catalog that renders what happened, likely reason, platform action, exact user action, and billing implications without leaking raw AWS/SSM data or IDs.
      - **12.5.3** [x] Make workers populate retry truthfully and make duplicate in-progress requests return the existing operation and progress instead of a generic conflict.
      - **12.5.4** [x] Add owner-bound, guild-bound, action-bound, state-bound, single-use confirmation records with atomic consumption and a 10-minute expiry.
      - **12.5.5** [x] Replace archive/terminate `confirm:true` with a durable `/rb confirm` and `/rb cancel-confirmation` follow-up, revalidating authorization and lifecycle state before queueing work.
      - **12.5.6** [x] Test every known error category, safe unknown-error fallback, redaction, retry wording, cost warnings, confirmation expiry, replay, mismatch, cancellation, and state drift.
    - **12.6** [x] Support safe post-creation modlist revisions.
      - **12.6.1** [x] Replace the single preset pointer with backward-compatible active and pending preset revision metadata plus immutable change events.
      - **12.6.2** [x] Implement `/rb mods` as a private preset-upload modal that validates and stages a revision without interrupting a running server.
      - **12.6.3** [x] Apply a pending revision on the next start, wake, or restore and keep the previous revision authoritative until installation and health verification succeed.
      - **12.6.4** [x] On application failure, retain the failed pending revision for diagnosis and attempt to return the service to the prior known-good mod configuration.
      - **12.6.5** [x] Show active/pending revisions and application timing on the card, and promote the downloadable attachment only after the revision becomes active.
      - **12.6.6** [x] Preserve active/pending intent through archive manifests, restore validation, termination cleanup, audit history, and focused regression tests.
    - **12.7** [x] Add milestone-based workflow progress presentation.
      - **12.7.1** [x] Define stable ordered milestone sets per workflow and persist the current milestone, completed milestones, operation start time, and last-progress time without raw command output.
      - **12.7.2** [x] Expose meaningful internal bootstrap checkpoints instead of treating the entire managed-node command as one opaque milestone.
      - **12.7.3** [x] Define deterministic completion, skip, replay, retry, rollback, failure, and cancellation semantics so milestone progress never falsely advances or moves backward.
      - **12.7.4** [x] Render a text-based completed-checkpoint bar, `Step X/Y`, current stage, and elapsed time on the public card and ephemeral status without presenting a time-completion percentage or ETA.
      - **12.7.5** [x] Show concise qualitative stage guidance and distinguish active, waiting, stalled, retrying, rollback, completed, and action-required progress while retaining milestone-only rate-limited card edits.
      - **12.7.6** [x] Test every workflow's milestone ordering, bootstrap checkpoints, skipped stages, replay, retries, rollback, failures, cancellation, clock anomalies, rendering bounds, and update rate limiting.
    - **12.8** [x] Finish admin, help, and release polish.
      - **12.8.1** [x] Consolidate administration under direct `/rb admin` as a permission-checked component menu for access and card repair only; replace or confirm removal of allowed roles, and recheck Administrator or Manage Server on every admin interaction.
      - **12.8.2** [x] Omit the wishlist Discord cost command entirely so the Lambda makes no Cost Explorer request and needs no Billing permission.
      - **12.8.3** [x] Keep budget alerts as an AWS operator concern and leave the existing Phase 5 Terraform budget guardrail unchanged pending any separately reviewed ownership migration.
      - **12.8.4** [x] Add `/rb help`, first-run guidance, state-aware next actions, and useful empty/success responses without duplicating the operational runbook.
      - **12.8.5** [x] Apply guild-only contexts, least-visible admin command defaults, mobile-safe layouts, concise controls, text-plus-color states, and graceful stale-interaction handling.
      - **12.8.6** [x] Update deployment/runbook documentation.
      - **12.8.7** [x] Make one `/rb start` orchestrate ready-session infrastructure provisioning through bootstrap and playable health acceptance; keep `/rb create` non-billable and return existing progress for repeated starts instead of requiring a second command.
      - **12.8.8** [x] Prevent bootstrap worker package/configuration drift, report the exact startup mismatch, and add a non-mutating deployment verification path before workflow retry.
      - **12.8.9** [x] Grant the Discord interaction role the DynamoDB condition-check permission required by atomic archive and termination confirmations.
      - **12.8.10** [x] Preserve Discord role context when confirmed archive and termination commands cross the asynchronous command boundary.
      - **12.8.11** [x] Use the cached Steam authorization for the Arma server package in both vanilla and modded bootstrap while keeping vanilla Workshop-free.
      - **12.8.12** [x] Preserve uploaded mission basenames and polish public-card heading order and section spacing.
      - **12.8.13** [x] Require an explicit supported game when opening `/rb create` and preserve that selection through submission.
      - **12.8.14** [x] Require change-specific operator application commands in every state-changing development handoff.
      - **12.8.15** [x] Review and harden the release branch with recoverable bootstrap continuation delivery, renewable Steam authorization leases, mission filename/config sanitization, and command-schema drift coverage.
    - **12.9** [x] Add an Administrator-only full runtime reset that returns the installed platform to an empty, ready-to-use state without destroying its control plane.
      - **12.9.1** [x] Define and implement one durable, idempotent reset operation with an environment-wide mutation lock, current Discord Administrator authorization, a ten-minute typed confirmation, and a minimal retained audit result. Preserve Terraform-managed infrastructure, guild access, secrets, configuration, and AWS-retained billing/audit history.
      - **12.9.2** [x] Implement bounded cleanup of all platform-owned runtime state: stop active executions, terminate exactly tagged game instances, delete exactly tagged disposable volumes, delete bot-owned session messages, remove every version under the session S3 prefix, purge runtime queues/DLQs, clear reset-scoped DynamoDB records, and remove eligible pre-reset application log streams. Fail closed on incomplete discovery or ownership ambiguity.
      - **12.9.3** [x] Add a streamlined `/rb admin` danger-area flow with an impact preview, typed reset modal, progress/result view, stale/replay protection, truthful partial-failure and billing warnings, and a disabled-by-default deployment gate.
      - **12.9.4** [x] Add focused authorization, confirmation, locking, idempotency, pagination, ownership-boundary, partial-failure, redaction, and preserved-state tests; wire least-privilege Terraform/packaging, update the reset runbook and handoffs, run proportional regression, then commit and push without executing a live reset.
    - **12.10** [x] Let Administrators provide the Arma 3 `server.cfg` used by game-server sessions.
      - **12.10.1** [x] Add a durable guild-level `server.cfg` revision and bounded private artifact-ingest path that accepts only an Administrator-uploaded UTF-8 `.cfg` file, stores it outside session prefixes, and never renders its contents because it may contain server passwords.
      - **12.10.2** [x] Add a streamlined `/rb admin` configuration area to upload, replace, inspect metadata for, or remove the active file with fresh Administrator checks, confirmation for removal, idempotency, stale-revision protection, and safe errors.
      - **12.10.3** [x] Capture the active configuration revision when start/bootstrap begins and deploy that exact object as Arma `server.cfg`; retain the existing generated safe default when no admin file is configured and preserve deterministic replay for in-progress sessions.
      - **12.10.4** [x] Add focused authorization, validation, redaction, persistence, bootstrap/default, replacement/removal, and replay tests; wire least-privilege S3 access, update documentation and handoffs, run proportional regression, then commit and push.
    - **12.11** [x] Simplify destructive follow-up confirmation without weakening its safety contract.
      - **12.11.1** [x] Replace user-entered codes with one durable pending archive/terminate confirmation slot per user and guild; resolve `/rb confirm` and `/rb cancel-confirmation` server-side while retaining owner/guild/action/state binding, ten-minute expiry, atomic consumption, idempotency, and replay protection.
      - **12.11.2** [x] Remove code options and code text from the Discord contract, responses, errors, documentation, and raw-payload tests; give clear guidance when no matching pending action exists or another action is already pending.
      - **12.11.3** [x] Run focused and proportional regression, update handoffs, then create and push one scoped Conventional Commit.
    - **12.12** [x] Complete the final cross-step release review and prepare the Phase 12 pull request.
      - **12.12.1** [x] Integrate the current `main` bootstrap hardening without regressing Phase 12 reset, configuration, or confirmation behavior.
      - **12.12.2** [x] Review and fix authorization, destructive atomicity, replay/idempotency, pagination, metadata preservation, private-artifact integrity/redaction, retry disposition, least privilege, and Discord UX findings.
      - **12.12.3** [x] Run focused and full proportional Go, Terraform, command-contract, Bash-artifact, packaging, and diff validation.
      - **12.12.4** [x] Refresh the release handoff, create and push the scoped review commit, and provide the proposed pull-request name and description without deploying or opening the PR.
      - **12.12.5** [x] Fix reset workflow ARN collection for the `for_each` lifecycle state machines and validate the Terraform configuration before replanning.
      - **12.12.6** [x] Remove the reserved `AWS_REGION` override from the reset worker Lambda configuration and rely on Lambda's runtime-provided region.
      - **12.12.7** [x] Remove stale confirmation-code deployment guidance and make archive/terminate responses explicitly direct optionless `/rb confirm` within ten minutes.
    - **12.13** [x] Add shared mod options to creation and existing-session revision flows.
      - **12.13.1** [x] Define the supported Arma 3 Creator DLC catalog and add validated, deterministic, backward-compatible session configuration persistence and audit data.
      - **12.13.2** [x] Split creation into base setup plus a conditional mod-options continuation, capture a durable `Begin server setup` intent, and make `/rb mods` open the same preset/cDLC page with stale-state, authorization, and idempotency safeguards.
      - **12.13.3** [x] Honor begin-setup intent after required artifact validation; treat the form as authoritative for cDLCs by stripping preset DLC/footer sections; support cDLC-only or Workshop+cDLC bootstrap and revisions; preserve selections through archive/restore; and render concise status/card details.
      - **12.13.4** [x] Preserve verified Discord role context through immediate and artifact-delayed automatic starts so the existing worker access policy authorizes them without an owner bypass.
      - **12.13.5** [x] Make cDLC-only bootstrap tolerate Steam's absent optional Workshop directory while retaining resumable checkpoints and the shared mod path.

13. **Product Expansion Foundations — Done**
    - **13.2** [x] Add optional and multiple Arma 3 mission files through a consolidated session-edit experience, while preserving a clean path to later mission rotation.
      - **13.2.1** [x] Replace the required single-mission projection with backward-compatible immutable mission records and atomic configured/current projections; make `MP_ZGM_m12.Stratis` the built-in configured default when no accepted upload is selected, preserve artifact history through archive/restore/termination, and prevent removal of the currently loaded mission.
      - **13.2.2** [x] Replace `/rb mods` with `/rb edit session:<slug> section:<mods|mission-files>` using a game-aware section contract; retain the existing preset and Creator DLC flow, and add a private five-per-page mission manager with validation state, `Default`, `Remove`, `Add mission`, and pagination controls plus a bounded `.pbo` upload modal.
      - **13.2.3** [x] Snapshot the configured mission at each start, wake, or restore; deploy the exact accepted upload or fall back to `MP_ZGM_m12.Stratis`; and always replace the effective `class Missions` block with the platform-generated selection even when an Administrator-provided `server.cfg` is active, without changing a running server in place.
      - **13.2.4** [x] Add focused domain, persistence, artifact, Discord, bootstrap, archive/restore, authorization, stale-component, idempotency, pagination, and compatibility coverage; update command registration, user/operator documentation, and deployment handoffs, then run proportional Go, Terraform, command-contract, Bash-artifact, packaging, and diff validation.
      - **13.2.5** [x] Make the mission-block rewrite portable to Ubuntu's default AWK by avoiding built-in function names as local parameters.
      - **13.2.6** [x] Keep zero-object termination results present in the Lambda contract so Step Functions can finalize empty sessions without leaving stale deletion locks.
      - **13.2.7** [x] Avoid the AWK built-in `close` name in mission-rewrite locals so configuration deployment runs on Ubuntu's default `mawk`.
      - **13.2.8** [x] Remove stale saved plans from the active development environment while preserving the current reviewed plan and recoverable local history.
    - **13.7** [x] Publish a self-hosted Discord bot deployment guide.
      - **13.7.1** [x] Document Discord application setup, AWS/Terraform deployment, secret installation, command registration, verification, and safe provisioning enablement; link the guide from the README.

14. **Automatic Inactivity Lifecycle — Complete**
    - Deliver the first conservative fixed-policy foundation: sleep a running
      session after 30 continuous minutes with no players, then archive it after
      72 continuous hours sleeping. Defer configurable policies, richer activity
      signals, warnings/cancellation, schedules, and extensions to later work.
    - **14.1** [x] Persist trustworthy inactivity evidence and transition timing from bounded player-count observations; treat missing, stale, malformed, or failed observations as unknown and never as zero players.
      - **14.1.1** [x] Define domain-level player-activity observation and continuous zero-player timing semantics, including explicit unknown-data behavior and reset rules.
      - **14.1.2** [x] Persist the inactivity evidence backward-compatibly through the session repository and immutable event history.
      - **14.1.3** [x] Feed bounded monitor player-count results into the domain model without interpreting query failures as zero players.
      - **14.1.4** [x] Add focused domain, monitor, and persistence tests for zero-player continuity, player return, unknown observations, replay, and legacy records.
    - **14.2** [x] Extend the scheduled monitor to idempotently request the existing sleep workflow after 30 continuous verified zero-player minutes, while respecting lifecycle state, active workflow locks, and concurrent player activity.
      - **14.2.1** [x] Define a fixed 30-minute automatic-sleep eligibility policy and deterministic request identity at the domain/application boundary.
      - **14.2.2** [x] Route eligible monitor results through the existing command and sleep-workflow safeguards using explicit system authority and least-privilege queue access.
      - **14.2.3** [x] Revalidate persisted inactivity, lifecycle state, and workflow-lock state at command consumption so player return or concurrent mutation fails closed.
      - **14.2.4** [x] Add focused due/not-due, replay, player-return, unknown-observation, state-drift, lock, and queue-failure tests and proportional Terraform validation.
    - **14.3** [x] Add a scheduled, idempotent 72-hour sleeping-state check that requests the existing verified archive workflow without weakening its resource-ownership, backup-integrity, or failure safeguards.
      - **14.3.1** [x] Persist an explicit sleeping-since timestamp across sleep completion and clear it on exit from the sleeping lifecycle, with backward-compatible repository handling.
      - **14.3.2** [x] Define fixed 72-hour automatic-archive eligibility, deterministic request identity, and system-authority revalidation at command consumption.
      - **14.3.3** [x] Extend scheduled inactivity scanning and the archive workflow to safely prepare a sleeping managed instance for the existing SSM backup and verified destruction stages.
      - **14.3.4** [x] Add focused due/not-due, replay, state-drift, workflow-lock, wake-for-archive, integrity-guard, queue-failure, and Terraform validation coverage.
    - **14.4** [x] Add focused timing, state-drift, replay, concurrency, unknown-activity, workflow-failure, persistence, notification/card, Terraform, and end-to-end regression coverage; document the fixed policy and the deferred expansion points.
      - **14.4.1** [x] Review the complete Phase 14 branch across domain, monitoring, workflow, persistence, and infrastructure boundaries; correct cross-step lifecycle or fail-closed gaps.
      - **14.4.2** [x] Add cross-layer regression coverage for automatic command handoff, stale or unknown activity, replay, concurrent mutation, workflow-start failure, and session-card notifications.
      - **14.4.3** [x] Document the fixed 30-minute sleep and 72-hour archive policy, operational failure behavior, and deferred richer activity, warning, cancellation, scheduling, and configuration features.
      - **14.4.4** [x] Run full Go, race/coverage, build, package, command-definition, bootstrap-script, and Terraform checks; review the final branch diff and refresh the phase handoff.
    - **14.5** [x] Add an MVP capacity preflight for start and wake requests while keeping session creation unrestricted; defer administrator-configurable active-session limits.
      - **14.5.1** [x] Expose a consistent repository capacity-availability check backed by the existing provisioned-session slots.
      - **14.5.2** [x] Reject start and wake before queue dispatch when the single slot belongs to another session, and return a concise Discord capacity message.
      - **14.5.3** [x] Add focused available, same-session, occupied-session, creation, and adapter regressions; run proportional validation and refresh the Phase 14 handoff.

18. **Miscellaneous Fixes and Quality of Life — Complete (approved out of order)**
    - Deliver on `codex/misc-fixes` by extending existing Discord, artifact,
      bootstrap, and SSM paths. Avoid new workers, queues, state machines, or
      persistent services unless implementation evidence shows they are needed.
    - **18.1** [x] Polish existing Discord interaction layouts without changing their safety or authorization contracts.
      - **18.1.1** [x] Group each `/rb edit` mission-file name and its controls on one row, move `Add mission` to the bottom, retain five-file pagination, and add focused rendering coverage.
    - **18.2** [x] Make setup and automatic lifecycle timing more informative through the existing Discord presentation paths.
      - **18.2.1** [x] Reframe bootstrap milestones around active work so host preparation no longer occupies most of setup; expose distinct game-file and Workshop installation stages while preserving deterministic replay, retries, and skipped-stage behavior.
      - **18.2.2** [x] Emit only bounded, allowlisted SteamCMD activity from the existing bootstrap/SSM progress protocol and render the latest safe target on the public card with existing rate limits; suppress raw output, credentials, authentication state, arbitrary paths, and untrusted names, and degrade cleanly when no signal is available.
      - **18.2.3** [x] Show Discord-native projected sleep or archive times in `/rb status` from authoritative `idle_since` or `sleeping_since` evidence and the existing policy thresholds; omit projections when state or evidence is unknown, interrupted, or inapplicable.
      - **18.2.4** [x] Add focused milestone, activity-redaction, replay, stale-output, inactivity-state, clock-anomaly, fallback, and Discord-bound regression coverage without adding monitoring infrastructure.
    - **18.3** [x] Support Arma 3 server-only mods through the existing session mod-management and bootstrap architecture.
      - **18.3.1** [x] Extend creation and `/rb edit session:<slug> section:mods` to accept a separately identified server-mod preset, reusing the existing private upload, validation, revision, authorization, and stale-state safeguards.
      - **18.3.2** [x] Persist server-only Workshop items separately, install them through the existing authenticated resumable SteamCMD path, and generate a deterministic `-serverMod=` launch argument while excluding them from the public card, client modlist artifact, and required client launch parameters.
      - **18.3.3** [x] Preserve server-mod active/pending intent through start, wake, revision, archive, restore, replacement hosts, and termination; add focused persistence, validation, replay, bootstrap, launch-argument, rollback, and backward-compatibility coverage.
    - **18.4** [x] Make newly accepted Arma 3 mission uploads available to a running server without restarting Arma or changing its current mission.
      - **18.4.1** [x] Extend the existing artifact acceptance path to revalidate the running lifecycle, exact managed instance, accepted mission revision, and workflow compatibility before requesting a bounded live copy; treat sleeping, archived, changing, or stale-instance sessions as bootstrap-only synchronization cases.
      - **18.4.2** [x] Reuse the artifact worker and narrowly scoped SSM access to download the exact S3 object to a temporary file, verify its checksum, set `steam:steam` ownership, and atomically place it in `arma3/mpmissions`; use existing retry and failure reporting and do not add a synchronization worker.
      - **18.4.3** [x] Synchronize every accepted active mission during start, wake, restore, and replacement-host bootstrap, and add focused live-copy, idempotency, checksum, state-drift, instance-drift, workflow-conflict, no-restart, and current-mission-preservation coverage.
    - **18.5** [x] Complete the final cross-step branch review and prepare the Phase 18 pull request.
      - **18.5.1** [x] Review Discord mod recovery, artifact replay, lifecycle state, revision authority, private-data boundaries, bootstrap compatibility, IAM scope, and deployment impact; allow first client-preset staging on established cDLC/server-only sessions and bind live-mission replay to the exact normalized filename.
      - **18.5.2** [x] Run full proportional Go, packaging, Bash, Terraform, and diff validation; consolidate the branch deployment handoff and provide the proposed pull-request title and description without deploying or opening the PR.
    - **18.6** [x] Make Terraform budget refreshes use AWS's reachable alternate endpoint.
      - **18.6.1** [x] Route only the existing AWS Budget through a us-east-1 billing provider configured for `budgets.us-east-1.api.aws`, without changing budget policy or other regional resources.
      - **18.6.2** [x] Verify a complete saved plan, refresh the operator handoff, then commit and push the fix.
    - **18.7** [x] Repair live bootstrap progress delivery after development verification showed SSM buffered command output until completion.
      - **18.7.1** [x] Publish a workflow-scoped, allowlisted progress snapshot from the host to the existing encrypted session-assets bucket and read it during the existing 30-second bootstrap poll, retaining SSM output as a terminal fallback.
      - **18.7.2** [x] Emit `HOST_PREPARED` only after host preparation succeeds, and preserve the public game-file and Workshop stage/activity projection without exposing raw SteamCMD output.
      - **18.7.3** [x] Add focused snapshot/key/parser coverage, review IAM and stale-workflow isolation, run proportional branch validation, and refresh the deployment handoff.
    - **18.8** [x] Polish the active public session card without changing private status detail.
      - **18.8.1** [x] Hide the progress field whenever the public card is in the running lifecycle, while retaining progress during setup, wake, restore, failure, and other transitions.
      - **18.8.2** [x] Present the live mission/map in a compact code-block box while keeping player counts and Discord-native session-start timing readable beneath it.
      - **18.8.3** [x] Add focused active/setup rendering coverage, update the deployment handoff, validate, commit, and push.
    - **18.9** [x] Reduce completed termination cards to durable tombstone information.
      - **18.9.1** [x] Render only game/session identity, retained description, and a Discord-native termination timestamp after the lifecycle reaches terminated.
      - **18.9.2** [x] Derive termination time from the completed terminal progress evidence with the tombstone update time as a backward-compatible fallback.
      - **18.9.3** [x] Add focused terminal rendering/timestamp coverage, update the handoff, validate, commit, and push.
    - **18.10** [x] Remove inapplicable public-card controls after termination.
      - **18.10.1** [x] Clear `Show players` and `Refresh` from terminated public cards through an explicit terminal notification contract, including termination, refresh, repair, Discord PATCH, backward-compatibility, and focused regression coverage.

19. **Discord Public Card Channel Configuration — Done**
    - **19.1** [x] Let an authorized guild administrator choose the channel where new public session cards are posted, using the existing `/rb admin` infrastructure.
      - **19.1.1** [x] Add one guild configuration field for the public-card channel ID and expose a channel-selection action through the existing `/rb admin` menu.
      - **19.1.2** [x] Use the configured channel when creating a public card and its linked public modlist message; continue storing the created message references so existing update and repair paths work unchanged.
      - **19.1.3** [x] Add focused admin-authorization, setting persistence, selected-channel delivery, and Discord failure coverage; update the relevant admin documentation and run proportional validation.

20. **Mission Wake Synchronization Repair — Done (approved out of order)**
    - **20.1** [x] Make wake/bootstrap content deployment replay when its exact mission or server-configuration inputs change instead of trusting the stale host-wide completion marker.
      - **20.1.1** [x] Bind the resumable content marker to a deterministic digest of the accepted mission manifest, selected mission, server configuration, display identity, and bootstrap revision; add regression coverage for unchanged replay and changed sleeping-session content.

15. **Maximum Session Duration Guardrails — Pending**
    - **15.1** [ ] Add an admin-configurable maximum session duration with safe defaults, bounded owner warnings, an auditable admin extension path, and enforcement that composes safely with inactivity sleep/archive and active workflow locks.

16. **Production Hardening and Optimization — Pending**
    - **16.1** [ ] Complete least-privilege and threat-model reviews across Discord, AWS, artifacts, workflows, and destructive lifecycle boundaries.
    - **16.2** [ ] Add OIDC deployment, staging, dashboards, alert validation, and tested production and disaster-recovery runbooks.
    - **16.3** [ ] Verify costs, quotas, failure recovery, backup restoration, and operational readiness against explicit release gates.
    - **16.4** [ ] Benchmark bootstrap throughput end to end using Steam, CPU, ENA, instance, and EBS measurements, then optimize only demonstrated bottlenecks within cost and reliability guardrails.
    - **16.5** [x] Reduce AWS orchestration overhead without weakening bounded lifecycle behavior, progress visibility, or recovery safeguards.
      - **16.5.1** [x] Enforce the persisted bootstrap command deadline in the existing observer and remove counter-only workflow states while preserving terminal SSM results, rollback, replay, and backward compatibility.
      - **16.5.2** [x] Publish bounded Workshop item ID/position snapshots and explicit shell stages; use 120-second Arma/Workshop installation polling with 30-second fallback and deadline capping; validate download/cache/retry behavior.
      - **16.5.3** [x] Move provisioning poll counters into existing observers while preserving 15-second cadence and 40-observation limits. Retain persisted Refresh behavior because live overlays would require separate persistence and ordering integration.
      - **16.5.4** [x] Validate the combined authorized changes, document deployment and progress semantics, and prepare the scoped commit and branch push without deploying.

17. **Potential Enhancements — Pending**
    - **17.1** [ ] Evaluate scheduling and operational analytics using the established admin and presentation contracts.
    - **17.2** [ ] Add games only after extracting stable game-specific configuration, artifact, bootstrap, health, and presentation capabilities; extend `/rb create` beyond Arma 3 through explicit game-specific setup contracts.
    - **17.3** [ ] Reevaluate a web UI, multi-account, and multi-region support only against demonstrated product or operational requirements.
    - **17.4** [ ] Provide options of different EC2 instance types (weaker or more powerful options with explanations).
    - **17.5** [ ] Let administrators configure the active-session capacity limit while preserving atomic slot enforcement and clear start/wake feedback.
    - **17.6** [x] Establish one safe, reusable Steam Workshop source-resolution boundary for Arma 3 missions and mods supplied as either individual items or collections.
      - **17.6.1** [x] Define backward-compatible Workshop source, requested target (`mission` or `mods`), immutable resolution snapshot, provenance, digest, lifecycle, audit, and mixed-collection child-classification contracts without weakening existing uploaded mission or preset revisions.
      - **17.6.2** [x] Add a bounded Steam metadata client that accepts only canonical public Workshop URLs, verifies Arma 3 consumer app `107410`, distinguishes individual items from collections, normalizes tags, expands one collection level into deterministic children, and classifies unavailable, private, malformed, nested, cross-game, rate-limited, and transient responses.
      - **17.6.3** [x] Integrate Workshop submission into the existing authorized `/rb create` and `/rb edit` flows with mutually exclusive upload-or-link inputs, idempotency, stale-state protection, asynchronous progress, and actionable sanitized errors.
      - **17.6.4** [x] Add focused URL, metadata, tag, collection-expansion, authorization, replay, state-drift, retry-disposition, redaction, and legacy-record coverage before enabling host downloads.
    - **17.7** [x] Resolve public multiplayer Workshop scenarios, supplied individually or by collection, into the existing immutable mission-file lifecycle.
      - **17.7.1** [x] Apply the shared resolver with target `mission`: require each accepted Arma 3 child to be tagged `Scenario` and `Multiplayer` or `Coop`, exclude other classified children with item-specific feedback, and retain downloaded-content validation as the final authority.
      - **17.7.2** [x] Extend the existing serialized authenticated SteamCMD bootstrap path to process each accepted item through the same item-scoped staging operation, retry only transient failures, validate safe paths and size limits, require one unambiguous deployable `.pbo` per item, and isolate partial collection failures without changing the current mission.
      - **17.7.3** [x] Checksum and snapshot every accepted `.pbo` into the existing session-assets bucket, then attach each through the current content-addressed mission records and live/bootstrap synchronization paths so a scenario collection adds mission-manager choices while publisher changes or deletion cannot alter wake, restore, or replacement-host behavior.
      - **17.7.4** [x] Preserve item and parent-collection provenance through mission management, cards/status, archive/restore, termination, and immutable events; never automatically change the configured/current mission, and add focused mixed-collection, partial-failure, Steam authentication, ambiguity, checksum, replay, and compatibility coverage.
    - **17.8** [x] Resolve public Workshop mods, supplied individually or by collection, into the existing pending/active mod-revision lifecycle as an alternative to an uploaded Launcher preset.
      - **17.8.1** [x] Apply the shared resolver with target `mods`: accept classified public Arma 3 client-mod children, deduplicate them, exclude scenarios and other child types with item-specific feedback, and define explicit handling for server-only items without rejecting an otherwise valid mixed collection.
      - **17.8.2** [x] Process every accepted mod through the same authenticated SteamCMD item operation and generate the sanitized internal preset and public modlist artifacts used by uploaded presets, including the source item or collection ID, complete classified child snapshot, resolution timestamp, digest, and explicit excluded-item summary.
      - **17.8.3** [x] Stage, apply, promote, roll back, display, archive, restore, and terminate collection-backed revisions through the existing mod workflow; require an explicit refresh to resolve later collection changes into a new pending revision.
      - **17.8.4** [x] Add least-privilege S3/IAM and timeout changes only where required, stable Workshop error catalog entries and observability, and focused collection mutation, ordering, bounds, SteamCMD, rollback, replay, and backward-compatibility coverage.
      - **17.8.5** [x] Complete cross-step security and operational review, update user/operator documentation and deployment handoffs, run proportional Go, packaging, Bash, Terraform, command-contract, and diff validation, then create and push scoped commits and prepare the Phase 17 pull request without deploying.
      - **17.8.6** [x] Repair live Steam metadata decoding after verification showed `file_size` is string-encoded, preserving numeric compatibility and preventing a retrying mission message from blocking the same session's queued mod resolution.
      - **17.8.7** [x] Remove standalone public Workshop acceptance messages and expose accepted mission-source state through the authoritative `/rb status` projection; do not persist interaction tokens or send direct messages.
      - **17.8.8** [x] Persist Steam's canonical scenario filename and expected size in new immutable mission resolutions, with strict decoding, normalization, digest, and backward-compatibility safeguards.
      - **17.8.9** [x] Accept one regular `.pbo` or numeric `*_legacy.bin` scenario payload during bootstrap, stage it under the recorded canonical `.pbo` filename, and retain existing size, checksum, symlink, ambiguity, and atomic-placement guards.
      - **17.8.10** [x] Project actionable legacy-record and payload-layout failures, assess whether Workshop mods require equivalent handling, and run focused cross-layer validation.
    - **17.9** [x] Synchronize resolved Workshop missions and mods on managed hosts when requested, using one bounded content path for items and collections without adding a polling state machine.
      - **17.9.1** [x] Tighten the shared resolution contract and lifecycle policy: reject collections with more than 50 direct children before child metadata lookup, revalidate the same ceiling in persisted snapshots and host requests, retain the existing per-target item limits, reject archived/destructive lifecycle edits, and define stable behavior for draft, provisioning, running, idle, sleeping, waking, restoring, failed, and deleted sessions.
      - **17.9.2** [x] Refactor the existing bootstrap Workshop logic into one target-aware, idempotent content-sync command used by initial bootstrap, wake/restore, and live requests; accept an immutable resolution digest, process item and collection children identically, serialize SteamCMD per host, enforce disk/runtime/size bounds, and return a bounded per-item result manifest without duplicating mission and mod download implementations.
      - **17.9.3** [x] Isolate live downloads from active server content: use workflow-scoped Steam staging, validate publisher identity, paths, type, filename, size, checksum, and available disk before promotion; atomically copy successful scenarios into `mpmissions` without selecting or restarting them, and retain validated mod content as a pending revision that cannot affect active launch arguments or files until a controlled restart.
      - **17.9.4** [x] Add an SSM-backed `WorkshopContentSync` workflow through the existing session lock, workflow record, artifact worker, and reliability worker; store the command ID on the workflow, identify callbacks with a strict platform-owned SSM comment, finalize terminal commands from one EventBridge rule, and use the existing scheduled active-workflow reconciliation as the missed-event fallback without adding a Lambda, queue, table, bucket, GSI, schedule, or Step Functions state machine.
      - **17.9.5** [x] Generalize the existing wake `DispatchMods` stage into one `DispatchContent` stage that applies both pending Workshop missions and mod revisions before health verification, preserving immutable in-flight inputs, rollback authority, existing polling cadence, and transition count; make initial bootstrap and restore consume the same content-sync contract.
      - **17.9.6** [x] Complete every user touch point: make `/rb create` defer resolved content to initial bootstrap, make `/rb edit` start live sync only for stable running/idle hosts and otherwise give precise queued or blocked behavior, keep acceptance private, and project resolving, queued, downloading, validating, available, awaiting restart, excluded-item summaries, and actionable terminal failures through `/rb status` and the existing session card rules.
      - **17.9.7** [x] Harden replay and failure recovery with digest-bound idempotency, one active content operation per session and host, stale instance/workflow/publisher checks, safe retry of the exact snapshot, recovery or cancellation when SSM dispatch succeeds before command-ID persistence, a durable metadata-resolution marker, a verified wake-time restart after mod promotion, bounded staging cleanup, termination and lifecycle-race handling, redacted Steam/authentication failures, and explicit user remedies for collection size, disk capacity, visibility, removed items, metadata drift, authentication, timeout, and individual download failures.
      - **17.9.8** [x] Review the streamlined architecture and infrastructure diff, document operating behavior and cost/performance safeguards, add focused item/collection and lifecycle matrix coverage plus EventBridge-loss reconciliation tests, run proportional Go, packaging, Bash, Terraform, Discord-contract, and diff validation, then commit and push the Phase 17 branch and refresh the pull-request handoff without deploying.
    - **17.10** [x] Repair Workshop result-manifest publication discovered during live verification.
      - **17.10.1** [x] Grant the managed game instance least-privilege write access to workflow-scoped Workshop result JSON objects.
      - **17.10.2** [x] Emit and present a stable actionable failure when result publication fails instead of exposing raw AWS diagnostics.
      - **17.10.3** [x] Add focused policy, bootstrap, and failure-presentation regression coverage and refresh the deployment handoff.
    - **17.11** [x] Repair bootstrap Workshop mission finalization and complete an end-to-end architecture hardening pass.
      - **17.11.1** [x] Import a validated Workshop mission manifest as one atomic domain mutation so bootstrap completion retains the strict single-version transaction invariant.
      - **17.11.2** [x] Add repository-realistic regression coverage for item and collection bootstrap completion, replay, malformed manifests, and unchanged mission selection.
      - **17.11.3** [x] Review submission through lifecycle replay, live synchronization, result publication, promotion, failure recovery, status, IAM, and cleanup; fix only demonstrated correctness or duplication gaps.
      - **17.11.4** [x] Run proportional Go, Bash, packaging, Terraform, command-contract, and diff validation; refresh the deployment and test-33 recovery handoff, then commit and push.
    - **17.12** [x] Repair the Workshop content-sync shell boundary found during `test-33` wake verification.
      - **17.12.1** [x] Preserve the generated Bash shebang as the first command line when adding Workshop operation variables, so AWS Run Command cannot fall back to POSIX `sh` before content synchronization begins.
      - **17.12.2** [x] Preserve actionable Workshop failure codes, add focused command-generation and wake-observation coverage, run proportional validation, and refresh the deployment and safe `test-33` recovery handoff.
    - **17.13** [x] Show accepted Workshop scenarios in the mission editor before host synchronization finishes.
      - **17.13.1** [x] Project deduplicated unresolved Workshop scenario items alongside finalized mission records as non-actionable awaiting-download entries.
      - **17.13.2** [x] Add focused pagination, promotion, collection, and backward-compatibility coverage; validate and refresh the deployment handoff.
    - **17.14** [x] Normalize Steam Workshop links copied with trailing query parameters.
      - **17.14.1** [x] Retain one valid Workshop `id`, discard all other query parameters, and persist the existing canonical URL while preserving host, path, scheme, fragment, and duplicate-ID safeguards.
      - **17.14.2** [x] Validate mod Workshop links before mutating session options, return an actionable private error for malformed links, and add focused regression coverage.
    - **17.15** [x] Accept Steam's public collection URL form.
      - **17.15.1** [x] Accept `/workshop/filedetails/` collection links at every shared Workshop entry point and normalize them to the existing canonical shared-file URL.
      - **17.15.2** [x] Add focused parser and `/rb edit` mod submission coverage, validate the affected components, and refresh the deployment handoff.
    - **17.16** [x] Repair live Workshop collection resolution and start feedback without supporting nested collections.
      - **17.16.1** [x] Detect root collections through Steam collection metadata, retain direct-child type data, and exclude nested collection children without recursively expanding them.
      - **17.16.2** [x] Harden Workshop SQS decoding diagnostics and terminal cleanup so malformed or rejected requests cannot leave a session indefinitely resolving.
      - **17.16.3** [x] Return actionable private `/rb start` feedback while Workshop metadata resolution is pending or the draft lacks an accepted mod revision.
      - **17.16.4** [x] Add focused Steam adapter, resolver, worker, session, and Discord regression coverage; validate, refresh the deployment handoff, commit, and push.
    - **17.17** [x] Make Workshop metadata resolution fast, status-driven, and free of standalone public error messages.
      - **17.17.1** [x] Decouple completed metadata persistence from best-effort card/modlist notification delivery so notification failures cannot trigger nine-minute SQS visibility retries or repeat Steam calls.
      - **17.17.2** [x] Remove standalone Workshop result/error notifications; persist a bounded last-resolution outcome and project its summary on the public card with actionable detail in ephemeral `/rb status`.
      - **17.17.3** [x] Add an offline end-to-end item/collection simulation using the observed public metadata shape for `3368879130`, covering seven direct mods, generated artifacts, lifecycle gating, and host-sync dispatch boundaries without AWS mutation or SteamCMD download.
      - **17.17.4** [x] Review all Workshop terminal paths for idempotency and latency, run proportional validation, refresh the deployment handoff, commit, and push.
    - **17.18** [x] Restore the single-version persistence invariant for Workshop mod resolution and eliminate hidden deterministic queue retries.
      - **17.18.1** [x] Make initial and replacement Workshop mod attachment one atomic domain mutation that advances the session exactly once while retaining existing revision, marker, provenance, and lifecycle rules.
      - **17.18.2** [x] Classify and log recorder failures at the worker boundary, immediately persist actionable status for deterministic failures, and reserve SQS retries for bounded external persistence failures.
      - **17.18.3** [x] Add domain, recorder, repository-realistic, and worker regression coverage for draft and established revisions, replay, stale state, and exact-marker cleanup.
      - **17.18.4** [x] Review the affected Workshop path against the branch architecture, run proportional validation, refresh the deployment/recovery handoff, commit, and push without adding infrastructure.
    - **17.19** [x] Skip already-materialized Workshop content during ordinary wake operations.
      - **17.19.1** [x] Derive pending Workshop scenarios from immutable source and accepted-mission evidence, dispatch only unresolved or refreshed scenario items alongside genuinely applying mod revisions, and add focused wake/manifest regression coverage plus deployment handoff validation.
    - **17.20** [x] Complete the Workshop content-source branch release-readiness review and pull-request handoff.
      - **17.20.1** [x] Review the complete branch diff for security, lifecycle correctness, bounded cost/performance, failure recovery, backward compatibility, duplication, and repository architecture; correct concrete gaps with focused coverage.
      - **17.20.2** [x] Reconcile user, operator, architecture, security, deployment, and roadmap documentation with the final implemented behavior and known operational constraints.
      - **17.20.3** [x] Run full proportional validation, refresh the exact deployment handoff, commit and push the wrap-up, and prepare the Phase 17 pull-request title and description without deploying or merging.
    - **17.21** [x] Repair Workshop mod visibility on the managed host after live verification.
      - **17.21.1** [x] Give the unprivileged Arma service read/traverse access to root-owned Workshop revision parents without granting write access, cover the permission contract, validate and document the correction, then commit and push it.
    - **17.22** [x] Repair live Workshop mission synchronization after `test-43` exposed a target-contract mismatch.
      - **17.22.1** [x] Align the bootstrap mission target with the domain's canonical singular value, add focused live-dispatch coverage, validate the affected path, refresh the deployment handoff, then commit and push the correction.
    - **17.23** [x] Remove the fixed heartbeat-tail latency from authenticated Steam operations.
      - **17.23.1** [x] Make the Steam authorization lease heartbeat promptly and safely interruptible across its normal and retry waits, preserve owner-checked release and parent-failure behavior, add focused regression coverage, validate the affected bootstrap path, and refresh the deployment handoff.
    - **17.24** [x] Reconcile Workshop documentation after the merged live verification fixes.
      - **17.24.1** [x] Correct stale host-target and deployment wording, document prompt authorization-heartbeat shutdown, and align the README and mission-management guide with Workshop-backed scenarios.
