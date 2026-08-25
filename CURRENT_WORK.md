# Current Work

## State and Objective

The branch `codex/reduce-bootstrap-state-transitions` is based on current
`main`. Tasks 16.5.1 and the explicitly requested side fix 18.10.1 are
complete. Bootstrap keeps its 30-second progress polling but no longer pays for
counter-only Step Functions states on every incomplete poll, and terminated
public cards no longer retain interactive controls.

## Completed Development

- Bootstrap dispatch persists the exact SSM command ID and an absolute command
  deadline derived from `BOOTSTRAP_COMMAND_TIMEOUT_SECONDS`.
- Replayed dispatch returns the persisted command instead of launching a
  duplicate SSM command.
- The observer rejects command drift, honors terminal SSM status at the
  deadline boundary, and returns the existing `ERR_BOOTSTRAP_TIMEOUT` failure
  only when a persisted command remains nonterminal at or after its deadline.
- Workflow persistence remains backward-compatible with records that predate
  command metadata.
- The bootstrap state machine now loops through only wait, observe, and result
  choice states. It removes the initial counter state and two counter-management
  states from every incomplete poll while preserving rollback behavior.
- Terminated-card notifications carry an explicit, backward-compatible control
  suppression flag derived from authoritative `DELETED` session state.
- Discord card edits send `components: []` for terminal tombstones, actively
  removing existing `Show players` and `Refresh` buttons instead of merely
  omitting new components. Termination progress, refresh, and repair requests
  all derive the flag from current session state.

## Review and Validation

- `go test ./...` passes.
- `go vet ./...` passes.
- `go build ./cmd/...` passes.
- `terraform fmt -check -recursive infra/terraform` passes.
- `terraform -chdir=infra/terraform/environments/dev validate` passes.
- `git diff --check` passes with only expected LF-to-CRLF working-tree
  warnings.
- Focused coverage verifies deadline persistence, dispatch replay, command
  drift rejection, nonterminal timeout, terminal SSM precedence at the
  deadline, DynamoDB round trips, and legacy records without command metadata.
- Focused card coverage verifies terminated progress suppresses controls and a
  Discord PATCH contains an explicit empty component array, while existing
  nonterminal control rendering remains covered.

## Next Development Task

- Review measured Step Functions `StateTransition` usage after deployment
  before applying the same observer-owned deadline pattern to restore, wake,
  archive, rollback, or termination loops.
- Verify a development termination edits the durable tombstone in place and
  removes both public buttons.
- Phase 15 remains next in the primary delivery order unless another Phase 16
  optimization is explicitly approved out of order.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"

# Rebuild the Lambda set because the shared notification contract affects card
# producers and the notification delivery worker.
./scripts/package-discord-lambda.ps1

# Create and review a fresh plan. Expect the affected Lambda packages plus any
# bootstrap state-machine/package difference not already deployed.
$PlanFile = "bootstrap-transitions-terminal-controls-$((Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')).tfplan"
terraform -chdir=infra/terraform/environments/dev plan -out $PlanFile
terraform -chdir=infra/terraform/environments/dev show $PlanFile
# Apply only after confirming that the reviewed plan contains no unrelated
# infrastructure mutations.
terraform -chdir=infra/terraform/environments/dev apply $PlanFile

# Verify the deployed bootstrap worker package and runtime configuration when
# the reviewed plan includes it.
./scripts/verify-bootstrap-worker-deployment.ps1

$BootstrapStateMachineArn = terraform -chdir=infra/terraform/environments/dev output -json workflow_state_machine_arns | ConvertFrom-Json | Select-Object -ExpandProperty BootstrapGameServer
aws stepfunctions describe-state-machine --state-machine-arn $BootstrapStateMachineArn
```

No Discord command registration is required. Verify one development bootstrap
completes with normal 30-second progress updates, then terminate a disposable
development session and confirm its existing public message becomes the
minimal tombstone with neither `Show players` nor `Refresh` present.
