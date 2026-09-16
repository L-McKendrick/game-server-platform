package dynamodbstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const (
	guildSessionIndexName = "gsi2"
	guildSessionIndexPK   = "SYSTEM#GUILD_SESSION_INDEX"
	guildSessionIndexSK   = "CUTOVER"
)

var _ ports.GuildSessionRepository = (*Repository)(nil)

type guildSessionCursor struct {
	Version   int               `json:"version"`
	Mode      string            `json:"mode"`
	GuildID   string            `json:"guild_id"`
	States    []string          `json:"states"`
	Positions map[string]string `json:"positions,omitempty"`
	Done      map[string]bool   `json:"done,omitempty"`
}

type guildSessionCompatibilityCursor struct {
	Version   int      `json:"version"`
	Mode      string   `json:"mode"`
	GuildID   string   `json:"guild_id"`
	States    []string `json:"states"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	SessionID string   `json:"session_id,omitempty"`
}

type indexedSession struct {
	Session domain.Session
	State   domain.LifecycleState
	SortKey string
}

// ListGuildSessions queries explicit state ranges from the verified sparse
// index. The result is candidate discovery only; callers must Get before a
// mutation to revalidate authoritative state and version.
func (repository *Repository) ListGuildSessions(ctx context.Context, guildID string, states []domain.LifecycleState, page ports.GuildSessionPage) (ports.GuildSessionPageResult, error) {
	if err := repository.validate(); err != nil {
		return ports.GuildSessionPageResult{}, err
	}
	guildID = strings.TrimSpace(guildID)
	if guildID == "" {
		return ports.GuildSessionPageResult{}, fmt.Errorf("guild ID is required")
	}
	normalized, err := normalizeGuildSessionStates(states)
	if err != nil {
		return ports.GuildSessionPageResult{}, err
	}
	size := page.Size
	if size <= 0 {
		size = 25
	}
	if size > 100 {
		return ports.GuildSessionPageResult{}, fmt.Errorf("page size must not exceed 100")
	}
	mode, err := guildSessionCursorMode(page.Cursor)
	if err != nil {
		return ports.GuildSessionPageResult{}, err
	}
	if mode == "scan" {
		return repository.listGuildSessionsCompatibility(ctx, guildID, normalized, size, page.Cursor)
	}
	ready, err := repository.guildSessionIndexReady(ctx)
	if err != nil {
		return ports.GuildSessionPageResult{}, err
	}
	if !ready {
		return repository.listGuildSessionsCompatibility(ctx, guildID, normalized, size, page.Cursor)
	}
	cursor, err := decodeGuildSessionCursor(page.Cursor, guildID, normalized)
	if err != nil {
		return ports.GuildSessionPageResult{}, err
	}

	streams := make(map[domain.LifecycleState][]indexedSession, len(normalized))
	lastKeys := make(map[domain.LifecycleState]map[string]types.AttributeValue, len(normalized))
	for _, state := range normalized {
		if cursor.Done[string(state)] {
			continue
		}
		input := &dynamodb.QueryInput{
			TableName:              aws.String(repository.tableName),
			IndexName:              aws.String(guildSessionIndexName),
			Limit:                  aws.Int32(size),
			ScanIndexForward:       aws.Bool(false),
			KeyConditionExpression: aws.String("gsi2pk = :guild AND begins_with(gsi2sk, :state)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":guild": &types.AttributeValueMemberS{Value: "GUILD#" + guildID},
				":state": &types.AttributeValueMemberS{Value: "STATE#" + string(state) + "#"},
			},
		}
		if position := cursor.Positions[string(state)]; position != "" {
			input.ExclusiveStartKey = guildSessionExclusiveStartKey(guildID, position)
		}
		output, queryErr := repository.client.Query(ctx, input)
		if queryErr != nil {
			return ports.GuildSessionPageResult{}, fmt.Errorf("query guild sessions in state %s: %w", state, queryErr)
		}
		lastKeys[state] = output.LastEvaluatedKey
		for _, attributes := range output.Items {
			var item sessionItem
			if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
				return ports.GuildSessionPageResult{}, fmt.Errorf("decode indexed guild session: %w", err)
			}
			session, err := fromSessionItem(item)
			if err != nil {
				return ports.GuildSessionPageResult{}, err
			}
			if session.GuildID != guildID || session.LifecycleState != state || item.GSI2PK != "GUILD#"+guildID {
				return ports.GuildSessionPageResult{}, fmt.Errorf("guild session index entry is inconsistent")
			}
			streams[state] = append(streams[state], indexedSession{Session: session, State: state, SortKey: item.GSI2SK})
		}
	}

	merged := make([]indexedSession, 0)
	for _, stream := range streams {
		merged = append(merged, stream...)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Session.UpdatedAt.Equal(merged[j].Session.UpdatedAt) {
			return merged[i].Session.ID > merged[j].Session.ID
		}
		return merged[i].Session.UpdatedAt.After(merged[j].Session.UpdatedAt)
	})
	if len(merged) > int(size) {
		merged = merged[:size]
	}

	consumed := make(map[domain.LifecycleState]int, len(normalized))
	result := ports.GuildSessionPageResult{Sessions: make([]domain.Session, 0, len(merged))}
	for _, candidate := range merged {
		result.Sessions = append(result.Sessions, candidate.Session)
		consumed[candidate.State]++
		cursor.Positions[string(candidate.State)] = candidate.SortKey
	}
	hasMore := false
	for _, state := range normalized {
		stream := streams[state]
		if consumed[state] < len(stream) || len(lastKeys[state]) != 0 {
			hasMore = true
			continue
		}
		cursor.Done[string(state)] = true
	}
	if hasMore {
		result.NextCursor, err = encodeGuildSessionCursor(cursor)
		if err != nil {
			return ports.GuildSessionPageResult{}, err
		}
	}
	return result, nil
}

func (repository *Repository) guildSessionIndexReady(ctx context.Context) (bool, error) {
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: guildSessionIndexPK},
			"sk": &types.AttributeValueMemberS{Value: guildSessionIndexSK},
		},
	})
	if err != nil {
		return false, fmt.Errorf("read guild session index cutover: %w", err)
	}
	if output == nil {
		return false, nil
	}
	status, ok := output.Item["status"].(*types.AttributeValueMemberS)
	return ok && status.Value == "READY", nil
}

func (repository *Repository) listGuildSessionsCompatibility(ctx context.Context, guildID string, states []domain.LifecycleState, size int32, encodedCursor string) (ports.GuildSessionPageResult, error) {
	cursor, err := decodeGuildSessionCompatibilityCursor(encodedCursor, guildID, states)
	if err != nil {
		return ports.GuildSessionPageResult{}, err
	}
	wanted := make(map[domain.LifecycleState]struct{}, len(states))
	for _, state := range states {
		wanted[state] = struct{}{}
	}
	sessions := make([]domain.Session, 0)
	var startKey map[string]types.AttributeValue
	for {
		output, scanErr := repository.client.Scan(ctx, &dynamodb.ScanInput{
			TableName: repository.tableNamePointer(), ConsistentRead: aws.Bool(true), Limit: aws.Int32(100), ExclusiveStartKey: startKey,
			FilterExpression: aws.String("entity_type = :type AND guild_id = :guild"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":type": &types.AttributeValueMemberS{Value: "Session"}, ":guild": &types.AttributeValueMemberS{Value: guildID},
			},
		})
		if scanErr != nil {
			return ports.GuildSessionPageResult{}, fmt.Errorf("scan compatibility guild sessions: %w", scanErr)
		}
		for _, attributes := range output.Items {
			var item sessionItem
			if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
				return ports.GuildSessionPageResult{}, fmt.Errorf("decode compatibility guild session: %w", err)
			}
			session, err := fromSessionItem(item)
			if err != nil {
				return ports.GuildSessionPageResult{}, err
			}
			if _, ok := wanted[session.LifecycleState]; ok {
				sessions = append(sessions, session)
			}
		}
		startKey = output.LastEvaluatedKey
		if len(startKey) == 0 {
			break
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].ID > sessions[j].ID
		}
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	start := 0
	if cursor.UpdatedAt != "" {
		position, _ := time.Parse(time.RFC3339Nano, cursor.UpdatedAt)
		start = len(sessions)
		for index, session := range sessions {
			if session.UpdatedAt.Before(position) || (session.UpdatedAt.Equal(position) && session.ID < cursor.SessionID) {
				start = index
				break
			}
		}
	}
	end := start + int(size)
	if end > len(sessions) {
		end = len(sessions)
	}
	result := ports.GuildSessionPageResult{Sessions: append([]domain.Session(nil), sessions[start:end]...)}
	if end < len(sessions) && len(result.Sessions) != 0 {
		last := result.Sessions[len(result.Sessions)-1]
		cursor.UpdatedAt, cursor.SessionID = last.UpdatedAt.UTC().Format(time.RFC3339Nano), last.ID
		data, _ := json.Marshal(cursor)
		result.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return result, nil
}

func normalizeGuildSessionStates(states []domain.LifecycleState) ([]domain.LifecycleState, error) {
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

func decodeGuildSessionCursor(encoded, guildID string, states []domain.LifecycleState) (guildSessionCursor, error) {
	stateNames := make([]string, len(states))
	for i, state := range states {
		stateNames[i] = string(state)
	}
	cursor := guildSessionCursor{Version: 1, Mode: "index", GuildID: guildID, States: stateNames, Positions: map[string]string{}, Done: map[string]bool{}}
	if encoded == "" {
		return cursor, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return guildSessionCursor{}, fmt.Errorf("decode guild session cursor: %w", err)
	}
	if err := json.Unmarshal(data, &cursor); err != nil {
		return guildSessionCursor{}, fmt.Errorf("decode guild session cursor: %w", err)
	}
	if cursor.Version != 1 || (cursor.Mode != "" && cursor.Mode != "index") || cursor.GuildID != guildID || !slices.Equal(cursor.States, stateNames) {
		return guildSessionCursor{}, errors.New("guild session cursor does not match request")
	}
	if cursor.Positions == nil {
		cursor.Positions = map[string]string{}
	}
	if cursor.Done == nil {
		cursor.Done = map[string]bool{}
	}
	allowed := make(map[string]struct{}, len(stateNames))
	for _, state := range stateNames {
		allowed[state] = struct{}{}
	}
	for state, position := range cursor.Positions {
		if _, ok := allowed[state]; !ok || !strings.HasPrefix(position, "STATE#"+state+"#UPDATED#") {
			return guildSessionCursor{}, errors.New("guild session cursor contains an invalid position")
		}
		parts := strings.Split(position, "#SESSION#")
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return guildSessionCursor{}, errors.New("guild session cursor contains an invalid session key")
		}
	}
	for state := range cursor.Done {
		if _, ok := allowed[state]; !ok {
			return guildSessionCursor{}, errors.New("guild session cursor contains an invalid state")
		}
	}
	return cursor, nil
}

func decodeGuildSessionCompatibilityCursor(encoded, guildID string, states []domain.LifecycleState) (guildSessionCompatibilityCursor, error) {
	stateNames := make([]string, len(states))
	for index, state := range states {
		stateNames[index] = string(state)
	}
	cursor := guildSessionCompatibilityCursor{Version: 1, Mode: "scan", GuildID: guildID, States: stateNames}
	if encoded == "" {
		return cursor, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || cursor.Mode != "scan" || cursor.GuildID != guildID || !slices.Equal(cursor.States, stateNames) {
		return guildSessionCompatibilityCursor{}, errors.New("guild session compatibility cursor does not match request")
	}
	if cursor.UpdatedAt == "" || cursor.SessionID == "" {
		return guildSessionCompatibilityCursor{}, errors.New("guild session compatibility cursor is incomplete")
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.UpdatedAt); err != nil {
		return guildSessionCompatibilityCursor{}, errors.New("guild session compatibility cursor has an invalid timestamp")
	}
	return cursor, nil
}

func guildSessionCursorMode(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode guild session cursor: %w", err)
	}
	header := struct {
		Mode string `json:"mode"`
	}{}
	if err := json.Unmarshal(data, &header); err != nil {
		return "", fmt.Errorf("decode guild session cursor: %w", err)
	}
	if header.Mode != "" && header.Mode != "index" && header.Mode != "scan" {
		return "", errors.New("guild session cursor mode is invalid")
	}
	return header.Mode, nil
}

func encodeGuildSessionCursor(cursor guildSessionCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode guild session cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func guildSessionExclusiveStartKey(guildID, indexSortKey string) map[string]types.AttributeValue {
	parts := strings.Split(indexSortKey, "#SESSION#")
	sessionID := parts[len(parts)-1]
	return map[string]types.AttributeValue{
		"pk":     &types.AttributeValueMemberS{Value: sessionPartitionKey(sessionID)},
		"sk":     &types.AttributeValueMemberS{Value: sessionSortKey},
		"gsi2pk": &types.AttributeValueMemberS{Value: "GUILD#" + guildID},
		"gsi2sk": &types.AttributeValueMemberS{Value: indexSortKey},
	}
}

func allGuildSessionLifecycleStates() []domain.LifecycleState {
	return []domain.LifecycleState{
		domain.StateDraft, domain.StateNew, domain.StateValidating, domain.StateProvisioning,
		domain.StateBootstrapping, domain.StateInstalling, domain.StateReady, domain.StateRunning,
		domain.StateIdle, domain.StateStopping, domain.StateSleeping, domain.StateWaking,
		domain.StateRestarting, domain.StateWarning1, domain.StateWarning2, domain.StateArchiving,
		domain.StateDestroying, domain.StateArchived, domain.StateRestoring, domain.StateDeleting,
		domain.StateDeleted, domain.StateFailed,
	}
}
