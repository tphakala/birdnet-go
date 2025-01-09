// workers.go contains the worker pool and the worker goroutines that process tasks.
package processor

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// TaskType defines types of tasks that can be handled by the worker.
type TaskType int

const (
	TaskTypeAction TaskType = iota // Represents an action task type
)

// Task represents a unit of work, encapsulating the detection and the action to be performed.
type Task struct {
	Type      TaskType
	Detection Detections
	Action    Action
}

// workerQueue is a channel that holds tasks to be processed by worker goroutines.
var workerQueue chan Task

// StartWorkerPool initializes a pool of worker goroutines to process tasks.
func (p *Processor) startWorkerPool(numWorkers int) {
	workerQueue = make(chan Task, 100) // Initialize the task queue with a buffer

	// Start the specified number of worker goroutines
	for i := 0; i < numWorkers; i++ {
		go p.actionWorker()
	}
}

// actionWorker is the goroutine that processes tasks from the workerQueue.
func (p *Processor) actionWorker() {
	for task := range workerQueue {
		if task.Type == TaskTypeAction {
			// Execute the action associated with the task
			err := task.Action.Execute(task.Detection)
			if err != nil {
				log.Printf("Error executing action: %s\n", err)
				// Handle errors appropriately (e.g., log, retry)
			}
		}
	}
}

// getActionsForItem determines the actions to be taken for a given detection.
func (p *Processor) getActionsForItem(detection Detections) []Action {
	speciesName := strings.ToLower(detection.Note.CommonName)

	// Check if species has custom configuration
	if speciesConfig, exists := p.Settings.Realtime.Species.Config[speciesName]; exists {
		if p.Settings.Debug {
			log.Println("Species config exists for custom actions")
		}

		var actions []Action

		// Add custom actions from the new structure
		for _, actionConfig := range speciesConfig.Actions {
			switch actionConfig.Type {
			case "ExecuteCommand":
				if len(actionConfig.Parameters) > 0 {
					actions = append(actions, ExecuteCommandAction{
						Command: actionConfig.Command,
						Params:  parseCommandParams(actionConfig.Parameters, detection),
					})
				}
			case "SendNotification":
				// Add notification action handling
				// ... implementation ...
			}
		}

		// If OnlyActions is true, return only custom actions
		if len(actions) > 0 {
			return actions
		}
	}

	// Fall back to default actions if no custom actions or if custom actions should be combined
	return p.getDefaultActions(detection)
}

// Helper function to parse command parameters
func parseCommandParams(params []string, detection Detections) map[string]interface{} {
	commandParams := make(map[string]interface{})
	for _, param := range params {
		value := getNoteValueByName(detection.Note, param)
		// Check if the parameter is confidence and normalize it
		if param == "confidence" {
			if confidence, ok := value.(float64); ok {
				value = confidence * 100
			}
		}
		commandParams[param] = value
	}
	return commandParams
}

// getDefaultActions returns the default actions to be taken for a given detection.
func (p *Processor) getDefaultActions(detection Detections) []Action {
	var actions []Action

	// Append various default actions based on the application settings
	if p.Settings.Realtime.Log.Enabled {
		actions = append(actions, LogAction{Settings: p.Settings, EventTracker: p.EventTracker, Note: detection.Note})
	}

	if p.Settings.Output.SQLite.Enabled || p.Settings.Output.MySQL.Enabled {
		actions = append(actions, DatabaseAction{
			Settings:     p.Settings,
			EventTracker: p.EventTracker,
			Note:         detection.Note,
			Results:      detection.Results,
			//AudioBuffer:  p.AudioBuffer,
			Ds: p.Ds})
	}

	/*	if p.Settings.Realtime.AudioExport.Enabled {
		actions = append(actions, SaveAudioAction{Settings: p.Settings, EventTracker: p.EventTracker, pcmData: detection.pcmDataExt, ClipName: detection.Note.ClipName})
	}*/

	// Add BirdWeatherAction if enabled and client is initialized
	if p.Settings.Realtime.Birdweather.Enabled && p.BwClient != nil {
		actions = append(actions, BirdWeatherAction{
			Settings:     p.Settings,
			EventTracker: p.EventTracker,
			BwClient:     p.BwClient,
			Note:         detection.Note,
			pcmData:      detection.pcmData3s})
	}

	// Add MQTT action if enabled
	if p.Settings.Realtime.MQTT.Enabled && p.MqttClient != nil {
		actions = append(actions, MqttAction{
			Settings:       p.Settings,
			MqttClient:     p.MqttClient,
			EventTracker:   p.EventTracker,
			Note:           detection.Note,
			BirdImageCache: p.BirdImageCache,
		})
	}

	// Check if UpdateRangeFilterAction needs to be executed for the day
	today := time.Now().Truncate(24 * time.Hour) // Current date with time set to midnight
	if p.Settings.BirdNET.RangeFilter.LastUpdated.Before(today) {
		fmt.Println("Updating species range filter")
		// Add UpdateRangeFilterAction if it hasn't been executed today
		actions = append(actions, UpdateRangeFilterAction{
			Bn:       p.Bn,
			Settings: p.Settings,
		})
	}

	return actions
}
