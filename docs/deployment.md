# Deploy the bot to a Discord server

This guide installs the development stack in one AWS account and one Discord
server. It uses PowerShell, AWS CLI, Go, and Terraform.

The stack creates AWS resources that incur charges. Keep provisioning disabled
until the control plane passes verification. Set an AWS Budget before starting
a game server.

## Prerequisites

Install these tools:

- [Git](https://git-scm.com/downloads)
- [Go 1.26.5](https://go.dev/dl/)
- [Terraform 1.15.x](https://developer.hashicorp.com/terraform/install)
- [AWS CLI v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)

Use an AWS account and role that can create the resources declared under
`infra/terraform`. The first deployment needs broad infrastructure permissions.
Restrict the deployment role after the stack exists.

Clone the repository and enter it:

```powershell
git clone https://github.com/L-McKendrick/game-server-platform.git
Set-Location game-server-platform
```

## 1. Create the Discord application

1. Open the [Discord Developer Portal](https://discord.com/developers/applications).
2. Select **New Application** and name the application.
3. Open **General Information**. Copy **Application ID** and **Public Key**.
4. Open **Bot**, select **Reset Token**, and copy the token. Store it in a
   password manager. Discord displays it once.
5. Leave privileged gateway intents disabled. The bot uses interactions and
   HTTP APIs.
6. Open **Installation**. Enable **Guild Install**.
7. Add the `applications.commands` and `bot` scopes.
8. Grant **View Channels**, **Send Messages**, **Embed Links**, **Attach Files**,
   and **Read Message History**.
9. Copy the install link, open it, and select the target Discord server. You
   must have permission to add applications to that server.

Enable Developer Mode in Discord under **User Settings > Advanced**. Right-click
the server name, select **Copy Server ID**, and save the value as the guild ID.

## 2. Authenticate to AWS

This repository uses the `game-server-dev` profile and `us-west-2` by default.
Create the profile with your normal AWS SSO or credential workflow, then run:

```powershell
aws login --profile game-server-dev --region us-west-2
$env:AWS_PROFILE = "game-server-dev"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity
```

If your AWS CLI does not support `aws login`, authenticate with `aws sso login
--profile game-server-dev` or your organization's approved method.

Check the returned account before continuing.

## 3. Create the Terraform state bucket

Initialize the bootstrap configuration without its remote backend:

```powershell
terraform -chdir=infra/terraform/bootstrap init -backend=false
terraform -chdir=infra/terraform/bootstrap plan -out=bootstrap-initial.tfplan
terraform -chdir=infra/terraform/bootstrap show bootstrap-initial.tfplan
terraform -chdir=infra/terraform/bootstrap apply bootstrap-initial.tfplan
$StateBucket = terraform -chdir=infra/terraform/bootstrap output -raw terraform_state_bucket_name
```

Review the saved plan before applying it. The bootstrap creates a private,
versioned S3 bucket protected from Terraform deletion.

Copy both backend templates:

```powershell
Copy-Item infra/terraform/bootstrap/backend.hcl.example infra/terraform/bootstrap/backend.hcl
Copy-Item infra/terraform/environments/dev/backend.hcl.example infra/terraform/environments/dev/backend.hcl
```

Replace `REPLACE-WITH-TERRAFORM-STATE-BUCKET` in both new files with the value
stored in `$StateBucket`. Then migrate the bootstrap state:

```powershell
terraform -chdir=infra/terraform/bootstrap init -migrate-state -backend-config=backend.hcl
```

Approve the state migration when Terraform prompts. Do not commit either
`backend.hcl` file or any state file.

## 4. Configure the environment

Create the ignored variable file:

```powershell
Copy-Item infra/terraform/environments/dev/terraform.tfvars.example infra/terraform/environments/dev/terraform.tfvars
```

Edit `infra/terraform/environments/dev/terraform.tfvars` and set:

```hcl
discord_public_key        = "<64-character-public-key>"
discord_application_id    = "<application-id>"
discord_allowed_guild_ids = ["<guild-id>"]

provisioning_enabled     = false
monthly_budget_limit_usd = 75
# budget_alert_email     = "you@example.com"
```

Keep `provisioning_enabled` false. Terraform rejects enabled provisioning
without `budget_alert_email`.

## 5. Build and validate the stack

Run the release checks from the repository root:

```powershell
$Workspace = (Resolve-Path ".").Path
$env:GOCACHE = Join-Path $Workspace ".cache/go-build"
$env:GOMODCACHE = Join-Path $Workspace ".cache/go-mod"

go test ./...
go vet ./...
go build ./cmd/...
terraform fmt -check -recursive infra/terraform
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev init -backend-config=backend.hcl -input=false
terraform -chdir=infra/terraform/environments/dev validate
```

Packaging writes ignored Lambda archives to `dist/`.

## 6. Deploy the control plane

Create a fresh saved plan:

```powershell
terraform -chdir=infra/terraform/environments/dev plan -out=initial-control-plane.tfplan
terraform -chdir=infra/terraform/environments/dev show initial-control-plane.tfplan
```

Check the account, region, public ingress, budget configuration, and resource
deletions. Confirm that `provisioning_enabled` remains false.

Apply the exact plan you reviewed:

```powershell
terraform -chdir=infra/terraform/environments/dev apply initial-control-plane.tfplan
```

Never reuse a saved plan after changing source files, variables, credentials,
or remote state.

## 7. Store the Discord bot token

Get the Terraform-managed secret name:

```powershell
$DiscordSecretName = terraform -chdir=infra/terraform/environments/dev output -raw discord_secret_name
$DiscordSecretName
```

Open AWS Secrets Manager in `us-west-2`, select that secret, and choose
**Retrieve secret value > Edit**. Store the bot token as plain text or as:

```json
{"token":"<discord-bot-token>"}
```

Do not place the token in Terraform variables, PowerShell history, logs, or
Git.

## 8. Connect Discord to the deployment

Read the interaction endpoint:

```powershell
$Endpoint = terraform -chdir=infra/terraform/environments/dev output -raw discord_interactions_endpoint_url
$Endpoint
```

Open the Discord application's **General Information** page. Paste `$Endpoint`
into **Interactions Endpoint URL** and save. Discord sends a signed check and
accepts the URL only after the Lambda returns the required response.

Register `/rb` in the target server:

```powershell
./scripts/register-discord-command.ps1 `
  -ApplicationId "<application-id>" `
  -GuildId "<guild-id>"
```

Paste the bot token into the secure prompt. The script replaces the guild's
application-command set with the repository's `/rb` definition.

## 9. Verify the bot

Run these checks in Discord:

1. Enter `/rb help`. The bot must return a private response.
2. Enter `/rb admin` as a server Administrator or member with Manage Server.
3. Add the roles allowed to use the platform and choose the **Public card
   channel** for newly created session cards.
4. Enter `/rb create` as an allowed member. Confirm its public card appears in
   the selected channel, then stop after creating the session.
   Creation does not allocate a game server while provisioning is disabled.
5. If Workshop sources are enabled in the selected release, verify a public
   Arma 3 scenario item and a direct-child mod collection through `/rb create`
   or `/rb edit`. Confirm the command response and `/rb status` are ephemeral,
   the public card receives only its bounded status projection, and no direct
   message or standalone public result is sent.

Check the deployed bootstrap worker before enabling game servers:

```powershell
./scripts/verify-bootstrap-worker-deployment.ps1
```

Fix any package or configuration mismatch through a new Terraform plan.

## 10. Enable game-server provisioning

Enabling provisioning permits EC2, EBS, network, storage, and data-transfer
charges.

1. Set a working `budget_alert_email` in `terraform.tfvars`.
2. Confirm the monthly limit and instance/storage sizes.
3. Change `provisioning_enabled` to `true`.
4. Create a new saved plan. Review the budget and provisioning changes.
5. Apply that exact plan.
6. Run `/rb start session:<session>` in Discord.

Use `/rb status` to follow deployment. Test sleep before leaving the server
unattended. Use archive or terminate when the server is no longer needed.

## Updating the deployment

Pull the selected release, rerun the checks and package command, then create a
new Terraform plan. Apply only the reviewed plan. Register the Discord command
again whenever `deploy/discord/rb-command.json` changes.

For release acceptance and failure handling, use the
[operator deployment runbook](runbooks/deploy-discord-interactions.md).
Workshop-specific limits, host behavior, failure remedies, and least-privilege
boundaries are documented in
[Steam Workshop content sources](workshop-content-sources.md).
