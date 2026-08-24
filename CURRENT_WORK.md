# Current Work

## State and Objective

Phases 1-10, 12-14 are complete. Phase 14 is complete on
`codex/phase-14-inactivity-lifecycle`; the branch is ready for pull-request
review. No deployment, Terraform apply, or Discord registration was performed.

## Completed Development

- Scheduled monitoring persists bounded A2S player-count observations and an
  immutable evidence trail. Positive or unknown observations reset continuous
  zero-player time; query failure is never treated as zero players.
- A fresh zero-player observation after 30 continuous idle minutes enqueues a
  deterministic system sleep command. Consumption reloads the session and
  revalidates the bound idle window, evidence freshness, lifecycle state, and
  workflow lock before using the existing guarded sleep workflow.
- Successful sleep records `sleeping_since`. A session still sleeping after 72
  continuous hours receives a deterministic system archive command with the
  same replay and state-drift safeguards.
- Automatic archive starts the retained tagged instance, waits up to ten
  minutes for EC2 and Systems Manager readiness, and then reuses the existing
  verified archive, checksum, resource-ownership, and destruction boundaries.
- DynamoDB decoding remains compatible with sessions created before the
  inactivity fields existed. Transition paths retain or clear timing evidence
  according to their lifecycle invariants.
- The monitor has scoped send access to the existing command FIFO. The archive
  worker has scoped EC2 start and Systems Manager readiness permissions.
- Cross-layer regression coverage now exercises monitor-to-command-to-workflow
  handoff, deterministic replay, concurrent/state-drift rejection, unknown
  evidence, queue and workflow-start failures, sleeping-state restoration,
  persistence, and session-card notifications.
- `docs/phase-14-inactivity-lifecycle.md` documents the fixed policy, timing,
  fail-closed behavior, operational impact, and deferred configurable,
  corroborating-signal, warning/cancellation, schedule, extension, and rest
  features. Phase 8/9 documentation and the README reflect the new behavior.

## Validation

- `go test -cover ./...`, `go vet ./...`, and `go build ./cmd/...` pass with
  workspace-local caches (Go 1.26.6).
- `go test -race -cover ./...` cannot run in this Windows environment because
  the race detector requires CGO; CI remains the race-validation gate.
- All 13 Lambda archives package successfully.
- `terraform fmt -check -recursive infra/terraform` and
  `terraform -chdir=infra/terraform/environments/dev validate` pass with
  Terraform 1.15.8.
- Discord command JSON validation and `git diff --check` pass.
- Bootstrap Bash syntax could not be rerun because neither Bash nor an installed
  WSL distribution is available. The script is unchanged by Phase 14, and its
  existing Go regression suite passes.

## Next Development Task

- Open and review the Phase 14 pull request. Apply only a newly generated and
  reviewed Terraform plan when the deployment is approved.
- Remaining delivery order is Phase 15 maximum-duration cost guardrails, Phase
  16 production hardening and measured optimization, then Phase 17 potential
  enhancements.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out phase-14-inactivity-lifecycle.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-14-inactivity-lifecycle.tfplan
# Apply only after reviewing and approving this exact saved plan.
terraform -chdir=infra/terraform/environments/dev apply phase-14-inactivity-lifecycle.tfplan
```

The package command rebuilds all Lambda archives, including the changed
monitor, command, sleep/wake, and archive workers. The fresh plan should deploy
those workers, monitor queue access, sleeping-host archive preparation states,
and the archive worker's scoped EC2/Systems Manager permissions. No Discord
command registration is required.
