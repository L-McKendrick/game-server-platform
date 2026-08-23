# Platform Runtime Reset

Use this runbook for the Administrator-only `/rb admin` reset. It is intended
to return the installed environment to an empty game-session state without
destroying Terraform-managed control-plane resources.

## Deployment gate

The Terraform input `reset_enabled` defaults to `false`. Review a fresh plan
before enabling it. The plan should add the reset FIFO queue/DLQ, worker,
event-source mapping, scoped IAM, and the Discord Lambda queue permission and
environment variables. Never reuse an older saved plan.

```powershell
./scripts/package-discord-lambda.ps1
terraform -chdir=infra/terraform/environments/dev plan -var="reset_enabled=true" -out=phase-12-9-reset.tfplan
terraform -chdir=infra/terraform/environments/dev show phase-12-9-reset.tfplan
# Apply only after separate approval of this exact saved plan.
```

Enabling the gate is not permission to execute a live reset. Live use remains
a separate destructive action initiated by a Discord Administrator.

## Administrator flow

1. Open `/rb admin` and choose **Reset platform**.
2. Read the preserved-state and billing warnings.
3. Choose **Prepare full reset**.
4. Within ten minutes, type the exact phrase shown by Discord.
5. Reopen `/rb admin` to view the current stage or latest terminal result.

The phrase is bound to its requesting Administrator and guild and is consumed
atomically with the environment lock. Replays do not enqueue duplicate work.
Manage Server and configured access roles cannot prepare or submit a reset.

## Scope and recovery

The worker stops active platform workflows, purges runtime queues and DLQs,
terminates exactly tagged game instances, removes exactly tagged disposable
volumes, deletes known bot session messages, removes every version below the
session S3 prefix, deletes runtime metadata, and removes eligible pre-reset
application log streams. It verifies key runtime inventories again before
reporting success.

Terraform resources/state, guild access, Discord/AWS secrets, guild-level
configuration, budgets, CloudTrail, billing records, AWS-retained execution
history, and the latest reset audit result remain. Billing history cannot be
reset, and resources may incur cost until absence is verified.

If the result is **incomplete**, do not assume the environment is empty and do
not immediately run another reset. No automatic cleanup retry is scheduled; an
unacknowledged first worker attempt is sent to the reset DLQ instead of being
run again. Inspect
the reset worker logs, current Step Functions executions, tagged EC2/EBS
inventory, session S3 versions, queues, and DynamoDB runtime records. Resolve
the specific cause, review remaining cost-bearing resources, then prepare a
new reset only as a deliberate Administrator action.
