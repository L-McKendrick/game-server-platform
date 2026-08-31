// Package failurecatalog maps stable internal failure codes to safe,
// actionable user presentation. It never consumes raw provider diagnostics.
package failurecatalog

import (
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type Presentation struct {
	WhatHappened     string
	LikelyReason     string
	PlatformAction   string
	UserAction       string
	RetryDisposition string
	BillingImpact    string
	SupportReference string
}

type entry struct {
	what, reason, platformAction, userAction string
}

var entries = map[string]entry{
	"ERR_WORKFLOW_START_FAILED": {
		"The requested operation could not start.",
		"The platform could not hand the operation to its workflow service.",
		"The platform stopped the request and released its session lock.",
		"Check `/rb status`, then run the command again once. If it still fails, give the support reference to an operator.",
	},
	"ERR_AMBIGUOUS_LAUNCH": {
		"Server infrastructure did not reach a confirmed ready state.",
		"The cloud launch result was ambiguous, so creating replacement resources would be unsafe.",
		"The platform stopped before attempting a duplicate launch and retained any discovered resources.",
		"Do not repeat the start command. Give the support reference to an operator so the existing resources can be reconciled.",
	},
	"ERR_PROVISIONING_FAILED": {
		"Server infrastructure could not be prepared.",
		"Cloud capacity, permissions, networking, or instance readiness prevented provisioning from completing.",
		"The platform stopped provisioning and preserved authoritative state for inspection.",
		"Check `/rb status`. If resources remain or the same error returns, give the support reference to an operator.",
	},
	"ERR_BOOTSTRAP_COMMAND_FAILED": {
		"Game and content setup did not complete.",
		"The managed setup command stopped before the game server passed health verification.",
		"The platform stopped the workflow and retained the server for diagnosis.",
		"Give the support reference to an operator before trying another start.",
	},
	"ERR_BOOTSTRAP_FAILED": {
		"Game and content setup did not complete.",
		"Installation, configuration, content setup, or health verification failed.",
		"The platform stopped the workflow and retained the server for diagnosis.",
		"Give the support reference to an operator before trying another start.",
	},
	"ERR_STEAM_REAUTH_REQUIRED": {
		"Steam authorization needs renewed approval.",
		"Steam rejected the cached login token or requested Steam Guard approval again.",
		"The platform invalidated automated authenticated downloads and removed authentication material from the game host.",
		"Ask an operator to run the Steam authorization enrollment procedure. Do not send a password or Steam Guard code through Discord.",
	},
	"ERR_WORKSHOP_UNAVAILABLE": {
		"The Steam Workshop source could not be used.",
		"The item or collection is unavailable, private, deleted, or inaccessible to Steam's public metadata service.",
		"The platform rejected the source without changing the active mission or mod revision.",
		"Confirm the Workshop page is public and submit its canonical shared-file link again.",
	},
	"ERR_WORKSHOP_RATE_LIMITED": {
		"Steam temporarily limited Workshop metadata requests.",
		"Steam asked the platform to slow down while resolving the item or collection.",
		"The platform retained the request for a bounded retry and did not change the active revision.",
		"Wait for the automatic retry. If it ultimately fails, submit the link again later.",
	},
	"ERR_WORKSHOP_TRANSIENT": {
		"Steam Workshop metadata could not be resolved yet.",
		"A temporary Steam service or network failure interrupted metadata resolution.",
		"The platform retained the request for a bounded retry and did not change the active revision.",
		"Wait for the automatic retry. If it ultimately fails, submit the link again later.",
	},
	"ERR_WORKSHOP_REJECTED": {
		"The Steam Workshop source was rejected.",
		"The page or its contents did not satisfy the platform's public Arma 3 content policy.",
		"The platform left the active content unchanged.",
		"Review the source type and tags, then submit an eligible public Arma 3 item or collection.",
	},
	"ERR_WORKSHOP_INVALID_RESPONSE": {
		"Steam returned incomplete Workshop metadata.",
		"The response could not be safely matched to the requested item or collection.",
		"The platform retained the request for a bounded retry and did not change the active revision.",
		"Wait for the automatic retry. If it continues, give the support reference to an operator.",
	},
	"ERR_WORKSHOP_SCENARIO_RESUBMIT": {
		"The Workshop scenario could not be installed from its recorded snapshot.",
		"The publisher changed its filename or content size after the platform resolved the source.",
		"The platform rejected the changed download and retained the previously authoritative mission state.",
		"Resubmit the Workshop link, then retry the failed operation.",
	},
	"ERR_WORKSHOP_SCENARIO_PAYLOAD": {
		"The Workshop scenario download was not deployable.",
		"Steam returned no mission payload, more than one possible payload, an unsafe link, or a payload outside the allowed size.",
		"The platform did not install the ambiguous or unsafe scenario and retained the server for recovery.",
		"Confirm the item is a public multiplayer Arma 3 scenario. Resubmit its link, or upload a correctly named mission PBO instead.",
	},
	"ERR_WORKSHOP_DISK_SPACE": {
		"The Workshop content could not be staged on the server.",
		"The managed data volume does not have enough free space for the isolated download and validated copy.",
		"The platform removed temporary staging and retained the active mission and mods.",
		"Remove unused session content or ask an operator to expand the data volume, then submit the Workshop link again.",
	},
	"ERR_WORKSHOP_VISIBILITY": {
		"A recorded Workshop item is not downloadable.",
		"The item is private, friends-only, restricted, or otherwise inaccessible to the server's Steam account.",
		"The platform stopped the exact snapshot and retained active content.",
		"Make every required item Public, confirm its page opens while signed out, then submit the item or collection link again.",
	},
	"ERR_WORKSHOP_ITEM_REMOVED": {
		"A recorded Workshop item is no longer available.",
		"The publisher removed the item or Steam no longer returns its downloadable files.",
		"The platform stopped without substituting different content and retained active content.",
		"Remove or replace the unavailable child in the collection, then submit the source again.",
	},
	"ERR_WORKSHOP_DOWNLOAD_TIMEOUT": {
		"A Workshop item did not download within the bounded retries.",
		"Steam or the network remained unavailable long enough to exhaust the item-level retry budget.",
		"The platform cleaned temporary staging and retained active content.",
		"Wait a few minutes and submit the same link again. If it repeats for one item, verify that item's availability.",
	},
	"ERR_WORKSHOP_ITEM_DOWNLOAD": {
		"An individual Workshop item could not be downloaded.",
		"Steam rejected or could not complete one item in the recorded item or collection snapshot.",
		"The platform stopped the snapshot, cleaned temporary staging, and retained active content.",
		"Check that every collection child is public and available, then submit the source again. If it repeats, give the support reference to an operator.",
	},
	"ERR_WORKSHOP_SYNC_DISPATCH": {
		"Workshop synchronization could not start on the managed server.",
		"The recorded instance changed, was no longer managed, or Systems Manager could not accept the command.",
		"The platform released the content lock and retained active content.",
		"Check `/rb status`, wait for any lifecycle operation to finish, then submit the Workshop link again.",
	},
	"ERR_WORKSHOP_METADATA_DRIFT": {
		"The downloaded Workshop item no longer matches the recorded snapshot.",
		"The publisher updated the item after the platform resolved its metadata.",
		"The platform rejected the changed files and retained active content.",
		"Submit the Workshop item or collection link again to create a new immutable snapshot.",
	},
	"ERR_ARCHIVE_COMMAND": {
		"The archive could not be created and verified.",
		"The managed backup command stopped before a portable archive was verified.",
		"The platform did not treat the archive as complete and did not intentionally delete unverified source resources.",
		"Keep the session stopped and give the support reference to an operator before requesting archive again.",
	},
	"ERR_ARCHIVE_FAILED": {
		"The archive operation did not complete.",
		"Backup creation, checksum verification, storage, or guarded resource cleanup failed.",
		"The platform preserved the last authoritative session and archive state.",
		"Check `/rb status` and give the support reference to an operator before requesting archive again.",
	},
	"ERR_RESTORE_COMMAND": {
		"Archived data could not be restored to the replacement server.",
		"The managed restore command stopped before data and services passed verification.",
		"The platform stopped the restore and retained authoritative archive metadata.",
		"Give the support reference to an operator before requesting restore again.",
	},
	"ERR_RESTORE_FAILED": {
		"The restore operation did not complete.",
		"Archive verification, replacement infrastructure, data restoration, or health verification failed.",
		"The platform stopped the restore and preserved the verified archive record.",
		"Check `/rb status` and give the support reference to an operator before requesting restore again.",
	},
	"ERR_WAKE_HEALTH": {
		"The server started but did not become healthy.",
		"The game service or an enabled voice service did not pass the post-wake health check.",
		"The platform stopped the wake workflow without declaring the session playable.",
		"Give the support reference to an operator before requesting wake again.",
	},
	"ERR_SLEEP_WAKE_FAILED": {
		"The requested sleep or wake operation did not complete.",
		"Instance control, managed-resource verification, or health verification failed.",
		"The platform stopped the operation and preserved the last confirmed state.",
		"Check `/rb status` and give the support reference to an operator before repeating the command.",
	},
	"ERR_TERMINATION_FAILED": {
		"Permanent deletion did not complete.",
		"One or more guarded infrastructure or stored-object deletion stages could not be verified.",
		"The platform stopped without claiming that all resources were deleted.",
		"Give the support reference to an operator so remaining resources and artifacts can be reconciled.",
	},
}

func Lookup(failure domain.FailureRecord) Presentation {
	item, ok := entries[failure.Code]
	if !ok && strings.HasPrefix(failure.Code, "ERR_BOOTSTRAP_COMMAND_") {
		item, ok = entries["ERR_BOOTSTRAP_COMMAND_FAILED"]
	}
	if !ok {
		item = entry{
			what:           "The operation stopped before completion.",
			reason:         "The platform recorded an unexpected error without exposing unsafe diagnostics.",
			platformAction: "The platform stopped the operation and preserved authoritative state for inspection.",
			userAction:     "Check `/rb status` and give the support reference to an operator before repeating the command.",
		}
	}
	return Presentation{
		WhatHappened: item.what, LikelyReason: item.reason,
		PlatformAction: item.platformAction, UserAction: item.userAction,
		RetryDisposition: retryText(failure.RetryDisposition),
		BillingImpact:    billingText(failure.ResourceImpact),
		SupportReference: failure.SupportReference,
	}
}

func retryText(disposition domain.RetryDisposition) string {
	if disposition == domain.RetryScheduled {
		return "A retry is scheduled by the platform."
	}
	return "No retry is scheduled."
}

func billingText(impact domain.ResourceCostImpact) string {
	switch impact {
	case domain.ResourceCostNone:
		return "No billable game-server resources remain from this operation."
	case domain.ResourceCostRetained:
		return "Resources remain and may continue to incur cost."
	default:
		return "Resource cleanup is not confirmed; resources may remain and incur cost."
	}
}
