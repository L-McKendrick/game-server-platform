package interactions

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const (
	createModalCustomID = "rb:create:v2:arma3"
	createGameOption    = "game"
	createGameArma3     = "arma-3"

	createNameCustomID        = "create:name"
	createDescriptionCustomID = "create:description"
	createFeaturesCustomID    = "create:features"
	createMissionCustomID     = "create:mission"
	createPresetCustomID      = "create:preset"

	createFeatureModded    = "modded"
	createFeatureTeamSpeak = "teamspeak"
	createFeatureAutoStart = "auto-start"

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

func createGameType(payload interactionPayload) (string, error) {
	subcommand, err := payload.subcommand()
	if err != nil {
		return "", err
	}
	for _, option := range subcommand.Options {
		if option.Name != createGameOption {
			continue
		}
		if option.Type != applicationCommandOptionString {
			return "", newUserError("Choose a supported game before opening session setup.")
		}
		var selected string
		if err := json.Unmarshal(option.Value, &selected); err != nil || selected != createGameArma3 {
			return "", newUserError("Choose a supported game before opening session setup.")
		}
		return "arma3", nil
	}
	return "", newUserError("Choose a game before opening session setup.")
}

func writeCreateModal(writer http.ResponseWriter, gameType string) {
	if gameType != "arma3" {
		writeInteractionMessage(writer, "That game is not supported yet.")
		return
	}
	writeSessionSetupModal(writer, createModalCustomID, "Create Arma 3 session", domain.Session{}, false, true)
}

func writeSessionSetupModal(writer http.ResponseWriter, customID, title string, session domain.Session, missionRequired, creation bool) {
	required, optional := true, false
	minimumName, maximumName := 1, 100
	maximumDescription := 64
	minimumNone, minimumOne, maximumOne, maximumFeatures := 0, 1, 1, 2
	if creation {
		maximumFeatures = 3
	}
	missionMinimum := minimumNone
	if missionRequired {
		missionMinimum = minimumOne
	}
	moddedDefault := session.ID == "" || !session.Vanilla

	featureOptions := []interactionSelectOption{
		{Label: "Modded", Value: createFeatureModded, Description: "Use a preset and optional Creator DLC", Default: moddedDefault},
		{Label: "TeamSpeak", Value: createFeatureTeamSpeak, Description: "Run a TeamSpeak server", Default: session.TeamSpeakEnabled},
	}
	if creation {
		featureOptions = append(featureOptions, interactionSelectOption{Label: "Begin server setup", Value: createFeatureAutoStart, Description: "Automatically start the server setup process", Default: session.StartWhenReady})
	}
	featureDescription := "Modded is the platform default; clear it for vanilla. TeamSpeak is optional."
	if creation {
		featureDescription = "Choose modded/vanilla, optional TeamSpeak, and automatic setup after validation."
	}
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
			Description: featureDescription,
			Component: &interactionComponent{
				Type: componentTypeCheckboxGroup, CustomID: createFeaturesCustomID,
				MinValues: &minimumNone, MaxValues: &maximumFeatures, Required: &optional,
				Options: featureOptions,
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
	}
	if !creation {
		components = append(components, interactionComponent{
			Type: componentTypeLabel, Label: "Launcher preset",
			Description: setupArtifactDescription("preset", session.PresetArtifactStatus, session.PresetObjectKey, false),
			Component:   &interactionComponent{Type: componentTypeFileUpload, CustomID: createPresetCustomID, MinValues: &minimumNone, MaxValues: &maximumOne, Required: &optional},
		})
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
		return "Optional Arma mission .pbo (maximum 100 MiB)."
	}
	if status == domain.ArtifactAccepted || status == domain.ArtifactPending || strings.TrimSpace(objectKey) != "" {
		return "Already accepted or validating; leave empty because it cannot be replaced here."
	}
	if kind == "mission" {
		return "Upload a .pbo mission file."
	}
	return "Upload a .html modlist file."
}
