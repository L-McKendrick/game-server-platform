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
	manifest, revision, err := resolvedWorkshopMissionManifest(session, false)
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
	_, _, err := resolvedWorkshopMissionManifest(session, false)
	if err == nil || !strings.Contains(err.Error(), "resubmitted") {
		t.Fatalf("legacy manifest error = %v", err)
	}
}

func TestResolvedWorkshopMissionManifestSkipsMaterializedLegacySourceForLiveSync(t *testing.T) {
	now := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC)
	session := domain.Session{
		WorkshopMissionSources: []domain.WorkshopMissionSource{{
			Source: domain.WorkshopReference{PublishedFileID: 10, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10"}, SourceKind: domain.WorkshopSourceItem,
			ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{10}, ResolvedAt: now,
		}},
		MissionFiles: []domain.MissionRecord{{WorkshopItemID: 10, ObjectKey: "mission-object", Filename: "Legacy.Altis.pbo", Status: domain.ArtifactAccepted, AddedAt: now.Add(time.Minute)}},
	}

	manifest, _, err := resolvedWorkshopMissionManifest(session, true)
	if err != nil || manifest != "" {
		t.Fatalf("materialized legacy manifest = %q, err = %v", manifest, err)
	}
}

func TestResolvedWorkshopMissionManifestLimitsLiveSyncToPendingItems(t *testing.T) {
	now := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC)
	session := domain.Session{
		ID: "session-1",
		WorkshopMissionSources: []domain.WorkshopMissionSource{{
			Source: domain.WorkshopReference{PublishedFileID: 1, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=1"}, SourceKind: domain.WorkshopSourceCollection,
			ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{20, 30}, AcceptedItems: []domain.WorkshopMissionItem{{PublishedFileID: 20, Filename: "Twenty.Stratis.pbo", FileSize: 200}, {PublishedFileID: 30, Filename: "Thirty.Altis.pbo", FileSize: 300}}, ResolvedAt: now,
		}},
		MissionFiles: []domain.MissionRecord{{WorkshopItemID: 20, ObjectKey: "sessions/session-1/input/missions/" + strings.Repeat("b", 64) + "-Twenty.Stratis.pbo", Filename: "Twenty.Stratis.pbo", Status: domain.ArtifactAccepted, AddedAt: now.Add(time.Minute)}},
	}

	manifest, revision, err := resolvedWorkshopMissionManifest(session, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(manifest, "20\t") || manifest != "30\t"+revision+"\tThirty.Altis.pbo\t300\n" {
		t.Fatalf("pending manifest = %q", manifest)
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
		`ensure_staged_workshop_mod`,
		`[ "$(cat -- "$marker")" = "$id:$expected_update" ]`,
		`STAGED_WORKSHOP_MOD_PATH="$revision_root/$id"`,
		`WORKSHOP_PROMOTE_MODS:=true`,
		`[ "$WORKSHOP_PROMOTE_MODS" = true ] || return 0`,
		`if [ "$WORKSHOP_PROMOTE_MODS" = true ]; then`,
		`launch_and_verify`,
		`ERR_WORKSHOP_DISK_SPACE`,
		`ERR_WORKSHOP_RESULT_PUBLISH`,
		`sessions/$SESSION_ID/workshop-sync/$WORKFLOW_ID.json`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("bootstrap script missing isolated-sync guard %q", required)
		}
	}
}

func TestGameInstancePolicyAllowsOnlyWorkshopSyncResultJSON(t *testing.T) {
	policyPath := filepath.Join("..", "..", "..", "..", "infra", "terraform", "environments", "dev", "phase6.tf")
	contents, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	policy := string(contents)
	for _, required := range []string{
		`sid       = "PublishWorkshopSyncResults"`,
		`actions   = ["s3:PutObject"]`,
		`/sessions/*/workshop-sync/*.json`,
	} {
		if !strings.Contains(policy, required) {
			t.Errorf("game-instance policy missing %q", required)
		}
	}
}

func TestWorkshopMissionFinalizersCanReadOnlyResolutionManifests(t *testing.T) {
	for _, filename := range []string{"phase4.tf", "phase8.tf", "phase10.tf"} {
		policyPath := filepath.Join("..", "..", "..", "..", "infra", "terraform", "environments", "dev", filename)
		contents, err := os.ReadFile(policyPath)
		if err != nil {
			t.Fatal(err)
		}
		policy := string(contents)
		if !strings.Contains(policy, `sid       = "ReadWorkshopMissionManifests"`) ||
			!strings.Contains(policy, `actions   = ["s3:GetObject"]`) ||
			!strings.Contains(policy, `/sessions/*/workshop-resolutions/*.tsv`) {
			t.Errorf("%s does not grant the bounded manifest read", filename)
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
