package domain

import (
	"slices"
	"testing"
)

func TestNormalizeCreatorDLCs_CanonicalizesCatalogOrder(t *testing.T) {
	t.Parallel()
	got, err := NormalizeCreatorDLCs([]string{CreatorDLCExpeditionaryForces, " WESTERN-SAHARA "})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{CreatorDLCWesternSahara, CreatorDLCExpeditionaryForces}
	if !slices.Equal(got, want) {
		t.Fatalf("NormalizeCreatorDLCs() = %#v; want %#v", got, want)
	}
}

func TestNormalizeCreatorDLCs_RejectsUnknownAndDuplicateValues(t *testing.T) {
	t.Parallel()
	for name, values := range map[string][]string{
		"unknown":   {"future-dlc"},
		"duplicate": {CreatorDLCReactionForces, CreatorDLCReactionForces},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeCreatorDLCs(values); err == nil {
				t.Fatalf("NormalizeCreatorDLCs(%#v) succeeded", values)
			}
		})
	}
}

func TestSupportedCreatorDLCs_ReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()
	first := SupportedCreatorDLCs()
	first[0] = "changed"
	if SupportedCreatorDLCs()[0] != CreatorDLCGlobalMobilization {
		t.Fatal("supported Creator DLC catalog was mutated")
	}
}

func TestCreatorDLCModFolders_UsesCanonicalServerDirectories(t *testing.T) {
	t.Parallel()
	got, err := CreatorDLCModFolders([]string{CreatorDLCReactionForces, CreatorDLCGlobalMobilization})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gm", "rf"}; !slices.Equal(got, want) {
		t.Fatalf("CreatorDLCModFolders() = %#v; want %#v", got, want)
	}
}
