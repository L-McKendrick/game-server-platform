package interactions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/componentid"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const cardRefreshWindow = time.Minute

const (
	detailedPlayerQueryTimeout = 2500 * time.Millisecond
	refreshPlayerQueryTimeout  = 750 * time.Millisecond
)

func (handler *Handler) handleSessionCardControl(ctx context.Context, payload interactionPayload, actor domain.Actor) (string, error) {
	if payload.Data == nil || payload.Data.ComponentType != componentTypeButton {
		return "", newUserError("This component is not supported or has expired.")
	}
	reference, err := parseComponentCustomID(payload.Data.CustomID)
	if err != nil || !supportedCardControl(reference.Action) || !sessioncard.ValidControlToken(reference.Token) {
		return "", newUserError("This component is not supported or has expired.")
	}
	session, err := handler.service.ResolveCardControl(ctx, appsession.CardControlQuery{
		Actor: actor, GuildID: payload.GuildID, Token: reference.Token,
	})
	if err != nil {
		return "", err
	}
	if session.ChannelID != strings.TrimSpace(payload.ChannelID) {
		return "", domain.ErrForbidden
	}
	currentRevision := uint64(session.Version)
	if reference.Action == componentid.ActionRefresh && reference.Revision > currentRevision {
		return "", newUserError("This card revision is no longer valid. Use `/rb status`.")
	}
	if reference.Action != componentid.ActionRefresh && reference.Revision != currentRevision {
		return "", newUserError("This card changed after the button was shown. Use `Refresh` on the latest card or `/rb status`.")
	}

	switch reference.Action {
	case componentid.ActionViewDetails:
		return handler.renderDetailedSession(ctx, session), nil
	case componentid.ActionRefresh:
		return handler.refreshSessionCard(ctx, payload, actor, session, reference.Revision)
	case componentid.ActionDownload:
		return handler.activeModlistLink(ctx, actor, session)
	case componentid.ActionHelp:
		return contextualCardHelp(session), nil
	default:
		return "", newUserError("This component is not supported or has expired.")
	}
}

func supportedCardControl(action string) bool {
	switch action {
	case componentid.ActionViewDetails, componentid.ActionRefresh, componentid.ActionDownload, componentid.ActionHelp:
		return true
	default:
		return false
	}
}

func (handler *Handler) refreshSessionCard(ctx context.Context, payload interactionPayload, actor domain.Actor, session domain.Session, requestedRevision uint64) (string, error) {
	now := handler.clock.Now().UTC()
	window := now.Truncate(cardRefreshWindow)
	options := sessioncard.Options{Now: window}
	if players := handler.querySessionPlayers(ctx, session, refreshPlayerQueryTimeout); players != nil {
		options.Players, options.PlayersObservedAt = players, now
	}
	correlationID, err := handler.ids.New(now)
	if err != nil {
		return "", fmt.Errorf("generate card refresh correlation ID: %w", err)
	}
	notificationID := fmt.Sprintf("card-refresh-%s-%d-%d", session.ID, session.Version, window.Unix())
	err = handler.service.RequestSessionCard(ctx, appsession.SessionCardCommand{
		Actor: actor, SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		CorrelationID: correlationID, NotificationID: notificationID,
		Content: sessioncard.RenderPublic(sessioncard.Project(session, options)), CardRevision: session.Version,
		AllowGuildMember: true, RequireCurrentRevision: true,
	})
	if err != nil && !errors.Is(err, domain.ErrIdempotencyConflict) {
		return "", fmt.Errorf("refresh session card: %w", err)
	}
	if errors.Is(err, domain.ErrIdempotencyConflict) {
		return "**Card refresh already queued**\nA refresh for the latest revision is already queued in this one-minute window.", nil
	}
	if requestedRevision != uint64(session.Version) {
		return "**Card refresh queued**\nThe session changed after this button was shown, so the latest persisted revision was queued instead.", nil
	}
	return "**Card refresh queued**\nThe latest persisted status and a bounded live-player check will update the public card.", nil
}

func (handler *Handler) activeModlistLink(ctx context.Context, actor domain.Actor, session domain.Session) (string, error) {
	reference, err := handler.service.GetActiveModlist(ctx, appsession.ActiveModlistQuery{
		Actor: actor, SessionID: session.ID, GuildID: session.GuildID, ExpectedRevision: session.Version,
	})
	if errors.Is(err, domain.ErrNotFound) {
		return "No active downloadable modlist is available for this session.", nil
	}
	if err != nil {
		return "", fmt.Errorf("get active session modlist: %w", err)
	}
	messageURL := sessioncard.DiscordMessageURL(session.GuildID, reference.ChannelID, reference.MessageID)
	if messageURL == "" {
		return "", fmt.Errorf("active modlist message reference is invalid")
	}
	return fmt.Sprintf("**Active modlist**\n[Open the stable modlist message](%s), then download `%s` and import it in Arma 3 Launcher.", messageURL, sanitizeCode(reference.Filename)), nil
}

func (handler *Handler) renderDetailedSession(ctx context.Context, session domain.Session) string {
	return renderSessionStatusAt(session, handler.querySessionPlayers(ctx, session, detailedPlayerQueryTimeout), handler.clock.Now().UTC())
}

func (handler *Handler) querySessionPlayers(ctx context.Context, session domain.Session, timeout time.Duration) *domain.PlayerStatus {
	if session.LifecycleState != domain.StateRunning && session.LifecycleState != domain.StateIdle {
		return nil
	}
	if strings.TrimSpace(session.Infrastructure.PublicIPv4) == "" || handler.playerQuery == nil {
		return nil
	}
	queryContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	players, err := handler.playerQuery.Query(queryContext, session.Infrastructure.PublicIPv4)
	if err != nil {
		handler.logger.Warn("live player query unavailable", slog.String("session_id", session.ID), slog.String("reason", "A2S query failed"))
		return nil
	}
	return &players
}

func contextualCardHelp(session domain.Session) string {
	next := "Use `/rb status` for current details."
	switch session.LifecycleState {
	case domain.StateDraft:
		next = "Use `/rb setup` to finish or repair configuration."
	case domain.StateNew, domain.StateReady:
		next = "Use `/rb start` when you are ready to allocate infrastructure."
	case domain.StateProvisioning, domain.StateBootstrapping, domain.StateInstalling, domain.StateWaking, domain.StateRestoring:
		next = "Setup is still running. Use `/rb status` to follow the current stage."
	case domain.StateRunning, domain.StateIdle:
		next = "Use `/rb status` for live details or `/rb sleep` when the server is no longer needed."
	case domain.StateStopping, domain.StateSleeping, domain.StateWarning1, domain.StateWarning2:
		next = "Use `/rb wake` to bring the retained server back online."
	case domain.StateArchived:
		next = "Use `/rb restore` to create replacement infrastructure from the verified archive."
	case domain.StateFailed:
		next = "Use `/rb status` for the latest safe failure details before choosing a recovery action."
	case domain.StateDeleting, domain.StateDeleted:
		next = "This session is terminated and has no lifecycle action available."
	}
	return fmt.Sprintf("**Help for %s**\n%s\n\nCard controls are read-only; lifecycle changes remain explicit `/rb` commands.", sanitizeInline(session.DisplayName), next)
}

func componentErrorMessage(err error) string {
	var userErr userError
	if errors.As(err, &userErr) {
		return userErr.message
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return "This session or card is no longer available. Use `/rb list` to find an active session."
	case errors.Is(err, domain.ErrForbidden):
		return "You do not have access to this session card."
	case errors.Is(err, domain.ErrConflict):
		return "This card changed while the control was being processed. Try `Refresh` again."
	default:
		return "This control could not be completed. Use `/rb status` and try again."
	}
}
