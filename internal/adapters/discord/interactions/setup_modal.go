package interactions

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const setupModalCustomIDPrefix = "rb:setup:v1:"

func (payload interactionPayload) isRBSetupCommand() bool {
	if payload.Type != interactionTypeApplicationCommand {
		return false
	}
	subcommand, err := payload.subcommand()
	return err == nil && subcommand.Name == "setup"
}

func isSetupModalCustomID(customID string) bool {
	return strings.TrimSpace(strings.TrimPrefix(customID, setupModalCustomIDPrefix)) != "" &&
		strings.HasPrefix(customID, setupModalCustomIDPrefix)
}

func (handler *Handler) openSetupModal(ctx context.Context, writer http.ResponseWriter, payload interactionPayload, actor domain.Actor) error {
	subcommand, err := payload.subcommand()
	if err != nil {
		return newUserError("Select a draft session to repair.")
	}
	sessionID, err := handler.resolveSessionID(ctx, subcommand.Options, actor, payload.GuildID, false, false)
	if err != nil {
		return err
	}
	session, err := handler.service.Get(ctx, appsession.GetQuery{Actor: actor, SessionID: sessionID, GuildID: payload.GuildID})
	if err != nil {
		return fmt.Errorf("get setup session: %w", err)
	}
	if session.LifecycleState != domain.StateDraft {
		return newUserError("Setup can only edit a session that is still being configured.")
	}
	customID := setupModalCustomIDPrefix + session.ID
	if len(customID) > 100 {
		return fmt.Errorf("setup modal identifier exceeds Discord limit")
	}
	writeSessionSetupModal(writer, customID, "Repair Arma 3 setup", session, false)
	return nil
}

func (handler *Handler) submitSetupModal(ctx context.Context, payload interactionPayload, actor domain.Actor, correlationID string) (string, error) {
	sessionID := strings.TrimSpace(strings.TrimPrefix(payload.Data.CustomID, setupModalCustomIDPrefix))
	keyPrefix := "discord:" + strings.TrimSpace(payload.ID) + ":setup-modal"
	now := handler.clock.Now().UTC()
	submission, err := parseSetupModalSubmission(payload, actor, correlationID, keyPrefix, now)
	if err != nil {
		return "", err
	}
	session, err := handler.service.Get(ctx, appsession.GetQuery{Actor: actor, SessionID: sessionID, GuildID: payload.GuildID})
	if err != nil {
		return "", fmt.Errorf("get setup draft: %w", err)
	}
	if session.LifecycleState != domain.StateDraft {
		return "", newUserError("This session is no longer a draft, so its setup cannot be edited.")
	}
	existing := session
	session, err = handler.service.UpdateDraftSetup(ctx, appsession.UpdateDraftSetupCommand{
		Actor: actor, SessionID: session.ID, GuildID: payload.GuildID,
		CorrelationID: correlationID, IdempotencyKey: keyPrefix + ":update",
		GameProfileID: defaultGameProfileID, SleepAfterSeconds: session.SleepAfterSeconds,
		ArchiveAfterSeconds: session.ArchiveAfterSeconds, TeamSpeakEnabled: submission.teamSpeak,
		Vanilla: !submission.modded, DisplayName: submission.name, Description: submission.description,
		ReplaceMission: submission.mission != nil, ReplacePreset: submission.preset != nil,
	})
	if err != nil {
		if submission.mission != nil && !artifactReplaceable(existing.MissionArtifactStatus, existing.MissionObjectKey) {
			return "", newUserError("The mission is already accepted or validating and cannot be replaced through `/rb setup`.")
		}
		if submission.preset != nil && !artifactReplaceable(existing.PresetArtifactStatus, existing.PresetObjectKey) {
			return "", newUserError("The preset is already accepted or validating and cannot be replaced through `/rb setup`.")
		}
		return "", fmt.Errorf("update setup draft: %w", err)
	}
	if err := handler.service.RequestSessionCard(ctx, appsession.SessionCardCommand{
		Actor: actor, SessionID: session.ID, GuildID: payload.GuildID, ChannelID: session.ChannelID,
		CorrelationID: correlationID, NotificationID: keyPrefix + ":card",
		Content: sessioncard.RenderPublic(sessioncard.Project(session, sessioncard.Options{Now: handler.clock.Now().UTC()})), CardRevision: session.Version,
	}); err != nil {
		return "", fmt.Errorf("refresh setup session card: %w", err)
	}

	queued := []string{}
	if submission.mission != nil {
		request := createArtifactRequest(payload, actor, correlationID, session.ID, domain.ArtifactMission, *submission.mission, keyPrefix+":mission", now)
		if err := handler.service.RequestArtifactIngest(ctx, actor, request); err != nil {
			return "", fmt.Errorf("queue replacement mission: %w", err)
		}
		queued = append(queued, "mission")
	}
	if submission.preset != nil {
		request := createArtifactRequest(payload, actor, correlationID, session.ID, domain.ArtifactPreset, *submission.preset, keyPrefix+":preset", now)
		if err := handler.service.RequestArtifactIngest(ctx, actor, request); err != nil {
			return "", fmt.Errorf("queue replacement preset: %w", err)
		}
		queued = append(queued, "preset")
	}
	result := "Setup fields updated."
	if len(queued) > 0 {
		result += " Replacement " + strings.Join(queued, " and ") + " queued for validation; the files have not been accepted yet."
	}
	return fmt.Sprintf(
		"**Draft setup updated**\nName: %s\nSlug: `%s`\n%s%s",
		sanitizeInline(session.DisplayName), sanitizeCode(session.Slug), result,
		payload.channelCapabilities().plainTextNotice(),
	), nil
}

func artifactReplaceable(status domain.ArtifactStatus, objectKey string) bool {
	return strings.TrimSpace(objectKey) == "" && (status == "" || status == domain.ArtifactRejected)
}

func parseSetupModalSubmission(payload interactionPayload, actor domain.Actor, correlationID, keyPrefix string, requestedAt time.Time) (createModalSubmission, error) {
	if payload.Data == nil || !isSetupModalCustomID(payload.Data.CustomID) || len(payload.Data.Components) != 5 {
		return createModalSubmission{}, newUserError("The setup form is malformed or expired. Run `/rb setup` again.")
	}
	var submission createModalSubmission
	seen := make(map[string]bool, 5)
	for _, label := range payload.Data.Components {
		if label.Type != componentTypeLabel || label.Component == nil {
			return createModalSubmission{}, newUserError("The setup form is malformed or expired. Run `/rb setup` again.")
		}
		component := label.Component
		if seen[component.CustomID] {
			return createModalSubmission{}, newUserError("The setup form contains a duplicate field. Run `/rb setup` again.")
		}
		seen[component.CustomID] = true
		switch component.CustomID {
		case createNameCustomID:
			if component.Type != componentTypeTextInput {
				return createModalSubmission{}, newUserError("The session name field is invalid. Run `/rb setup` again.")
			}
			submission.name = normalizeSingleLine(component.Value)
		case createDescriptionCustomID:
			if component.Type != componentTypeTextInput {
				return createModalSubmission{}, newUserError("The description field is invalid. Run `/rb setup` again.")
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
			features := map[string]bool{}
			for _, value := range component.Values {
				if features[value] {
					return createModalSubmission{}, newUserError("The mode and features selection is invalid.")
				}
				features[value] = true
				switch value {
				case createFeatureModded:
					submission.modded = true
				case createFeatureTeamSpeak:
					submission.teamSpeak = true
				default:
					return createModalSubmission{}, newUserError("Choose only the supported mode and TeamSpeak options.")
				}
			}
		case createMissionCustomID, createPresetCustomID:
			attachment, err := resolveModalAttachment(payload.Data, component, false)
			if err != nil {
				return createModalSubmission{}, newUserError("Choose at most one valid replacement file per artifact.")
			}
			if component.CustomID == createMissionCustomID {
				submission.mission = attachment
			} else {
				submission.preset = attachment
			}
		default:
			return createModalSubmission{}, newUserError("The setup form contains an unsupported field. Run `/rb setup` again.")
		}
	}
	for _, customID := range []string{createNameCustomID, createDescriptionCustomID, createFeaturesCustomID, createMissionCustomID, createPresetCustomID} {
		if !seen[customID] {
			return createModalSubmission{}, newUserError("The setup form is incomplete. Run `/rb setup` again.")
		}
	}
	if count := utf8.RuneCountInString(submission.name); count < 1 || count > 100 {
		return createModalSubmission{}, newUserError("The session name must contain 1 to 100 characters.")
	}
	if submission.mission != nil {
		request := createArtifactRequest(payload, actor, correlationID, "pending-session", domain.ArtifactMission, *submission.mission, keyPrefix+":mission", requestedAt)
		if err := request.Validate(); err != nil {
			return createModalSubmission{}, newUserError("The mission upload must be a .pbo file no larger than 100 MiB from Discord.")
		}
	}
	if submission.preset != nil {
		request := createArtifactRequest(payload, actor, correlationID, "pending-session", domain.ArtifactPreset, *submission.preset, keyPrefix+":preset", requestedAt)
		if err := request.Validate(); err != nil {
			return createModalSubmission{}, newUserError("The preset upload must be an .html or .htm file no larger than 10 MiB from Discord.")
		}
	}
	return submission, nil
}
