package app

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"sort"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// RecoverReadInventory restores only pinned accepted-read references and context
// for trusted command retries. It does not return the old asset bearer URLs;
// IssueWithEnvironment still handles exact-inventory replay and bounded refresh.
func (issuer HostManifestIssuer) RecoverReadInventory(ctx context.Context, scope domain.HostAccessScope) (HostObjectIssuer, []domain.HostObjectRequest, map[string]string, error) {
	manifest, err := issuer.recoverCommandManifest(ctx, scope)
	if err != nil {
		return HostObjectIssuer{}, nil, nil, err
	}
	return issuer.recoverReadInventory(ctx, scope, manifest)
}

// RecoverCommandInventory preserves both accepted reads and derived Workshop
// staging slots. Renewal must never silently drop write capabilities.
func (issuer HostManifestIssuer) RecoverCommandInventory(ctx context.Context, scope domain.HostAccessScope) (HostObjectIssuer, []domain.HostObjectRequest, map[string]string, error) {
	manifest, err := issuer.recoverCommandManifest(ctx, scope)
	if err != nil {
		return HostObjectIssuer{}, nil, nil, err
	}
	inputs, reads, environment, err := issuer.recoverReadInventory(ctx, scope, manifest)
	if err != nil {
		return HostObjectIssuer{}, nil, nil, err
	}
	outputs, err := inputs.WorkflowCommandOutputInventory(ctx, scope)
	if err != nil {
		return HostObjectIssuer{}, nil, nil, err
	}
	var stored []domain.HostObjectRequest
	for _, capability := range manifest.Objects {
		if capability.Object.Purpose.Action() != domain.HostObjectRead {
			stored = append(stored, capability.Object)
		}
	}
	if len(stored) != len(outputs) || (len(outputs) > 0 && !reflect.DeepEqual(stored, outputs)) {
		return HostObjectIssuer{}, nil, nil, domain.ErrConflict
	}
	return inputs, append(reads, outputs...), environment, nil
}

func (issuer HostManifestIssuer) recoverReadInventory(ctx context.Context, scope domain.HostAccessScope, manifest ports.HostAccessManifest) (HostObjectIssuer, []domain.HostObjectRequest, map[string]string, error) {
	inputs := issuer.Inputs
	inputs.VerifiedLegacyReads = make(map[string]domain.HostObjectRequest)
	var storedReads []domain.HostObjectRequest
	seen := make(map[string]bool)
	for _, capability := range manifest.Objects {
		object := capability.Object
		if object.Purpose.Action() != domain.HostObjectRead {
			continue
		}
		if object.Validate(scope) != nil || seen[object.Key] {
			return HostObjectIssuer{}, nil, nil, domain.ErrConflict
		}
		seen[object.Key] = true
		storedReads = append(storedReads, object)
		if acceptedKeyDigest(object.Key) == "" && (object.Purpose == domain.HostObjectMission || object.Purpose == domain.HostObjectClientPreset || object.Purpose == domain.HostObjectServerPreset) {
			if object.VersionID == "" || object.VersionID == "null" || object.SHA256 == "" {
				return HostObjectIssuer{}, nil, nil, domain.ErrConflict
			}
			inputs.VerifiedLegacyReads[object.Key] = object
		}
	}
	inputs, objects, err := inputs.WorkflowReadInventory(ctx, scope, nil)
	if err != nil {
		return HostObjectIssuer{}, nil, nil, err
	}
	if !reflect.DeepEqual(objects, storedReads) {
		return HostObjectIssuer{}, nil, nil, domain.ErrConflict
	}
	return inputs, objects, manifest.Environment, nil
}

// WorkflowReadInventory derives accepted reads from strong current records. The
// returned issuer owns fresh command-local legacy references; it does not mutate
// shared wiring. Recovery may preload references from the pinned manifest so it
// never silently substitutes newer legacy bytes.
func (issuer HostObjectIssuer) WorkflowReadInventory(ctx context.Context, scope domain.HostAccessScope, inspector ports.HostReadInspector) (HostObjectIssuer, []domain.HostObjectRequest, error) {
	session, workflow, err := issuer.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return HostObjectIssuer{}, nil, err
	}
	if issuer.Rollback != (scope.CommandMode == "rollback") || issuer.Script.Validate(scope) != nil || !issuer.accepts(session, workflow, issuer.Script) {
		return HostObjectIssuer{}, nil, fmt.Errorf("host read inventory script invalid")
	}
	verified := make(map[string]domain.HostObjectRequest)
	objects := []domain.HostObjectRequest{issuer.Script}
	appendInput := func(object domain.HostObjectRequest) error {
		if object.SHA256 == "" {
			if pinned, ok := issuer.VerifiedLegacyReads[object.Key]; ok {
				if pinned.Purpose != object.Purpose || pinned.Slot != object.Slot || pinned.MaxBytes > object.MaxBytes || pinned.Validate(scope) != nil || !issuer.accepts(session, workflow, pinned) {
					return domain.ErrConflict
				}
				object = pinned
			} else if object.Purpose == domain.HostObjectMission {
				object, err = issuer.InspectLegacyMission(ctx, scope, object, inspector)
			} else {
				object, err = issuer.InspectLegacyPreset(ctx, scope, object, inspector)
			}
			if err != nil {
				return err
			}
			verified[object.Key] = object
		}
		objects = append(objects, object)
		return nil
	}
	missions := session.AcceptedMissionFiles()
	sort.Slice(missions, func(i, j int) bool { return missions[i].ObjectKey < missions[j].ObjectKey })
	for index, mission := range missions {
		if err := appendInput(domain.HostObjectRequest{Purpose: domain.HostObjectMission, Slot: fmt.Sprintf("mission-%04d", index), Key: mission.ObjectKey, SHA256: acceptedKeyDigest(mission.ObjectKey), MaxBytes: domain.MaximumWorkshopMissionBytes}); err != nil {
			return HostObjectIssuer{}, nil, err
		}
	}
	clientKey, serverKey := session.PresetObjectKeyForApplication(), session.ServerPresetObjectKeyForApplication()
	if issuer.Rollback {
		clientKey, serverKey = session.EffectiveActivePresetRevision().PresetObjectKey, session.EffectiveActiveServerPresetRevision().PresetObjectKey
	}
	for _, object := range []domain.HostObjectRequest{
		{Purpose: domain.HostObjectClientPreset, Slot: "client-preset", Key: clientKey, SHA256: acceptedKeyDigest(clientKey), MaxBytes: 10 * 1024 * 1024},
		{Purpose: domain.HostObjectServerPreset, Slot: "server-preset", Key: serverKey, SHA256: acceptedKeyDigest(serverKey), MaxBytes: 10 * 1024 * 1024},
	} {
		if object.Key != "" {
			if err := appendInput(object); err != nil {
				return HostObjectIssuer{}, nil, err
			}
		}
	}
	if session.ServerConfigObjectKey != "" {
		objects = append(objects, domain.HostObjectRequest{Purpose: domain.HostObjectServerConfig, Slot: "server-config", Key: session.ServerConfigObjectKey, SHA256: hostBase64Digest(session.ServerConfigSHA256), MaxBytes: domain.MaximumServerConfigBytes})
	}
	issuer.VerifiedLegacyReads = verified
	for _, object := range objects {
		if object.Validate(scope) != nil || !issuer.accepts(session, workflow, object) {
			return HostObjectIssuer{}, nil, fmt.Errorf("host read inventory unauthorized")
		}
	}
	if _, _, err := issuer.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC()); err != nil {
		return HostObjectIssuer{}, nil, err
	}
	return issuer, objects, nil
}

func acceptedKeyDigest(key string) string {
	name := path.Base(key)
	if len(name) > 65 && name[64] == '-' {
		return hostBase64Digest(name[:64])
	}
	return ""
}
