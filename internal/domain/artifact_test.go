package domain

import (
	"strings"
	"testing"
	"time"
)

func TestArtifactIngestRequestRejectsUnapprovedSourceAndPaths(t *testing.T) {
	t.Parallel()

	request := validArtifactRequest()
	request.SourceURL = "https://example.com/mission.pbo"
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "Discord CDN") {
		t.Fatalf("Validate() error = %v; want Discord CDN rejection", err)
	}

	request = validArtifactRequest()
	request.Filename = "../mission.pbo"
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("Validate() error = %v; want path rejection", err)
	}
}

func TestServerConfigIngestRequestIsGuildBoundAndSmall(t *testing.T) {
	t.Parallel()
	request := validArtifactRequest()
	request.SessionID = ""
	request.Kind = ArtifactServerConfig
	request.Filename = "server.cfg"
	request.SizeBytes = MaximumServerConfigBytes
	request.SourceURL = "https://cdn.discordapp.com/attachments/1/2/server.cfg"
	request.Purpose = ArtifactPurposeServerConfig
	if err := request.Validate(); err != nil {
		t.Fatalf("valid server config request: %v", err)
	}
	request.SizeBytes++
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("oversized request error = %v", err)
	}
	request.SizeBytes = 10
	request.Filename = "server.txt"
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), ".cfg") {
		t.Fatalf("wrong extension error = %v", err)
	}
}

func validArtifactRequest() ArtifactIngestRequest {
	return ArtifactIngestRequest{
		SchemaVersion: 1, SessionID: "session-1", Kind: ArtifactMission,
		AttachmentID: "attachment-1", Filename: "mission.pbo", SizeBytes: 1024,
		SourceURL: "https://cdn.discordapp.com/attachments/1/2/mission.pbo",
		ActorID:   "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
		CorrelationID: "correlation-1", IdempotencyKey: "discord:interaction-1",
		RequestedAt: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
	}
}
