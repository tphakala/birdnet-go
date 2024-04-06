// execute.go
package processor

import (
	"fmt"
	"os/exec"
	"reflect"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

type ExecuteScriptAction struct {
	ScriptPath string
	Params     map[string]interface{}
}

// A map to store the action configurations for different species
//var speciesActionsMap map[string]SpeciesActionConfig

func (a ExecuteScriptAction) Execute(data interface{}) error {
	//log.Println("Executing script:", a.ScriptPath)
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

	// Executing the script with the provided arguments
	cmd := exec.Command(a.ScriptPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error executing script: %v, output: %s", err, string(output))
	}

	//fmt.Printf("Script executed successfully: %s", string(output))
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
