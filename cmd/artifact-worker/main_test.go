package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

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

func TestWorkshopRecordMessagesGiveUserCorrectRecovery(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{domain.ErrConflict, "Review `/rb status`"},
		{domain.ErrWorkflowLocked, "Wait for the current"},
		{domain.ErrForbidden, "configured server and channel"},
		{domain.ErrWorkshopSnapshotLimit, "uploaded preset"},
		{errors.New("s3 unavailable"), "contact an operator"},
	}
	for _, test := range tests {
		notice := workshopRecordUserMessage(test.err, domain.WorkshopTargetMods, true)
		if !strings.Contains(notice, test.want) {
			t.Errorf("notice %q omitted %q", notice, test.want)
		}
	}
}
