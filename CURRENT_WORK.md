# Current Work

## State and Objective

The Test B1 creation-time automatic-start race is fixed on
`codex/fix-test-b1-setup-race`, based on `dcb6877` (`main`). Test B1 recovered
without operator mutation and is running; the source correction is not yet
deployed.

## Current Handoff

- Test B1 (`01M2W7PESG2ZQDMZB9GBQF7592`) accepted its Workshop mission at
  2026-09-19 07:04:40 UTC while an earlier automatic-start command raced the
  same session-version update.
- The first workflow transaction lost that optimistic race and was reported as
  `session workflow lock is held`. Later retries rebuilt different event/time
  fields while reusing the workflow ID as DynamoDB's client request token,
  producing `IdempotentParameterMismatchException` until the token window
  expired.
- Creation now explicitly defers automatic start when a Workshop mission link
  is queued. The shared automatic-start boundary also refuses to queue while a
  Workshop resolution marker is pending. The accepted-resolution worker remains
  the sole trigger that starts the ready session.
- Workflow acquisition now binds DynamoDB idempotency to the exact event/attempt
  rather than the reusable workflow ID, so a later optimistic-lock retry can
  safely rebuild its transaction.
- Test B1's original command succeeded at 07:19:39 UTC after the old token
  window expired. Provisioning and bootstrap both succeeded, and the session
  reached `RUNNING` at 08:00:59 UTC. No live repair was performed.
- Focused sessions, DynamoDB repository, Discord interaction, artifact-worker,
  and command-worker tests pass. `go vet ./...`, `go build ./cmd/...`, and
  `git diff --check` pass.
- `go test ./...` still has the pre-existing unrelated failure
  `TestArchiveWorkflowCompletesSuccessfullyAfterDurableCompletion`; all affected
  packages pass.

## Important User Attention

- Deploy this branch before relying on automatic setup for another session that
  uses a Workshop mission link.
- The unrelated archive state-machine security-contract failure remains a
  release-gate concern and was not changed in this focused fix.

## Commands to Apply Current Changes

Package the affected Lambda sources, create and review a fresh Terraform plan,
then apply that exact reviewed plan only after approval:

```powershell
./scripts/package-discord-lambda.ps1
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity
terraform -chdir=infra/terraform/environments/dev init -backend-config=backend.hcl -input=false
terraform -chdir=infra/terraform/environments/dev plan -out=test-b1-setup-race.tfplan
terraform -chdir=infra/terraform/environments/dev show test-b1-setup-race.tfplan
terraform -chdir=infra/terraform/environments/dev apply test-b1-setup-race.tfplan
```

After deployment, create a disposable vanilla session with automatic setup and
a Workshop mission link. Verify that no provisioning workflow starts before
`WorkshopMissionResolved`, then verify one provisioning workflow proceeds to
`RUNNING`.

Discord command registration is not required because command definitions did
not change.
