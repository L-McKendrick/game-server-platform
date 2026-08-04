# Deploy the development Discord interaction endpoint

This runbook deploys only the serverless Discord metadata-command boundary. It
does not create EC2, EBS, NAT Gateway, load balancers, Step Functions, or SQS.

## 1. Build the Lambda package

From the repository root in PowerShell:

```powershell
./scripts/build-discord-lambda.ps1
```

Expected artifact:

```text
dist/discord-interactions.zip
```

## 2. Create ignored Terraform values

```powershell
Copy-Item `
  infra/terraform/environments/dev/discord.auto.tfvars.example `
  infra/terraform/environments/dev/discord.auto.tfvars

code infra/terraform/environments/dev/discord.auto.tfvars
```

Replace the Discord placeholders. Do not put a bot token in this file.

## 3. Authenticate and initialize Terraform

```powershell
aws login --profile game-server-dev --region us-west-2
$env:AWS_PROFILE = "game-server-terraform"
$env:AWS_REGION = "us-west-2"
$env:AWS_EC2_METADATA_DISABLED = "true"
aws sts get-caller-identity

terraform -chdir=infra/terraform/environments/dev init `
  -backend-config=backend.hcl `
  -input=false
```

## 4. Create and review a plan

```powershell
terraform -chdir=infra/terraform/environments/dev plan `
  -out=discord-interactions.tfplan
```

The first plan should add only:

- two CloudWatch log groups;
- one Lambda execution role and one inline policy;
- one Lambda function;
- one HTTP API, integration, route, and default stage;
- one API Gateway Lambda invoke permission;
- two CloudWatch alarms.

Stop if the plan contains EC2, EBS, NAT Gateway, load balancers, or destructive
changes to existing metadata resources.

## 5. Apply the reviewed plan

```powershell
terraform -chdir=infra/terraform/environments/dev apply `
  discord-interactions.tfplan
```

Get the endpoint:

```powershell
$Endpoint = terraform -chdir=infra/terraform/environments/dev output `
  -raw discord_interactions_endpoint_url
$Endpoint
```

## 6. Verify the public route

An unsigned request must reach Lambda and be rejected with HTTP 401:

```powershell
try {
    Invoke-WebRequest `
      -Method Post `
      -Uri $Endpoint `
      -ContentType "application/json" `
      -Body '{"type":1}'
}
catch {
    $_.Exception.Response.StatusCode.value__
}
```

Expected result:

```text
401
```

## 7. Configure Discord

In the Discord developer portal, set **Interactions Endpoint URL** to the
Terraform output. Discord sends a signed `PING`; saving succeeds only when the
endpoint returns a valid `PONG`.

## 8. Register the development-guild command

```powershell
./scripts/register-discord-command.ps1 `
  -ApplicationId "<application-id>" `
  -GuildId "<development-guild-id>"
```

The script prompts securely for the bot token when `DISCORD_BOT_TOKEN` is not
already set. Registering the same command name again updates the existing guild
command.

## 9. Smoke test in Discord

Run, in order:

```text
/session create slug:test-session name:Test Session
/session list
/session status session-id:<ID returned by create>
```

Confirm the session is written to the development DynamoDB table and that no
duplicate event is created when Discord retries the same interaction.
