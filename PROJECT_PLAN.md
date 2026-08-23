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

11. **Production Hardening — Pending**
    - **11.1** [ ] Complete least-privilege and threat-model reviews.
    - **11.2** [ ] Add OIDC deployment, staging, dashboards, and tested runbooks.
    - **11.3** [ ] Verify costs and operational readiness.

12. **Discord Experience and Session UX — Done (approved reorder; Phase 11 remains pending)**
    - Proceeded before Phases 10 and 11 by explicit user approval because of
      current limitations. Phase 10 is now complete; the reorder does not waive Phase 11.
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

13. **Expansion and Optimization — Pending**
    - **13.1** [ ] Benchmark and optimize bootstrap throughput end to end using Steam, CPU, ENA, instance, and EBS measurements with cost guardrails.
    - **13.2** [ ] Add multiple mission uploads and safe in-game mission rotation with artifact history and authorization.
    - **13.3** [ ] Add an admin-configurable maximum session duration, owner warnings, and auditable admin extension.
    - **13.4** [ ] Evaluate scheduling and operational analytics using the Phase 12 admin and presentation contracts.
    - **13.5** [ ] Add games only after extracting stable game-specific configuration, artifact, bootstrap, health, and presentation capabilities; extend `/rb create` beyond its required Arma 3 choice and route each supported game into its game-specific setup contract.
    - **13.6** [ ] Reevaluate a web UI, multi-account, and multi-region support only against demonstrated product or operational requirements.
