# Deploy and accept the development Discord experience

This runbook packages, deploys, registers, and accepts the Phase 12 `/rb`
experience in the approved development guild. Deployment can update the full
control plane and can enable billable provisioning; it is not a serverless-only
change.

## 1. Confirm approvals and credentials

Before planning or deploying, confirm all of the following:

- the populated `.tfvars` file and any saved plan remain ignored;
- the `game-server-dev` AWS session is current;
- any existing Phase 5 Terraform budget is preserved unless a separate manual
  ownership migration has been reviewed;
- `provisioning_enabled` stays false unless billable live-start acceptance is
  explicitly approved;
- no Terraform apply will occur without review of the exact saved plan; and
- a short-lived Discord bot token is available only for command registration.

Do not put Discord tokens, AWS credentials, populated `.tfvars`, plans, state,
or Lambda archives in Git.

## 2. Run the local release checks

From the repository root in PowerShell:

```powershell
$Workspace = (Resolve-Path ".").Path
$env:GOCACHE = Join-Path $Workspace ".cache/go-build"
$env:GOMODCACHE = Join-Path $Workspace ".cache/go-mod"

go test ./...
go test -race -cover ./...
go vet ./...
go build ./cmd/...
terraform fmt -check -recursive infra/terraform
terraform -chdir=infra/terraform/environments/dev validate
```

In a fresh checkout, run `terraform init -backend=false` before validation.

## 3. Package every Lambda runtime

```powershell
./scripts/package-discord-lambda.ps1
```

The script builds all worker custom runtimes as Linux/amd64 ZIP files under
ignored `dist/`. Review the output list and verify that
`dist/discord-interactions.zip` is present. Do not commit the archives.

## 4. Configure reviewed Terraform values

Copy `infra/terraform/environments/dev/terraform.tfvars.example` to an ignored
`.tfvars` file and replace the Discord placeholders.

The Discord Lambda has no Cost Explorer or AWS Budgets integration and needs no
Billing permissions. Cost commands are intentionally omitted; operators manage
budget alerts in AWS.

Phase 5 still declares the existing Terraform budget guardrail. If budget
alerts are moving to fully manual AWS ownership, handle that as a separate
Terraform state/resource migration; do not let this routine release plan delete
or replace an existing alert.

## 5. Authenticate, initialize, plan, and review

```powershell
aws login --profile game-server-dev --region us-west-2
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

terraform -chdir=infra/terraform/environments/dev init `
  -backend-config=backend.hcl `
  -input=false
terraform -chdir=infra/terraform/environments/dev plan `
  -out=phase-12.tfplan
```

Review the complete plan. Stop for any unintended deletion/replacement,
credential or sensitive-value exposure, unexpected public network access,
budget-recipient drift, provisioning enablement, or resource outside the
approved development environment. A routine validation run never applies.

Only after separate approval of that exact saved plan:

```powershell
terraform -chdir=infra/terraform/environments/dev apply phase-12.tfplan
```

Before retrying any failed bootstrap workflow, verify that the deployed worker
matches the freshly packaged archive and the Steam authorization-cache runtime
contract:

```powershell
./scripts/verify-bootstrap-worker-deployment.ps1
```

Stop if verification reports a code-hash or configuration mismatch. Do not
restore the retired `STEAM_SECRET_ID` password configuration to make an older
worker start. Repackage and deploy the current worker through a newly reviewed
Terraform plan instead. If the package and configuration match, reconcile any
expired workflow lease before retrying `/rb start`; retained EC2/EBS resources
and durable bootstrap markers must be reused rather than recreated.

## 6. Verify the signed interaction boundary

Read the endpoint after an approved apply:

```powershell
$Endpoint = terraform -chdir=infra/terraform/environments/dev output `
  -raw discord_interactions_endpoint_url
```

An unsigned `POST` with `{"type":1}` must return HTTP 401. In the Discord
developer portal, save this value as **Interactions Endpoint URL**; Discord's
signed `PING` must receive `PONG`. Confirm Lambda/API alarms remain clear.

## 7. Bulk-register the guild command

```powershell
./scripts/register-discord-command.ps1 `
  -ApplicationId "<application-id>" `
  -GuildId "<development-guild-id>"
```

The script prompts securely when `DISCORD_BOT_TOKEN` is absent and bulk
overwrites the development guild's command set with only `/rb`. This removes
the retired standalone `/admin` command. Clear the environment token after any
non-interactive use.

Guild registration is inherently unavailable in DMs. Discord cannot attach
default permissions to only the `/rb admin` subcommand, so do not restrict the
root to Manage Server: doing so would hide normal commands from configured
platform roles. Use Discord role/channel command permissions for coarse `/rb`
visibility and rely on the backend's fresh Administrator/Manage Server check
for the admin command and every component click.

## 8. Complete live-guild acceptance

Use one normal member with an allowed platform role and one Administrator or
Manage Server member. Record the command version, Lambda package hash, time,
guild, and pass/fail result without tokens, immutable session IDs, or raw AWS
errors.

Run the non-billable checks first:

1. `/rb help` shows first-run or existing-user guidance privately.
2. `/rb create` opens one private five-field modal. Submit a disposable Arma 3
   name, optional description, mode/features, valid mission, and optional
   preset. Confirm the response says validation is pending and no game-server
   infrastructure was allocated.
3. `/rb list`, `/rb status session:<choice>`, `/rb setup session:<choice>`, and
   `/rb help session:<choice>` use readable labels/slugs, never visible
   immutable IDs, and return private mobile-safe output.
4. The public card is created once, retains `Show players` and `Refresh` only,
   and uses text/icon labels in addition to color. A stale card or modal returns
   refresh/reopen guidance without state leakage.
5. A normal member is denied `/rb admin`; a manager can open `/rb admin`,
   replace allowed Discord roles, confirm removal of all normal-role access,
   and repair a card. Verify the manager recovery path after removing roles.
   No cost or Billing control is registered or rendered.
6. Repeating an accepted or active operation must return current progress or an
   idempotent disposition; it must not create a second session, card, workflow,
   notification, or cost-bearing resource.

Billable/destructive checks require their own approval and a disposable
session:

1. Confirm the budget recipient, saved plan, `provisioning_enabled`, capacity
   limit, AMI, subnet, security groups, and instance profile.
2. After files are accepted, run `/rb start session:<choice>` and use
   `/rb status` to follow the durable milestones. Repeating start during an
   active operation must show progress and queue nothing new.
3. Verify playable health and connection details, then test sleep/wake.
4. Exercise archive/restore only when replacement-resource cost is approved.
   Exercise terminate only when permanent deletion of the disposable session
   is approved. `/rb confirm` and `/rb cancel-confirmation` take no options;
   the pending action is resolved privately from the invoking user and guild,
   expires after ten minutes, and remains single-use.
5. On any unknown or uncertain failure, stop. The response must provide a safe
   reference and truthful resource/billing warning; no unscheduled retry may be
   implied or performed.

Live acceptance is complete only when every applicable item has recorded
evidence. Mark skipped billable/destructive items as **not run — approval
required**, never as passed.
