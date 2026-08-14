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

10. **Reliability — Pending**
    - **10.1** [ ] Add bounded retries, DLQ operations, reconciliation, and cancellation.
    - **10.2** [ ] Add orphan cleanup and disaster recovery.
    - **10.3** [ ] Automate or streamline Steam authentication for new hosts with a secure, short-lived Steam Guard challenge flow, cached machine authorization where Steam permits it, operator-safe redaction, and automatic cleanup. Never carry Guard codes in Discord, workflow input, Lambda configuration, SSM command text, or persistent logs.

11. **Production Hardening — Pending**
    - **11.1** [ ] Complete least-privilege and threat-model reviews.
    - **11.2** [ ] Add OIDC deployment, staging, dashboards, and tested runbooks.
    - **11.3** [ ] Verify costs and operational readiness.

12. **Expansion — Pending**
    - **12.1** [ ] Add games after the Arma lifecycle is stable.
    - **12.2** [ ] Evaluate scheduling, analytics, web UI, multi-account, and multi-region support.
    - **12.3** [ ] Improve Discord UX with an admin menu, tutorial, user-friendly session names, and requester-visible provisioning progress. Show a measured ETA derived from historical per-stage durations and update it at useful, rate-limited milestones. For actions that need confirmation, instead of doing the confirmation in the same command, require the user to confirm in a follow up command instead.
    - **12.3.2** [ ] Have the /session list command only list non-deleted sessions by default. Add optional command parameter to filter list by status (including terminated). Also, the output for this command should include column headers.
    - **12.4** [ ] Benchmark and optimize bootstrap throughput end to end. Instrument Steam download rate, CPU, ENA allowance/drop counters, instance network/EBS ceilings, and EBS queue/throughput before changing settings; then evaluate supported Steam configuration, safe kernel/socket/ring tuning, instance sizing, explicit gp3 IOPS/throughput, and cache/snapshot/prebake strategies with cost guardrails.
    - **12.5** [ ] Support multiple mission uploads per session and safe in-game mission rotation without requiring game/mod configuration changes; preserve each uploaded mission filename in the Arma server mission directory, retain mission artifact history, and validate ownership/authorization.
    - **12.6** [ ] Add an admin-configurable maximum session duration (default 8 hours), owner warning before expiry, and an auditable admin-only extension action.
