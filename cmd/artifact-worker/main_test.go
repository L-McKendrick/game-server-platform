package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type recordingAutomaticStarter struct {
	sessions     []domain.Session
	correlations []string
	roles        [][]string
}

func (starter *recordingAutomaticStarter) RequestAutomaticStart(_ context.Context, session domain.Session, correlationID string, roles []string) error {
	starter.sessions = append(starter.sessions, session)
	starter.correlations = append(starter.correlations, correlationID)
	starter.roles = append(starter.roles, append([]string(nil), roles...))
	return nil
}

func TestResolvedWorkshopCollectionQueuesNormalAutomaticStart(t *testing.T) {
	starter := &recordingAutomaticStarter{}
	handler := &handler{startService: starter}
	session := domain.Session{ID: "session-collection", StartWhenReady: true, LifecycleState: domain.StateNew}
	request := domain.WorkshopSourceRequest{CorrelationID: "collection-correlation", Roles: []string{"role-allowed"}}
	if err := handler.requestAutomaticStart(context.Background(), session, request); err != nil {
		t.Fatal(err)
	}
	if len(starter.sessions) != 1 || starter.sessions[0].ID != session.ID || starter.correlations[0] != request.CorrelationID {
		t.Fatalf("automatic start = sessions %#v correlations %#v", starter.sessions, starter.correlations)
	}
	if len(starter.roles) != 1 || len(starter.roles[0]) != 1 || starter.roles[0][0] != "role-allowed" {
		t.Fatalf("automatic start roles = %#v", starter.roles)
	}
}

func TestDecodeWorkshopRequestAcceptsLegacyUnixTime(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"message_type": "workshop_resolution", "schema_version": 1, "session_id": "session-1", "target": "mods", "source_url": "https://steamcommunity.com/sharedfiles/filedetails/?id=42", "actor_id": "owner", "guild_id": "guild", "channel_id": "channel", "correlation_id": "correlation", "idempotency_key": "key", "requested_at": int64(1788415147)})
	request, err := decodeWorkshopRequest(string(body))
	if err != nil || request.RequestedAt.Unix() != 1788415147 {
		t.Fatalf("request = %#v, %v", request, err)
	}
}

func TestWorkshopFinalAttemptAndActionableMetadataMessage(t *testing.T) {
	message := events.SQSMessage{Attributes: map[string]string{"ApproximateReceiveCount": "5"}}
	if !workshopFinalAttempt(message) {
		t.Fatal("fifth receive was not treated as the final bounded attempt")
	}
	notice := workshopResolutionUserMessage(domain.WorkshopMetadataError{Code: domain.WorkshopMetadataTransient, Retryable: true}, true)
	for _, want := range []string{"left unchanged", "Wait", "public", "submit"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice %q omitted %q", notice, want)
		}
	}
}

func TestWorkshopCollectionLimitMessageExplainsRecovery(t *testing.T) {
	notice := workshopResolutionUserMessage(domain.WorkshopMetadataError{Code: domain.WorkshopMetadataCollectionLimit}, false)
	for _, want := range []string{"50", "Split", "submit"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice %q omitted %q", notice, want)
		}
	}
}

func TestWorkshopNestedCollectionMessageExplainsDirectChildren(t *testing.T) {
	notice := workshopRecordUserMessage(fmt.Errorf("%w: %w", domain.ErrPermanentWorkshopRejection, domain.ErrWorkshopNestedOnly), domain.WorkshopTargetMods, false)
	if !strings.Contains(notice, "Nested collections are not supported") || !strings.Contains(notice, "direct children") {
		t.Fatalf("notice = %q", notice)
	}
}

func TestWorkshopRecordMessagesGiveUserCorrectRecovery(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{domain.ErrConflict, "Review `/rb status`"},
		{domain.ErrWorkflowLocked, "Wait for the current"},
		{domain.ErrForbidden, "configured server and channel"},
		{domain.ErrWorkshopSnapshotLimit, "uploaded preset"},
		{domain.ErrPersistenceInvariant, "submitting the link repeatedly will not help"},
		{errors.New("s3 unavailable"), "contact an operator"},
	}
	for _, test := range tests {
		notice := workshopRecordUserMessage(test.err, domain.WorkshopTargetMods, true)
		if !strings.Contains(notice, test.want) {
			t.Errorf("notice %q omitted %q", notice, test.want)
		}
	}
}

func TestPersistenceInvariantIsTerminalWorkshopRecordError(t *testing.T) {
	if !permanentWorkshopRecordError(fmt.Errorf("save: %w", domain.ErrPersistenceInvariant)) {
		t.Fatal("persistence invariant would be retried through the nine-minute visibility timeout")
	}
}

func TestStaleWorkshopCallbackErrorsAreIgnored(t *testing.T) {
	for _, err := range []error{domain.ErrForbidden, domain.ErrNotFound, domain.ErrConflict} {
		if !ignorableWorkshopCallbackError(fmt.Errorf("callback: %w", err)) {
			t.Fatalf("callback error %v would trigger EventBridge retries", err)
		}
	}
	if ignorableWorkshopCallbackError(errors.New("SSM unavailable")) {
		t.Fatal("transient callback failure was incorrectly ignored")
	}
}
