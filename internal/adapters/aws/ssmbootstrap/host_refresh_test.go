package ssmbootstrap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type publicationAccess struct {
	fakeHostCommandAccess
	calls int
	scope domain.HostAccessScope
	fail  bool
}

func (access *publicationAccess) PublishHostWorkshop(_ context.Context, scope domain.HostAccessScope) error {
	access.calls++
	if !scope.Equal(access.scope) || access.fail {
		return errors.New("publication not accepted")
	}
	return nil
}

func TestSuccessfulObservationRequiresOwnedWorkshopPublication(t *testing.T) {
	for _, scenario := range []string{"valid", "publication-error", "wrong-instance", "wrong-command", "missing-marker", "failed-command"} {
		t.Run(scenario, func(t *testing.T) {
			scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-1", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: time.Now().Add(time.Hour)}
			payload, _ := json.Marshal(scope)
			command := types.Command{CommandId: aws.String("command-1"), InstanceIds: []string{"i-1"}, DocumentName: aws.String(documentName), Parameters: map[string][]string{"commands": {"# GSP_HOST_ACCESS:" + base64.StdEncoding.EncodeToString(payload)}}}
			access := &publicationAccess{scope: scope, fail: scenario == "publication-error"}
			status := types.CommandInvocationStatusSuccess
			switch scenario {
			case "wrong-instance":
				command.InstanceIds = []string{"i-other"}
			case "wrong-command":
				command.CommandId = aws.String("other")
			case "missing-marker":
				command.Parameters["commands"] = []string{"echo done"}
			case "failed-command":
				status = types.CommandInvocationStatusFailed
			}
			client := &fakeSSM{invocation: &ssm.GetCommandInvocationOutput{Status: status}, commands: &ssm.ListCommandsOutput{Commands: []types.Command{command}}}
			config := testConfig()
			config.HostAccess = access
			runner, err := New(client, config)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := runner.Observe(context.Background(), "i-1", "command-1")
			if scenario == "valid" {
				if err != nil || observed.Status != "Success" || access.calls != 1 {
					t.Fatal("owned success not published", err)
				}
			} else if scenario == "failed-command" {
				if err != nil || access.calls != 0 || observed.Status != "Failed" {
					t.Fatal("failed command attempted publication", err)
				}
			} else {
				if err == nil || observed.Status == "Success" {
					t.Fatal("unverified publication reported success")
				}
				if scenario != "publication-error" && access.calls != 0 {
					t.Fatal("unowned command reached publication")
				}
			}
		})
	}
}

type renewalAccess struct {
	fakeHostCommandAccess
	reference ports.HostAccessReference
	records   int
}

type completionBroker struct {
	fakeSteamBroker
	reference, outcome string
	err                error
}

func (broker *completionBroker) Complete(_ context.Context, reference, outcome string) error {
	broker.reference, broker.outcome = reference, outcome
	return broker.err
}

func TestPublicationFailureStillFinalizesSteamExchange(t *testing.T) {
	for _, completionFails := range []bool{false, true} {
		t.Run(fmt.Sprint(completionFails), func(t *testing.T) {
			scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-1", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: time.Now().Add(time.Hour)}
			payload, _ := json.Marshal(scope)
			broker := &completionBroker{}
			if completionFails {
				broker.err = errors.New("broker unavailable")
			}
			client := &fakeSSM{invocation: &ssm.GetCommandInvocationOutput{Status: types.CommandInvocationStatusSuccess, StandardOutputContent: aws.String("GSP_STEAM_EXCHANGE:operation:bootstrap:exchange\n")}, commands: &ssm.ListCommandsOutput{Commands: []types.Command{{CommandId: aws.String("command-1"), InstanceIds: []string{"i-1"}, DocumentName: aws.String(documentName), Parameters: map[string][]string{"commands": {"# GSP_HOST_ACCESS:" + base64.StdEncoding.EncodeToString(payload)}}}}}}
			config := testConfig()
			config.HostAccess = &publicationAccess{scope: scope, fail: true}
			config.SteamBroker = broker
			runner, err := New(client, config)
			if err != nil {
				t.Fatal(err)
			}
			status, err := runner.Observe(context.Background(), "i-1", "command-1")
			if err == nil || status.Status == "Success" || !strings.Contains(err.Error(), "publication not accepted") || broker.reference != "operation:bootstrap:exchange" || broker.outcome != "succeeded" {
				t.Fatal("publication failure skipped exchange finalization or reported success", err)
			}
			if completionFails && !errors.Is(err, broker.err) {
				t.Fatal("exchange finalization failure was lost", err)
			}
		})
	}
}

func (access *renewalAccess) RefreshHostCommand(_ context.Context, scope domain.HostAccessScope) (ports.HostAccessReference, error) {
	if !scope.Equal(access.reference.Scope) {
		return ports.HostAccessReference{}, domain.ErrConflict
	}
	return access.reference, nil
}
func (access *renewalAccess) RecordHostRefresh(_ context.Context, scope domain.HostAccessScope, generation int64) error {
	if !scope.Equal(access.reference.Scope) || generation != access.reference.Generation {
		return domain.ErrConflict
	}
	access.records++
	return nil
}

type renewalSSM struct {
	fakeSSM
	original types.Command
	refresh  *types.Command
}

func (client *renewalSSM) ListCommands(_ context.Context, input *ssm.ListCommandsInput, _ ...func(*ssm.Options)) (*ssm.ListCommandsOutput, error) {
	if aws.ToString(input.CommandId) != "" {
		return &ssm.ListCommandsOutput{Commands: []types.Command{client.original}}, nil
	}
	if client.refresh != nil {
		return &ssm.ListCommandsOutput{Commands: []types.Command{*client.refresh}}, nil
	}
	return &ssm.ListCommandsOutput{}, nil
}

func TestHostRefreshConfirmsInstallAndRetriesFailedDelivery(t *testing.T) {
	for _, scenario := range []string{"new", "pending", "succeeded", "failed", "wrong-instance"} {
		t.Run(scenario, func(t *testing.T) {
			scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-1", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: time.Now().Add(time.Hour)}
			payload, _ := json.Marshal(scope)
			access := &renewalAccess{reference: ports.HostAccessReference{SchemaVersion: domain.HostAccessSchemaVersion, Scope: scope, Generation: 2, URL: "https://assets.example.test/manifest", SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 100, ExpiresAt: time.Now().Add(10 * time.Minute)}}
			client := &renewalSSM{original: types.Command{CommandId: aws.String("command-1"), InstanceIds: []string{"i-1"}, DocumentName: aws.String(documentName), Parameters: map[string][]string{"commands": {"# GSP_HOST_ACCESS:" + base64.StdEncoding.EncodeToString(payload)}}}}
			if scenario == "wrong-instance" {
				client.original.InstanceIds = []string{"i-other"}
			}
			if scenario != "new" && scenario != "wrong-instance" {
				status := types.CommandStatusPending
				if scenario == "succeeded" {
					status = types.CommandStatusSuccess
				}
				if scenario == "failed" {
					status = types.CommandStatusFailed
				}
				client.refresh = &types.Command{Comment: aws.String("gsp:host-refresh:attempt:2"), InstanceIds: []string{"i-1"}, DocumentName: aws.String(documentName), Status: status}
			}
			config := testConfig()
			config.HostAccess = access
			runner, err := New(client, config)
			if err != nil {
				t.Fatal(err)
			}
			err = runner.renewHostAccess(context.Background(), "i-1", "command-1")
			if scenario == "wrong-instance" {
				if err == nil || client.sent != nil || access.records != 0 {
					t.Fatal("wrong instance refreshed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantSend := scenario == "new" || scenario == "failed"
			if (client.sent != nil) != wantSend {
				t.Fatal("incorrect refresh retry")
			}
			if scenario == "succeeded" {
				if access.records != 1 {
					t.Fatal("successful install not recorded")
				}
			} else if access.records != 0 {
				t.Fatal("delivery recorded without install success")
			}
			if client.sent != nil {
				if aws.ToString(client.sent.Comment) != "gsp:host-refresh:attempt:2" || client.sent.OutputS3BucketName != nil || len(client.sent.Parameters["commands"][0]) > domain.MaxHostAccessCommandBytes {
					t.Fatal("refresh transport not scoped/bounded")
				}
				assertBashSyntax(t, []byte(client.sent.Parameters["commands"][0]))
			}
		})
	}
}
