package sessioncard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestProjectMapsEveryAuthoritativeCardSectionWithoutInternalIDs(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	now := started.Add(17*time.Minute + 12*time.Second)
	infrastructureObserved := started.Add(10 * time.Minute)
	playersObserved := started.Add(16 * time.Minute)
	session := domain.Session{
		ID: "internal-session-id", Version: 12,
		DisplayName: "Saturday Arma", Slug: "saturday-arma", Description: "Weekly co-op",
		GameType: "arma3", Vanilla: false, TeamSpeakEnabled: true,
		LifecycleState: domain.StateInstalling, HealthStatus: domain.HealthStarting,
		MissionArtifactStatus: domain.ArtifactAccepted, PresetArtifactStatus: domain.ArtifactAccepted,
		ActiveWorkflowID: "internal-workflow-id", ActiveWorkflowType: domain.BootstrapWorkflowType,
		ActiveWorkflowStartedAt: started,
		Progress: domain.SessionProgress{
			WorkflowID: "internal-workflow-id", WorkflowType: domain.BootstrapWorkflowType,
			Milestone: domain.ProgressHealthVerification,
			CompletedMilestones: []domain.ProgressMilestone{
				domain.ProgressAccepted, domain.ProgressHostPrepared, domain.ProgressGameServerInstalled,
				domain.ProgressModsApplied, domain.ProgressConfigurationReady, domain.ProgressServiceStarted,
			},
			State: domain.ProgressActive, StartedAt: started, LastProgressAt: started.Add(15 * time.Minute),
		},
		Infrastructure:         domain.Infrastructure{PublicIPv4: "203.0.113.10", LastObservedAt: infrastructureObserved},
		PresetObjectKey:        "sessions/internal-session-id/input/presets/v2.html",
		PresetRevisionSequence: 3,
		ActivePresetRevision: domain.PresetRevision{
			Number: 2, PresetObjectKey: "sessions/internal-session-id/input/presets/v2.html", Status: domain.PresetRevisionActive,
			StagedAt: started.Add(-2 * time.Hour), ActivatedAt: started.Add(-time.Hour),
		},
		PendingPresetRevision: domain.PresetRevision{
			Number: 3, BaseRevision: 2, PresetObjectKey: "sessions/internal-session-id/input/presets/v3.html", Status: domain.PresetRevisionApplying,
			StagedAt: started.Add(-10 * time.Minute), ApplyWorkflowID: "internal-workflow-id", ApplyStartedAt: started,
		},
		UpdatedAt: started.Add(15 * time.Minute),
	}
	workflow := &domain.Workflow{
		ID: "internal-workflow-id", SessionID: session.ID, Type: domain.BootstrapWorkflowType,
		Status: domain.WorkflowRunning, CurrentStage: "HealthVerification", StartedAt: started,
	}
	players := &domain.PlayerStatus{PlayerCount: 2, MaxPlayers: 32, PlayerNames: []string{"Alice", "Bob"}, MissionName: "Liberation RX", MapName: "Altis"}

	projection := Project(session, Options{
		Now: now, Workflow: workflow, Players: players, PlayersObservedAt: playersObserved,
		GameDNS: "arma.example.test", TeamSpeakDNS: "voice.example.test",
		ModlistURL: "https://discord.com/channels/guild-1/channel-1/modlist-message",
	})

	if projection.Revision != 12 || projection.Name != "Saturday Arma" || projection.Slug != "saturday-arma" ||
		projection.Description != "Weekly co-op" || projection.Game != "Arma 3" || projection.Mode != "Modded" || !projection.TeamSpeak {
		t.Fatalf("identity/configuration projection = %#v", projection)
	}
	if projection.Lifecycle != "Setting up" || projection.Health != "Starting" ||
		projection.CurrentOperation != "Setting up game and content" || projection.Stage != "Verifying health" ||
		projection.OperationStartedAt != started || projection.Elapsed != 17*time.Minute+12*time.Second {
		t.Fatalf("lifecycle projection = %#v", projection)
	}
	if !projection.Progress.Visible || projection.Progress.Bar != "■■■■■■□□" || projection.Progress.Step != 7 || projection.Progress.Total != 8 || projection.Progress.Completed != 6 {
		t.Fatalf("progress projection = %#v", projection.Progress)
	}
	if !projection.Players.Available || projection.Players.Count != 2 || projection.Players.Capacity != 32 ||
		len(projection.Players.Names) != 2 || projection.Players.Mission != "Liberation RX" || projection.Players.Map != "Altis" || projection.Players.ObservedAt != playersObserved {
		t.Fatalf("players projection = %#v", projection.Players)
	}
	players.PlayerNames[0] = "mutated"
	if projection.Players.Names[0] != "Alice" {
		t.Fatal("player names were not defensively copied")
	}
	if projection.Endpoints.Game.Available || projection.Endpoints.TeamSpeak.Available {
		t.Fatalf("pre-health endpoints must not be advertised: %#v", projection.Endpoints)
	}
	if !projection.Mods.Required || projection.Mods.Status != "Applying pending revision" || projection.Mods.ActiveRevision != 2 ||
		projection.Mods.ActiveSince != started.Add(-time.Hour) || projection.Mods.PendingRevision != 3 || projection.Mods.PendingStatus != "Applying" ||
		projection.Mods.PendingSince != started || projection.Mods.DownloadURL == "" {
		t.Fatalf("mods projection = %#v", projection.Mods)
	}
	if projection.Failure.Present || projection.Freshness.SessionUpdatedAt != session.UpdatedAt ||
		projection.Freshness.InfrastructureObservedAt != infrastructureObserved || projection.Freshness.PlayersObservedAt != playersObserved {
		t.Fatalf("failure/freshness projection = failure %#v freshness %#v", projection.Failure, projection.Freshness)
	}

	public := RenderPublic(projection)
	detailed := RenderDetailed(projection)
	for _, internalID := range []string{session.ID, workflow.ID, session.Infrastructure.InstanceID} {
		if internalID != "" && (strings.Contains(public, internalID) || strings.Contains(detailed, internalID)) {
			t.Fatalf("rendered projection exposed internal ID %q", internalID)
		}
	}
	if strings.Contains(public, "Alice") || !strings.Contains(detailed, "Player names: Alice, Bob") ||
		!strings.Contains(public, "**Progress:** `■■■■■■□□` — Step 7/8") || !strings.Contains(public, "**Current stage:** Verifying health") ||
		!strings.Contains(public, "Started:** <t:") || strings.Contains(public, "**Guidance:**") || !strings.Contains(public, "Active mod revision:** `2` — active <t:") ||
		!strings.Contains(public, "Pending mod revision:** `3` — Applying <t:") || !strings.Contains(detailed, "Players observed <t:") {
		t.Fatalf("public=%q detailed=%q", public, detailed)
	}
	for _, forbidden := range []string{"Milestone", "ETA", "%"} {
		if strings.Contains(public, forbidden) || strings.Contains(detailed, forbidden) {
			t.Fatalf("rendered progress included forbidden label %q: public=%q detailed=%q", forbidden, public, detailed)
		}
	}
}

func TestProgressBarFillsCompletedCheckpointsOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC)
	session := domain.Session{
		DisplayName: "Vanilla wake", Slug: "vanilla-wake", GameType: "arma3",
		LifecycleState: domain.StateWaking, HealthStatus: domain.HealthStarting, UpdatedAt: now,
		Progress: domain.SessionProgress{
			WorkflowID: "wake-1", WorkflowType: domain.WakeWorkflowType,
			Milestone:           domain.ProgressServiceStarted,
			CompletedMilestones: []domain.ProgressMilestone{domain.ProgressAccepted, domain.ProgressComputeReady},
			SkippedMilestones:   []domain.ProgressMilestone{domain.ProgressModsApplied},
			State:               domain.ProgressActive, StartedAt: now, LastProgressAt: now,
		},
	}
	projection := Project(session, Options{Now: now.Add(-time.Minute)})
	if projection.Progress.Bar != "■■□□□□" || projection.Progress.Completed != 2 || projection.Elapsed != 0 {
		t.Fatalf("skipped/clock progress = %#v elapsed=%s", projection.Progress, projection.Elapsed)
	}
	content := RenderDetailed(projection)
	if !strings.Contains(content, "Step 4/6") || !strings.Contains(content, "**Elapsed:** 0s") || strings.Contains(content, "Milestone") {
		t.Fatalf("progress content = %q", content)
	}
}

func TestProjectShowsAcceptedWorkshopMissionSourceBeforeBootstrapDownload(t *testing.T) {
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	session := domain.Session{DisplayName: "Workshop", Slug: "workshop", GameType: "arma3", LifecycleState: domain.StateDraft, UpdatedAt: now,
		WorkshopMissionSources: []domain.WorkshopMissionSource{{Source: domain.WorkshopReference{PublishedFileID: 42, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=42"}, SourceKind: domain.WorkshopSourceItem, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{42}, ResolvedAt: now}}}
	projection := Project(session, Options{Now: now})
	if projection.Artifacts.Mission.Status != "Workshop scenarios queued for initial start" || projection.Artifacts.Mission.Issue != "" {
		t.Fatalf("mission projection = %#v", projection.Artifacts.Mission)
	}
}

func TestProjectShowsWorkshopMetadataResolutionBeforeSourceExists(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	session := domain.Session{DisplayName: "Test", Slug: "test", GameType: "arma3", LifecycleState: domain.StateRunning, UpdatedAt: now, WorkshopResolutionTarget: domain.WorkshopTargetMission, WorkshopResolutionRequestKey: "request-1", WorkshopResolutionRequestedAt: now}
	projection := Project(session, Options{Now: now})
	if projection.Artifacts.Mission.Status != "Resolving Workshop source metadata" {
		t.Fatalf("mission status = %q", projection.Artifacts.Mission.Status)
	}
	session.WorkshopResolutionTarget = domain.WorkshopTargetMods
	projection = Project(session, Options{Now: now})
	if projection.Mods.Status != "Resolving Workshop source metadata" {
		t.Fatalf("mod status = %q", projection.Mods.Status)
	}
}

func TestProjectShowsLiveWorkshopSyncAndExcludedChildren(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	session := domain.Session{ID: "session-1", DisplayName: "Test", Slug: "test", GameType: "arma3", LifecycleState: domain.StateRunning, UpdatedAt: now, ActiveWorkflowID: "wsync-1", ActiveWorkflowType: domain.WorkshopContentSyncWorkflowType, Progress: domain.SessionProgress{WorkflowID: "wsync-1", WorkflowType: domain.WorkshopContentSyncWorkflowType, Milestone: domain.ProgressAccepted, State: domain.ProgressActive, StartedAt: now, LastProgressAt: now}, WorkshopMissionSources: []domain.WorkshopMissionSource{{ExcludedItems: []domain.WorkshopResolutionItem{{PublishedFileID: 42, Class: domain.WorkshopItemClientMod}}}}}
	projection := Project(session, Options{Now: now})
	if projection.CurrentOperation != "Synchronizing Workshop content" || projection.Stage != "Downloading and validating" || projection.Artifacts.Mission.Status != "Workshop scenarios downloading and validating" || !strings.Contains(projection.Artifacts.Mission.Issue, "42 (client_mod)") {
		t.Fatalf("projection = %#v", projection)
	}
}

func TestBootstrapActivityAndInactivityDeadlinesRenderFromAuthoritativeState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	installing := domain.Session{
		DisplayName: "Install", Slug: "install", GameType: "arma3", LifecycleState: domain.StateInstalling, UpdatedAt: now,
		Progress: domain.SessionProgress{
			WorkflowID: "bootstrap-1", WorkflowType: domain.BootstrapWorkflowType,
			Milestone: domain.ProgressGameServerInstalled, Activity: "Arma 3 server files",
			State: domain.ProgressActive, StartedAt: now.Add(-time.Minute), LastProgressAt: now,
		},
	}
	card := Project(installing, Options{Now: now})
	if card.Stage != "Downloading and installing game files" || card.Progress.Activity != "Arma 3 server files" ||
		!strings.Contains(RenderPublicEmbed(card).Fields[1].Value, "**Current download:** Arma 3 server files") {
		t.Fatalf("installing projection = %#v embed = %#v", card, RenderPublicEmbed(card))
	}

	idle := domain.Session{
		DisplayName: "Idle", Slug: "idle", GameType: "arma3", LifecycleState: domain.StateRunning, UpdatedAt: now,
		PlayerCountKnown: true, PlayerCount: 0, IdleSince: now.Add(-10 * time.Minute),
	}
	idleContent := RenderDetailed(Project(idle, Options{Now: now}))
	wantSleep := idle.IdleSince.Add(domain.AutomaticSleepAfter).Unix()
	if !strings.Contains(idleContent, fmt.Sprintf("**Automatic sleep:** <t:%d:F> (<t:%d:R>)", wantSleep, wantSleep)) {
		t.Fatalf("idle status = %q", idleContent)
	}

	sleeping := idle
	sleeping.LifecycleState = domain.StateSleeping
	sleeping.SleepingSince = now.Add(-time.Hour)
	sleeping.IdleSince = time.Time{}
	wantArchive := sleeping.SleepingSince.Add(domain.AutomaticArchiveAfter).Unix()
	sleepingContent := RenderDetailed(Project(sleeping, Options{Now: now}))
	if !strings.Contains(sleepingContent, fmt.Sprintf("**Automatic archive:** <t:%d:F> (<t:%d:R>)", wantArchive, wantArchive)) {
		t.Fatalf("sleeping status = %q", sleepingContent)
	}
}

func TestInactivityDeadlinesOmitUnknownInterruptedAndAnomalousEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		session domain.Session
	}{
		{name: "unknown players", session: domain.Session{LifecycleState: domain.StateRunning, IdleSince: now.Add(-time.Minute)}},
		{name: "active players", session: domain.Session{LifecycleState: domain.StateRunning, PlayerCountKnown: true, PlayerCount: 2, IdleSince: now.Add(-time.Minute)}},
		{name: "idle evidence interrupted", session: domain.Session{LifecycleState: domain.StateRunning, PlayerCountKnown: true, PlayerCount: 0}},
		{name: "future idle clock", session: domain.Session{LifecycleState: domain.StateRunning, PlayerCountKnown: true, PlayerCount: 0, IdleSince: now.Add(time.Minute)}},
		{name: "sleep timestamp in wrong state", session: domain.Session{LifecycleState: domain.StateArchived, SleepingSince: now.Add(-time.Hour)}},
		{name: "future sleep clock", session: domain.Session{LifecycleState: domain.StateSleeping, SleepingSince: now.Add(time.Minute)}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.session.DisplayName, test.session.Slug, test.session.GameType, test.session.UpdatedAt = "Timing", "timing", "arma3", now
			card := Project(test.session, Options{Now: now})
			if card.LifecycleTiming.Label != "" || strings.Contains(RenderDetailed(card), "**Automatic sleep:**") || strings.Contains(RenderDetailed(card), "**Automatic archive:**") {
				t.Fatalf("unexpected timing = %#v content=%q", card.LifecycleTiming, RenderDetailed(card))
			}
		})
	}
}

func TestProgressConditionAndGuidanceCoverOperationalStates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 6, 30, 0, 0, time.UTC)
	tests := []struct {
		name      string
		state     domain.ProgressState
		milestone domain.ProgressMilestone
		lease     time.Time
		want      string
	}{
		{name: "active", state: domain.ProgressActive, milestone: domain.ProgressGameServerInstalled, lease: now.Add(time.Hour), want: "Active"},
		{name: "waiting", state: domain.ProgressWaiting, milestone: domain.ProgressHostPrepared, lease: now.Add(time.Hour), want: "Waiting"},
		{name: "stalled", state: domain.ProgressActive, milestone: domain.ProgressHostPrepared, lease: now.Add(-time.Second), want: "Stalled"},
		{name: "retrying", state: domain.ProgressRetrying, milestone: domain.ProgressHostPrepared, lease: now.Add(time.Hour), want: "Retrying"},
		{name: "rollback", state: domain.ProgressRollingBack, milestone: domain.ProgressModsApplied, lease: now.Add(time.Hour), want: "Rollback"},
		{name: "completed", state: domain.ProgressCompletedState, milestone: domain.ProgressCompleted, want: "Completed"},
		{name: "action required", state: domain.ProgressActionRequired, milestone: domain.ProgressHostPrepared, want: "Action required"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			session := domain.Session{
				ID: "session-1", DisplayName: "Progress", Slug: "progress", GameType: "arma3",
				ActiveWorkflowID: "workflow-1", ActiveWorkflowType: domain.BootstrapWorkflowType,
				ActiveWorkflowLeaseExpiresAt: test.lease, LifecycleState: domain.StateInstalling,
				UpdatedAt: now,
				Progress: domain.SessionProgress{
					WorkflowID: "workflow-1", WorkflowType: domain.BootstrapWorkflowType,
					Milestone: test.milestone, State: test.state, StartedAt: now.Add(-time.Minute), LastProgressAt: now,
				},
			}
			projection := Project(session, Options{Now: now})
			if projection.Progress.Condition != test.want || projection.Progress.Guidance == "" {
				t.Fatalf("condition = %#v; want %q with guidance", projection.Progress, test.want)
			}
			public := RenderPublic(projection)
			detailed := RenderDetailed(projection)
			if !strings.Contains(public, "**Progress state:** "+test.want) || strings.Contains(public, "**Guidance:**") || !strings.Contains(detailed, "**Guidance:**") {
				t.Fatalf("public = %q detailed = %q", public, detailed)
			}
		})
	}
}

func TestTerminalProgressElapsedTimeDoesNotKeepGrowing(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 8, 18, 7, 0, 0, 0, time.UTC)
	finished := started.Add(12 * time.Minute)
	session := domain.Session{
		DisplayName: "Finished", Slug: "finished", GameType: "arma3", LifecycleState: domain.StateRunning, UpdatedAt: finished,
		Progress: domain.SessionProgress{
			WorkflowID: "wake-1", WorkflowType: domain.WakeWorkflowType,
			Milestone: domain.ProgressCompleted, State: domain.ProgressCompletedState,
			CompletedMilestones: []domain.ProgressMilestone{
				domain.ProgressAccepted, domain.ProgressComputeReady, domain.ProgressModsApplied,
				domain.ProgressServiceStarted, domain.ProgressHealthVerification, domain.ProgressCompleted,
			},
			StartedAt: started, LastProgressAt: finished,
		},
	}
	projection := Project(session, Options{Now: finished.Add(24 * time.Hour)})
	if projection.Elapsed != 12*time.Minute || !strings.Contains(RenderPublic(projection), "**Started:** <t:") {
		t.Fatalf("terminal elapsed = %s content=%q", projection.Elapsed, RenderPublic(projection))
	}
}

func TestProjectRepresentsGenericFailureAndOfflineEndpointsSafely(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	session := domain.Session{
		ID: "session-failed", DisplayName: "Failed", Slug: "failed", GameType: "arma3",
		LifecycleState: domain.StateFailed, HealthStatus: domain.HealthUnhealthy,
		Infrastructure: domain.Infrastructure{InstanceID: "i-secret", PublicIPv4: "203.0.113.20", LastObservedAt: now.Add(-time.Minute)},
		UpdatedAt:      now,
	}
	workflow := &domain.Workflow{
		SessionID: session.ID, Type: "ProvisionSession", Status: domain.WorkflowFailed,
		CurrentStage: "Failed", ErrorCode: "ERR_SECRET", ErrorMessage: "raw operator detail",
	}
	projection := Project(session, Options{Now: now, Workflow: workflow})
	if !projection.Failure.Present || !strings.Contains(projection.Failure.BillingImpact, "incur cost") ||
		projection.Failure.Summary == "" || projection.Endpoints.Game.Available {
		t.Fatalf("failed projection = %#v", projection)
	}
	content := RenderPublic(projection)
	for _, forbidden := range []string{"ERR_SECRET", "raw operator detail", "i-secret", session.ID} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("failure card exposed %q: %q", forbidden, content)
		}
	}

	session.LifecycleState = domain.StateArchived
	archived := Project(session, Options{Now: now})
	if !archived.Endpoints.Game.Offline {
		t.Fatalf("archived endpoint = %#v; want explicitly offline", archived.Endpoints.Game)
	}
	if !strings.Contains(RenderPublic(archived), "**Arma IP:** `203.0.113.20:2302` — Offline (retained address)") {
		t.Fatalf("archived card = %q", RenderPublic(archived))
	}
}

func TestActiveModlistDeliveryMustMatchActiveRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 1, 0, 0, 0, time.UTC)
	activeModlist := domain.PresetModlistMetadata{
		ObjectKey: "sessions/session-1/input/modlists/v2/session-1-modlist.html", Filename: "session-1-modlist.html",
		SHA256: strings.Repeat("b", 64), SizeBytes: 512, WorkshopCount: 3,
	}
	session := domain.Session{
		ID: "session-1", GuildID: "guild-1", ChannelID: "channel-1", Version: 8,
		PresetObjectKey:      "sessions/session-1/input/presets/v2.html",
		ActivePresetRevision: domain.PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/presets/v2.html", Modlist: activeModlist, Status: domain.PresetRevisionActive, StagedAt: now.Add(-time.Hour), ActivatedAt: now},
	}
	reference := domain.SessionModlistReference{
		SessionID: session.ID, ChannelID: session.ChannelID, MessageID: "message-1", ObjectKey: activeModlist.ObjectKey,
		Filename: activeModlist.Filename, DeliveredRevision: session.Version, DeliveredNotificationID: "modlist-v2", ContentSHA256: activeModlist.SHA256,
	}
	attachment := domain.NotificationAttachment{ObjectKey: activeModlist.ObjectKey, Filename: activeModlist.Filename, SHA256: activeModlist.SHA256, SizeBytes: activeModlist.SizeBytes, Revision: session.Version}
	if !IsActiveModlistReference(session, reference) || !IsActiveModlistAttachment(session, attachment) {
		t.Fatal("current active modlist was rejected")
	}
	reference.ObjectKey = "sessions/session-1/input/modlists/v1/session-1-modlist.html"
	attachment.ObjectKey = reference.ObjectKey
	if IsActiveModlistReference(session, reference) || IsActiveModlistAttachment(session, attachment) {
		t.Fatal("stale modlist remained downloadable after promotion")
	}
}

func TestUnknownPersistedFailureRendersSafeFallbackWithBillingAndReference(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 17, 0, 0, 0, time.UTC)
	failure, err := domain.NewFailureRecord(domain.FailureRecordInput{
		Code: "ERR_UNMAPPED_PROVIDER", Stage: "Provider call", RetryDisposition: domain.RetryNotScheduled,
		ResourceImpact: domain.ResourceCostUnknown,
		Detail:         "arn:aws:ssm:us-west-2:123456789012:parameter/secret token=hunter2 i-0123456789abcdef0",
		FailedAt:       now, SupportReference: "ref_ABC123",
	})
	if err != nil {
		t.Fatal(err)
	}
	session := domain.Session{ID: "session-secret", DisplayName: "Failed", Slug: "failed", GameType: "arma3", LifecycleState: domain.StateFailed, HealthStatus: domain.HealthUnhealthy, Failure: failure, UpdatedAt: now}
	players := &domain.PlayerStatus{PlayerCount: 100, MaxPlayers: 100, PlayerNames: make([]string, 100)}
	for index := range players.PlayerNames {
		players.PlayerNames[index] = strings.Repeat("LongPlayerName", 6)
	}
	content := RenderDetailed(Project(session, Options{Now: now, Players: players, PlayersObservedAt: now}))
	for _, required := range []string{"unexpected error", "No retry is scheduled", "may remain and incur cost", "ref_ABC123"} {
		if !strings.Contains(content, required) {
			t.Fatalf("content %q omitted %q", content, required)
		}
	}
	for _, forbidden := range []string{"ERR_UNMAPPED_PROVIDER", "arn:aws", "hunter2", "i-0123456789abcdef0", "session-secret"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("content exposed %q: %q", forbidden, content)
		}
	}
}

func TestLegacyWorkshopMissionBootstrapFailureRequestsResubmission(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	failure, err := domain.NewFailureRecord(domain.FailureRecordInput{
		Code: "ERR_BOOTSTRAP_COMMAND_FAILED", Stage: "Started", RetryDisposition: domain.RetryNotScheduled,
		ResourceImpact: domain.ResourceCostRetained, Detail: "setup stopped", FailedAt: now, SupportReference: "ref_legacy123",
	})
	if err != nil {
		t.Fatal(err)
	}
	session := domain.Session{LifecycleState: domain.StateFailed, Failure: failure, WorkshopMissionSources: []domain.WorkshopMissionSource{{
		Source:     domain.WorkshopReference{PublishedFileID: 10, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10"},
		SourceKind: domain.WorkshopSourceItem, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{10}, ResolvedAt: now,
	}}}
	projection := failureProjection(session, nil)
	if !strings.Contains(projection.UserAction, "Resubmit the Workshop mission link") || projection.SupportReference != "ref_legacy123" || !strings.Contains(projection.BillingImpact, "incur cost") {
		t.Fatalf("failure projection = %#v", projection)
	}
}

func TestProjectPrefersDNSAndFallsBackToPublicIPv4(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	session := domain.Session{
		DisplayName: "Saturday Arma", Slug: "saturday-arma", GameType: "arma3", TeamSpeakEnabled: true,
		LifecycleState: domain.StateRunning, HealthStatus: domain.HealthHealthy,
		Infrastructure: domain.Infrastructure{PublicIPv4: "203.0.113.10", LastObservedAt: now.Add(-time.Minute)},
		UpdatedAt:      now,
	}

	dns := Project(session, Options{Now: now, GameDNS: "ARMA.Example.Test.", TeamSpeakDNS: "voice.example.test"})
	if dns.Endpoints.Game.Host != "arma.example.test" || dns.Endpoints.Game.AddressType != "DNS" || dns.Endpoints.Game.Port != ArmaGamePort ||
		dns.Endpoints.TeamSpeak.Host != "voice.example.test" || dns.Endpoints.TeamSpeak.AddressType != "DNS" || dns.Endpoints.TeamSpeak.Port != TeamSpeakVoicePort {
		t.Fatalf("DNS endpoints = %#v", dns.Endpoints)
	}
	dnsContent := RenderPublic(dns)
	for _, expected := range []string{"**Arma DNS:** `arma.example.test:2302`", "**TeamSpeak DNS:** `voice.example.test:9987`"} {
		if !strings.Contains(dnsContent, expected) {
			t.Fatalf("DNS card = %q; missing %q", dnsContent, expected)
		}
	}

	fallback := Project(session, Options{Now: now, GameDNS: "not a hostname", TeamSpeakDNS: "-invalid.example"})
	if fallback.Endpoints.Game.Host != "203.0.113.10" || fallback.Endpoints.Game.AddressType != "IP" ||
		fallback.Endpoints.TeamSpeak.Host != "203.0.113.10" || fallback.Endpoints.TeamSpeak.AddressType != "IP" {
		t.Fatalf("fallback endpoints = %#v", fallback.Endpoints)
	}
	if content := RenderDetailed(fallback); !strings.Contains(content, "**Arma IP:** `203.0.113.10:2302`") ||
		!strings.Contains(content, "**TeamSpeak IP:** `203.0.113.10:9987`") {
		t.Fatalf("fallback card = %q", content)
	}

	session.TeamSpeakEnabled = false
	withoutTeamSpeak := Project(session, Options{Now: now, TeamSpeakDNS: "voice.example.test"})
	if withoutTeamSpeak.Endpoints.TeamSpeak.Available || strings.Contains(RenderPublic(withoutTeamSpeak), "TeamSpeak IP:") || strings.Contains(RenderPublic(withoutTeamSpeak), "TeamSpeak DNS:") {
		t.Fatalf("disabled TeamSpeak endpoint = %#v", withoutTeamSpeak.Endpoints.TeamSpeak)
	}
}

func TestProjectHidesPrivateAndUnhealthyEndpointsAndMarksSleepingOffline(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 8, 30, 0, 0, time.UTC)
	session := domain.Session{
		DisplayName: "Private", Slug: "private", GameType: "arma3", LifecycleState: domain.StateRunning,
		Infrastructure: domain.Infrastructure{PublicIPv4: "10.0.0.5", LastObservedAt: now}, UpdatedAt: now,
	}
	if endpoint := Project(session, Options{Now: now}).Endpoints.Game; endpoint.Available {
		t.Fatalf("private endpoint = %#v; want hidden", endpoint)
	}

	session.Infrastructure.PublicIPv4 = "203.0.113.25"
	session.LifecycleState = domain.StateWaking
	if endpoint := Project(session, Options{Now: now}).Endpoints.Game; endpoint.Available {
		t.Fatalf("pre-health wake endpoint = %#v; want hidden", endpoint)
	}

	session.LifecycleState = domain.StateSleeping
	offline := Project(session, Options{Now: now}).Endpoints.Game
	if !offline.Available || !offline.Offline || offline.AddressType != "IP" {
		t.Fatalf("sleeping endpoint = %#v; want retained offline IP", offline)
	}
	if content := RenderPublic(Project(session, Options{Now: now})); !strings.Contains(content, "Offline (retained address)") {
		t.Fatalf("sleeping card = %q", content)
	}
}

func TestRenderProjectionIsMentionSafeAndUnicodeBounded(t *testing.T) {
	t.Parallel()
	projection := Projection{
		Name: "@everyone " + strings.Repeat("🎮", maximumContentRunes), Slug: "safe-slug",
		Game: "Arma 3", Mode: "Vanilla", Lifecycle: "Ready", Health: "Healthy", Stage: "Accepted",
		Artifacts: ArtifactProjection{Mission: ArtifactView{Status: "Accepted"}, Preset: ArtifactView{Status: "Not required for vanilla"}},
		Mods:      ModsProjection{Status: "Not required for vanilla"},
	}
	content := RenderPublic(projection)
	if len([]rune(content)) > maximumContentRunes || strings.Contains(content, "@everyone") || !strings.HasSuffix(content, "…") {
		t.Fatalf("bounded content = %q", content)
	}
}

func TestProjectCreatorDLCOnlySessionDoesNotClaimPresetIsMissing(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	session := domain.Session{DisplayName: "cDLC only", Slug: "cdlc-only", GameType: "arma3", CreatorDLCs: []string{domain.CreatorDLCWesternSahara}, LifecycleState: domain.StateNew, HealthStatus: domain.HealthUnknown, UpdatedAt: now}
	projection := Project(session, Options{Now: now})
	content := RenderDetailed(projection)
	if projection.Mods.Status != "Creator DLC only" || projection.Artifacts.Preset.Status != "Not needed for Creator DLC only" || strings.Contains(content, "Preset:** Missing") {
		t.Fatalf("projection=%#v content=%q", projection, content)
	}
}
