// processor.go
package processor

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
)

// Compile-time assertion to ensure *spectrogram.PreRenderer implements PreRendererSubmit
var _ PreRendererSubmit = (*spectrogram.PreRenderer)(nil)

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
	eventTrackerMu      sync.RWMutex            // Mutex to protect EventTracker access
	NewSpeciesTracker   *species.SpeciesTracker // Tracks new species detections
	speciesTrackerMu    sync.RWMutex            // Mutex to protect NewSpeciesTracker access
	lastSyncAttempt     time.Time               // Last time sync was attempted
	syncMutex           sync.Mutex              // Mutex to protect sync operations
	syncInProgress      atomic.Bool             // Flag to prevent overlapping syncs
	LastDogDetection    map[string]time.Time    // keep track of dog barks per audio source
	LastHumanDetection  map[string]time.Time    // keep track of human vocal per audio source
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
	thresholdsCtx       context.Context    // Context for threshold persistence/cleanup goroutines
	thresholdsCancel    context.CancelFunc // Function to cancel threshold persistence/cleanup goroutines
	preRenderer         PreRendererSubmit  // Spectrogram pre-renderer for background generation
	preRendererOnce     sync.Once          // Ensures pre-renderer is initialized only once
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
	CorrelationID string              // Unique detection identifier for log correlation
	pcmData3s     []byte              // 3s PCM data containing the detection
	Note          datastore.Note      // Note containing highest match
	Results       []datastore.Results // Full BirdNET prediction results
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

// suggestLevelForDisabledFilter provides smart recommendations for filter levels
// when filtering is disabled (level 0). It analyzes current overlap settings
// and suggests an appropriate filter level that matches the user's configuration.
func suggestLevelForDisabledFilter(overlap float64) {
	recommendedLevel, _ := getRecommendedLevelForOverlap(overlap)
	if recommendedLevel > 0 {
		GetLogger().Info("False positive filtering is disabled",
			"current_level", 0,
			"current_overlap", overlap,
			"recommended_level", recommendedLevel,
			"recommended_level_name", getLevelName(recommendedLevel),
			"recommendation", fmt.Sprintf("Consider enabling filtering with level %d (%s) which matches your current overlap %.1f",
				recommendedLevel, getLevelName(recommendedLevel), overlap),
			"operation", "false_positive_filter_config")
		log.Printf("False positive filtering: DISABLED (level 0)")
		log.Printf("💡 Suggestion: Your current overlap (%.1f) supports up to Level %d (%s) filtering",
			overlap, recommendedLevel, getLevelName(recommendedLevel))
		log.Printf("   Enable filtering to reduce false positives: set realtime.falsepositivefilter.level = %d", recommendedLevel)

		// Notify users through the web UI
		notification.NotifyInfo(
			"False Positive Filtering Disabled",
			fmt.Sprintf("Your system can support Level %d (%s) filtering with your current overlap of %.1f. Enable it in settings to reduce false detections from wind, cars, and other noise.",
				recommendedLevel, getLevelName(recommendedLevel), overlap),
		)
	} else {
		GetLogger().Info("False positive filtering is disabled",
			"current_level", 0,
			"operation", "false_positive_filter_config")
		log.Printf("False positive filtering: DISABLED (level 0)")
	}
}

// validateOverlapForLevel checks if the current overlap is sufficient for the
// configured filter level and provides warnings/recommendations if not optimal.
func validateOverlapForLevel(level int, overlap, minOverlap float64, minDetections int) {
	if overlap < minOverlap {
		// Overlap is too low for this level
		GetLogger().Warn("Overlap below recommended minimum for filtering level",
			"level", level,
			"level_name", getLevelName(level),
			"min_overlap", minOverlap,
			"current_overlap", overlap,
			"min_detections", minDetections,
			"hardware_req", getHardwareRequirementForLevel(level),
			"operation", "false_positive_filter_config")
		log.Printf("⚠️  False positive filtering: Level %d (%s) - OVERLAP TOO LOW",
			level, getLevelName(level))
		log.Printf("   Current overlap: %.1f, Recommended minimum: %.1f", overlap, minOverlap)
		log.Printf("   Requires %d confirmations in 6 seconds", minDetections)
		log.Printf("   Hardware: %s", getHardwareRequirementForLevel(level))
		recommendedForCurrent, _ := getRecommendedLevelForOverlap(overlap)
		log.Printf("   Consider increasing overlap or using Level %d for your current overlap",
			recommendedForCurrent)

		// Warn users through the web UI
		notification.NotifyWarning(
			"analysis",
			"Filter Level May Not Work Optimally",
			fmt.Sprintf("Level %d (%s) filtering requires overlap %.1f or higher, but current overlap is %.1f. Consider increasing overlap to %.1f or using Level %d (%s) instead.",
				level, getLevelName(level), minOverlap, overlap, minOverlap, recommendedForCurrent, getLevelName(recommendedForCurrent)),
		)
	} else {
		// Configuration is good
		GetLogger().Info("False positive filtering configured",
			"level", level,
			"level_name", getLevelName(level),
			"overlap", overlap,
			"min_overlap", minOverlap,
			"min_detections", minDetections,
			"hardware_req", getHardwareRequirementForLevel(level),
			"operation", "false_positive_filter_config")
		log.Printf("False positive filtering: Level %d (%s)",
			level, getLevelName(level))
		log.Printf("  Overlap: %.1f (min required: %.1f) ✓", overlap, minOverlap)
		log.Printf("  Requires %d confirmations in 6 seconds", minDetections)
		log.Printf("  Hardware: %s", getHardwareRequirementForLevel(level))
	}
}

// warnAboutHardwareRequirements checks if high filter levels (4-5) have
// sufficient hardware performance based on overlap settings and inference time.
func warnAboutHardwareRequirements(level int, overlap float64) {
	if level >= 4 {
		// Check if overlap is within valid range for calculation
		if overlap >= 3.0 {
			GetLogger().Warn("Overlap value too high for hardware calculation",
				"overlap", overlap,
				"max_valid", 2.9,
				"operation", "false_positive_filter_config")
		} else {
			stepSize := 3.0 - overlap
			maxInferenceTime := stepSize * 1000 // Convert to ms
			GetLogger().Warn("High filtering level requires fast hardware",
				"level", level,
				"required_inference_ms", maxInferenceTime,
				"operation", "false_positive_filter_config")
			log.Printf("  ⚠️  High level requires fast hardware: inference must complete in < %.0fms", maxInferenceTime)
		}
	}
}

// validateAndLogFilterConfig validates false positive filter configuration,
// logs appropriate messages, and sends UI notifications. This function handles
// all validation, logging, and user notification for the false positive filter.
func validateAndLogFilterConfig(settings *conf.Settings) {
	// Validate configuration
	if err := settings.Realtime.FalsePositiveFilter.Validate(); err != nil {
		GetLogger().Error("Invalid false positive filter configuration",
			"error", err,
			"operation", "false_positive_filter_validation")
		log.Printf("⚠️  Configuration error: %v", err)
		log.Printf("   Falling back to default: Level 0 (Off)")
		// Reset to safe default
		settings.Realtime.FalsePositiveFilter.Level = 0
	}

	level := settings.Realtime.FalsePositiveFilter.Level
	overlap := settings.BirdNET.Overlap
	minOverlap := getMinimumOverlapForLevel(level)

	// Calculate what minDetections will be with current settings
	minDetections := calculateMinDetectionsFromSettings(settings)

	if level == 0 {
		// Smart migration: suggest a level based on current overlap
		suggestLevelForDisabledFilter(overlap)
	} else {
		// Filtering is enabled - validate overlap and warn about hardware if needed
		validateOverlapForLevel(level, overlap, minOverlap, minDetections)
		warnAboutHardwareRequirements(level, overlap)
	}
}

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

	// Validate detection window configuration
	captureLength := time.Duration(settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength := time.Duration(settings.Realtime.Audio.Export.PreCapture) * time.Second
	detectionWindow := max(time.Duration(0), captureLength-preCaptureLength)

	// Warn if detection window is very short (may affect overlap-based filtering)
	minRecommendedWindow := 3 * time.Second
	if detectionWindow < minRecommendedWindow {
		GetLogger().Warn("Detection window very short, may affect accuracy",
			"window_seconds", detectionWindow.Seconds(),
			"capture_length_seconds", captureLength.Seconds(),
			"pre_capture_seconds", preCaptureLength.Seconds(),
			"min_recommended_seconds", minRecommendedWindow.Seconds(),
			"operation", "config_validation")
		log.Printf("Warning: Detection window (%v) is very short, may affect overlap-based filtering accuracy. "+
			"Minimum recommended: %v (capture_length=%v, pre_capture=%v)",
			detectionWindow, minRecommendedWindow, captureLength, preCaptureLength)
	}

	// Validate and log false positive filter configuration
	validateAndLogFilterConfig(settings)

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

			p.NewSpeciesTracker = species.NewTrackerFromSettings(ds, &hemisphereAwareTracking)

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

	// Load persisted dynamic thresholds from database if enabled
	if settings.Realtime.DynamicThreshold.Enabled {
		if err := p.loadDynamicThresholdsFromDB(); err != nil {
			GetLogger().Debug("Starting with fresh dynamic thresholds",
				"reason", err.Error(),
				"operation", "load_dynamic_thresholds")
			// This is normal on first run or if table doesn't exist yet
			// System will start with fixed thresholds and learn from detections
		}

		// Start periodic persistence goroutine
		p.startThresholdPersistence()

		// Start periodic cleanup goroutine
		p.startThresholdCleanup()
	}

	// Initialize spectrogram pre-renderer if mode is "prerender"
	if settings.Realtime.Dashboard.Spectrogram.IsPreRenderEnabled() {
		p.initPreRenderer()
	}

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
		"source", item.Source.DisplayName,
		"start_time", item.StartTime,
		"results_count", len(item.Results),
		"elapsed_time_ms", item.ElapsedTime.Milliseconds(),
		"operation", "process_detections_entry")

	// Detection window sets wait time before a detection is considered final and is flushed.
	// This represents the duration to wait from NOW (detection creation time) before flushing,
	// allowing overlapping analyses to accumulate confirmations for false positive filtering.
	captureLength := time.Duration(p.Settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength := time.Duration(p.Settings.Realtime.Audio.Export.PreCapture) * time.Second
	// Ensure detectionWindow is non-negative to prevent early flushes
	detectionWindow := max(time.Duration(0), captureLength-preCaptureLength)

	// processResults() returns a slice of detections, we iterate through each and process them
	// detections are put into pendingDetections map where they are held until flush deadline is reached
	// once deadline is reached detections are delivered to workers for actions (save to db etc) processing
	detectionResults := p.processResults(item)

	// Log processing results with deduplication to prevent spam
	p.logDetectionResults(item.Source.ID, len(item.Results), len(detectionResults))

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
				existing.Source = item.Source.ID
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
				"source", item.Source.DisplayName,
				"flush_deadline", time.Now().Add(detectionWindow),
				"operation", "create_pending_detection")
			p.pendingDetections[commonName] = PendingDetection{
				Detection:     detection,
				Confidence:    confidence,
				Source:        item.Source.ID,
				FirstDetected: item.StartTime,
				// FlushDeadline is relative to NOW (not startTime) to ensure it's always in the future.
				// startTime is backdated for audio extraction, but FlushDeadline needs to be a future deadline.
				FlushDeadline: time.Now().Add(detectionWindow),
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
		shouldSkip, _ := p.shouldFilterDetection(result, commonName, speciesLowercase, baseThreshold, item.Source.ID)
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
				"source", p.getDisplayNameForSource(source),
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

	// Get capture length and pre-capture length for detection end time calculation
	captureLength := time.Duration(p.Settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength := time.Duration(p.Settings.Realtime.Audio.Export.PreCapture) * time.Second

	// Set begin and end time for note
	beginTime := item.StartTime
	endTime := item.StartTime.Add(captureLength - preCaptureLength)

	// Get occurrence probability for this species at detection time
	occurrence := p.Bn.GetSpeciesOccurrenceAtTime(result.Species, item.StartTime)

	// Create the note
	note := p.NewWithSpeciesInfo(
		beginTime, endTime,
		scientificName, commonName, speciesCode,
		float64(result.Confidence),
		item.Source.ID, clipName,
		item.ElapsedTime, occurrence)

	// Update species tracker if enabled
	p.speciesTrackerMu.RLock()
	tracker := p.NewSpeciesTracker
	p.speciesTrackerMu.RUnlock()

	if tracker != nil {
		tracker.UpdateSpecies(scientificName, item.StartTime)
	}

	// Generate unique correlation ID for detection tracking
	correlationID := p.generateCorrelationID(commonName, item.StartTime)

	return Detections{
		CorrelationID: correlationID,
		pcmData3s:     item.PCMdata,
		Note:          note,
		Results:       item.Results,
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
			"source", item.Source.DisplayName,
			"operation", "dog_bark_filter")
		log.Printf("Dog detected with confidence %.3f/%.3f from source %s", result.Confidence, p.Settings.Realtime.DogBarkFilter.Confidence, item.Source.DisplayName)
		p.detectionMutex.Lock()
		p.LastDogDetection[item.Source.ID] = item.StartTime
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
			"source", item.Source.DisplayName,
			"operation", "privacy_filter")
		log.Printf("Human detected with confidence %.3f/%.3f from source %s", result.Confidence, p.Settings.Realtime.PrivacyFilter.Confidence, item.Source.DisplayName)
		// put human detection timestamp into LastHumanDetection map. This is used to discard
		// bird detections if a human vocalization is detected after the first detection
		p.detectionMutex.Lock()
		p.LastHumanDetection[item.Source.ID] = item.StartTime
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
			"source", p.getDisplayNameForSource(item.Source),
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
				"source", p.getDisplayNameForSource(item.Source),
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
				"source", p.getDisplayNameForSource(item.Source),
				"operation", "dog_bark_filter")
			return true, "recent dog bark"
		}
	}

	return false, ""
}

// processApprovedDetection handles an approved detection by sending it to the worker queue
func (p *Processor) processApprovedDetection(item *PendingDetection, speciesName string) {
	// Safely get confidence value
	var confidence float64
	if len(item.Detection.Results) > 0 {
		confidence = float64(item.Detection.Results[0].Confidence)
	}

	// Add structured logging
	GetLogger().Info("Approving detection",
		"species", speciesName,
		"source", p.getDisplayNameForSource(item.Source),
		"match_count", item.Count,
		"confidence", confidence,
		"has_results", len(item.Detection.Results) > 0,
		"operation", "approve_detection")
	log.Printf("Approving detection of %s from source %s, matched %d times\n",
		speciesName, p.getDisplayNameForSource(item.Source), item.Count)

	item.Detection.Note.BeginTime = item.FirstDetected
	actionList := p.getActionsForItem(&item.Detection)
	for _, action := range actionList {
		task := &Task{Type: TaskTypeAction, Detection: item.Detection, Action: action}
		if err := p.EnqueueTask(task); err != nil {
			// Check error message instead of using errors.Is to avoid import cycle
			if err.Error() == "worker queue is full" {
				// Add structured logging
				GetLogger().Warn("Worker queue is full, dropping task",
					"species", speciesName,
					"operation", "enqueue_task",
					"error", "queue_full")
				log.Printf("❌ Worker queue is full, dropping task for %s", speciesName)
			} else {
				sanitizedErr := sanitizeError(err)
				// Add structured logging
				GetLogger().Error("Failed to enqueue task",
					"error", sanitizedErr,
					"species", speciesName,
					"operation", "enqueue_task")
				log.Printf("Failed to enqueue task for %s: %v", speciesName, sanitizedErr)
			}
			continue
		}
	}

	// Update BirdNET metrics detection counter if enabled
	if p.Settings.Realtime.Telemetry.Enabled && p.Metrics != nil && p.Metrics.BirdNET != nil {
		p.Metrics.BirdNET.IncrementDetectionCounter(item.Detection.Note.CommonName)
	}
}

// calculateMinDetections computes the minimum number of required detections based on
// the overlap setting and false positive filter level to filter false positives through
// repeated detection confirmation.
//
// The overlap determines how frequently the same audio content is analyzed:
//   - With overlap 2.0: step size = 1.0s, so a 3-second chunk is analyzed ~3 times
//   - With overlap 2.5: step size = 0.5s, so a 3-second chunk is analyzed ~6 times
//
// A real bird call should be detected consistently across these overlapping analyses,
// while false positives (noise, wind) are random and won't repeat reliably.
//
// The function calculates:
//   1. How many times audio can be analyzed within a 6-second bird vocalization window
//   2. Requires a percentage of those analyses to detect the same species (based on level)
//
// Filtering Levels (0-5):
//   Level 0: Off (no filtering, 1 detection required)
//   Level 1: Lenient (20% threshold, ~2 detections)
//   Level 2: Moderate (30% threshold, ~3 detections)
//   Level 3: Balanced (50% threshold, ~5 detections - original pre-Sept 2025 behavior)
//   Level 4: Strict (60% threshold, ~12 detections - requires RPi 4+)
//   Level 5: Maximum (70% threshold, ~21 detections - requires RPi 4+)
//
// Note: Audio clip length (captureLength/preCapture) does NOT affect this calculation.
// Those settings control saved audio length, not detection sensitivity.
//
// Edge cases handled:
//   - If level is 0: minDetections = 1 (filtering disabled)
//   - If overlap is 0 (no overlap): minDetections = 1 (no repeated confirmation possible)
//   - Very high overlap (>2.9): may require many detections at higher levels
//   - Floating-point precision: epsilon subtraction prevents values like 5.0000003 from ceiling to 6
// calculateMinDetectionsFromSettings computes minimum detections from settings alone.
// This is a standalone function that doesn't require a Processor instance.
func calculateMinDetectionsFromSettings(settings *conf.Settings) int {
	// BirdNET uses 3-second chunks for analysis
	const chunkDurationSeconds = 3.0
	// Bird vocalization reference window - typical duration of a bird call
	// Used to calculate how many detections are possible within a single vocalization
	const referenceWindowSeconds = 6.0
	// Minimum segment length to prevent division by near-zero values
	const minSegmentLength = 0.1
	// Small epsilon to prevent floating-point rounding errors in ceil()
	// Without this, values like 5.0000000003 would ceil to 6 instead of 5
	const epsilon = 1e-9

	// Get filtering level from settings
	level := settings.Realtime.FalsePositiveFilter.Level
	overlap := settings.BirdNET.Overlap

	// Level 0: no filtering
	if level == 0 {
		return 1
	}

	// Validate overlap is within valid range
	if overlap >= chunkDurationSeconds {
		GetLogger().Warn("Overlap equals or exceeds chunk duration",
			"overlap", overlap,
			"chunk_duration", chunkDurationSeconds,
			"operation", "calculate_min_detections")
		// Continue with safe fallback
	}

	// Validate overlap meets minimum for level (warning only, don't block)
	minOverlap := getMinimumOverlapForLevel(level)
	if overlap < minOverlap {
		GetLogger().Warn("Overlap too low for filtering level",
			"level", level,
			"level_name", getLevelName(level),
			"min_overlap", minOverlap,
			"current_overlap", overlap,
			"operation", "calculate_min_detections")
		// Continue with calculation - system will work but may not achieve target filtering
	}

	// Calculate segment length (how often we analyze)
	segmentLength := math.Max(minSegmentLength, chunkDurationSeconds-overlap)

	// How many detections are possible within a 6-second bird vocalization window?
	maxDetectionsIn6s := referenceWindowSeconds / segmentLength

	// Get threshold percentage for this level
	threshold := getThresholdForLevel(level)

	// Calculate minimum required detections
	// Use Ceil to ensure we require at least the threshold percentage
	// Subtract epsilon before ceiling to handle floating-point precision issues
	// (e.g., 5.0000000003 becomes 4.9999999993, which correctly ceils to 5)
	// Always require at least 1 detection
	required := maxDetectionsIn6s*threshold - epsilon
	minDetections := int(math.Max(1, math.Ceil(required)))

	return minDetections
}

// calculateMinDetections is a convenience method that calls calculateMinDetectionsFromSettings
// with the processor's settings.
func (p *Processor) calculateMinDetections() int {
	return calculateMinDetectionsFromSettings(p.Settings)
}

// pendingDetectionsFlusher runs a goroutine that periodically checks the pending detections
// and flushes them to the worker queue if their deadline has passed.
func (p *Processor) pendingDetectionsFlusher() {
	// Add structured logging for pending detections flusher startup
	GetLogger().Info("Starting pending detections flusher",
		"flush_interval_seconds", 1,
		"operation", "pending_flusher_startup")

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		// Track last minDetections value to log changes
		lastMinDetections := -1

		for {
			<-ticker.C
			now := time.Now()

			// Recalculate minDetections on each iteration to account for runtime config changes
			minDetections := p.calculateMinDetections()

			// Log when minDetections changes due to config update
			if lastMinDetections != -1 && minDetections != lastMinDetections {
				GetLogger().Info("minDetections updated due to config change",
					"old_value", lastMinDetections,
					"new_value", minDetections,
					"operation", "pending_flusher_config_update")
			}
			lastMinDetections = minDetections

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
							"source", p.getDisplayNameForSource(item.Source),
							"reason", reason,
							"count", item.Count,
							"operation", "discard_detection")
						log.Printf("Discarding detection of %s from source %s due to %s\n",
							species, p.getDisplayNameForSource(item.Source), reason)
						delete(p.pendingDetections, species)
						continue
					}

					// Log when detection is flushed to help debug future timing issues
					GetLogger().Debug("Flushing detection",
						"species", species,
						"source", p.getDisplayNameForSource(item.Source),
						"deadline_reached", now.After(item.FlushDeadline),
						"count", item.Count,
						"required", minDetections,
						"operation", "flush_detection")

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
	var databaseAction *DatabaseAction
	var sseAction *SSEAction

	// Append various default actions based on the application settings
	if p.Settings.Realtime.Log.Enabled {
		actions = append(actions, &LogAction{
			Settings:      p.Settings,
			EventTracker:  p.GetEventTracker(),
			Note:          detection.Note,
			CorrelationID: detection.CorrelationID,
		})
	}

	// Create DatabaseAction if database is enabled
	if p.Settings.Output.SQLite.Enabled || p.Settings.Output.MySQL.Enabled {
		p.speciesTrackerMu.RLock()
		tracker := p.NewSpeciesTracker
		p.speciesTrackerMu.RUnlock()

		databaseAction = &DatabaseAction{
			Settings:          p.Settings,
			EventTracker:      p.GetEventTracker(),
			NewSpeciesTracker: tracker,
			processor:         p, // Add processor reference for source name resolution
			PreRenderer:       p.preRenderer,
			Note:              detection.Note,
			Results:           detection.Results,
			Ds:                p.Ds,
			CorrelationID:     detection.CorrelationID,
		}
	}

	// Create SSE action if broadcaster is available (enabled when SSE API is configured)
	if sseBroadcaster := p.GetSSEBroadcaster(); sseBroadcaster != nil {
		// Create SSE retry config - use sensible defaults since SSE should be reliable
		sseRetryConfig := jobqueue.RetryConfig{
			Enabled:      true, // Enable retries for SSE to improve reliability
			MaxRetries:   3,    // Conservative retry count for real-time streaming
			InitialDelay: 1 * time.Second,
			MaxDelay:     5 * time.Second,
			Multiplier:   2.0,
		}

		sseAction = &SSEAction{
			Settings:       p.Settings,
			Note:           detection.Note,
			BirdImageCache: p.BirdImageCache,
			EventTracker:   p.GetEventTracker(),
			RetryConfig:    sseRetryConfig,
			SSEBroadcaster: sseBroadcaster,
			Ds:             p.Ds,
			CorrelationID:  detection.CorrelationID,
		}
	}

	// CRITICAL FIX for GitHub issue #1158: Race condition between DatabaseAction and SSEAction
	//
	// Problem: When both database and SSE are enabled, they execute concurrently via the job queue.
	// SSEAction polls for database records that haven't been saved yet, causing timeout errors:
	//   - "database ID not assigned for Eastern Wood-Pewee after 10s timeout"
	//   - "note not found in database"
	//   - "audio file ... not ready after 5s timeout"
	//
	// Solution: Combine DatabaseAction and SSEAction into a CompositeAction that executes them
	// sequentially. This ensures the database save completes before SSE attempts to broadcast,
	// eliminating the race condition while maintaining all other actions' concurrent execution.
	//
	// This is particularly important on resource-constrained hardware (e.g., Raspberry Pi with
	// SD cards) where database writes can take several seconds to complete.
	if databaseAction != nil && sseAction != nil {
		// Create composite action for sequential execution
		compositeAction := &CompositeAction{
			Actions:       []Action{databaseAction, sseAction},
			Description:   "Database save and SSE broadcast (sequential)",
			CorrelationID: detection.CorrelationID,
		}
		actions = append(actions, compositeAction)
	} else {
		// Add them individually if only one is enabled
		if databaseAction != nil {
			actions = append(actions, databaseAction)
		}
		if sseAction != nil {
			actions = append(actions, sseAction)
		}
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
				Settings:      p.Settings,
				EventTracker:  p.GetEventTracker(),
				BwClient:      bwClient,
				Note:          detection.Note,
				pcmData:       detection.pcmData3s,
				RetryConfig:   bwRetryConfig,
				CorrelationID: detection.CorrelationID,
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
				CorrelationID:  detection.CorrelationID,
			})
		}
	}

	// Check if UpdateRangeFilterAction needs to be executed for the day
	// Use atomic check-and-set to prevent race conditions (see GitHub issue #1357)
	// This ensures only ONE goroutine will trigger the daily range filter update,
	// preventing concurrent updates that could cause species list inconsistencies
	if p.Settings.ShouldUpdateRangeFilterToday() {
		// Add structured logging
		GetLogger().Info("Scheduling daily range filter update",
			"last_updated", p.Settings.GetLastRangeFilterUpdate(),
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
// Falls back to sanitized source if lookup fails (prevents credential exposure)
// TODO: Consider moving to AudioSource struct throughout the pipeline to eliminate this lookup
func (p *Processor) getDisplayNameForSource(sourceID string) string {
	registry := myaudio.GetRegistry()
	if registry != nil {
		// Try lookup by ID first
		if source, exists := registry.GetSourceByID(sourceID); exists {
			return source.DisplayName
		}

		// Try lookup by connection string (handles legacy case)
		if source, exists := registry.GetSourceByConnection(sourceID); exists {
			return source.DisplayName
		}
	}

	// Fallback: sanitize the source to prevent credential exposure in logs
	// This handles cases where sourceID might be a raw RTSP URL
	return privacy.SanitizeRTSPUrl(sourceID)
}

// Shutdown gracefully stops all processor components
func (p *Processor) Shutdown() error {
	// Stop threshold persistence and cleanup goroutines first
	if p.thresholdsCancel != nil {
		p.thresholdsCancel()
	}

	// Flush dynamic thresholds to database before shutting down with timeout
	if p.Settings.Realtime.DynamicThreshold.Enabled {
		// Use context-based timeout for cleaner cancellation handling
		ctx, cancel := context.WithTimeout(context.Background(), DefaultFlushTimeout)
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- p.FlushDynamicThresholds()
		}()

		select {
		case err := <-done:
			if err != nil {
				GetLogger().Warn("Failed to flush dynamic thresholds during shutdown",
					"error", err,
					"operation", "shutdown_flush_thresholds")
			}
		case <-ctx.Done():
			GetLogger().Warn("Timeout flushing dynamic thresholds during shutdown",
				"timeout_seconds", int(DefaultFlushTimeout.Seconds()),
				"operation", "shutdown_flush_thresholds")
		}
	}

	// Cancel all worker goroutines
	if p.workerCancel != nil {
		p.workerCancel()
	}

	// Stop the spectrogram pre-renderer
	if p.preRenderer != nil {
		p.preRenderer.Stop()
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
	elapsedTime time.Duration,
	occurrence float64) datastore.Note {

	// detectionTime is time now minus 3 seconds to account for the delay in the detection
	now := time.Now()
	date := now.Format("2006-01-02")
	detectionTime := now.Add(-2 * time.Second)
	timeStr := detectionTime.Format("15:04:05")

	var sourceStruct datastore.AudioSource
	if p.Settings.Input.Path != "" {
		// For file input, create simple source struct
		sourceStruct = datastore.AudioSource{
			ID:          source, // Use original source as ID for file operations
			SafeString:  p.Settings.Input.Path,
			DisplayName: filepath.Base(p.Settings.Input.Path),
		}
	} else {
		// Use registry to get proper AudioSource struct with ID, SafeString, and DisplayName
		registry := myaudio.GetRegistry()
		if registry != nil {
			// Try to get existing source by connection string
			if existingSource, exists := registry.GetSourceByConnection(source); exists {
				sourceStruct = datastore.AudioSource{
					ID:          existingSource.ID,          // Use source ID for buffer operations
					SafeString:  existingSource.SafeString,  // Use sanitized string for logging
					DisplayName: existingSource.DisplayName, // Use display name for UI
				}
			} else {
				// Try to get by ID directly
				if registrySource, exists := registry.GetSourceByID(source); exists {
					sourceStruct = datastore.AudioSource{
						ID:          registrySource.ID,
						SafeString:  registrySource.SafeString,
						DisplayName: registrySource.DisplayName,
					}
				} else {
					// Last resort: create struct with manual sanitization for safety
					sourceStruct = datastore.AudioSource{
						ID:          source,                          // Use original as ID
						SafeString:  privacy.SanitizeRTSPUrl(source), // Sanitize for logging
						DisplayName: privacy.SanitizeRTSPUrl(source), // Use same for display
					}
				}
			}
		} else {
			// Fallback when registry not available
			sourceStruct = datastore.AudioSource{
				ID:          source,                          // Use original as ID
				SafeString:  privacy.SanitizeRTSPUrl(source), // Sanitize for logging
				DisplayName: privacy.SanitizeRTSPUrl(source), // Use same for display
			}
		}
	}

	// Round confidence to two decimal places
	roundedConfidence := math.Round(confidence*100) / 100

	// Return a new Note struct populated with the provided parameters and the current date and time
	return datastore.Note{
		SourceNode:     p.Settings.Main.Name,           // From the provided configuration settings
		Date:           date,                           // Use ISO 8601 date format
		Time:           timeStr,                        // Use 24-hour time format
		Source:         sourceStruct,                   // Proper AudioSource struct with ID, SafeString, DisplayName
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
		Occurrence:     occurrence,                     // Runtime occurrence probability (not persisted to DB)
	}
}

// logDetectionResults logs detection processing results using the LogDeduplicator
// to prevent repetitive logging while maintaining observability.
//
// Strategy (BG-18):
//   - INFO level: Only when filtered_detections_count > 0 (actual detections)
//   - DEBUG level: Zero-detection cycles (for troubleshooting without log spam)
//
// This prevents ~40,000+ identical "filtered_detections_count:0" logs per day
// while still allowing debug-mode visibility into the detection pipeline.
func (p *Processor) logDetectionResults(source string, rawCount, filteredCount int) {
	// Use the LogDeduplicator to determine if we should log
	shouldLog, reason := p.logDedup.ShouldLog(source, rawCount, filteredCount)

	if shouldLog {
		// Only log at INFO level when there are actual filtered detections
		// This prevents log spam from empty analysis cycles
		if filteredCount > 0 {
			GetLogger().Info("Detection processing results",
				"source", p.getDisplayNameForSource(source),
				"raw_results_count", rawCount,
				"filtered_detections_count", filteredCount,
				"log_reason", reason,
				"operation", "process_detections_summary")
		} else {
			// Log zero-detection cycles at DEBUG level for troubleshooting
			// without flooding INFO logs with noise
			GetLogger().Debug("Detection processing results",
				"source", p.getDisplayNameForSource(source),
				"raw_results_count", rawCount,
				"filtered_detections_count", 0,
				"log_reason", reason,
				"operation", "process_detections_summary")
		}
	}
}

// generateCorrelationID creates a unique, human-readable identifier for detection tracking
// Format: SPEC_HHMM_XXXX (e.g., "CROW_1108_a7f3")
func (p *Processor) generateCorrelationID(speciesName string, timestamp time.Time) string {
	// Create species prefix (first 4 characters, uppercase)
	speciesPrefix := strings.ToUpper(speciesName)
	if len(speciesPrefix) > 4 {
		speciesPrefix = speciesPrefix[:4]
	}
	// Pad with underscores if too short
	for len(speciesPrefix) < 4 {
		speciesPrefix += "_"
	}

	// Format time as HHMM
	timeStr := timestamp.Format("1504")

	// Generate 4-character random hex suffix
	randomSuffix := generateRandomHex(4)

	return fmt.Sprintf("%s_%s_%s", speciesPrefix, timeStr, randomSuffix)
}

// generateRandomHex generates a random hexadecimal string of specified length
func generateRandomHex(length int) string {
	bytes := make([]byte, (length+1)/2) // Round up for odd lengths
	_, err := rand.Read(bytes)
	if err != nil {
		// Fallback to timestamp-based randomness if crypto/rand fails
		// Build a hex string of exactly the requested length
		fallback := fmt.Sprintf("%016x", time.Now().UnixNano())
		// Repeat the fallback string if needed to ensure we have enough length
		for len(fallback) < length {
			fallback += fmt.Sprintf("%016x", time.Now().UnixNano())
		}
		return fallback[:length]
	}

	hex := fmt.Sprintf("%x", bytes)
	if len(hex) > length {
		hex = hex[:length]
	}
	return hex
}

// initPreRenderer initializes the spectrogram pre-renderer if enabled.
// This is called during processor initialization if spectrogram pre-rendering is enabled in settings.
func (p *Processor) initPreRenderer() {
	p.preRendererOnce.Do(func() {
		// Validate export path
		if p.Settings.Realtime.Audio.Export.Path == "" {
			GetLogger().Error("Export path not configured, disabling pre-rendering",
				"operation", "prerenderer_init")
			return
		}

		// Verify export path exists and is writable
		if err := os.MkdirAll(p.Settings.Realtime.Audio.Export.Path, 0o755); err != nil {
			GetLogger().Error("Export path not writable, disabling pre-rendering",
				"path", p.Settings.Realtime.Audio.Export.Path,
				"error", err,
				"operation", "prerenderer_init")
			return
		}

		// Validate spectrogram size configuration early using shared validation
		size := p.Settings.Realtime.Dashboard.Spectrogram.Size
		validSizesList := spectrogram.GetValidSizes()
		if !slices.Contains(validSizesList, size) {
			GetLogger().Error("Invalid spectrogram size, disabling pre-rendering",
				"size", size,
				"valid_sizes", validSizesList,
				"operation", "prerenderer_init")
			log.Printf("❌ Invalid spectrogram size '%s', pre-rendering disabled. Valid sizes: %v", size, validSizesList)
			return
		}

		// Validate Sox binary is configured and exists
		if p.Settings.Realtime.Audio.SoxPath == "" {
			GetLogger().Error("Sox binary not configured, disabling pre-rendering",
				"operation", "prerenderer_init")
			return
		}
		if _, err := exec.LookPath(p.Settings.Realtime.Audio.SoxPath); err != nil {
			GetLogger().Error("Sox binary not found, disabling pre-rendering",
				"path", p.Settings.Realtime.Audio.SoxPath,
				"error", err,
				"operation", "prerenderer_init")
			return
		}

		// Create SecureFS for path validation
		sfs, err := securefs.New(p.Settings.Realtime.Audio.Export.Path)
		if err != nil {
			GetLogger().Error("Failed to create SecureFS for pre-renderer",
				"error", err,
				"export_path", p.Settings.Realtime.Audio.Export.Path,
				"operation", "prerenderer_init")
			return
		}

		// Create context for pre-renderer lifecycle (derived from processor's context if available)
		ctx := context.Background()

		// Create and start pre-renderer
		pr := spectrogram.NewPreRenderer(ctx, p.Settings, sfs, GetLogger())
		pr.Start()

		p.preRenderer = pr

		GetLogger().Info("Spectrogram pre-renderer initialized",
			"size", p.Settings.Realtime.Dashboard.Spectrogram.Size,
			"raw", p.Settings.Realtime.Dashboard.Spectrogram.Raw,
			"operation", "prerenderer_init")
	})
}
