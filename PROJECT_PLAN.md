# Project Plan

## Summary and Goal

Build a secure, cost-bounded AWS platform controlled through Discord that provisions, operates, sleeps, archives, restores, and destroys temporary dedicated game servers. Deliver the complete Arma 3 lifecycle first, then reuse the platform for other games.

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
     - **9.4.3** [x] Bootstrap vanilla sessions with anonymous SteamCMD and skip all preset, Workshop, and Steam-secret access.
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
      - **10.3.4** [x] Fail closed on renewed Steam Guard challenges, provide operator-safe guidance, and preserve anonymous vanilla behavior.
      - **10.3.5** [x] Add replacement-host reuse, invalidation, concurrency, cleanup, redaction, archive/restore, and vanilla regression coverage plus the operating runbook.

11. **Production Hardening — Pending**
    - **11.1** [ ] Complete least-privilege and threat-model reviews.
    - **11.2** [ ] Add OIDC deployment, staging, dashboards, and tested runbooks.
    - **11.3** [ ] Verify costs and operational readiness.

12. **Discord Experience and Session UX — In Progress (approved reorder; Phase 11 remains pending)**
    - Proceeded before Phases 10 and 11 by explicit user approval because of
      current limitations. Phase 10 is now complete; the reorder does not waive Phase 11.
    - Detailed design and execution guidance: `docs/phase-12-discord-experience.md`.
    - **12.1** [x] Establish the Discord interaction and presentation foundation.
      - **12.1.1** [x] Replace the `/session` command definition and routing with guild-only `/rb`; retain `/admin` separately and do not ship a compatibility alias.
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
      - **12.5.5** [x] Replace archive/terminate `confirm:true` with `/rb confirm code:<code>` and `/rb cancel-confirmation`, revalidating authorization and lifecycle state before queueing work.
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
    - **12.8** [ ] Finish admin, help, cost, and release polish.
      - **12.8.1** [ ] Expand `/admin` into a permission-checked component menu for access, card repair, costs, and placeholders linked only to implemented policies.
      - **12.8.2** [ ] Add ephemeral `/admin costs` for yesterday, seven-day, and month-to-date platform totals, budget utilization, service breakdown, freshness, caching, and useful setup errors.
      - **12.8.3** [ ] Add optional per-session cost grouping only after cost-allocation tags are active; resolve hidden tag values to name/slug and label all figures as delayed estimates.
      - **12.8.4** [ ] Add `/rb help`, first-run guidance, state-aware next actions, and useful empty/success responses without duplicating the operational runbook.
      - **12.8.5** [ ] Apply guild-only contexts, least-visible admin command defaults, mobile-safe layouts, concise controls, text-plus-color states, and graceful stale-interaction handling.
      - **12.8.6** [ ] Update deployment/runbook documentation and complete unit, integration, packaging, Terraform, Discord registration, and live guild acceptance for the phase.
      - **12.8.7** [ ] Make one `/rb start` orchestrate ready-session infrastructure provisioning through bootstrap and playable health acceptance; keep `/rb create` non-billable and return existing progress for repeated starts instead of requiring a second command.

13. **Expansion and Optimization — Pending**
    - **13.1** [ ] Benchmark and optimize bootstrap throughput end to end using Steam, CPU, ENA, instance, and EBS measurements with cost guardrails.
    - **13.2** [ ] Add multiple mission uploads and safe in-game mission rotation with artifact history and authorization.
    - **13.3** [ ] Add an admin-configurable maximum session duration, owner warnings, and auditable admin extension.
    - **13.4** [ ] Evaluate scheduling and operational analytics using the Phase 12 admin and presentation contracts.
    - **13.5** [ ] Add games only after extracting stable game-specific configuration, artifact, bootstrap, health, and presentation capabilities; add an autocomplete game field to `/rb create` and route the selected supported game into its game-specific setup contract.
    - **13.6** [ ] Reevaluate a web UI, multi-account, and multi-region support only against demonstrated product or operational requirements.
