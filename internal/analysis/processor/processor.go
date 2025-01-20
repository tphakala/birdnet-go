// processor.go
package processor

import (
	"context"
	"fmt"
	"log"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/queue"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observation"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// Processor represents the main processing unit for audio analysis.
type Processor struct {
	Settings            *conf.Settings
	Ds                  datastore.Interface
	Bn                  *birdnet.BirdNET
	BwClient            *birdweather.BwClient
	MqttClient          mqtt.Client
	BirdImageCache      *imageprovider.BirdImageCache
	EventTracker        *EventTracker
	LastDogDetection    map[string]time.Time // keep track of dog barks per audio source
	LastHumanDetection  map[string]time.Time // keep track of human vocal per audio source
	Metrics             *telemetry.Metrics
	DynamicThresholds   map[string]*DynamicThreshold
	thresholdsMutex     sync.RWMutex // Mutex to protect access to DynamicThresholds
	pendingDetections   map[string]PendingDetection
	pendingMutex        sync.Mutex // Mutex to protect access to pendingDetections
	lastDogDetectionLog map[string]time.Time
	dogDetectionMutex   sync.Mutex
	controlChan         chan string
}

// DynamicThreshold represents the dynamic threshold configuration for a species.
type DynamicThreshold struct {
	Level         int
	CurrentValue  float64
	Timer         time.Time
	HighConfCount int
	ValidHours    int
}

type Detections struct {
	pcmData3s []byte              // 3s PCM data containing the detection
	Note      datastore.Note      // Note containing highest match
	Results   []datastore.Results // Full BirdNET prediction results
}

// PendingDetection struct represents a single detection held in memory,
// including its last updated timestamp and a deadline for flushing it to the worker queue.
type PendingDetection struct {
	Detection     Detections // The detection data
	Confidence    float64    // Confidence level of the detection
	Source        string     // Audio source of the detection, RTSP URL or audio card name
	FirstDetected time.Time  // Time the detection was first detected
	LastUpdated   time.Time  // Last time this detection was updated
	FlushDeadline time.Time  // Deadline by which the detection must be processed
	Count         int        // Number of times this detection has been updated
}

// mutex is used to synchronize access to the PendingDetections map,
// ensuring thread safety when the map is accessed or modified by concurrent goroutines.
var mutex sync.Mutex

// func New(settings *conf.Settings, ds datastore.Interface, bn *birdnet.BirdNET, audioBuffers map[string]*myaudio.AudioBuffer, metrics *telemetry.Metrics) *Processor {
func New(settings *conf.Settings, ds datastore.Interface, bn *birdnet.BirdNET, metrics *telemetry.Metrics, birdImageCache *imageprovider.BirdImageCache) *Processor {
	p := &Processor{
		Settings:            settings,
		Ds:                  ds,
		Bn:                  bn,
		BirdImageCache:      birdImageCache,
		EventTracker:        NewEventTracker(time.Duration(settings.Realtime.Interval) * time.Second),
		Metrics:             metrics,
		LastDogDetection:    make(map[string]time.Time),
		LastHumanDetection:  make(map[string]time.Time),
		DynamicThresholds:   make(map[string]*DynamicThreshold),
		pendingDetections:   make(map[string]PendingDetection),
		lastDogDetectionLog: make(map[string]time.Time),
	}

	// Start the detection processor
	p.startDetectionProcessor()

	// Start the worker pool for action processing
	p.startWorkerPool(10)

	// Start the held detection flusher
	p.pendingDetectionsFlusher()

	// Initialize BirdWeather client if enabled in settings
	if settings.Realtime.Birdweather.Enabled {
		var err error
		p.BwClient, err = birdweather.New(settings)
		if err != nil {
			log.Printf("failed to create Birdweather client: %s", err)
		}
	}

	// Initialize MQTT client if enabled in settings.
	if settings.Realtime.MQTT.Enabled {
		var err error
		// Create a new MQTT client using the settings and metrics
		p.MqttClient, err = mqtt.NewClient(settings, p.Metrics)
		if err != nil {
			// Log an error if client creation fails
			log.Printf("failed to create MQTT client: %s", err)
		} else {
			// Create a context with a 30-second timeout for the connection attempt
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel() // Ensure the cancel function is called to release resources

			// Attempt to connect to the MQTT broker
			if err := p.MqttClient.Connect(ctx); err != nil {
				// Log an error if the connection attempt fails
				log.Printf("failed to connect to MQTT broker: %s", err)
			}
			// Note: Successful connection is implied if no error is logged
		}
	}

	return p
}

// Start goroutine to process detections from the queue
func (p *Processor) startDetectionProcessor() {
	go func() {
		// ResultsQueue is fed by myaudio.ProcessData()
		for item := range queue.ResultsQueue {
			p.processDetections(item)
		}
	}()
}

// processDetections examines each detection from the queue, updating held detections
// with new or higher-confidence instances and setting an appropriate flush deadline.
func (p *Processor) processDetections(item queue.Results) {
	// Delay before a detection is considered final and is flushed.
	// TODO: make this configurable
	const delay = 15 * time.Second

	// processResults() returns a slice of detections, we iterate through each and process them
	// detections are put into pendingDetections map where they are held until flush deadline is reached
	// once deadline is reached detections are delivered to workers for actions (save to db etc) processing
	for _, detection := range p.processResults(item) {
		commonName := strings.ToLower(detection.Note.CommonName)
		confidence := detection.Note.Confidence

		// Lock the mutex to ensure thread-safe access to shared resources
		p.pendingMutex.Lock()

		// Check if this species is already in the pending detections
		pendingDetection, exists := p.pendingDetections[commonName]

		if exists {
			// Update the existing detection if it's already in pendingDetections map
			p.updateExistingDetection(&pendingDetection, detection, item, confidence)
		} else {
			// Create a new pending detection if it doesn't exist
			p.createNewDetection(&pendingDetection, detection, item, confidence, delay)
		}

		// Update detection count for this species
		pendingDetection.Count++
		// DEBUG
		//log.Printf("debug: increasing count for %s to %d\n", commonName, pendingDetection.Count)

		// Update the detection in the map with the modified or new pending detection
		p.pendingDetections[commonName] = pendingDetection

		// Update the dynamic threshold for this species if enabled
		p.updateDynamicThreshold(commonName, confidence)

		// Unlock the mutex to allow other goroutines to access shared resources
		p.pendingMutex.Unlock()
	}
}

// updateExistingDetection updates an existing detection if the new confidence is higher
// and adjusts the FlushDeadline if necessary.
func (p *Processor) updateExistingDetection(pendingDetection *PendingDetection, detection Detections, item queue.Results, confidence float64) {
	// Only update detection details if new confidence is higher
	if confidence > pendingDetection.Confidence {
		log.Printf("Updating detection: %s with confidence: %.2f, source: %v, count: %d\n", detection.Note.CommonName, confidence, item.Source, pendingDetection.Count+1)
		pendingDetection.Detection = detection
		pendingDetection.Confidence = confidence
		pendingDetection.Source = item.Source
		pendingDetection.LastUpdated = time.Now()
	}

	// Keep the original FirstDetected
	// Update FlushDeadline only if it has passed
	/*if time.Now().After(pendingDetection.FlushDeadline) {
		log.Printf("Updating FlushDeadline for %s to %v\n", detection.Note.CommonName, time.Now().Add(15*time.Second))
		pendingDetection.FlushDeadline = time.Now().Add(15 * time.Second)
	}*/
}

// createNewDetection initializes a new PendingDetection struct with the given detection information.
func (p *Processor) createNewDetection(pendingDetection *PendingDetection, detection Detections, item queue.Results, confidence float64, delay time.Duration) {
	log.Printf("New detection: %s with confidence: %.2f, source: %v\n", detection.Note.CommonName, confidence, item.Source)

	firstDetected := item.StartTime
	*pendingDetection = PendingDetection{
		Detection:     detection,
		Confidence:    confidence,
		Source:        item.Source,
		FirstDetected: firstDetected,
		FlushDeadline: firstDetected.Add(delay),
		Count:         0, // Will be incremented to 1 in the main function
	}
}

// processResults processes the results from the BirdNET prediction and returns a list of detections.
func (p *Processor) processResults(item queue.Results) []Detections {
	var detections []Detections

	// Collect processing time metric
	if p.Settings.Realtime.Telemetry.Enabled && p.Metrics != nil && p.Metrics.BirdNET != nil {
		p.Metrics.BirdNET.SetProcessTime(float64(item.ElapsedTime.Milliseconds()))
	}

	// Process each result in item.Results
	for _, result := range item.Results {
		var confidenceThreshold float32
		scientificName, commonName, _ := observation.ParseSpeciesString(result.Species)

		// Convert species to lowercase for case-insensitive comparison
		speciesLowercase := strings.ToLower(commonName)

		// Handle dog and human detection, this sets LastDogDetection and LastHumanDetection which is
		// later used to discard detection if privacy filter or dog bark filters are enabled in settings.
		p.handleDogDetection(item, speciesLowercase, result)
		p.handleHumanDetection(item, speciesLowercase, result)

		// Determine base confidence threshold
		baseThreshold := p.getBaseConfidenceThreshold(speciesLowercase)

		// If result is human and detection exceeds base threshold, discard it
		// due to privacy reasons we do not want human detections to reach actions stage
		if strings.Contains(strings.ToLower(commonName), "human") &&
			result.Confidence > baseThreshold {
			continue
		}

		if p.Settings.Realtime.DynamicThreshold.Enabled {
			// Apply dynamic threshold adjustments
			confidenceThreshold = p.getAdjustedConfidenceThreshold(speciesLowercase, result, baseThreshold)
		} else {
			// Use the base threshold if dynamic thresholds are disabled
			confidenceThreshold = baseThreshold
		}

		// Skip processing if confidence is too low
		if result.Confidence <= confidenceThreshold {
			continue
		}

		// Match against location-based filter
		if !p.Settings.IsSpeciesIncluded(result.Species) {
			if p.Settings.Debug {
				log.Printf("Species not on included list: %s\n", result.Species)
			}
			continue
		}

		if p.Settings.Realtime.DynamicThreshold.Enabled {
			// Add species to dynamic thresholds if it passes the filter
			p.addSpeciesToDynamicThresholds(speciesLowercase, baseThreshold)
		}

		// Create file name for audio clip
		clipName := p.generateClipName(scientificName, result.Confidence)

		// set begin and end time for note
		// TODO: adjust end time based on detection pending delay
		beginTime, endTime := item.StartTime, item.StartTime.Add(15*time.Second)

		note := observation.New(p.Settings, beginTime, endTime, result.Species, float64(result.Confidence), item.Source, clipName, item.ElapsedTime)

		// Detection passed all filters, process it
		detections = append(detections, Detections{
			pcmData3s: item.PCMdata,
			Note:      note,
			Results:   item.Results,
		})
	}

	return detections
}

// handleDogDetection handles the detection of dog barks and updates the last detection timestamp.
func (p *Processor) handleDogDetection(item queue.Results, speciesLowercase string, result datastore.Results) {
	if p.Settings.Realtime.DogBarkFilter.Enabled && strings.Contains(speciesLowercase, "dog") &&
		result.Confidence > p.Settings.Realtime.DogBarkFilter.Confidence {
		log.Printf("Dog detected with confidence %.3f/%.3f from source %s", result.Confidence, p.Settings.Realtime.DogBarkFilter.Confidence, item.Source)
		p.LastDogDetection[item.Source] = item.StartTime
	}
}

// handleHumanDetection handles the detection of human vocalizations and updates the last detection timestamp.
func (p *Processor) handleHumanDetection(item queue.Results, speciesLowercase string, result datastore.Results) {
	// only check this if privacy filter is enabled
	if p.Settings.Realtime.PrivacyFilter.Enabled && strings.Contains(speciesLowercase, "human ") &&
		result.Confidence > p.Settings.Realtime.PrivacyFilter.Confidence {
		log.Printf("Human detected with confidence %.3f/%.3f from source %s", result.Confidence, p.Settings.Realtime.PrivacyFilter.Confidence, item.Source)
		// put human detection timestamp into LastHumanDetection map. This is used to discard
		// bird detections if a human vocalization is detected after the first detection
		p.LastHumanDetection[item.Source] = item.StartTime
	}
}

// getBaseConfidenceThreshold retrieves the confidence threshold for a species, using custom or global thresholds.
func (p *Processor) getBaseConfidenceThreshold(speciesLowercase string) float32 {
	// Check if species has a custom threshold in the new structure
	if config, exists := p.Settings.Realtime.Species.Config[speciesLowercase]; exists {
		if p.Settings.Debug {
			log.Printf("\nUsing custom confidence threshold of %.2f for %s\n", config.Threshold, speciesLowercase)
		}
		return float32(config.Threshold)
	}

	// Fall back to global threshold
	return float32(p.Settings.BirdNET.Threshold)
}

// generateClipName generates a clip name for the given scientific name and confidence.
func (p *Processor) generateClipName(scientificName string, confidence float32) string {
	// Get the base path from the configuration
	basePath := p.Settings.Realtime.Audio.Export.Path

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

	// Get the file extension from the export settings
	fileType := myaudio.GetFileExtension(p.Settings.Realtime.Audio.Export.Type)

	// Construct the clip name with the new pattern, including year and month subdirectories
	// Use filepath.ToSlash to convert the path to a forward slash on Windows to avoid issues with URL encoding
	clipName := filepath.ToSlash(filepath.Join(basePath, year, month, fmt.Sprintf("%s_%s_%s.%s", formattedName, formattedConfidence, timestamp, fileType)))

	return clipName
}

// pendingDetectionsFlusher runs a goroutine that periodically checks the pending detections
// and flushes them to the worker queue if their deadline has passed.
func (p *Processor) pendingDetectionsFlusher() {
	// Determine minDetections based on Settings.BirdNET.Overlap
	var minDetections int

	// Calculate segment length based on overlap setting, minimum 0.1 seconds
	segmentLength := math.Max(0.1, 3.0-p.Settings.BirdNET.Overlap)
	// Calculate minimum detections needed based on segment length, at least 1
	minDetections = int(math.Max(1, 3/segmentLength))

	go func() {
		// Create a ticker that ticks every second to frequently check for flush deadlines.
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop() // Ensure the ticker is stopped to avoid leaking resources.

		for {
			<-ticker.C // Wait for the ticker to tick.
			now := time.Now()

			// Lock the mutex to ensure thread-safe access to the PendingDetections map.
			p.pendingMutex.Lock()

			// Iterate through the pending detections map
			for species, item := range p.pendingDetections {
				// If the current time is past the flush deadline, process the detection.
				if now.After(item.FlushDeadline) {
					// check if count is less than minDetections, if so discard the detection
					if item.Count < minDetections {
						log.Printf("Discarding detection of %s from source %s as false positive, matched %d/%d times\n", species, item.Source, item.Count, minDetections)
						delete(p.pendingDetections, species)
						continue
					}

					// Check if human was detected after the first detection and discard if so
					if p.Settings.Realtime.PrivacyFilter.Enabled {
						lastHumanDetection, exists := p.LastHumanDetection[item.Source]
						if exists && lastHumanDetection.After(item.FirstDetected) {
							log.Printf("Discarding detection of %s from source %s due to privacy filter\n", species, item.Source)
							delete(p.pendingDetections, species)
							continue
						}
					}

					// Check dog bark filter
					if p.Settings.Realtime.DogBarkFilter.Enabled {
						if p.Settings.Realtime.DogBarkFilter.Debug {
							log.Printf("Last dog detection: %s\n", p.LastDogDetection)
						}
						// Check against common name
						if p.CheckDogBarkFilter(item.Detection.Note.CommonName, p.LastDogDetection[item.Source]) {
							log.Printf("Discarding detection of %s from source %s due to recent dog bark\n", item.Detection.Note.CommonName, item.Source)
							delete(p.pendingDetections, species)
							continue
						}
						// Check against scientific name
						if p.CheckDogBarkFilter(item.Detection.Note.ScientificName, p.LastDogDetection[item.Source]) {
							log.Printf("Discarding detection of %s from source %s due to recent dog bark\n", item.Detection.Note.CommonName, item.Source)
							delete(p.pendingDetections, species)
							continue
						}
					}

					log.Printf("Approving detection of %s from source %s, matched %d/%d times\n", species, item.Source, item.Count, minDetections)

					// Set the detection's begin time to the time it was first detected, this is
					// where we start audio export for the detection.
					item.Detection.Note.BeginTime = item.FirstDetected

					// Retrieve and execute actions based on the held detection.
					actionList := p.getActionsForItem(item.Detection)
					for _, action := range actionList {
						workerQueue <- Task{Type: TaskTypeAction, Detection: item.Detection, Action: action}
					}

					// Detection is now processed, remove it from pending detections map.
					delete(p.pendingDetections, species)

					// Update BirdNET metrics detection counter
					if p.Settings.Realtime.Telemetry.Enabled && p.Metrics != nil && p.Metrics.BirdNET != nil {
						p.Metrics.BirdNET.IncrementDetectionCounter(item.Detection.Note.CommonName)
					}
				}
			}
			p.pendingMutex.Unlock()

			// Perform cleanup of stale dynamic thresholds
			p.cleanUpDynamicThresholds()
		}
	}()
}

// Helper function to check if a slice contains a string (case-insensitive)
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}
