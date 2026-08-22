package domain

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// ArtifactKind identifies an input artifact accepted from Discord.
type ArtifactKind string

// ArtifactStatus is the durable user-facing validation projection. An empty
// value is backward-compatible and means validation has not produced a result.
type ArtifactStatus string

// ArtifactIngestPurpose distinguishes legacy draft setup from a safe
// post-creation preset revision. Empty remains the legacy setup value.
type ArtifactIngestPurpose string

const (
	ArtifactMission               ArtifactKind          = "MISSION"
	ArtifactPreset                ArtifactKind          = "PRESET"
	ArtifactPending               ArtifactStatus        = "PENDING"
	ArtifactAccepted              ArtifactStatus        = "ACCEPTED"
	ArtifactRejected              ArtifactStatus        = "REJECTED"
	ArtifactPurposePresetRevision ArtifactIngestPurpose = "PRESET_REVISION"
)

const maxArtifactFilenameBytes = 255

// NormalizeMissionFilename preserves conventional Arma mission names while
// replacing characters that are unsafe in object keys, host paths, or
// server.cfg string values. The .pbo extension is normalized for consistency.
func NormalizeMissionFilename(filename string) (string, error) {
	name := strings.TrimSpace(filename)
	switch {
	case name == "":
		return "", fmt.Errorf("attachment filename is required")
	case len([]byte(name)) > maxArtifactFilenameBytes:
		return "", fmt.Errorf("attachment filename exceeds %d bytes", maxArtifactFilenameBytes)
	case strings.ContainsAny(name, `/\\`):
		return "", fmt.Errorf("attachment filename must not contain a path")
	}

	extension := filepath.Ext(name)
	if !strings.EqualFold(extension, ".pbo") {
		return "", fmt.Errorf("mission attachment must use the .pbo extension")
	}

	stem := strings.TrimSuffix(name, extension)
	var normalized strings.Builder
	replaced := false
	for _, character := range stem {
		safe := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '-'
		if safe {
			normalized.WriteRune(character)
			replaced = false
			continue
		}
		if !replaced {
			normalized.WriteByte('_')
			replaced = true
		}
	}
	if strings.Trim(normalized.String(), "._-") == "" {
		return "mission.pbo", nil
	}
	return normalized.String() + ".pbo", nil
}

func (status ArtifactStatus) Valid() bool {
	return status == "" || status == ArtifactPending || status == ArtifactAccepted || status == ArtifactRejected
}

// ArtifactIngestRequest is the durable contract between Discord and the ingest worker.
type ArtifactIngestRequest struct {
	SchemaVersion                int                   `json:"schema_version"`
	SessionID                    string                `json:"session_id"`
	Kind                         ArtifactKind          `json:"kind"`
	AttachmentID                 string                `json:"attachment_id"`
	Filename                     string                `json:"filename"`
	ContentType                  string                `json:"content_type"`
	SizeBytes                    int64                 `json:"size_bytes"`
	SourceURL                    string                `json:"source_url"`
	ActorID                      string                `json:"actor_id"`
	GuildID                      string                `json:"guild_id"`
	ChannelID                    string                `json:"channel_id"`
	CorrelationID                string                `json:"correlation_id"`
	IdempotencyKey               string                `json:"idempotency_key"`
	RequestedAt                  time.Time             `json:"requested_at"`
	Purpose                      ArtifactIngestPurpose `json:"purpose,omitempty"`
	ExpectedActivePresetRevision int64                 `json:"expected_active_preset_revision,omitempty"`
}

// Validate verifies that a queued ingest request is bounded and Discord-hosted.
func (request ArtifactIngestRequest) Validate() error {
	parsed, err := url.Parse(strings.TrimSpace(request.SourceURL))
	if err != nil {
		return fmt.Errorf("parse attachment URL: %w", err)
	}

	host := strings.ToLower(parsed.Hostname())
	allowedHost := host == "cdn.discordapp.com" || host == "media.discordapp.net"
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(request.Filename)))
	var missionFilenameError error
	if request.Kind == ArtifactMission {
		_, missionFilenameError = NormalizeMissionFilename(request.Filename)
	}

	switch {
	case request.SchemaVersion != 1:
		return fmt.Errorf("unsupported artifact ingest schema version %d", request.SchemaVersion)
	case strings.TrimSpace(request.SessionID) == "":
		return fmt.Errorf("session ID is required")
	case request.Kind != ArtifactMission && request.Kind != ArtifactPreset:
		return fmt.Errorf("unsupported artifact kind %q", request.Kind)
	case strings.TrimSpace(request.AttachmentID) == "":
		return fmt.Errorf("attachment ID is required")
	case strings.TrimSpace(request.Filename) == "":
		return fmt.Errorf("attachment filename is required")
	case len([]byte(strings.TrimSpace(request.Filename))) > maxArtifactFilenameBytes:
		return fmt.Errorf("attachment filename exceeds %d bytes", maxArtifactFilenameBytes)
	case strings.ContainsAny(request.Filename, `/\\`):
		return fmt.Errorf("attachment filename must not contain a path")
	case request.Kind == ArtifactMission && missionFilenameError != nil:
		return missionFilenameError
	case request.Kind == ArtifactPreset && extension != ".html" && extension != ".htm":
		return fmt.Errorf("preset attachment must use the .html or .htm extension")
	case request.SizeBytes <= 0:
		return fmt.Errorf("attachment size must be positive")
	case request.Kind == ArtifactMission && request.SizeBytes > 100*1024*1024:
		return fmt.Errorf("mission attachment exceeds 100 MiB")
	case request.Kind == ArtifactPreset && request.SizeBytes > 10*1024*1024:
		return fmt.Errorf("preset attachment exceeds 10 MiB")
	case parsed.Scheme != "https" || !allowedHost:
		return fmt.Errorf("attachment URL must use an approved Discord CDN host")
	case strings.TrimSpace(request.ActorID) == "":
		return fmt.Errorf("actor ID is required")
	case strings.TrimSpace(request.GuildID) == "":
		return fmt.Errorf("guild ID is required")
	case strings.TrimSpace(request.ChannelID) == "":
		return fmt.Errorf("channel ID is required")
	case strings.TrimSpace(request.CorrelationID) == "":
		return fmt.Errorf("correlation ID is required")
	case strings.TrimSpace(request.IdempotencyKey) == "":
		return fmt.Errorf("idempotency key is required")
	case request.RequestedAt.IsZero():
		return fmt.Errorf("requested timestamp is required")
	case request.Purpose != "" && request.Purpose != ArtifactPurposePresetRevision:
		return fmt.Errorf("unsupported artifact ingest purpose %q", request.Purpose)
	case request.Purpose == ArtifactPurposePresetRevision && request.Kind != ArtifactPreset:
		return fmt.Errorf("preset revision ingestion requires a preset artifact")
	case request.Purpose == ArtifactPurposePresetRevision && request.ExpectedActivePresetRevision < 1:
		return fmt.Errorf("preset revision ingestion requires the expected active revision")
	case request.Purpose == "" && request.ExpectedActivePresetRevision != 0:
		return fmt.Errorf("expected active preset revision requires revision ingestion")
	default:
		return nil
	}
}

func (request ArtifactIngestRequest) IsPresetRevision() bool {
	return request.Purpose == ArtifactPurposePresetRevision
}
