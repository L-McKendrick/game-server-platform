package domain

import (
	"fmt"
	"strings"
	"time"
)

const MaximumServerConfigBytes int64 = 64 * 1024

const (
	ServerConfigModeParameter     = "server_config_mode"
	ServerConfigRevisionParameter = "server_config_revision"
	ServerConfigObjectParameter   = "server_config_object_key"
	ServerConfigSHAParameter      = "server_config_sha256"
	ServerConfigModeGenerated     = "generated"
	ServerConfigModeCustom        = "custom"
)

// GuildServerConfig is the active private Arma server.cfg metadata. Contents
// remain in S3 and must never be rendered into Discord or logs.
type GuildServerConfig struct {
	GuildID    string
	Revision   int64
	ObjectKey  string
	Filename   string
	SHA256     string
	SizeBytes  int64
	UploadedBy string
	UpdatedAt  time.Time
}

func (config GuildServerConfig) Active() bool { return strings.TrimSpace(config.ObjectKey) != "" }

func (config GuildServerConfig) Validate() error {
	switch {
	case strings.TrimSpace(config.GuildID) == "" || config.Revision < 0 || config.UpdatedAt.IsZero():
		return fmt.Errorf("guild server configuration identity is invalid")
	case !config.Active() && (config.Filename != "" || config.SHA256 != "" || config.SizeBytes != 0 || config.UploadedBy != ""):
		return fmt.Errorf("removed guild server configuration retains artifact metadata")
	case config.Active() && (config.Revision < 1 || strings.TrimSpace(config.Filename) == "" || len(config.SHA256) != 64 || config.SizeBytes <= 0 || config.SizeBytes > MaximumServerConfigBytes || strings.TrimSpace(config.UploadedBy) == ""):
		return fmt.Errorf("active guild server configuration metadata is invalid")
	default:
		return nil
	}
}

func (session *Session) SelectServerConfig(config GuildServerConfig) error {
	if config.GuildID != session.GuildID {
		return ErrForbidden
	}
	if err := config.Validate(); err != nil {
		return err
	}
	return session.SelectServerConfigSnapshot(config.Revision, config.ObjectKey, config.SHA256)
}

func (session *Session) SelectServerConfigSnapshot(revision int64, objectKey, sha256 string) error {
	session.ServerConfigRevision = revision
	session.ServerConfigObjectKey = strings.TrimSpace(objectKey)
	session.ServerConfigSHA256 = strings.TrimSpace(sha256)
	return session.Validate()
}

func (session *Session) SelectGeneratedServerConfig() error {
	session.ServerConfigRevision = 0
	session.ServerConfigObjectKey = ""
	session.ServerConfigSHA256 = ""
	return session.Validate()
}
