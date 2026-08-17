// Package componentid owns the bounded, revisioned custom-ID contract shared
// by Discord message delivery and interaction handling.
package componentid

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	Prefix                = "rb:v1"
	MaximumCustomIDLength = 100

	ActionViewDetails = "view-details"
	ActionRefresh     = "refresh"
	ActionDownload    = "download-modlist"
	ActionHelp        = "help"
)

var (
	actionPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	tokenPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
)

type Reference struct {
	Action   string
	Revision uint64
	Token    string
}

func New(action string, revision uint64, token string) (string, error) {
	action = strings.TrimSpace(action)
	token = strings.TrimSpace(token)
	if !actionPattern.MatchString(action) {
		return "", fmt.Errorf("component action is invalid")
	}
	if revision == 0 {
		return "", fmt.Errorf("component revision must be positive")
	}
	if !tokenPattern.MatchString(token) {
		return "", fmt.Errorf("component token is invalid")
	}
	customID := Prefix + ":" + action + ":" + strconv.FormatUint(revision, 10) + ":" + token
	if len(customID) > MaximumCustomIDLength {
		return "", fmt.Errorf("component custom ID exceeds %d characters", MaximumCustomIDLength)
	}
	return customID, nil
}

func Parse(customID string) (Reference, error) {
	if customID == "" || customID != strings.TrimSpace(customID) || len(customID) > MaximumCustomIDLength {
		return Reference{}, fmt.Errorf("component custom ID is invalid")
	}
	parts := strings.Split(customID, ":")
	if len(parts) != 5 || parts[0]+":"+parts[1] != Prefix {
		return Reference{}, fmt.Errorf("component custom ID schema is unsupported")
	}
	revision, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		return Reference{}, fmt.Errorf("component revision is invalid")
	}
	canonical, err := New(parts[2], revision, parts[4])
	if err != nil || canonical != customID {
		return Reference{}, fmt.Errorf("component custom ID is invalid")
	}
	return Reference{Action: parts[2], Revision: revision, Token: parts[4]}, nil
}
