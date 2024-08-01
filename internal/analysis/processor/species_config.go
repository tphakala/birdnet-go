package processor

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
)

/*
// ActionConfig holds configuration details for a specific action.

	type ActionConfig struct {
		Name       string   // Name of the action
		Parameters []string // List of parameters for the action
	}

// SpeciesActionConfig represents the configuration for actions specific to a species.

	type SpeciesActionConfig struct {
		SpeciesName string         // Name of the species
		Actions     []ActionConfig // Configurations for actions associated with this species
		Exclude     []string       // List of actions to exclude
		OnlyActions bool           // Flag to indicate if only these actions should be executed
	}

// SpeciesConfig holds custom thresholds and action configurations for species.

	type SpeciesConfig struct {
		Threshold map[string]float32             // Custom confidence thresholds for species
		Actions   map[string]SpeciesActionConfig // Actions configurations for species
	}
*/

func LoadSpeciesConfig(fileName string) (conf.SpeciesSettings, error) {
	var speciesConfig conf.SpeciesSettings
	speciesConfig.Threshold = make(map[string]float32)
	speciesConfig.Actions = make(map[string]conf.SpeciesActionConfig)

	// Retrieve the default config paths.
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		return conf.SpeciesSettings{}, fmt.Errorf("error getting default config paths: %w", err)
	}

	var file *os.File

	// Try to open the file in one of the default config paths.
	for _, path := range configPaths {
		fullPath := filepath.Join(path, fileName)
		file, err = os.Open(fullPath)
		if err == nil {
			break
		}
	}

	if file == nil {
		return conf.SpeciesSettings{}, fmt.Errorf("file '%s' not found in default config paths", fileName)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comment = '#'        // Set comment character
	reader.FieldsPerRecord = -1 // Allow a variable number of fields

	log.Println("Loading species config from file:", fileName)

	records, err := reader.ReadAll()
	if err != nil {
		log.Printf("Error reading CSV file '%s': %v", fileName, err)
		return conf.SpeciesSettings{}, fmt.Errorf("error reading CSV file '%s': %w", fileName, err)
	}

	for _, record := range records {
		if len(record) < 2 {
			log.Printf("Invalid line in species config: %v", record)
			continue // Skip malformed lines
		}

		species := strings.ToLower(strings.TrimSpace(record[0]))
		confidence, err := strconv.ParseFloat(strings.TrimSpace(record[1]), 32)
		if err != nil {
			log.Printf("Invalid confidence value for species '%s': %v", species, err)
			continue
		} else {
			if conf.Setting().Debug {
				log.Printf("Config loaded species: %s confidence: %.3f\n", species, confidence)
			}
		}

		// Initialize default actions and exclude
		var actions []conf.ActionConfig
		var exclude []string
		onlyActions := false

		// If the line has action configurations
		if len(record) > 2 {
			actions, exclude, onlyActions = parseActions(strings.TrimSpace(record[2]))
		}

		speciesConfig.Threshold[species] = float32(confidence)
		speciesConfig.Actions[species] = conf.SpeciesActionConfig{
			SpeciesName: species,
			Actions:     actions,
			Exclude:     exclude,
			OnlyActions: onlyActions,
		}
	}

	return speciesConfig, nil
}

// parseActions interprets the action string from the CSV and returns action configurations.
func parseActions(actionStr string) (actions []conf.ActionConfig, exclude []string, onlyActions bool) {
	actionList := strings.Split(actionStr, ";")
	onlyActions = true

	for _, action := range actionList {
		actionDetails := strings.Split(action, ":")
		actionName := strings.TrimPrefix(actionDetails[0], "+")

		// Check for exclusion
		if strings.HasPrefix(actionDetails[0], "-") {
			exclude = append(exclude, strings.TrimPrefix(actionDetails[0], "-"))
			continue
		}

		// Handle inclusion and parameters
		actions = append(actions, conf.ActionConfig{
			Name:       actionName,
			Parameters: actionDetails[1:], // Parameters follow the action name
		})

		if strings.HasPrefix(actionDetails[0], "+") {
			onlyActions = false
		}
	}

	return actions, exclude, onlyActions
}

// createActionsFromConfig creates actions based on the given species configuration and detection.
func (p *Processor) createActionsFromConfig(speciesConfig conf.SpeciesActionConfig, detection Detections) []Action {
	var actions []Action

	for _, actionConfig := range speciesConfig.Actions {
		// Skip excluded actions
		if contains(speciesConfig.Exclude, actionConfig.Name) {
			continue
		}

		// Handle different action types
		switch actionConfig.Name {
		case "ExecuteScript":
			if len(actionConfig.Parameters) >= 1 {
				scriptPath := actionConfig.Parameters[0]
				scriptParams := make(map[string]interface{})
				for _, paramName := range actionConfig.Parameters[1:] {
					scriptParams[paramName] = getNoteValueByName(detection.Note, paramName)
				}
				actions = append(actions, ExecuteScriptAction{
					ScriptPath: scriptPath,
					Params:     scriptParams,
				})
			}
		case "SendNotification":
			// Create SendNotification action
			// ... implementation ...
		}

		// Add more cases for additional action types as needed
	}

	return actions
}

// contains checks if a slice of strings contains a specific string.
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
