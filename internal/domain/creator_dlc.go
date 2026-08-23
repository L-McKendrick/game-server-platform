package domain

import (
	"fmt"
	"slices"
	"strings"
)

const (
	CreatorDLCGlobalMobilization  = "global-mobilization"
	CreatorDLCSOGPrairieFire      = "sog-prairie-fire"
	CreatorDLCCSLAIronCurtain     = "csla-iron-curtain"
	CreatorDLCWesternSahara       = "western-sahara"
	CreatorDLCSpearhead1944       = "spearhead-1944"
	CreatorDLCReactionForces      = "reaction-forces"
	CreatorDLCExpeditionaryForces = "expeditionary-forces"
)

// SupportedCreatorDLCs is the stable storage and presentation order for the
// Arma 3 Creator DLC catalog supported by the platform.
var supportedCreatorDLCs = []string{
	CreatorDLCGlobalMobilization,
	CreatorDLCSOGPrairieFire,
	CreatorDLCCSLAIronCurtain,
	CreatorDLCWesternSahara,
	CreatorDLCSpearhead1944,
	CreatorDLCReactionForces,
	CreatorDLCExpeditionaryForces,
}

var creatorDLCModFolders = map[string]string{
	CreatorDLCGlobalMobilization: "gm", CreatorDLCSOGPrairieFire: "vn",
	CreatorDLCCSLAIronCurtain: "csla", CreatorDLCWesternSahara: "ws",
	CreatorDLCSpearhead1944: "spe", CreatorDLCReactionForces: "rf",
	CreatorDLCExpeditionaryForces: "ef",
}

// SupportedCreatorDLCs returns a copy of the supported catalog so callers
// cannot mutate the validation source.
func SupportedCreatorDLCs() []string {
	return append([]string(nil), supportedCreatorDLCs...)
}

// NormalizeCreatorDLCs validates a user selection and returns a unique copy in
// catalog order so persistence, hashing, and replay remain deterministic.
func NormalizeCreatorDLCs(values []string) ([]string, error) {
	selected := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if !slices.Contains(supportedCreatorDLCs, value) {
			return nil, fmt.Errorf("unsupported Creator DLC %q", value)
		}
		if _, exists := selected[value]; exists {
			return nil, fmt.Errorf("duplicate Creator DLC %q", value)
		}
		selected[value] = struct{}{}
	}
	normalized := make([]string, 0, len(selected))
	for _, value := range supportedCreatorDLCs {
		if _, exists := selected[value]; exists {
			normalized = append(normalized, value)
		}
	}
	return normalized, nil
}

// CreatorDLCModFolders returns official server mod directories in canonical order.
func CreatorDLCModFolders(values []string) ([]string, error) {
	normalized, err := NormalizeCreatorDLCs(values)
	if err != nil {
		return nil, err
	}
	folders := make([]string, 0, len(normalized))
	for _, value := range normalized {
		folders = append(folders, creatorDLCModFolders[value])
	}
	return folders, nil
}
