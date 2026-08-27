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
	modsModalCustomIDPrefix  = "rb:mods:v2:"
	createModsContinuePrefix = "rb:create-mods:v1:"
	modsPresetCustomID       = "mods:preset"
	modsWorkshopCustomID     = "mods:workshop"
	modsServerPresetCustomID = "mods:server-preset"
	modsCreatorDLCsCustomID  = "mods:creator-dlcs"
	modsModeCreate           = "create"
	modsModeRevision         = "revision"
)

type modsModalState struct {
	mode                                          string
	sessionID                                     string
	activeRevision, activeServerRevision, version int64
}

func createModsContinueCustomID(sessionID string, version int64) (string, error) {
	value := fmt.Sprintf("%s%s:%d", createModsContinuePrefix, strings.TrimSpace(sessionID), version)
	if strings.TrimSpace(sessionID) == "" || version < 1 || len(value) > 100 {
		return "", fmt.Errorf("create mod continuation state is invalid")
	}
	return value, nil
}

func parseCreateModsContinueCustomID(value string) (string, int64, error) {
	remainder := strings.TrimPrefix(strings.TrimSpace(value), createModsContinuePrefix)
	separator := strings.LastIndexByte(remainder, ':')
	if remainder == "" || separator < 1 {
		return "", 0, fmt.Errorf("create mod continuation is invalid")
	}
	version, err := strconv.ParseInt(remainder[separator+1:], 10, 64)
	if err != nil || version < 1 {
		return "", 0, fmt.Errorf("create mod continuation version is invalid")
	}
	return remainder[:separator], version, nil
}

func isCreateModsContinue(payload interactionPayload) bool {
	if payload.Type != interactionTypeMessageComponent || payload.Data == nil || payload.Data.ComponentType != componentTypeButton {
		return false
	}
	_, _, err := parseCreateModsContinueCustomID(payload.Data.CustomID)
	return err == nil
}

func modsModalCustomID(state modsModalState) (string, error) {
	value := fmt.Sprintf("%s%s:%s:%d:%d:%d", modsModalCustomIDPrefix, state.mode, strings.TrimSpace(state.sessionID), state.activeRevision, state.activeServerRevision, state.version)
	if (state.mode != modsModeCreate && state.mode != modsModeRevision) || strings.TrimSpace(state.sessionID) == "" || state.activeRevision < 0 || state.activeServerRevision < 0 || state.version < 1 || len(value) > 100 {
		return "", fmt.Errorf("mods modal state is invalid")
	}
	return value, nil
}

func parseModsModalCustomID(value string) (modsModalState, error) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(value), modsModalCustomIDPrefix), ":")
	if len(parts) != 5 || (parts[0] != modsModeCreate && parts[0] != modsModeRevision) || strings.TrimSpace(parts[1]) == "" {
		return modsModalState{}, fmt.Errorf("mods modal identifier is invalid")
	}
	active, activeErr := strconv.ParseInt(parts[2], 10, 64)
	activeServer, activeServerErr := strconv.ParseInt(parts[3], 10, 64)
	version, versionErr := strconv.ParseInt(parts[4], 10, 64)
	if activeErr != nil || activeServerErr != nil || versionErr != nil || active < 0 || activeServer < 0 || version < 1 {
		return modsModalState{}, fmt.Errorf("mods modal revision is invalid")
	}
	return modsModalState{mode: parts[0], sessionID: parts[1], activeRevision: active, activeServerRevision: activeServer, version: version}, nil
}

func isModsModalCustomID(customID string) bool {
	_, err := parseModsModalCustomID(customID)
	return err == nil
}

func (handler *Handler) openCreateModsModal(ctx context.Context, writer http.ResponseWriter, payload interactionPayload, actor domain.Actor) error {
	sessionID, version, err := parseCreateModsContinueCustomID(payload.Data.CustomID)
	if err != nil {
		return newUserError("This creation step is stale. Use `/rb edit` with section `mods` to continue setup.")
	}
	session, err := handler.service.Get(ctx, appsession.GetQuery{Actor: actor, SessionID: sessionID, GuildID: payload.GuildID})
	if err != nil {
		return err
	}
	if session.Version != version || session.Vanilla || session.LifecycleState != domain.StateDraft {
		return newUserError("This creation step is stale. Use `/rb edit` with section `mods` to continue setup.")
	}
	return writeModsModal(writer, session, modsModeCreate)
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
	if session.Vanilla {
		return newUserError("This is a vanilla session. Change it to modded in `/rb setup` before configuring mods.")
	}
	if session.ActiveWorkflowID != "" {
		return newUserError("Wait for the active lifecycle operation to finish before changing mods.")
	}
	mode := modsModeRevision
	if session.LifecycleState == domain.StateDraft && session.EffectiveActivePresetRevision().Empty() {
		mode = modsModeCreate
	}
	return writeModsModal(writer, session, mode)
}

func writeModsModal(writer http.ResponseWriter, session domain.Session, mode string) error {
	active := session.EffectiveActivePresetRevision()
	customID, err := modsModalCustomID(modsModalState{mode: mode, sessionID: session.ID, activeRevision: active.Number, activeServerRevision: session.EffectiveActiveServerPresetRevision().Number, version: session.Version})
	if err != nil {
		return err
	}
	optional, minimumNone, maximumOne, maximumDLCs, maximumURL := false, 0, 1, len(domain.SupportedCreatorDLCs()), 200
	labels := map[string]string{
		domain.CreatorDLCGlobalMobilization:  "Global Mobilization – Cold War Germany",
		domain.CreatorDLCSOGPrairieFire:      "S.O.G. Prairie Fire",
		domain.CreatorDLCCSLAIronCurtain:     "ČSLA Iron Curtain",
		domain.CreatorDLCWesternSahara:       "Western Sahara",
		domain.CreatorDLCSpearhead1944:       "Spearhead 1944",
		domain.CreatorDLCReactionForces:      "Reaction Forces",
		domain.CreatorDLCExpeditionaryForces: "Expeditionary Forces",
	}
	options := make([]interactionSelectOption, 0, maximumDLCs)
	for _, value := range domain.SupportedCreatorDLCs() {
		options = append(options, interactionSelectOption{Label: labels[value], Value: value, Default: containsString(session.CreatorDLCs, value)})
	}
	components := []interactionComponent{
		{Type: componentTypeLabel, Label: "Arma Launcher preset", Description: "Optional here. Upload .html/.htm to add or replace the Workshop modlist.", Component: &interactionComponent{Type: componentTypeFileUpload, CustomID: modsPresetCustomID, Required: &optional, MinValues: &minimumNone, MaxValues: &maximumOne}},
		{Type: componentTypeLabel, Label: "Workshop mod link", Description: "Optional public Arma 3 mod item or collection; do not also upload a client preset.", Component: &interactionComponent{Type: componentTypeTextInput, CustomID: modsWorkshopCustomID, Style: textInputStyleShort, Placeholder: "https://steamcommunity.com/sharedfiles/filedetails/?id=...", MaxLength: &maximumURL, Required: &optional}},
		{Type: componentTypeLabel, Label: "Server-only mod preset", Description: "Optional. These Workshop mods load only on the server and are not shared with players.", Component: &interactionComponent{Type: componentTypeFileUpload, CustomID: modsServerPresetCustomID, Required: &optional, MinValues: &minimumNone, MaxValues: &maximumOne}},
		{Type: componentTypeLabel, Label: "Creator DLC to load", Description: "Select every official Creator DLC required by the mission.", Component: &interactionComponent{Type: componentTypeCheckboxGroup, CustomID: modsCreatorDLCsCustomID, Required: &optional, MinValues: &minimumNone, MaxValues: &maximumDLCs, Options: options}},
	}
	writeJSON(writer, http.StatusOK, interactionResponse{Type: interactionResponseModal, Data: &interactionResponseData{CustomID: customID, Title: "Arma 3 mod options", Components: &components}})
	return nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (handler *Handler) submitModsModal(ctx context.Context, payload interactionPayload, actor domain.Actor, correlationID string) (string, error) {
	state, err := parseModsModalCustomID(payload.Data.CustomID)
	if err != nil || (len(payload.Data.Components) != 3 && len(payload.Data.Components) != 4) {
		return "", newUserError("The mod options form is malformed or expired. Run `/rb edit` with section `mods` again.")
	}
	var attachment, serverAttachment *interactionAttachment
	var workshopURL string
	var creatorDLCs []string
	seen := map[string]bool{}
	for _, label := range payload.Data.Components {
		if label.Type != componentTypeLabel || label.Component == nil || seen[label.Component.CustomID] {
			return "", newUserError("The mod options form is malformed or expired. Run `/rb edit` with section `mods` again.")
		}
		seen[label.Component.CustomID] = true
		switch label.Component.CustomID {
		case modsPresetCustomID:
			attachment, err = resolveModalAttachment(payload.Data, label.Component, false)
		case modsWorkshopCustomID:
			if label.Component.Type != componentTypeTextInput {
				err = fmt.Errorf("invalid Workshop link control")
			} else {
				workshopURL = strings.TrimSpace(label.Component.Value)
			}
		case modsServerPresetCustomID:
			serverAttachment, err = resolveModalAttachment(payload.Data, label.Component, false)
		case modsCreatorDLCsCustomID:
			if label.Component.Type != componentTypeCheckboxGroup {
				err = fmt.Errorf("invalid Creator DLC control")
			} else {
				creatorDLCs, err = domain.NormalizeCreatorDLCs(label.Component.Values)
			}
		default:
			err = fmt.Errorf("unsupported mod options field")
		}
		if err != nil {
			return "", newUserError("Choose only supported Creator DLCs and at most one preset file.")
		}
	}
	if !seen[modsPresetCustomID] || !seen[modsServerPresetCustomID] || !seen[modsCreatorDLCsCustomID] || (len(payload.Data.Components) == 4 && !seen[modsWorkshopCustomID]) {
		return "", newUserError("The mod options form is incomplete. Run `/rb edit` with section `mods` again.")
	}
	if attachment != nil && workshopURL != "" {
		return "", newUserError("Provide either a client preset upload or Workshop mod link, not both.")
	}
	keyPrefix := "discord:" + strings.TrimSpace(payload.ID) + ":mod-options"
	current, err := handler.service.Get(ctx, appsession.GetQuery{Actor: actor, SessionID: state.sessionID, GuildID: payload.GuildID})
	if err != nil {
		return "", err
	}
	if current.Version != state.version || current.EffectiveActivePresetRevision().Number != state.activeRevision || current.EffectiveActiveServerPresetRevision().Number != state.activeServerRevision {
		return "", newUserError("The session changed while the form was open. Run `/rb edit` with section `mods` again.")
	}
	clientRevision := current.LifecycleState != domain.StateDraft || state.activeRevision > 0
	serverRevision := current.LifecycleState != domain.StateDraft || state.activeServerRevision > 0
	if attachment != nil && clientRevision {
		if err := current.ValidatePresetRevisionStaging(state.activeRevision); err != nil {
			return "", modsStagingUserError(err)
		}
	}
	if serverAttachment != nil && serverRevision {
		if err := current.ValidateServerPresetRevisionStaging(state.activeServerRevision); err != nil {
			return "", modsStagingUserError(err)
		}
	}
	var presetRequest *domain.ArtifactIngestRequest
	var serverPresetRequest *domain.ArtifactIngestRequest
	if attachment != nil {
		request := createArtifactRequest(payload, actor, correlationID, state.sessionID, domain.ArtifactPreset, *attachment, keyPrefix+":preset", handler.clock.Now().UTC())
		request.ChannelID = current.ChannelID
		if clientRevision {
			request.Purpose = domain.ArtifactPurposePresetRevision
			request.ExpectedActivePresetRevision = state.activeRevision
		}
		if err := request.Validate(); err != nil {
			return "", newUserError("The preset upload must be an .html or .htm file no larger than 10 MiB from Discord.")
		}
		presetRequest = &request
	}
	if serverAttachment != nil {
		request := createArtifactRequest(payload, actor, correlationID, state.sessionID, domain.ArtifactServerPreset, *serverAttachment, keyPrefix+":server-preset", handler.clock.Now().UTC())
		request.ChannelID = current.ChannelID
		if serverRevision {
			request.Purpose = domain.ArtifactPurposeServerPresetRevision
			request.ExpectedActiveServerPresetRevision = state.activeServerRevision
		}
		if err := request.Validate(); err != nil {
			return "", newUserError("The server-only preset upload must be an .html or .htm file no larger than 10 MiB from Discord.")
		}
		serverPresetRequest = &request
	}
	session, err := handler.service.UpdateModOptions(ctx, appsession.UpdateModOptionsCommand{
		Actor: actor, SessionID: state.sessionID, GuildID: payload.GuildID, CorrelationID: correlationID,
		IdempotencyKey: keyPrefix + ":configure", ExpectedVersion: state.version, CreatorDLCs: creatorDLCs,
		PreparePreset:       presetRequest != nil && !clientRevision,
		PrepareServerPreset: serverPresetRequest != nil && !serverRevision,
		Roles:               interactionRoles(payload),
	})
	if err != nil {
		return "", modsStagingUserError(err)
	}
	presetStatus := "No preset was uploaded."
	if presetRequest != nil {
		if clientRevision {
			if err := session.ValidatePresetRevisionStaging(state.activeRevision); err != nil {
				return "", modsStagingUserError(err)
			}
		}
		if err := handler.service.RequestArtifactIngest(ctx, actor, *presetRequest); err != nil {
			return "", modsStagingUserError(err)
		}
		presetStatus = fmt.Sprintf("Preset `%s` queued for validation.", sanitizeCode(attachment.Filename))
	} else if workshopURL != "" {
		request := createWorkshopRequest(payload, actor, correlationID, state.sessionID, domain.WorkshopTargetMods, workshopURL, keyPrefix+":workshop", handler.clock.Now().UTC())
		request.ChannelID = current.ChannelID
		request.ExpectedActivePresetRevision = state.activeRevision
		if err := requestWorkshopResolve(ctx, handler.service, actor, request); err != nil {
			return "", modsStagingUserError(err)
		}
		presetStatus = "Workshop mod link queued for metadata validation."
	}
	if current.LifecycleState == domain.StateDraft && attachment == nil && serverAttachment == nil {
		presetStatus = "No preset was uploaded; this modded session remains a recoverable draft."
	} else if current.LifecycleState == domain.StateDraft && attachment == nil {
		presetStatus = "No client preset was uploaded; this server-only mod setup can become ready after validation."
	}
	serverPresetStatus := "No server-only preset was uploaded."
	if serverPresetRequest != nil {
		if serverRevision {
			if err := session.ValidateServerPresetRevisionStaging(state.activeServerRevision); err != nil {
				return "", modsStagingUserError(err)
			}
		}
		if err := handler.service.RequestArtifactIngest(ctx, actor, *serverPresetRequest); err != nil {
			return "", modsStagingUserError(err)
		}
		serverPresetStatus = fmt.Sprintf("Server-only preset `%s` queued for private validation.", sanitizeCode(serverAttachment.Filename))
	}
	return fmt.Sprintf("**Mod options saved**\nCreator DLC selected: %d\n%s\n%s\nNo running server was changed in place.\n\nNext: use `/rb status` to verify validation and readiness.", len(creatorDLCs), presetStatus, serverPresetStatus), nil
}

func modsStagingUserError(err error) error {
	switch {
	case strings.Contains(err.Error(), "vanilla session"):
		return newUserError("This is a vanilla session. Change it to modded in `/rb setup` before configuring mods.")
	case strings.Contains(err.Error(), "already") && strings.Contains(err.Error(), "preset revision"):
		return newUserError("A mod revision is already pending or being applied. Use `/rb status` before uploading another.")
	case strings.Contains(err.Error(), "changed while"), strings.Contains(err.Error(), "active preset revision changed"), strings.Contains(err.Error(), "lifecycle state"), strings.Contains(err.Error(), "active lifecycle operation"):
		return newUserError("The session changed while the form was open. Run `/rb edit` with section `mods` again after the current operation finishes.")
	default:
		return err
	}
}
