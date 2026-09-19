package ssmrestore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"strings"
	"testing"
	"time"
)

type recoveryAccess struct{ reference ports.HostAccessReference }

func (access recoveryAccess) PrepareRestoreArchive(context.Context, string, time.Duration) (ports.HostAccessReference, error) {
	return access.reference, nil
}
func (recoveryAccess) RefreshHostCommand(context.Context, domain.HostAccessScope) (ports.HostAccessReference, error) {
	return ports.HostAccessReference{}, nil
}
func (recoveryAccess) RecordHostRefresh(context.Context, domain.HostAccessScope, int64) error {
	return nil
}

type recoveryClient struct {
	testSSM
	calls   int
	command types.Command
}

func (client *recoveryClient) ListCommands(_ context.Context, input *ssm.ListCommandsInput, _ ...func(*ssm.Options)) (*ssm.ListCommandsOutput, error) {
	client.calls++
	if client.calls == 1 {
		return &ssm.ListCommandsOutput{NextToken: aws.String("next")}, nil
	}
	if aws.ToString(input.NextToken) != "next" {
		panic("missing pagination token")
	}
	return &ssm.ListCommandsOutput{Commands: []types.Command{client.command}}, nil
}

func TestRestoreRecoversOnlyExactOwnedScopeAcrossPages(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(strings.ToLower(map[bool]string{false: "owned", true: "changed"}[changed]), func(t *testing.T) {
			now := time.Now().UTC()
			digest := base64.StdEncoding.EncodeToString(make([]byte, 32))
			session := domain.Session{ID: "session", GuildID: "guild", LifecycleState: domain.StateRestoring, ActiveWorkflowID: "operation", ActiveWorkflowType: domain.RestoreWorkflowType, Infrastructure: domain.Infrastructure{InstanceID: "i-123", DataVolumeID: "vol-data"}, Archive: domain.ArchiveMetadata{ID: "previous", ObjectKey: "sessions/session/archives/previous/session.tar.gz", ManifestObjectKey: "sessions/session/archives/previous/manifest.json", SHA256: digest, ManifestSHA256: digest, SizeBytes: 42, ManifestSizeBytes: 42, Format: "tar+gzip", VerifiedAt: now}}
			scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", InstanceID: "i-123", AttemptID: "attempt", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(time.Hour)}
			marker := scope
			if changed {
				marker.OperationID = "other"
			}
			payload, _ := json.Marshal(marker)
			client := &recoveryClient{command: types.Command{CommandId: aws.String("owned-command"), Comment: aws.String("gsp:restore:attempt"), DocumentName: aws.String("AWS-RunShellScript"), InstanceIds: []string{"i-123"}, Parameters: map[string][]string{"commands": {"# GSP_HOST_ACCESS:" + base64.StdEncoding.EncodeToString(payload)}}}}
			runner, _ := New(client, "assets", "us-west-2", 3600)
			runner.WithArchiveAccess(recoveryAccess{reference: ports.HostAccessReference{Scope: scope}})
			command, err := runner.Start(context.Background(), session)
			if changed {
				if err == nil || command != "" {
					t.Fatal("changed owned scope accepted")
				}
			} else if err != nil || command != "owned-command" {
				t.Fatal("owned command not recovered", err)
			}
			if client.calls != 2 {
				t.Fatal("did not paginate")
			}
		})
	}
}
