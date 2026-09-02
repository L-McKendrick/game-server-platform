# Current Work

## State and Objective

Phase 17.14 is complete on `codex/workshop-content-sources`. Steam Workshop
item and collection links copied with extra query parameters are normalized to
the existing canonical shared-file URL instead of being rejected.

## Completed Development

- Workshop URL parsing now requires exactly one valid numeric `id` but ignores
  and strips all other query parameters such as `l`, `searchtext`, and tracking
  values.
- HTTPS, exact Steam Community host, exact shared-file path, no credentials or
  port, no fragment, and no duplicate `id` remain mandatory.
- The shared Discord Workshop request builder queues only
  `https://steamcommunity.com/sharedfiles/filedetails/?id=<id>`, covering create,
  mission, mod-item, and mod-collection entry points.
- `/rb edit` mod links are validated before mod options mutate. Invalid links
  now return a direct private correction instead of a generic reference error
  after a partial configuration change.

## Live Finding

`test-35` (`01M1GN9VX56E304VWKMHG8YQ4A`) remains a recoverable `DRAFT` with no
active workflow or Workshop-resolution marker. Correlation reference
`01M1GNE74T71240CS0RVZB753V` failed synchronously because its mod collection
URL did not match the previous query-strict canonical form. The mission source
was accepted independently; no host download or SteamCMD operation began for
the rejected mod link.

## Validation

- Focused domain and Discord tests pass for query stripping, canonical queued
  requests, duplicate-ID rejection, invalid-link private feedback, and
  pre-mutation behavior.
- `go test ./...`, `go vet ./...`, `go build ./cmd/...`, Lambda packaging, and
  `git diff --check` pass. Windows reports only expected LF/CRLF warnings.
- Terraform and Discord command definitions did not change. The Discord
  interactions and artifact-worker Lambdas consume the changed shared domain
  code and require deployment; command registration is unnecessary.

## Commands to Apply Current Changes

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=workshop-url-normalization-20260902.tfplan
terraform -chdir=infra/terraform/environments/dev show workshop-url-normalization-20260902.tfplan
terraform -chdir=infra/terraform/environments/dev apply workshop-url-normalization-20260902.tfplan
aws lambda get-function-configuration --function-name game-server-platform-dev-discord-interactions --query '{State:State,LastModified:LastModified,CodeSha256:CodeSha256}' --output table
aws lambda get-function-configuration --function-name game-server-platform-dev-artifact-worker --query '{State:State,LastModified:LastModified,CodeSha256:CodeSha256}' --output table
```

The reviewed plan must add and destroy no infrastructure. Discord command
registration is not required. After deployment, reopen `/rb edit` -> `Mods`
for `test-35` and submit the collection link again; the draft is not stuck.
