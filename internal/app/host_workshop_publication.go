package app

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// PublishHostWorkshop is called only after observation of the owned command's
// success. All outputs are validated and pinned before any durable publication.
func (delivery HostCommandDelivery) PublishHostWorkshop(ctx context.Context, scope domain.HostAccessScope) error {
	issuer := delivery.Issuer
	issuer.Inputs.Rollback = scope.CommandMode == "rollback"
	_, objects, environment, err := issuer.RecoverCommandInventory(ctx, scope)
	if err != nil {
		return err
	}
	var outputs []domain.HostObjectRequest
	for _, object := range objects {
		if object.Purpose == domain.HostObjectWorkshopMission || object.Purpose == domain.HostObjectWorkshopResolution || object.Purpose == domain.HostObjectWorkshopResult {
			outputs = append(outputs, object)
		}
	}
	if len(outputs) == 0 {
		return nil
	}
	if delivery.WorkshopStore == nil || delivery.Inspector == nil {
		return fmt.Errorf("Workshop publication dependencies unavailable")
	}
	current, err := issuer.Attempts.GetHostAccessAttempt(ctx, scope.SessionID, scope.OperationID, scope.AttemptID)
	if err != nil {
		return err
	}
	if !current.Scope.Equal(scope) || current.Validate() != nil || current.DispatchState == domain.HostAccessFinished {
		return domain.ErrConflict
	}
	if len(current.WorkshopOutputs) > 0 && len(current.WorkshopOutputs) != len(outputs) {
		return domain.ErrConflict
	}
	pins := make(map[string]domain.HostOutputVersion, len(outputs))
	var result, resolution []byte
	var missionIDs []uint64
	for _, object := range outputs {
		if _, _, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC()); err != nil {
			return err
		}
		prior, pinned := current.WorkshopOutputs[object.Slot]
		if len(current.WorkshopOutputs) > 0 && !pinned {
			return domain.ErrConflict
		}
		var pin domain.HostOutputVersion
		if object.Purpose == domain.HostObjectWorkshopMission {
			if pinned {
				// Immutable source versions were already independently hashed
				// before the atomic full-set pin. Avoid rehashing large PBOs.
				pin = prior
			} else {
				checksum, version, size, inspectErr := delivery.Inspector.InspectHostRead(ctx, object.Key, prior.VersionID, object.MaxBytes)
				if inspectErr != nil {
					return fmt.Errorf("Workshop staged mission verification unavailable")
				}
				pin = domain.HostOutputVersion{VersionID: version, SHA256: checksum, SizeBytes: size}
			}
			id, parseErr := strconv.ParseUint(strings.TrimPrefix(object.Slot, "mission-"), 10, 64)
			if parseErr != nil {
				return domain.ErrConflict
			}
			missionIDs = append(missionIDs, id)
		} else {
			body, inspected, readErr := delivery.WorkshopStore.ReadHostOutput(ctx, object.Key, prior.VersionID, object.ContentType, object.MaxBytes)
			if readErr != nil {
				return fmt.Errorf("Workshop staged document verification unavailable")
			}
			pin = inspected
			switch object.Purpose {
			case domain.HostObjectWorkshopResult:
				result = body
			case domain.HostObjectWorkshopResolution:
				resolution = body
			default:
				return domain.ErrConflict
			}
		}
		if pin.SizeBytes < object.MinBytes || pin.SizeBytes > object.MaxBytes || (pinned && pin != prior) {
			return domain.ErrConflict
		}
		pins[object.Slot] = pin
	}
	session, workflow, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return err
	}
	target, expected, err := delivery.workshopExpectedItems(ctx, scope, objects, environment)
	if err != nil {
		return err
	}
	if err := validateWorkshopResult(result, scope, target, workflow.StartedAt, time.Now().UTC(), expected); err != nil {
		return err
	}
	keys := make(map[string]string)
	var canonical []byte
	if len(missionIDs) > 0 {
		keys, err = validatedWorkshopMissionKeys(session, missionIDs, resolution, pins)
		if err != nil {
			return err
		}
		canonical, err = canonicalWorkshopResolution(session, keys)
		if err != nil {
			return err
		}
	}
	// Refresh may have advanced manifest generation while inspection streamed.
	// Preserve its latest metadata; conditional save arbitrates competing pin sets.
	latest, err := issuer.Attempts.GetHostAccessAttempt(ctx, scope.SessionID, scope.OperationID, scope.AttemptID)
	if err != nil {
		return err
	}
	if !latest.Scope.Equal(scope) || latest.Validate() != nil || latest.DispatchState == domain.HostAccessFinished {
		return domain.ErrConflict
	}
	session, _, err = issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return err
	}
	if len(latest.WorkshopOutputs) > 0 {
		if !reflect.DeepEqual(latest.WorkshopOutputs, pins) {
			return domain.ErrConflict
		}
	} else {
		next := latest
		next.Revision++
		next.WorkshopOutputs = pins
		if err := issuer.Attempts.SaveHostAccessAttempt(ctx, next, latest.Revision, session.Version, time.Now().UTC()); err != nil {
			return err
		}
	}
	publish := func(action func() error) error {
		if _, _, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC()); err != nil {
			return err
		}
		if err := action(); err != nil {
			return err
		}
		_, _, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
		return err
	}
	for _, object := range outputs {
		if object.Purpose != domain.HostObjectWorkshopMission {
			continue
		}
		if err := publish(func() error {
			_, err := delivery.WorkshopStore.PromoteHostOutput(ctx, object.Key, keys[object.Slot], object.ContentType, pins[object.Slot])
			return err
		}); err != nil {
			return err
		}
	}
	if len(canonical) > 0 {
		revision, err := session.WorkshopMissionRevision()
		if err != nil || revision != environment["WORKSHOP_MISSION_REVISION_B64"] {
			return domain.ErrConflict
		}
		if err := publish(func() error {
			_, err := delivery.WorkshopStore.PublishHostDocument(ctx, path.Join("sessions", scope.SessionID, "workshop-resolutions", revision+".tsv"), "text/tab-separated-values", canonical)
			return err
		}); err != nil {
			return err
		}
	}
	resultName := scope.OperationID
	if scope.CommandMode == "rollback" {
		resultName += "-rollback-" + scope.AttemptID
	}
	return publish(func() error {
		_, err := delivery.WorkshopStore.PromoteHostOutput(ctx, scope.StagingKey("workshop-result"), path.Join("sessions", scope.SessionID, "workshop-sync", resultName+".json"), "application/json", pins["workshop-result"])
		return err
	})
}
