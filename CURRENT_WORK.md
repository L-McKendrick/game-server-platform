# Current Work

## State and Objective

Phase 19.1 development is complete on `codex/phase-19-production-guardrails`.
Managed game hosts no longer need standing Secrets Manager or Steam-lease
DynamoDB access. Phase 19.2 maximum-duration guardrails are next; Phase 19.3
will close the separately accepted development-stage cross-session S3 risk.

## Current Handoff

- Added a trusted Steam authorization exchange broker used by bootstrap, wake,
  restart, restore, Workshop synchronization, and reliability recovery paths.
- Each exchange is bound to the exact session, workflow, workflow type,
  instance, purpose, source secret version, random 256-bit ID, and expiry.
  Duplicate work reuses the same active exchange; competing workflows fail the
  global owner-checked lease.
- The broker writes cache material only to encrypted non-session S3 exchange
  objects and passes exact expiring GET/PUT capabilities to the target through
  SSM. The secret identifier and metadata-table name are no longer present in
  host command text.
- The host validates and stages the cache under `/run`, uses username-only
  SteamCMD login, uploads a bounded update, and scrubs authentication material
  on every exit path. It no longer calls Secrets Manager or DynamoDB.
- The broker rejects wrong references, stale source versions, malformed or
  oversized updates, missing output, and terminal replay conflicts. It
  preserves `ERR_STEAM_REAUTH_REQUIRED`, serialized promotion, and cleanup.
- Removed Secrets Manager and DynamoDB authorization from the managed-game
  bootstrap policy. Exact broker permissions are attached only to the five
  trusted lifecycle workers. S3 and DynamoDB TTLs backstop abandoned exchange
  cleanup.
- Advanced the bootstrap runtime contract to `steam-auth-broker-v1`, so an
  inconsistent worker/script Terraform rollout fails closed.
- Focused broker, bootstrap/script, IAM security-contract, affected command,
  and Terraform validation passed. Per user direction, the full repository
  test/coverage, vet, build, packaging verification, and recursive Terraform
  checks are deferred for streamlined development.
- Nothing was deployed, registered, or mutated in AWS, Discord, or Steam. No
  cache rotation is required without evidence of exposure.

## Important Operator Attention

- Deploy the broker, worker packages, bootstrap artifact, lifecycle rule, and
  IAM removal together through one fresh reviewed Terraform plan. Do not apply
  only the restrictive host IAM change ahead of the compatible workers and
  script.
- Existing EC2 instance-role credentials may retain the old permissions until
  AWS expires them. Verify explicit denial after deployment and credential
  refresh or instance replacement before treating standing access as closed
  live.
- Cross-session session-assets access remains intentionally accepted for
  supervised development only and is tracked by Phase 19.3. Production and
  multi-tenant use remain blocked until it is closed.
- The inherited Phase 20 restore correction and Test-56 live acceptance remain
  pending. Do not recover Test-54.

## Commands to Apply Current Changes

Run from the repository root. Package the affected workers and create a new
saved plan; preserve every existing user-owned plan file.

```powershell
$env:AWS_PROFILE = "game-server-dev"
$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$steamBrokerPlan = "phase19-steam-broker-$timestamp.tfplan"
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan "-out=$steamBrokerPlan"
terraform -chdir=infra/terraform/environments/dev show $steamBrokerPlan
```

Confirm the plan updates the bootstrap artifact and the artifact, bootstrap,
sleep/wake, restore, and reliability workers; adds the broker policies and
exchange lifecycle rule; and removes Steam secret/lease permissions only from
the managed-game instance policy. After approving that exact plan, in the same
PowerShell session:

```powershell
terraform -chdir=infra/terraform/environments/dev apply $steamBrokerPlan
aws iam get-role-policy --role-name game-server-platform-dev-game-instance --policy-name bootstrap-secrets --query PolicyDocument --output json
aws s3api get-bucket-lifecycle-configuration --bucket game-server-platform-dev-assets-622211271532-us-west-2 --region us-west-2 --output json
./scripts/verify-bootstrap-worker-deployment.ps1
```

No Discord command registration is required. After deployment, run one
controlled vanilla or modded start and one replay-capable wake/restart path.
Confirm successful Steam download, exchange-object cleanup, cache promotion or
unchanged completion, lease release, redacted logs, and explicit host denial
for Secrets Manager and the `STEAM_AUTH#CACHE` DynamoDB item.
