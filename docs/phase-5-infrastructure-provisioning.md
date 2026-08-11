# Phase 5: Infrastructure Provisioning

Phase 5 turns the workflow contracts into a cost-bounded EC2/EBS provisioning path. It does not install or start Arma 3; successful provisioning stops at `BOOTSTRAPPING` for Phase 6.

## Control flow

1. `/session start` validates guild access, ownership, and `NEW` state, then sends a normalized FIFO command.
2. The command worker revalidates access and state, conditionally acquires the session workflow lease, and starts `ProvisionSession`.
3. The provisioning workflow reserves a DynamoDB capacity slot before launching compute.
4. The provisioning worker discovers an existing tagged instance or launches exactly one using an EC2 client token.
5. The workflow polls bounded EC2 and Systems Manager readiness checks.
6. Resource identifiers are written conditionally to the authoritative session record.
7. Success records `BOOTSTRAPPING`, completes the workflow, releases the lease, and queues a Discord notification.
8. Failure records `FAILED`, preserves discovered identifiers, and releases unused capacity when no instance exists.

## Shared infrastructure

- Dedicated `10.40.0.0/16` VPC with two public subnets across available Availability Zones.
- Internet Gateway and public routing; no NAT Gateway.
- Arma UDP `2302-2306` and optional TeamSpeak UDP `9987` security groups.
- No inbound SSH or public TeamSpeak ServerQuery port.
- EC2 instance profile with Systems Manager and session-scoped S3 permissions.
- Current Amazon Linux 2023 x86_64 AMI resolved from the AWS public SSM parameter.
- Encrypted `gp3` root and persistent data volumes with IMDSv2 required.

## Cost and safety gates

- `PROVISIONING_ENABLED` defaults to `false`.
- Terraform refuses to enable provisioning without `budget_alert_email`.
- AWS Budget forecasted 80% and actual 100% notifications are configured when an email is supplied.
- `max_provisioned_sessions` defaults to one and is enforced with conditional DynamoDB capacity slots.
- Instance type and volume sizes are operator-controlled Terraform inputs with bounded validation.
- Session and environment tags allow recovery when a metadata update is interrupted.
- `/session start` must not be registered or enabled in the deployed guild until a reviewed Terraform plan is applied and an end-to-end test succeeds.

## Phase boundary

Phase 5 verifies that the instance is running, its data volume is discoverable, and the instance is online in Systems Manager. Steam credentials, SteamCMD, Arma, DLC, Workshop content, mission deployment, TeamSpeak installation, and game health remain Phase 6.
