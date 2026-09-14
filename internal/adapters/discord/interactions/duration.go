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

type durationService interface {
	ConfigureMaximumDuration(context.Context, appsession.DurationCommand) (domain.Session, error)
	ExtendMaximumDuration(context.Context, appsession.DurationCommand) (domain.Session, error)
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
