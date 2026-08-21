package interactions

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const (
	modsModalCustomIDPrefix = "rb:mods:v1:"
	modsPresetCustomID      = "mods:preset"
)

func (payload interactionPayload) isRBModsCommand() bool {
	if payload.Type != interactionTypeApplicationCommand {
		return false
	}
	subcommand, err := payload.subcommand()
	return err == nil && subcommand.Name == "mods"
}

func isModsModalCustomID(customID string) bool {
	_, _, err := parseModsModalCustomID(customID)
	return err == nil
}

func modsModalCustomID(sessionID string, activeRevision int64) (string, error) {
	customID := fmt.Sprintf("%s%s:%d", modsModalCustomIDPrefix, strings.TrimSpace(sessionID), activeRevision)
	if strings.TrimSpace(sessionID) == "" || activeRevision < 1 || len(customID) > 100 {
		return "", fmt.Errorf("mods modal state is invalid")
	}
	return customID, nil
}

func parseModsModalCustomID(customID string) (string, int64, error) {
	remainder := strings.TrimPrefix(strings.TrimSpace(customID), modsModalCustomIDPrefix)
	separator := strings.LastIndexByte(remainder, ':')
	if remainder == "" || separator < 1 || separator == len(remainder)-1 {
		return "", 0, fmt.Errorf("mods modal identifier is invalid")
	}
	revision, err := strconv.ParseInt(remainder[separator+1:], 10, 64)
	if err != nil || revision < 1 {
		return "", 0, fmt.Errorf("mods modal revision is invalid")
	}
	sessionID := strings.TrimSpace(remainder[:separator])
	if sessionID == "" {
		return "", 0, fmt.Errorf("mods modal session is invalid")
	}
	return sessionID, revision, nil
}

func (handler *Handler) openModsModal(ctx context.Context, writer http.ResponseWriter, payload interactionPayload, actor domain.Actor) error {
	subcommand, err := payload.subcommand()
	if err != nil {
		return newUserError("Select a modded session to update.")
	}
	sessionID, err := handler.resolveSessionID(ctx, subcommand.Options, actor, payload.GuildID, false, false)
	if err != nil {
		return err
	}
	session, err := handler.service.Get(ctx, appsession.GetQuery{Actor: actor, SessionID: sessionID, GuildID: payload.GuildID})
	if err != nil {
		return fmt.Errorf("get mods session: %w", err)
	}
	active := session.EffectiveActivePresetRevision()
	if err := session.ValidatePresetRevisionStaging(active.Number); err != nil {
		return modsStagingUserError(err)
	}
	customID, err := modsModalCustomID(session.ID, active.Number)
	if err != nil {
		return err
	}
	required := true
	one := 1
	components := []interactionComponent{{
		Type: componentTypeLabel, Label: "Launcher preset", Description: "Upload one .html or .htm preset. A running server will not be interrupted.",
		Component: &interactionComponent{Type: componentTypeFileUpload, CustomID: modsPresetCustomID, Required: &required, MinValues: &one, MaxValues: &one},
	}}
	writeJSON(writer, http.StatusOK, interactionResponse{Type: interactionResponseModal, Data: &interactionResponseData{
		CustomID:   customID,
		Title:      "Stage Arma 3 mod revision",
		Components: &components,
	}})
	return nil
}

func (handler *Handler) submitModsModal(ctx context.Context, payload interactionPayload, actor domain.Actor, correlationID string) (string, error) {
	sessionID, expectedActiveRevision, err := parseModsModalCustomID(payload.Data.CustomID)
	if err != nil || len(payload.Data.Components) != 1 {
		return "", newUserError("The mod revision form is malformed or expired. Run `/rb mods` again.")
	}
	label := payload.Data.Components[0]
	if label.Type != componentTypeLabel || label.Component == nil || label.Component.Type != componentTypeFileUpload || label.Component.CustomID != modsPresetCustomID {
		return "", newUserError("The mod revision form is malformed or expired. Run `/rb mods` again.")
	}
	attachment, err := resolveModalAttachment(payload.Data, label.Component, true)
	if err != nil || attachment == nil {
		return "", newUserError("Choose exactly one Arma Launcher .html or .htm preset no larger than 10 MiB.")
	}
	request := createArtifactRequest(payload, actor, correlationID, sessionID, domain.ArtifactPreset, *attachment, "discord:"+strings.TrimSpace(payload.ID)+":mods", handler.clock.Now().UTC())
	request.Purpose = domain.ArtifactPurposePresetRevision
	request.ExpectedActivePresetRevision = expectedActiveRevision
	if err := request.Validate(); err != nil {
		return "", newUserError("The preset upload must be an .html or .htm file no larger than 10 MiB from Discord.")
	}
	if err := handler.service.RequestArtifactIngest(ctx, actor, request); err != nil {
		return "", modsStagingUserError(err)
	}
	return fmt.Sprintf("**Mod revision queued for validation**\n`%s` has not been accepted yet. The running server was not interrupted. If validation succeeds, revision %d will remain pending until the next start, wake, or restore.\n\nNext: use `/rb status` to verify validation; a failed validation does not schedule a retry.", sanitizeCode(attachment.Filename), expectedActiveRevision+1), nil
}

func modsStagingUserError(err error) error {
	switch {
	case strings.Contains(err.Error(), "vanilla sessions"):
		return newUserError("This is a vanilla session and does not have an active mod preset to revise.")
	case strings.Contains(err.Error(), "already") && strings.Contains(err.Error(), "preset revision"):
		return newUserError("A mod revision is already pending or being applied. Use `/rb status` before uploading another.")
	case strings.Contains(err.Error(), "active preset revision changed"), strings.Contains(err.Error(), "lifecycle state"), strings.Contains(err.Error(), "active lifecycle operation"):
		return newUserError("The session changed while the form was open. Run `/rb mods` again after the current operation finishes.")
	default:
		return err
	}
}
