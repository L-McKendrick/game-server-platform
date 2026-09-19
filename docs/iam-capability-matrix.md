# IAM Capability Matrix

This matrix records the intended responsibility of each deployed runtime role.
Terraform policy documents remain the executable authority. Service APIs such
as EC2 Describe, SSM command observation, and Step Functions logging require
wildcard resources; those entries are read/observe operations unless explicitly
identified otherwise.

| Principal | Intended capabilities | Explicitly excluded |
| --- | --- | --- |
| Discord interaction Lambda | Read and conditionally mutate metadata; query the verified sparse guild/state session index; enqueue artifact, command, notification, and enabled reset requests; write its log group | No EC2, S3 object, secret, SSM, workflow-start, or destructive access |
| Artifact worker | Validate/write session inputs and guild configuration; delete only superseded guild config; consume artifact queue; send tagged live-copy SSM commands; broker an exact Steam exchange for content sync; enqueue commands/notifications | No unrelated secrets, EC2 lifecycle, archive deletion, or arbitrary S3 prefixes |
| Notification worker | Read card metadata/modlist objects; consume notification queue; read only Discord bot token; call Discord over HTTPS | No lifecycle mutation, EC2/SSM, archive write/delete, or Steam secret |
| Command worker | Read/mutate workflow metadata; consume command queue; start only declared lifecycle state machines; enqueue notifications | No EC2/SSM/S3/secret access and no direct lifecycle implementation |
| Provisioning worker | Conditional metadata writes; tagged EC2/EBS creation; pass only game instance role; observe EC2/SSM; enqueue notifications | No instance termination, S3 objects, or secrets |
| Bootstrap worker | Conditional metadata writes; tagged SSM commands; scoped input/staging delivery and verified Workshop publication; broker an exact Steam exchange; enqueue notifications | No EC2 creation/destruction, unrelated secret reads, or arbitrary S3 objects |
| Monitor worker | Scan/update session health; tagged SSM health commands; enqueue health/lifecycle requests; write metrics/logs | No EC2 mutation, secrets, or artifact mutation |
| Sleep/wake worker | Conditional metadata writes; describe EC2; start/stop only project/environment-tagged instances; tagged SSM commands; read Workshop manifests; broker an exact Steam exchange; enqueue notifications | No EC2 create/terminate, EBS mutation, unrelated secrets, or arbitrary S3 prefixes |
| Archive worker | Conditional metadata writes; scoped create-only archive issuance and trusted object/manifest verification; manifest write; tagged SSM archive command; tagged start/terminate/delete during guarded cleanup; notifications | No untagged compute mutation, arbitrary S3 prefixes, or secrets |
| Restore worker | Conditional metadata writes; read verified archives; constrained tagged EC2/EBS creation; pass only game role; tagged SSM; broker an exact Steam exchange; notifications | No archive mutation/deletion, untagged compute mutation, or unrelated secrets |
| Termination worker | Conditional metadata writes; list/delete only versioned session prefix; terminate/delete tagged session compute; notifications | No creation, SSM, secrets, or non-session S3 prefixes |
| Reliability worker | Reconcile metadata/workflows; observe SSM/EC2/S3; sweep owned expired host-attempt versions; scoped delivery/publication for recovered content; tag/quarantine or clean tagged orphan compute; inspect/redrive declared DLQs; broker an exact Steam exchange; notifications | No S3 deletion outside Steam exchanges/host-attempt staging, security-group deletion, resource creation, or unrelated secrets |
| Reset worker | Execute separately confirmed environment reset: metadata/session-prefix deletion, tagged compute cleanup, declared queue purge/workflow stop/log cleanup, Discord message cleanup | No resource creation, non-project compute, non-session S3 objects, Steam secret, or Terraform state |
| Shared workflow role | Invoke declared lifecycle workers, send declared continuation messages, deliver Step Functions logs | No DynamoDB, EC2, S3, secrets, SSM, or arbitrary Lambda invocation |
| Managed game instance | Reviewed AmazonSSMManagedInstanceCore; exact expiring asset/Steam HTTPS capabilities during trusted authorized operations | No standing S3 grants, EC2/IAM/queue/workflow, Secrets Manager, or Steam-lease DynamoDB APIs; actual-host cross-session/guild and secret/lease denial plus coherent lifecycle acceptance passed in 19.3.8 |
| Steam enrollment role | Short-lived trusted operator assumption; update only Steam authorization secret and its exact DynamoDB lease key | No secret read through IAM policy, game infrastructure, sessions, queues, or Terraform state |
| Terraform bootstrap/deployer | Create and update declared infrastructure and protected remote state | Runtime roles must never read Terraform state; scoped OIDC deployment roles are deferred to 20.3 |

## Policy review rules

Phase 19.3.7 source cutover removes both host inline policies. Six existing
issuers receive exact purpose-prefix grants for versioned input/staging reads,
tagged attempt writes, immutable Workshop publication and transactional attempt
authority checks. Archive writes belong only to archive; pinned archive reads
belong to archive/restore. Reliability alone gains expired-attempt version
listing/deletion; termination retains session-wide all-version deletion and gains
strong session-partition queries. These are trusted control-plane roles: strong
application ownership checks, rather than IAM wildcards across session IDs,
authorize each capability and acceptance. Bounded native SSM output replaces
host S3 transcripts. The shared runtime guard and normalized full script digest
apply to every issuer. Quiesced rollout/rollback is in
`docs/phase-19-host-object-access.md`. On 2026-09-18, the original test-67 host had
no inline policy and only the reviewed SSM attachment. Its actual credentials
passed STS identity and failed populated cross-session/guild S3 read/write and
secret/Steam-lease probes. Capability key/version/expiry/upload constraints passed.
Full final-revision lifecycle acceptance remains pending; operator adapter tests
must not be treated as proof of deployed worker permissions.

The sole host managed attachment includes GetParameter/GetParameters on all
resources. Platform secrets must not be stored in Parameter Store. The asset
bucket policy is a TLS Deny, with no Allow grant to hosts. Source attachment and
bucket review is not evidence of effective deployed credential denial.

- Mutating EC2/EBS actions require exact account/region resources plus Project
  and Environment resource-tag conditions wherever AWS supports them.
- `iam:PassRole` names only the managed game-instance role and requires the EC2
  service target.
- `ssm:SendCommand` requires both the fixed AWS RunShellScript document and
  project/environment-tagged instances.
- S3 permissions name exact bucket prefixes; bucket policies deny insecure
  transport and public-access blocks remain enabled.
- Secret reads and writes name one secret ARN. Discord and Steam secrets are
  never shared between workers.
- Queue consumers and producers name only their required queues. DLQ redrive is
  isolated to the reliability worker and declared DLQs.
- Wildcard resources require a documented AWS API limitation and should remain
  observation or service-delivery capabilities, not unconstrained mutation.
