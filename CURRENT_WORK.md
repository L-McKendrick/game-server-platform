# Current Work

## State and Objective

Phase 10 is complete on `codex/phase-10-reliability`. Steps 10.1-10.3 are
implemented and focused-validated. No Terraform apply, live Steam enrollment,
or external cleanup/redrive action was run.

## Completed Reliability Work

- Bounded retries, cooperative pre-mutation cancellation, DLQ inspection and
  redrive, stale-workflow reconciliation, alarms, and retry-safe audit state.
- Conservative orphan detection with age, quarantine, immutable-tag, current
  reference, and pre-delete revalidation gates; uncertain resources remain
  report-only.
- Disaster-recovery procedures for metadata, archives, Terraform state,
  workflows, and retained volumes.
- A versioned Secrets Manager SteamCMD `config.vdf` cache following Valve's
  documented username-only CI login pattern. An MFA-gated local operator role
  owns enrollment, invalidation, and rollback; passwords and Guard codes never
  enter Discord or managed command channels.
- A global DynamoDB lease serializes authenticated downloads. Hosts inject the
  cache only under `/run`, preserve valid updates, fail closed with
  `ERR_STEAM_REAUTH_REQUIRED`, and scrub authentication material before launch,
  exit, archive, and restore. Vanilla sessions remain anonymous and receive no
  authorization-cache identifiers.
- Frozen AMIs/EBS snapshots may retain scrubbed SteamCMD, Arma, and Workshop
  data, but never a signed-in Steam cache.

## Validation

- Focused affected-package Go tests, `go vet`, and command builds pass.
- Bootstrap Bash syntax and operator PowerShell parsing pass.
- Development Terraform formatting and validation pass.
- Diff checks and legacy Steam password-flow searches pass.

## Operator Attention

- Do not run Terraform apply without the separate plan, budget-recipient, and
  deployment approvals. The existing untracked
  `infra/terraform/environments/dev/tfplan` remains untouched.
- A live 15-minute operator enrollment and one controlled modded bootstrap are
  still deployment acceptance exercises; follow `docs/steam-auth-cache.md`.
- The ignored local `terraform.tfvars` remains outside this change.

## Exact Next Step

Await direction between Phase 11.1 production-hardening review and the
previously reordered Step 12.8. Default to Phase 11.1; before development,
break that step into numbered tasks in `PROJECT_PLAN.md`.
