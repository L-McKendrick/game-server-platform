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
		!strings.Contains(embed.Fields[0].Value, "Liberation RX on Altis\n12 of 40 players · session started <t:") {
		t.Fatalf("mission field = %#v", embed.Fields)
	}
	if embed.Fields[1].Name != "\u200b\nGame server" || !strings.Contains(embed.Fields[1].Value, "`203.0.113.20:2302`\n\n**Modlist:** [Saturday Operations]") {
		t.Fatalf("game connection field = %#v", embed.Fields[1])
	}
	if embed.Fields[2].Name != "TeamSpeak" || embed.Fields[2].Value != "`203.0.113.20:9987`" {
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
	if embed.Color != embedColorInactive || !strings.Contains(embed.Title, "ARCHIVED · OFFLINE") {
		t.Fatalf("archived presentation = %#v", embed)
	}
	for _, field := range embed.Fields {
		if field.Name == "TeamSpeak" {
			t.Fatalf("disabled TeamSpeak field rendered: %#v", embed.Fields)
		}
		if strings.TrimSpace(strings.TrimPrefix(field.Name, "\u200b")) == "Game server" && !strings.Contains(field.Value, "**Modlist:** None") {
			t.Fatalf("vanilla modlist = %#v", field)
		}
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
	linked := WithModlistLinkEmbed(embed, "Saturday Operations", "https://discord.com/channels/guild/channel/message")
	if linked == embed || !strings.Contains(linked.Fields[0].Value, "\n\n**Modlist:** [Saturday Operations](https://discord.com/channels/guild/channel/message)") || linked.Fields[1] != embed.Fields[1] {
		t.Fatalf("linked embed = %#v", linked)
	}
	if got := WithModlistLinkEmbed(embed, "Session", "https://example.test/unsafe"); got != embed {
		t.Fatalf("unsafe URL changed embed: %#v", got)
	}
}
