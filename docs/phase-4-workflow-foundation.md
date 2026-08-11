# Phase 4: Workflow Foundation

Phase 4 establishes the asynchronous control-plane contracts without claiming that game-server infrastructure can already be provisioned.

## Delivered

- Normalized, versioned command envelopes and notification requests.
- Conditional DynamoDB workflow records and per-session workflow leases.
- Replay-safe workflow IDs derived from command IDs.
- FIFO command, artifact-ingest, and notification queues with dead-letter queues.
- Partial-batch SQS handling in the artifact and notification Lambda workers.
- Strict Discord CDN download rules, bounded attachment sizes and redirects, SHA-256 hashing, format validation, isolated S3 input keys, and metadata updates only after validation and object persistence.
- DynamoDB-backed guild role policy configured with an ephemeral Discord role select menu and restricted to members with Administrator or Manage Server permission.
- Secrets Manager-backed Discord notification delivery with mentions disabled.
- Canonically named Step Functions Standard state machines.
- Reproducible Linux Lambda packages and CI validation.

## Safety boundary

The lifecycle state machines are explicit `PhaseNotImplemented` definitions and `/session start` is not registered. Phase 5 replaces the `ProvisionSession` boundary with real EC2/EBS/SSM tasks before exposing start controls.

## Deployment inputs still required

- Discord Ed25519 public key.
- Discord application and development guild IDs.
- Discord bot token stored as the existing Secrets Manager secret value, either as plain text or `{ "token": "..." }`.

Administrator, role, and channel IDs do not need to be Terraform inputs. After deployment, a Discord server administrator runs:

```text
/admin access
```

Discord responds with a role select menu. The selected role IDs are stored in DynamoDB and used immediately by the interaction Lambda.
