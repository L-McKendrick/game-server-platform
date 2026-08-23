package interactions

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func parseServerConfigRevision(customID, prefix string) (int64, error) {
	if !strings.HasPrefix(customID, prefix) {
		return 0, fmt.Errorf("server configuration control is invalid")
	}
	revision, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(customID, prefix)), 10, 64)
	if err != nil || revision < 0 {
		return 0, fmt.Errorf("server configuration revision is invalid")
	}
	return revision, nil
}

func (handler *Handler) writeAdminServerConfigView(ctx context.Context, writer http.ResponseWriter, responseType int, guildID, status string) error {
	if handler.serverConfig == nil {
		handler.writeAdminView(writer, responseType, "**Arma server config**\nConfiguration uploads are unavailable in this deployment.", adminMenuServerConfig, nil, true)
		return nil
	}
	config, found, err := handler.serverConfig.Current(ctx, guildID, true)
	if err != nil {
		return fmt.Errorf("read guild server configuration: %w", err)
	}
	revision := int64(0)
	if found {
		revision = config.Revision
	}
	content := "**Arma server config**\nFuture sessions use the platform-generated safe default. Upload one UTF-8 `.cfg` file up to 64 KiB to replace it. File contents are private and are never displayed. Invalid files and stale revisions are not retried; transient processing failures use the worker's bounded retry policy."
	controls := []interactionComponent{{Type: componentTypeActionRow, Components: []interactionComponent{{Type: componentTypeButton, Style: buttonStylePrimary, Label: "Upload server.cfg", CustomID: fmt.Sprintf("%s%d", adminServerConfigUploadPrefix, revision)}}}}
	if found && config.Active() {
		content = fmt.Sprintf("**Arma server config**\nActive: `%s`\nRevision: `%d`\nSize: %d bytes\nUpdated: <t:%d:F>\n\nContents are private and are never displayed. New sessions snapshot this exact revision when start begins; existing session replays retain their snapshot.", sanitizeCode(config.Filename), config.Revision, config.SizeBytes, config.UpdatedAt.UTC().Unix())
		controls = append(controls, interactionComponent{Type: componentTypeActionRow, Components: []interactionComponent{{Type: componentTypeButton, Style: buttonStyleDanger, Label: "Remove custom config", CustomID: fmt.Sprintf("%s%d", adminServerConfigRemovePrefix, config.Revision)}}})
	}
	if strings.TrimSpace(status) != "" {
		content = "**Saved**\n" + status + "\n\n" + content
	}
	handler.writeAdminView(writer, responseType, content, adminMenuServerConfig, controls, true)
	return nil
}

func (handler *Handler) openServerConfigModal(writer http.ResponseWriter, payload interactionPayload) error {
	if payload.Type != interactionTypeMessageComponent || payload.Data.ComponentType != componentTypeButton || !payload.memberIsAdministrator() || handler.serverConfig == nil {
		return domain.ErrForbidden
	}
	revision, err := parseServerConfigRevision(payload.Data.CustomID, adminServerConfigUploadPrefix)
	if err != nil {
		return newUserError("This server configuration control is malformed or stale. Reopen `/rb admin`.")
	}
	required, one := true, 1
	components := []interactionComponent{{
		Type: componentTypeLabel, Label: "Arma server.cfg", Description: "Upload one private UTF-8 .cfg file up to 64 KiB.",
		Component: &interactionComponent{Type: componentTypeFileUpload, CustomID: adminServerConfigFileCustomID, Required: &required, MinValues: &one, MaxValues: &one},
	}}
	writeJSON(writer, http.StatusOK, interactionResponse{Type: interactionResponseModal, Data: &interactionResponseData{
		CustomID: fmt.Sprintf("%s%d", adminServerConfigUploadPrefix, revision), Title: "Set Arma server config", Components: &components,
	}})
	return nil
}

func (handler *Handler) submitServerConfigModal(ctx context.Context, writer http.ResponseWriter, payload interactionPayload, actorID, correlationID string) error {
	if !payload.memberIsAdministrator() || handler.serverConfig == nil {
		return domain.ErrForbidden
	}
	revision, err := parseServerConfigRevision(payload.Data.CustomID, adminServerConfigUploadPrefix)
	if err != nil || len(payload.Data.Components) != 1 {
		return newUserError("The server configuration form is malformed or stale. Reopen `/rb admin`.")
	}
	label := payload.Data.Components[0]
	if label.Type != componentTypeLabel || label.Component == nil || label.Component.Type != componentTypeFileUpload || label.Component.CustomID != adminServerConfigFileCustomID {
		return newUserError("The server configuration form is malformed or stale. Reopen `/rb admin`.")
	}
	attachment, err := resolveModalAttachment(payload.Data, label.Component, true)
	if err != nil || attachment == nil {
		return newUserError("Choose exactly one UTF-8 `.cfg` file no larger than 64 KiB.")
	}
	request := createArtifactRequest(payload, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: actorID}, correlationID, "", domain.ArtifactServerConfig, *attachment, "discord:"+strings.TrimSpace(payload.ID)+":server-config", handler.clock.Now().UTC())
	request.Purpose = domain.ArtifactPurposeServerConfig
	request.ExpectedServerConfigRevision = revision
	if err := request.Validate(); err != nil {
		return newUserError("The server configuration must be one `.cfg` file no larger than 64 KiB from Discord.")
	}
	if err := handler.serverConfig.RequestUpload(ctx, request, true); err != nil {
		return fmt.Errorf("queue guild server configuration: %w", err)
	}
	handler.writeAdminView(writer, interactionResponseChannelMessageWithSource,
		fmt.Sprintf("**Server configuration queued for private validation**\n`%s` is not active yet. Reopen `/rb admin` to verify the new revision after processing. Invalid files and stale revisions are not retried; transient processing failures use bounded retries.", sanitizeCode(attachment.Filename)),
		adminMenuServerConfig, nil, true)
	return nil
}

func (handler *Handler) writeServerConfigRemovePrompt(writer http.ResponseWriter, payload interactionPayload) error {
	if payload.Type != interactionTypeMessageComponent || payload.Data.ComponentType != componentTypeButton || !payload.memberIsAdministrator() {
		return domain.ErrForbidden
	}
	revision, err := parseServerConfigRevision(payload.Data.CustomID, adminServerConfigRemovePrefix)
	if err != nil || revision < 1 {
		return newUserError("This server configuration control is malformed or stale. Reopen `/rb admin`.")
	}
	controls := []interactionComponent{{Type: componentTypeActionRow, Components: []interactionComponent{
		{Type: componentTypeButton, Style: buttonStyleDanger, Label: "Use generated default", CustomID: fmt.Sprintf("%s%d", adminServerConfigConfirmPrefix, revision)},
		{Type: componentTypeButton, Style: buttonStyleSecondary, Label: "Keep custom config", CustomID: adminServerConfigCancelID},
	}}}
	handler.writeAdminView(writer, interactionResponseUpdateMessage, "**Remove the active custom server.cfg?**\nFuture sessions will use the generated safe default. Existing sessions keep their captured revision for deterministic replay.", adminMenuServerConfig, controls, true)
	return nil
}

func (handler *Handler) removeServerConfig(ctx context.Context, writer http.ResponseWriter, payload interactionPayload, actorID string) error {
	if payload.Type != interactionTypeMessageComponent || payload.Data.ComponentType != componentTypeButton || !payload.memberIsAdministrator() || handler.serverConfig == nil {
		return domain.ErrForbidden
	}
	revision, err := parseServerConfigRevision(payload.Data.CustomID, adminServerConfigConfirmPrefix)
	if err != nil || revision < 1 {
		return newUserError("This server configuration control is malformed or stale. Reopen `/rb admin`.")
	}
	removed, err := handler.serverConfig.Remove(ctx, payload.GuildID, actorID, revision, true)
	if err != nil {
		return fmt.Errorf("remove guild server configuration: %w", err)
	}
	return handler.writeAdminServerConfigView(ctx, writer, interactionResponseUpdateMessage, payload.GuildID, fmt.Sprintf("Custom `server.cfg` removed at revision `%d`; future sessions use the generated default.", removed.Revision))
}
