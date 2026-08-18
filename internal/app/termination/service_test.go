package termination

import (
	"context"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type destroyer struct{ terminated, volumeDeleted bool }

func (value *destroyer) TerminateInstance(context.Context, string, string) error {
	value.terminated = true
	return nil
}
func (*destroyer) InstanceTerminated(context.Context, string, string) (bool, error) { return true, nil }
func (value *destroyer) DeleteVolume(context.Context, string, string) error {
	value.volumeDeleted = true
	return nil
}
func (*destroyer) VolumeDeleted(context.Context, string, string) (bool, error) { return true, nil }

type cleaner struct{ sessionID string }

func (value *cleaner) DeleteSessionObjects(_ context.Context, sessionID string) (int, error) {
	value.sessionID = sessionID
	return 3, nil
}

type clock struct{ now time.Time }

func (value clock) Now() time.Time { return value.now }

type ids struct{ value string }

func (value ids) New(time.Time) (string, error) { return value.value, nil }

func TestServicePermanentlyDeletesResourcesAndLeavesTombstone(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", Description: "Weekly co-op night", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateRunning, domain.StateRunning, domain.StateRunning, domain.HealthHealthy
	session.MissionObjectKey, session.PresetObjectKey = "sessions/session-1/input/mission.pbo", "sessions/session-1/input/preset.html"
	session.Infrastructure = domain.Infrastructure{CapacitySlotID: "slot-1", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now}
	event := domain.NewSessionCreatedEvent("create", "create-correlation", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	idempotency, _ := domain.NewCompletedIdempotencyRecord("create", "hash", session.ID, now, time.Hour)
	if err := repository.Create(ctx, session, event, idempotency); err != nil {
		t.Fatal(err)
	}
	expected := session.Version
	if err := session.BeginTermination("terminate-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	workflow := domain.Workflow{ID: "terminate-1", SessionID: session.ID, Type: domain.TerminationWorkflowType, Status: domain.WorkflowRunning, RequestedBy: "owner-1", CorrelationID: "terminate-correlation", ExpectedVersion: expected, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}
	start := domain.NewWorkflowEvent("start", domain.EventTerminationStarted, workflow.CorrelationID, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, workflow, now)
	if err := repository.AcquireWorkflow(ctx, session, expected, workflow, start); err != nil {
		t.Fatal(err)
	}

	destroy := &destroyer{}
	clean := &cleaner{}
	service, err := NewService(repository, repository, repository, destroy, clean, nil, ids{"complete"}, clock{now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{ActionTerminateInstance, ActionObserveTermination, ActionDeleteVolume, ActionObserveVolume} {
		if _, err := service.Handle(ctx, TaskRequest{Action: action, SessionID: session.ID, WorkflowID: workflow.ID}); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	deleted, err := service.Handle(ctx, TaskRequest{Action: ActionDeleteObjects, SessionID: session.ID, WorkflowID: workflow.ID})
	if err != nil || deleted.ObjectsDeleted != 3 {
		t.Fatalf("delete objects = %#v, %v", deleted, err)
	}
	completed, err := service.Handle(ctx, TaskRequest{Action: ActionComplete, SessionID: session.ID, WorkflowID: workflow.ID, ObjectsDeleted: deleted.ObjectsDeleted})
	if err != nil || !completed.Succeeded {
		t.Fatalf("complete = %#v, %v", completed, err)
	}
	stored, err := repository.Get(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !destroy.terminated || !destroy.volumeDeleted || clean.sessionID != session.ID || stored.LifecycleState != domain.StateDeleted || !stored.Infrastructure.Empty() || stored.MissionObjectKey != "" || stored.PresetObjectKey != "" {
		t.Fatalf("destroy = %#v, clean = %#v, session = %#v", destroy, clean, stored)
	}
	if stored.DisplayName != "Saturday Arma" || stored.Slug != "saturday-arma" || stored.Description != "Weekly co-op night" {
		t.Fatalf("tombstone readable identity = %#v", stored)
	}
	events := repository.Events(session.ID)
	terminated := events[len(events)-1]
	if terminated.Data["display_name"] != stored.DisplayName || terminated.Data["slug"] != stored.Slug || terminated.Data["description"] != stored.Description {
		t.Fatalf("termination event identity = %#v", terminated.Data)
	}
}
