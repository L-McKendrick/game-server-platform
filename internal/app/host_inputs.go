package app

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// HostObjectIssuer authorizes exact accepted reads and fixed output slots. Script is the worker's
// deployed full-digest reference, never an object selected by the host.
type HostObjectIssuer struct {
	Authority HostAccessAuthority
	Signer    ports.HostObjectSigner
	Script    domain.HostObjectRequest
	Rollback  bool // Trusted lifecycle command mode; never selected by the host.
	// VerifiedLegacyReads contains worker-inspected, pinned references only. It is
	// command-local trusted configuration, never host-supplied request metadata.
	VerifiedLegacyReads map[string]domain.HostObjectRequest
}

// InspectLegacyPreset authorizes an accepted preset before any privileged read.
// Callers place the returned reference in VerifiedLegacyReads for issuance and
// retain that exact reference across replay instead of inspecting a newer version.
func (issuer HostObjectIssuer) InspectLegacyPreset(ctx context.Context, scope domain.HostAccessScope, object domain.HostObjectRequest, inspector ports.HostReadInspector) (domain.HostObjectRequest, error) {
	if object.Purpose != domain.HostObjectClientPreset && object.Purpose != domain.HostObjectServerPreset {
		return domain.HostObjectRequest{}, fmt.Errorf("legacy preset purpose invalid")
	}
	return issuer.inspectLegacyRead(ctx, scope, object, inspector, 10*1024*1024)
}

// InspectLegacyMission preserves accepted mission records predating digest keys.
// The returned digest and version must be retained as a trusted command reference.
func (issuer HostObjectIssuer) InspectLegacyMission(ctx context.Context, scope domain.HostAccessScope, object domain.HostObjectRequest, inspector ports.HostReadInspector) (domain.HostObjectRequest, error) {
	if object.Purpose != domain.HostObjectMission {
		return domain.HostObjectRequest{}, fmt.Errorf("legacy mission purpose invalid")
	}
	return issuer.inspectLegacyRead(ctx, scope, object, inspector, 100*1024*1024)
}

func (issuer HostObjectIssuer) inspectLegacyRead(ctx context.Context, scope domain.HostAccessScope, object domain.HostObjectRequest, inspector ports.HostReadInspector, limit int64) (domain.HostObjectRequest, error) {
	shape := object
	// A checksum is required for mission issuance, but cannot exist until these
	// accepted legacy bytes are inspected. This value checks shape only.
	if shape.Purpose == domain.HostObjectMission {
		shape.SHA256 = hostBase64Digest(strings.Repeat("0", 64))
	}
	if inspector == nil || issuer.Rollback != (scope.CommandMode == "rollback") ||
		object.SHA256 != "" || object.VersionID != "" || object.MaxBytes > limit || shape.Validate(scope) != nil {
		return domain.HostObjectRequest{}, fmt.Errorf("legacy read inspection invalid")
	}
	session, workflow, err := issuer.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return domain.HostObjectRequest{}, err
	}
	if !issuer.accepts(session, workflow, object) {
		return domain.HostObjectRequest{}, fmt.Errorf("legacy read unauthorized")
	}
	digest, version, size, err := inspector.InspectHostRead(ctx, object.Key, "", object.MaxBytes)
	if err != nil || version == "" || version == "null" || size < 1 || size > object.MaxBytes {
		return domain.HostObjectRequest{}, fmt.Errorf("legacy read verification failed")
	}
	object.SHA256, object.VersionID, object.MaxBytes = digest, version, size
	if object.Validate(scope) != nil {
		return domain.HostObjectRequest{}, fmt.Errorf("legacy read verification invalid")
	}
	if _, _, err := issuer.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC()); err != nil {
		return domain.HostObjectRequest{}, err
	}
	return object, nil
}

func (issuer HostObjectIssuer) SignWorkflowObject(ctx context.Context, scope domain.HostAccessScope, object domain.HostObjectRequest) (ports.HostObjectCapability, error) {
	if issuer.Rollback != (scope.CommandMode == "rollback") {
		return ports.HostObjectCapability{}, fmt.Errorf("host command mode mismatch")
	}
	session, workflow, err := issuer.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return ports.HostObjectCapability{}, err
	}
	if issuer.Signer == nil || object.Validate(scope) != nil || !issuer.acceptsObject(session, workflow, object) {
		return ports.HostObjectCapability{}, fmt.Errorf("host input is not authorized for this operation")
	}
	capability, err := issuer.Signer.Sign(ctx, scope, object)
	if err != nil {
		return ports.HostObjectCapability{}, fmt.Errorf("host input signing failed")
	}
	// Signing may retrieve refreshed credentials or take time. Never release a
	// capability after authority changed during that work.
	if _, _, err := issuer.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC()); err != nil {
		return ports.HostObjectCapability{}, err
	}
	return capability, nil
}

func (issuer HostObjectIssuer) acceptsObject(session domain.Session, workflow domain.Workflow, object domain.HostObjectRequest) bool {
	if object.Purpose.Action() == domain.HostObjectRead {
		return issuer.accepts(session, workflow, object)
	}
	return authorizedHostOutput(session, workflow, object)
}

func (issuer HostObjectIssuer) accepts(session domain.Session, workflow domain.Workflow, object domain.HostObjectRequest) bool {
	if issuer.Rollback && !session.HasApplyingPresetRevision(workflow.ID) {
		return false
	}
	if object.Purpose == domain.HostObjectRestoreArchive {
		return workflow.Type == domain.RestoreWorkflowType && session.Archive.Validate() == nil &&
			object.Key == session.Archive.ObjectKey && object.SHA256 == session.Archive.SHA256 && object.MaxBytes == session.Archive.SizeBytes
	}
	switch workflow.Type {
	case domain.BootstrapWorkflowType, domain.WakeWorkflowType, domain.RestartWorkflowType, domain.RestoreWorkflowType, domain.WorkshopContentSyncWorkflowType:
	default:
		return false
	}
	switch object.Purpose {
	case domain.HostObjectBootstrapScript:
		return issuer.Script.Purpose == object.Purpose && issuer.Script.Key == object.Key && issuer.Script.SHA256 == object.SHA256 &&
			issuer.Script.VersionID == object.VersionID && issuer.Script.MaxBytes == object.MaxBytes && issuer.Script.SHA256 != ""
	case domain.HostObjectMission:
		for _, mission := range session.AcceptedMissionFiles() {
			if mission.ObjectKey == object.Key {
				name := path.Base(mission.ObjectKey)
				if len(name) > 65 && name[64] == '-' && hostBase64Digest(name[:64]) != "" {
					return object.SHA256 == hostBase64Digest(name[:64])
				}
				if verified, ok := issuer.VerifiedLegacyReads[object.Key]; ok {
					return verified == object && verified.VersionID != "" && verified.SHA256 != ""
				}
				// Empty digest is inspection-only: domain validation rejects it
				// before mission signing or manifest publication.
				return object.SHA256 == "" && object.VersionID == ""
			}
		}
	case domain.HostObjectServerConfig:
		return object.Key == session.ServerConfigObjectKey && object.SHA256 == hostBase64Digest(session.ServerConfigSHA256)
	case domain.HostObjectClientPreset:
		key := session.PresetObjectKeyForApplication()
		if issuer.Rollback {
			key = session.EffectiveActivePresetRevision().PresetObjectKey
		}
		return object.Key == key && issuer.presetDigestAccepted(object)
	case domain.HostObjectServerPreset:
		key := session.ServerPresetObjectKeyForApplication()
		if issuer.Rollback {
			key = session.EffectiveActiveServerPresetRevision().PresetObjectKey
		}
		return object.Key == key && issuer.presetDigestAccepted(object)
	}
	return false
}

func hostBase64Digest(value string) string {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(decoded)
}

func (issuer HostObjectIssuer) presetDigestAccepted(object domain.HostObjectRequest) bool {
	// Current accepted preset keys carry their immutable SHA-256 prefix. Legacy
	// accepted keys lack it; a supplied digest/version must match a worker-inspected
	// reference. The old empty-digest path remains until consumer migration/cutover.
	name := path.Base(object.Key)
	if len(name) > 65 && name[64] == '-' {
		digest := hostBase64Digest(name[:64])
		if digest != "" {
			return object.SHA256 == digest
		}
	}
	if verified, ok := issuer.VerifiedLegacyReads[object.Key]; ok {
		return verified == object && verified.VersionID != "" && verified.SHA256 != ""
	}
	return strings.TrimSpace(object.SHA256) == "" && object.VersionID == ""
}
