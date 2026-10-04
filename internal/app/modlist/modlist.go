// Package modlist generates bounded, sanitized Arma 3 Launcher preset files
// from validated user uploads. It never republishes the original HTML.
package modlist

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const contentType = "text/html; charset=utf-8"

var (
	modRowPattern      = regexp.MustCompile(`(?is)<tr\b[^>]*\bdata-type\s*=\s*["']ModContainer["'][^>]*>(.*?)</tr>`)
	dlcRowPattern      = regexp.MustCompile(`(?is)<tr\b[^>]*\bdata-type\s*=\s*["']DlcContainer["'][^>]*>(.*?)</tr>`)
	hrefPattern        = regexp.MustCompile(`(?is)<a\b[^>]*\shref\s*=\s*["']([^"']+)["'][^>]*>`)
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

type WorkshopMod struct {
	ID   uint64
	Name string
}

// Store identities are separate from Workshop identities, even when their
// numeric IDs overlap. These entries mirror the platform's supported catalog.
var creatorDLCApps = map[string]workshopMod{
	domain.CreatorDLCGlobalMobilization:  {ID: "1042220", Name: "Global Mobilization - Cold War Germany"},
	domain.CreatorDLCSOGPrairieFire:      {ID: "1227700", Name: "S.O.G. Prairie Fire"},
	domain.CreatorDLCCSLAIronCurtain:     {ID: "1294440", Name: "CSLA Iron Curtain"},
	domain.CreatorDLCWesternSahara:       {ID: "1681170", Name: "Western Sahara"},
	domain.CreatorDLCSpearhead1944:       {ID: "1175380", Name: "Spearhead 1944"},
	domain.CreatorDLCReactionForces:      {ID: "2647760", Name: "Reaction Forces"},
	domain.CreatorDLCExpeditionaryForces: {ID: "2647830", Name: "Expeditionary Forces"},
}

func GenerateWorkshop(mods []WorkshopMod, sessionID, sessionName, sessionSlug string, creatorDLCs ...string) (Artifact, error) {
	if len(mods) == 0 || len(mods) > 250 {
		return Artifact{}, fmt.Errorf("Workshop mod count must be between 1 and 250")
	}
	normalized := make([]workshopMod, 0, len(mods))
	seen := map[uint64]bool{}
	for _, mod := range mods {
		if mod.ID == 0 || seen[mod.ID] {
			return Artifact{}, fmt.Errorf("Workshop mod identity is invalid or duplicated")
		}
		seen[mod.ID] = true
		name := normalizedPlainName(mod.Name)
		if name == "" {
			name = fmt.Sprintf("Steam Workshop item %d", mod.ID)
		}
		normalized = append(normalized, workshopMod{ID: fmt.Sprintf("%d", mod.ID), Name: name})
	}
	filename := modlistFilename(sessionSlug)
	dlcs, err := presetDLCs("", creatorDLCs)
	if err != nil {
		return Artifact{}, err
	}
	body := renderPreset(sessionName, normalized, dlcs)
	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	return Artifact{ObjectKey: fmt.Sprintf("sessions/%s/input/modlists/%s/%s", strings.TrimSpace(sessionID), digestHex, filename), Filename: filename, ContentType: contentType, Body: body, SHA256Hex: digestHex, SHA256Base64: base64.StdEncoding.EncodeToString(digest[:]), WorkshopCount: len(normalized)}, nil
}

// Generate extracts Steam Workshop and Steam Store DLC identities and bounded names,
// then rebuilds a deterministic launcher-compatible file from scratch.
func Generate(source []byte, sessionID, sessionName, sessionSlug string, allowEmpty bool, creatorDLCs ...string) (Artifact, error) {
	mods := extractWorkshopMods(string(source))
	if len(mods) == 0 && !allowEmpty {
		return Artifact{}, fmt.Errorf("launcher preset does not contain a Steam Workshop mod")
	}
	if len(mods) > 250 {
		return Artifact{}, fmt.Errorf("launcher preset references more than 250 Workshop items")
	}
	filename := modlistFilename(sessionSlug)
	dlcs, err := presetDLCs(string(source), creatorDLCs)
	if err != nil {
		return Artifact{}, err
	}
	body := renderPreset(sessionName, mods, dlcs)
	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	objectKey := fmt.Sprintf("sessions/%s/input/modlists/%s/%s", strings.TrimSpace(sessionID), digestHex, filename)
	return Artifact{
		ObjectKey: objectKey, Filename: filename, ContentType: contentType, Body: body,
		SHA256Hex: digestHex, SHA256Base64: base64.StdEncoding.EncodeToString(digest[:]), WorkshopCount: len(mods),
	}, nil
}

func presetDLCs(source string, configured []string) ([]workshopMod, error) {
	selected, err := domain.NormalizeCreatorDLCs(configured)
	if err != nil {
		return nil, err
	}
	dlcs := make([]workshopMod, 0)
	seen := make(map[string]bool)
	for _, row := range dlcRowPattern.FindAllStringSubmatch(source, -1) {
		for _, link := range hrefPattern.FindAllStringSubmatch(row[1], -1) {
			parsed, err := url.Parse(html.UnescapeString(link[1]))
			if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "store.steampowered.com") || parsed.User != nil {
				continue
			}
			parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
			if len(parts) < 2 || parts[0] != "app" {
				continue
			}
			appID, err := strconv.ParseUint(parts[1], 10, 32)
			if err != nil || appID == 0 {
				continue
			}
			id := strconv.FormatUint(appID, 10)
			if seen[id] {
				continue
			}
			name := "Steam DLC " + id
			if display := displayNamePattern.FindStringSubmatch(row[1]); len(display) == 2 {
				if normalized := normalizedName(display[1]); normalized != "" {
					name = normalized
				}
			}
			seen[id] = true
			dlcs = append(dlcs, workshopMod{ID: id, Name: name})
		}
	}
	for _, selection := range selected {
		dlc := creatorDLCApps[selection]
		if !seen[dlc.ID] {
			seen[dlc.ID] = true
			dlcs = append(dlcs, dlc)
		}
	}
	if len(dlcs) > 32 {
		return nil, fmt.Errorf("launcher preset references more than 32 DLCs")
	}
	return dlcs, nil
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
		case unicode.IsControl(character), unicode.Is(unicode.Cf, character), character == '\ufffe', character == '\uffff':
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

func renderPreset(sessionName string, mods, dlcs []workshopMod) []byte {
	name := html.EscapeString(normalizedPlainName(sessionName))
	if name == "" {
		name = "Game session"
	}
	var builder strings.Builder
	builder.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<html>\n<head>\n<meta name=\"arma:Type\" content=\"preset\" />\n")
	fmt.Fprintf(&builder, "<meta name=\"arma:PresetName\" content=\"%s\" />\n", name)
	builder.WriteString("<meta name=\"generator\" content=\"Game Server Platform\" />\n")
	builder.WriteString("<title>Arma 3 Launcher Preset</title>\n</head>\n<body>\n")
	fmt.Fprintf(&builder, "<h1>Arma 3 - Preset <strong>%s</strong></h1>\n", name)
	builder.WriteString("<p><em>Import this file from Mods / Preset / Import in Arma 3 Launcher.</em></p>\n<div class=\"mod-list\">\n<table>\n")
	for _, mod := range mods {
		workshopURL := "https://steamcommunity.com/sharedfiles/filedetails/?id=" + mod.ID
		fmt.Fprintf(&builder, "<tr data-type=\"ModContainer\">\n<td data-type=\"DisplayName\">%s</td>\n<td><span class=\"from-steam\">Steam</span></td>\n<td><a href=\"%s\" data-type=\"Link\">%s</a></td>\n</tr>\n",
			html.EscapeString(mod.Name), workshopURL, workshopURL)
	}
	builder.WriteString("</table>\n</div>\n")
	if len(dlcs) > 0 {
		builder.WriteString("<div class=\"dlc-list\">\n<table>\n")
		for _, dlc := range dlcs {
			storeURL := "https://store.steampowered.com/app/" + dlc.ID
			fmt.Fprintf(&builder, "<tr data-type=\"DlcContainer\">\n<td data-type=\"DisplayName\">%s</td>\n<td><a href=\"%s\" data-type=\"Link\">%s</a></td>\n</tr>\n", html.EscapeString(dlc.Name), storeURL, storeURL)
		}
		builder.WriteString("</table>\n</div>\n")
	}
	builder.WriteString("</body>\n</html>\n")
	return []byte(builder.String())
}
