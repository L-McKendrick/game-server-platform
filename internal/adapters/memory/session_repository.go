package memory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// SessionRepository is an in-memory implementation for tests and local tools.
type SessionRepository struct {
	mu                 sync.RWMutex
	sessions           map[string]domain.Session
	events             map[string][]domain.SessionEvent
	idempotency        map[string]domain.IdempotencyRecord
	workflows          map[string]domain.Workflow
	capacity           map[string]string
	cards              map[string]domain.SessionCardReference
	cardControls       map[string]string
	modlists           map[string]domain.SessionModlistReference
	confirmations      map[string]domain.Confirmation
	reconciliation     map[string][]domain.ReconciliationFinding
	deadLetters        map[string]domain.DeadLetterOperation
	orphans            map[string]domain.OrphanFinding
	resetConfirmations map[string]domain.ResetConfirmation
	resetOperations    map[string]domain.ResetOperation
	activeResets       map[string]string
	latestResets       map[string]domain.ResetOperation
	serverConfigs      map[string]domain.GuildServerConfig
	lifecycleTimeouts  map[string]domain.GuildLifecycleTimeoutPolicy
	lifecycleAudits    map[string][]domain.LifecycleTimeoutPolicyAudit
}

var _ ports.SessionRepository = (*SessionRepository)(nil)
var _ ports.GuildSessionRepository = (*SessionRepository)(nil)
var _ ports.LifecycleTimeoutPolicyRepository = (*SessionRepository)(nil)
var _ ports.SessionSlugRepository = (*SessionRepository)(nil)
var _ ports.SessionCardRepository = (*SessionRepository)(nil)
var _ ports.SessionCardControlRepository = (*SessionRepository)(nil)

// NewSessionRepository creates an empty repository.
func NewSessionRepository() *SessionRepository {
	return &SessionRepository{
		sessions:           make(map[string]domain.Session),
		events:             make(map[string][]domain.SessionEvent),
		idempotency:        make(map[string]domain.IdempotencyRecord),
		workflows:          make(map[string]domain.Workflow),
		capacity:           make(map[string]string),
		cards:              make(map[string]domain.SessionCardReference),
		cardControls:       make(map[string]string),
		modlists:           make(map[string]domain.SessionModlistReference),
		confirmations:      make(map[string]domain.Confirmation),
		reconciliation:     make(map[string][]domain.ReconciliationFinding),
		deadLetters:        make(map[string]domain.DeadLetterOperation),
		orphans:            make(map[string]domain.OrphanFinding),
		resetConfirmations: make(map[string]domain.ResetConfirmation),
		resetOperations:    make(map[string]domain.ResetOperation),
		activeResets:       make(map[string]string),
		latestResets:       make(map[string]domain.ResetOperation),
		serverConfigs:      make(map[string]domain.GuildServerConfig),
		lifecycleTimeouts:  make(map[string]domain.GuildLifecycleTimeoutPolicy),
		lifecycleAudits:    make(map[string][]domain.LifecycleTimeoutPolicyAudit),
	}
}

func (repository *SessionRepository) GetLifecycleTimeoutPolicy(ctx context.Context, guildID string) (domain.GuildLifecycleTimeoutPolicy, error) {
	if err := ctx.Err(); err != nil {
		return domain.GuildLifecycleTimeoutPolicy{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	policy, found := repository.lifecycleTimeouts[guildID]
	if !found {
		return domain.GuildLifecycleTimeoutPolicy{}, domain.ErrNotFound
	}
	return policy, nil
}

func (repository *SessionRepository) SaveLifecycleTimeoutPolicy(ctx context.Context, policy domain.GuildLifecycleTimeoutPolicy, expectedVersion int64, audit domain.LifecycleTimeoutPolicyAudit, idempotency domain.IdempotencyRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if err := audit.Validate(); err != nil {
		return err
	}
	if err := idempotency.Validate(); err != nil {
		return err
	}
	if idempotency.ResultReference != policy.GuildID {
		return fmt.Errorf("lifecycle timeout idempotency result must reference guild")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, found := repository.lifecycleTimeouts[policy.GuildID]
	if stored, exists := repository.idempotency[idempotency.Key]; exists {
		if stored.RequestHash != idempotency.RequestHash {
			return domain.ErrIdempotencyConflict
		}
		return nil
	}
	if policy.Version < 1 || (!found && expectedVersion != 0) || (found && current.Version != expectedVersion) {
		return domain.ErrConflict
	}
	repository.lifecycleTimeouts[policy.GuildID] = policy
	repository.lifecycleAudits[policy.GuildID] = append(repository.lifecycleAudits[policy.GuildID], audit)
	repository.idempotency[idempotency.Key] = idempotency
	return nil
}

func (repository *SessionRepository) LifecycleTimeoutAudits(guildID string) []domain.LifecycleTimeoutPolicyAudit {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	return append([]domain.LifecycleTimeoutPolicyAudit(nil), repository.lifecycleAudits[guildID]...)
}

// Create atomically stores a session and its initial event.
func (repository *SessionRepository) Create(
	ctx context.Context,
	session domain.Session,
	event domain.SessionEvent,
	idempotency domain.IdempotencyRecord,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate session: %w", err)
	}

	if err := validateEvent(session.ID, event); err != nil {
		return err
	}

	if err := validateIdempotency(session.ID, idempotency); err != nil {
		return err
	}

	repository.mu.Lock()
	defer repository.mu.Unlock()

	if _, exists := repository.idempotency[idempotency.Key]; exists {
		return fmt.Errorf(
			"%w: idempotency key %s",
			domain.ErrAlreadyExists,
			idempotency.Key,
		)
	}

	if _, exists := repository.sessions[session.ID]; exists {
		return fmt.Errorf(
			"%w: session %s",
			domain.ErrAlreadyExists,
			session.ID,
		)
	}
	for _, existing := range repository.sessions {
		if existing.GuildID == session.GuildID && existing.Slug == session.Slug {
			return fmt.Errorf("%w: %s", domain.ErrSlugConflict, session.Slug)
		}
	}

	repository.sessions[session.ID] = session
	repository.events[session.ID] = []domain.SessionEvent{
		cloneEvent(event),
	}
	repository.idempotency[idempotency.Key] = idempotency

	return nil
}

// Get returns one session by ID.
func (repository *SessionRepository) Get(
	ctx context.Context,
	sessionID string,
) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}

	repository.mu.RLock()
	defer repository.mu.RUnlock()

	session, exists := repository.sessions[sessionID]
	if !exists {
		return domain.Session{}, fmt.Errorf(
			"%w: session %s",
			domain.ErrNotFound,
			sessionID,
		)
	}

	return session, nil
}

func (repository *SessionRepository) GetByGuildSlug(ctx context.Context, guildID, slug string) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	for _, session := range repository.sessions {
		if session.GuildID == guildID && session.Slug == slug {
			return session, nil
		}
	}
	return domain.Session{}, domain.ErrNotFound
}

// SaveCardReference stores replaceable delivery metadata independently of the
// session's optimistic lifecycle version.
func (repository *SessionRepository) GetCardReference(ctx context.Context, sessionID string) (domain.SessionCardReference, error) {
	if err := ctx.Err(); err != nil {
		return domain.SessionCardReference{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	reference, found := repository.cards[sessionID]
	if !found {
		return domain.SessionCardReference{}, fmt.Errorf("%w: session card %s", domain.ErrNotFound, sessionID)
	}
	return reference, nil
}

func (repository *SessionRepository) SaveCardReference(ctx context.Context, reference domain.SessionCardReference) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := reference.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	session, found := repository.sessions[reference.SessionID]
	if !found {
		return fmt.Errorf("%w: session %s", domain.ErrNotFound, reference.SessionID)
	}
	if session.ChannelID != reference.ChannelID {
		return fmt.Errorf("card channel does not match session channel: %w", domain.ErrForbidden)
	}
	token := domain.SessionCardControlToken(reference.SessionID)
	if claimedSessionID, exists := repository.cardControls[token]; exists && claimedSessionID != reference.SessionID {
		return fmt.Errorf("card control token is already claimed: %w", domain.ErrConflict)
	}
	repository.cards[reference.SessionID] = reference
	repository.cardControls[token] = reference.SessionID
	return nil
}

func (repository *SessionRepository) ResolveCardControl(ctx context.Context, guildID string, token string) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	sessionID, found := repository.cardControls[token]
	if !found {
		// Preserve compatibility with card references saved before the direct
		// token claim was introduced.
		for candidateID := range repository.sessions {
			if domain.SessionCardControlToken(candidateID) == token {
				sessionID, found = candidateID, true
				repository.cardControls[token] = candidateID
				break
			}
		}
	}
	session, exists := repository.sessions[sessionID]
	if !found || !exists || session.GuildID != guildID {
		return domain.Session{}, domain.ErrNotFound
	}
	return session, nil
}

func (repository *SessionRepository) GetModlistReference(ctx context.Context, sessionID string) (domain.SessionModlistReference, error) {
	if err := ctx.Err(); err != nil {
		return domain.SessionModlistReference{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	reference, found := repository.modlists[sessionID]
	if !found {
		return domain.SessionModlistReference{}, fmt.Errorf("%w: session modlist %s", domain.ErrNotFound, sessionID)
	}
	return reference, nil
}

func (repository *SessionRepository) SaveModlistReference(ctx context.Context, reference domain.SessionModlistReference) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := reference.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	session, found := repository.sessions[reference.SessionID]
	if !found {
		return fmt.Errorf("%w: session %s", domain.ErrNotFound, reference.SessionID)
	}
	if session.ChannelID != reference.ChannelID {
		return fmt.Errorf("modlist channel does not match session channel: %w", domain.ErrForbidden)
	}
	repository.modlists[reference.SessionID] = reference
	return nil
}

// SaveWithEvent updates a session using optimistic concurrency.
func (repository *SessionRepository) SaveWithEvent(
	ctx context.Context,
	session domain.Session,
	expectedVersion int64,
	event domain.SessionEvent,
	idempotency domain.IdempotencyRecord,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate session: %w", err)
	}

	if err := validateEvent(session.ID, event); err != nil {
		return err
	}

	if err := validateIdempotency(session.ID, idempotency); err != nil {
		return err
	}

	if session.Version != expectedVersion+1 {
		return fmt.Errorf(
			"session version %d must equal expected version %d plus one",
			session.Version,
			expectedVersion,
		)
	}

	repository.mu.Lock()
	defer repository.mu.Unlock()

	if _, exists := repository.idempotency[idempotency.Key]; exists {
		return fmt.Errorf(
			"%w: idempotency key %s",
			domain.ErrConflict,
			idempotency.Key,
		)
	}

	current, exists := repository.sessions[session.ID]
	if !exists {
		return fmt.Errorf(
			"%w: session %s",
			domain.ErrNotFound,
			session.ID,
		)
	}

	if current.Version != expectedVersion {
		return fmt.Errorf(
			"%w: session %s expected version %d but found %d",
			domain.ErrConflict,
			session.ID,
			expectedVersion,
			current.Version,
		)
	}

	repository.sessions[session.ID] = session
	repository.events[session.ID] = append(
		repository.events[session.ID],
		cloneEvent(event),
	)
	repository.idempotency[idempotency.Key] = idempotency

	return nil
}

// GetIdempotency returns a durable command result by external key.
func (repository *SessionRepository) GetIdempotency(
	ctx context.Context,
	key string,
) (domain.IdempotencyRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.IdempotencyRecord{}, err
	}

	repository.mu.RLock()
	defer repository.mu.RUnlock()

	record, exists := repository.idempotency[key]
	if !exists {
		return domain.IdempotencyRecord{}, fmt.Errorf(
			"%w: idempotency key %s",
			domain.ErrNotFound,
			key,
		)
	}

	return record, nil
}

// ListByOwner returns an owner's sessions, newest first.
func (repository *SessionRepository) ListByOwner(
	ctx context.Context,
	ownerDiscordUserID string,
	limit int32,
) ([]domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 25
	}

	if limit > 100 {
		limit = 100
	}

	repository.mu.RLock()
	defer repository.mu.RUnlock()

	sessions := make([]domain.Session, 0)

	for _, session := range repository.sessions {
		if session.OwnerDiscordUserID == ownerDiscordUserID {
			sessions = append(sessions, session)
		}
	}

	sort.Slice(
		sessions,
		func(first, second int) bool {
			if sessions[first].UpdatedAt.Equal(
				sessions[second].UpdatedAt,
			) {
				return sessions[first].ID > sessions[second].ID
			}

			return sessions[first].UpdatedAt.After(
				sessions[second].UpdatedAt,
			)
		},
	)

	if len(sessions) > int(limit) {
		sessions = sessions[:limit]
	}

	return sessions, nil
}

type guildSessionMemoryCursor struct {
	GuildID string                  `json:"guild_id"`
	States  []domain.LifecycleState `json:"states"`
	Offset  int                     `json:"offset"`
}

func (repository *SessionRepository) ListGuildSessions(ctx context.Context, guildID string, states []domain.LifecycleState, page ports.GuildSessionPage) (ports.GuildSessionPageResult, error) {
	if err := ctx.Err(); err != nil {
		return ports.GuildSessionPageResult{}, err
	}
	normalized, err := normalizeMemoryGuildStates(states)
	if err != nil {
		return ports.GuildSessionPageResult{}, err
	}
	guildID = strings.TrimSpace(guildID)
	if guildID == "" {
		return ports.GuildSessionPageResult{}, fmt.Errorf("guild ID is required")
	}
	size := page.Size
	if size <= 0 {
		size = 25
	}
	if size > 100 {
		return ports.GuildSessionPageResult{}, fmt.Errorf("page size must not exceed 100")
	}
	offset := 0
	if page.Cursor != "" {
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(page.Cursor)
		if decodeErr != nil {
			return ports.GuildSessionPageResult{}, fmt.Errorf("decode guild session cursor: %w", decodeErr)
		}
		var cursor guildSessionMemoryCursor
		if json.Unmarshal(decoded, &cursor) != nil || cursor.GuildID != guildID || !slices.Equal(cursor.States, normalized) || cursor.Offset < 0 {
			return ports.GuildSessionPageResult{}, fmt.Errorf("guild session cursor does not match request")
		}
		offset = cursor.Offset
	}
	wanted := make(map[domain.LifecycleState]struct{}, len(normalized))
	for _, state := range normalized {
		wanted[state] = struct{}{}
	}
	repository.mu.RLock()
	matching := make([]domain.Session, 0)
	for _, session := range repository.sessions {
		if session.GuildID == guildID {
			if _, ok := wanted[session.LifecycleState]; ok {
				matching = append(matching, session)
			}
		}
	}
	repository.mu.RUnlock()
	sort.Slice(matching, func(i, j int) bool {
		if matching[i].UpdatedAt.Equal(matching[j].UpdatedAt) {
			return matching[i].ID > matching[j].ID
		}
		return matching[i].UpdatedAt.After(matching[j].UpdatedAt)
	})
	if offset > len(matching) {
		return ports.GuildSessionPageResult{}, fmt.Errorf("guild session cursor is beyond available results")
	}
	end := offset + int(size)
	if end > len(matching) {
		end = len(matching)
	}
	result := ports.GuildSessionPageResult{Sessions: append([]domain.Session(nil), matching[offset:end]...)}
	if end < len(matching) {
		encoded, _ := json.Marshal(guildSessionMemoryCursor{GuildID: guildID, States: normalized, Offset: end})
		result.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return result, nil
}

func normalizeMemoryGuildStates(states []domain.LifecycleState) ([]domain.LifecycleState, error) {
	if len(states) == 0 {
		return nil, fmt.Errorf("at least one lifecycle state is required")
	}
	seen := make(map[domain.LifecycleState]struct{}, len(states))
	result := make([]domain.LifecycleState, 0, len(states))
	for _, state := range states {
		if !state.Valid() {
			return nil, fmt.Errorf("invalid lifecycle state %q", state)
		}
		if _, ok := seen[state]; !ok {
			seen[state] = struct{}{}
			result = append(result, state)
		}
	}
	slices.Sort(result)
	return result, nil
}

// Events returns a copy of the events stored for a session.
//
// This method is intentionally not part of the production repository
// interface. It exists to support application-layer assertions.
func (repository *SessionRepository) Events(
	sessionID string,
) []domain.SessionEvent {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	stored := repository.events[sessionID]
	events := make([]domain.SessionEvent, 0, len(stored))

	for _, event := range stored {
		events = append(events, cloneEvent(event))
	}

	return events
}

func validateEvent(
	sessionID string,
	event domain.SessionEvent,
) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate event: %w", err)
	}

	if event.SessionID != sessionID {
		return fmt.Errorf(
			"event session ID %q does not match session %q",
			event.SessionID,
			sessionID,
		)
	}

	return nil
}

func validateIdempotency(
	sessionID string,
	record domain.IdempotencyRecord,
) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("validate idempotency record: %w", err)
	}

	if record.Status != domain.IdempotencyCompleted {
		return fmt.Errorf("metadata mutation requires a completed idempotency record")
	}

	if record.ResultReference != sessionID {
		return fmt.Errorf(
			"idempotency result reference %q does not match session %q",
			record.ResultReference,
			sessionID,
		)
	}

	return nil
}

func cloneEvent(
	event domain.SessionEvent,
) domain.SessionEvent {
	cloned := event

	if event.Data != nil {
		cloned.Data = make(map[string]string, len(event.Data))

		for key, value := range event.Data {
			cloned.Data[key] = value
		}
	}

	return cloned
}
