package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const hostLiveCopyLifetime = 150 * time.Second // Existing 30s delivery + 120s execution.

func liveCopyOperation(sessionID, instanceID, key string) string {
	digest := sha256.Sum256([]byte(sessionID + "\x00" + instanceID + "\x00" + key))
	return "live-copy-" + hex.EncodeToString(digest[:])[:26]
}

// SignLiveMission creates a short accepted-read capability without a fabricated
// lifecycle lock. Copying immutable accepted bytes is idempotent; this operation
// has no staged output or refresh/recovery state to persist.
func (issuer HostObjectIssuer) SignLiveMission(ctx context.Context, sessionID, key string) (ports.HostAccessManifest, error) {
	denied := fmt.Errorf("live mission access is not currently authorized")
	if issuer.Authority.Records == nil || issuer.Authority.Instances == nil || issuer.Signer == nil {
		return ports.HostAccessManifest{}, denied
	}
	session, err := issuer.Authority.Records.Get(ctx, sessionID)
	if err != nil || session.ID != sessionID {
		return ports.HostAccessManifest{}, denied
	}
	mission, ok := session.LiveMissionCopyTarget(key)
	if !ok {
		return ports.HostAccessManifest{}, denied
	}
	name := path.Base(mission.ObjectKey)
	if !strings.HasPrefix(mission.ObjectKey, path.Join("sessions", session.ID, "input", "missions")+"/") || len(name) <= 65 || name[64] != '-' || name[65:] != mission.Filename || hostBase64Digest(name[:64]) == "" {
		return ports.HostAccessManifest{}, denied
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ports.HostAccessManifest{}, denied
	}
	scope := domain.HostAccessScope{SessionID: session.ID, GuildID: session.GuildID, OperationID: liveCopyOperation(session.ID, session.Infrastructure.InstanceID, key), AttemptID: hex.EncodeToString(nonce[:]), InstanceID: session.Infrastructure.InstanceID, SnapshotSHA256: HostContentSnapshot(session), DeadlineAt: time.Now().UTC().Add(hostLiveCopyLifetime).Truncate(time.Second)}
	object := domain.HostObjectRequest{Purpose: domain.HostObjectMission, Slot: "mission", Key: mission.ObjectKey, SHA256: hostBase64Digest(name[:64]), MaxBytes: domain.MaximumWorkshopMissionBytes}
	if object.Validate(scope) != nil || issuer.Authority.ValidateLiveMission(ctx, scope, key, time.Now().UTC()) != nil {
		return ports.HostAccessManifest{}, denied
	}
	capability, err := issuer.Signer.Sign(ctx, scope, object)
	if err != nil {
		return ports.HostAccessManifest{}, fmt.Errorf("live mission access signing failed")
	}
	manifest := ports.HostAccessManifest{SchemaVersion: domain.HostAccessSchemaVersion, Scope: scope, Objects: []ports.HostObjectCapability{capability}}
	if _, err := manifest.Encode(time.Now().UTC()); err != nil {
		return ports.HostAccessManifest{}, denied
	}
	if issuer.Authority.ValidateLiveMission(ctx, scope, key, time.Now().UTC()) != nil {
		return ports.HostAccessManifest{}, denied
	}
	return manifest, nil
}

func (authority HostAccessAuthority) ValidateLiveMission(ctx context.Context, scope domain.HostAccessScope, key string, now time.Time) error {
	denied := fmt.Errorf("live mission access authority no longer current")
	if authority.Records == nil || authority.Instances == nil || scope.Validate() != nil || now.IsZero() || !scope.DeadlineAt.After(now) || scope.DeadlineAt.After(now.Add(hostLiveCopyLifetime)) {
		return denied
	}
	session, err := authority.Records.Get(ctx, scope.SessionID)
	if err != nil || session.ID != scope.SessionID || session.GuildID != scope.GuildID || session.Infrastructure.InstanceID != scope.InstanceID || HostContentSnapshot(session) != scope.SnapshotSHA256 || scope.OperationID != liveCopyOperation(scope.SessionID, scope.InstanceID, key) {
		return denied
	}
	if _, ok := session.LiveMissionCopyTarget(key); !ok {
		return denied
	}
	if authority.Instances.VerifyHostInstance(ctx, scope.SessionID, scope.InstanceID) != nil {
		return denied
	}
	return nil
}
