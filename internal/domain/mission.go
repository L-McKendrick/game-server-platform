package domain

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

const DefaultArma3MissionTemplate = "MP_ZGM_m12.Stratis"

var missionHashPrefix = regexp.MustCompile(`^[0-9a-fA-F]{64}-`)

type MissionRecord struct {
	ObjectKey string         `json:"object_key"`
	Filename  string         `json:"filename"`
	Status    ArtifactStatus `json:"status"`
	Issue     string         `json:"issue,omitempty"`
	AddedAt   time.Time      `json:"added_at"`
	RemovedAt time.Time      `json:"removed_at,omitempty"`
}

func (record MissionRecord) Active() bool { return record.RemovedAt.IsZero() }

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
