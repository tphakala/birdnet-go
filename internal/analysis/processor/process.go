package processor

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/queue"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/observation"
)

type Processor struct {
	Settings           *conf.Settings
	Ds                 datastore.Interface
	Bn                 *birdnet.BirdNET
	BwClient           *birdweather.BwClient
	EventTracker       *EventTracker
	SpeciesConfig      SpeciesConfig
	IncludedSpecies    *[]string // Field to hold the list of included species
	SpeciesListUpdated time.Time
}

type Detections struct {
	pcmData []byte
	Note    datastore.Note
}

func New(settings *conf.Settings, ds datastore.Interface, bn *birdnet.BirdNET) *Processor {
	p := &Processor{
		Settings:        settings,
		Ds:              ds,
		Bn:              bn,
		EventTracker:    NewEventTracker(),
		IncludedSpecies: new([]string),
	}

	// Start the detection processor
	p.StartDetectionProcessor()
	p.StartWorkerPool(5)

	// Load Species configs
	p.SpeciesConfig, _ = LoadSpeciesConfig(conf.SpeciesConfigCSV)

	// Initialize BirdWeather client if enabled in settings.
	if settings.Realtime.Birdweather.Enabled {
		p.BwClient = birdweather.New(settings)
	}

	// Initialize included species list
	today := time.Now().Truncate(24 * time.Hour)
	*p.IncludedSpecies = bn.GetProbableSpecies()
	p.SpeciesListUpdated = today

	return p
}

func (p *Processor) StartDetectionProcessor() {
	go func() {
		for item := range queue.ResultsQueue {
			p.processDetections(item)
		}
	}()
}

// processDetection processes a single detection and queues actions for it.
func (p *Processor) processDetections(item *queue.Results) {
	detections, err := p.processResults(item)
	if err != nil {
		// Handle error
		return
	}

	for _, detection := range detections {
		actionList := p.getActionsForItem(detection)
		//fmt.Println("Detection:", detection, "Action:", actionList)
		for _, action := range actionList {
			workerQueue <- Task{Type: TaskTypeAction, Detection: detection, Action: action}
		}
	}

}

func (p *Processor) processResults(item *queue.Results) ([]Detections, error) {
	var detections []Detections

	// item.Results could contain up to 10 results, process all of them
	for _, result := range item.Results {
		scientificName, commonName, _ := observation.ParseSpeciesString(result.Species)
		confidence := result.Confidence

		// Convert species to lowercase for case-insensitive comparison
		speciesLowercase := strings.ToLower(commonName)
		if confidence > 0.01 {
			//fmt.Println("speciesLowercase: ", speciesLowercase)
		}

		// Use custom confidence threshold if it exists for the species, otherwise use the global threshold
		confidenceThreshold, exists := p.SpeciesConfig.Threshold[speciesLowercase]
		if !exists {
			confidenceThreshold = float32(p.Settings.BirdNET.Threshold)
		} else {
			if p.Settings.Debug {
				//fmt.Printf("\nUsing confidence threshold of %.2f for %s\n", confidenceThreshold, species)
			}
		}

		if confidence <= confidenceThreshold {
			// confidence too low, skip processing
			continue
		}

		// match against location based filter
		if !isSpeciesIncluded(result.Species, *p.IncludedSpecies) {
			if p.Settings.Debug {
				log.Printf("Species not on included list: %s\n", commonName)
			}
			continue
		}

		item.ClipName = p.generateClipName(scientificName, confidence)
		//log.Println("clipName: ", item.ClipName)

		beginTime, endTime := 0.0, 0.0
		note := observation.New(p.Settings, beginTime, endTime, result.Species, float64(result.Confidence), item.ClipName, item.ElapsedTime)

		// detection passed all filters, process it
		detections = append(detections, Detections{
			pcmData: item.PCMdata,
			Note:    note,
		})
	}

	//return nil, clipName
	return detections, nil
}

func (p *Processor) generateClipName(scientificName string, confidence float32) string {
	// Get the base path from the configuration
	basePath := conf.GetBasePath(p.Settings.Realtime.AudioExport.Path)

	// Replace whitespaces with underscores and convert to lowercase
	formattedName := strings.ToLower(strings.ReplaceAll(scientificName, " ", "_"))

	// Normalize the confidence value to a percentage and append 'p'
	normalizedConfidence := confidence * 100
	formattedConfidence := fmt.Sprintf("%.0fp", normalizedConfidence)

	// Get the current time
	currentTime := time.Now()

	// Format the timestamp in ISO 8601 format
	timestamp := currentTime.Format("20060102T150405Z")

	// Extract the year and month for directory structure
	year := currentTime.Format("2006")
	month := currentTime.Format("01")

	// Set the file extension
	fileType := "wav"

	// Construct the clip name with the new pattern, including year and month subdirectories
	clipName := fmt.Sprintf("%s/%s/%s/%s_%s_%s.%s", basePath, year, month, formattedName, formattedConfidence, timestamp, fileType)

	return clipName
}

// isSpeciesIncluded checks if the given species is in the included species list.
// It returns true if the species is in the list, or if the list is empty (no filtering).
func isSpeciesIncluded(species string, includedList []string) bool {
	if len(includedList) == 0 {
		return true // no filtering applied when the list is empty
	}
	for _, s := range includedList {
		if species == s {
			return true
		}
	}

	return false
}
