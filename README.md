# Game Server Platform

An on-demand platform for provisioning and managing temporary dedicated game servers.

## Current status

Game Server Platform lets a Discord community create and operate temporary
Arma 3 servers through `/rb` commands. It deploys each server to AWS, installs
the selected mission and mods, publishes connection details, and reports live
status in Discord.

Members can start, sleep, wake, archive, restore, or permanently terminate a
server without using the AWS console. Server owners can revise mods and mission
files between runs. Discord administrators control access, upload a shared
`server.cfg`, repair session cards, and reset runtime data.

The platform keeps persistent session records and portable archives while game
servers remain disposable. Provisioning limits and AWS Budget alerts constrain
cost. Confirmation checks protect destructive actions.

## Deploy to your Discord server

Follow [Deploy the bot to a Discord server](docs/deployment.md) for the full
Discord application, AWS, Terraform, secret, command-registration, and
verification procedure.

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
- Missions: optional, immutable Arma `.pbo` uploads managed through
  `/rb edit`; sessions use `MP_ZGM_m12.Stratis` by default
- Deployment archive: a long-term backup of a deployment
