package sessioncard

import (
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

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
	Elapsed            time.Duration

	Players   PlayerProjection
	Endpoints EndpointProjection
	Mods      ModsProjection
	Failure   FailureProjection
	Freshness FreshnessProjection
	Artifacts ArtifactProjection
}

type PlayerProjection struct {
	Available  bool
	Count      int
	Capacity   int
	Names      []string
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
	Required        bool
	Status          string
	ActiveRevision  string
	PendingRevision string
	DownloadURL     string
}

type FailureProjection struct {
	Present           bool
	Summary           string
	Action            string
	ResourcesMayExist bool
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
	Now                time.Time
	Workflow           *domain.Workflow
	Players            *domain.PlayerStatus
	PlayersObservedAt  time.Time
	GameDNS            string
	TeamSpeakDNS       string
	ActiveModRevision  string
	PendingModRevision string
	ModlistURL         string
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
		Mods: ModsProjection{
			Required: !session.Vanilla, Status: modStatus(session),
			ActiveRevision:  strings.TrimSpace(options.ActiveModRevision),
			PendingRevision: strings.TrimSpace(options.PendingModRevision),
			DownloadURL:     normalizeModlistURL(options.ModlistURL),
		},
		Artifacts: ArtifactProjection{
			Mission: artifactView(session.MissionArtifactStatus, session.MissionObjectKey, session.MissionArtifactIssue, false),
			Preset:  artifactView(session.PresetArtifactStatus, session.PresetObjectKey, session.PresetArtifactIssue, session.Vanilla),
		},
		Freshness: FreshnessProjection{
			SessionUpdatedAt:         sessionUpdatedAt,
			InfrastructureObservedAt: session.Infrastructure.LastObservedAt.UTC(),
		},
	}
	if label := progressLabel(session.Progress.Milestone); label != "" {
		projection.Stage = label
	}

	if session.ActiveWorkflowStartedAt.IsZero() == false {
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

func progressLabel(milestone domain.ProgressMilestone) string {
	switch milestone {
	case domain.ProgressAccepted:
		return "Accepted"
	case domain.ProgressInfrastructureReady:
		return "Infrastructure ready"
	case domain.ProgressGameContentSetup:
		return "Game and content setup"
	case domain.ProgressHealthVerification:
		return "Health verification"
	case domain.ProgressCompleted:
		return "Completed"
	case domain.ProgressFailed:
		return "Failed"
	default:
		return ""
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
	default:
		return ""
	}
}

func failureProjection(session domain.Session, workflow *domain.Workflow) FailureProjection {
	resources := !session.Infrastructure.Empty()
	if session.MissionArtifactStatus == domain.ArtifactRejected || session.PresetArtifactStatus == domain.ArtifactRejected {
		return FailureProjection{
			Present: true, Summary: "One or more setup files were rejected.",
			Action: "Use `/rb setup` to replace the rejected file.", ResourcesMayExist: resources,
		}
	}
	if session.LifecycleState == domain.StateFailed || (workflow != nil && workflow.Status == domain.WorkflowFailed) {
		return FailureProjection{
			Present: true, Summary: "The current operation stopped before completion.",
			Action: "Use `/rb status` for the latest safe details.", ResourcesMayExist: resources,
		}
	}
	return FailureProjection{}
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

func modStatus(session domain.Session) string {
	if session.Vanilla {
		return "Not required for vanilla"
	}
	return artifactView(session.PresetArtifactStatus, session.PresetObjectKey, session.PresetArtifactIssue, false).Status
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
