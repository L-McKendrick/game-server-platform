package dynamodbstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

func TestGuildServerConfigRoundTripUsesConditionalRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	config := domain.GuildServerConfig{GuildID: "guild-1", Revision: 1, ObjectKey: "guilds/guild-1/server-config/revisions/000001-a/server.cfg", Filename: "private.cfg", SHA256: strings.Repeat("a", 64), SizeBytes: 20, UploadedBy: "admin-1", UpdatedAt: now}
	client := &fakeAPI{}
	repository := New(client, "metadata")
	stored, err := repository.SaveGuildServerConfig(context.Background(), config, 0)
	if err != nil || stored != config || client.putItemInput == nil || client.putItemInput.ConditionExpression == nil {
		t.Fatalf("stored=%#v input=%#v err=%v", stored, client.putItemInput, err)
	}
	client.getItemOutput = &dynamodb.GetItemOutput{Item: client.putItemInput.Item}
	read, err := repository.GetGuildServerConfig(context.Background(), "guild-1")
	if err != nil || read != config {
		t.Fatalf("read=%#v err=%v", read, err)
	}
}
