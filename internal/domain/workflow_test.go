package domain

import (
	"strings"
	"testing"
	"time"
)

func TestCommandEnvelopeMapsSupportedCommandToCanonicalWorkflow(t *testing.T) {
	t.Parallel()

	command := testCommandEnvelope()
	workflowType, err := command.WorkflowType()
	if err != nil {
		t.Fatalf("WorkflowType() returned error: %v", err)
	}
	if workflowType != "ProvisionSession" {
		t.Fatalf("workflow type = %q; want ProvisionSession", workflowType)
	}
}

func TestCommandEnvelopeRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	command := testCommandEnvelope()
	command.CommandType = "DoAnything"
	if err := command.Validate(); err == nil {
		t.Fatal("Validate() returned nil error for unsupported command")
	}
}

func TestCommandEnvelopeRejectsTransactionUnsafeCommandID(t *testing.T) {
	t.Parallel()

	command := testCommandEnvelope()
	command.CommandID = "1234567890123456789012345678901234567"
	if err := command.Validate(); err == nil {
		t.Fatal("Validate() returned nil error for a command ID longer than the DynamoDB transaction token limit")
	}
}

func TestBootstrapContinuationCommandIDIsDeterministicAndBounded(t *testing.T) {
	t.Parallel()
	first := BootstrapContinuationCommandID("provision-workflow-with-a-very-long-external-identifier")
	second := BootstrapContinuationCommandID("provision-workflow-with-a-very-long-external-identifier")
	other := BootstrapContinuationCommandID("another-provision-workflow")
	if first != second || first == other || len(first) != 36 || !strings.HasPrefix(first, "bootstrap-") {
		t.Fatalf("continuation IDs = %q, %q, %q", first, second, other)
	}
}

func testCommandEnvelope() CommandEnvelope {
	return CommandEnvelope{
		SchemaVersion: 1, CommandID: "command-1", CommandType: CommandStartSession,
		RequestedAt: time.Date(2026, 8, 8, 23, 0, 0, 0, time.UTC),
		Actor:       CommandActor{DiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"},
		SessionID:   "session-1", IdempotencyKey: "discord:command-1", CorrelationID: "correlation-1",
	}
}
