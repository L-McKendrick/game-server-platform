package domain

import (
	"strings"
	"testing"
	"time"
)

func TestSessionServerConfigSnapshotCanSelectCustomOrGenerated(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	config := GuildServerConfig{GuildID: "guild-1", Revision: 3, ObjectKey: "guilds/guild-1/server-config/revisions/000003-" + strings.Repeat("a", 64) + "/server.cfg", Filename: "server.cfg", SHA256: strings.Repeat("a", 64), SizeBytes: 20, UploadedBy: "admin-1", UpdatedAt: now}
	if err := session.SelectServerConfig(config); err != nil || session.ServerConfigRevision != 3 || session.ServerConfigObjectKey != config.ObjectKey {
		t.Fatalf("custom snapshot session=%#v err=%v", session, err)
	}
	if err := session.SelectGeneratedServerConfig(); err != nil || session.ServerConfigRevision != 0 || session.ServerConfigObjectKey != "" || session.ServerConfigSHA256 != "" {
		t.Fatalf("generated snapshot session=%#v err=%v", session, err)
	}
}

func TestServerConfigSnapshotRejectsCrossGuildAndMalformedKeys(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	for _, snapshot := range []struct {
		key    string
		digest string
	}{
		{key: "guilds/guild-2/server-config/revisions/000001-" + digest + "/server.cfg", digest: digest},
		{key: "guilds/guild-1/server-config/revisions/000001-" + digest + "/other.cfg", digest: digest},
		{key: "guilds/guild-1/server-config/revisions/000001-" + strings.Repeat("z", 64) + "/server.cfg", digest: strings.Repeat("z", 64)},
	} {
		if err := session.SelectServerConfigSnapshot(1, snapshot.key, snapshot.digest); err == nil {
			t.Fatalf("accepted unsafe snapshot key=%q digest=%q", snapshot.key, snapshot.digest)
		}
	}
}
