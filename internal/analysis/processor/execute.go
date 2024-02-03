package processor

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"os/exec"
	"reflect"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

type ExecuteScriptAction struct {
	ScriptPath string
	Params     map[string]interface{}
}

type SpeciesActionConfig struct {
	SpeciesName string
	Actions     []string // List of action names
	Override    bool     // If true, overrides the default actions
}

// A map to store the action configurations for different species
var speciesActionsMap map[string]SpeciesActionConfig

func LoadSpeciesActionsFromCSV(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}

	speciesActionsMap = make(map[string]SpeciesActionConfig)
	for _, record := range records {
		species := record[0]
		actions := strings.Split(record[1], ";")
		override := record[2] == "true"

		speciesActionsMap[species] = SpeciesActionConfig{
			SpeciesName: species,
			Actions:     actions,
			Override:    override,
		}
	}

	return nil
}

func (p *Processor) createActionsFromConfig(actionConfigs []string, detection Detections) []Action {
	var actions []Action

	for _, config := range actionConfigs {
		actionDetails := strings.Split(config, ":")
		actionName := actionDetails[0]

		switch actionName {
		case "ExecuteScript":
			scriptPath := actionDetails[1]
			params := make(map[string]interface{})

			// Parse additional parameters
			for i := 2; i < len(actionDetails); i++ {
				paramName := actionDetails[i]
				paramValue := getNoteValueByName(detection.Note, paramName)
				params[paramName] = paramValue
			}

			actions = append(actions, ExecuteScriptAction{
				ScriptPath: scriptPath,
				Params:     params,
			})

		}
	}

	return actions
}

func (a ExecuteScriptAction) Execute(data interface{}) error {
	log.Println("Executing script:", a.ScriptPath)
	// Type assertion to check if data is of type Detections
	detection, ok := data.(Detections)
	if !ok {
		return fmt.Errorf("ExecuteScriptAction requires Detections type, got %T", data)
	}

	// Building the command line arguments from the Params map
	var args []string
	for key, value := range a.Params {
		// Fetching the value from detection.Note using reflection
		noteValue := getNoteValueByName(detection.Note, key)
		if noteValue == nil {
			noteValue = value // Use default value if not found in Note
		}

		arg := fmt.Sprintf("--%s=%v", key, noteValue)
		args = append(args, arg)
	}

	fmt.Println("Script arguments:", args)

	// Executing the script with the provided arguments
	cmd := exec.Command(a.ScriptPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error executing script: %v, output: %s", err, string(output))
	}

	fmt.Printf("Script executed successfully: %s\n", string(output))
	return nil
}

func getNoteValueByName(note datastore.Note, paramName string) interface{} {
	val := reflect.ValueOf(note)
	fieldVal := val.FieldByName(paramName)

	// Check if the field is valid (exists in the struct) and can be interfaced
	if fieldVal.IsValid() && fieldVal.CanInterface() {
		return fieldVal.Interface()
	}

	// Return nil or an appropriate zero value if the field does not exist
	return nil
}
