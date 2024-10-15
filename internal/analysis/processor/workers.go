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
	// match lower case
	speciesName := strings.ToLower(detection.Note.CommonName)
	speciesConfig, exists := p.Settings.Realtime.Species.Actions[speciesName]

	var actions []Action
	if exists {
		if p.Settings.Debug {
			log.Println("Species config exists for custom actions")
		}
		customActions := p.createActionsFromConfig(speciesConfig, detection)

		// Determine whether to use only custom actions or combine with default actions
		if speciesConfig.OnlyActions {
			//log.Println("Only using custom actions for", speciesName)
			actions = customActions
		} else {
			//log.Println("Using default actions with custom actions for", speciesName)
			defaultActions := p.getDefaultActions(detection)
			actions = append(defaultActions, customActions...)
		}
	} else {
		if p.Settings.Debug {
			log.Println("No species config found, using default actions for", speciesName)
		}
		actions = p.getDefaultActions(detection)
	}

	return actions
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
