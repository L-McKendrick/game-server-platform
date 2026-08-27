# Current Work

## State and Objective

Phase 17.7 Workshop scenario ingestion is complete on
`codex/workshop-content-sources`. Public scenario items and one-level
collections now enter the existing immutable mission-file lifecycle without
automatically changing the configured or currently loaded mission.

## Completed Development

- Mission resolutions accept only Arma 3 `Scenario` items tagged
  `Multiplayer` or `Coop`, retain classified exclusions for mixed collections,
  and persist bounded immutable source snapshots with replay protection.
- The serialized cached-auth SteamCMD path downloads each accepted item in an
  isolated operation, retries only recognized transient failures, and rejects
  authentication failures, symlinks, unsafe metadata, invalid sizes, and
  missing or ambiguous PBO layouts.
- Staging is item/revision scoped and atomic. Successful collection siblings
  remain reusable after another child fails; an incomplete collection never
  publishes its completion manifest or changes mission metadata.
- Original safe PBO filenames are retained. Successful content is checksummed,
  published under content-addressed session-assets keys, copied safely into
  `mpmissions`, and described by a bounded revision manifest.
- Bootstrap completion verifies the manifest against authoritative session
  source intent before appending accepted mission records. Duplicate item
  snapshots are replay-safe, superseded item versions are retained as removed
  history, and configured/current mission selection is untouched.
- Mission records retain Workshop item and parent source provenance. The
  mission manager identifies Workshop choices, archive/restore preserves and
  verifies provenance, and termination removes all objects through the existing
  session-prefix cleanup.
- EC2 and bootstrap-worker IAM now permit only the required mission snapshot
  publication and manifest-read paths.
- Full `go test ./...`, `go vet ./...`, recursive Terraform formatting checks,
  bootstrap shell syntax validation, and diff whitespace checks pass.

## Next Development Task

- Implement **17.8.1** only: apply the shared resolver's `mods` policy and
  define deterministic collection-to-preset classification and feedback.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"

./scripts/package-discord-lambda.ps1
$PlanFile = "workshop-scenario-lifecycle-$((Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')).tfplan"
terraform -chdir=infra/terraform/environments/dev plan -out $PlanFile
terraform -chdir=infra/terraform/environments/dev show $PlanFile
# Apply only after confirming the reviewed plan updates the artifact/bootstrap
# worker packages, bootstrap script object, and narrowly scoped IAM statements
# without unrelated infrastructure mutations.
terraform -chdir=infra/terraform/environments/dev apply $PlanFile
```

No Discord command registration is required because command definitions did
not change.
