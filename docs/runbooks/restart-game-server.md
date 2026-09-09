# Restart a game server

`/rb restart session:<slug>` immediately interrupts Arma on a stable running or
idle session. It requires the session owner or a signed Administrator/Manage
Server member. No player-count check or confirmation delays the restart.
TeamSpeak and EC2 stay running; this command does not sleep or reprovision a host.
Use `/rb start` for sleeping or archived sessions. Archived sessions enter the
restore workflow; `/rb restore` remains available as an explicit equivalent.

The command captures the current guild server.cfg revision and acquires the
existing exclusive session workflow lock. Pending client and server-mod
revisions become applying under that lock; already queued commands revalidate
state and permission in the command worker. Repeating an active request returns
current progress. `/rb status` and the public card show Restarting and the
existing content/service/health milestones.

The RestartSession workflow uses the existing wake state machine and sleep/wake
worker. Its initial branch bypasses EC2 control and wake waits. The bounded SSM
host path stops Arma before changing active content, reuses validated staged
mods, applies accepted mission files and server settings before synchronizing
pending Workshop scenarios, and restarts only Arma. Synchronizing last ensures
an updated scenario with the same filename replaces its previously accepted
file, including legacy single-mission records. Accepted files matching their
checksum are reused; no new mission selection operation is introduced. Without pending
downloads it skips Steam authorization enrollment/login. Missing or invalid
staged content fails safely rather than declaring success.

Health verification promotes pending mod metadata only after Arma is healthy.
The shared SSM health adapter explicitly accepts the `RESTARTING` lifecycle
state and ignores TeamSpeak health for restart acceptance because restart never
touches that service.
Failure retains runtime resources, records actionable status, and releases the
workflow lock without claiming a rollback. Runtime links or settings may already
have changed, and EC2/EBS remain billable. An operator should inspect the support
reference and recover the failed game service before requesting more work.
The existing reliability reconciliation handles interrupted or timed-out
executions and marks applying revisions failed.

SSM dispatch replay looks up the same session/workflow/instance command. A
host-local workflow completion marker also prevents a completed replay from
restarting the service twice. The existing managed-host lock serializes work.

## Deployment and acceptance

Package the Lambda archives and review a fresh Terraform plan. The change updates
the bootstrap script object, the shared wake definition, worker packages, and
adds ListCommands read permission to the sleep/wake worker. It creates no new
Lambda, queue, table, or state machine. Apply only the exact reviewed plan and
register the updated `/rb` commands. Keep provisioning and budget settings
unchanged. Use the commands in CURRENT_WORK.md for the current release.

With a live restart explicitly approved, verify a no-change restart and one with
pending client/server mods or server settings. Confirm one game-server restart
and that a refreshed Workshop scenario with the same filename retains its
new content on disk after restart, matching the newly accepted mission record.
Verify the unchanged EC2 instance and TeamSpeak process, truthful progress/failure output,
and promotion only after health verification. Live acceptance is separate from
offline unit, shell, packaging, and Terraform validation.
