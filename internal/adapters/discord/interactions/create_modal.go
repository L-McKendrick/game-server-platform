package interactions

import (
	"net/http"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const (
	createModalCustomID = "rb:create:v1"

	createNameCustomID        = "create:name"
	createDescriptionCustomID = "create:description"
	createFeaturesCustomID    = "create:features"
	createMissionCustomID     = "create:mission"
	createPresetCustomID      = "create:preset"

	createFeatureModded    = "modded"
	createFeatureTeamSpeak = "teamspeak"

	defaultGameProfileID = "arma3-default"
	defaultSleepMinutes  = int64(30)
	defaultArchiveDays   = int64(7)
)

func (payload interactionPayload) isRBCreateCommand() bool {
	if payload.Type != interactionTypeApplicationCommand {
		return false
	}
	subcommand, err := payload.subcommand()
	return err == nil && subcommand.Name == "create"
}

func writeCreateModal(writer http.ResponseWriter) {
	writeSessionSetupModal(writer, createModalCustomID, "Create Arma 3 session", domain.Session{}, true)
}

func writeSessionSetupModal(writer http.ResponseWriter, customID, title string, session domain.Session, missionRequired bool) {
	required, optional := true, false
	minimumName, maximumName := 1, 100
	maximumDescription := 64
	minimumNone, minimumOne, maximumOne, maximumFeatures := 0, 1, 1, 2
	missionMinimum := minimumNone
	if missionRequired {
		missionMinimum = minimumOne
	}
	moddedDefault := session.ID == "" || !session.Vanilla

	components := []interactionComponent{
		{
			Type: componentTypeLabel, Label: "Session name",
			Description: "A human-readable name; the platform generates the stable slug.",
			Component: &interactionComponent{
				Type: componentTypeTextInput, CustomID: createNameCustomID, Style: textInputStyleShort,
				Placeholder: "Saturday Arma", Value: session.DisplayName, MinLength: &minimumName, MaxLength: &maximumName, Required: &required,
			},
		},
		{
			Type: componentTypeLabel, Label: "Description",
			Description: "Optional single-line summary (maximum 64 characters).",
			Component: &interactionComponent{
				Type: componentTypeTextInput, CustomID: createDescriptionCustomID, Style: textInputStyleShort,
				Placeholder: "Weekly co-op session", Value: session.Description, MaxLength: &maximumDescription, Required: &optional,
			},
		},
		{
			Type: componentTypeLabel, Label: "Mode and features",
			Description: "Modded is the platform default; clear it for vanilla. TeamSpeak is optional.",
			Component: &interactionComponent{
				Type: componentTypeCheckboxGroup, CustomID: createFeaturesCustomID,
				MinValues: &minimumNone, MaxValues: &maximumFeatures, Required: &optional,
				Options: []interactionSelectOption{
					{Label: "Modded", Value: createFeatureModded, Description: "Use an Arma Launcher preset", Default: moddedDefault},
					{Label: "TeamSpeak", Value: createFeatureTeamSpeak, Description: "Run a TeamSpeak server", Default: session.TeamSpeakEnabled},
				},
			},
		},
		{
			Type: componentTypeLabel, Label: "Mission file",
			Description: setupArtifactDescription("mission", session.MissionArtifactStatus, session.MissionObjectKey, missionRequired),
			Component: &interactionComponent{
				Type: componentTypeFileUpload, CustomID: createMissionCustomID,
				MinValues: &missionMinimum, MaxValues: &maximumOne, Required: &missionRequired,
			},
		},
		{
			Type: componentTypeLabel, Label: "Launcher preset",
			Description: setupArtifactDescription("preset", session.PresetArtifactStatus, session.PresetObjectKey, false),
			Component: &interactionComponent{
				Type: componentTypeFileUpload, CustomID: createPresetCustomID,
				MinValues: &minimumNone, MaxValues: &maximumOne, Required: &optional,
			},
		},
	}

	writeJSON(writer, http.StatusOK, interactionResponse{
		Type: interactionResponseModal,
		Data: &interactionResponseData{
			CustomID:   customID,
			Title:      title,
			Components: &components,
		},
	})
}

func setupArtifactDescription(kind string, status domain.ArtifactStatus, objectKey string, creation bool) string {
	if creation && kind == "mission" {
		return "Required Arma mission .pbo file (maximum 100 MiB)."
	}
	if status == domain.ArtifactAccepted || status == domain.ArtifactPending || strings.TrimSpace(objectKey) != "" {
		return "Already accepted or validating; leave empty because it cannot be replaced here."
	}
	if kind == "mission" {
		return "Upload a replacement .pbo only when the mission is missing or rejected."
	}
	return "Upload a replacement .html only when the preset is missing or rejected."
}
