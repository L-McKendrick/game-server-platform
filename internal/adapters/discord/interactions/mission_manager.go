package interactions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const missionManagerPrefix = "rb:missions:v1:"
const missionUploadPrefix = "rb:mission-upload:v1:"
const missionUploadField = "mission:file"
const missionWorkshopField = "mission:workshop"
const maximumMissionButtonLabelRunes = 80

type missionManagerEntry struct {
	record  domain.MissionRecord
	index   int
	pending bool
}

func (payload interactionPayload) isRBEditCommand() bool {
	if payload.Type != interactionTypeApplicationCommand {
		return false
	}
	subcommand, err := payload.subcommand()
	return err == nil && subcommand.Name == "edit"
}

func editSection(options []applicationCommandOption) string {
	for _, option := range options {
		if option.Name == "section" {
			var value string
			_ = json.Unmarshal(option.Value, &value)
			return value
		}
	}
	return ""
}

func (handler *Handler) openEdit(ctx context.Context, writer http.ResponseWriter, payload interactionPayload, actor domain.Actor) error {
	subcommand, err := payload.subcommand()
	if err != nil {
		return err
	}
	switch editSection(subcommand.Options) {
	case "mods":
		return handler.openModsModal(ctx, writer, payload, actor)
	case "mission-files":
		sessionID, err := handler.resolveSessionID(ctx, subcommand.Options, actor, payload.GuildID, false, false)
		if err != nil {
			return err
		}
		session, err := handler.service.Get(ctx, appsession.GetQuery{Actor: actor, SessionID: sessionID, GuildID: payload.GuildID})
		if err != nil {
			return err
		}
		writeMissionManager(writer, session, 0)
		return nil
	default:
		return newUserError("Choose an edit section supported by this game.")
	}
}

func missionCustomID(sessionID, action string, index, page int, version int64) string {
	return fmt.Sprintf("%s%s:%s:%d:%d:%d", missionManagerPrefix, action, sessionID, index, page, version)
}

func writeMissionManager(writer http.ResponseWriter, session domain.Session, page int) {
	active := make([]missionManagerEntry, 0, len(session.MissionFiles))
	finalizedWorkshopItems := make(map[uint64]bool)
	for index, record := range session.MissionFiles {
		if record.WorkshopItemID != 0 {
			finalizedWorkshopItems[record.WorkshopItemID] = true
		}
		if record.Active() {
			active = append(active, missionManagerEntry{record: record, index: index})
		}
	}
	pendingWorkshopItems := make(map[uint64]bool)
	for _, source := range session.WorkshopMissionSources {
		for _, item := range source.AcceptedItems {
			if item.PublishedFileID == 0 || finalizedWorkshopItems[item.PublishedFileID] || pendingWorkshopItems[item.PublishedFileID] {
				continue
			}
			pendingWorkshopItems[item.PublishedFileID] = true
			active = append(active, missionManagerEntry{record: domain.MissionRecord{Filename: item.Filename, WorkshopItemID: item.PublishedFileID}, index: -1, pending: true})
		}
		// Older source snapshots do not retain canonical filenames. Keep them
		// visible by Workshop identity until the host publishes the final name.
		for _, itemID := range source.AcceptedItemIDs {
			if itemID == 0 || finalizedWorkshopItems[itemID] || pendingWorkshopItems[itemID] {
				continue
			}
			pendingWorkshopItems[itemID] = true
			active = append(active, missionManagerEntry{record: domain.MissionRecord{Filename: fmt.Sprintf("Workshop item #%d", itemID), WorkshopItemID: itemID}, index: -1, pending: true})
		}
	}
	pages := (len(active) + 4) / 5
	if pages < 1 {
		pages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	configured := session.ConfiguredMission
	if configured.Template == "" {
		configured = session.MissionForApplication()
	}
	components := []interactionComponent{{
		Type:    componentTypeTextDisplay,
		Content: fmt.Sprintf("**Mission files — %s**\nConfigured default: `%s`\nPage %d/%d", sanitizeInline(session.DisplayName), sanitizeCode(configured.Template), page+1, pages),
	}}
	builtInRow := interactionComponent{Type: componentTypeActionRow, Components: []interactionComponent{{
		Type: componentTypeButton, Style: buttonStyleSecondary, Label: missionButtonLabel(domain.DefaultArma3MissionTemplate, "built-in"),
		CustomID: missionCustomID(session.ID, "label", -1, page, session.Version), Disabled: true,
	}}}
	if !configured.IsDefault() {
		builtInRow.Components = append(builtInRow.Components, interactionComponent{
			Type: componentTypeButton, Style: buttonStyleSecondary, Label: "Default", CustomID: missionCustomID(session.ID, "default-built-in", -1, page, session.Version),
		})
	}
	components = append(components, builtInRow)

	start, end := page*5, page*5+5
	if end > len(active) {
		end = len(active)
	}
	for _, entry := range active[start:end] {
		status := string(entry.record.Status)
		if entry.pending {
			status = "awaiting download"
		}
		if entry.record.WorkshopItemID != 0 {
			status += fmt.Sprintf(", Workshop #%d", entry.record.WorkshopItemID)
		}
		if entry.record.ObjectKey != "" && session.CurrentMission.ObjectKey == entry.record.ObjectKey {
			status += ", currently loaded"
		}
		if entry.record.ObjectKey != "" && configured.ObjectKey == entry.record.ObjectKey {
			status += ", configured"
		}
		row := interactionComponent{Type: componentTypeActionRow, Components: []interactionComponent{{
			Type: componentTypeButton, Style: buttonStyleSecondary, Label: missionButtonLabel(entry.record.Filename, status),
			CustomID: missionCustomID(session.ID, "label", entry.index, page, session.Version), Disabled: true,
		}}}
		if !entry.pending && entry.record.Status == domain.ArtifactAccepted && configured.ObjectKey != entry.record.ObjectKey {
			row.Components = append(row.Components, interactionComponent{
				Type: componentTypeButton, Style: buttonStyleSecondary, Label: "Default", CustomID: missionCustomID(session.ID, "default", entry.index, page, session.Version),
			})
		}
		if !entry.pending && session.CurrentMission.ObjectKey != entry.record.ObjectKey {
			row.Components = append(row.Components, interactionComponent{
				Type: componentTypeButton, Style: buttonStyleDanger, Label: "Remove", CustomID: missionCustomID(session.ID, "remove", entry.index, page, session.Version),
			})
		}
		components = append(components, row)
	}
	if pages > 1 {
		components = append(components, interactionComponent{Type: componentTypeActionRow, Components: []interactionComponent{
			interactionComponent{Type: componentTypeButton, Style: buttonStyleSecondary, Label: "Previous", CustomID: missionCustomID(session.ID, "page", -1, page-1, session.Version), Disabled: page == 0},
			interactionComponent{Type: componentTypeButton, Style: buttonStyleSecondary, Label: "Next", CustomID: missionCustomID(session.ID, "page", -1, page+1, session.Version), Disabled: page+1 >= pages},
		}})
	}
	components = append(components, interactionComponent{Type: componentTypeActionRow, Components: []interactionComponent{{
		Type: componentTypeButton, Style: buttonStylePrimary, Label: "Add mission", CustomID: missionCustomID(session.ID, "add", -1, page, session.Version),
	}}})
	container := []interactionComponent{{Type: componentTypeContainer, Components: components}}
	writeJSON(writer, http.StatusOK, interactionResponse{
		Type: interactionResponseChannelMessageWithSource,
		Data: renderer.messageData("", messageFlagEphemeral|messageFlagComponentsV2, &container),
	})
}

func missionButtonLabel(filename, status string) string {
	filename = normalizeSingleLine(filename)
	if filename == "" {
		filename = "(unnamed)"
	}
	status = normalizeSingleLine(status)
	suffix := " — " + status
	suffixRunes := []rune(suffix)
	if len(suffixRunes) >= maximumMissionButtonLabelRunes {
		return string(suffixRunes[:maximumMissionButtonLabelRunes-1]) + "…"
	}
	available := maximumMissionButtonLabelRunes - len(suffixRunes)
	filenameRunes := []rune(filename)
	if len(filenameRunes) > available {
		if available == 1 {
			filename = "…"
		} else {
			filename = string(filenameRunes[:available-1]) + "…"
		}
	}
	return filename + suffix
}

func parseMissionCustomID(value string) (action, sessionID string, index, page int, version int64, err error) {
	if !strings.HasPrefix(value, missionManagerPrefix) || len(value) > 100 {
		err = fmt.Errorf("invalid mission control")
		return
	}
	parts := strings.Split(strings.TrimPrefix(value, missionManagerPrefix), ":")
	if len(parts) != 5 {
		err = fmt.Errorf("invalid mission control")
		return
	}
	action, sessionID = parts[0], parts[1]
	index, err = strconv.Atoi(parts[2])
	if err != nil {
		return
	}
	page, err = strconv.Atoi(parts[3])
	if err != nil {
		return
	}
	version, err = strconv.ParseInt(parts[4], 10, 64)
	if err == nil {
		allowed := action == "add" || action == "page" || action == "default" || action == "default-built-in" || action == "remove"
		if !allowed || strings.TrimSpace(sessionID) == "" || version < 1 || page < -1 {
			err = fmt.Errorf("invalid mission control")
		}
	}
	return
}

func isMissionManagerComponent(payload interactionPayload) bool {
	return payload.Data != nil && strings.HasPrefix(payload.Data.CustomID, missionManagerPrefix)
}

func (handler *Handler) handleMissionManagerComponent(ctx context.Context, writer http.ResponseWriter, payload interactionPayload, actor domain.Actor) error {
	action, sessionID, index, page, version, err := parseMissionCustomID(payload.Data.CustomID)
	if err != nil {
		return err
	}
	if action == "add" {
		session, err := handler.service.Get(ctx, appsession.GetQuery{Actor: actor, SessionID: sessionID, GuildID: payload.GuildID})
		if err != nil {
			return err
		}
		if session.Version != version || session.ActiveWorkflowID != "" {
			return domain.ErrConflict
		}
		return writeMissionUploadModal(writer, sessionID, version)
	}
	if action == "page" {
		session, err := handler.service.Get(ctx, appsession.GetQuery{Actor: actor, SessionID: sessionID, GuildID: payload.GuildID})
		if err != nil {
			return err
		}
		if session.Version != version {
			return domain.ErrConflict
		}
		writeMissionManager(writer, session, page)
		return nil
	}
	session, err := handler.service.UpdateMission(ctx, appsession.UpdateMissionCommand{Actor: actor, SessionID: sessionID, GuildID: payload.GuildID, CorrelationID: payload.ID, IdempotencyKey: "discord:" + payload.ID + ":mission", ExpectedVersion: version, Action: action, MissionIndex: index})
	if err != nil {
		return err
	}
	writeMissionManager(writer, session, page)
	return nil
}

func writeMissionUploadModal(writer http.ResponseWriter, sessionID string, version int64) error {
	optional := false
	zero, one, maximumURL := 0, 1, 200
	components := []interactionComponent{
		{Type: componentTypeLabel, Label: "Mission file", Description: "Optional .pbo upload; do not also provide a Workshop link.", Component: &interactionComponent{Type: componentTypeFileUpload, CustomID: missionUploadField, Required: &optional, MinValues: &zero, MaxValues: &one}},
		{Type: componentTypeLabel, Label: "Steam Workshop link", Description: "Optional public Arma 3 scenario item or collection.", Component: &interactionComponent{Type: componentTypeTextInput, CustomID: missionWorkshopField, Style: textInputStyleShort, Placeholder: "https://steamcommunity.com/sharedfiles/filedetails/?id=...", MaxLength: &maximumURL, Required: &optional}},
	}
	writeJSON(writer, http.StatusOK, interactionResponse{Type: interactionResponseModal, Data: &interactionResponseData{CustomID: fmt.Sprintf("%s%s:%d", missionUploadPrefix, sessionID, version), Title: "Add mission file", Components: &components}})
	return nil
}

func isMissionUploadModal(value string) bool { return strings.HasPrefix(value, missionUploadPrefix) }

func (handler *Handler) submitMissionUpload(ctx context.Context, payload interactionPayload, actor domain.Actor, correlationID string) (string, error) {
	remainder := strings.TrimPrefix(payload.Data.CustomID, missionUploadPrefix)
	split := strings.LastIndexByte(remainder, ':')
	if split < 1 {
		return "", newUserError("This mission upload is stale.")
	}
	sessionID := remainder[:split]
	version, err := strconv.ParseInt(remainder[split+1:], 10, 64)
	if err != nil {
		return "", err
	}
	session, err := handler.service.Get(ctx, appsession.GetQuery{Actor: actor, SessionID: sessionID, GuildID: payload.GuildID})
	if err != nil {
		return "", err
	}
	if session.Version != version || session.ActiveWorkflowID != "" {
		return "", domain.ErrConflict
	}
	if len(payload.Data.Components) != 2 || payload.Data.Components[0].Component == nil || payload.Data.Components[1].Component == nil {
		return "", newUserError("Provide one .pbo file or Workshop link.")
	}
	attachment, err := resolveModalAttachment(payload.Data, payload.Data.Components[0].Component, false)
	if err != nil {
		return "", newUserError("Provide one .pbo file or Workshop link.")
	}
	workshopURL := strings.TrimSpace(payload.Data.Components[1].Component.Value)
	if (attachment == nil) == (workshopURL == "") {
		return "", newUserError("Provide either one .pbo file or one Workshop link, not both.")
	}
	if workshopURL != "" {
		request := createWorkshopRequest(payload, actor, correlationID, sessionID, domain.WorkshopTargetMission, workshopURL, "discord:"+payload.ID+":mission-workshop", handler.clock.Now().UTC())
		request.ChannelID = session.ChannelID
		if err := requestWorkshopResolve(ctx, handler.service, actor, request); err != nil {
			return "", err
		}
		return "Workshop scenario link queued for metadata validation. Accepted scenarios will be added as mission choices in the download phase.", nil
	}
	request := createArtifactRequest(payload, actor, correlationID, sessionID, domain.ArtifactMission, *attachment, "discord:"+payload.ID+":mission-upload", handler.clock.Now().UTC())
	request.ChannelID = session.ChannelID
	if err := request.Validate(); err != nil {
		return "", newUserError("The mission must be a .pbo file no larger than 100 MiB.")
	}
	if err := handler.service.RequestArtifactIngest(ctx, actor, request); err != nil {
		return "", err
	}
	return fmt.Sprintf("Mission `%s` queued for validation. Reopen `/rb edit` → `mission-files` to see the accepted file.", sanitizeCode(attachment.Filename)), nil
}
