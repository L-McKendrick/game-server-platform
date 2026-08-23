// Package modlist generates bounded, sanitized Arma 3 Launcher preset files
// from validated user uploads. It never republishes the original HTML.
package modlist

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"
)

const contentType = "text/html; charset=utf-8"

var (
	modRowPattern      = regexp.MustCompile(`(?is)<tr\b[^>]*\bdata-type\s*=\s*["']ModContainer["'][^>]*>(.*?)</tr>`)
	displayNamePattern = regexp.MustCompile(`(?is)<td\b[^>]*\bdata-type\s*=\s*["']DisplayName["'][^>]*>(.*?)</td>`)
	workshopIDPattern  = regexp.MustCompile(`(?i)(?:[?&]id=|data-publishedfileid=["'])([0-9]{6,20})`)
	tagPattern         = regexp.MustCompile(`(?s)<[^>]*>`)
)

type Artifact struct {
	ObjectKey     string
	Filename      string
	ContentType   string
	Body          []byte
	SHA256Hex     string
	SHA256Base64  string
	WorkshopCount int
}

type workshopMod struct {
	ID   string
	Name string
}

// Generate extracts only Steam Workshop identity and a bounded display name,
// then rebuilds a deterministic launcher-compatible file from scratch.
func Generate(source []byte, sessionID, sessionName, sessionSlug string, allowEmpty bool) (Artifact, error) {
	mods := extractWorkshopMods(string(source))
	if len(mods) == 0 && !allowEmpty {
		return Artifact{}, fmt.Errorf("launcher preset does not contain a Steam Workshop mod")
	}
	if len(mods) > 250 {
		return Artifact{}, fmt.Errorf("launcher preset references more than 250 Workshop items")
	}
	filename := modlistFilename(sessionSlug)
	body := renderPreset(sessionName, mods)
	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	objectKey := fmt.Sprintf("sessions/%s/input/modlists/%s/%s", strings.TrimSpace(sessionID), digestHex, filename)
	return Artifact{
		ObjectKey: objectKey, Filename: filename, ContentType: contentType, Body: body,
		SHA256Hex: digestHex, SHA256Base64: base64.StdEncoding.EncodeToString(digest[:]), WorkshopCount: len(mods),
	}, nil
}

func extractWorkshopMods(source string) []workshopMod {
	mods := make([]workshopMod, 0)
	seen := make(map[string]struct{})
	for _, row := range modRowPattern.FindAllStringSubmatch(source, -1) {
		ids := workshopIDPattern.FindAllStringSubmatch(row[1], -1)
		if len(ids) == 0 {
			continue
		}
		name := ""
		if display := displayNamePattern.FindStringSubmatch(row[1]); len(display) == 2 {
			name = normalizedName(display[1])
		}
		for _, id := range ids {
			if _, found := seen[id[1]]; found {
				continue
			}
			seen[id[1]] = struct{}{}
			modName := name
			if modName == "" {
				modName = "Steam Workshop item " + id[1]
			}
			mods = append(mods, workshopMod{ID: id[1], Name: modName})
		}
	}
	return mods
}

func normalizedName(value string) string {
	return normalizedPlainName(html.UnescapeString(tagPattern.ReplaceAllString(value, " ")))
}

func normalizedPlainName(value string) string {
	var builder strings.Builder
	lastSpace := true
	for _, character := range strings.TrimSpace(value) {
		switch {
		case unicode.IsControl(character), unicode.Is(unicode.Cf, character):
			continue
		case unicode.IsSpace(character):
			if !lastSpace {
				builder.WriteByte(' ')
				lastSpace = true
			}
		default:
			builder.WriteRune(character)
			lastSpace = false
		}
	}
	runes := []rune(strings.TrimSpace(builder.String()))
	if len(runes) > 100 {
		runes = runes[:100]
	}
	return string(runes)
}

func modlistFilename(slug string) string {
	slug = strings.Trim(strings.ToLower(strings.TrimSpace(slug)), "-")
	if slug == "" {
		slug = "session"
	}
	runes := []rune(slug)
	if len(runes) > 52 {
		slug = strings.TrimRight(string(runes[:52]), "-")
	}
	return slug + "-modlist.html"
}

func renderPreset(sessionName string, mods []workshopMod) []byte {
	name := html.EscapeString(normalizedPlainName(sessionName))
	if name == "" {
		name = "Game session"
	}
	var builder strings.Builder
	builder.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n")
	fmt.Fprintf(&builder, "<meta name=\"arma:PresetName\" content=\"%s\">\n", name)
	builder.WriteString("<title>Arma 3 Launcher Preset</title>\n</head>\n<body>\n")
	fmt.Fprintf(&builder, "<h1>Arma 3 - Preset <strong>%s</strong></h1>\n", name)
	builder.WriteString("<p><em>Import this file from Mods / Preset / Import in Arma 3 Launcher.</em></p>\n<div class=\"mod-list\">\n<table>\n")
	for _, mod := range mods {
		workshopURL := "https://steamcommunity.com/sharedfiles/filedetails/?id=" + mod.ID
		fmt.Fprintf(&builder, "<tr data-type=\"ModContainer\">\n<td data-type=\"DisplayName\">%s</td>\n<td><span class=\"from-steam\">Steam</span></td>\n<td><a href=\"%s\" data-type=\"Link\">%s</a></td>\n</tr>\n",
			html.EscapeString(mod.Name), workshopURL, workshopURL)
	}
	builder.WriteString("</table>\n</div>\n</body>\n</html>\n")
	return []byte(builder.String())
}
