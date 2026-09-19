package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type hostWorkshopResultItem struct {
	Target          string `json:"target"`
	PublishedFileID string `json:"published_file_id"`
	Revision        string `json:"revision"`
	Status          string `json:"status"`
}

// validateWorkshopResult compares the complete host report to trusted expected
// item/revision intent. A successful SSM command alone is not output acceptance.
func validateWorkshopResult(body []byte, scope domain.HostAccessScope, target string, startedAt, now time.Time, expected []hostWorkshopResultItem) error {
	if len(body) == 0 || len(body) > domain.MaxHostResultBytes || len(expected) > domain.MaximumWorkshopMissionItems+domain.MaximumWorkshopModItems || startedAt.IsZero() || now.IsZero() {
		return fmt.Errorf("Workshop result bounds invalid")
	}
	var result struct {
		SchemaVersion int                      `json:"schema_version"`
		SessionID     string                   `json:"session_id"`
		WorkflowID    string                   `json:"workflow_id"`
		Target        string                   `json:"target"`
		CompletedAt   time.Time                `json:"completed_at"`
		Items         []hostWorkshopResultItem `json:"items"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.SchemaVersion != 1 || result.SessionID != scope.SessionID || result.WorkflowID != scope.OperationID || result.Target != target || result.CompletedAt.Before(startedAt.Truncate(time.Second)) || result.CompletedAt.After(now.Add(time.Minute)) || result.CompletedAt.After(scope.DeadlineAt) || len(result.Items) != len(expected) {
		return fmt.Errorf("Workshop result envelope differs from current operation")
	}
	approved := make(map[string]hostWorkshopResultItem, len(expected))
	for _, item := range expected {
		if item.Status != "succeeded" || item.PublishedFileID == "" || item.Revision == "" {
			return fmt.Errorf("Workshop expected result intent invalid")
		}
		key := item.Target + "\x00" + item.PublishedFileID
		if _, exists := approved[key]; exists {
			return fmt.Errorf("Workshop expected result duplicate")
		}
		approved[key] = item
	}
	for _, item := range result.Items {
		key := item.Target + "\x00" + item.PublishedFileID
		approvedItem, exists := approved[key]
		if !exists || approvedItem != item {
			return fmt.Errorf("Workshop result item differs from approved revision")
		}
		delete(approved, key)
	}
	if len(approved) != 0 {
		return fmt.Errorf("Workshop result omitted approved item")
	}
	return nil
}
