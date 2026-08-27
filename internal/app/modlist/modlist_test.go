package modlist

import (
	"strings"
	"testing"
)

func TestGenerateRebuildsSanitizedLauncherPreset(t *testing.T) {
	t.Parallel()
	source := []byte(`<!doctype html><html><head><meta name="generator" content="C:\Users\secret"></head><body>
<script>sendSecret()</script>
<tr data-type="ModContainer"><td data-type="DisplayName">CBA &amp; Friends</td><td><a href="https://steamcommunity.com/sharedfiles/filedetails/?id=450814997" data-type="Link">mod</a></td></tr>
<tr data-type="ModContainer"><td data-type="DisplayName">Duplicate</td><td><a href="https://steamcommunity.com/sharedfiles/filedetails/?id=450814997">mod</a></td></tr>
<tr data-type="ModContainer"><td data-type="DisplayName">ACE</td><td><a data-publishedfileid="463939057">ACE</a></td></tr>
<tr data-type="DlcContainer"><td data-type="DisplayName">S.O.G. Prairie Fire</td><td><a data-publishedfileid="1227700">DLC</a></td></tr></body></html>`)
	artifact, err := Generate(source, "session-1", "Saturday <Ops>", "saturday-ops", false)
	if err != nil {
		t.Fatal(err)
	}
	content := string(artifact.Body)
	for _, expected := range []string{
		`name="arma:PresetName" content="Saturday &lt;Ops&gt;"`,
		`data-type="ModContainer"`, `data-type="DisplayName">CBA &amp; Friends`,
		`?id=450814997`, `?id=463939057`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("sanitized preset missing %q: %s", expected, content)
		}
	}
	for _, forbidden := range []string{"C:\\Users", "sendSecret", "<script", "Duplicate"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("sanitized preset retained %q: %s", forbidden, content)
		}
	}
	if strings.Contains(content, "1227700") || strings.Contains(content, "Prairie Fire") {
		t.Fatalf("sanitized preset retained Creator DLC section: %s", content)
	}
	if artifact.Filename != "saturday-ops-modlist.html" || artifact.WorkshopCount != 2 ||
		!strings.HasPrefix(artifact.ObjectKey, "sessions/session-1/input/modlists/") || len(artifact.SHA256Hex) != 64 {
		t.Fatalf("artifact = %#v", artifact)
	}
}

func TestGenerateWorkshopIsDeterministicAndSanitizesNames(t *testing.T) {
	first, err := GenerateWorkshop([]WorkshopMod{{ID: 222222, Name: "<unsafe>\u0000 Mod"}, {ID: 111111, Name: "Second"}}, "session-1", "Session", "session")
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateWorkshop([]WorkshopMod{{ID: 222222, Name: "<unsafe>\u0000 Mod"}, {ID: 111111, Name: "Second"}}, "session-1", "Session", "session")
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256Hex != second.SHA256Hex || first.WorkshopCount != 2 || strings.Contains(string(first.Body), "\u0000") || !strings.Contains(string(first.Body), "id=222222") {
		t.Fatalf("artifact = %#v", first)
	}
}

func TestGenerateIgnoresWorkshopLookingIDsOutsideModRows(t *testing.T) {
	t.Parallel()
	source := []byte(`<html><body>
<tr data-type="ModContainer"><td data-type="DisplayName">CBA</td><td><a href="https://steamcommunity.com/sharedfiles/filedetails/?id=450814997">mod</a></td></tr>
<tr data-type="DlcContainer"><td data-type="DisplayName">Creator DLC</td><td><a data-publishedfileid="999999999">dlc</a></td></tr>
<a href="https://steamcommunity.com/sharedfiles/filedetails/?id=888888888">untrusted footer</a>
</body></html>`)
	artifact, err := Generate(source, "session-1", "Session", "session", false)
	if err != nil {
		t.Fatal(err)
	}
	content := string(artifact.Body)
	if artifact.WorkshopCount != 1 || !strings.Contains(content, "450814997") || strings.Contains(content, "999999999") || strings.Contains(content, "888888888") {
		t.Fatalf("filtered artifact = %#v body=%s", artifact, content)
	}
}

func TestGenerateRejectsPresetWithoutWorkshopMods(t *testing.T) {
	t.Parallel()
	if _, err := Generate([]byte(`<html><body>empty</body></html>`), "session-1", "Empty", "empty", false); err == nil {
		t.Fatal("empty preset was accepted")
	}
	artifact, err := Generate([]byte(`<html><body><tr data-type="DlcContainer"><td data-type="DisplayName">Creator DLC</td><td data-publishedfileid="1227700"></td></tr></body></html>`), "session-1", "cDLC", "cdlc", true)
	if err != nil || artifact.WorkshopCount != 0 || strings.Contains(string(artifact.Body), "1227700") {
		t.Fatalf("cDLC-authorized empty Workshop preset = %#v err=%v", artifact, err)
	}
}
