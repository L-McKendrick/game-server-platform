# Current Work

## State and Objective

Phase 17 Workshop content synchronization is complete on
`codex/workshop-content-sources`. Live verification of `test-32` exposed one
deployment gap: the managed game-instance role could write resolved missions
but could not publish the workflow result JSON. The code and infrastructure
repair is implemented locally. No deployment, retry, commit, or pull request
was performed in this handoff.

## Completed Repair

- Added a separate least-privilege `s3:PutObject` statement for
  `sessions/*/workshop-sync/*.json`; it does not grant reads, deletes, bucket
  access, or writes outside result JSON objects.
- Wrapped result publication with `ERR_WORKSHOP_RESULT_PUBLISH`, cleanup, and a
  stable adapter mapping so raw AWS diagnostics do not reach Discord.
- Added failure-catalog guidance telling the user to provide the support
  reference to an operator and wait for the platform repair before retrying.
- Added focused regression coverage for the exact Terraform resource scope,
  bootstrap failure marker, and SSM failure mapping. Updated the Workshop
  operating documentation.

## Validation

- `go test ./internal/adapters/aws/ssmbootstrap ./internal/app/failurecatalog`
  passes with repository-local Go caches.
- Git Bash syntax validation of `deploy/bootstrap/arma3-bootstrap.sh` passes.
- `terraform fmt -check infra/terraform/environments/dev/phase6.tf` passes.
- `terraform -chdir=infra/terraform/environments/dev validate` passes.
- `git diff --check` passes with only expected Windows LF/CRLF warnings.

## Operator Note

`test-32` still has a running EC2 instance and attached 100-GiB gp3 data volume,
so it may continue to incur cost. Its Workshop scenario payload and immutable
mission object were already downloaded; the bootstrap failed only when writing
the result manifest. Do not assume that the failed workflow finalized the
session revision. Deploy the repair, inspect `/rb status`, and retry the start
through the normal command path.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"

./scripts/package-discord-lambda.ps1
$PlanFile = "workshop-result-publication-fix-$((Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')).tfplan"
terraform -chdir=infra/terraform/environments/dev plan -out $PlanFile
terraform -chdir=infra/terraform/environments/dev show $PlanFile
# Confirm the reviewed plan updates the bootstrap object, affected Lambda
# package(s), and only the intended game-instance S3 PutObject permission.
terraform -chdir=infra/terraform/environments/dev apply $PlanFile
```

After deployment, inspect `test-32` with `/rb status` and retry `/rb start` only
if the session is not already in an active lifecycle workflow. No Discord
command registration is required because command definitions did not change.
