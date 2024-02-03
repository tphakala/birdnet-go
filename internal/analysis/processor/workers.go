// processor/workers.go

package processor

import (
	"log"
)

type TaskType int

const (
	TaskTypeAction TaskType = iota
)

type Task struct {
	Type      TaskType
	Detection Detections
	Action    Action
}

var workerQueue chan Task

// StartWorkerPool initializes the worker pool for actions.
func (p *Processor) StartWorkerPool(numWorkers int) {
	workerQueue = make(chan Task, 100) // Buffer size can be adjusted as needed

	for i := 0; i < numWorkers; i++ {
		go p.actionWorker()
	}
}

// actionWorker is the goroutine that processes each action.
func (p *Processor) actionWorker() {
	for task := range workerQueue {
		if task.Type == TaskTypeAction {
			err := task.Action.Execute(task.Detection)
			if err != nil {
				log.Printf("Error executing action: %s\n", err)
				// Handle error for action, e.g., logging or adding to a retry queue
			}
		}
	}
}

func (p *Processor) worker(id int) {
	for task := range workerQueue {
		err := task.Action.Execute(task.Detection)
		if err != nil {
			// Handle error (e.g., add to retry queue)
		}
	}
}

func (p *Processor) getActionsForItem(detection Detections) []Action {
	speciesConfig, exists := speciesActionsMap[detection.Note.CommonName]

	var actions []Action
	if exists {
		log.Println("Species config exists for custom actions")
		if speciesConfig.Override {
			log.Println("Overriding default actions")
			actions = p.createActionsFromConfig(speciesConfig.Actions, detection)
		} else {
			actions = p.getDefaultActions(detection)
			extraActions := p.createActionsFromConfig(speciesConfig.Actions, detection)
			actions = append(actions, extraActions...)
		}
	} else {
		log.Println("No species config found, using default actions")
		actions = p.getDefaultActions(detection)
	}

	return actions
}

// getDefaultActions returns the default actions for a detections
func (p *Processor) getDefaultActions(detection Detections) []Action {
	var actions []Action
	// OBS Chatlog
	if p.Ctx.Settings.Realtime.Log.Enabled {
		actions = append(actions, LogAction{Ctx: p.Ctx, Note: detection.Note})
	}
	// Save to database
	if p.Ctx.Settings.Output.SQLite.Enabled || p.Ctx.Settings.Output.MySQL.Enabled {
		actions = append(actions, DatabaseAction{Ctx: p.Ctx, Note: detection.Note, Ds: p.Ds})
	}
	// Save audio clips to disk
	if p.Ctx.Settings.Realtime.AudioExport.Enabled {
		actions = append(actions, SaveAudioAction{Ctx: p.Ctx, pcmData: detection.pcmData, ClipName: detection.Note.ClipName})
	}
	// Upload to BirdWeather
	if p.Ctx.Settings.Realtime.Birdweather.Enabled {
		actions = append(actions, BirdweatherAction{Ctx: p.Ctx, BirdweatherClient: p.BirdweatherClient, Note: detection.Note})
	}

	return actions
}

/*
func (p *Processor) getActionsForItem(detection Detections) []Action {
	logAction := LogAction{
		Ctx:  p.Ctx,
		Note: detection.Note, // Assuming item has a Result field
	}

	saveAudioAction := SaveAudioAction{
		Ctx:      p.Ctx,
		PCMdata:  detection.PCMdata, // Assuming item has a Result field
		ClipName: detection.Note.ClipName,
	}

	databaseAction := DatabaseAction{
		Ctx:  p.Ctx,
		Note: detection.Note, // Assuming item has a Result field
		Ds:   p.Ds,
	}

	birdweatherAction := BirdweatherAction{
		Ctx:  p.Ctx,
		Note: detection.Note, // Assuming item has a Result field
	}

	return []Action{
		logAction,         // Log the detection
		saveAudioAction,   // Save the audio clip
		databaseAction,    // Save the detection to the database
		birdweatherAction, // Send the detection to BirdWeather API
	}
}*/
