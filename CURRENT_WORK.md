# Current Work

## State and Objective

Phase 10 is in progress on `codex/phase-10-reliability`. Steps 10.1 and 10.2
are implemented and reviewed. Work is intentionally paused before Step 10.3 at
the user's requested boundary.

## Completed Reliability Work

- Bounded Step Functions retries cover Lambda service, SDK, and throttling
  failures only; application failures retain terminal catch/rollback behavior.
- Owner-requested cancellation is persisted atomically and honored only before
  the workflow's initial external mutation. Termination is not cancellable.
- A scheduled reliability worker reconciles expired workflow locks, inspects
  and idempotently redrives DLQs, records conservative orphan findings, and
  exposes explicit quarantine and cleanup operator actions.
- Orphan cleanup is restricted to fully tagged EC2 instances and detached EBS
  volumes after a 24-hour age gate, 24-hour quarantine, and immediate
  revalidation. Security groups, S3, malformed evidence, and uncertain cases
  remain report-only.
- `docs/phase-10-reliability.md` contains operator and disaster-recovery
  procedures. Terraform defines the worker schedule, redrive allow policies,
  least-privilege runtime policy, and DLQ/Lambda alarms.

## Validation

- Full `go test ./...`, `go vet ./...`, and `go build ./cmd/...` pass.
- Focused reliability, orphan, domain, persistence, and adapter tests pass.
- `terraform fmt -recursive infra/terraform` and development-environment
  `terraform validate` pass. No Terraform apply or live cleanup/redrive action
  was run.

## Operator Attention

- Do not begin Step 10.3 until the user resumes it. It is the cached Steam
  authorization and frozen EBS snapshot work.
- Do not run Terraform apply without the separate plan, budget-recipient, and
  deployment approvals. The existing untracked
  `infra/terraform/environments/dev/tfplan` remains untouched.
- Terraform formatting normalized the ignored local `terraform.tfvars` file;
  no values were displayed or intentionally changed.

## Exact Next Step

Pause. When explicitly resumed, begin task 10.3.1 only.
