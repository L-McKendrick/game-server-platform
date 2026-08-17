package sessioncard

import (
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
		Infrastructure:          domain.Infrastructure{PublicIPv4: "203.0.113.10", LastObservedAt: infrastructureObserved},
		UpdatedAt:               started.Add(15 * time.Minute),
	}
	workflow := &domain.Workflow{
		ID: "internal-workflow-id", SessionID: session.ID, Type: domain.BootstrapWorkflowType,
		Status: domain.WorkflowRunning, CurrentStage: "HealthVerification", StartedAt: started,
	}
	players := &domain.PlayerStatus{PlayerCount: 2, MaxPlayers: 32, PlayerNames: []string{"Alice", "Bob"}}

	projection := Project(session, Options{
		Now: now, Workflow: workflow, Players: players, PlayersObservedAt: playersObserved,
		GameDNS: "arma.example.test", TeamSpeakDNS: "voice.example.test",
		ActiveModRevision: "mods-v2", PendingModRevision: "mods-v3", ModlistURL: "https://discord.com/channels/guild-1/channel-1/modlist-message",
	})

	if projection.Revision != 12 || projection.Name != "Saturday Arma" || projection.Slug != "saturday-arma" ||
		projection.Description != "Weekly co-op" || projection.Game != "Arma 3" || projection.Mode != "Modded" || !projection.TeamSpeak {
		t.Fatalf("identity/configuration projection = %#v", projection)
	}
	if projection.Lifecycle != "Setting up" || projection.Health != "Starting" ||
		projection.CurrentOperation != "Setting up game and content" || projection.Stage != "Health verification" ||
		projection.OperationStartedAt != started || projection.Elapsed != 17*time.Minute+12*time.Second {
		t.Fatalf("lifecycle projection = %#v", projection)
	}
	if !projection.Players.Available || projection.Players.Count != 2 || projection.Players.Capacity != 32 ||
		len(projection.Players.Names) != 2 || projection.Players.ObservedAt != playersObserved {
		t.Fatalf("players projection = %#v", projection.Players)
	}
	players.PlayerNames[0] = "mutated"
	if projection.Players.Names[0] != "Alice" {
		t.Fatal("player names were not defensively copied")
	}
	if projection.Endpoints.Game.Available || projection.Endpoints.TeamSpeak.Available {
		t.Fatalf("pre-health endpoints must not be advertised: %#v", projection.Endpoints)
	}
	if !projection.Mods.Required || projection.Mods.Status != "Accepted" || projection.Mods.ActiveRevision != "mods-v2" ||
		projection.Mods.PendingRevision != "mods-v3" || projection.Mods.DownloadURL == "" {
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
		!strings.Contains(public, "Elapsed:** 17m 12s") || !strings.Contains(detailed, "Players observed <t:") {
		t.Fatalf("public=%q detailed=%q", public, detailed)
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
	if !projection.Failure.Present || !projection.Failure.ResourcesMayExist ||
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
