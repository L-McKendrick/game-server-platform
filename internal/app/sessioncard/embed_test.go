package sessioncard

import (
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestRenderPublicEmbedMatchesApprovedCardAndUsesLiveMission(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	session := domain.Session{
		DisplayName: "Saturday Operations", Description: "Weekly cooperative campaign.", GameType: "arma3",
		LifecycleState: domain.StateRunning, HealthStatus: domain.HealthHealthy, TeamSpeakEnabled: true,
		PresetArtifactStatus: domain.ArtifactAccepted,
		Infrastructure:       domain.Infrastructure{PublicIPv4: "203.0.113.20"},
		Progress:             domain.SessionProgress{Milestone: domain.ProgressCompleted, State: domain.ProgressCompletedState, LastProgressAt: started},
		UpdatedAt:            started,
	}
	projection := Project(session, Options{
		Now:               started.Add(42 * time.Minute),
		Players:           &domain.PlayerStatus{PlayerCount: 12, MaxPlayers: 40, MissionName: "Liberation RX", MapName: "Altis"},
		PlayersObservedAt: started.Add(42 * time.Minute), ModlistURL: "https://discord.com/channels/guild/channel/message",
	})
	embed := RenderPublicEmbed(projection)
	if err := embed.Validate(); err != nil {
		t.Fatalf("embed validation error = %v", err)
	}
	if embed.Title != "🟢 ONLINE · HEALTHY" || embed.Color != embedColorOnline || !strings.HasPrefix(embed.Description, "**ARMA 3 | Saturday Operations**") {
		t.Fatalf("embed heading = %#v", embed)
	}
	if len(embed.Fields) != 3 || embed.Fields[0].Name != "\u200b\nCURRENT MISSION" ||
		!strings.Contains(embed.Fields[0].Value, "```\nLiberation RX on Altis\n```\n12 of 40 players · session started <t:") {
		t.Fatalf("mission field = %#v", embed.Fields)
	}
	for _, field := range embed.Fields {
		if strings.TrimSpace(strings.TrimPrefix(field.Name, "\u200b")) == "PROGRESS" {
			t.Fatalf("running public card retained completed progress: %#v", embed.Fields)
		}
	}
	if embed.Fields[1].Name != "\u200b\nGame server" || !strings.Contains(embed.Fields[1].Value, "`203.0.113.20:2302`\n\n**Modlist:** [Saturday Operations]") {
		t.Fatalf("game connection field = %#v", embed.Fields[1])
	}
	if embed.Fields[2].Name != "\u200b\nTeamSpeak" || embed.Fields[2].Value != "`203.0.113.20:9987`" {
		t.Fatalf("TeamSpeak field = %#v", embed.Fields[2])
	}
	for _, removed := range []string{"Guidance", "Last updated", "View details", "Help"} {
		if strings.Contains(embed.Description, removed) || strings.Contains(embed.Fields[0].Value, removed) {
			t.Fatalf("embed contains removed card text %q: %#v", removed, embed)
		}
	}
}

func TestRenderPublicEmbedOmitsTeamSpeakAndUsesVanillaModlist(t *testing.T) {
	t.Parallel()
	session := domain.Session{
		DisplayName: "Vanilla Night", GameType: "arma3", Vanilla: true,
		LifecycleState: domain.StateArchived, HealthStatus: domain.HealthStopped,
		Infrastructure: domain.Infrastructure{PublicIPv4: "203.0.113.21"}, UpdatedAt: time.Now().UTC(),
	}
	embed := RenderPublicEmbed(Project(session, Options{Now: session.UpdatedAt}))
	if embed.Color != embedColorArchived || embed.Title != "ARMA 3 | Vanilla Night" {
		t.Fatalf("archived presentation = %#v", embed)
	}
	for _, field := range embed.Fields {
		if field.Name == "TeamSpeak" {
			t.Fatalf("disabled TeamSpeak field rendered: %#v", embed.Fields)
		}
		if field.Name == "MODLIST" && field.Value != "None" {
			t.Fatalf("vanilla modlist = %#v", field)
		}
	}
}

func TestRenderPublicEmbedOmitsMissionOutsideActiveStates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	for _, state := range []domain.LifecycleState{domain.StateDraft, domain.StateInstalling, domain.StateSleeping, domain.StateArchived, domain.StateDeleted, domain.StateFailed} {
		session := domain.Session{DisplayName: "Lifecycle", GameType: "arma3", LifecycleState: state, UpdatedAt: now}
		embed := RenderPublicEmbed(Project(session, Options{Now: now, Players: &domain.PlayerStatus{MissionName: "Altis"}}))
		for _, field := range embed.Fields {
			if strings.Contains(field.Name, "CURRENT MISSION") {
				t.Fatalf("state %s retained mission field: %#v", state, embed.Fields)
			}
		}
	}
}

func TestRenderPublicHidesCompletedSleepProgressButDetailedStatusRetainsIt(t *testing.T) {
	t.Parallel()
	finishedAt := time.Date(2026, 9, 7, 20, 0, 0, 0, time.UTC)
	session := domain.Session{
		DisplayName: "Sleeping Ops", GameType: "arma3", LifecycleState: domain.StateSleeping, HealthStatus: domain.HealthStopped,
		Progress: domain.SessionProgress{
			WorkflowID: "sleep-1", WorkflowType: domain.SleepWorkflowType,
			Milestone: domain.ProgressCompleted, State: domain.ProgressCompletedState,
			CompletedMilestones: []domain.ProgressMilestone{domain.ProgressAccepted, domain.ProgressInstanceStopped, domain.ProgressCompleted},
			StartedAt:           finishedAt.Add(-time.Minute), LastProgressAt: finishedAt,
		},
		UpdatedAt: finishedAt,
	}
	card := Project(session, Options{Now: finishedAt.Add(time.Minute)})
	embed := RenderPublicEmbed(card)
	for _, field := range embed.Fields {
		if strings.TrimSpace(strings.TrimPrefix(field.Name, "\u200b")) == "PROGRESS" {
			t.Fatalf("completed sleeping card retained progress: %#v", embed.Fields)
		}
	}
	public := RenderPublic(card)
	for _, unwanted := range []string{"**Progress:**", "**Current stage:** Completed", "**Progress state:** Completed", "**Started:**"} {
		if strings.Contains(public, unwanted) {
			t.Fatalf("completed sleeping fallback retained %q: %q", unwanted, public)
		}
	}
	if detailed := RenderDetailed(card); !strings.Contains(detailed, "### Progress") || !strings.Contains(detailed, "**Progress state:** Completed") {
		t.Fatalf("detailed sleeping status lost completed progress: %q", detailed)
	}
}

func TestRenderPublicEmbedReducesCompletedArchiveAndUsesActiveModlist(t *testing.T) {
	t.Parallel()
	archivedAt := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	session := domain.Session{
		DisplayName: "Saturday Ops", Description: "Weekly operation.", GameType: "arma3", LifecycleState: domain.StateArchived,
		PresetArtifactStatus:  domain.ArtifactAccepted,
		ActivePresetRevision:  domain.PresetRevision{Number: 2, Status: domain.PresetRevisionActive, Modlist: domain.PresetModlistMetadata{Filename: "saturday-ops-modlist.html"}},
		PendingPresetRevision: domain.PresetRevision{Number: 3, Status: domain.PresetRevisionPending, Modlist: domain.PresetModlistMetadata{Filename: "pending-modlist.html"}},
		Progress:              domain.SessionProgress{WorkflowType: domain.ArchiveWorkflowType, Milestone: domain.ProgressCompleted, State: domain.ProgressCompletedState, LastProgressAt: archivedAt}, UpdatedAt: archivedAt.Add(time.Second),
	}
	card := Project(session, Options{Now: archivedAt.Add(time.Hour), ModlistURL: "https://discord.com/channels/guild/channel/message"})
	embed := RenderPublicEmbed(card)
	wantTimestamp := timestamp(archivedAt)
	if embed.Title != "ARMA 3 | Saturday Ops" || embed.Color != embedColorArchived || embed.Description != "Weekly operation.\n\nArchived: "+wantTimestamp || len(embed.Fields) != 1 {
		t.Fatalf("archived card = %#v", embed)
	}
	if embed.Fields[0].Value != "[saturday-ops-modlist.html](https://discord.com/channels/guild/channel/message)" || strings.Contains(embed.Fields[0].Value, "pending") {
		t.Fatalf("archived modlist = %#v", embed.Fields[0])
	}
	public := RenderPublic(card)
	if strings.Contains(public, "Progress") || strings.Contains(public, "Mission") || !strings.Contains(public, "**Archived:** "+wantTimestamp) {
		t.Fatalf("archived fallback = %q", public)
	}
}

func TestRenderPublicEmbedArchivedLegacyAndRestoreFailureFallbacks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	legacy := RenderPublicEmbed(Project(domain.Session{DisplayName: "Legacy", GameType: "arma3", LifecycleState: domain.StateArchived, UpdatedAt: now}, Options{Now: now}))
	if strings.Contains(legacy.Description, "Archived:") || len(legacy.Fields) != 1 || legacy.Fields[0].Value != "Unavailable" {
		t.Fatalf("legacy archived card invented data: %#v", legacy)
	}
	failed := Projection{Name: "Restore failed", Game: "Arma 3", Lifecycle: "Archived", Failure: FailureProjection{Present: true, Summary: "Restore failed", UserAction: "Retry restore"}}
	failedEmbed := RenderPublicEmbed(failed)
	if failedEmbed.Color != embedColorError || len(failedEmbed.Fields) == 0 || failedEmbed.Fields[0].Name != "ACTION REQUIRED" {
		t.Fatalf("archived restore failure lost diagnostics: %#v", failedEmbed)
	}
}

func TestRenderPublicEmbedReducesCompletedTerminationToTombstone(t *testing.T) {
	t.Parallel()
	terminatedAt := time.Date(2026, 8, 24, 7, 30, 0, 0, time.UTC)
	session := domain.Session{
		DisplayName: "Test 19", Description: "description goes here", GameType: "arma3",
		LifecycleState: domain.StateDeleted, HealthStatus: domain.HealthStopped,
		Progress:  domain.SessionProgress{Milestone: domain.ProgressCompleted, State: domain.ProgressCompletedState, LastProgressAt: terminatedAt},
		UpdatedAt: terminatedAt.Add(time.Second),
	}
	embed := RenderPublicEmbed(Project(session, Options{Now: terminatedAt.Add(56 * time.Second)}))
	if err := embed.Validate(); err != nil {
		t.Fatalf("embed validation error = %v", err)
	}
	if embed.Title != "ARMA 3 | Test 19" || embed.Description != "description goes here\n\nTerminated: <t:1787556600:R>" || embed.Color != embedColorInactive || len(embed.Fields) != 0 {
		t.Fatalf("terminated tombstone = %#v", embed)
	}
}

func TestRenderPublicEmbedUsesTombstoneUpdateTimeForLegacyTermination(t *testing.T) {
	t.Parallel()
	terminatedAt := time.Date(2026, 8, 24, 7, 30, 0, 0, time.UTC)
	session := domain.Session{DisplayName: "Legacy", GameType: "arma3", LifecycleState: domain.StateDeleted, UpdatedAt: terminatedAt}
	embed := RenderPublicEmbed(Project(session, Options{Now: terminatedAt.Add(time.Minute)}))
	if embed.Description != "Terminated: <t:1787556600:R>" || len(embed.Fields) != 0 {
		t.Fatalf("legacy terminated tombstone = %#v", embed)
	}
}

func TestRenderPublicEmbedUsesSetupAndFailureColorsWithTextLabels(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	setup := RenderPublicEmbed(Project(domain.Session{
		DisplayName: "Setup", GameType: "arma3", LifecycleState: domain.StateInstalling,
		Progress:  domain.SessionProgress{WorkflowID: "workflow", WorkflowType: domain.BootstrapWorkflowType, Milestone: domain.ProgressModsApplied, State: domain.ProgressActive, StartedAt: now},
		UpdatedAt: now,
	}, Options{Now: now}))
	if setup.Color != embedColorSetup || !strings.Contains(setup.Title, "SETTING UP") {
		t.Fatalf("setup embed = %#v", setup)
	}
	if len(setup.Fields) < 1 || strings.TrimSpace(strings.TrimPrefix(setup.Fields[0].Name, "\u200b")) != "PROGRESS" {
		t.Fatalf("setup progress was hidden: %#v", setup.Fields)
	}
	failure := RenderPublicEmbed(Project(domain.Session{
		DisplayName: "Failed", GameType: "arma3", LifecycleState: domain.StateFailed, HealthStatus: domain.HealthUnhealthy,
		UpdatedAt: now,
	}, Options{Now: now}))
	if failure.Color != embedColorError || !strings.Contains(failure.Title, "ACTION REQUIRED") {
		t.Fatalf("failure embed = %#v", failure)
	}
}

func TestWithModlistLinkEmbedEnrichesOnlyTheGameServerField(t *testing.T) {
	t.Parallel()
	embed := &domain.NotificationEmbed{Title: "ARMA 3 | Session", Description: "ONLINE", Color: embedColorOnline, Fields: []domain.NotificationEmbedField{
		{Name: "Game server", Value: "`203.0.113.20:2302`\n\n**Modlist:** Accepted", Inline: true},
		{Name: "TeamSpeak", Value: "`203.0.113.20:9987`", Inline: true},
	}}
	linked := WithModlistLinkEmbed(embed, "saturday-ops-modlist.html", "https://discord.com/channels/guild/channel/message")
	if linked == embed || !strings.Contains(linked.Fields[0].Value, "\n\n**Modlist:** [saturday-ops-modlist.html](https://discord.com/channels/guild/channel/message)") || linked.Fields[1] != embed.Fields[1] {
		t.Fatalf("linked embed = %#v", linked)
	}
	if got := WithModlistLinkEmbed(embed, "Session", "https://example.test/unsafe"); got != embed {
		t.Fatalf("unsafe URL changed embed: %#v", got)
	}
}
