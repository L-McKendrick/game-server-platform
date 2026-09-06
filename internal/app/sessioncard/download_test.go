package sessioncard

import (
	"strings"
	"testing"
	"time"
)

func TestDownloadActivityOnlyLinksCanonicalWorkshopItems(t *testing.T) {
	for _, value := range []string{"Workshop item 0 (1/7)", "Workshop item 01 (1/7)", "Workshop item 18446744073709551616 (1/7)", "Workshop item 123 (8/7)", "Workshop item 123 (1/251)", "Workshop item 123 (1/7) [bad](https://evil.test)", "Arma 3 server files (88%)"} {
		if got := downloadActivity(value); got != safe(value) {
			t.Fatalf("unsafe or altered fallback: %q", got)
		}
	}
	if got := downloadActivity("Workshop item 450814997 (3/7)"); got != "[450814997](https://steamcommunity.com/sharedfiles/filedetails/?id=450814997)" {
		t.Fatal(got)
	}
}

func TestDetailedStatusGroupsSourcesAndPrioritizesFailure(t *testing.T) {
	card := Projection{Name: "Test", Lifecycle: "Running", Game: "Arma 3", Mode: "Modded", Health: "Healthy",
		Mods:       ModsProjection{ActiveRevision: 1, ActiveWorkshopSourceID: 3368879130, PendingRevision: 2, PendingWorkshopSourceID: 42, PendingStatus: "Staged"},
		ServerMods: ServerModsProjection{Status: "Awaiting upload"},
		Failure:    FailureProjection{Present: true, Summary: "Content needs attention", UserAction: "Resubmit the source"},
	}
	got := RenderDetailed(card)
	for _, want := range []string{"### Content", "**Active Workshop source:** [3368879130](https://steamcommunity.com/sharedfiles/filedetails/?id=3368879130)", "**Pending Workshop source:** [42](https://steamcommunity.com/sharedfiles/filedetails/?id=42)", "Resubmit the source"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	for _, unwanted := range []string{"Server-only mods", "TeamSpeak", "Current stage", "Status: Running", "### Progress"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("irrelevant %q: %s", unwanted, got)
		}
	}
	if strings.Index(got, "Action required") > strings.Index(got, "### Content") {
		t.Fatal("failure follows content")
	}
}

func TestPublicProgressUsesWorkshopCountAndSeparatesStarted(t *testing.T) {
	card := Projection{Stage: "Synchronizing Workshop content", Progress: ProgressProjection{Visible: true, Condition: "Active", Activity: "Workshop item 450814997 (3/7)"}, OperationStartedAt: time.Unix(1800000000, 0)}
	for _, got := range []string{publicProgressValue(card), RenderPublic(card)} {
		for _, want := range []string{"**Current stage:** Downloading and installing workshop files ", "3 of 7", "**Current download:** [450814997](https://steamcommunity.com/sharedfiles/filedetails/?id=450814997)", "\n\n**Started:**"} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q: %s", want, got)
			}
		}
		for _, unwanted := range []string{"State:** Active", "Progress state:** Active", "Item 3/7", "Synchronizing"} {
			if strings.Contains(got, unwanted) {
				t.Fatalf("unexpected %q: %s", unwanted, got)
			}
		}
	}
	card.Progress.Condition = "Retrying"
	if !strings.Contains(publicProgressValue(card), "**State:** Retrying") {
		t.Fatal("actionable condition omitted")
	}
}
