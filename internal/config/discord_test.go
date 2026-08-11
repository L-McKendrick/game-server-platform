package config

import (
	"reflect"
	"testing"
	"time"
)

func TestLoadDiscord(t *testing.T) {
	t.Setenv("DISCORD_PUBLIC_KEY", "public-key")
	t.Setenv("DISCORD_APPLICATION_ID", "application-1")
	t.Setenv("DISCORD_ALLOWED_GUILD_IDS", "guild-1, guild-2, guild-1")
	t.Setenv("DISCORD_ALLOWED_ROLE_IDS", "role-1")
	t.Setenv("DISCORD_ALLOWED_CHANNEL_IDS", "channel-1")
	t.Setenv("DISCORD_LISTEN_ADDRESS", "127.0.0.1:9090")
	t.Setenv("DISCORD_MAX_REQUEST_BYTES", "32768")
	t.Setenv("DISCORD_SIGNATURE_MAX_AGE_SECONDS", "120")

	config, err := LoadDiscord()
	if err != nil {
		t.Fatalf("LoadDiscord() returned error: %v", err)
	}

	if config.PublicKeyHex != "public-key" {
		t.Errorf("PublicKeyHex = %q; want public-key", config.PublicKeyHex)
	}
	if config.ApplicationID != "application-1" {
		t.Errorf("ApplicationID = %q; want application-1", config.ApplicationID)
	}
	if !reflect.DeepEqual(config.AllowedGuildIDs, []string{"guild-1", "guild-2"}) {
		t.Errorf("AllowedGuildIDs = %#v; want guild-1 and guild-2", config.AllowedGuildIDs)
	}
	if config.ListenAddress != "127.0.0.1:9090" {
		t.Errorf("ListenAddress = %q; want 127.0.0.1:9090", config.ListenAddress)
	}
	if config.MaxRequestBytes != 32768 {
		t.Errorf("MaxRequestBytes = %d; want 32768", config.MaxRequestBytes)
	}
	if config.SignatureMaxAge != 2*time.Minute {
		t.Errorf("SignatureMaxAge = %s; want 2m", config.SignatureMaxAge)
	}
}

func TestLoadDiscordRequiresGuilds(t *testing.T) {
	t.Setenv("DISCORD_PUBLIC_KEY", "public-key")
	t.Setenv("DISCORD_APPLICATION_ID", "application-1")
	t.Setenv("DISCORD_ALLOWED_GUILD_IDS", "")
	t.Setenv("DISCORD_ALLOWED_ROLE_IDS", "role-1")
	t.Setenv("DISCORD_ALLOWED_CHANNEL_IDS", "channel-1")

	_, err := LoadDiscord()
	if err == nil {
		t.Fatal("LoadDiscord() returned nil error; want guild validation error")
	}
}

func TestLoadDiscordDoesNotRequirePreconfiguredAccessIDs(t *testing.T) {
	t.Setenv("DISCORD_PUBLIC_KEY", "public-key")
	t.Setenv("DISCORD_APPLICATION_ID", "application-1")
	t.Setenv("DISCORD_ALLOWED_GUILD_IDS", "guild-1")
	t.Setenv("DISCORD_ALLOWED_ROLE_IDS", "")
	t.Setenv("DISCORD_ALLOWED_CHANNEL_IDS", "")

	config, err := LoadDiscord()
	if err != nil {
		t.Fatalf("LoadDiscord() returned error: %v", err)
	}
	if len(config.AllowedRoleIDs) != 0 || len(config.AllowedChannelIDs) != 0 {
		t.Fatalf("fallback access IDs = %#v/%#v; want empty", config.AllowedRoleIDs, config.AllowedChannelIDs)
	}
}

func TestLoadDiscordRejectsInvalidListenAddress(t *testing.T) {
	t.Setenv("DISCORD_PUBLIC_KEY", "public-key")
	t.Setenv("DISCORD_APPLICATION_ID", "application-1")
	t.Setenv("DISCORD_ALLOWED_GUILD_IDS", "guild-1")
	t.Setenv("DISCORD_ALLOWED_ROLE_IDS", "role-1")
	t.Setenv("DISCORD_ALLOWED_CHANNEL_IDS", "channel-1")
	t.Setenv("DISCORD_LISTEN_ADDRESS", "localhost")

	_, err := LoadDiscord()
	if err == nil {
		t.Fatal("LoadDiscord() returned nil error; want listen-address validation error")
	}
}
