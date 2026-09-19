# Game Server Platform

An on-demand platform for provisioning and managing temporary dedicated game servers.

## Current status

Game Server Platform lets a Discord community create and operate temporary
Arma 3 servers through `/rb` commands. It deploys each server to AWS, installs
the selected mission and mods, publishes connection details, and reports live
status in Discord.

Members can start, sleep, restart, archive, restore, or permanently terminate a
server without using the AWS console; `/rb start` also wakes sleeping servers.
Server owners can revise mods and mission files between runs. Discord
administrators control access, upload a shared `server.cfg`, repair session
cards, and reset runtime data.

Running servers automatically sleep after the session's configured period of
verified zero-player activity (30 minutes by default). A server that remains
sleeping for its configured archive period (7 days by default)
automatically enters the same verified archive workflow used by an owner
request. Missing or failed player queries pause the policy rather than being
treated as an empty server; see [Automatic inactivity lifecycle](docs/phase-14-inactivity-lifecycle.md).

The platform keeps persistent session records and portable archives while game
servers remain disposable. Provisioning limits and AWS Budget alerts constrain
cost. Confirmation checks protect destructive actions.

Arma missions and client mods may come from validated uploads or public Steam
Workshop items and direct-child collections. Eligible Workshop scenarios added
to a stable running server become mission choices without changing the current
mission; Workshop mod revisions remain pending until a controlled restart.
Creation can queue the normal start automatically after required mod input is
accepted, and owners may opt into one creation-channel ping after initial
health verification succeeds.

The public card shows live mission/player information only while the game
server is active. A completed archived card is reduced to its description,
last active modlist link, and archive time; it retains only the `Refresh`
control. Actionable archive/restore failures continue to show diagnostics.

## Set up the app in Discord

The app needs a Discord application, an AWS deployment, and the `/rb` command
registered in your server. The complete copy-and-paste procedure is in
[Deploy the bot to a Discord server](docs/deployment.md).

Before starting, install Git, Go 1.26.5, Terraform 1.15.x, and AWS CLI v2. You
also need permission to deploy the AWS stack and add apps to the Discord
server.

1. Open the [Discord Developer Portal](https://discord.com/developers/applications),
   create an application, and copy its **Application ID** and **Public Key**.
2. On the application's **Bot** page, create a bot token and store it in a
   password manager. Never put it in Git, Terraform variables, or shell
   history. Privileged gateway intents are not required.
3. On **Installation**, enable **Guild Install**. Add the
   `applications.commands` and `bot` scopes, then grant **View Channels**,
   **Send Messages**, **Embed Links**, **Attach Files**, and
   **Read Message History**.
4. Use the installation link to add the app to your Discord server. Enable
   Discord Developer Mode and copy the server ID.
5. Follow [the deployment guide](docs/deployment.md#2-authenticate-to-aws) to
   bootstrap Terraform state, create `terraform.tfvars`, package the Lambdas,
   review a fresh Terraform plan, and deploy the control plane. Keep
   `provisioning_enabled = false` during initial setup.
6. Store the bot token in the Terraform-managed AWS Secrets Manager secret;
   do not store it in Terraform state. See
   [Store the Discord bot token](docs/deployment.md#7-store-the-discord-bot-token).
7. Copy the Terraform output `discord_interactions_endpoint_url` into the
   application's **Interactions Endpoint URL** field in the Developer Portal.
   Discord must accept its signed endpoint check before you continue.
8. Register `/rb` in the server from the repository root:

   ```powershell
   ./scripts/register-discord-command.ps1 `
     -ApplicationId "<application-id>" `
     -GuildId "<server-id>"
   ```

   Enter the bot token only in the script's secure prompt. Registration
   replaces this app's guild command set with the repository's `/rb` command.
9. In Discord, run `/rb help`, then run `/rb admin` as an Administrator or a
   member with **Manage Server**. Choose the roles allowed to use the app and
   the channel where public session cards should be posted.
10. As an allowed member, run `/rb create` and confirm that the private setup
    flow opens and its public session card appears. No game server is created
    while provisioning remains disabled.

Do not enable game-server provisioning until the AWS budget recipient,
capacity limit, network settings, and a fresh Terraform plan have been
reviewed. Enabling it can create EC2, EBS, storage, and data-transfer charges.

## Local Discord interaction server

The local server uses in-memory repositories. Data resets whenever the process restarts.

Required environment variables:

```text
DISCORD_PUBLIC_KEY=<hex public key from the Discord developer portal>
DISCORD_APPLICATION_ID=<Discord application ID>
DISCORD_ALLOWED_GUILD_IDS=<comma-separated development guild IDs>
```

No administrator, role, or channel IDs need to be preconfigured. Discord members with Administrator or Manage Server permission can open `/rb admin`, replace the allowed roles, or remove all normal-role access after a confirmation. Optional `DISCORD_ALLOWED_ROLE_IDS` and `DISCORD_ALLOWED_CHANNEL_IDS` values remain available as a deployment fallback until a guild policy is persisted.

Optional environment variables:

```text
DISCORD_LISTEN_ADDRESS=127.0.0.1:8080
DISCORD_MAX_REQUEST_BYTES=65536
DISCORD_SIGNATURE_MAX_AGE_SECONDS=300
```

Run:

```powershell
$env:DISCORD_PUBLIC_KEY = "<public-key>"
$env:DISCORD_APPLICATION_ID = "<application-id>"
$env:DISCORD_ALLOWED_GUILD_IDS = "<development-guild-id>"
go run ./cmd/discord-local
```

The endpoint is `POST /discord/interactions`. Versioned development command definitions are stored in:

```text
deploy/discord/rb-command.json
```

## Development deployment

Package all Linux Lambda custom runtimes:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/package-discord-lambda.ps1
```

Copy `infra/terraform/environments/dev/terraform.tfvars.example` to an ignored `.tfvars` file and provide the Discord public key, application ID, and development guild ID. Initialize with the existing remote backend configuration and apply only a reviewed plan.

After apply, set Terraform output `discord_interactions_endpoint` as the Discord application's interaction endpoint. Register the guild commands with a short-lived process environment containing the bot token:

```powershell
$env:DISCORD_APPLICATION_ID = "<application-id>"
$env:DISCORD_GUILD_ID = "<development-guild-id>"
$env:DISCORD_BOT_TOKEN = "<bot-token-from-Secrets-Manager>"
go run ./cmd/discord-register
Remove-Item Env:DISCORD_BOT_TOKEN
```

Registration bulk-overwrites the development guild with `/rb` only, including
`/rb help` and the protected `/rb admin` group. Follow the complete packaging,
plan-review, cost-tag, role, and live-acceptance procedure in
`docs/runbooks/deploy-discord-interactions.md`.

Never place the bot token in a `.tfvars` file, command definition, log, or Terraform state.

## Initial technology choices

- AWS
- EC2, EBS, and S3
- Terraform
- Go
- DynamoDB
- AWS Secrets Manager
- SQS and Step Functions
- EventBridge
- AWS Lambda and API Gateway
- GitHub Actions

## Terminology

- Deployment: a persistent game-server configuration and lifecycle record
- Server: a temporary compute instance
- Missions: optional, immutable Arma `.pbo` uploads or validated Workshop
  scenarios managed through `/rb edit`; sessions use `MP_ZGM_m12.Stratis` by
  default
- Deployment archive: a long-term backup of a deployment

Use `/rb start` to provision a configured session or wake a sleeping server.
Archived sessions can use `/rb start` to enter the restore workflow; the
explicit `/rb restore` command remains available. Administrators and Manage
Server members retain permission to wake sleeping sessions; initial
provisioning remains owner-only. `/rb wake` is no longer registered.

Use `/rb restart session:<slug>` to immediately restart a running or idle game
server, including when players are connected. It applies pending client/server
mods and game-server settings, then verifies Arma health. It leaves EC2 and
TeamSpeak running. Owners and Administrator/Manage Server members may restart;
other active workflows block the operation. Restart failures require checking
`/rb status`; there is no automatic rollback. See
[restart operations](docs/runbooks/restart-game-server.md).
