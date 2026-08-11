package domain

import (
	"fmt"
	"strings"
	"time"
)

type WorkflowStatus string

const (
	WorkflowPending   WorkflowStatus = "PENDING"
	WorkflowRunning   WorkflowStatus = "RUNNING"
	WorkflowSucceeded WorkflowStatus = "SUCCEEDED"
	WorkflowFailed    WorkflowStatus = "FAILED"
	WorkflowCancelled WorkflowStatus = "CANCELLED"
)

func (status WorkflowStatus) Valid() bool {
	switch status {
	case WorkflowPending, WorkflowRunning, WorkflowSucceeded, WorkflowFailed, WorkflowCancelled:
		return true
	default:
		return false
	}
}

type Workflow struct {
	ID                string
	SessionID         string
	Type              string
	Status            WorkflowStatus
	RequestedBy       string
	CorrelationID     string
	ExpectedVersion   int64
	ExecutionARN      string
	CurrentStage      string
	ErrorCode         string
	ErrorMessage      string
	StartedAt         time.Time
	CompletedAt       time.Time
	LeaseExpiresAt    time.Time
	CancelRequestedAt time.Time
	CancelRequestedBy string
}

func (workflow Workflow) Validate() error {
	switch {
	case strings.TrimSpace(workflow.ID) == "":
		return fmt.Errorf("workflow ID is required")
	case strings.TrimSpace(workflow.SessionID) == "":
		return fmt.Errorf("workflow session ID is required")
	case strings.TrimSpace(workflow.Type) == "":
		return fmt.Errorf("workflow type is required")
	case !workflow.Status.Valid():
		return fmt.Errorf("invalid workflow status %q", workflow.Status)
	case strings.TrimSpace(workflow.RequestedBy) == "":
		return fmt.Errorf("workflow requester is required")
	case strings.TrimSpace(workflow.CorrelationID) == "":
		return fmt.Errorf("workflow correlation ID is required")
	case workflow.ExpectedVersion < 1:
		return fmt.Errorf("expected session version must be positive")
	case workflow.StartedAt.IsZero():
		return fmt.Errorf("workflow start timestamp is required")
	case workflow.LeaseExpiresAt.IsZero() || !workflow.LeaseExpiresAt.After(workflow.StartedAt):
		return fmt.Errorf("workflow lease expiration must follow its start")
	default:
		return nil
	}
}

type CommandActor struct {
	DiscordUserID string   `json:"discord_user_id"`
	GuildID       string   `json:"guild_id"`
	ChannelID     string   `json:"channel_id"`
	Roles         []string `json:"roles"`
}

const (
	CommandStartSession     = "StartSession"
	CommandBootstrapServer  = "BootstrapGameServer"
	CommandSleepSession     = "SleepSession"
	CommandWakeSession      = "WakeSession"
	CommandArchiveSession   = "ArchiveSession"
	CommandRestoreSession   = "RestoreSession"
	CommandDestroySession   = "DestroySession"
	CommandReconcileSession = "ReconcileSession"
)

var commandWorkflowTypes = map[string]string{
	CommandStartSession:     "ProvisionSession",
	CommandBootstrapServer:  "BootstrapGameServer",
	CommandSleepSession:     "SleepSession",
	CommandWakeSession:      "WakeSession",
	CommandArchiveSession:   "ArchiveSession",
	CommandRestoreSession:   "RestoreSession",
	CommandDestroySession:   "DestroySession",
	CommandReconcileSession: "ReconcileSession",
}

type CommandEnvelope struct {
	SchemaVersion  int               `json:"schema_version"`
	CommandID      string            `json:"command_id"`
	CommandType    string            `json:"command_type"`
	RequestedAt    time.Time         `json:"requested_at"`
	Actor          CommandActor      `json:"actor"`
	SessionID      string            `json:"session_id"`
	IdempotencyKey string            `json:"idempotency_key"`
	CorrelationID  string            `json:"correlation_id"`
	Parameters     map[string]string `json:"parameters"`
}

func (command CommandEnvelope) Validate() error {
	switch {
	case command.SchemaVersion != 1:
		return fmt.Errorf("unsupported command schema version %d", command.SchemaVersion)
	case strings.TrimSpace(command.CommandID) == "":
		return fmt.Errorf("command ID is required")
	case strings.TrimSpace(command.CommandType) == "":
		return fmt.Errorf("command type is required")
	case commandWorkflowTypes[command.CommandType] == "":
		return fmt.Errorf("unsupported command type %q", command.CommandType)
	case command.RequestedAt.IsZero():
		return fmt.Errorf("command request timestamp is required")
	case strings.TrimSpace(command.Actor.DiscordUserID) == "":
		return fmt.Errorf("command actor is required")
	case strings.TrimSpace(command.Actor.GuildID) == "":
		return fmt.Errorf("command guild is required")
	case strings.TrimSpace(command.Actor.ChannelID) == "":
		return fmt.Errorf("command channel is required")
	case strings.TrimSpace(command.SessionID) == "":
		return fmt.Errorf("command session ID is required")
	case strings.TrimSpace(command.IdempotencyKey) == "":
		return fmt.Errorf("command idempotency key is required")
	case strings.TrimSpace(command.CorrelationID) == "":
		return fmt.Errorf("command correlation ID is required")
	default:
		return nil
	}
}

func (command CommandEnvelope) WorkflowType() (string, error) {
	if err := command.Validate(); err != nil {
		return "", err
	}
	return commandWorkflowTypes[command.CommandType], nil
}
