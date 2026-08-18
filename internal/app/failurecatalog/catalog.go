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
