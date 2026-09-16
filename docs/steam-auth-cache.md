# Steam Authorization Cache Runbook

## Validated design

Valve's Steamworks CI/CD documentation instructs operators to authenticate
SteamCMD once, preserve `<Steam>/config/config.vdf`, use username-only login on
future runs, and retain successful updates to that file. It also warns that
providing the password again causes another Steam Guard token to be required:

- <https://partner.steamgames.com/doc/sdk/uploading?l=english#527>

This platform follows that documented mechanism for authenticated Arma 3 and
Workshop downloads. It does not generate Guard codes, disable Steam Guard, or
accept a Steam password through Discord.

The cache contract stored as an encrypted Secrets Manager value is:

```json
{
  "schema_version": 1,
  "cache_format": "steamcmd-config-vdf",
  "status": "ACTIVE",
  "username": "dedicated_account_name",
  "config_vdf_base64": "base64 encoded config.vdf",
  "config_sha256": "lowercase SHA-256",
  "enrolled_at": "RFC3339 UTC",
  "updated_at": "RFC3339 UTC",
  "source_version_id": "prior Secrets Manager version ID or empty"
}
```

Secrets Manager encrypts the value at rest with its AWS managed KMS key;
`AWSCURRENT` is the active cache and `AWSPREVIOUS` is the rollback point. Only
trusted lifecycle workers and the MFA-gated enrollment role can use the cache.
The managed game-instance role has no Secrets Manager or Steam-lease DynamoDB
permission. DynamoDB key `STEAM_AUTH#CACHE / STATE` stores only non-secret
status, version/checksum evidence, and the global mutation lease. The managed
key keeps this same-account design cost-bounded and avoids a separately billed
key while the exact-secret IAM policies remain the access-control boundary.

This matches AWS's documented encrypted secret-version model and staging
labels: <https://docs.aws.amazon.com/secretsmanager/latest/userguide/whats-in-a-secret.html>.
It also follows the Systems Manager warning not to place secrets directly in
Run Command text:
<https://docs.aws.amazon.com/systems-manager/latest/userguide/running-commands.html>.

## Initial enrollment or reauthorization

Use a dedicated Steam account that owns Arma 3 and required Workshop content.
Perform these steps on a trusted operator workstation, never on a game host.

1. Create an isolated SteamCMD directory outside the repository. Start that
   copy interactively with `steamcmd.exe +login <username>`. Enter the password
   and approve or enter Steam Guard only at SteamCMD's local prompt.
2. Run `info` and verify the account is connected, then run `quit`. Locate the
   resulting `<isolated SteamCMD>/config/config.vdf`.
3. Assume the Terraform output `steam_auth_enrollment_role_arn` with MFA and
   `--duration-seconds 900`. Export the returned temporary AWS credentials only
   in the current operator process. Do not paste them into a script or log.
4. Enroll the verified cache:

   ```powershell
   ./scripts/steam-auth-cache.ps1 -Action Enroll `
     -SecretId '/game-server-platform/dev/steam-authorization-cache' `
     -MetadataTableName 'game-server-platform-dev-metadata' `
     -Region 'us-west-2' `
     -Username '<steam-account-name>' `
     -ConfigVdfPath '<isolated-steamcmd>/config/config.vdf'
   ```

5. Run the same script with `-Action Status`. Confirm `ACTIVE` and matching
   current/state version IDs. Clear the temporary AWS environment variables,
   sign out of the local SteamCMD copy, and securely remove the isolated
   enrollment directory.

The script refuses ordinary AWS identities: its caller ARN must be the
MFA-gated enrollment role. AWS permits a one-hour minimum role maximum, so the
900-second session limit is enforced by the documented assume-role command,
not by the role resource itself. The script uploads the cache from a temporary
local file and removes that file in `finally`; passwords and Guard codes are
never arguments or payload fields.

## Runtime behavior

For an authenticated Steam operation, the trusted lifecycle worker:

1. reloads and validates the exact session, workflow, purpose, and instance;
2. acquires the global cache lease and refuses `REAUTH_REQUIRED`;
3. reads and validates `AWSCURRENT`, then writes a short-lived exact exchange
   object outside every session prefix;
4. passes exact-object presigned HTTPS GET and PUT capabilities—not cache bytes
   or the secret identifier—through the targeted SSM command;
5. validates the returned payload and source version before conditionally
   promoting it to Secrets Manager; and
6. deletes both exchange objects and releases the owner-checked lease on every
   terminal path. S3 lifecycle expiration is a one-day cleanup backstop.

The game host downloads the cache only while the authorized operation is
active, injects `config.vdf` through `/run`-backed directories, invokes
SteamCMD with username only, uploads the bounded updated envelope, and removes
config, sentry, login-user, and Steam log material before Arma can start. It
cannot list exchange objects, call Secrets Manager, or mutate the cache lease.
The exchange expiry spans the bounded Arma or Workshop download and prevents
another workflow from racing the cache update.

Vanilla and modded sessions use the same cached authorization and serialized
lease for the Arma server package. Vanilla sessions still skip preset and
Workshop processing.

## Guard challenge, invalidation, and rollback

When Steam asks for Guard approval, a password, or renewed two-factor login,
bootstrap fails closed with `ERR_STEAM_REAUTH_REQUIRED`; the broker marks state
`REAUTH_REQUIRED`, deletes the exchange, releases its lease, and the host
removes authentication material. Re-run the
local enrollment procedure; do not send authentication data through Discord.

An operator can deliberately invalidate the cache:

```powershell
./scripts/steam-auth-cache.ps1 -Action Invalidate -SecretId '<secret>' -MetadataTableName '<table>' -Region '<region>'
```

If a newly persisted token is defective but the preceding version is known
good, move `AWSPREVIOUS` back to `AWSCURRENT` under the same lease:

```powershell
./scripts/steam-auth-cache.ps1 -Action Rollback -SecretId '<secret>' -MetadataTableName '<table>' -Region '<region>'
```

After rollback, run `Status`, then perform one controlled modded bootstrap.
Repeated Guard rejection means rollback is not valid; invalidate and enroll
again.

## Migration and validation

The Terraform change replaces the former password-oriented secret name with
`/<project>/<environment>/steam-authorization-cache`. After a separately
approved Terraform plan/apply, enroll the new cache before starting a modded
session. Verify that the former credential secret enters its configured
recovery window and is no longer referenced by any IAM policy or worker.

Focused validation includes Go tests for command redaction, username-only
login behavior across vanilla and modded sessions, stable reauthorization errors, archive/restore
scrubbing, PowerShell parsing, Bash syntax, Terraform validation, and a static
search for legacy password-login code. A live enrollment and Steam download
remain an explicit operator acceptance exercise because they require Steam
credentials and create external state.
