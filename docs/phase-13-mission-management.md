# Phase 13.2 Mission Management

Arma 3 sessions no longer require a mission upload during `/rb create`. When
the field is empty, the configured mission is BI's `MP_ZGM_m12.Stratis` and no
S3 placeholder is created. Selecting **Begin server setup** still queues the
same deterministic automatic start as soon as all required mod content is
ready; a vanilla default-mission session is ready immediately.

Use `/rb edit session:<slug> section:mission-files` to open the private mission
manager. It shows five records per page with their validation state. **Default**
chooses the built-in mission or an accepted uploaded/Workshop scenario for the
next start, wake, or restore. **Remove** logically removes a mission while
preserving its audit record; it is unavailable for the mission currently loaded
by the server.
**Add mission** accepts either a bounded `.pbo` upload or a public Arma 3
Workshop item/collection link through mutually exclusive inputs. The `mods`
section accepts an uploaded Launcher preset or a Workshop item/collection and
retains the Creator DLC workflow formerly exposed as `/rb mods`.

The configured selection is editable desired state. The current selection is
snapshotted when start, wake, or restore begins, so edits never hot-swap a
running server. Uploaded missions use content-addressed object names, allowing
different revisions with the same original filename to coexist. Legacy rows
with only `mission_object_key` are expanded on read and retain the compatibility
field on write.

Newly accepted uploads and Workshop scenarios are copied to `arma3/mpmissions`
on a stable running managed host without restarting Arma or changing the current
mission. Uploaded files use the bounded S3 copy path with checksum and ownership
validation. Workshop scenarios use workflow-isolated SteamCMD staging, validate
the immutable item metadata and payload, publish a bounded result manifest, and
then attach the content-addressed mission record. Sleeping, changing, or
instance-less sessions defer accepted content to normal start/wake bootstrap;
archived sessions reject edits to preserve restore-manifest consistency.

Bootstrap receives the selected template plus a checksum-bound manifest of all
active accepted uploads. It synchronizes that complete set during start, wake,
restore, and replacement-host bootstrap while the selected template alone
remains authoritative for launch. After loading either the generated configuration or an
Administrator-provided `server.cfg`, bootstrap removes the effective
`class Missions` block and appends the platform-generated block. Administrator
settings outside that block remain intact.

## Deployment

Package the Lambda archives, create and review a fresh Terraform plan, apply
only that reviewed plan, verify the bootstrap worker package, and bulk-register
the guild command definition. Registration is required because `/rb mods` is
replaced by `/rb edit`. Never reuse an older saved Terraform plan.
