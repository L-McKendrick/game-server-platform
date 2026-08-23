# Arma Server Configuration

Discord Administrators can set the guild-level Arma 3 `server.cfg` from
`/rb admin` → **Server config**. Manage Server and configured platform roles do
not have this permission.

## Upload or replace

1. Open `/rb admin` and choose **Server config**.
2. Choose **Upload server.cfg**.
3. Upload one non-empty UTF-8 `.cfg` file no larger than 64 KiB.
4. Reopen the view after processing and verify that the revision, filename,
   size, and update timestamp changed.

The response means queued for private validation, not active. If the revision
does not change, correct the file and upload again. Validation rejection or a
stale revision schedules no automatic retry.

The platform never displays file contents or its private S3 object key. Treat
the file as secret-bearing: it may include Arma server/admin passwords. The S3
bucket remains private, encrypted, versioned, and blocked from public access.

## Session behavior

When `/rb start` is accepted, the internal command captures the active config
revision, object key, and SHA-256. Workflow lock acquisition persists that
selection atomically. Bootstrap downloads that exact object, verifies the
digest, and installs it as `/srv/game-server/config/server.cfg`. If no custom
file is active, bootstrap generates the existing safe default.

Replacing the guild file does not silently change a running or in-progress
session. Existing sessions retain their captured revision across replay, wake,
and restore; later session starts capture the then-active revision.

## Remove the active file

Choose **Remove custom config**, review the confirmation, then choose **Use
generated default**. The action is bound to the displayed revision and is
idempotent. Future sessions use the generated default. Existing session
snapshots and their private revision artifacts remain so deterministic recovery
does not drift.

## Deployment checks

Package the Lambdas and review a fresh Terraform plan. It should update the
Discord and artifact workers, the bootstrap artifact, and only the scoped S3
permissions allowing the artifact worker to write and game instances to read
`guilds/*/server-config/revisions/*/server.cfg`.

```powershell
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -out=phase-12-10-server-config.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-12-10-server-config.tfplan
# Apply only after separate approval of this exact saved plan.
```
