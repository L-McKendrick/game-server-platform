package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	Arma3WorkshopAppID                  uint32 = 107410
	MaximumWorkshopChildren                    = 500
	MaximumWorkshopMissionItems                = 20
	MaximumWorkshopMissionSources              = 20
	MaximumWorkshopMissionSnapshotItems        = 1000
)

type WorkshopTarget string

const (
	WorkshopTargetMission WorkshopTarget = "mission"
	WorkshopTargetMods    WorkshopTarget = "mods"
)

type WorkshopSourceKind string

const (
	WorkshopSourceItem       WorkshopSourceKind = "item"
	WorkshopSourceCollection WorkshopSourceKind = "collection"
)

type WorkshopItemClass string

const (
	WorkshopItemClientMod           WorkshopItemClass = "client_mod"
	WorkshopItemServerMod           WorkshopItemClass = "server_mod"
	WorkshopItemMultiplayerScenario WorkshopItemClass = "multiplayer_scenario"
	WorkshopItemScenario            WorkshopItemClass = "scenario"
	WorkshopItemNestedCollection    WorkshopItemClass = "nested_collection"
	WorkshopItemCrossGame           WorkshopItemClass = "cross_game"
	WorkshopItemUnavailable         WorkshopItemClass = "unavailable"
	WorkshopItemUnsupported         WorkshopItemClass = "unsupported"
)

type WorkshopMetadataErrorCode string

const (
	WorkshopMetadataUnavailable     WorkshopMetadataErrorCode = "WORKSHOP_UNAVAILABLE"
	WorkshopMetadataRateLimited     WorkshopMetadataErrorCode = "WORKSHOP_RATE_LIMITED"
	WorkshopMetadataTransient       WorkshopMetadataErrorCode = "WORKSHOP_TRANSIENT"
	WorkshopMetadataRejected        WorkshopMetadataErrorCode = "WORKSHOP_REJECTED"
	WorkshopMetadataInvalidResponse WorkshopMetadataErrorCode = "WORKSHOP_INVALID_RESPONSE"
)

type WorkshopMetadataError struct {
	Code      WorkshopMetadataErrorCode
	Retryable bool
	Detail    string
}

func (err WorkshopMetadataError) Error() string {
	if strings.TrimSpace(err.Detail) == "" {
		return string(err.Code)
	}
	return fmt.Sprintf("%s: %s", err.Code, err.Detail)
}

type WorkshopSourceRequest struct {
	MessageType    string         `json:"message_type"`
	SchemaVersion  int            `json:"schema_version"`
	SessionID      string         `json:"session_id"`
	Target         WorkshopTarget `json:"target"`
	SourceURL      string         `json:"source_url"`
	ActorID        string         `json:"actor_id"`
	GuildID        string         `json:"guild_id"`
	ChannelID      string         `json:"channel_id"`
	CorrelationID  string         `json:"correlation_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	RequestedAt    time.Time      `json:"requested_at"`
}

func (request WorkshopSourceRequest) Validate() error {
	switch {
	case request.MessageType != "workshop_resolution":
		return fmt.Errorf("unsupported Workshop message type")
	case request.SchemaVersion != 1:
		return fmt.Errorf("unsupported Workshop request schema version")
	case strings.TrimSpace(request.SessionID) == "":
		return fmt.Errorf("session ID is required")
	case request.Target != WorkshopTargetMission && request.Target != WorkshopTargetMods:
		return fmt.Errorf("Workshop target must be mission or mods")
	case strings.TrimSpace(request.ActorID) == "" || strings.TrimSpace(request.GuildID) == "" || strings.TrimSpace(request.ChannelID) == "":
		return fmt.Errorf("Workshop requester context is required")
	case strings.TrimSpace(request.CorrelationID) == "" || strings.TrimSpace(request.IdempotencyKey) == "":
		return fmt.Errorf("Workshop request identity is required")
	case request.RequestedAt.IsZero():
		return fmt.Errorf("Workshop request time is required")
	}
	_, err := ParseWorkshopURL(request.SourceURL)
	return err
}

type WorkshopReference struct {
	PublishedFileID uint64 `json:"published_file_id"`
	CanonicalURL    string `json:"canonical_url"`
}

func ParseWorkshopURL(raw string) (WorkshopReference, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "steamcommunity.com") || parsed.Path != "/sharedfiles/filedetails/" {
		return WorkshopReference{}, fmt.Errorf("Workshop URL must be a canonical Steam Community shared-file link")
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" {
		return WorkshopReference{}, fmt.Errorf("Workshop URL contains unsupported components")
	}
	values := parsed.Query()
	if len(values) != 1 || len(values["id"]) != 1 {
		return WorkshopReference{}, fmt.Errorf("Workshop URL must contain only one item ID")
	}
	id, err := strconv.ParseUint(values.Get("id"), 10, 64)
	if err != nil || id == 0 {
		return WorkshopReference{}, fmt.Errorf("Workshop item ID is invalid")
	}
	return WorkshopReference{PublishedFileID: id, CanonicalURL: fmt.Sprintf("https://steamcommunity.com/sharedfiles/filedetails/?id=%d", id)}, nil
}

type WorkshopItem struct {
	PublishedFileID uint64            `json:"published_file_id"`
	ConsumerAppID   uint32            `json:"consumer_app_id"`
	Title           string            `json:"title"`
	Tags            []string          `json:"tags,omitempty"`
	UpdatedAt       time.Time         `json:"updated_at,omitempty"`
	FileSize        int64             `json:"file_size,omitempty"`
	Available       bool              `json:"available"`
	Collection      bool              `json:"collection,omitempty"`
	Class           WorkshopItemClass `json:"class"`
	MatchesTarget   bool              `json:"matches_target"`
	Issue           string            `json:"issue,omitempty"`
}

type WorkshopResolution struct {
	SchemaVersion    int                `json:"schema_version"`
	Target           WorkshopTarget     `json:"target"`
	SourceKind       WorkshopSourceKind `json:"source_kind"`
	Source           WorkshopReference  `json:"source"`
	Items            []WorkshopItem     `json:"items"`
	ResolvedAt       time.Time          `json:"resolved_at"`
	ResolutionSHA256 string             `json:"resolution_sha256"`
}

type WorkshopResolutionItem struct {
	PublishedFileID uint64            `json:"published_file_id"`
	Class           WorkshopItemClass `json:"class"`
}

type WorkshopMissionSource struct {
	Source           WorkshopReference        `json:"source"`
	SourceKind       WorkshopSourceKind       `json:"source_kind"`
	ResolutionSHA256 string                   `json:"resolution_sha256"`
	AcceptedItemIDs  []uint64                 `json:"accepted_item_ids"`
	ExcludedItems    []WorkshopResolutionItem `json:"excluded_items,omitempty"`
	ResolvedAt       time.Time                `json:"resolved_at"`
}

func NewWorkshopMissionSource(resolution WorkshopResolution) (WorkshopMissionSource, error) {
	if resolution.Target != WorkshopTargetMission || resolution.SchemaVersion != 1 || resolution.Source.PublishedFileID == 0 || !validHexSHA256(resolution.ResolutionSHA256) || resolution.ResolvedAt.IsZero() {
		return WorkshopMissionSource{}, fmt.Errorf("Workshop mission resolution is invalid")
	}
	source := WorkshopMissionSource{Source: resolution.Source, SourceKind: resolution.SourceKind, ResolutionSHA256: resolution.ResolutionSHA256, ResolvedAt: resolution.ResolvedAt.UTC()}
	for _, item := range resolution.Items {
		if item.MatchesTarget && item.Class == WorkshopItemMultiplayerScenario {
			source.AcceptedItemIDs = append(source.AcceptedItemIDs, item.PublishedFileID)
		} else {
			source.ExcludedItems = append(source.ExcludedItems, WorkshopResolutionItem{PublishedFileID: item.PublishedFileID, Class: item.Class})
		}
	}
	if err := source.Validate(); err != nil {
		return WorkshopMissionSource{}, err
	}
	return source, nil
}

func (source WorkshopMissionSource) Validate() error {
	if source.Source.PublishedFileID == 0 || source.Source.CanonicalURL == "" || (source.SourceKind != WorkshopSourceItem && source.SourceKind != WorkshopSourceCollection) || !validHexSHA256(source.ResolutionSHA256) || source.ResolvedAt.IsZero() {
		return fmt.Errorf("Workshop mission source metadata is invalid")
	}
	reference, err := ParseWorkshopURL(source.Source.CanonicalURL)
	if err != nil || reference.PublishedFileID != source.Source.PublishedFileID || reference.CanonicalURL != source.Source.CanonicalURL {
		return fmt.Errorf("Workshop mission source reference is invalid")
	}
	if len(source.AcceptedItemIDs) == 0 || len(source.AcceptedItemIDs) > MaximumWorkshopMissionItems {
		return fmt.Errorf("Workshop mission source must contain 1 to %d accepted scenarios", MaximumWorkshopMissionItems)
	}
	seen := make(map[uint64]struct{}, len(source.AcceptedItemIDs)+len(source.ExcludedItems))
	for _, id := range source.AcceptedItemIDs {
		if id == 0 {
			return fmt.Errorf("Workshop mission source contains an invalid item")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("Workshop mission source contains duplicate items")
		}
		seen[id] = struct{}{}
	}
	if source.SourceKind == WorkshopSourceItem && (len(source.AcceptedItemIDs) != 1 || source.AcceptedItemIDs[0] != source.Source.PublishedFileID || len(source.ExcludedItems) != 0) {
		return fmt.Errorf("Workshop mission item source must resolve only itself")
	}
	for _, item := range source.ExcludedItems {
		if item.PublishedFileID == 0 || item.Class == "" {
			return fmt.Errorf("Workshop mission exclusion is invalid")
		}
		if _, exists := seen[item.PublishedFileID]; exists {
			return fmt.Errorf("Workshop mission source contains duplicate items")
		}
		seen[item.PublishedFileID] = struct{}{}
	}
	return nil
}

func (session *Session) RecordWorkshopMissionSource(source WorkshopMissionSource, now time.Time) error {
	if err := source.Validate(); err != nil {
		return err
	}
	if session.LifecycleState == StateDeleting || session.LifecycleState == StateDeleted || session.LifecycleState == StateArchiving || session.LifecycleState == StateDestroying {
		return fmt.Errorf("%w: Workshop missions cannot change in the current lifecycle", ErrInvalidTransition)
	}
	for index, existing := range session.WorkshopMissionSources {
		if existing.Source.PublishedFileID == source.Source.PublishedFileID && existing.SourceKind == source.SourceKind {
			if existing.ResolutionSHA256 == source.ResolutionSHA256 {
				return nil
			}
			updated := append([]WorkshopMissionSource(nil), session.WorkshopMissionSources...)
			updated[index] = source
			if workshopMissionItemCount(updated) > MaximumWorkshopMissionItems || workshopMissionSnapshotItemCount(updated) > MaximumWorkshopMissionSnapshotItems {
				return fmt.Errorf("Workshop mission item limit reached")
			}
			session.WorkshopMissionSources = updated
			return session.RecordMutation(now)
		}
	}
	if len(session.WorkshopMissionSources) >= MaximumWorkshopMissionSources {
		return fmt.Errorf("Workshop mission source limit reached")
	}
	if workshopMissionSnapshotItemCount(append(append([]WorkshopMissionSource(nil), session.WorkshopMissionSources...), source)) > MaximumWorkshopMissionSnapshotItems {
		return fmt.Errorf("Workshop mission snapshot limit reached")
	}
	if workshopMissionItemCount(append(append([]WorkshopMissionSource(nil), session.WorkshopMissionSources...), source)) > MaximumWorkshopMissionItems {
		return fmt.Errorf("Workshop mission item limit reached")
	}
	session.WorkshopMissionSources = append(session.WorkshopMissionSources, source)
	return session.RecordMutation(now)
}

func workshopMissionItemCount(sources []WorkshopMissionSource) int {
	seen := map[uint64]struct{}{}
	for _, source := range sources {
		for _, id := range source.AcceptedItemIDs {
			seen[id] = struct{}{}
		}
	}
	return len(seen)
}

func workshopMissionSnapshotItemCount(sources []WorkshopMissionSource) int {
	total := 0
	for _, source := range sources {
		total += len(source.AcceptedItemIDs) + len(source.ExcludedItems)
	}
	return total
}

func (session Session) WorkshopMissionItemIDs() []uint64 {
	seen := map[uint64]struct{}{}
	for _, source := range session.WorkshopMissionSources {
		for _, id := range source.AcceptedItemIDs {
			seen[id] = struct{}{}
		}
	}
	ids := make([]uint64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (session Session) WorkshopSourcesForItem(itemID uint64) []WorkshopReference {
	var result []WorkshopReference
	for _, source := range session.WorkshopMissionSources {
		if slices.Contains(source.AcceptedItemIDs, itemID) {
			result = append(result, source.Source)
		}
	}
	slices.SortFunc(result, func(a, b WorkshopReference) int {
		if a.PublishedFileID < b.PublishedFileID {
			return -1
		}
		if a.PublishedFileID > b.PublishedFileID {
			return 1
		}
		return strings.Compare(a.CanonicalURL, b.CanonicalURL)
	})
	return result
}

func (session Session) WorkshopMissionRevision() (string, error) {
	type snapshot struct {
		id     uint64
		digest string
	}
	var snapshots []snapshot
	for _, source := range session.WorkshopMissionSources {
		if err := source.Validate(); err != nil {
			return "", err
		}
		for _, id := range source.AcceptedItemIDs {
			snapshots = append(snapshots, snapshot{id, source.ResolutionSHA256})
		}
	}
	slices.SortFunc(snapshots, func(a, b snapshot) int {
		if a.id < b.id {
			return -1
		}
		if a.id > b.id {
			return 1
		}
		return strings.Compare(a.digest, b.digest)
	})
	digest := sha256.New()
	for _, item := range snapshots {
		fmt.Fprintf(digest, "%d:%s\n", item.id, item.digest)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (resolution *WorkshopResolution) Finalize(now time.Time) error {
	if resolution.SchemaVersion != 1 || (resolution.Target != WorkshopTargetMission && resolution.Target != WorkshopTargetMods) {
		return fmt.Errorf("invalid Workshop resolution")
	}
	if resolution.Source.PublishedFileID == 0 || len(resolution.Items) == 0 || len(resolution.Items) > MaximumWorkshopChildren {
		return fmt.Errorf("Workshop resolution item count is invalid")
	}
	slices.SortFunc(resolution.Items, func(left, right WorkshopItem) int {
		if left.PublishedFileID < right.PublishedFileID {
			return -1
		}
		if left.PublishedFileID > right.PublishedFileID {
			return 1
		}
		return 0
	})
	seen := make(map[uint64]struct{}, len(resolution.Items))
	var digestInput strings.Builder
	digestInput.WriteString(string(resolution.Target) + "\n" + string(resolution.SourceKind) + "\n" + strconv.FormatUint(resolution.Source.PublishedFileID, 10) + "\n")
	for index := range resolution.Items {
		item := &resolution.Items[index]
		if item.PublishedFileID == 0 {
			return fmt.Errorf("Workshop resolution contains an invalid item ID")
		}
		if _, duplicate := seen[item.PublishedFileID]; duplicate {
			return fmt.Errorf("Workshop resolution contains duplicate items")
		}
		seen[item.PublishedFileID] = struct{}{}
		item.Tags = normalizeWorkshopTags(item.Tags)
		digestInput.WriteString(fmt.Sprintf("%d\t%d\t%s\t%t\t%d\t%s\n", item.PublishedFileID, item.ConsumerAppID, item.Class, item.MatchesTarget, item.UpdatedAt.Unix(), strings.Join(item.Tags, ",")))
	}
	digest := sha256.Sum256([]byte(digestInput.String()))
	resolution.ResolvedAt = now.UTC()
	resolution.ResolutionSHA256 = hex.EncodeToString(digest[:])
	return nil
}

func ClassifyWorkshopItem(item WorkshopItem, target WorkshopTarget) WorkshopItem {
	item.Tags = normalizeWorkshopTags(item.Tags)
	tags := make(map[string]struct{}, len(item.Tags))
	for _, tag := range item.Tags {
		tags[strings.ToLower(tag)] = struct{}{}
	}
	_, scenario := tags["scenario"]
	_, multiplayer := tags["multiplayer"]
	_, coop := tags["coop"]
	_, mod := tags["mod"]
	_, server := tags["server"]
	switch {
	case !item.Available:
		item.Class, item.Issue = WorkshopItemUnavailable, "Workshop item is unavailable or private."
	case item.ConsumerAppID != Arma3WorkshopAppID:
		item.Class, item.Issue = WorkshopItemCrossGame, "Workshop item does not belong to Arma 3."
	case item.Collection:
		item.Class, item.Issue = WorkshopItemNestedCollection, "Nested Workshop collections are not supported."
	case scenario && (multiplayer || coop):
		item.Class = WorkshopItemMultiplayerScenario
		item.MatchesTarget = target == WorkshopTargetMission
	case scenario:
		item.Class, item.Issue = WorkshopItemScenario, "Scenario is not tagged Multiplayer or Coop."
	case server:
		item.Class = WorkshopItemServerMod
		item.MatchesTarget = target == WorkshopTargetMods
	case mod:
		item.Class = WorkshopItemClientMod
		item.MatchesTarget = target == WorkshopTargetMods
	default:
		item.Class, item.Issue = WorkshopItemUnsupported, "Workshop item has an unsupported data type."
	}
	if !item.MatchesTarget && item.Issue == "" {
		item.Issue = "Workshop item does not match the requested content type."
	}
	return item
}

func normalizeWorkshopTags(tags []string) []string {
	seen := make(map[string]string, len(tags))
	for _, raw := range tags {
		tag := strings.TrimSpace(raw)
		if tag != "" {
			seen[strings.ToLower(tag)] = tag
		}
	}
	result := make([]string, 0, len(seen))
	for _, tag := range seen {
		result = append(result, tag)
	}
	slices.SortFunc(result, func(left, right string) int { return strings.Compare(strings.ToLower(left), strings.ToLower(right)) })
	return result
}
