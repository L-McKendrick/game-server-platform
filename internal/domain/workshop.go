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
	Arma3WorkshopAppID uint32 = 107410
	// MaximumWorkshopCollectionChildren bounds the complete direct membership
	// snapshot before child metadata is fetched or any host work is requested.
	MaximumWorkshopCollectionChildren         = 50
	MaximumWorkshopMetadataItems              = 500
	MaximumWorkshopMissionItems               = 20
	MaximumWorkshopMissionSources             = 20
	MaximumWorkshopMissionSnapshotItems       = 1000
	MaximumWorkshopMissionBytes         int64 = 100 * 1024 * 1024
	MaximumWorkshopModItems                   = 250
	MaximumWorkshopModSnapshotItems           = 1000
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
	WorkshopMetadataCollectionLimit WorkshopMetadataErrorCode = "WORKSHOP_COLLECTION_LIMIT"
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
	MessageType                  string         `json:"message_type"`
	SchemaVersion                int            `json:"schema_version"`
	SessionID                    string         `json:"session_id"`
	Target                       WorkshopTarget `json:"target"`
	SourceURL                    string         `json:"source_url"`
	ActorID                      string         `json:"actor_id"`
	GuildID                      string         `json:"guild_id"`
	ChannelID                    string         `json:"channel_id"`
	CorrelationID                string         `json:"correlation_id"`
	IdempotencyKey               string         `json:"idempotency_key"`
	RequestedAt                  time.Time      `json:"requested_at"`
	ExpectedActivePresetRevision int64          `json:"expected_active_preset_revision,omitempty"`
}

func (session *Session) BeginWorkshopResolution(target WorkshopTarget, requestKey string, now time.Time) error {
	requestKey = strings.TrimSpace(requestKey)
	if (target != WorkshopTargetMission && target != WorkshopTargetMods) || requestKey == "" || now.IsZero() {
		return fmt.Errorf("invalid Workshop resolution marker")
	}
	if session.WorkshopResolutionRequestKey != "" && (session.WorkshopResolutionTarget != target || session.WorkshopResolutionRequestKey != requestKey) {
		return ErrConflict
	}
	session.WorkshopResolutionTarget = target
	session.WorkshopResolutionRequestKey = requestKey
	session.WorkshopResolutionRequestedAt = now.UTC()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) FinishWorkshopResolution(target WorkshopTarget, requestKey string, now time.Time) error {
	if session.WorkshopResolutionRequestKey == "" {
		return nil
	}
	if session.WorkshopResolutionTarget != target || session.WorkshopResolutionRequestKey != strings.TrimSpace(requestKey) {
		return ErrConflict
	}
	session.WorkshopResolutionTarget = ""
	session.WorkshopResolutionRequestKey = ""
	session.WorkshopResolutionRequestedAt = time.Time{}
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) clearWorkshopResolutionMarker(target WorkshopTarget) bool {
	if session.WorkshopResolutionTarget == target {
		session.WorkshopResolutionTarget = ""
		session.WorkshopResolutionRequestKey = ""
		session.WorkshopResolutionRequestedAt = time.Time{}
		return true
	}
	return false
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
	case request.ExpectedActivePresetRevision < 0:
		return fmt.Errorf("expected active preset revision cannot be negative")
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
	if len(values["id"]) != 1 {
		return WorkshopReference{}, fmt.Errorf("Workshop URL must contain one item ID")
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
	Filename        string            `json:"filename,omitempty"`
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
	AcceptedItems    []WorkshopMissionItem    `json:"accepted_items,omitempty"`
	ExcludedItems    []WorkshopResolutionItem `json:"excluded_items,omitempty"`
	ResolvedAt       time.Time                `json:"resolved_at"`
}

// WorkshopMissionItem freezes the Steam-provided deployment identity used by
// the host. AcceptedItemIDs remains populated for backward-compatible reads.
type WorkshopMissionItem struct {
	PublishedFileID uint64 `json:"published_file_id"`
	Filename        string `json:"filename"`
	FileSize        int64  `json:"file_size"`
}

type WorkshopModItem struct {
	PublishedFileID uint64    `json:"published_file_id"`
	Title           string    `json:"title"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
	FileSize        int64     `json:"file_size,omitempty"`
}
type WorkshopModSource struct {
	Source            WorkshopReference        `json:"source"`
	SourceKind        WorkshopSourceKind       `json:"source_kind"`
	ResolutionSHA256  string                   `json:"resolution_sha256"`
	AcceptedItems     []WorkshopModItem        `json:"accepted_items"`
	ExcludedItems     []WorkshopResolutionItem `json:"excluded_items,omitempty"`
	ResolvedAt        time.Time                `json:"resolved_at"`
	PresetObjectKey   string                   `json:"preset_object_key"`
	ModlistObjectKey  string                   `json:"modlist_object_key"`
	ManifestObjectKey string                   `json:"manifest_object_key"`
	ArtifactSHA256    string                   `json:"artifact_sha256"`
}

// CanChangeWorkshopSources limits user-authored source snapshots to lifecycle
// boundaries where they cannot alter an in-flight bootstrap/archive manifest.
// Sleeping and warning sessions may safely retain content for their next wake.
func (session Session) CanChangeWorkshopSources() bool {
	if session.ActiveWorkflowID != "" {
		return false
	}
	switch session.LifecycleState {
	case StateDraft, StateNew, StateReady, StateRunning, StateIdle, StateSleeping, StateWarning1, StateWarning2, StateFailed:
		return true
	default:
		return false
	}
}

// PersistenceProjection omits untrusted display titles from the hot session
// row. The immutable S3 source manifest retains them for artifact provenance.
func (source WorkshopModSource) PersistenceProjection() WorkshopModSource {
	projection := source
	projection.AcceptedItems = append([]WorkshopModItem(nil), source.AcceptedItems...)
	for index := range projection.AcceptedItems {
		projection.AcceptedItems[index].Title = ""
	}
	projection.ExcludedItems = append([]WorkshopResolutionItem(nil), source.ExcludedItems...)
	return projection
}

func NewWorkshopModSource(resolution WorkshopResolution) (WorkshopModSource, error) {
	if resolution.Target != WorkshopTargetMods || resolution.SchemaVersion != 1 || !validHexSHA256(resolution.ResolutionSHA256) || resolution.ResolvedAt.IsZero() {
		return WorkshopModSource{}, fmt.Errorf("Workshop mod resolution is invalid")
	}
	source := WorkshopModSource{Source: resolution.Source, SourceKind: resolution.SourceKind, ResolutionSHA256: resolution.ResolutionSHA256, ResolvedAt: resolution.ResolvedAt.UTC()}
	for _, item := range resolution.Items {
		if item.MatchesTarget && item.Class == WorkshopItemClientMod {
			source.AcceptedItems = append(source.AcceptedItems, WorkshopModItem{PublishedFileID: item.PublishedFileID, Title: boundedWorkshopTitle(item.Title), UpdatedAt: item.UpdatedAt.UTC(), FileSize: item.FileSize})
		} else {
			source.ExcludedItems = append(source.ExcludedItems, WorkshopResolutionItem{PublishedFileID: item.PublishedFileID, Class: item.Class})
		}
	}
	if len(source.AcceptedItems) == 0 || len(source.AcceptedItems) > MaximumWorkshopModItems {
		return WorkshopModSource{}, fmt.Errorf("Workshop mod source must contain 1 to %d client mods", MaximumWorkshopModItems)
	}
	return source, nil
}

func boundedWorkshopTitle(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 100 {
		runes = runes[:100]
	}
	return string(runes)
}

func (source WorkshopModSource) Validate() error {
	ref, err := ParseWorkshopURL(source.Source.CanonicalURL)
	if err != nil || ref != source.Source || (source.SourceKind != WorkshopSourceItem && source.SourceKind != WorkshopSourceCollection) || !validHexSHA256(source.ResolutionSHA256) || source.ResolvedAt.IsZero() || len(source.AcceptedItems) == 0 || len(source.AcceptedItems) > MaximumWorkshopModItems {
		return fmt.Errorf("Workshop mod source metadata is invalid")
	}
	seen := map[uint64]bool{}
	for _, item := range source.AcceptedItems {
		if item.PublishedFileID == 0 || seen[item.PublishedFileID] || item.Title != boundedWorkshopTitle(item.Title) || item.FileSize < 0 {
			return fmt.Errorf("Workshop mod item is invalid")
		}
		seen[item.PublishedFileID] = true
	}
	for _, item := range source.ExcludedItems {
		if item.PublishedFileID == 0 || item.Class == "" || seen[item.PublishedFileID] {
			return fmt.Errorf("Workshop mod exclusion is invalid")
		}
		seen[item.PublishedFileID] = true
	}
	if source.SourceKind == WorkshopSourceCollection && len(source.AcceptedItems)+len(source.ExcludedItems) > MaximumWorkshopCollectionChildren {
		return fmt.Errorf("Workshop collection exceeds the %d-item limit", MaximumWorkshopCollectionChildren)
	}
	if source.SourceKind == WorkshopSourceItem && (len(source.AcceptedItems) != 1 || source.AcceptedItems[0].PublishedFileID != source.Source.PublishedFileID || len(source.ExcludedItems) != 0) {
		return fmt.Errorf("Workshop mod item source must resolve only itself")
	}
	if source.PresetObjectKey != "" || source.ModlistObjectKey != "" || source.ManifestObjectKey != "" || source.ArtifactSHA256 != "" {
		keys := source.PresetObjectKey + source.ModlistObjectKey + source.ManifestObjectKey
		if source.PresetObjectKey == "" || source.ModlistObjectKey == "" || source.ManifestObjectKey == "" || !validHexSHA256(source.ArtifactSHA256) || !strings.Contains(source.PresetObjectKey, source.ArtifactSHA256) || !strings.Contains(source.ModlistObjectKey, source.ArtifactSHA256) || !strings.Contains(source.ManifestObjectKey, source.ResolutionSHA256) || strings.Contains(keys, "..") {
			return fmt.Errorf("Workshop mod artifacts are incomplete")
		}
	}
	return nil
}

func (session *Session) RecordWorkshopModSource(source WorkshopModSource, now time.Time) error {
	if err := source.Validate(); err != nil {
		return err
	}
	source = source.PersistenceProjection()
	if !session.CanChangeWorkshopSources() {
		return fmt.Errorf("%w: Workshop mods cannot change in the current lifecycle", ErrInvalidTransition)
	}
	session.clearWorkshopResolutionMarker(WorkshopTargetMods)
	for _, existing := range session.WorkshopModSources {
		if existing.Source == source.Source && existing.ResolutionSHA256 == source.ResolutionSHA256 && existing.ArtifactSHA256 == source.ArtifactSHA256 {
			return nil
		}
	}
	if len(session.WorkshopModSources) >= MaximumWorkshopMissionSources {
		return ErrWorkshopSnapshotLimit
	}
	if workshopModSnapshotItemCount(append(append([]WorkshopModSource(nil), session.WorkshopModSources...), source)) > MaximumWorkshopModSnapshotItems {
		return ErrWorkshopSnapshotLimit
	}
	session.WorkshopModSources = append(session.WorkshopModSources, source)
	return session.RecordMutation(now)
}

func workshopModSnapshotItemCount(sources []WorkshopModSource) int {
	count := 0
	for _, source := range sources {
		count += len(source.AcceptedItems) + len(source.ExcludedItems)
	}
	return count
}

// AttachWorkshopModSource records immutable Workshop provenance and routes its
// generated artifacts through the same active/pending revision authority used
// by uploaded presets. Resolving metadata alone never refreshes this state.
func (session *Session) AttachWorkshopModSource(source WorkshopModSource, expectedActiveRevision int64, modlist PresetModlistMetadata, now time.Time) (PresetRevision, error) {
	if err := source.Validate(); err != nil {
		return PresetRevision{}, err
	}
	if source.PresetObjectKey == "" || source.ModlistObjectKey != modlist.ObjectKey || source.ArtifactSHA256 != modlist.SHA256 {
		return PresetRevision{}, fmt.Errorf("Workshop mod artifacts do not match revision metadata")
	}
	if session.Vanilla {
		return PresetRevision{}, fmt.Errorf("%w: vanilla sessions do not have a mod preset", ErrInvalidTransition)
	}
	active := session.EffectiveActivePresetRevision()
	if active.Number != expectedActiveRevision {
		return PresetRevision{}, fmt.Errorf("%w: active preset revision changed", ErrConflict)
	}
	if session.LifecycleState == StateDraft || session.LifecycleState == StateNew {
		if active.Number != 0 {
			return PresetRevision{}, fmt.Errorf("%w: initial Workshop preset already exists", ErrConflict)
		}
		if err := session.AttachArtifact(ArtifactPreset, source.PresetObjectKey, now); err != nil {
			return PresetRevision{}, err
		}
		session.ActivePresetRevision.Modlist = modlist
		session.ActivePresetRevision.WorkshopResolutionSHA256 = source.ResolutionSHA256
		session.ActivePresetRevision.WorkshopSourceID = source.Source.PublishedFileID
		if err := session.RecordWorkshopModSource(source, now); err != nil {
			return PresetRevision{}, err
		}
		return session.ActivePresetRevision, session.Validate()
	}
	revision, err := session.StagePresetRevision(expectedActiveRevision, source.PresetObjectKey, modlist, now)
	if err != nil {
		return PresetRevision{}, err
	}
	revision.WorkshopResolutionSHA256 = source.ResolutionSHA256
	revision.WorkshopSourceID = source.Source.PublishedFileID
	session.PendingPresetRevision = revision
	if err := session.RecordWorkshopModSource(source, now); err != nil {
		return PresetRevision{}, err
	}
	return revision, session.Validate()
}

func WorkshopExclusionSummary(items []WorkshopResolutionItem, limit int) string {
	if limit <= 0 || len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, min(len(items), limit))
	for index, item := range items {
		if index >= limit {
			break
		}
		parts = append(parts, fmt.Sprintf("%d (%s)", item.PublishedFileID, item.Class))
	}
	if len(items) > limit {
		parts = append(parts, fmt.Sprintf("and %d more", len(items)-limit))
	}
	return strings.Join(parts, ", ")
}

func NewWorkshopMissionSource(resolution WorkshopResolution) (WorkshopMissionSource, error) {
	if resolution.Target != WorkshopTargetMission || resolution.SchemaVersion != 1 || resolution.Source.PublishedFileID == 0 || !validHexSHA256(resolution.ResolutionSHA256) || resolution.ResolvedAt.IsZero() {
		return WorkshopMissionSource{}, fmt.Errorf("Workshop mission resolution is invalid")
	}
	source := WorkshopMissionSource{Source: resolution.Source, SourceKind: resolution.SourceKind, ResolutionSHA256: resolution.ResolutionSHA256, ResolvedAt: resolution.ResolvedAt.UTC()}
	for _, item := range resolution.Items {
		if item.MatchesTarget && item.Class == WorkshopItemMultiplayerScenario {
			filename, err := NormalizeMissionFilename(item.Filename)
			if err != nil || filename != item.Filename {
				return WorkshopMissionSource{}, fmt.Errorf("Workshop scenario %d has no safe canonical PBO filename", item.PublishedFileID)
			}
			if item.FileSize < 16 || item.FileSize > MaximumWorkshopMissionBytes {
				return WorkshopMissionSource{}, fmt.Errorf("Workshop scenario %d size is outside the allowed range", item.PublishedFileID)
			}
			source.AcceptedItemIDs = append(source.AcceptedItemIDs, item.PublishedFileID)
			source.AcceptedItems = append(source.AcceptedItems, WorkshopMissionItem{PublishedFileID: item.PublishedFileID, Filename: filename, FileSize: item.FileSize})
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
	if len(source.AcceptedItems) > 0 {
		if len(source.AcceptedItems) != len(source.AcceptedItemIDs) {
			return fmt.Errorf("Workshop mission deployment metadata is incomplete")
		}
		for index, item := range source.AcceptedItems {
			filename, filenameErr := NormalizeMissionFilename(item.Filename)
			if item.PublishedFileID != source.AcceptedItemIDs[index] || filenameErr != nil || filename != item.Filename || item.FileSize < 16 || item.FileSize > MaximumWorkshopMissionBytes {
				return fmt.Errorf("Workshop mission deployment metadata is invalid")
			}
		}
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
	if source.SourceKind == WorkshopSourceCollection && len(source.AcceptedItemIDs)+len(source.ExcludedItems) > MaximumWorkshopCollectionChildren {
		return fmt.Errorf("Workshop collection exceeds the %d-item limit", MaximumWorkshopCollectionChildren)
	}
	return nil
}

func (session *Session) RecordWorkshopMissionSource(source WorkshopMissionSource, now time.Time) error {
	if err := source.Validate(); err != nil {
		return err
	}
	if !session.CanChangeWorkshopSources() {
		return fmt.Errorf("%w: Workshop missions cannot change in the current lifecycle", ErrInvalidTransition)
	}
	resolutionCleared := session.clearWorkshopResolutionMarker(WorkshopTargetMission)
	for index, existing := range session.WorkshopMissionSources {
		if existing.Source.PublishedFileID == source.Source.PublishedFileID && existing.SourceKind == source.SourceKind {
			if existing.ResolutionSHA256 == source.ResolutionSHA256 {
				if resolutionCleared {
					return session.RecordMutation(now)
				}
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
	maximumItems := 1
	if resolution.SourceKind == WorkshopSourceCollection {
		maximumItems = MaximumWorkshopCollectionChildren
	}
	if resolution.Source.PublishedFileID == 0 || len(resolution.Items) == 0 || len(resolution.Items) > maximumItems {
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
		digestInput.WriteString(fmt.Sprintf("%d\t%d\t%s\t%s\t%d\t%t\t%d\t%s\n", item.PublishedFileID, item.ConsumerAppID, item.Class, item.Filename, item.FileSize, item.MatchesTarget, item.UpdatedAt.Unix(), strings.Join(item.Tags, ",")))
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
		item.Issue = "Server-only Workshop items require the separate server-mod workflow and are excluded from client presets."
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
