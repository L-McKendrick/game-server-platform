package modlist

import (
	"encoding/xml"
	"os"
	"strings"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// Model the reference Launcher's XML metadata and typed rows independently of
// the generator's regex extraction, so malformed HTML cannot pass a round trip.
type launcherPreset struct {
	XMLName xml.Name `xml:"html"`
	Head    struct {
		Metadata []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"meta"`
	} `xml:"head"`
	Body struct {
		Lists []struct {
			Class string `xml:"class,attr"`
			Table struct {
				Rows []struct {
					Type  string `xml:"data-type,attr"`
					Cells []struct {
						Type string `xml:"data-type,attr"`
						Text string `xml:",chardata"`
						Link *struct {
							Type string `xml:"data-type,attr"`
							Href string `xml:"href,attr"`
						} `xml:"a"`
					} `xml:"td"`
				} `xml:"tr"`
			} `xml:"table"`
		} `xml:"div"`
	} `xml:"body"`
}

func parseLauncherPreset(t *testing.T, body []byte) launcherPreset {
	t.Helper()
	var preset launcherPreset
	if err := xml.Unmarshal(body, &preset); err != nil {
		t.Fatalf("preset is not well-formed Launcher XML: %v", err)
	}
	typed := false
	for _, meta := range preset.Head.Metadata {
		if meta.Name == "arma:Type" && meta.Content == "preset" {
			typed = true
		}
	}
	if !typed {
		t.Fatal("preset omits arma:Type=preset")
	}
	return preset
}

func launcherRows(t *testing.T, preset launcherPreset, rowType string) []string {
	t.Helper()
	var rows []string
	for _, list := range preset.Body.Lists {
		for _, row := range list.Table.Rows {
			if row.Type != rowType {
				continue
			}
			name, link := "", ""
			for _, cell := range row.Cells {
				if cell.Type == "DisplayName" {
					name = cell.Text
				}
				if cell.Link != nil && cell.Link.Type == "Link" {
					link = cell.Link.Href
				}
			}
			if name == "" || link == "" {
				t.Fatalf("Launcher row lacks a display name or typed link: %#v", row)
			}
			rows = append(rows, name+"|"+link)
		}
	}
	return rows
}

func TestGenerateMatchesLauncherReferenceAndPreservesDLCs(t *testing.T) {
	source, err := os.ReadFile("testdata/launcher-reference.html")
	if err != nil {
		t.Fatal(err)
	}
	reference := parseLauncherPreset(t, source)
	artifact, err := Generate(source, "session-1", "my_server", "my-server", false)
	if err != nil {
		t.Fatal(err)
	}
	generated := parseLauncherPreset(t, artifact.Body)
	for _, rowType := range []string{"ModContainer", "DlcContainer"} {
		want := strings.Join(launcherRows(t, reference, rowType), "\n")
		got := strings.Join(launcherRows(t, generated, rowType), "\n")
		if got != want {
			t.Fatalf("%s rows changed:\ngot %s\nwant %s", rowType, got, want)
		}
	}
	if artifact.WorkshopCount != len(launcherRows(t, reference, "ModContainer")) || len(launcherRows(t, generated, "DlcContainer")) != 2 {
		t.Fatal("DLC entries must not count as Workshop mods or be duplicated")
	}
	repeated, err := Generate(artifact.Body, "session-1", "my_server", "my-server", false, domain.CreatorDLCReactionForces, domain.CreatorDLCExpeditionaryForces)
	if err != nil || repeated.SHA256Hex != artifact.SHA256Hex {
		t.Fatalf("regenerated preset is not deterministic: %v", err)
	}
}

func TestGenerateWorkshopIncludesConfiguredCreatorDLCs(t *testing.T) {
	artifact, err := GenerateWorkshop([]WorkshopMod{{ID: 450814997, Name: "CBA_A3"}}, "session-1", "<Ops> & Friends\ufffe", "ops", domain.SupportedCreatorDLCs()...)
	if err != nil {
		t.Fatal(err)
	}
	preset := parseLauncherPreset(t, artifact.Body)
	dlcs := launcherRows(t, preset, "DlcContainer")
	want := []string{
		"Global Mobilization - Cold War Germany|https://store.steampowered.com/app/1042220",
		"S.O.G. Prairie Fire|https://store.steampowered.com/app/1227700",
		"CSLA Iron Curtain|https://store.steampowered.com/app/1294440",
		"Western Sahara|https://store.steampowered.com/app/1681170",
		"Spearhead 1944|https://store.steampowered.com/app/1175380",
		"Reaction Forces|https://store.steampowered.com/app/2647760",
		"Expeditionary Forces|https://store.steampowered.com/app/2647830",
	}
	if strings.Join(dlcs, "\n") != strings.Join(want, "\n") || artifact.WorkshopCount != 1 {
		t.Fatalf("generated DLC catalog = %#v", dlcs)
	}
}

func TestGenerateDLCOnlyPresetAndRejectsUntrustedLinks(t *testing.T) {
	source := []byte(`<html><body><table>
<tr data-type="DlcContainer"><td data-type="DisplayName">Reaction &amp; Forces</td><td><a href="https://store.steampowered.com/app/2647760/?tracking=private">valid</a></td></tr>
<tr data-type="DlcContainer"><td data-type="DisplayName">Duplicate</td><td><a href="https://store.steampowered.com/app/2647760">valid</a></td></tr>
<tr data-type="DlcContainer"><td data-type="DisplayName">Unsafe</td><td><a href="https://store.steampowered.com.evil/app/1">invalid</a></td></tr>
<tr data-type="DlcContainer"><td data-type="DisplayName">Unsafe</td><td><a href="https://store.steampowered.com@evil/app/2">invalid</a></td></tr>
<tr data-type="DlcContainer"><td data-type="DisplayName">Unsafe</td><td><a href="javascript:alert(1)">invalid</a></td></tr>
<tr data-type="DlcContainer"><td data-type="DisplayName">Not a store ID</td><td data-publishedfileid="999999999"></td></tr>
</table><a href="https://store.steampowered.com/app/1681170">footer</a></body></html>`)
	artifact, err := Generate(source, "session-1", "cDLC", "cdlc", true, domain.CreatorDLCReactionForces, domain.CreatorDLCExpeditionaryForces)
	if err != nil {
		t.Fatal(err)
	}
	preset := parseLauncherPreset(t, artifact.Body)
	dlcs := launcherRows(t, preset, "DlcContainer")
	if len(dlcs) != 2 || dlcs[0] != "Reaction & Forces|https://store.steampowered.com/app/2647760" || artifact.WorkshopCount != 0 {
		t.Fatalf("DLC-only preset = %#v", dlcs)
	}
	for _, invalid := range []string{"Unsafe", "private", "javascript", "999999999", "1681170"} {
		if strings.Contains(string(artifact.Body), invalid) {
			t.Fatalf("untrusted source data retained: %s", invalid)
		}
	}
	if _, err := Generate(source, "session-1", "Session", "session", true, "unknown-dlc"); err == nil {
		t.Fatal("unknown configured DLC was accepted")
	}
}
