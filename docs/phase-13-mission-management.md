# Phase 13.2 Mission Management

Arma 3 sessions no longer require a mission upload during `/rb create`. When
the field is empty, the configured mission is BI's `MP_ZGM_m12.Stratis` and no
S3 placeholder is created. Selecting **Begin server setup** still queues the
same deterministic automatic start as soon as all required mod content is
ready; a vanilla default-mission session is ready immediately.

Use `/rb edit session:<slug> section:mission-files` to open the private mission
manager. It shows five records per page with their validation state. **Default**
chooses the built-in mission or an accepted upload for the next start, wake, or
restore. **Remove** logically removes an upload while preserving its audit
record; it is unavailable for the mission currently loaded by the server.
**Add mission** opens a bounded `.pbo` upload modal. The `mods` section retains
the existing Launcher preset and Creator DLC workflow formerly exposed as
`/rb mods`.

The configured selection is editable desired state. The current selection is
snapshotted when start, wake, or restore begins, so edits never hot-swap a
running server. Uploaded missions use content-addressed object names, allowing
different revisions with the same original filename to coexist. Legacy rows
with only `mission_object_key` are expanded on read and retain the compatibility
field on write.

Bootstrap receives both the selected template and optional S3 key. It downloads
an accepted upload exactly when a key exists; otherwise it launches the built-in
template. After loading either the generated configuration or an
Administrator-provided `server.cfg`, bootstrap removes the effective
`class Missions` block and appends the platform-generated block. Administrator
settings outside that block remain intact.

## Deployment

Package the Lambda archives, create and review a fresh Terraform plan, apply
only that reviewed plan, verify the bootstrap worker package, and bulk-register
the guild command definition. Registration is required because `/rb mods` is
replaced by `/rb edit`. Never reuse an older saved Terraform plan.
