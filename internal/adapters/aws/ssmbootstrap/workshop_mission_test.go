package ssmbootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func readBootstrapArtifact(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func TestResolvedWorkshopMissionManifestIsDeterministicAndDeduplicated(t *testing.T) {
	now := time.Now().UTC()
	session := domain.Session{WorkshopMissionSources: []domain.WorkshopMissionSource{
		{Source: domain.WorkshopReference{PublishedFileID: 2, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=2"}, SourceKind: domain.WorkshopSourceCollection, ResolutionSHA256: strings.Repeat("b", 64), AcceptedItemIDs: []uint64{30, 20}, AcceptedItems: []domain.WorkshopMissionItem{{PublishedFileID: 30, Filename: "Thirty.Altis.pbo", FileSize: 300}, {PublishedFileID: 20, Filename: "Twenty.Stratis.pbo", FileSize: 200}}, ResolvedAt: now},
		{Source: domain.WorkshopReference{PublishedFileID: 1, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=1"}, SourceKind: domain.WorkshopSourceCollection, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{20}, AcceptedItems: []domain.WorkshopMissionItem{{PublishedFileID: 20, Filename: "Twenty.Stratis.pbo", FileSize: 200}}, ResolvedAt: now},
	}}
	manifest, revision, err := resolvedWorkshopMissionManifest(session)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(manifest, "20\t"+revision+"\tTwenty.Stratis.pbo\t200\n30\t"+revision+"\tThirty.Altis.pbo\t300\n") || len(revision) != 64 {
		t.Fatalf("manifest %q revision %q", manifest, revision)
	}
}

func TestResolvedWorkshopMissionManifestRequiresCanonicalMetadataForLegacyRecords(t *testing.T) {
	now := time.Now().UTC()
	session := domain.Session{WorkshopMissionSources: []domain.WorkshopMissionSource{{
		Source:     domain.WorkshopReference{PublishedFileID: 10, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10"},
		SourceKind: domain.WorkshopSourceItem, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{10}, ResolvedAt: now,
	}}}
	_, _, err := resolvedWorkshopMissionManifest(session)
	if err == nil || !strings.Contains(err.Error(), "resubmitted") {
		t.Fatalf("legacy manifest error = %v", err)
	}
}

func TestBootstrapScriptContainsIsolatedWorkshopMissionGuards(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	for _, required := range []string{"install_workshop_missions", "sync_workshop_content", "Workshop scenario must contain exactly one PBO or legacy payload", "_legacy\\.[bB][iI][nN]", "expected_filename", "expected_size", "Workshop scenario content contains a symbolic link", "ERR_WORKSHOP_SCENARIO_RESUBMIT", "ERR_WORKSHOP_SCENARIO_PAYLOAD", "return 75", "workshop-missions/$id", "104857600", "$stage.missions-$WORKSHOP_MISSION_REVISION"} {
		if !strings.Contains(script, required) {
			t.Errorf("bootstrap script missing %q", required)
		}
	}
}

func TestBootstrapScriptUsesWorkflowIsolatedWorkshopStaging(t *testing.T) {
	script := readBootstrapArtifact(t)
	for _, required := range []string{
		`WORKSHOP_STAGING_ROOT="$ROOT/workshop-staging/$WORKFLOW_ID"`,
		`ln -s "$WORKSHOP_STAGING_ROOT/steamapps"`,
		`source="$WORKSHOP_STAGING_ROOT/steamapps/workshop/content/107410/$id"`,
		`$ROOT/workshop/mod-revisions/client-$PRESET_REVISION`,
		`$ROOT/workshop/mod-revisions/server-$SERVER_PRESET_REVISION`,
		`WORKSHOP_PROMOTE_MODS:=true`,
		`[ "$WORKSHOP_PROMOTE_MODS" = true ] || return 0`,
		`if [ "$WORKSHOP_PROMOTE_MODS" = true ]; then`,
		`launch_and_verify`,
		`ERR_WORKSHOP_DISK_SPACE`,
		`sessions/$SESSION_ID/workshop-sync/$WORKFLOW_ID.json`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("bootstrap script missing isolated-sync guard %q", required)
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
