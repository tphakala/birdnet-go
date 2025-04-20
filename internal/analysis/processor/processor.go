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

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// Processor represents the main processing unit for audio analysis.
type Processor struct {
	Settings            *conf.Settings
	Ds                  datastore.Interface
	Bn                  *birdnet.BirdNET
	BwClient            *birdweather.BwClient
	bwClientMutex       sync.RWMutex // Mutex to protect BwClient access
	MqttClient          mqtt.Client
	mqttMutex           sync.RWMutex // Mutex to protect MQTT client access
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
	detectionMutex      sync.RWMutex // Mutex to protect LastDogDetection and LastHumanDetection maps
	controlChan         chan string
	JobQueue            *jobqueue.JobQueue // Queue for managing job retries
	workerCancel        context.CancelFunc // Function to cancel worker goroutines
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
		controlChan:         make(chan string, 10),  // Buffered channel to prevent blocking
		JobQueue:            jobqueue.NewJobQueue(), // Initialize the job queue
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
		bwClient, err := birdweather.New(settings)
		if err != nil {
			log.Printf("failed to create Birdweather client: %s", err)
		} else {
			p.SetBwClient(bwClient) // Use setter for thread safety
		}
	}

	// Initialize MQTT client if enabled in settings
	p.initializeMQTT(settings)

	// Start the job queue
	p.JobQueue.Start()

	return p
}

// Start goroutine to process detections from the queue
func (p *Processor) startDetectionProcessor() {
	go func() {
		// ResultsQueue is fed by myaudio.ProcessData()
		for item := range birdnet.ResultsQueue {
			itemCopy := item
			p.processDetections(&itemCopy)
		}
	}()
}

// processDetections examines each detection from the queue, updating held detections
// with new or higher-confidence instances and setting an appropriate flush deadline.
func (p *Processor) processDetections(item *birdnet.Results) {
	// Delay before a detection is considered final and is flushed.
	// TODO: make this configurable
	const delay = 15 * time.Second

	// processResults() returns a slice of detections, we iterate through each and process them
	// detections are put into pendingDetections map where they are held until flush deadline is reached
	// once deadline is reached detections are delivered to workers for actions (save to db etc) processing
	detectionResults := p.processResults(item)
	for i := 0; i < len(detectionResults); i++ {
		detection := detectionResults[i]
		commonName := strings.ToLower(detection.Note.CommonName)
		confidence := detection.Note.Confidence

		// Lock the mutex to ensure thread-safe access to shared resources
		p.pendingMutex.Lock()

		if existing, exists := p.pendingDetections[commonName]; exists {
			// Update the existing detection if it's already in pendingDetections map
			if confidence > existing.Confidence {
				existing.Detection = detection
				existing.Confidence = confidence
				existing.Source = item.Source
				existing.LastUpdated = time.Now()
			}
			existing.Count++
			p.pendingDetections[commonName] = existing
		} else {
			// Create a new pending detection if it doesn't exist
			p.pendingDetections[commonName] = PendingDetection{
				Detection:     detection,
				Confidence:    confidence,
				Source:        item.Source,
				FirstDetected: item.StartTime,
				FlushDeadline: item.StartTime.Add(delay),
				Count:         1,
			}
		}

		// Update the dynamic threshold for this species if enabled
		p.updateDynamicThreshold(commonName, confidence)

		// Unlock the mutex to allow other goroutines to access shared resources
		p.pendingMutex.Unlock()
	}
}

// processResults processes the results from the BirdNET prediction and returns a list of detections.
func (p *Processor) processResults(item *birdnet.Results) []Detections {
	var detections []Detections

	// Collect processing time metric
	if p.Settings.Realtime.Telemetry.Enabled && p.Metrics != nil && p.Metrics.BirdNET != nil {
		p.Metrics.BirdNET.SetProcessTime(float64(item.ElapsedTime.Milliseconds()))
	}

	// Process each result in item.Results
	for _, result := range item.Results {
		var confidenceThreshold float32

		// Use BirdNET's EnrichResultWithTaxonomy instead of ParseSpeciesString
		// to ensure we get the correct species code from the taxonomy map
		scientificName, commonName, speciesCode := p.Bn.EnrichResultWithTaxonomy(result.Species)

		// Skip processing if we couldn't parse the species properly
		if commonName == "" && scientificName == "" {
			if p.Settings.Debug {
				log.Printf("Skipping species with invalid format: %s", result.Species)
			}
			continue
		}

		// If using a custom model and the species doesn't have a taxonomy code,
		// a placeholder code may have been generated. Log this if in debug mode.
		if p.Settings.BirdNET.ModelPath != "" && p.Settings.Debug && speciesCode != "" {
			// Check if the code looks like a placeholder (has the pattern XX or similar followed by 6 hex chars)
			if len(speciesCode) == 8 && (speciesCode[:2] == "XX" || (speciesCode[0] >= 'A' && speciesCode[0] <= 'Z' && speciesCode[1] >= 'A' && speciesCode[1] <= 'Z')) {
				log.Printf("Using placeholder taxonomy code %s for species %s (%s)", speciesCode, scientificName, commonName)
			}
		}

		// Convert species to lowercase for case-insensitive comparison
		speciesLowercase := strings.ToLower(commonName)

		// Fall back to using scientific name if common name is empty
		if speciesLowercase == "" && scientificName != "" {
			speciesLowercase = strings.ToLower(scientificName)
		}

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

		// Use the new function to preserve the species code from the taxonomy lookup
		note := p.NewWithSpeciesInfo(
			beginTime, endTime,
			scientificName, commonName, speciesCode,
			float64(result.Confidence),
			item.Source, clipName,
			item.ElapsedTime)

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
func (p *Processor) handleDogDetection(item *birdnet.Results, speciesLowercase string, result datastore.Results) {
	if p.Settings.Realtime.DogBarkFilter.Enabled && strings.Contains(speciesLowercase, "dog") &&
		result.Confidence > p.Settings.Realtime.DogBarkFilter.Confidence {
		log.Printf("Dog detected with confidence %.3f/%.3f from source %s", result.Confidence, p.Settings.Realtime.DogBarkFilter.Confidence, item.Source)
		p.detectionMutex.Lock()
		p.LastDogDetection[item.Source] = item.StartTime
		p.detectionMutex.Unlock()
	}
}

// handleHumanDetection handles the detection of human vocalizations and updates the last detection timestamp.
func (p *Processor) handleHumanDetection(item *birdnet.Results, speciesLowercase string, result datastore.Results) {
	// only check this if privacy filter is enabled
	if p.Settings.Realtime.PrivacyFilter.Enabled && strings.Contains(speciesLowercase, "human ") &&
		result.Confidence > p.Settings.Realtime.PrivacyFilter.Confidence {
		log.Printf("Human detected with confidence %.3f/%.3f from source %s", result.Confidence, p.Settings.Realtime.PrivacyFilter.Confidence, item.Source)
		// put human detection timestamp into LastHumanDetection map. This is used to discard
		// bird detections if a human vocalization is detected after the first detection
		p.detectionMutex.Lock()
		p.LastHumanDetection[item.Source] = item.StartTime
		p.detectionMutex.Unlock()
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
	// Use filepath.ToSlash to convert the path to a forward slash for web URLs
	clipName := filepath.ToSlash(filepath.Join(year, month, fmt.Sprintf("%s_%s_%s.%s", formattedName, formattedConfidence, timestamp, fileType)))

	return clipName
}

// shouldDiscardDetection checks if a detection should be discarded based on various criteria
func (p *Processor) shouldDiscardDetection(item *PendingDetection, minDetections int) (shouldDiscard bool, reason string) {
	// Check minimum detection count
	if item.Count < minDetections {
		return true, fmt.Sprintf("false positive, matched %d/%d times", item.Count, minDetections)
	}

	// Check privacy filter
	if p.Settings.Realtime.PrivacyFilter.Enabled {
		p.detectionMutex.RLock()
		lastHumanDetection, exists := p.LastHumanDetection[item.Source]
		p.detectionMutex.RUnlock()
		if exists && lastHumanDetection.After(item.FirstDetected) {
			return true, "privacy filter"
		}
	}

	// Check dog bark filter
	if p.Settings.Realtime.DogBarkFilter.Enabled {
		if p.Settings.Realtime.DogBarkFilter.Debug {
			p.detectionMutex.RLock()
			log.Printf("Last dog detection: %s\n", p.LastDogDetection)
			p.detectionMutex.RUnlock()
		}
		p.detectionMutex.RLock()
		lastDogDetection := p.LastDogDetection[item.Source]
		p.detectionMutex.RUnlock()
		if p.CheckDogBarkFilter(item.Detection.Note.CommonName, lastDogDetection) ||
			p.CheckDogBarkFilter(item.Detection.Note.ScientificName, lastDogDetection) {
			return true, "recent dog bark"
		}
	}

	return false, ""
}

// processApprovedDetection handles an approved detection by sending it to the worker queue
func (p *Processor) processApprovedDetection(item *PendingDetection, species string) {
	log.Printf("Approving detection of %s from source %s, matched %d times\n",
		species, item.Source, item.Count)

	item.Detection.Note.BeginTime = item.FirstDetected
	actionList := p.getActionsForItem(&item.Detection)
	for _, action := range actionList {
		task := &Task{Type: TaskTypeAction, Detection: item.Detection, Action: action}
		if err := p.EnqueueTask(task); err != nil {
			// Check error message instead of using errors.Is to avoid import cycle
			if err.Error() == "worker queue is full" {
				log.Printf("❌ Worker queue is full, dropping task for %s", species)
			} else {
				sanitizedErr := sanitizeError(err)
				log.Printf("Failed to enqueue task for %s: %v", species, sanitizedErr)
			}
			continue
		}
	}

	// Update BirdNET metrics detection counter if enabled
	if p.Settings.Realtime.Telemetry.Enabled && p.Metrics != nil && p.Metrics.BirdNET != nil {
		p.Metrics.BirdNET.IncrementDetectionCounter(item.Detection.Note.CommonName)
	}
}

// pendingDetectionsFlusher runs a goroutine that periodically checks the pending detections
// and flushes them to the worker queue if their deadline has passed.
func (p *Processor) pendingDetectionsFlusher() {
	// Calculate minimum detections based on overlap setting
	segmentLength := math.Max(0.1, 3.0-p.Settings.BirdNET.Overlap)
	minDetections := int(math.Max(1, 3/segmentLength))

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			<-ticker.C
			now := time.Now()

			p.pendingMutex.Lock()
			for species := range p.pendingDetections {
				item := p.pendingDetections[species]
				if now.After(item.FlushDeadline) {
					if shouldDiscard, reason := p.shouldDiscardDetection(&item, minDetections); shouldDiscard {
						log.Printf("Discarding detection of %s from source %s due to %s\n",
							species, item.Source, reason)
						delete(p.pendingDetections, species)
						continue
					}

					p.processApprovedDetection(&item, species)
					delete(p.pendingDetections, species)
				}
			}
			p.pendingMutex.Unlock()

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

// getActionsForItem determines the actions to be taken for a given detection.
func (p *Processor) getActionsForItem(detection *Detections) []Action {
	speciesName := strings.ToLower(detection.Note.CommonName)

	// Check if species has custom configuration
	if speciesConfig, exists := p.Settings.Realtime.Species.Config[speciesName]; exists {
		if p.Settings.Debug {
			log.Println("Species config exists for custom actions")
		}

		var actions []Action
		var executeDefaults bool

		// Add custom actions from the new structure
		for _, actionConfig := range speciesConfig.Actions {
			switch actionConfig.Type {
			case "ExecuteCommand":
				if len(actionConfig.Parameters) > 0 {
					actions = append(actions, &ExecuteCommandAction{
						Command: actionConfig.Command,
						Params:  parseCommandParams(actionConfig.Parameters, detection),
					})
				}
			case "SendNotification":
				// Add notification action handling
				// ... implementation ...
			}
			// If any action has ExecuteDefaults set to true, we'll include default actions
			if actionConfig.ExecuteDefaults {
				executeDefaults = true
			}
		}

		// If there are custom actions, return only those unless executeDefaults is true
		if len(actions) > 0 && !executeDefaults {
			return actions
		}

		// If executeDefaults is true, combine custom and default actions
		if len(actions) > 0 && executeDefaults {
			defaultActions := p.getDefaultActions(detection)
			return append(actions, defaultActions...)
		}
	}

	// Fall back to default actions if no custom actions or if custom actions should be combined
	return p.getDefaultActions(detection)
}

// Helper function to parse command parameters
func parseCommandParams(params []string, detection *Detections) map[string]interface{} {
	commandParams := make(map[string]interface{})
	for _, param := range params {
		value := getNoteValueByName(&detection.Note, param)
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
func (p *Processor) getDefaultActions(detection *Detections) []Action {
	var actions []Action

	// Append various default actions based on the application settings
	if p.Settings.Realtime.Log.Enabled {
		actions = append(actions, &LogAction{Settings: p.Settings, EventTracker: p.EventTracker, Note: detection.Note})
	}

	if p.Settings.Output.SQLite.Enabled || p.Settings.Output.MySQL.Enabled {
		actions = append(actions, &DatabaseAction{
			Settings:     p.Settings,
			EventTracker: p.EventTracker,
			Note:         detection.Note,
			Results:      detection.Results,
			Ds:           p.Ds})
	}

	// Add BirdWeatherAction if enabled and client is initialized
	if p.Settings.Realtime.Birdweather.Enabled {
		bwClient := p.GetBwClient() // Use getter for thread safety
		if bwClient != nil {
			// Create BirdWeather retry config from settings
			bwRetryConfig := jobqueue.RetryConfig{
				Enabled:      p.Settings.Realtime.Birdweather.RetrySettings.Enabled,
				MaxRetries:   p.Settings.Realtime.Birdweather.RetrySettings.MaxRetries,
				InitialDelay: time.Duration(p.Settings.Realtime.Birdweather.RetrySettings.InitialDelay) * time.Second,
				MaxDelay:     time.Duration(p.Settings.Realtime.Birdweather.RetrySettings.MaxDelay) * time.Second,
				Multiplier:   p.Settings.Realtime.Birdweather.RetrySettings.BackoffMultiplier,
			}

			actions = append(actions, &BirdWeatherAction{
				Settings:     p.Settings,
				EventTracker: p.EventTracker,
				BwClient:     bwClient,
				Note:         detection.Note,
				pcmData:      detection.pcmData3s,
				RetryConfig:  bwRetryConfig,
			})
		}
	}

	// Add MQTT action if enabled and client is available
	if p.Settings.Realtime.MQTT.Enabled {
		mqttClient := p.GetMQTTClient()
		if mqttClient != nil && mqttClient.IsConnected() {
			// Create MQTT retry config from settings
			mqttRetryConfig := jobqueue.RetryConfig{
				Enabled:      p.Settings.Realtime.MQTT.RetrySettings.Enabled,
				MaxRetries:   p.Settings.Realtime.MQTT.RetrySettings.MaxRetries,
				InitialDelay: time.Duration(p.Settings.Realtime.MQTT.RetrySettings.InitialDelay) * time.Second,
				MaxDelay:     time.Duration(p.Settings.Realtime.MQTT.RetrySettings.MaxDelay) * time.Second,
				Multiplier:   p.Settings.Realtime.MQTT.RetrySettings.BackoffMultiplier,
			}

			actions = append(actions, &MqttAction{
				Settings:       p.Settings,
				MqttClient:     mqttClient,
				EventTracker:   p.EventTracker,
				Note:           detection.Note,
				BirdImageCache: p.BirdImageCache,
				RetryConfig:    mqttRetryConfig,
			})
		}
	}

	// Check if UpdateRangeFilterAction needs to be executed for the day
	today := time.Now().Truncate(24 * time.Hour) // Current date with time set to midnight
	if p.Settings.BirdNET.RangeFilter.LastUpdated.Before(today) {
		fmt.Println("Updating species range filter")
		// Add UpdateRangeFilterAction if it hasn't been executed today
		actions = append(actions, &UpdateRangeFilterAction{
			Bn:       p.Bn,
			Settings: p.Settings,
		})
	}

	return actions
}

// GetBwClient safely returns the current BirdWeather client
func (p *Processor) GetBwClient() *birdweather.BwClient {
	p.bwClientMutex.RLock()
	defer p.bwClientMutex.RUnlock()
	return p.BwClient
}

// SetBwClient safely sets a new BirdWeather client
func (p *Processor) SetBwClient(client *birdweather.BwClient) {
	p.bwClientMutex.Lock()
	defer p.bwClientMutex.Unlock()
	p.BwClient = client
}

// DisconnectBwClient safely disconnects and removes the BirdWeather client
func (p *Processor) DisconnectBwClient() {
	p.bwClientMutex.Lock()
	defer p.bwClientMutex.Unlock()
	// Call the Close method if the client exists
	if p.BwClient != nil {
		p.BwClient.Close()
		p.BwClient = nil
	}
}

// GetJobQueueStats returns statistics about the job queue
// This method is thread-safe as it delegates to JobQueue.GetStats() which handles locking internally
func (p *Processor) GetJobQueueStats() jobqueue.JobStatsSnapshot {
	return p.JobQueue.GetStats()
}

// Shutdown gracefully stops all processor components
func (p *Processor) Shutdown() error {
	// Cancel all worker goroutines
	if p.workerCancel != nil {
		p.workerCancel()
	}

	// Stop the job queue with a timeout
	if err := p.JobQueue.StopWithTimeout(30 * time.Second); err != nil {
		log.Printf("Warning: job queue shutdown timed out: %v", err)
	}

	// Disconnect BirdWeather client
	p.DisconnectBwClient()

	// Disconnect MQTT client if connected
	mqttClient := p.GetMQTTClient()
	if mqttClient != nil && mqttClient.IsConnected() {
		mqttClient.Disconnect()
	}

	log.Println("Processor shutdown complete")
	return nil
}

// NewWithSpeciesInfo creates a new observation note with pre-parsed species information
// This ensures that the species code from the taxonomy lookup is preserved
func (p *Processor) NewWithSpeciesInfo(
	beginTime, endTime time.Time,
	scientificName, commonName, speciesCode string,
	confidence float64,
	source, clipName string,
	elapsedTime time.Duration) datastore.Note {

	// detectionTime is time now minus 3 seconds to account for the delay in the detection
	now := time.Now()
	date := now.Format("2006-01-02")
	detectionTime := now.Add(-2 * time.Second)
	timeStr := detectionTime.Format("15:04:05")

	var audioSource string
	if p.Settings.Input.Path != "" {
		audioSource = p.Settings.Input.Path
	} else {
		audioSource = source
	}

	// Round confidence to two decimal places
	roundedConfidence := math.Round(confidence*100) / 100

	// Return a new Note struct populated with the provided parameters and the current date and time
	return datastore.Note{
		SourceNode:     p.Settings.Main.Name,           // From the provided configuration settings
		Date:           date,                           // Use ISO 8601 date format
		Time:           timeStr,                        // Use 24-hour time format
		Source:         audioSource,                    // From the provided configuration settings
		BeginTime:      beginTime,                      // Start time of the observation
		EndTime:        endTime,                        // End time of the observation
		SpeciesCode:    speciesCode,                    // Species code from taxonomy lookup
		ScientificName: scientificName,                 // Scientific name from taxonomy lookup
		CommonName:     commonName,                     // Common name from taxonomy lookup
		Confidence:     roundedConfidence,              // Confidence score of the observation
		Latitude:       p.Settings.BirdNET.Latitude,    // Geographic latitude where the observation was made
		Longitude:      p.Settings.BirdNET.Longitude,   // Geographic longitude where the observation was made
		Threshold:      p.Settings.BirdNET.Threshold,   // Threshold setting from configuration
		Sensitivity:    p.Settings.BirdNET.Sensitivity, // Sensitivity setting from configuration
		ClipName:       clipName,                       // Name of the audio clip
		ProcessingTime: elapsedTime,                    // Time taken to process the observation
	}
}
