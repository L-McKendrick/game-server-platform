# Current Work

## State and Objective

Phase 19.3 is complete on `codex/phase-19-session-host-access`, baseline
`967cc6c` (PR #28). SEC-20-01 is closed for controlled beta admission. Prepare
the reviewed branch for its pull request; do not create the PR from this task.

## Current Handoff

- Managed hosts retain only the reviewed Systems Manager managed policy and have
  no standing S3, Secrets Manager, or Steam-lease DynamoDB access. Existing
  trusted workers issue exact, expiring HTTPS capabilities after checking the
  current session, workflow, instance tags, purpose, and content snapshot.
- Live 19.3.8 acceptance covered modded, vanilla and TeamSpeak bootstrap;
  manifest renewal; live mission copy; Workshop sync; sleep, wake and restart;
  failure diagnostics; verified archive creation; original-resource destruction;
  replacement-host restore; and terminal Steam exchange cleanup.
- The test-67 host passed populated same-guild and synthetic-other-guild
  read/write denials plus 19 constrained-capability checks using its actual
  credentials. Secret and Steam-lease access were denied.
- Archive/restore defects found by test-69 through test-71 were corrected and
  deployed. The deployed ArchiveSession definition now ends its successful
  `Complete` task truthfully. All affected package/runtime verifiers pass.
- Test-67, test-69, test-70 and test-71 are `DELETED`. Test-71 termination
  workflow `1550712481541005322` succeeded; no tagged EC2 instance, EBS volume,
  session S3 object version, or delete marker remains.
- Test-71's completed Steam exchanges have logical delete markers and their
  noncurrent temporary versions remain under the intended three-day lifecycle.
  The current Steam lease belongs to a separate running bootstrap workflow,
  not Phase 19.3 acceptance residue.
- Go 1.26.5 coverage, vet/build, focused regressions, package generation,
  Terraform 1.15.8 fmt/validation, offline IAM checks, deployment verifiers, and
  the user-confirmed Ubuntu CI race run pass.
  No PR was created.

## Beta Preparation

- SEC-20-01 no longer blocks a controlled beta. Continue monitoring failed
  lifecycle cleanup, Steam exchange finalization, capability-renewal failures,
  DLQs, budget alarms, and retained-resource reports during beta.
- Production release remains subject to the open Phase 20 hardening work,
  especially protected OIDC deployment, staging, operational dashboards,
  cost/quota recovery gates, and measured performance work.

## Commands to Apply Current Changes

No Lambda packaging, Terraform apply, Discord command registration, migration,
or live cleanup is required. The final source and deployed environment are
coherent. Open the pull request only when requested.
