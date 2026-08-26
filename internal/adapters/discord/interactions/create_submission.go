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
	gameType        string
	name            string
	description     string
	modded          bool
	teamSpeak       bool
	autoStart       bool
	mission         *interactionAttachment
	missionWorkshop string
	preset          *interactionAttachment
}

type createModalResult struct {
	content    string
	components []interactionComponent
}

func (handler *Handler) submitCreateModal(
	ctx context.Context,
	payload interactionPayload,
	actor domain.Actor,
	correlationID string,
) (createModalResult, error) {
	keyPrefix := "discord:" + strings.TrimSpace(payload.ID) + ":create-modal"
	now := handler.clock.Now().UTC()
	submission, err := parseCreateModalSubmission(payload, actor, correlationID, keyPrefix, now)
	if err != nil {
		return createModalResult{}, err
	}
	cardChannelID, err := handler.access.PublicCardChannel(ctx, payload.GuildID)
	if err != nil {
		return createModalResult{}, fmt.Errorf("read public card channel: %w", err)
	}
	if cardChannelID == "" {
		cardChannelID = strings.TrimSpace(payload.ChannelID)
	}

	session, err := handler.service.Create(ctx, appsession.CreateCommand{
		Actor:          actor,
		CorrelationID:  correlationID,
		IdempotencyKey: keyPrefix + ":create",
		DisplayName:    submission.name,
		Description:    submission.description,
		GameType:       submission.gameType,
		GuildID:        strings.TrimSpace(payload.GuildID),
		ChannelID:      cardChannelID,
	})
	if err != nil {
		return createModalResult{}, fmt.Errorf("create modal draft: %w", err)
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
		StartWhenReady:      submission.autoStart,
	})
	if err != nil {
		return createModalResult{}, fmt.Errorf("configure modal draft: %w", err)
	}
	session, err = handler.service.PrepareCreationArtifacts(ctx, appsession.PrepareCreationArtifactsCommand{
		Actor: actor, SessionID: session.ID, GuildID: payload.GuildID,
		CorrelationID: correlationID, IdempotencyKey: keyPrefix + ":artifacts",
		HasPreset: false, HasMission: submission.mission != nil, Roles: interactionRoles(payload),
	})
	if err != nil {
		return createModalResult{}, fmt.Errorf("prepare creation artifacts: %w", err)
	}
	cardProjection := sessioncard.Project(session, sessioncard.Options{Now: handler.clock.Now().UTC()})
	if err := handler.service.RequestSessionCard(ctx, appsession.SessionCardCommand{
		Actor: actor, SessionID: session.ID, GuildID: payload.GuildID, ChannelID: cardChannelID,
		CorrelationID: correlationID, NotificationID: keyPrefix + ":card",
		Content: sessioncard.RenderPublic(cardProjection), Embed: sessioncard.RenderPublicEmbed(cardProjection), CardRevision: session.Version,
	}); err != nil {
		return createModalResult{}, fmt.Errorf("publish creation session card: %w", err)
	}

	queued := "Default mission: `MP_ZGM_m12.Stratis`."
	if submission.mission != nil {
		missionRequest := createArtifactRequest(payload, actor, correlationID, session.ID, domain.ArtifactMission, *submission.mission, keyPrefix+":mission", now)
		missionRequest.ChannelID = session.ChannelID
		if err := handler.service.RequestArtifactIngest(ctx, actor, missionRequest); err != nil {
			return createModalResult{}, fmt.Errorf("queue creation mission: %w", err)
		}
		queued = "Mission queued for validation."
	} else if submission.missionWorkshop != "" {
		request := createWorkshopRequest(payload, actor, correlationID, session.ID, domain.WorkshopTargetMission, submission.missionWorkshop, keyPrefix+":mission-workshop", now)
		request.ChannelID = session.ChannelID
		if err := requestWorkshopResolve(ctx, handler.service, actor, request); err != nil {
			return createModalResult{}, fmt.Errorf("queue creation Workshop mission: %w", err)
		}
		queued = "Workshop mission link queued for metadata validation."
	}
	components := []interactionComponent(nil)
	if submission.modded {
		queued += " Continue to mod options to upload a preset and choose Creator DLC."
		customID, customErr := createModsContinueCustomID(session.ID, session.Version)
		if customErr != nil {
			return createModalResult{}, customErr
		}
		components = []interactionComponent{{Type: componentTypeActionRow, Components: []interactionComponent{{
			Type: componentTypeButton, Style: buttonStylePrimary, Label: "Continue to mod options", CustomID: customID,
		}}}}
	}

	mode := "Modded"
	if !submission.modded {
		mode = "Vanilla"
	}
	teamSpeak := "Off"
	if submission.teamSpeak {
		teamSpeak = "On"
	}
	setup := "Manual"
	if submission.autoStart {
		setup = "Automatic after required files validate"
	}
	return createModalResult{content: fmt.Sprintf(
		"**Draft session created**\nName: %s\nSlug: `%s`\nMode: %s\nTeamSpeak: %s\nServer setup: %s\n%s\nUploads have not been validated yet.%s\n\nNext: %s",
		sanitizeInline(session.DisplayName), sanitizeCode(session.Slug), mode, teamSpeak, setup,
		queued, payload.channelCapabilities().plainTextNotice(), createNextAction(submission.modded, submission.autoStart),
	), components: components}, nil
}

func createNextAction(modded, autoStart bool) string {
	if modded {
		return "continue to mod options, then use `/rb status` while validation finishes."
	}
	if autoStart {
		return "use `/rb status`; setup will begin automatically after mission validation."
	}
	return "use `/rb status` while validation finishes, then `/rb start` when ready."
}

func parseCreateModalSubmission(
	payload interactionPayload,
	actor domain.Actor,
	correlationID string,
	keyPrefix string,
	requestedAt time.Time,
) (createModalSubmission, error) {
	if payload.Data == nil || payload.Data.CustomID != createModalCustomID || (len(payload.Data.Components) != 4 && len(payload.Data.Components) != 5) {
		return createModalSubmission{}, newUserError("The creation form is malformed or expired. Run `/rb create` again.")
	}

	var submission = createModalSubmission{gameType: "arma3"}
	seen := make(map[string]bool, 4)
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
			if component.Type != componentTypeCheckboxGroup || len(component.Values) > 3 {
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
				case createFeatureAutoStart:
					submission.autoStart = true
				default:
					return createModalSubmission{}, newUserError("Choose only the supported mode and TeamSpeak options.")
				}
			}
		case createMissionCustomID:
			attachment, err := resolveModalAttachment(payload.Data, component, false)
			if err != nil {
				return createModalSubmission{}, newUserError("Upload at most one mission .pbo file.")
			}
			submission.mission = attachment
		case createMissionWorkshopID:
			if component.Type != componentTypeTextInput {
				return createModalSubmission{}, newUserError("The Workshop link field is invalid.")
			}
			submission.missionWorkshop = strings.TrimSpace(component.Value)
		default:
			return createModalSubmission{}, newUserError("The creation form contains an unsupported field. Run `/rb create` again.")
		}
	}

	for _, customID := range []string{
		createNameCustomID, createDescriptionCustomID, createFeaturesCustomID,
		createMissionCustomID,
	} {
		if !seen[customID] {
			return createModalSubmission{}, newUserError("The creation form is incomplete. Run `/rb create` again.")
		}
	}
	if len(payload.Data.Components) == 5 && !seen[createMissionWorkshopID] {
		return createModalSubmission{}, newUserError("The creation form is incomplete. Run `/rb create` again.")
	}
	if count := utf8.RuneCountInString(submission.name); count < 1 || count > 100 {
		return createModalSubmission{}, newUserError("The session name must contain 1 to 100 characters.")
	}

	if submission.mission != nil {
		missionRequest := createArtifactRequest(payload, actor, correlationID, "pending-session", domain.ArtifactMission, *submission.mission, keyPrefix+":mission", requestedAt)
		if err := missionRequest.Validate(); err != nil {
			return createModalSubmission{}, newUserError("The mission upload must be a .pbo file no larger than 100 MiB from Discord.")
		}
	}
	if submission.mission != nil && submission.missionWorkshop != "" {
		return createModalSubmission{}, newUserError("Provide either a mission upload or Workshop link, not both.")
	}
	if submission.missionWorkshop != "" {
		if _, err := domain.ParseWorkshopURL(submission.missionWorkshop); err != nil {
			return createModalSubmission{}, newUserError("Provide a canonical public Steam Workshop link.")
		}
	}
	return submission, nil
}

type workshopRequester interface {
	RequestWorkshopResolve(context.Context, domain.Actor, domain.WorkshopSourceRequest) error
}

func requestWorkshopResolve(ctx context.Context, service SessionService, actor domain.Actor, request domain.WorkshopSourceRequest) error {
	requester, ok := service.(workshopRequester)
	if !ok {
		return fmt.Errorf("Workshop resolution is not configured")
	}
	return requester.RequestWorkshopResolve(ctx, actor, request)
}

func createWorkshopRequest(payload interactionPayload, actor domain.Actor, correlationID, sessionID string, target domain.WorkshopTarget, sourceURL, idempotencyKey string, requestedAt time.Time) domain.WorkshopSourceRequest {
	return domain.WorkshopSourceRequest{MessageType: "workshop_resolution", SchemaVersion: 1, SessionID: sessionID, Target: target, SourceURL: sourceURL, ActorID: actor.ID, GuildID: strings.TrimSpace(payload.GuildID), ChannelID: strings.TrimSpace(payload.ChannelID), CorrelationID: correlationID, IdempotencyKey: idempotencyKey, RequestedAt: requestedAt.UTC()}
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
		Roles:          interactionRoles(payload),
		GuildID:        strings.TrimSpace(payload.GuildID),
		ChannelID:      strings.TrimSpace(payload.ChannelID),
		CorrelationID:  strings.TrimSpace(correlationID),
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
		RequestedAt:    requestedAt.UTC(),
	}
}

func interactionRoles(payload interactionPayload) []string {
	if payload.Member == nil {
		return nil
	}
	return append([]string(nil), payload.Member.Roles...)
}
