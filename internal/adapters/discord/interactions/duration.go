package interactions

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type durationService interface {
	ConfigureMaximumDuration(context.Context, appsession.DurationCommand) (domain.Session, error)
	ExtendMaximumDuration(context.Context, appsession.DurationCommand) (domain.Session, error)
}

type lifecycleTimeoutQueryService interface {
	LifecycleTimeoutDefaults(context.Context, string) (domain.GuildLifecycleTimeoutPolicy, error)
}

type lifecycleTimeoutMutationService interface {
	lifecycleTimeoutQueryService
	ConfigureLifecycleTimeoutDefaults(context.Context, appsession.LifecycleTimeoutDefaultsCommand) (domain.GuildLifecycleTimeoutPolicy, error)
	ExtendLifecycleTimeouts(context.Context, appsession.LifecycleTimeoutExtensionCommand) (domain.Session, error)
}

func timeoutText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func (handler *Handler) writeAdminTimeoutView(ctx context.Context, writer http.ResponseWriter, guildID, actorID string) error {
	service, ok := handler.service.(lifecycleTimeoutQueryService)
	if !ok {
		return domain.ErrFeatureDisabled
	}
	policy, err := service.LifecycleTimeoutDefaults(ctx, guildID)
	if err != nil {
		return err
	}
	content := fmt.Sprintf("**Session timeouts**\nFuture sessions sleep after **%d minutes** without players and archive after **%d days** asleep.", policy.SleepAfterSeconds/60, policy.ArchiveAfterSeconds/86400)
	controls := []interactionComponent{{Type: componentTypeActionRow, Components: []interactionComponent{{Type: componentTypeButton, Style: buttonStylePrimary, Label: "Edit future sessions", CustomID: adminTimeoutDefaultsID}}}}
	selections := make([]appsession.Selection, 0, 25)
	for _, state := range []string{string(domain.StateRunning), string(domain.StateIdle)} {
		matches, selectErr := handler.service.Select(ctx, appsession.SelectQuery{Actor: domain.Actor{Type: domain.ActorTypeDiscordUser, ID: actorID}, GuildID: guildID, Search: state, Limit: 25, AllowGuildMember: true})
		if selectErr != nil {
			return selectErr
		}
		for _, selection := range matches {
			if len(selections) < 25 {
				selections = append(selections, selection)
			}
		}
	}
	if len(selections) > 0 {
		options := make([]interactionSelectOption, 0, len(selections))
		for _, selection := range selections {
			options = append(options, interactionSelectOption{Label: timeoutText(selection.DisplayName, 100), Value: selection.ID, Description: timeoutText(selection.Slug+" · "+string(selection.LifecycleState), 100)})
		}
		minimum, maximum := 1, 1
		controls = append(controls, interactionComponent{Type: componentTypeActionRow, Components: []interactionComponent{{Type: componentTypeStringSelect, CustomID: adminTimeoutSessionID, Placeholder: "Extend an active session", MinValues: &minimum, MaxValues: &maximum, Options: options}}})
	} else {
		content += "\n\nNo running or idle sessions can be extended."
	}
	handler.writeAdminView(writer, interactionResponseUpdateMessage, content, adminMenuDuration, controls, true)
	return nil
}

func writeTimeoutDefaultsModal(writer http.ResponseWriter, policy domain.GuildLifecycleTimeoutPolicy) {
	required := true
	minimum, maximum, reasonMaximum := 1, 5, 200
	components := []interactionComponent{
		{Type: componentTypeLabel, Label: "Sleep after no players (minutes)", Description: fmt.Sprintf("Current default: %d minutes", policy.SleepAfterSeconds/60), Component: &interactionComponent{Type: componentTypeTextInput, CustomID: adminTimeoutSleepID, Style: textInputStyleShort, Value: strconv.FormatInt(policy.SleepAfterSeconds/60, 10), MinLength: &minimum, MaxLength: &maximum, Required: &required}},
		{Type: componentTypeLabel, Label: "Archive after sleeping (days)", Description: fmt.Sprintf("Current default: %d days", policy.ArchiveAfterSeconds/86400), Component: &interactionComponent{Type: componentTypeTextInput, CustomID: adminTimeoutArchiveID, Style: textInputStyleShort, Value: strconv.FormatInt(policy.ArchiveAfterSeconds/86400, 10), MinLength: &minimum, MaxLength: &maximum, Required: &required}},
		{Type: componentTypeLabel, Label: "Reason", Description: "Saved in the audit history.", Component: &interactionComponent{Type: componentTypeTextInput, CustomID: adminTimeoutReasonID, Style: textInputStyleParagraph, MinLength: &minimum, MaxLength: &reasonMaximum, Required: &required}},
	}
	writeJSON(writer, http.StatusOK, interactionResponse{Type: interactionResponseModal, Data: &interactionResponseData{CustomID: adminTimeoutDefaultsID, Title: "Future session timeouts", Components: &components}})
}

func writeTimeoutExtensionModal(writer http.ResponseWriter, session domain.Session) {
	required := true
	minimum, maximum, reasonMaximum := 1, 5, 200
	components := []interactionComponent{
		{Type: componentTypeLabel, Label: "Add no-player time (minutes)", Description: fmt.Sprintf("Current: %d minutes", session.SleepAfterSeconds/60), Component: &interactionComponent{Type: componentTypeTextInput, CustomID: adminTimeoutSleepID, Style: textInputStyleShort, Placeholder: "0", MinLength: &minimum, MaxLength: &maximum, Required: &required}},
		{Type: componentTypeLabel, Label: "Add sleeping time (days)", Description: fmt.Sprintf("Current: %d days", session.ArchiveAfterSeconds/86400), Component: &interactionComponent{Type: componentTypeTextInput, CustomID: adminTimeoutArchiveID, Style: textInputStyleShort, Placeholder: "0", MinLength: &minimum, MaxLength: &maximum, Required: &required}},
		{Type: componentTypeLabel, Label: "Reason", Description: "Saved in the audit history.", Component: &interactionComponent{Type: componentTypeTextInput, CustomID: adminTimeoutReasonID, Style: textInputStyleParagraph, MinLength: &minimum, MaxLength: &reasonMaximum, Required: &required}},
	}
	writeJSON(writer, http.StatusOK, interactionResponse{Type: interactionResponseModal, Data: &interactionResponseData{CustomID: adminTimeoutExtendPrefix + session.ID, Title: "Add time to " + timeoutText(session.DisplayName, 28), Components: &components}})
}

func timeoutModalValues(payload interactionPayload) (map[string]string, error) {
	if payload.Type != interactionTypeModalSubmit || payload.Data == nil || len(payload.Data.Components) != 3 {
		return nil, newUserError("This timeout form is invalid. Reopen `/rb admin`.")
	}
	values := make(map[string]string, 3)
	for _, label := range payload.Data.Components {
		if label.Type != componentTypeLabel || label.Component == nil || label.Component.Type != componentTypeTextInput {
			return nil, newUserError("This timeout form is invalid. Reopen `/rb admin`.")
		}
		input := label.Component
		if _, exists := values[input.CustomID]; exists {
			return nil, newUserError("This timeout form is invalid. Reopen `/rb admin`.")
		}
		values[input.CustomID] = strings.TrimSpace(input.Value)
	}
	if values[adminTimeoutSleepID] == "" || values[adminTimeoutArchiveID] == "" || values[adminTimeoutReasonID] == "" || len([]rune(values[adminTimeoutReasonID])) > 200 {
		return nil, newUserError("Enter both timeout values and a reason of at most 200 characters.")
	}
	return values, nil
}

func (handler *Handler) submitTimeoutModal(ctx context.Context, writer http.ResponseWriter, payload interactionPayload, actorID, correlationID string) error {
	service, ok := handler.service.(lifecycleTimeoutMutationService)
	if !ok {
		return domain.ErrFeatureDisabled
	}
	values, err := timeoutModalValues(payload)
	if err != nil {
		return err
	}
	sleepValue, sleepErr := strconv.ParseInt(values[adminTimeoutSleepID], 10, 64)
	archiveValue, archiveErr := strconv.ParseInt(values[adminTimeoutArchiveID], 10, 64)
	if sleepErr != nil || archiveErr != nil {
		return newUserError("Use whole numbers for minutes and days.")
	}
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: actorID}
	if payload.Data.CustomID == adminTimeoutDefaultsID {
		if sleepValue < 10 || sleepValue > 1440 || archiveValue < 1 || archiveValue > 90 {
			return newUserError("Sleep must be 10–1,440 minutes. Archive must be 1–90 days.")
		}
		policy, err := service.ConfigureLifecycleTimeoutDefaults(ctx, appsession.LifecycleTimeoutDefaultsCommand{Actor: actor, GuildID: payload.GuildID, CorrelationID: correlationID, IdempotencyKey: "discord:timeout-defaults:" + payload.ID, Reason: values[adminTimeoutReasonID], IsAdministrator: payload.memberIsAdministrator(), SleepAfterSeconds: sleepValue * 60, ArchiveAfterSeconds: archiveValue * 86400})
		if err != nil {
			return err
		}
		writeInteractionMessage(writer, fmt.Sprintf("**Future session timeouts updated**\nSleep after **%d minutes** without players.\nArchive after **%d days** asleep.", policy.SleepAfterSeconds/60, policy.ArchiveAfterSeconds/86400))
		return nil
	}
	if sleepValue < 1 || sleepValue > 1440 || archiveValue < 1 || archiveValue > 90 {
		return newUserError("Add 1–1,440 minutes and 1–90 days. The resulting settings must stay within the allowed limits.")
	}
	sessionID := strings.TrimPrefix(payload.Data.CustomID, adminTimeoutExtendPrefix)
	if sessionID == "" {
		return newUserError("This timeout form is invalid. Reopen `/rb admin`.")
	}
	session, err := service.ExtendLifecycleTimeouts(ctx, appsession.LifecycleTimeoutExtensionCommand{Actor: actor, GuildID: payload.GuildID, SessionID: sessionID, CorrelationID: correlationID, IdempotencyKey: "discord:timeout-extension:" + payload.ID, Reason: values[adminTimeoutReasonID], IsAdministrator: payload.memberIsAdministrator(), SleepExtensionSeconds: sleepValue * 60, ArchiveExtensionSeconds: archiveValue * 86400})
	if err != nil {
		return err
	}
	writeInteractionMessage(writer, fmt.Sprintf("**Session timeouts extended**\nSession: `%s`\nSleep after **%d minutes** without players.\nArchive after **%d days** asleep.", sanitizeInline(session.Slug), session.SleepAfterSeconds/60, session.ArchiveAfterSeconds/86400))
	return nil
}

func writeDurationModal(writer http.ResponseWriter, action string) {
	required := true
	minimum, maximum := 1, 100
	maxHours := 3
	maxReason := 200
	label := "Maximum hours (1-168)"
	title := "Configure maximum duration"
	if action == adminDurationExtendID {
		label, title = "Additional hours (1-168)", "Extend session deadline"
	}
	components := []interactionComponent{
		{Type: componentTypeActionRow, Components: []interactionComponent{{Type: componentTypeTextInput, CustomID: adminDurationSessionID, Style: textInputStyleShort, Label: "Exact session slug or ID", Required: &required, MinLength: &minimum, MaxLength: &maximum}}},
		{Type: componentTypeActionRow, Components: []interactionComponent{{Type: componentTypeTextInput, CustomID: adminDurationHoursID, Style: textInputStyleShort, Label: label, Required: &required, MinLength: &minimum, MaxLength: &maxHours}}},
		{Type: componentTypeActionRow, Components: []interactionComponent{{Type: componentTypeTextInput, CustomID: adminDurationReasonID, Style: textInputStyleParagraph, Label: "Reason (audited)", Required: &required, MinLength: &minimum, MaxLength: &maxReason}}},
	}
	writeJSON(writer, http.StatusOK, interactionResponse{Type: interactionResponseModal, Data: &interactionResponseData{CustomID: action, Title: title, Components: &components}})
}

func durationModalValues(payload interactionPayload) (map[string]string, error) {
	if payload.Type != interactionTypeModalSubmit || payload.Data == nil || len(payload.Data.Components) != 3 {
		return nil, newUserError("The duration form is malformed. Reopen `/rb admin`.")
	}
	values := map[string]string{}
	for _, row := range payload.Data.Components {
		if row.Type != componentTypeActionRow || len(row.Components) != 1 || row.Components[0].Type != componentTypeTextInput {
			return nil, newUserError("The duration form is malformed. Reopen `/rb admin`.")
		}
		input := row.Components[0]
		if _, exists := values[input.CustomID]; exists {
			return nil, newUserError("The duration form is malformed. Reopen `/rb admin`.")
		}
		values[input.CustomID] = strings.TrimSpace(input.Value)
	}
	if len(values[adminDurationSessionID]) == 0 || len(values[adminDurationSessionID]) > 100 || len(values[adminDurationHoursID]) == 0 || len(values[adminDurationReasonID]) == 0 || len([]rune(values[adminDurationReasonID])) > 200 {
		return nil, newUserError("Enter an exact session, whole hours, and a reason of at most 200 characters.")
	}
	return values, nil
}

func (handler *Handler) submitDurationModal(ctx context.Context, writer http.ResponseWriter, payload interactionPayload, actorID, correlationID string) error {
	service, ok := handler.service.(durationService)
	if !ok {
		return domain.ErrFeatureDisabled
	}
	values, err := durationModalValues(payload)
	if err != nil {
		return err
	}
	hours, err := strconv.ParseInt(values[adminDurationHoursID], 10, 64)
	if err != nil || hours < 1 || hours > 168 {
		return newUserError("Enter whole hours from 1 through 168.")
	}
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: actorID}
	selection, err := handler.service.Resolve(ctx, appsession.ResolveQuery{Actor: actor, GuildID: payload.GuildID, Reference: values[adminDurationSessionID], CanManageGuild: true, AllowGuildMember: true})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return newUserError("Session not found in this server. Enter its exact slug or ID and try again.")
		}
		return err
	}
	command := appsession.DurationCommand{Actor: actor, GuildID: payload.GuildID, SessionID: selection.ID, CorrelationID: correlationID, IdempotencyKey: "discord:duration:" + payload.ID, Reason: values[adminDurationReasonID], IsAdministrator: payload.memberIsAdministrator()}
	var session domain.Session
	if payload.Data.CustomID == adminDurationExtendID {
		command.ExtensionSeconds = hours * 3600
		session, err = service.ExtendMaximumDuration(ctx, command)
	} else {
		command.Seconds = hours * 3600
		session, err = service.ConfigureMaximumDuration(ctx, command)
	}
	if err != nil {
		return err
	}
	content := fmt.Sprintf("**Maximum duration updated**\nSession: `%s`\nLimit: %d hours", sanitizeInline(session.Slug), session.MaximumDuration.EffectiveSeconds()/3600)
	if !session.MaximumDuration.DeadlineAt.IsZero() {
		content += "\nDeadline: <t:" + strconv.FormatInt(session.MaximumDuration.DeadlineAt.Unix(), 10) + ":F>"
	}
	writeInteractionMessage(writer, content)
	return nil
}
