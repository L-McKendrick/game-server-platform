package provisioning

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/app/failurestate"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const (
	ActionPrepare      = "prepare"
	ActionEnsure       = "ensure_instance"
	ActionObserve      = "observe_instance"
	ActionCheckManaged = "check_managed"
	ActionComplete     = "complete"
	ActionFail         = "fail"
)

type Clock interface{ Now() time.Time }
type IDGenerator interface {
	New(time.Time) (string, error)
}

type Config struct {
	Project              string
	Environment          string
	AMIID                string
	InstanceType         string
	SubnetID             string
	GameSecurityGroupID  string
	VoiceSecurityGroupID string
	InstanceProfile      string
	RootVolumeGiB        int32
	DataVolumeGiB        int32
	MaxProvisioned       int
}

func (config Config) Validate() error {
	switch {
	case strings.TrimSpace(config.Project) == "", strings.TrimSpace(config.Environment) == "":
		return fmt.Errorf("project and environment are required")
	case strings.TrimSpace(config.AMIID) == "", strings.TrimSpace(config.InstanceType) == "":
		return fmt.Errorf("AMI and instance type are required")
	case strings.TrimSpace(config.SubnetID) == "", strings.TrimSpace(config.GameSecurityGroupID) == "":
		return fmt.Errorf("subnet and game security group are required")
	case strings.TrimSpace(config.InstanceProfile) == "":
		return fmt.Errorf("instance profile is required")
	case config.RootVolumeGiB < 8 || config.RootVolumeGiB > 100:
		return fmt.Errorf("root volume size must be between 8 and 100 GiB")
	case config.DataVolumeGiB < 20 || config.DataVolumeGiB > 500:
		return fmt.Errorf("data volume size must be between 20 and 500 GiB")
	case config.MaxProvisioned < 1 || config.MaxProvisioned > 20:
		return fmt.Errorf("max provisioned sessions must be between 1 and 20")
	default:
		return nil
	}
}

type TaskRequest struct {
	Action        string `json:"action"`
	SessionID     string `json:"session_id"`
	WorkflowID    string `json:"workflow_id"`
	CorrelationID string `json:"correlation_id"`
	ErrorCode     string `json:"error_code,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
}

type TaskResult struct {
	SessionID  string `json:"session_id"`
	WorkflowID string `json:"workflow_id"`
	Ready      bool   `json:"ready,omitempty"`
	Managed    bool   `json:"managed,omitempty"`
	State      string `json:"state,omitempty"`
	Warning    string `json:"warning,omitempty"`
}

type Service struct {
	sessions      ports.SessionRepository
	stages        ports.ProvisioningRepository
	workflows     ports.WorkflowRepository
	compute       ports.ComputeProvisioner
	notifications ports.NotificationQueue
	ids           IDGenerator
	clock         Clock
	config        Config
}

func NewService(sessions ports.SessionRepository, stages ports.ProvisioningRepository, workflows ports.WorkflowRepository, compute ports.ComputeProvisioner, notifications ports.NotificationQueue, ids IDGenerator, clock Clock, config Config) (*Service, error) {
	if sessions == nil || stages == nil || workflows == nil || compute == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("provisioning dependencies are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Service{sessions: sessions, stages: stages, workflows: workflows, compute: compute, notifications: notifications, ids: ids, clock: clock, config: config}, nil
}

func (service *Service) Handle(ctx context.Context, request TaskRequest) (TaskResult, error) {
	if strings.TrimSpace(request.SessionID) == "" || strings.TrimSpace(request.WorkflowID) == "" {
		return TaskResult{}, fmt.Errorf("session and workflow IDs are required")
	}
	switch request.Action {
	case ActionPrepare:
		return service.prepare(ctx, request)
	case ActionEnsure:
		return service.ensure(ctx, request)
	case ActionObserve:
		return service.observe(ctx, request)
	case ActionCheckManaged:
		return service.checkManaged(ctx, request)
	case ActionComplete:
		return service.complete(ctx, request)
	case ActionFail:
		return service.fail(ctx, request)
	default:
		return TaskResult{}, fmt.Errorf("unsupported provisioning action %q", request.Action)
	}
}

func (service *Service) prepare(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	if session.LifecycleState == domain.StateProvisioning && session.Infrastructure.CapacitySlotID != "" {
		return result(session, workflow), nil
	}
	slot, err := service.stages.AcquireCapacitySlot(ctx, session.ID, workflow.ID, service.config.MaxProvisioned, service.clock.Now())
	if err != nil {
		return TaskResult{}, err
	}
	expectedVersion := session.Version
	if err := session.BeginInfrastructureProvisioning(workflow.ID, slot, service.clock.Now()); err != nil {
		return TaskResult{}, err
	}
	if err := service.saveStage(ctx, session, expectedVersion, workflow, "CapacityReserved"); err != nil {
		return TaskResult{}, err
	}
	return result(session, workflow), nil
}

func (service *Service) ensure(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	launch := service.launchRequest(session)
	observation, err := service.compute.EnsureInstance(ctx, launch, session.Infrastructure.InstanceID)
	if err != nil {
		return TaskResult{}, err
	}
	if session.Infrastructure.InstanceID == observation.InstanceID && observation.InstanceID != "" {
		return resultWithObservation(session, workflow, observation), nil
	}
	infrastructure := domain.Infrastructure{
		CapacitySlotID: session.Infrastructure.CapacitySlotID, AvailabilityZone: observation.AvailabilityZone,
		SubnetID: service.config.SubnetID, SecurityGroupIDs: launch.SecurityGroupIDs,
		InstanceProfile: service.config.InstanceProfile, AMIID: service.config.AMIID,
		InstanceType: service.config.InstanceType, InstanceID: observation.InstanceID,
		DataVolumeID: observation.DataVolumeID, PublicIPv4: observation.PublicIPv4,
		LastObservedAt: service.clock.Now().UTC(),
	}
	expectedVersion := session.Version
	if err := session.RecordInfrastructureLaunch(workflow.ID, infrastructure, service.clock.Now()); err != nil {
		return TaskResult{}, err
	}
	if err := service.saveStage(ctx, session, expectedVersion, workflow, "InstanceLaunched"); err != nil {
		return TaskResult{}, err
	}
	return resultWithObservation(session, workflow, observation), nil
}

func (service *Service) observe(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	if session.Infrastructure.InstanceID == "" {
		return TaskResult{}, fmt.Errorf("instance ID has not been recorded")
	}
	observation, err := service.compute.ObserveInstance(ctx, session.Infrastructure.InstanceID)
	if err != nil {
		return TaskResult{}, err
	}
	ready := observation.State == "running" && observation.DataVolumeID != ""
	if ready && (session.Infrastructure.DataVolumeID != observation.DataVolumeID || session.Infrastructure.PublicIPv4 != observation.PublicIPv4) {
		infrastructure := session.Infrastructure
		infrastructure.DataVolumeID = observation.DataVolumeID
		infrastructure.PublicIPv4 = observation.PublicIPv4
		infrastructure.AvailabilityZone = observation.AvailabilityZone
		infrastructure.LastObservedAt = service.clock.Now().UTC()
		expectedVersion := session.Version
		if err := session.RecordInfrastructureLaunch(workflow.ID, infrastructure, service.clock.Now()); err != nil {
			return TaskResult{}, err
		}
		if err := service.saveStage(ctx, session, expectedVersion, workflow, "InstanceRunning"); err != nil {
			return TaskResult{}, err
		}
	}
	response := resultWithObservation(session, workflow, observation)
	response.Ready = ready
	return response, nil
}

func (service *Service) checkManaged(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	managed, err := service.compute.IsManaged(ctx, session.Infrastructure.InstanceID)
	if err != nil {
		return TaskResult{}, err
	}
	response := result(session, workflow)
	response.Managed = managed
	return response, nil
}

func (service *Service) complete(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.loadRecords(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	if workflow.Status == domain.WorkflowSucceeded {
		return result(session, workflow), nil
	}
	if session.ActiveWorkflowID != workflow.ID || workflow.Type != "ProvisionSession" {
		return TaskResult{}, domain.ErrConflict
	}
	expectedVersion := session.Version
	now := service.clock.Now().UTC()
	session.ClearFailure()
	if err := session.CompleteInfrastructureProvisioning(workflow.ID, now); err != nil {
		return TaskResult{}, err
	}
	workflow.Status = domain.WorkflowSucceeded
	workflow.CurrentStage = "InfrastructureReady"
	workflow.CompletedAt = now
	eventID, err := service.ids.New(now)
	if err != nil {
		return TaskResult{}, err
	}
	event := domain.NewProvisioningEvent(eventID, domain.EventInfrastructureReady, "InfrastructureReady", workflow, session, now)
	if err := service.workflows.CompleteWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return TaskResult{}, err
	}
	response := result(session, workflow)
	if notifyErr := sessioncard.EnqueueProgress(ctx, service.notifications, session, workflow, now); notifyErr != nil {
		response.Warning = notifyErr.Error()
	}
	return response, nil
}

func (service *Service) fail(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.loadRecords(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	if workflow.Status == domain.WorkflowFailed {
		return result(session, workflow), nil
	}
	if session.ActiveWorkflowID != workflow.ID || workflow.Type != "ProvisionSession" {
		return TaskResult{}, domain.ErrConflict
	}
	slotID := session.Infrastructure.CapacitySlotID
	releaseSlot := false
	warning := ""
	if session.Infrastructure.InstanceID == "" {
		launch := service.launchRequest(session)
		observation, found, discoveryErr := service.compute.FindInstance(ctx, launch)
		switch {
		case discoveryErr != nil:
			// Preserve capacity on an ambiguous EC2 outcome. Reconciliation may
			// be needed, but a failed discovery must never permit an over-quota launch.
			warning = "capacity slot retained because EC2 discovery failed: " + discoveryErr.Error()
		case found && slotID != "":
			session.Infrastructure = domain.Infrastructure{
				CapacitySlotID: slotID, AvailabilityZone: observation.AvailabilityZone,
				SubnetID: service.config.SubnetID, SecurityGroupIDs: launch.SecurityGroupIDs,
				InstanceProfile: service.config.InstanceProfile, AMIID: service.config.AMIID,
				InstanceType: service.config.InstanceType, InstanceID: observation.InstanceID,
				DataVolumeID: observation.DataVolumeID, PublicIPv4: observation.PublicIPv4,
				LastObservedAt: service.clock.Now().UTC(),
			}
		case !found:
			releaseSlot = true
		}
	}
	if releaseSlot {
		session.Infrastructure = domain.Infrastructure{}
	}
	expectedVersion := session.Version
	now := service.clock.Now().UTC()
	if err := failurestate.Record(&session, workflow, request.ErrorCode, "ERR_PROVISIONING_FAILED", workflow.CurrentStage,
		"Infrastructure provisioning stopped before readiness was confirmed.", failurestate.Impact(session, warning != "" || strings.EqualFold(strings.TrimSpace(request.ErrorCode), "ERR_AMBIGUOUS_LAUNCH")), now); err != nil {
		return TaskResult{}, err
	}
	if err := session.FailInfrastructureProvisioning(workflow.ID, now); err != nil {
		return TaskResult{}, err
	}
	workflow.Status = domain.WorkflowFailed
	workflow.CurrentStage = "Failed"
	workflow.ErrorCode = bounded(request.ErrorCode, 100, "ERR_PROVISIONING_FAILED")
	workflow.ErrorMessage = bounded(request.ErrorMessage, 500, "Infrastructure provisioning failed")
	workflow.CompletedAt = now
	eventID, err := service.ids.New(now)
	if err != nil {
		return TaskResult{}, err
	}
	event := domain.NewProvisioningEvent(eventID, domain.EventProvisioningFailed, "Failed", workflow, session, now)
	if err := service.workflows.CompleteWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return TaskResult{}, err
	}
	if releaseSlot && slotID != "" {
		if err := service.stages.ReleaseCapacitySlot(ctx, slotID, session.ID); err != nil {
			response := result(session, workflow)
			response.Warning = err.Error()
			return response, nil
		}
	}
	response := result(session, workflow)
	response.Warning = warning
	if notifyErr := sessioncard.EnqueueProgress(ctx, service.notifications, session, workflow, now); notifyErr != nil && response.Warning == "" {
		response.Warning = notifyErr.Error()
	}
	return response, nil
}

func (service *Service) launchRequest(session domain.Session) domain.ComputeLaunchRequest {
	securityGroups := []string{service.config.GameSecurityGroupID}
	if session.TeamSpeakEnabled && strings.TrimSpace(service.config.VoiceSecurityGroupID) != "" {
		securityGroups = append(securityGroups, service.config.VoiceSecurityGroupID)
	}
	return domain.ComputeLaunchRequest{
		SessionID: session.ID, SessionName: session.DisplayName, SessionSlug: session.Slug,
		GameType: session.GameType, Project: service.config.Project,
		Environment: service.config.Environment, AMIID: service.config.AMIID,
		InstanceType: service.config.InstanceType, SubnetID: service.config.SubnetID,
		SecurityGroupIDs: securityGroups, InstanceProfile: service.config.InstanceProfile,
		RootVolumeGiB: service.config.RootVolumeGiB, DataVolumeGiB: service.config.DataVolumeGiB,
	}
}

func (service *Service) load(ctx context.Context, request TaskRequest) (domain.Session, domain.Workflow, error) {
	session, workflow, err := service.loadRecords(ctx, request)
	if err != nil {
		return domain.Session{}, domain.Workflow{}, err
	}
	if session.ActiveWorkflowID != workflow.ID || workflow.Type != "ProvisionSession" {
		return domain.Session{}, domain.Workflow{}, domain.ErrConflict
	}
	return session, workflow, nil
}

func (service *Service) loadRecords(ctx context.Context, request TaskRequest) (domain.Session, domain.Workflow, error) {
	session, err := service.sessions.Get(ctx, strings.TrimSpace(request.SessionID))
	if err != nil {
		return domain.Session{}, domain.Workflow{}, err
	}
	workflow, err := service.workflows.GetWorkflow(ctx, session.ID, strings.TrimSpace(request.WorkflowID))
	if err != nil {
		return domain.Session{}, domain.Workflow{}, err
	}
	return session, workflow, nil
}

func (service *Service) saveStage(ctx context.Context, session domain.Session, expectedVersion int64, workflow domain.Workflow, stage string) error {
	now := service.clock.Now().UTC()
	eventID, err := service.ids.New(now)
	if err != nil {
		return err
	}
	event := domain.NewProvisioningEvent(eventID, domain.EventProvisioningStage, stage, workflow, session, now)
	return service.stages.SaveProvisioningStage(ctx, session, expectedVersion, event)
}

func result(session domain.Session, workflow domain.Workflow) TaskResult {
	return TaskResult{SessionID: session.ID, WorkflowID: workflow.ID, State: string(session.LifecycleState)}
}

func resultWithObservation(session domain.Session, workflow domain.Workflow, observation domain.ComputeObservation) TaskResult {
	response := result(session, workflow)
	response.State = observation.State
	return response
}

func bounded(value string, maximum int, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if len(value) > maximum {
		value = value[:maximum]
	}
	return value
}
