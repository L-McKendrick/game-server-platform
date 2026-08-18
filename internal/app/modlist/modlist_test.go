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
<a data-publishedfileid="463939057">ACE</a></body></html>`)
	artifact, err := Generate(source, "session-1", "Saturday <Ops>", "saturday-ops")
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
	if artifact.Filename != "saturday-ops-modlist.html" || artifact.WorkshopCount != 2 ||
		!strings.HasPrefix(artifact.ObjectKey, "sessions/session-1/input/modlists/") || len(artifact.SHA256Hex) != 64 {
		t.Fatalf("artifact = %#v", artifact)
	}
}

func TestGenerateRejectsPresetWithoutWorkshopMods(t *testing.T) {
	t.Parallel()
	if _, err := Generate([]byte(`<html><body>empty</body></html>`), "session-1", "Empty", "empty"); err == nil {
		t.Fatal("empty preset was accepted")
	}
}
