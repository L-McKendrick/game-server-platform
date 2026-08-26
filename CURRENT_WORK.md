# Current Work

## State and Objective

Phase 20 is complete on `codex/mission-wake-sync-fix`. The fix addresses the
verified `test-26-2` wake failure where an accepted sleeping-session mission
was present in metadata and S3 but the host reused `deploy_content.complete`,
leaving no `.pbo` in `mpmissions` and retaining the built-in mission in
`server.cfg`.

## Completed Development

- Bootstrap commands now carry a deterministic SHA-256 content revision bound
  to the bootstrap artifact, display identity, selected mission, complete
  accepted mission manifest, and custom server-configuration identity.
- The host keys the resumable `deploy_content` marker by that revision. An
  unchanged replay remains skipped, while changing/selecting a mission,
  changing server configuration, changing display identity, or deploying a new
  bootstrap artifact reruns checksum-verified mission synchronization and
  regenerates the effective `class Missions` block before service restart.
- Successful content deployment removes prior content markers before recording
  the installed revision. An interruption before that final marker leaves no
  false completion evidence, so retry safely replays; reverting to an earlier
  content digest cannot match a stale historical marker.
- Focused coverage verifies unchanged replay stability, changed accepted
  missions on a sleeping session, server-configuration changes, bootstrap
  revision changes, command transport, and the revisioned Bash marker.

## Review and Validation

- `go test ./internal/adapters/aws/ssmbootstrap ./cmd/bootstrap-worker` passes.
- `go vet ./internal/adapters/aws/ssmbootstrap ./cmd/bootstrap-worker` passes.
- `go build ./cmd/bootstrap-worker` passes.
- Lambda packaging, Git Bash syntax validation, and `git diff --check` pass.
- `go test ./...` reaches an unrelated pre-existing failure in
  `internal/app/sessioncard`: the user-owned `embed.go` edit changes the
  TeamSpeak field name expected by `embed_test.go`. That file and the existing
  `internal/adapters/aws/ssmmonitor/runner.go` edit are preserved and excluded
  from this phase.

## Next Development Task

- Deploy through a fresh reviewed Terraform plan, then sleep and wake the
  active “Test 26” session and verify `ZGMv2-Stratis.Stratis.pbo` exists on the
  host and the effective `class Missions` selects `ZGMv2-Stratis.Stratis`.
- Resolve or finish the two unrelated user-owned edits separately before using
  a full-suite result as a branch-wide release gate.

## Commands to Apply Current Changes

From the repository root in PowerShell:

```powershell
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"

# Rebuild the Lambda set containing the bootstrap worker and publish the new
# content-addressed bootstrap artifact through one fresh reviewed plan.
./scripts/package-discord-lambda.ps1
$PlanFile = "mission-wake-sync-fix-$((Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')).tfplan"
terraform -chdir=infra/terraform/environments/dev plan -out $PlanFile
terraform -chdir=infra/terraform/environments/dev show $PlanFile
# Apply only after confirming the reviewed plan contains the expected
# bootstrap-worker and bootstrap-artifact changes and no unrelated mutations.
terraform -chdir=infra/terraform/environments/dev apply $PlanFile

$BootstrapBucket = terraform -chdir=infra/terraform/environments/dev output -raw session_assets_bucket_name
$BootstrapContent = (Get-Content -Raw "deploy/bootstrap/arma3-bootstrap.sh").Replace("`r`n", "`n")
$BootstrapHash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($BootstrapContent))).ToLowerInvariant()
$BootstrapKey = "platform/bootstrap/arma3-$($BootstrapHash.Substring(0, 16)).sh"
aws s3api head-object --bucket $BootstrapBucket --key $BootstrapKey
```

No Discord command registration is required. After deployment, sleep and wake
the active `test-26-2` session, then verify the uploaded mission is present and
selected before considering the incident resolved.
