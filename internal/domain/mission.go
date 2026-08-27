package domain

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

const DefaultArma3MissionTemplate = "MP_ZGM_m12.Stratis"

var missionHashPrefix = regexp.MustCompile(`^[0-9a-fA-F]{64}-`)

type MissionRecord struct {
	ObjectKey       string              `json:"object_key"`
	Filename        string              `json:"filename"`
	Status          ArtifactStatus      `json:"status"`
	Issue           string              `json:"issue,omitempty"`
	AddedAt         time.Time           `json:"added_at"`
	RemovedAt       time.Time           `json:"removed_at,omitempty"`
	WorkshopItemID  uint64              `json:"workshop_item_id,omitempty"`
	WorkshopSources []WorkshopReference `json:"workshop_sources,omitempty"`
}

func (session *Session) AttachWorkshopMission(record MissionRecord, now time.Time) error {
	if record.WorkshopItemID == 0 || len(record.WorkshopSources) == 0 || record.Status != ArtifactAccepted || strings.TrimSpace(record.ObjectKey) == "" {
		return fmt.Errorf("Workshop mission record is invalid")
	}
	filename, err := NormalizeMissionFilename(record.Filename)
	if err != nil || filename != record.Filename || missionFilenameFromObjectKey(record.ObjectKey) != filename {
		return fmt.Errorf("Workshop mission filename or object key is invalid")
	}
	if session.LifecycleState == StateDeleting || session.LifecycleState == StateDeleted || session.LifecycleState == StateArchiving || session.LifecycleState == StateDestroying {
		return fmt.Errorf("%w: Workshop missions cannot change in the current lifecycle", ErrInvalidTransition)
	}
	for _, source := range record.WorkshopSources {
		matched := false
		for _, persisted := range session.WorkshopMissionSources {
			if persisted.Source == source && slices.Contains(persisted.AcceptedItemIDs, record.WorkshopItemID) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("Workshop mission provenance is not authorized")
		}
	}
	for index := range session.MissionFiles {
		if session.MissionFiles[index].ObjectKey == record.ObjectKey {
			return nil
		}
		if session.MissionFiles[index].WorkshopItemID == record.WorkshopItemID && session.MissionFiles[index].Active() {
			session.MissionFiles[index].RemovedAt = now.UTC()
		}
	}
	record.AddedAt = now.UTC()
	session.MissionFiles = append(session.MissionFiles, record)
	return session.RecordMutation(now)
}

func (record MissionRecord) Active() bool { return record.RemovedAt.IsZero() }

func (record MissionRecord) Accepted() bool {
	return record.Active() && record.Status == ArtifactAccepted && strings.TrimSpace(record.ObjectKey) != ""
}

// AcceptedMissionFiles returns the bounded bootstrap/live-copy authority in
// stable insertion order. Removed and rejected records never reach a host.
func (session Session) AcceptedMissionFiles() []MissionRecord {
	missions := make([]MissionRecord, 0, len(session.MissionFiles))
	seen := make(map[string]bool, len(session.MissionFiles))
	for _, record := range session.MissionFiles {
		if !record.Accepted() || seen[record.ObjectKey] {
			continue
		}
		seen[record.ObjectKey] = true
		missions = append(missions, record)
	}
	// Preserve legacy sessions whose accepted mission predates MissionFiles.
	selection := session.MissionForApplication()
	if selection.ObjectKey != "" && !seen[selection.ObjectKey] {
		missions = append(missions, MissionRecord{ObjectKey: selection.ObjectKey, Filename: missionFilenameFromObjectKey(selection.ObjectKey), Status: ArtifactAccepted})
	}
	return missions
}

func (session Session) LiveMissionCopyTarget(objectKey string) (MissionRecord, bool) {
	if (session.LifecycleState != StateRunning && session.LifecycleState != StateIdle) || session.ActiveWorkflowID != "" || strings.TrimSpace(session.Infrastructure.InstanceID) == "" {
		return MissionRecord{}, false
	}
	for _, record := range session.AcceptedMissionFiles() {
		if record.ObjectKey == strings.TrimSpace(objectKey) {
			return record, true
		}
	}
	return MissionRecord{}, false
}

type MissionSelection struct {
	Template  string `json:"template"`
	ObjectKey string `json:"object_key,omitempty"`
}

func DefaultMissionSelection() MissionSelection {
	return MissionSelection{Template: DefaultArma3MissionTemplate}
}

func UploadedMissionSelection(objectKey string) MissionSelection {
	objectKey = strings.TrimSpace(objectKey)
	return MissionSelection{Template: missionTemplateFromObjectKey(objectKey), ObjectKey: objectKey}
}

func (selection MissionSelection) IsDefault() bool {
	return selection.ObjectKey == "" && selection.Template == DefaultArma3MissionTemplate
}

func (selection MissionSelection) Validate() error {
	if selection.IsDefault() {
		return nil
	}
	if strings.TrimSpace(selection.ObjectKey) == "" || strings.TrimSpace(selection.Template) == "" {
		return fmt.Errorf("mission selection requires an object key and template")
	}
	return nil
}

func missionTemplateFromObjectKey(objectKey string) string {
	name := missionFilenameFromObjectKey(objectKey)
	return strings.TrimSuffix(name, path.Ext(name))
}

func missionFilenameFromObjectKey(objectKey string) string {
	name := path.Base(strings.TrimSpace(objectKey))
	name = missionHashPrefix.ReplaceAllString(name, "")
	return name
}

func (session *Session) normalizeMissionCompatibility() {
	if session.ConfiguredMission.Template == "" {
		if session.MissionObjectKey != "" {
			session.ConfiguredMission = MissionSelection{Template: missionTemplateFromObjectKey(session.MissionObjectKey), ObjectKey: session.MissionObjectKey}
		} else {
			session.ConfiguredMission = DefaultMissionSelection()
		}
	}
	if session.ConfiguredMission.ObjectKey != "" {
		session.MissionObjectKey = session.ConfiguredMission.ObjectKey
	} else {
		session.MissionObjectKey = ""
	}
}

func (session *Session) ConfigureMission(objectKey string, now time.Time) error {
	objectKey = strings.TrimSpace(objectKey)
	if objectKey == "" {
		session.ConfiguredMission = DefaultMissionSelection()
		session.MissionObjectKey = ""
	} else {
		found := false
		for _, record := range session.MissionFiles {
			if record.ObjectKey == objectKey && record.Active() && record.Status == ArtifactAccepted {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: mission is not an accepted active file", ErrNotFound)
		}
		session.ConfiguredMission = MissionSelection{Template: missionTemplateFromObjectKey(objectKey), ObjectKey: objectKey}
		session.MissionObjectKey = objectKey
	}
	return session.RecordMutation(now)
}

func (session *Session) SnapshotConfiguredMission() {
	session.normalizeMissionCompatibility()
	session.CurrentMission = session.ConfiguredMission
}

func (session Session) MissionForApplication() MissionSelection {
	if session.CurrentMission.Template != "" {
		return session.CurrentMission
	}
	if session.ConfiguredMission.Template != "" {
		return session.ConfiguredMission
	}
	if session.MissionObjectKey != "" {
		return MissionSelection{Template: missionTemplateFromObjectKey(session.MissionObjectKey), ObjectKey: session.MissionObjectKey}
	}
	return DefaultMissionSelection()
}

func (session *Session) RemoveMission(objectKey string, now time.Time) error {
	objectKey = strings.TrimSpace(objectKey)
	if session.CurrentMission.ObjectKey == objectKey {
		return fmt.Errorf("%w: currently loaded mission cannot be removed", ErrConflict)
	}
	for index := range session.MissionFiles {
		if session.MissionFiles[index].ObjectKey == objectKey && session.MissionFiles[index].Active() {
			session.MissionFiles[index].RemovedAt = now.UTC()
			if session.ConfiguredMission.ObjectKey == objectKey {
				session.ConfiguredMission = DefaultMissionSelection()
				session.MissionObjectKey = ""
			}
			return session.RecordMutation(now)
		}
	}
	return ErrNotFound
}

func (session *Session) RejectMissionUpload(recordID, filename, issue string, now time.Time) error {
	filename, err := NormalizeMissionFilename(filename)
	if err != nil {
		filename = "rejected-upload.pbo"
	}
	issue = sanitizeFailureDetail(issue)
	if issue == "" {
		issue = "The uploaded file did not pass validation."
	}
	if len([]rune(issue)) > 160 {
		issue = string([]rune(issue)[:160])
	}
	session.MissionFiles = append(session.MissionFiles, MissionRecord{ObjectKey: "rejected:" + strings.TrimSpace(recordID), Filename: filename, Status: ArtifactRejected, Issue: issue, AddedAt: now.UTC()})
	if session.MissionArtifactStatus == ArtifactPending {
		session.MissionArtifactStatus, session.MissionArtifactIssue = ArtifactRejected, issue
	}
	return session.RecordMutation(now)
}
