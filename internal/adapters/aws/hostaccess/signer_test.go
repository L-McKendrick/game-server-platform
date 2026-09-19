package hostaccess_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/hostaccess"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
)

type expiringCredentials struct {
	calls  int
	expiry time.Time
}

func (provider *expiringCredentials) Retrieve(context.Context) (aws.Credentials, error) {
	provider.calls++
	return aws.Credentials{AccessKeyID: "key", SecretAccessKey: "secret", SessionToken: "token", CanExpire: true, Expires: provider.expiry}, nil
}

func TestSignerFreezesCredentialsAndProducesValidTransport(t *testing.T) {
	now := time.Now().UTC()
	scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(time.Hour)}
	digest := base64.StdEncoding.EncodeToString(make([]byte, 32))
	objects := []domain.HostObjectRequest{
		{Purpose: domain.HostObjectMission, Slot: "mission", Key: "sessions/session/input/mission.pbo", SHA256: digest, MaxBytes: 100},
		{Purpose: domain.HostObjectArchiveUpload, Slot: "archive", Key: "sessions/session/archives/operation/session.tar.gz", SHA256: digest, ContentType: "application/gzip", MinBytes: 7, MaxBytes: 7},
		{Purpose: domain.HostObjectProgress, Slot: "progress", Key: scope.StagingKey("progress"), ContentType: "text/plain", MinBytes: 1, MaxBytes: 100},
	}
	for _, object := range objects {
		t.Run(string(object.Purpose), func(t *testing.T) {
			provider := &expiringCredentials{expiry: now.Add(4 * time.Minute)}
			signer := hostaccess.NewSigner(aws.Config{Region: "us-west-2", Credentials: provider}, "assets")
			capability, err := signer.Sign(context.Background(), scope, object)
			if err != nil {
				t.Fatal(err)
			}
			if provider.calls != 1 {
				t.Fatalf("credential retrievals = %d", provider.calls)
			}
			if capability.ExpiresAt.After(provider.expiry.Add(-domain.HostAccessCredentialMargin)) {
				t.Fatal("credential expiry exceeded")
			}
			manifest := ports.HostAccessManifest{SchemaVersion: domain.HostAccessSchemaVersion, Scope: scope, Objects: []ports.HostObjectCapability{capability}}
			if capability.Method == "POST" {
				policy, err := base64.StdEncoding.DecodeString(capability.Form["policy"])
				if err != nil {
					t.Fatal(err)
				}
				var decoded struct {
					Conditions []json.RawMessage `json:"conditions"`
				}
				if json.Unmarshal(policy, &decoded) != nil {
					t.Fatal("invalid signed policy")
				}
				found := false
				for _, condition := range decoded.Conditions {
					var exact map[string]string
					if json.Unmarshal(condition, &exact) == nil && exact["tagging"] != "" && exact["tagging"] == capability.Form["tagging"] {
						found = true
					}
				}
				if !found || !strings.Contains(capability.Form["tagging"], "<Value>host-access</Value>") {
					t.Fatal("retention tag not enforced by signed policy")
				}
			}
			if _, err := manifest.Encode(time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSignerRejectsUnknownCredentialExpiry(t *testing.T) {
	provider := &expiringCredentials{}
	scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: time.Now().Add(time.Hour)}
	object := domain.HostObjectRequest{Purpose: domain.HostObjectProgress, Slot: "progress", Key: scope.StagingKey("progress"), ContentType: "text/plain", MinBytes: 1, MaxBytes: 100}
	_, err := hostaccess.NewSigner(aws.Config{Region: "us-west-2", Credentials: provider}, "assets").Sign(context.Background(), scope, object)
	if err == nil {
		t.Fatal("accepted unknown expiring credential lifetime")
	}
}
