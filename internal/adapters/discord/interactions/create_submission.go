package interactions

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type createModalSubmission struct {
	name        string
	description string
	modded      bool
	teamSpeak   bool
	mission     *interactionAttachment
	preset      *interactionAttachment
}

func (handler *Handler) submitCreateModal(
	ctx context.Context,
	payload interactionPayload,
	actor domain.Actor,
	correlationID string,
) (string, error) {
	keyPrefix := "discord:" + strings.TrimSpace(payload.ID) + ":create-modal"
	now := handler.clock.Now().UTC()
	submission, err := parseCreateModalSubmission(payload, actor, correlationID, keyPrefix, now)
	if err != nil {
		return "", err
	}

	session, err := handler.service.Create(ctx, appsession.CreateCommand{
		Actor:          actor,
		CorrelationID:  correlationID,
		IdempotencyKey: keyPrefix + ":create",
		DisplayName:    submission.name,
		Description:    submission.description,
		GameType:       "arma3",
		GuildID:        strings.TrimSpace(payload.GuildID),
		ChannelID:      strings.TrimSpace(payload.ChannelID),
	})
	if err != nil {
		return "", fmt.Errorf("create modal draft: %w", err)
	}

	session, err = handler.service.Configure(ctx, appsession.ConfigureCommand{
		Actor:               actor,
		SessionID:           session.ID,
		GuildID:             strings.TrimSpace(payload.GuildID),
		CorrelationID:       correlationID,
		IdempotencyKey:      keyPrefix + ":configure",
		GameProfileID:       defaultGameProfileID,
		SleepAfterSeconds:   defaultSleepMinutes * 60,
		ArchiveAfterSeconds: defaultArchiveDays * 86400,
		TeamSpeakEnabled:    submission.teamSpeak,
		Vanilla:             !submission.modded,
	})
	if err != nil {
		return "", fmt.Errorf("configure modal draft: %w", err)
	}
	session, err = handler.service.PrepareCreationArtifacts(ctx, appsession.PrepareCreationArtifactsCommand{
		Actor: actor, SessionID: session.ID, GuildID: payload.GuildID,
		CorrelationID: correlationID, IdempotencyKey: keyPrefix + ":artifacts",
		HasPreset: submission.preset != nil,
	})
	if err != nil {
		return "", fmt.Errorf("prepare creation artifacts: %w", err)
	}
	if err := handler.service.RequestSessionCard(ctx, appsession.SessionCardCommand{
		Actor: actor, SessionID: session.ID, GuildID: payload.GuildID, ChannelID: payload.ChannelID,
		CorrelationID: correlationID, NotificationID: keyPrefix + ":card",
		Content: sessioncard.RenderSetup(session, handler.clock.Now().UTC()),
	}); err != nil {
		return "", fmt.Errorf("publish creation session card: %w", err)
	}

	missionRequest := createArtifactRequest(
		payload, actor, correlationID, session.ID, domain.ArtifactMission,
		*submission.mission, keyPrefix+":mission", now,
	)
	if err := handler.service.RequestArtifactIngest(ctx, actor, missionRequest); err != nil {
		return "", fmt.Errorf("queue creation mission: %w", err)
	}

	queued := "Mission queued for validation."
	if submission.preset != nil {
		presetRequest := createArtifactRequest(
			payload, actor, correlationID, session.ID, domain.ArtifactPreset,
			*submission.preset, keyPrefix+":preset", now,
		)
		if err := handler.service.RequestArtifactIngest(ctx, actor, presetRequest); err != nil {
			return "", fmt.Errorf("queue creation preset: %w", err)
		}
		queued += " Preset queued for validation."
	} else if submission.modded {
		queued += " No preset was provided; add one before this modded session can become ready."
	}

	mode := "Modded"
	if !submission.modded {
		mode = "Vanilla"
	}
	teamSpeak := "Off"
	if submission.teamSpeak {
		teamSpeak = "On"
	}
	return fmt.Sprintf(
		"**Draft session created**\nName: %s\nSlug: `%s`\nMode: %s\nTeamSpeak: %s\n%s\nUploads have not been validated yet.%s",
		sanitizeInline(session.DisplayName), sanitizeCode(session.Slug), mode, teamSpeak, queued,
		payload.channelCapabilities().plainTextNotice(),
	), nil
}

func parseCreateModalSubmission(
	payload interactionPayload,
	actor domain.Actor,
	correlationID string,
	keyPrefix string,
	requestedAt time.Time,
) (createModalSubmission, error) {
	if payload.Data == nil || payload.Data.CustomID != createModalCustomID || len(payload.Data.Components) != 5 {
		return createModalSubmission{}, newUserError("The creation form is malformed or expired. Run `/rb create` again.")
	}

	var submission createModalSubmission
	seen := make(map[string]bool, 5)
	for _, label := range payload.Data.Components {
		if label.Type != componentTypeLabel || label.Component == nil {
			return createModalSubmission{}, newUserError("The creation form is malformed or expired. Run `/rb create` again.")
		}
		component := label.Component
		if seen[component.CustomID] {
			return createModalSubmission{}, newUserError("The creation form contains a duplicate field. Run `/rb create` again.")
		}
		seen[component.CustomID] = true

		switch component.CustomID {
		case createNameCustomID:
			if component.Type != componentTypeTextInput {
				return createModalSubmission{}, newUserError("The session name field is invalid. Run `/rb create` again.")
			}
			submission.name = normalizeSingleLine(component.Value)
		case createDescriptionCustomID:
			if component.Type != componentTypeTextInput {
				return createModalSubmission{}, newUserError("The description field is invalid. Run `/rb create` again.")
			}
			description, err := domain.NormalizeSessionDescription(component.Value)
			if err != nil {
				return createModalSubmission{}, newUserError("The description must contain at most 64 characters.")
			}
			submission.description = description
		case createFeaturesCustomID:
			if component.Type != componentTypeCheckboxGroup || len(component.Values) > 2 {
				return createModalSubmission{}, newUserError("Choose only the supported mode and TeamSpeak options.")
			}
			featureSeen := map[string]bool{}
			for _, value := range component.Values {
				if featureSeen[value] {
					return createModalSubmission{}, newUserError("The mode and features selection is invalid.")
				}
				featureSeen[value] = true
				switch value {
				case createFeatureModded:
					submission.modded = true
				case createFeatureTeamSpeak:
					submission.teamSpeak = true
				default:
					return createModalSubmission{}, newUserError("Choose only the supported mode and TeamSpeak options.")
				}
			}
		case createMissionCustomID:
			attachment, err := resolveModalAttachment(payload.Data, component, true)
			if err != nil {
				return createModalSubmission{}, newUserError("A single mission .pbo upload is required.")
			}
			submission.mission = attachment
		case createPresetCustomID:
			attachment, err := resolveModalAttachment(payload.Data, component, false)
			if err != nil {
				return createModalSubmission{}, newUserError("Choose at most one Arma Launcher .html preset.")
			}
			submission.preset = attachment
		default:
			return createModalSubmission{}, newUserError("The creation form contains an unsupported field. Run `/rb create` again.")
		}
	}

	for _, customID := range []string{
		createNameCustomID, createDescriptionCustomID, createFeaturesCustomID,
		createMissionCustomID, createPresetCustomID,
	} {
		if !seen[customID] {
			return createModalSubmission{}, newUserError("The creation form is incomplete. Run `/rb create` again.")
		}
	}
	if count := utf8.RuneCountInString(submission.name); count < 1 || count > 100 {
		return createModalSubmission{}, newUserError("The session name must contain 1 to 100 characters.")
	}

	missionRequest := createArtifactRequest(
		payload, actor, correlationID, "pending-session", domain.ArtifactMission,
		*submission.mission, keyPrefix+":mission", requestedAt,
	)
	if err := missionRequest.Validate(); err != nil {
		return createModalSubmission{}, newUserError("The mission upload must be a .pbo file no larger than 100 MiB from Discord.")
	}
	if submission.preset != nil {
		presetRequest := createArtifactRequest(
			payload, actor, correlationID, "pending-session", domain.ArtifactPreset,
			*submission.preset, keyPrefix+":preset", requestedAt,
		)
		if err := presetRequest.Validate(); err != nil {
			return createModalSubmission{}, newUserError("The preset upload must be an .html or .htm file no larger than 10 MiB from Discord.")
		}
	}
	return submission, nil
}

func resolveModalAttachment(
	data *applicationCommandData,
	component *interactionComponent,
	required bool,
) (*interactionAttachment, error) {
	if component.Type != componentTypeFileUpload || len(component.Values) > 1 {
		return nil, fmt.Errorf("invalid file upload component")
	}
	if len(component.Values) == 0 {
		if required {
			return nil, fmt.Errorf("attachment is required")
		}
		return nil, nil
	}
	if data.Resolved == nil {
		return nil, fmt.Errorf("resolved attachments are required")
	}
	attachmentID := strings.TrimSpace(component.Values[0])
	attachment, found := data.Resolved.Attachments[attachmentID]
	if !found || strings.TrimSpace(attachment.ID) != attachmentID {
		return nil, fmt.Errorf("attachment was not resolved")
	}
	return &attachment, nil
}

func createArtifactRequest(
	payload interactionPayload,
	actor domain.Actor,
	correlationID string,
	sessionID string,
	kind domain.ArtifactKind,
	attachment interactionAttachment,
	idempotencyKey string,
	requestedAt time.Time,
) domain.ArtifactIngestRequest {
	return domain.ArtifactIngestRequest{
		SchemaVersion:  1,
		SessionID:      strings.TrimSpace(sessionID),
		Kind:           kind,
		AttachmentID:   strings.TrimSpace(attachment.ID),
		Filename:       strings.TrimSpace(attachment.Filename),
		ContentType:    strings.TrimSpace(attachment.ContentType),
		SizeBytes:      attachment.Size,
		SourceURL:      strings.TrimSpace(attachment.URL),
		ActorID:        actor.ID,
		GuildID:        strings.TrimSpace(payload.GuildID),
		ChannelID:      strings.TrimSpace(payload.ChannelID),
		CorrelationID:  strings.TrimSpace(correlationID),
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
		RequestedAt:    requestedAt.UTC(),
	}
}
