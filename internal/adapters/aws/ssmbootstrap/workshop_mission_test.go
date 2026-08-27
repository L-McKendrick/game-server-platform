package ssmbootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestResolvedWorkshopMissionManifestIsDeterministicAndDeduplicated(t *testing.T) {
	now := time.Now().UTC()
	session := domain.Session{WorkshopMissionSources: []domain.WorkshopMissionSource{
		{Source: domain.WorkshopReference{PublishedFileID: 2, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=2"}, SourceKind: domain.WorkshopSourceCollection, ResolutionSHA256: strings.Repeat("b", 64), AcceptedItemIDs: []uint64{30, 20}, ResolvedAt: now},
		{Source: domain.WorkshopReference{PublishedFileID: 1, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=1"}, SourceKind: domain.WorkshopSourceCollection, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{20}, ResolvedAt: now},
	}}
	manifest, revision, err := resolvedWorkshopMissionManifest(session)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(manifest, "20\t"+revision+"\n30\t"+revision+"\n") || len(revision) != 64 {
		t.Fatalf("manifest %q revision %q", manifest, revision)
	}
}

func TestBootstrapScriptContainsIsolatedWorkshopMissionGuards(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	for _, required := range []string{"install_workshop_missions", "Workshop mission must contain exactly one deployable PBO", "Workshop mission content contains a symbolic link", "return 75", "workshop-missions/$id", "104857600", "install_workshop_missions.revision-"} {
		if !strings.Contains(script, required) {
			t.Errorf("bootstrap script missing %q", required)
		}
	}
}

func TestBootstrapScriptDownloadsPresetModsPerItemWithBoundedFailureHandling(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	for _, required := range []string{"download_workshop_item", "for attempt in 1 2 3", "[ \"$code\" -eq 75 ] || return", "Workshop mod content contains a symbolic link", "21474836480", "Workshop mod count exceeds the supported limit"} {
		if !strings.Contains(script, required) {
			t.Errorf("bootstrap script missing %q", required)
		}
	}
	if strings.Contains(script, "for id in \"${ids[@]}\" \"${server_ids[@]}\"; do [ -z \"$id\" ] || printf 'workshop_download_item") {
		t.Error("preset mods are still batched into one SteamCMD operation")
	}
}
