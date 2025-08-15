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
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// Species identification constants for filtering
const (
	speciesDog   = "dog"
	speciesHuman = "human"
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
	eventTrackerMu      sync.RWMutex         // Mutex to protect EventTracker access
	NewSpeciesTracker   *NewSpeciesTracker   // Tracks new species detections
	speciesTrackerMu    sync.RWMutex         // Mutex to protect NewSpeciesTracker access
	lastSyncAttempt     time.Time            // Last time sync was attempted
	syncMutex           sync.Mutex           // Mutex to protect sync operations
	syncInProgress      atomic.Bool          // Flag to prevent overlapping syncs
	LastDogDetection    map[string]time.Time // keep track of dog barks per audio source
	LastHumanDetection  map[string]time.Time // keep track of human vocal per audio source
	Metrics             *observability.Metrics
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
	// SSE related fields
	SSEBroadcaster      func(note *datastore.Note, birdImage *imageprovider.BirdImage) error // Function to broadcast detection via SSE
	sseBroadcasterMutex sync.RWMutex                                                         // Mutex to protect SSE broadcaster access

	// Backup system fields (optional)
	backupManager   interface{} // Use interface{} to avoid import cycle
	backupScheduler interface{} // Use interface{} to avoid import cycle
	backupMutex     sync.RWMutex

	// Log deduplication (extracted to separate type for SRP)
	logDedup *LogDeduplicator // Handles log deduplication logic
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

// func New(settings *conf.Settings, ds datastore.Interface, bn *birdnet.BirdNET, audioBuffers map[string]*myaudio.AudioBuffer, metrics *observability.Metrics) *Processor {
func New(settings *conf.Settings, ds datastore.Interface, bn *birdnet.BirdNET, metrics *observability.Metrics, birdImageCache *imageprovider.BirdImageCache) *Processor {
	p := &Processor{
		Settings:       settings,
		Ds:             ds,
		Bn:             bn,
		BirdImageCache: birdImageCache,
		EventTracker: NewEventTrackerWithConfig(
			time.Duration(settings.Realtime.Interval)*time.Second,
			settings.Realtime.Species.Config,
		),
		Metrics:             metrics,
		LastDogDetection:    make(map[string]time.Time),
		LastHumanDetection:  make(map[string]time.Time),
		DynamicThresholds:   make(map[string]*DynamicThreshold),
		pendingDetections:   make(map[string]PendingDetection),
		lastDogDetectionLog: make(map[string]time.Time),
		controlChan:         make(chan string, 10),  // Buffered channel to prevent blocking
		JobQueue:            jobqueue.NewJobQueue(), // Initialize the job queue
	}

	// Initialize log deduplicator with configuration from settings
	// This addresses separation of concerns by extracting deduplication logic
	healthCheckInterval := 60 * time.Second // default
	
	// Validate and use settings if available
	if settings.Realtime.LogDeduplication.HealthCheckIntervalSeconds > 0 {
		// Cap at reasonable maximum (1 hour) to prevent misconfiguration
		if settings.Realtime.LogDeduplication.HealthCheckIntervalSeconds > 3600 {
			healthCheckInterval = time.Hour
			GetLogger().Warn("Log deduplication health check interval capped at 1 hour",
				"requested_seconds", settings.Realtime.LogDeduplication.HealthCheckIntervalSeconds,
				"capped_seconds", 3600,
				"operation", "config_validation")
		} else {
			healthCheckInterval = time.Duration(settings.Realtime.LogDeduplication.HealthCheckIntervalSeconds) * time.Second
		}
	}
	enabled := settings.Realtime.LogDeduplication.Enabled
	
	logConfig := DeduplicationConfig{
		HealthCheckInterval: healthCheckInterval,
		Enabled:             enabled,
	}
	p.logDedup = NewLogDeduplicator(logConfig)

	// Initialize new species tracker if enabled
	if settings.Realtime.SpeciesTracking.Enabled {
		// Validate species tracking configuration
		if err := settings.Realtime.SpeciesTracking.Validate(); err != nil {
			// Add structured logging
			GetLogger().Error("Invalid species tracking configuration",
				"error", err,
				"operation", "species_tracking_validation")
			log.Printf("Invalid species tracking configuration: %v", err)
			// Continue with defaults or disable tracking
			settings.Realtime.SpeciesTracking.Enabled = false
		} else {
			// Adjust seasonal tracking for hemisphere based on BirdNET latitude
			hemisphereAwareTracking := settings.Realtime.SpeciesTracking
			if hemisphereAwareTracking.SeasonalTracking.Enabled {
				hemisphereAwareTracking.SeasonalTracking = conf.GetSeasonalTrackingWithHemisphere(
					hemisphereAwareTracking.SeasonalTracking,
					settings.BirdNET.Latitude,
				)
			}
			
			p.NewSpeciesTracker = NewSpeciesTrackerFromSettings(ds, &hemisphereAwareTracking)

			// Initialize species tracker from database
			if err := p.NewSpeciesTracker.InitFromDatabase(); err != nil {
				// Add structured logging
				GetLogger().Error("Failed to initialize species tracker from database",
					"error", err,
					"operation", "species_tracker_init")
				log.Printf("Failed to initialize species tracker from database: %v", err)
				// Continue anyway - tracker will work for new detections
			}

			hemisphere := conf.DetectHemisphere(settings.BirdNET.Latitude)
			// Add structured logging
			GetLogger().Info("Species tracking enabled",
				"window_days", settings.Realtime.SpeciesTracking.NewSpeciesWindowDays,
				"sync_interval_minutes", settings.Realtime.SpeciesTracking.SyncIntervalMinutes,
				"hemisphere", hemisphere,
				"latitude", settings.BirdNET.Latitude,
				"operation", "species_tracking_config")
			log.Printf("Species tracking enabled: window=%d days, sync=%d minutes, hemisphere=%s (lat=%.2f)",
				settings.Realtime.SpeciesTracking.NewSpeciesWindowDays,
				settings.Realtime.SpeciesTracking.SyncIntervalMinutes,
				hemisphere,
				settings.BirdNET.Latitude)
		}
	}

	// Start the detection processor
	p.startDetectionProcessor()

	// Start the worker pool for action processing
	p.startWorkerPool()

	// Start the held detection flusher
	p.pendingDetectionsFlusher()

	// Initialize BirdWeather client if enabled in settings
	if settings.Realtime.Birdweather.Enabled {
		var err error
		bwClient, err := birdweather.New(settings)
		if err != nil {
			// Add structured logging
			GetLogger().Error("Failed to create BirdWeather client",
				"error", err,
				"operation", "birdweather_client_init",
				"integration", "birdweather")
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
	// Add structured logging for detection processor startup
	GetLogger().Info("Starting detection processor",
		"operation", "detection_processor_startup")
	go func() {
		// ResultsQueue is fed by myaudio.ProcessData()
		for item := range birdnet.ResultsQueue {
			// Pass by value since we own the data (see queue.go ownership comment)
			p.processDetections(item)
		}
		// Add structured logging when processor stops
		GetLogger().Info("Detection processor stopped",
			"operation", "detection_processor_shutdown")
	}()
}

// processDetections examines each detection from the queue, updating held detections
// with new or higher-confidence instances and setting an appropriate flush deadline.
//
//nolint:gocritic // hugeParam: Pass by value is intentional - avoids pointer dereferencing in hot path
func (p *Processor) processDetections(item birdnet.Results) {
	// Add structured logging for detection pipeline entry
	GetLogger().Debug("Processing detections from queue",
		"source", item.Source,
		"start_time", item.StartTime,
		"results_count", len(item.Results),
		"elapsed_time_ms", item.ElapsedTime.Milliseconds(),
		"operation", "process_detections_entry")

	// Delay before a detection is considered final and is flushed.
	// TODO: make this configurable
	const delay = 15 * time.Second

	// processResults() returns a slice of detections, we iterate through each and process them
	// detections are put into pendingDetections map where they are held until flush deadline is reached
	// once deadline is reached detections are delivered to workers for actions (save to db etc) processing
	detectionResults := p.processResults(item)
	
	// Log processing results with deduplication to prevent spam
	p.logDetectionResults(item.Source, len(item.Results), len(detectionResults))

	for i := 0; i < len(detectionResults); i++ {
		detection := detectionResults[i]
		commonName := strings.ToLower(detection.Note.CommonName)
		confidence := detection.Note.Confidence

		// Lock the mutex to ensure thread-safe access to shared resources
		p.pendingMutex.Lock()

		if existing, exists := p.pendingDetections[commonName]; exists {
			// Update the existing detection if it's already in pendingDetections map
			oldConfidence := existing.Confidence
			if confidence > existing.Confidence {
				existing.Detection = detection
				existing.Confidence = confidence
				existing.Source = item.Source
				existing.LastUpdated = time.Now()
				// Add structured logging for confidence update
				GetLogger().Debug("Updated pending detection with higher confidence",
					"species", commonName,
					"old_confidence", oldConfidence,
					"new_confidence", confidence,
					"count", existing.Count+1,
					"operation", "update_pending_detection")
			}
			existing.Count++
			p.pendingDetections[commonName] = existing
		} else {
			// Create a new pending detection if it doesn't exist
			// Add structured logging for new pending detection
			GetLogger().Info("Created new pending detection",
				"species", commonName,
				"confidence", confidence,
				"source", item.Source,
				"flush_deadline", item.StartTime.Add(delay),
				"operation", "create_pending_detection")
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
//
//nolint:gocritic // hugeParam: Pass by value is intentional - avoids pointer dereferencing in hot path
func (p *Processor) processResults(item birdnet.Results) []Detections {
	// Pre-allocate slice with capacity for all results
	detections := make([]Detections, 0, len(item.Results))

	// Collect processing time metric
	if p.Settings.Realtime.Telemetry.Enabled && p.Metrics != nil && p.Metrics.BirdNET != nil {
		p.Metrics.BirdNET.SetProcessTime(float64(item.ElapsedTime.Milliseconds()))
	}

	// Sync species tracker if needed
	p.syncSpeciesTrackerIfNeeded()

	// Process each result in item.Results
	for _, result := range item.Results {
		// Parse and validate species information
		scientificName, commonName, speciesCode, speciesLowercase := p.parseAndValidateSpecies(result, item)
		// Skip if either scientific or common name is missing (partial/invalid parsing)
		if scientificName == "" || commonName == "" {
			if p.Settings.Debug {
				GetLogger().Debug("Skipping partially parsed species",
					"scientific_name", scientificName,
					"common_name", commonName,
					"species_code", speciesCode,
					"species_lowercase", speciesLowercase,
					"original_species", result.Species,
					"confidence", result.Confidence,
					"operation", "validate_species")
			}
			continue // Skip invalid or partially parsed species
		}

		// Handle dog and human detection, this sets LastDogDetection and LastHumanDetection which is
		// later used to discard detection if privacy filter or dog bark filters are enabled in settings.
		p.handleDogDetection(item, speciesLowercase, result)
		p.handleHumanDetection(item, speciesLowercase, result)

		// Determine confidence threshold and check filters
		baseThreshold := p.getBaseConfidenceThreshold(speciesLowercase)
		
		// Check if detection should be filtered
		shouldSkip, _ := p.shouldFilterDetection(result, commonName, speciesLowercase, baseThreshold, item.Source)
		if shouldSkip {
			continue
		}

		// Add species to dynamic thresholds if enabled and passed filters
		if p.Settings.Realtime.DynamicThreshold.Enabled {
			p.addSpeciesToDynamicThresholds(speciesLowercase, baseThreshold)
		}

		// Create the detection
		detection := p.createDetection(item, result, scientificName, commonName, speciesCode)
		detections = append(detections, detection)
	}

	return detections
}

// parseAndValidateSpecies parses species information and validates it
//
//nolint:gocritic // hugeParam: Pass by value is intentional - avoids pointer dereferencing in hot path
func (p *Processor) parseAndValidateSpecies(result datastore.Results, item birdnet.Results) (scientificName, commonName, speciesCode, speciesLowercase string) {
	// Use BirdNET's EnrichResultWithTaxonomy to get species information
	scientificName, commonName, speciesCode = p.Bn.EnrichResultWithTaxonomy(result.Species)

	// Skip processing if we couldn't parse the species properly (either name missing)
	if commonName == "" || scientificName == "" {
		if p.Settings.Debug {
			GetLogger().Debug("Skipping species with invalid format",
				"species", result.Species,
				"confidence", result.Confidence,
				"operation", "species_format_validation")
			log.Printf("Skipping species with invalid format: %s", result.Species)
		}
		return "", "", "", ""
	}

	// Log placeholder taxonomy codes if using custom model
	if p.Settings.BirdNET.ModelPath != "" && p.Settings.Debug && speciesCode != "" {
		if len(speciesCode) == 8 && (speciesCode[:2] == "XX" || (speciesCode[0] >= 'A' && speciesCode[0] <= 'Z' && speciesCode[1] >= 'A' && speciesCode[1] <= 'Z')) {
			GetLogger().Debug("Using placeholder taxonomy code",
				"taxonomy_code", speciesCode,
				"scientific_name", scientificName,
				"common_name", commonName,
				"operation", "taxonomy_code_assignment")
			log.Printf("Using placeholder taxonomy code %s for species %s (%s)", speciesCode, scientificName, commonName)
		}
	}

	// Convert species to lowercase for case-insensitive comparison
	speciesLowercase = strings.ToLower(commonName)
	if speciesLowercase == "" && scientificName != "" {
		speciesLowercase = strings.ToLower(scientificName)
	}

	return
}

// shouldFilterDetection checks if a detection should be filtered out
func (p *Processor) shouldFilterDetection(result datastore.Results, commonName, speciesLowercase string, baseThreshold float32, source string) (shouldFilter bool, confidenceThreshold float32) {
	// Check human detection privacy filter
	if strings.Contains(strings.ToLower(commonName), speciesHuman) && result.Confidence > baseThreshold {
		return true, 0 // Filter out human detections for privacy
	}

	// Determine confidence threshold
	if p.Settings.Realtime.DynamicThreshold.Enabled {
		confidenceThreshold = p.getAdjustedConfidenceThreshold(speciesLowercase, result, baseThreshold)
	} else {
		confidenceThreshold = baseThreshold
	}

	// Check confidence threshold
	if result.Confidence <= confidenceThreshold {
		if p.Settings.Debug {
			GetLogger().Debug("Detection filtered out due to low confidence",
				"species", result.Species,
				"confidence", result.Confidence,
				"threshold", confidenceThreshold,
				"source", source,
				"operation", "confidence_filter")
		}
		return true, confidenceThreshold
	}

	// Check species inclusion filter
	if !p.Settings.IsSpeciesIncluded(result.Species) {
		if p.Settings.Debug {
			GetLogger().Debug("Species not on included list",
				"species", result.Species,
				"confidence", result.Confidence,
				"operation", "species_inclusion_filter")
			log.Printf("Species not on included list: %s\n", result.Species)
		}
		return true, confidenceThreshold
	}

	return false, confidenceThreshold
}

// createDetection creates a detection object with all necessary information
//
//nolint:gocritic // hugeParam: Pass by value is intentional - avoids pointer dereferencing in hot path
func (p *Processor) createDetection(item birdnet.Results, result datastore.Results, scientificName, commonName, speciesCode string) Detections {
	// Create file name for audio clip
	clipName := p.generateClipName(scientificName, result.Confidence)

	// Set begin and end time for note
	beginTime, endTime := item.StartTime, item.StartTime.Add(15*time.Second)

	// Create the note
	note := p.NewWithSpeciesInfo(
		beginTime, endTime,
		scientificName, commonName, speciesCode,
		float64(result.Confidence),
		item.Source, clipName,
		item.ElapsedTime)

	// Update species tracker if enabled
	p.speciesTrackerMu.RLock()
	tracker := p.NewSpeciesTracker
	p.speciesTrackerMu.RUnlock()

	if tracker != nil {
		tracker.UpdateSpecies(scientificName, item.StartTime)
	}

	return Detections{
		pcmData3s: item.PCMdata,
		Note:      note,
		Results:   item.Results,
	}
}

// syncSpeciesTrackerIfNeeded syncs the species tracker if conditions are met
func (p *Processor) syncSpeciesTrackerIfNeeded() {
	p.speciesTrackerMu.RLock()
	tracker := p.NewSpeciesTracker
	p.speciesTrackerMu.RUnlock()

	if tracker != nil {
		// Rate limit sync operations to avoid excessive goroutines
		p.syncMutex.Lock()
		if time.Since(p.lastSyncAttempt) >= time.Minute {
			// Check if sync is already in progress
			if !p.syncInProgress.Load() {
				p.lastSyncAttempt = time.Now()
				p.syncInProgress.Store(true) // Mark sync as in progress
				go func() {
					defer p.syncInProgress.Store(false) // Always clear the flag when done
					if err := tracker.SyncIfNeeded(); err != nil {
						GetLogger().Error("Failed to sync species tracker",
							"error", err,
							"operation", "species_tracker_sync")
						log.Printf("Failed to sync species tracker: %v", err)
					}
				}()
			}
		}
		p.syncMutex.Unlock()
	}
}

// handleDogDetection handles the detection of dog barks and updates the last detection timestamp.
//
//nolint:gocritic // hugeParam: Pass by value is intentional - avoids pointer dereferencing in hot path
func (p *Processor) handleDogDetection(item birdnet.Results, speciesLowercase string, result datastore.Results) {
	if p.Settings.Realtime.DogBarkFilter.Enabled && strings.Contains(speciesLowercase, speciesDog) &&
		result.Confidence > p.Settings.Realtime.DogBarkFilter.Confidence {
		// Add structured logging
		GetLogger().Info("Dog detection filtered",
			"confidence", result.Confidence,
			"threshold", p.Settings.Realtime.DogBarkFilter.Confidence,
			"source", item.Source,
			"operation", "dog_bark_filter")
		log.Printf("Dog detected with confidence %.3f/%.3f from source %s", result.Confidence, p.Settings.Realtime.DogBarkFilter.Confidence, p.getDisplayNameForSource(item.Source))
		p.detectionMutex.Lock()
		p.LastDogDetection[item.Source] = item.StartTime
		p.detectionMutex.Unlock()
	}
}

// handleHumanDetection handles the detection of human vocalizations and updates the last detection timestamp.
//
//nolint:gocritic // hugeParam: Pass by value is intentional - avoids pointer dereferencing in hot path
func (p *Processor) handleHumanDetection(item birdnet.Results, speciesLowercase string, result datastore.Results) {
	// only check this if privacy filter is enabled
	if p.Settings.Realtime.PrivacyFilter.Enabled && strings.Contains(speciesLowercase, "human ") &&
		result.Confidence > p.Settings.Realtime.PrivacyFilter.Confidence {
		// Add structured logging
		GetLogger().Info("Human detection filtered",
			"confidence", result.Confidence,
			"threshold", p.Settings.Realtime.PrivacyFilter.Confidence,
			"source", item.Source,
			"operation", "privacy_filter")
		log.Printf("Human detected with confidence %.3f/%.3f from source %s", result.Confidence, p.Settings.Realtime.PrivacyFilter.Confidence, p.getDisplayNameForSource(item.Source))
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
			// Add structured logging
			GetLogger().Debug("Using custom confidence threshold",
				"species", speciesLowercase,
				"threshold", config.Threshold,
				"operation", "custom_threshold_lookup")
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
		// Add structured logging for minimum count filtering
		GetLogger().Debug("Detection discarded due to insufficient count",
			"species", item.Detection.Note.CommonName,
			"count", item.Count,
			"minimum_required", minDetections,
			"source", item.Source,
			"operation", "minimum_count_filter")
		return true, fmt.Sprintf("false positive, matched %d/%d times", item.Count, minDetections)
	}

	// Check privacy filter
	if p.Settings.Realtime.PrivacyFilter.Enabled {
		p.detectionMutex.RLock()
		lastHumanDetection, exists := p.LastHumanDetection[item.Source]
		p.detectionMutex.RUnlock()
		if exists && lastHumanDetection.After(item.FirstDetected) {
			// Add structured logging for privacy filter
			GetLogger().Debug("Detection discarded by privacy filter",
				"species", item.Detection.Note.CommonName,
				"detection_time", item.FirstDetected,
				"last_human_detection", lastHumanDetection,
				"source", item.Source,
				"operation", "privacy_filter")
			return true, "privacy filter"
		}
	}

	// Check dog bark filter
	if p.Settings.Realtime.DogBarkFilter.Enabled {
		if p.Settings.Realtime.DogBarkFilter.Debug {
			p.detectionMutex.RLock()
			// Add structured logging
			GetLogger().Debug("Last dog detection status",
				"last_detections", p.LastDogDetection,
				"operation", "dog_detection_debug")
			log.Printf("Last dog detection: %s\n", p.LastDogDetection)
			p.detectionMutex.RUnlock()
		}
		p.detectionMutex.RLock()
		lastDogDetection := p.LastDogDetection[item.Source]
		p.detectionMutex.RUnlock()
		if p.CheckDogBarkFilter(item.Detection.Note.CommonName, lastDogDetection) ||
			p.CheckDogBarkFilter(item.Detection.Note.ScientificName, lastDogDetection) {
			// Add structured logging for dog bark filter
			GetLogger().Debug("Detection discarded by dog bark filter",
				"species", item.Detection.Note.CommonName,
				"detection_time", item.FirstDetected,
				"last_dog_detection", lastDogDetection,
				"source", item.Source,
				"operation", "dog_bark_filter")
			return true, "recent dog bark"
		}
	}

	return false, ""
}

// processApprovedDetection handles an approved detection by sending it to the worker queue
func (p *Processor) processApprovedDetection(item *PendingDetection, species string) {
	// Safely get confidence value
	var confidence float64
	if len(item.Detection.Results) > 0 {
		confidence = float64(item.Detection.Results[0].Confidence)
	}
	
	// Add structured logging
	GetLogger().Info("Approving detection",
		"species", species,
		"source", item.Source,
		"match_count", item.Count,
		"confidence", confidence,
		"has_results", len(item.Detection.Results) > 0,
		"operation", "approve_detection")
	log.Printf("Approving detection of %s from source %s, matched %d times\n",
		species, p.getDisplayNameForSource(item.Source), item.Count)

	item.Detection.Note.BeginTime = item.FirstDetected
	actionList := p.getActionsForItem(&item.Detection)
	for _, action := range actionList {
		task := &Task{Type: TaskTypeAction, Detection: item.Detection, Action: action}
		if err := p.EnqueueTask(task); err != nil {
			// Check error message instead of using errors.Is to avoid import cycle
			if err.Error() == "worker queue is full" {
				// Add structured logging
				GetLogger().Warn("Worker queue is full, dropping task",
					"species", species,
					"operation", "enqueue_task",
					"error", "queue_full")
				log.Printf("❌ Worker queue is full, dropping task for %s", species)
			} else {
				sanitizedErr := sanitizeError(err)
				// Add structured logging
				GetLogger().Error("Failed to enqueue task",
					"error", sanitizedErr,
					"species", species,
					"operation", "enqueue_task")
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

	// Add structured logging for pending detections flusher startup
	GetLogger().Info("Starting pending detections flusher",
		"min_detections", minDetections,
		"flush_interval_seconds", 1,
		"operation", "pending_flusher_startup")

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			<-ticker.C
			now := time.Now()

			p.pendingMutex.Lock()
			pendingCount := len(p.pendingDetections)
			flushableCount := 0
			for species := range p.pendingDetections {
				item := p.pendingDetections[species]
				if now.After(item.FlushDeadline) {
					flushableCount++
					if shouldDiscard, reason := p.shouldDiscardDetection(&item, minDetections); shouldDiscard {
						// Add structured logging
					GetLogger().Info("Discarding detection",
						"species", species,
						"source", item.Source,
						"reason", reason,
						"count", item.Count,
						"operation", "discard_detection")
					log.Printf("Discarding detection of %s from source %s due to %s\n",
							species, p.getDisplayNameForSource(item.Source), reason)
						delete(p.pendingDetections, species)
						continue
					}

					p.processApprovedDetection(&item, species)
					delete(p.pendingDetections, species)
				}
			}
			// Add structured logging for flusher activity (only when there's activity)
			if pendingCount > 0 || flushableCount > 0 {
				GetLogger().Debug("Pending detections flusher cycle",
					"pending_count", pendingCount,
					"flushable_count", flushableCount,
					"operation", "pending_flusher_cycle")
			}
			p.pendingMutex.Unlock()

			p.cleanUpDynamicThresholds()
		}
	}()
}


// getActionsForItem determines the actions to be taken for a given detection.
func (p *Processor) getActionsForItem(detection *Detections) []Action {
	speciesName := strings.ToLower(detection.Note.CommonName)

	// Check if species has custom configuration
	if speciesConfig, exists := p.Settings.Realtime.Species.Config[speciesName]; exists {
		if p.Settings.Debug {
			// Add structured logging
			GetLogger().Debug("Species config exists for custom actions",
				"species", speciesName,
				"operation", "custom_action_check")
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
	defaultActions := p.getDefaultActions(detection)
	// Add structured logging for default actions
	GetLogger().Debug("Using default actions for detection",
		"species", strings.ToLower(detection.Note.CommonName),
		"actions_count", len(defaultActions),
		"operation", "get_default_actions")
	return defaultActions
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
		actions = append(actions, &LogAction{Settings: p.Settings, EventTracker: p.GetEventTracker(), Note: detection.Note})
	}

	if p.Settings.Output.SQLite.Enabled || p.Settings.Output.MySQL.Enabled {
		p.speciesTrackerMu.RLock()
		tracker := p.NewSpeciesTracker
		p.speciesTrackerMu.RUnlock()

		actions = append(actions, &DatabaseAction{
			Settings:          p.Settings,
			EventTracker:      p.GetEventTracker(),
			NewSpeciesTracker: tracker,
			processor:         p, // Add processor reference for source name resolution
			Note:              detection.Note,
			Results:           detection.Results,
			Ds:                p.Ds})
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
				EventTracker: p.GetEventTracker(),
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
				EventTracker:   p.GetEventTracker(),
				Note:           detection.Note,
				BirdImageCache: p.BirdImageCache,
				RetryConfig:    mqttRetryConfig,
			})
		}
	}

	// Add SSE action if broadcaster is available (enabled when SSE API is configured)
	if sseBroadcaster := p.GetSSEBroadcaster(); sseBroadcaster != nil {
		// Create SSE retry config - use sensible defaults since SSE should be reliable
		sseRetryConfig := jobqueue.RetryConfig{
			Enabled:      true, // Enable retries for SSE to improve reliability
			MaxRetries:   3,    // Conservative retry count for real-time streaming
			InitialDelay: 1 * time.Second,
			MaxDelay:     5 * time.Second,
			Multiplier:   2.0,
		}

		actions = append(actions, &SSEAction{
			Settings:       p.Settings,
			Note:           detection.Note,
			BirdImageCache: p.BirdImageCache,
			EventTracker:   p.GetEventTracker(),
			RetryConfig:    sseRetryConfig,
			SSEBroadcaster: sseBroadcaster,
			Ds:             p.Ds,
		})
	}

	// Check if UpdateRangeFilterAction needs to be executed for the day
	today := time.Now().Truncate(24 * time.Hour) // Current date with time set to midnight
	if p.Settings.BirdNET.RangeFilter.LastUpdated.Before(today) {
		// Add structured logging
		GetLogger().Info("Updating species range filter",
			"last_updated", p.Settings.BirdNET.RangeFilter.LastUpdated,
			"today", today,
			"operation", "update_range_filter")
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

// SetEventTracker safely replaces the current EventTracker
func (p *Processor) SetEventTracker(tracker *EventTracker) {
	p.eventTrackerMu.Lock()
	defer p.eventTrackerMu.Unlock()
	p.EventTracker = tracker
}

// GetEventTracker safely returns the current EventTracker
func (p *Processor) GetEventTracker() *EventTracker {
	p.eventTrackerMu.RLock()
	defer p.eventTrackerMu.RUnlock()
	return p.EventTracker
}

// GetJobQueueStats returns statistics about the job queue
// This method is thread-safe as it delegates to JobQueue.GetStats() which handles locking internally
func (p *Processor) GetJobQueueStats() jobqueue.JobStatsSnapshot {
	return p.JobQueue.GetStats()
}

// GetBn returns the BirdNET instance
// Deprecated: Use GetBirdNET instead
func (p *Processor) GetBn() *birdnet.BirdNET {
	return p.Bn
}

// GetBirdNET returns the BirdNET instance
func (p *Processor) GetBirdNET() *birdnet.BirdNET {
	return p.Bn
}

// SetSSEBroadcaster safely sets the SSE broadcaster function
func (p *Processor) SetSSEBroadcaster(broadcaster func(note *datastore.Note, birdImage *imageprovider.BirdImage) error) {
	p.sseBroadcasterMutex.Lock()
	defer p.sseBroadcasterMutex.Unlock()
	p.SSEBroadcaster = broadcaster
}

// GetSSEBroadcaster safely returns the current SSE broadcaster function
func (p *Processor) GetSSEBroadcaster() func(note *datastore.Note, birdImage *imageprovider.BirdImage) error {
	p.sseBroadcasterMutex.RLock()
	defer p.sseBroadcasterMutex.RUnlock()
	return p.SSEBroadcaster
}

// SetBackupManager safely sets the backup manager
func (p *Processor) SetBackupManager(manager interface{}) {
	p.backupMutex.Lock()
	defer p.backupMutex.Unlock()
	p.backupManager = manager
}

// GetBackupManager safely returns the backup manager
func (p *Processor) GetBackupManager() interface{} {
	p.backupMutex.RLock()
	defer p.backupMutex.RUnlock()
	return p.backupManager
}

// SetBackupScheduler safely sets the backup scheduler
func (p *Processor) SetBackupScheduler(scheduler interface{}) {
	p.backupMutex.Lock()
	defer p.backupMutex.Unlock()
	p.backupScheduler = scheduler
}

// GetBackupScheduler safely returns the backup scheduler
func (p *Processor) GetBackupScheduler() interface{} {
	p.backupMutex.RLock()
	defer p.backupMutex.RUnlock()
	return p.backupScheduler
}

// CleanupLogDeduplicator removes stale log deduplication entries to prevent memory growth.
// Returns the number of entries removed.
func (p *Processor) CleanupLogDeduplicator(staleAfter time.Duration) int {
	if p.logDedup == nil {
		return 0
	}
	removed := p.logDedup.Cleanup(staleAfter)
	if removed > 0 {
		GetLogger().Debug("Cleaned stale log deduplication entries",
			"removed_count", removed,
			"stale_after", staleAfter,
			"operation", "log_dedup_cleanup")
	}
	return removed
}

// getDisplayNameForSource converts a source ID to user-friendly DisplayName
// Falls back to source ID if lookup fails (for debug/technical contexts)
func (p *Processor) getDisplayNameForSource(sourceID string) string {
	registry := myaudio.GetRegistry()
	if source, exists := registry.GetSourceByID(sourceID); exists {
		return source.DisplayName
	}
	// Fallback to source ID if not found in registry
	return sourceID
}

// Shutdown gracefully stops all processor components
func (p *Processor) Shutdown() error {
	// Cancel all worker goroutines
	if p.workerCancel != nil {
		p.workerCancel()
	}

	// Stop the job queue with a timeout
	if err := p.JobQueue.StopWithTimeout(30 * time.Second); err != nil {
		// Add structured logging
		GetLogger().Warn("Job queue shutdown timed out",
			"error", err,
			"timeout_seconds", 30,
			"operation", "job_queue_shutdown")
		log.Printf("Warning: job queue shutdown timed out: %v", err)
	}

	// Disconnect BirdWeather client
	p.DisconnectBwClient()

	// Disconnect MQTT client if connected
	mqttClient := p.GetMQTTClient()
	if mqttClient != nil && mqttClient.IsConnected() {
		mqttClient.Disconnect()
	}

	// Close the species tracker to release resources
	p.speciesTrackerMu.RLock()
	tracker := p.NewSpeciesTracker
	p.speciesTrackerMu.RUnlock()
	
	if tracker != nil {
		if err := tracker.Close(); err != nil {
			// Add structured logging
			GetLogger().Warn("Failed to close species tracker",
				"error", err,
				"operation", "species_tracker_cleanup")
			log.Printf("Warning: failed to close species tracker: %v", err)
		}
	}

	// Add structured logging
	GetLogger().Info("Processor shutdown complete",
		"operation", "processor_shutdown")
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

// logDetectionResults logs detection processing results using the LogDeduplicator
// to prevent repetitive logging while maintaining observability.
func (p *Processor) logDetectionResults(source string, rawCount, filteredCount int) {
	// Use the LogDeduplicator to determine if we should log
	shouldLog, reason := p.logDedup.ShouldLog(source, rawCount, filteredCount)
	
	if shouldLog {
		GetLogger().Info("Detection processing results",
			"source", source,
			"raw_results_count", rawCount,
			"filtered_detections_count", filteredCount,
			"log_reason", reason,
			"operation", "process_detections_summary")
	}
}
