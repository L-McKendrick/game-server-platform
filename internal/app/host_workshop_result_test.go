package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestWorkshopResultRequiresCurrentExactItemsAndRevisions(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	scope := domain.HostAccessScope{SessionID: "session", OperationID: "operation", DeadlineAt: now.Add(time.Hour)}
	expected := []hostWorkshopResultItem{{Target: "mission", PublishedFileID: "42", Revision: "approved-revision", Status: "succeeded"}, {Target: "server_mod", PublishedFileID: "43", Revision: "2", Status: "succeeded"}}
	for _, scenario := range []string{"valid", "session", "workflow", "target", "revision", "status", "item", "duplicate", "omitted", "future", "before-start", "unknown-field", "trailing-json"} {
		t.Run(scenario, func(t *testing.T) {
			items := append([]hostWorkshopResultItem(nil), expected...)
			result := map[string]any{"schema_version": 1, "session_id": "session", "workflow_id": "operation", "target": "all", "completed_at": now, "items": items}
			switch scenario {
			case "session":
				result["session_id"] = "other"
			case "workflow":
				result["workflow_id"] = "other"
			case "target":
				result["target"] = "mods"
			case "revision":
				items[0].Revision = "unapproved"
			case "status":
				items[0].Status = "failed"
			case "item":
				items[0].PublishedFileID = "44"
			case "duplicate":
				items[1] = items[0]
			case "omitted":
				result["items"] = items[:1]
			case "future":
				result["completed_at"] = now.Add(2 * time.Minute)
			case "before-start":
				result["completed_at"] = now.Add(-time.Hour)
			case "unknown-field":
				result["extra"] = "unaccepted"
			}
			body, _ := json.Marshal(result)
			if scenario == "trailing-json" {
				body = append(body, []byte(" {}")...)
			}
			err := validateWorkshopResult(body, scope, "all", now.Add(-time.Minute), now, expected)
			if (err == nil) != (scenario == "valid") {
				t.Fatal("wrong result acceptance", err)
			}
		})
	}
	if validateWorkshopResult([]byte(strings.Repeat("x", domain.MaxHostResultBytes+1)), scope, "all", now, now, nil) == nil {
		t.Fatal("oversized result accepted")
	}
}
