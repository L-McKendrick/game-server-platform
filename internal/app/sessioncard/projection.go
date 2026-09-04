package sessioncard

import (
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/L-McKendrick/game-server-platform/internal/app/failurecatalog"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const (
	ArmaGamePort       = 2302
	TeamSpeakVoicePort = 9987
)

var discordIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Projection is the application-owned, adapter-neutral source for every
// public card and private detailed status view. It deliberately contains no
// immutable session, workflow, or infrastructure identifiers.
type Projection struct {
	Revision    int64
	Name        string
	Slug        string
	Description string
	Game        string
	Mode        string
	TeamSpeak   bool

	Lifecycle          string
	Health             string
	CurrentOperation   string
	Stage              string
	OperationStartedAt time.Time
	StatusSince        time.Time
	Elapsed            time.Duration
	Progress           ProgressProjection
	LifecycleTiming    LifecycleTimingProjection

	Players    PlayerProjection
	Endpoints  EndpointProjection
	Mods       ModsProjection
	ServerMods ServerModsProjection
	Failure    FailureProjection
	Freshness  FreshnessProjection
	Artifacts  ArtifactProjection
}

type ProgressProjection struct {
	Visible   bool
	Bar       string
	Step      int
	Total     int
	Completed int
	Condition string
	Guidance  string
	Activity  string
}

type LifecycleTimingProjection struct {
	Label string
	DueAt time.Time
}

type PlayerProjection struct {
	Available  bool
	Count      int
	Capacity   int
	Names      []string
	Mission    string
	Map        string
	ObservedAt time.Time
}

type ConnectionProjection struct {
	Available   bool
	Host        string
	AddressType string
	Port        int
	Offline     bool
	ObservedAt  time.Time
}

type EndpointProjection struct {
	Game      ConnectionProjection
	TeamSpeak ConnectionProjection
}

type ModsProjection struct {
	Required                bool
	Status                  string
	ActiveRevision          int64
	ActiveSince             time.Time
	PendingRevision         int64
	PendingStatus           string
	PendingSince            time.Time
	DownloadURL             string
	CreatorDLCs             []string
	ActiveWorkshopSourceID  uint64
	PendingWorkshopSourceID uint64
	Issue                   string
}

type ServerModsProjection struct {
	Status          string
	Issue           string
	ActiveRevision  int64
	PendingRevision int64
	PendingStatus   string
}

type FailureProjection struct {
	Present          bool
	Summary          string
	Reason           string
	PlatformAction   string
	UserAction       string
	RetryDisposition string
	BillingImpact    string
	SupportReference string
	OccurredAt       time.Time
}

type FreshnessProjection struct {
	SessionUpdatedAt         time.Time
	InfrastructureObservedAt time.Time
	PlayersObservedAt        time.Time
}

type ArtifactProjection struct {
	Mission ArtifactView
	Preset  ArtifactView
}

type ArtifactView struct {
	Status string
	Issue  string
}

// Options carries point-in-time projection inputs that are not part of the
// session aggregate without introducing a second card model.
type Options struct {
	Now               time.Time
	Workflow          *domain.Workflow
	Players           *domain.PlayerStatus
	PlayersObservedAt time.Time
	GameDNS           string
	TeamSpeakDNS      string
	ModlistURL        string
}

// Project builds one safe presentation model from authoritative state.
func Project(session domain.Session, options Options) Projection {
	now := options.Now.UTC()
	if now.IsZero() {
		now = session.UpdatedAt.UTC()
	}
	sessionUpdatedAt := session.UpdatedAt.UTC()
	if sessionUpdatedAt.IsZero() {
		sessionUpdatedAt = now
	}
	projection := Projection{
		Revision: session.Version, Name: session.DisplayName, Slug: session.Slug,
		Description: session.Description, Game: gameLabel(session.GameType),
		Mode: modeLabel(session.Vanilla), TeamSpeak: session.TeamSpeakEnabled,
		Lifecycle: LifecycleLabel(session.LifecycleState), Health: HealthLabel(session.HealthStatus),
		CurrentOperation: operationLabel(session.ActiveWorkflowType), Stage: stageLabel(session),
		Mods:       modProjection(session, options.ModlistURL),
		ServerMods: serverModProjection(session),
		Artifacts: ArtifactProjection{
			Mission: missionArtifactView(session),
			Preset:  presetArtifactView(session),
		},
		Freshness: FreshnessProjection{
			SessionUpdatedAt:         sessionUpdatedAt,
			InfrastructureObservedAt: session.Infrastructure.LastObservedAt.UTC(),
		},
	}
	if label := progressLabel(session.Progress.Milestone); label != "" {
		projection.Stage = label
	}
	if session.Progress.WorkflowType == domain.WorkshopContentSyncWorkflowType {
		switch session.Progress.State {
		case domain.ProgressActive, domain.ProgressWaiting, domain.ProgressRetrying:
			projection.Stage = "Downloading and validating"
		case domain.ProgressCompletedState:
			projection.Stage = "Available"
		case domain.ProgressActionRequired:
			projection.Stage = "Action required"
		}
	}
	projection.Progress = progressProjection(session, options.Workflow, now)
	projection.LifecycleTiming = lifecycleTimingProjection(session, now)
	if session.LifecycleState == domain.StateRunning || session.LifecycleState == domain.StateIdle {
		if session.Progress.State == domain.ProgressCompletedState && !session.Progress.LastProgressAt.IsZero() {
			projection.StatusSince = session.Progress.LastProgressAt.UTC()
		}
	}
	if session.LifecycleState == domain.StateDeleted {
		projection.StatusSince = sessionUpdatedAt
		if session.Progress.State == domain.ProgressCompletedState && !session.Progress.LastProgressAt.IsZero() {
			projection.StatusSince = session.Progress.LastProgressAt.UTC()
		}
	}

	if projection.Progress.Visible && !session.Progress.StartedAt.IsZero() {
		projection.OperationStartedAt = session.Progress.StartedAt.UTC()
		elapsedAt := now
		if (session.Progress.State == domain.ProgressCompletedState || session.Progress.State == domain.ProgressActionRequired || session.Progress.State == domain.ProgressCancelled) && !session.Progress.LastProgressAt.IsZero() {
			elapsedAt = session.Progress.LastProgressAt.UTC()
		}
		if elapsedAt.After(projection.OperationStartedAt) {
			projection.Elapsed = elapsedAt.Sub(projection.OperationStartedAt).Round(time.Second)
		}
	} else if session.ActiveWorkflowStartedAt.IsZero() == false {
		projection.OperationStartedAt = session.ActiveWorkflowStartedAt.UTC()
		if now.After(projection.OperationStartedAt) {
			projection.Elapsed = now.Sub(projection.OperationStartedAt).Round(time.Second)
		}
	}
	workflowMatches := options.Workflow != nil && options.Workflow.SessionID == session.ID &&
		(session.ActiveWorkflowID == "" || options.Workflow.ID == session.ActiveWorkflowID)
	workflowActive := workflowMatches && (options.Workflow.Status == domain.WorkflowPending || options.Workflow.Status == domain.WorkflowRunning)
	if workflowMatches && session.Progress.Milestone == "" {
		if strings.TrimSpace(options.Workflow.CurrentStage) != "" {
			projection.Stage = humanize(options.Workflow.CurrentStage)
		}
	}
	if workflowActive {
		projection.CurrentOperation = operationLabel(options.Workflow.Type)
		if projection.OperationStartedAt.IsZero() && !options.Workflow.StartedAt.IsZero() {
			projection.OperationStartedAt = options.Workflow.StartedAt.UTC()
			if now.After(projection.OperationStartedAt) {
				projection.Elapsed = now.Sub(projection.OperationStartedAt).Round(time.Second)
			}
		}
	}

	if options.Players != nil {
		projection.Freshness.PlayersObservedAt = options.PlayersObservedAt.UTC()
		projection.Players = PlayerProjection{
			Available:  true,
			Count:      max(0, options.Players.PlayerCount),
			Capacity:   max(0, options.Players.MaxPlayers),
			Names:      append([]string(nil), options.Players.PlayerNames...),
			Mission:    options.Players.MissionName,
			Map:        options.Players.MapName,
			ObservedAt: options.PlayersObservedAt.UTC(),
		}
	}

	endpointsVisible, offline := endpointVisibility(session.LifecycleState)
	gameHost, gameAddressType := preferredEndpoint(options.GameDNS, session.Infrastructure.PublicIPv4)
	if endpointsVisible && gameHost != "" {
		projection.Endpoints.Game = ConnectionProjection{
			Available: true, Host: gameHost, AddressType: gameAddressType, Port: ArmaGamePort, Offline: offline,
			ObservedAt: session.Infrastructure.LastObservedAt.UTC(),
		}
	}
	teamSpeakHost, teamSpeakAddressType := preferredEndpoint(options.TeamSpeakDNS, session.Infrastructure.PublicIPv4)
	if endpointsVisible && session.TeamSpeakEnabled && teamSpeakHost != "" {
		projection.Endpoints.TeamSpeak = ConnectionProjection{
			Available: true, Host: teamSpeakHost, AddressType: teamSpeakAddressType, Port: TeamSpeakVoicePort, Offline: offline,
			ObservedAt: session.Infrastructure.LastObservedAt.UTC(),
		}
	}

	var workflow *domain.Workflow
	if workflowMatches {
		workflow = options.Workflow
	}
	projection.Failure = failureProjection(session, workflow)
	return projection
}

func serverModProjection(session domain.Session) ServerModsProjection {
	view := artifactView(session.ServerPresetArtifactStatus, session.ServerPresetObjectKey, session.ServerPresetArtifactIssue, session.Vanilla)
	projection := ServerModsProjection{Status: view.Status, Issue: view.Issue}
	if active := session.EffectiveActiveServerPresetRevision(); !active.Empty() {
		projection.ActiveRevision = active.Number
	}
	if pending := session.PendingServerPresetRevision; !pending.Empty() {
		projection.PendingRevision = pending.Number
		switch pending.Status {
		case domain.PresetRevisionPending:
			projection.PendingStatus = "Staged"
		case domain.PresetRevisionApplying:
			projection.PendingStatus = "Applying"
		case domain.PresetRevisionFailed:
			projection.PendingStatus = "Failed"
		}
	}
	return projection
}

func presetArtifactView(session domain.Session) ArtifactView {
	if !session.Vanilla && artifactAcceptedServerPreset(session) && session.PresetArtifactStatus == "" && session.PresetObjectKey == "" {
		return ArtifactView{Status: "Not needed without client mods"}
	}
	if !session.Vanilla && len(session.CreatorDLCs) > 0 && session.PresetArtifactStatus == "" && session.PresetObjectKey == "" {
		return ArtifactView{Status: "Not needed for Creator DLC only"}
	}
	return artifactView(session.PresetArtifactStatus, session.PresetObjectKey, session.PresetArtifactIssue, session.Vanilla)
}

// DiscordMessageURL returns a safe, stable jump link without displaying the
// underlying Discord identifiers as card text.
func DiscordMessageURL(guildID, channelID, messageID string) string {
	for _, value := range []string{guildID, channelID, messageID} {
		if !discordIdentifierPattern.MatchString(strings.TrimSpace(value)) {
			return ""
		}
	}
	return "https://discord.com/channels/" + strings.TrimSpace(guildID) + "/" + strings.TrimSpace(channelID) + "/" + strings.TrimSpace(messageID)
}

func normalizeModlistURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host != "discord.com" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) != 4 || segments[0] != "channels" {
		return ""
	}
	for _, segment := range segments[1:] {
		decoded, decodeErr := url.PathUnescape(segment)
		if decodeErr != nil || !discordIdentifierPattern.MatchString(decoded) {
			return ""
		}
	}
	return parsed.String()
}

// IsActiveModlistReference prevents a persisted companion-message link from
// being shown after revision promotion but before the replacement attachment
// has been delivered. Legacy sessions without modlist metadata remain valid.
func IsActiveModlistReference(session domain.Session, reference domain.SessionModlistReference) bool {
	active := session.EffectiveActivePresetRevision()
	if active.Modlist.Empty() {
		return strings.TrimSpace(session.PresetObjectKey) != ""
	}
	return reference.SessionID == session.ID && reference.ChannelID == session.ChannelID &&
		reference.ObjectKey == active.Modlist.ObjectKey && reference.Filename == active.Modlist.Filename &&
		reference.ContentSHA256 == active.Modlist.SHA256 && reference.DeliveredRevision <= session.Version
}

// IsActiveModlistAttachment binds queued bytes to current active authority.
func IsActiveModlistAttachment(session domain.Session, attachment domain.NotificationAttachment) bool {
	active := session.EffectiveActivePresetRevision()
	if active.Modlist.Empty() {
		return strings.TrimSpace(session.PresetObjectKey) != ""
	}
	return attachment.ObjectKey == active.Modlist.ObjectKey && attachment.Filename == active.Modlist.Filename &&
		attachment.SHA256 == active.Modlist.SHA256 && attachment.SizeBytes == active.Modlist.SizeBytes
}

func progressLabel(milestone domain.ProgressMilestone) string {
	switch milestone {
	case domain.ProgressAccepted:
		return "Request accepted"
	case domain.ProgressCapacityReserved:
		return "Reserving capacity"
	case domain.ProgressComputeReady:
		return "Starting compute"
	case domain.ProgressInfrastructureReady:
		return "Infrastructure ready"
	case domain.ProgressHostPrepared:
		return "Preparing host"
	case domain.ProgressGameServerInstalled:
		return "Downloading and installing game files"
	case domain.ProgressModsApplied:
		return "Synchronizing Workshop content"
	case domain.ProgressConfigurationReady:
		return "Deploying configuration"
	case domain.ProgressServiceStarted:
		return "Starting services"
	case domain.ProgressGameContentSetup:
		return "Game and content setup"
	case domain.ProgressHealthVerification:
		return "Verifying health"
	case domain.ProgressInstanceStopped:
		return "Stopping instance"
	case domain.ProgressArchiveCreated:
		return "Creating archive"
	case domain.ProgressArchiveVerified:
		return "Verifying archive"
	case domain.ProgressDataRestored:
		return "Restoring data"
	case domain.ProgressRuntimeRemoved:
		return "Removing runtime resources"
	case domain.ProgressArtifactsRemoved:
		return "Deleting stored artifacts"
	case domain.ProgressResourcesInspected:
		return "Inspecting resources"
	case domain.ProgressMetadataReconciled:
		return "Reconciling state"
	case domain.ProgressCompleted:
		return "Completed"
	case domain.ProgressFailed:
		return "Failed"
	default:
		return ""
	}
}

// ProgressStageLabel exposes the same sanitized stage vocabulary to concise
// interaction acknowledgements without duplicating renderer text.
func ProgressStageLabel(milestone domain.ProgressMilestone) string {
	return progressLabel(milestone)
}

func ProgressStep(workflowType string, milestone domain.ProgressMilestone) (int, int, bool) {
	milestones, ok := domain.MilestonesForWorkflow(workflowType)
	if !ok {
		return 0, 0, false
	}
	index := slices.Index(milestones, milestone)
	return index + 1, len(milestones), index >= 0
}

func progressProjection(session domain.Session, workflow *domain.Workflow, now time.Time) ProgressProjection {
	progress := session.Progress
	milestones, ok := domain.MilestonesForWorkflow(progress.WorkflowType)
	if !ok || progress.Milestone == "" {
		return ProgressProjection{}
	}
	current := slices.Index(milestones, progress.Milestone)
	if current < 0 {
		return ProgressProjection{}
	}
	completed := make(map[domain.ProgressMilestone]bool, len(progress.CompletedMilestones))
	for _, milestone := range progress.CompletedMilestones {
		completed[milestone] = true
	}
	var bar strings.Builder
	for _, milestone := range milestones {
		if completed[milestone] {
			bar.WriteRune('■')
		} else {
			bar.WriteRune('□')
		}
	}
	condition := progressCondition(session, workflow, now)
	return ProgressProjection{
		Visible: true, Bar: bar.String(), Step: current + 1,
		Total: len(milestones), Completed: len(progress.CompletedMilestones),
		Condition: condition, Guidance: progressGuidance(progress.Milestone, condition), Activity: progressActivity(progress),
	}
}

func progressActivity(progress domain.SessionProgress) string {
	if progress.Milestone != domain.ProgressGameServerInstalled && progress.Milestone != domain.ProgressModsApplied {
		return ""
	}
	return strings.TrimSpace(progress.Activity)
}

func lifecycleTimingProjection(session domain.Session, now time.Time) LifecycleTimingProjection {
	switch {
	case (session.LifecycleState == domain.StateRunning || session.LifecycleState == domain.StateIdle) &&
		session.PlayerCountKnown && session.PlayerCount == 0 && !session.IdleSince.IsZero() && !session.IdleSince.After(now):
		return LifecycleTimingProjection{Label: "Automatic sleep", DueAt: session.IdleSince.UTC().Add(domain.AutomaticSleepAfter)}
	case session.LifecycleState == domain.StateSleeping && !session.SleepingSince.IsZero() && !session.SleepingSince.After(now):
		return LifecycleTimingProjection{Label: "Automatic archive", DueAt: session.SleepingSince.UTC().Add(domain.AutomaticArchiveAfter)}
	default:
		return LifecycleTimingProjection{}
	}
}

func progressCondition(session domain.Session, workflow *domain.Workflow, now time.Time) string {
	state := session.Progress.State
	if state == "" {
		switch session.Progress.Milestone {
		case domain.ProgressCompleted:
			state = domain.ProgressCompletedState
		case domain.ProgressFailed:
			state = domain.ProgressActionRequired
		default:
			state = domain.ProgressActive
		}
	}
	leaseExpired := session.ActiveWorkflowID == session.Progress.WorkflowID &&
		!session.ActiveWorkflowLeaseExpiresAt.IsZero() && !now.Before(session.ActiveWorkflowLeaseExpiresAt)
	workflowMatches := workflow != nil && workflow.ID == session.Progress.WorkflowID && workflow.SessionID == session.ID
	if leaseExpired && (state == domain.ProgressActive || state == domain.ProgressWaiting) {
		return "Stalled"
	}
	switch state {
	case domain.ProgressWaiting:
		return "Waiting"
	case domain.ProgressRetrying:
		return "Retrying"
	case domain.ProgressRollingBack:
		return "Rollback"
	case domain.ProgressCompletedState:
		return "Completed"
	case domain.ProgressActionRequired:
		return "Action required"
	case domain.ProgressCancelled:
		return "Action required (cancelled)"
	default:
		if workflowMatches && workflow.Status == domain.WorkflowPending {
			return "Waiting"
		}
		return "Active"
	}
}

func progressGuidance(milestone domain.ProgressMilestone, condition string) string {
	switch condition {
	case "Waiting":
		return "Waiting for the current platform check; no additional operation was queued."
	case "Stalled":
		return "The workflow lease expired without a newer durable checkpoint; review status before taking action."
	case "Retrying":
		return "A bounded retry recorded by the workflow is in progress."
	case "Rollback":
		return "Returning to the prior known-good mod configuration; no automatic retry is scheduled afterward."
	case "Completed":
		return "The workflow finished; required steps completed and non-applicable steps remained skipped."
	case "Action required":
		return "Progress stopped; follow the action below. No retry is scheduled."
	case "Action required (cancelled)":
		return "The operation was cancelled without completing the current step."
	}
	switch milestone {
	case domain.ProgressGameServerInstalled, domain.ProgressModsApplied:
		return "Often the longest stage; large modlists may take considerably longer."
	case domain.ProgressArchiveCreated, domain.ProgressDataRestored:
		return "Large saved-data sets may take longer."
	case domain.ProgressAccepted:
		return "The request is recorded and work is beginning."
	default:
		return "Usually a few minutes."
	}
}

func LifecycleLabel(state domain.LifecycleState) string {
	switch state {
	case "", domain.StateDraft, domain.StateNew, domain.StateValidating, domain.StateProvisioning,
		domain.StateBootstrapping, domain.StateInstalling:
		return "Setting up"
	case domain.StateReady:
		return "Ready"
	case domain.StateWaking, domain.StateRestoring:
		return "Starting"
	case domain.StateRunning, domain.StateIdle:
		return "Running"
	case domain.StateStopping, domain.StateSleeping, domain.StateWarning1, domain.StateWarning2,
		domain.StateArchiving, domain.StateDestroying:
		return "Sleeping"
	case domain.StateArchived:
		return "Archived"
	case domain.StateDeleting, domain.StateDeleted:
		return "Terminated"
	default:
		return "Action required"
	}
}

func HealthLabel(status domain.HealthStatus) string {
	switch status {
	case domain.HealthStarting:
		return "Starting"
	case domain.HealthHealthy:
		return "Healthy"
	case domain.HealthDegraded:
		return "Degraded — action may be required"
	case domain.HealthUnhealthy:
		return "Unhealthy — action required"
	case domain.HealthStopped:
		return "Stopped"
	default:
		return "Not available"
	}
}

func stageLabel(session domain.Session) string {
	switch session.LifecycleState {
	case domain.StateDraft:
		if session.MissionArtifactStatus == domain.ArtifactRejected || session.PresetArtifactStatus == domain.ArtifactRejected {
			return "Setup action required"
		}
		if session.MissionArtifactStatus == domain.ArtifactPending || session.PresetArtifactStatus == domain.ArtifactPending {
			return "Validating uploads"
		}
		return "Preparing setup"
	case domain.StateNew, domain.StateValidating:
		return "Accepted"
	case domain.StateProvisioning:
		return "Infrastructure"
	case domain.StateBootstrapping, domain.StateInstalling:
		return "Game and content setup"
	case domain.StateReady, domain.StateWaking, domain.StateRestoring:
		return "Health verification"
	case domain.StateRunning, domain.StateIdle:
		return "Playable"
	case domain.StateFailed:
		return "Stopped"
	default:
		return LifecycleLabel(session.LifecycleState)
	}
}

func operationLabel(value string) string {
	switch strings.TrimSpace(value) {
	case "ProvisionSession":
		return "Starting server"
	case domain.BootstrapWorkflowType:
		return "Setting up game and content"
	case domain.SleepWorkflowType:
		return "Putting server to sleep"
	case domain.WakeWorkflowType:
		return "Waking server"
	case domain.ArchiveWorkflowType:
		return "Archiving server"
	case domain.RestoreWorkflowType:
		return "Restoring server"
	case domain.TerminationWorkflowType:
		return "Terminating server"
	case domain.WorkshopContentSyncWorkflowType:
		return "Synchronizing Workshop content"
	default:
		return ""
	}
}

func failureProjection(session domain.Session, workflow *domain.Workflow) FailureProjection {
	if session.MissionArtifactStatus == domain.ArtifactRejected || session.PresetArtifactStatus == domain.ArtifactRejected {
		return FailureProjection{
			Present: true, Summary: "One or more setup files were rejected.",
			UserAction:       "Use `/rb setup` to replace the rejected file.",
			RetryDisposition: "No retry is scheduled.", BillingImpact: "No billable game-server resources were created for this draft.",
		}
	}
	if !session.Failure.Empty() {
		presentation := failurecatalog.Lookup(session.Failure)
		if hasLegacyWorkshopMissionSource(session) && strings.HasPrefix(session.Failure.Code, "ERR_BOOTSTRAP") {
			return FailureProjection{
				Present: true, Summary: "A Workshop scenario source must be submitted again before setup can continue.",
				Reason:           "The source was accepted before canonical Steam scenario filenames were recorded.",
				PlatformAction:   "The platform stopped without guessing a mission filename or changing the current mission.",
				UserAction:       "Resubmit the Workshop mission link, then retry the failed operation.",
				RetryDisposition: presentation.RetryDisposition, BillingImpact: presentation.BillingImpact,
				SupportReference: presentation.SupportReference, OccurredAt: session.Failure.FailedAt,
			}
		}
		return FailureProjection{
			Present: true, Summary: presentation.WhatHappened, Reason: presentation.LikelyReason,
			PlatformAction: presentation.PlatformAction, UserAction: presentation.UserAction,
			RetryDisposition: presentation.RetryDisposition, BillingImpact: presentation.BillingImpact,
			SupportReference: presentation.SupportReference, OccurredAt: session.Failure.FailedAt,
		}
	}
	if session.LifecycleState == domain.StateFailed || (workflow != nil && workflow.Status == domain.WorkflowFailed) {
		return FailureProjection{
			Present: true, Summary: "The current operation stopped before completion.",
			Reason:           "The persisted failure predates the actionable error catalog.",
			PlatformAction:   "The platform preserved the last authoritative state.",
			UserAction:       "Use `/rb status` and give its support reference to an operator before repeating the command.",
			RetryDisposition: "No retry is scheduled.", BillingImpact: legacyBillingImpact(session),
		}
	}
	return FailureProjection{}
}

func hasLegacyWorkshopMissionSource(session domain.Session) bool {
	for _, source := range session.WorkshopMissionSources {
		if len(source.AcceptedItemIDs) > 0 && len(source.AcceptedItems) == 0 {
			return true
		}
	}
	return false
}

func legacyBillingImpact(session domain.Session) string {
	if session.Infrastructure.Empty() {
		return "No billable game-server resources are recorded for this session."
	}
	return "Resources remain and may continue to incur cost."
}

func artifactView(status domain.ArtifactStatus, objectKey, issue string, notRequired bool) ArtifactView {
	label := "Awaiting upload"
	switch status {
	case domain.ArtifactPending:
		label = "Queued for validation"
	case domain.ArtifactAccepted:
		label = "Accepted"
	case domain.ArtifactRejected:
		label = "Rejected"
	default:
		if strings.TrimSpace(objectKey) != "" {
			label = "Accepted"
		} else if notRequired {
			label = "Not required for vanilla"
		}
	}
	return ArtifactView{Status: label, Issue: strings.TrimSpace(issue)}
}

func missionArtifactView(session domain.Session) ArtifactView {
	view := artifactView(session.MissionArtifactStatus, session.MissionObjectKey, session.MissionArtifactIssue, false)
	if session.WorkshopResolutionTarget == domain.WorkshopTargetMission {
		view.Status = "Resolving Workshop source metadata"
		return view
	}
	if session.WorkshopResolutionLastTarget == domain.WorkshopTargetMission && session.WorkshopResolutionIssue != "" {
		view.Status = "Workshop source needs attention"
		view.Issue = session.WorkshopResolutionIssue
		return view
	}
	if len(session.WorkshopMissionSources) > 0 {
		pendingMissions := len(session.PendingWorkshopMissionItemIDs()) > 0
		switch {
		case session.Progress.WorkflowType == domain.WorkshopContentSyncWorkflowType && session.Progress.State == domain.ProgressActive:
			view.Status = "Workshop scenarios downloading and validating"
		case session.Progress.WorkflowType == domain.WorkshopContentSyncWorkflowType && session.Progress.State == domain.ProgressCompletedState:
			view.Status = "Workshop scenarios available"
		case session.Progress.WorkflowType == domain.WorkshopContentSyncWorkflowType && session.Progress.State == domain.ProgressActionRequired:
			view.Status = "Workshop scenario sync failed"
		case pendingMissions && (session.LifecycleState == domain.StateDraft || session.LifecycleState == domain.StateNew):
			view.Status = "Workshop scenarios queued for initial start"
		case pendingMissions && (session.LifecycleState == domain.StateSleeping || session.LifecycleState == domain.StateWarning1 || session.LifecycleState == domain.StateWarning2 || session.LifecycleState == domain.StateFailed):
			view.Status = "Workshop scenarios queued for next wake or start"
		case !pendingMissions:
			view.Status = "Workshop scenarios available"
		default:
			view.Status = "Workshop source accepted"
		}
		latest := session.WorkshopMissionSources[len(session.WorkshopMissionSources)-1]
		if summary := domain.WorkshopExclusionSummary(latest.ExcludedItems, 8); summary != "" {
			view.Issue = "Excluded: " + summary
		}
	}
	return view
}

func modStatus(session domain.Session) string {
	if session.Vanilla {
		return "Not required for vanilla"
	}
	if len(session.CreatorDLCs) > 0 && session.PresetArtifactStatus == "" && session.PresetObjectKey == "" {
		return "Creator DLC only"
	}
	if artifactAcceptedServerPreset(session) && session.PresetArtifactStatus == "" && session.PresetObjectKey == "" {
		return "No client mods"
	}
	return artifactView(session.PresetArtifactStatus, session.PresetObjectKey, session.PresetArtifactIssue, false).Status
}

func artifactAcceptedServerPreset(session domain.Session) bool {
	return session.ServerPresetArtifactStatus == domain.ArtifactAccepted || (session.ServerPresetArtifactStatus == "" && strings.TrimSpace(session.ServerPresetObjectKey) != "")
}

func modProjection(session domain.Session, modlistURL string) ModsProjection {
	projection := ModsProjection{Required: !session.Vanilla, Status: modStatus(session), DownloadURL: normalizeModlistURL(modlistURL), CreatorDLCs: append([]string(nil), session.CreatorDLCs...)}
	if session.Vanilla {
		return projection
	}
	if session.WorkshopResolutionTarget == domain.WorkshopTargetMods {
		projection.Status = "Resolving Workshop source metadata"
		return projection
	}
	if len(session.WorkshopModSources) > 0 {
		latest := session.WorkshopModSources[len(session.WorkshopModSources)-1]
		if summary := domain.WorkshopExclusionSummary(latest.ExcludedItems, 8); summary != "" {
			projection.Issue = "Excluded: " + summary
		}
	}
	if session.WorkshopResolutionLastTarget == domain.WorkshopTargetMods && session.WorkshopResolutionIssue != "" {
		projection.Status = "Workshop source needs attention"
		projection.Issue = session.WorkshopResolutionIssue
	}
	active := session.EffectiveActivePresetRevision()
	if !active.Empty() {
		projection.ActiveRevision = active.Number
		projection.ActiveSince = active.ActivatedAt.UTC()
		projection.ActiveWorkshopSourceID = active.WorkshopSourceID
	}
	pending := session.PendingPresetRevision
	if pending.Empty() {
		return projection
	}
	projection.PendingRevision = pending.Number
	projection.PendingWorkshopSourceID = pending.WorkshopSourceID
	switch pending.Status {
	case domain.PresetRevisionPending:
		if session.Progress.WorkflowType == domain.WorkshopContentSyncWorkflowType && session.Progress.State == domain.ProgressActive {
			projection.Status, projection.PendingStatus = "Downloading and validating pending revision", "Downloading"
		} else if session.Progress.WorkflowType == domain.WorkshopContentSyncWorkflowType && session.Progress.State == domain.ProgressCompletedState {
			projection.Status, projection.PendingStatus = "Revision downloaded; awaiting restart", "Awaiting restart"
		} else {
			projection.Status, projection.PendingStatus = "Revision queued for next start", "Queued"
		}
		projection.PendingSince = pending.StagedAt.UTC()
	case domain.PresetRevisionApplying:
		projection.Status = "Applying pending revision"
		projection.PendingStatus = "Applying"
		projection.PendingSince = pending.ApplyStartedAt.UTC()
	case domain.PresetRevisionFailed:
		projection.Status = "Pending revision failed; active revision retained"
		projection.PendingStatus = "Failed"
		projection.PendingSince = pending.FailedAt.UTC()
	}
	return projection
}

func endpointVisibility(state domain.LifecycleState) (visible bool, offline bool) {
	switch state {
	case domain.StateReady, domain.StateRunning, domain.StateIdle:
		return true, false
	case domain.StateStopping, domain.StateSleeping, domain.StateWarning1, domain.StateWarning2,
		domain.StateArchiving, domain.StateDestroying, domain.StateArchived:
		return true, true
	default:
		return false, false
	}
}

func preferredEndpoint(dns, publicIPv4 string) (host string, addressType string) {
	if host = normalizeDNSHost(dns); host != "" {
		return host, "DNS"
	}
	if host = normalizePublicIPv4(publicIPv4); host != "" {
		return host, "IP"
	}
	return "", ""
}

func normalizeDNSHost(value string) string {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if len(host) == 0 || len(host) > 253 || !strings.Contains(host, ".") {
		return ""
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return ""
			}
		}
	}
	return host
}

func normalizePublicIPv4(value string) string {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || !address.Is4() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return ""
	}
	return address.String()
}

func gameLabel(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "arma3") || strings.TrimSpace(value) == "" {
		return "Arma 3"
	}
	return humanize(value)
}

func modeLabel(vanilla bool) string {
	if vanilla {
		return "Vanilla"
	}
	return "Modded"
}

func humanize(value string) string {
	value = strings.NewReplacer("_", " ", "-", " ").Replace(strings.TrimSpace(value))
	var expanded strings.Builder
	var previous rune
	for _, character := range value {
		if unicode.IsUpper(character) && (unicode.IsLower(previous) || unicode.IsDigit(previous)) {
			expanded.WriteByte(' ')
		}
		expanded.WriteRune(character)
		previous = character
	}
	words := strings.Fields(expanded.String())
	for index := range words {
		word := []rune(strings.ToLower(words[index]))
		if index == 0 && len(word) > 0 {
			word[0] = []rune(strings.ToUpper(string(word[0])))[0]
		}
		words[index] = string(word)
	}
	return strings.Join(words, " ")
}
