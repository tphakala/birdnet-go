// buffers.go: The file contains the implementation of the buffer monitor that reads audio data from the ring buffer and processes it when enough data is present.
package myaudio

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/ringbuffer"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	pollInterval             = time.Millisecond * 10
	warningCapacityThreshold = 0.9 // 90% full

	// Overwrite tracking constants for detecting sustained buffer overload.
	overwriteWindowDuration = 5 * time.Minute // sliding window for rate calculation
	overwriteRateThreshold  = 10              // percentage threshold to trigger notification
	overwriteMinWrites      = 50              // minimum writes in window before checking rate
	overwriteNotifyCooldown = 1 * time.Hour   // minimum time between notifications per source
)

// overwriteTracker tracks buffer overwrite events for a source over a sliding window.
type overwriteTracker struct {
	mu             sync.Mutex
	totalWrites    int64     // total writes in current window
	overwriteCount int64     // overwrites in current window
	windowStart    time.Time // start of current tracking window
	lastNotified   time.Time // last time a notification was sent
}

var (
	overlapSize          int                               // overlapSize is the number of bytes to overlap between chunks
	readSize             int                               // readSize is the number of bytes to read from the ring buffer
	analysisBuffers      map[string]*ringbuffer.RingBuffer // analysisBuffers is a map to store ring buffers for each audio source
	prevData             map[string][]byte                 // prevData is a map to store the previous data for each audio source
	abMutex              sync.RWMutex                      // Mutex to protect access to the analysisBuffers and prevData maps
	warningCounter       map[string]int
	warningCounterMutex  sync.Mutex              // Mutex to protect access to warningCounter map
	analysisMetrics      *metrics.MyAudioMetrics // Global metrics instance for analysis buffer operations
	analysisMetricsMutex sync.RWMutex            // Mutex for thread-safe access to analysisMetrics
	analysisMetricsOnce  sync.Once               // Ensures metrics are only set once
	readBufferPool       *BufferPool             // Global buffer pool for read operations
	bufferPoolInitMu     sync.Mutex              // Protects buffer pool initialization

	overwriteTrackers   = make(map[string]*overwriteTracker) // per-source overwrite trackers
	overwriteTrackersMu sync.RWMutex                         // Mutex to protect overwriteTrackers map
)

// init initializes the warningCounter map
func init() {
	warningCounter = make(map[string]int)
}

// SetAnalysisMetrics sets the metrics instance for analysis buffer operations.
// This function is thread-safe and ensures metrics are only set once per process lifetime.
// Subsequent calls will be ignored due to sync.Once (idempotent behavior).
func SetAnalysisMetrics(myAudioMetrics *metrics.MyAudioMetrics) {
	analysisMetricsOnce.Do(func() {
		analysisMetricsMutex.Lock()
		defer analysisMetricsMutex.Unlock()
		analysisMetrics = myAudioMetrics
	})
}

// getAnalysisMetrics returns the current metrics instance in a thread-safe manner
func getAnalysisMetrics() *metrics.MyAudioMetrics {
	analysisMetricsMutex.RLock()
	defer analysisMetricsMutex.RUnlock()
	return analysisMetrics
}

// SecondsToBytes converts overlap in seconds to bytes
func SecondsToBytes(seconds float64) int {
	return int(seconds * float64(conf.SampleRate) * float64(conf.BitDepth/8))
}

// AllocateAnalysisBuffer initializes a ring buffer for a single audio source ID.
// It returns an error if memory allocation fails or if the input is invalid.
func AllocateAnalysisBuffer(capacity int, sourceID string) error {

	start := time.Now()

	// Validate inputs
	if capacity <= 0 {
		enhancedErr := errors.Newf("invalid analysis buffer capacity: %d, must be greater than 0", capacity).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "allocate_analysis_buffer").
			Context("source", sourceID).
			Context("requested_capacity", capacity).
			Build()

		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferAllocation("analysis", sourceID, "error")
			m.RecordBufferAllocationError("analysis", sourceID, "invalid_capacity")
		}
		return enhancedErr
	}
	if sourceID == "" {
		enhancedErr := errors.Newf("empty source name provided for analysis buffer allocation").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "allocate_analysis_buffer").
			Build()

		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferAllocation("analysis", "unknown", "error")
			m.RecordBufferAllocationError("analysis", "unknown", "empty_source")
		}
		return enhancedErr
	}

	settings := conf.Setting()

	// Initialize buffer pool if not yet created (or after full cleanup).
	// Uses a separate mutex to avoid holding abMutex during initialization.
	bufferPoolInitMu.Lock()
	if readBufferPool == nil {
		overlapSize = SecondsToBytes(settings.BirdNET.Overlap)
		readSize = conf.BufferSize - overlapSize
		var initErr error
		readBufferPool, initErr = NewBufferPool(readSize)
		if initErr != nil {
			bufferPoolInitMu.Unlock()
			return errors.New(initErr).
				Component("myaudio").
				Category(errors.CategorySystem).
				Context("operation", "allocate_analysis_buffer").
				Context("source", sourceID).
				Context("buffer_pool_size", readSize).
				Build()
		}
	}
	bufferPoolInitMu.Unlock()

	// Initialize the analysis ring buffer with overwrite mode enabled.
	// When the buffer is full, new writes overwrite the oldest data instead of
	// returning ErrIsFull. This provides graceful backpressure handling for
	// real-time audio: dropping stale samples is preferable to failing writes
	// or dropping current samples.
	ab := ringbuffer.New(capacity)
	if ab == nil {
		enhancedErr := errors.Newf("failed to allocate ring buffer memory for analysis buffer").
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "allocate_analysis_buffer").
			Context("source", sourceID).
			Context("requested_capacity", capacity).
			Build()

		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferAllocation("analysis", sourceID, "error")
			m.RecordBufferAllocationError("analysis", sourceID, "memory_allocation_failed")
		}
		return enhancedErr
	}
	ab.SetOverwrite(true)

	// Update global variables safely
	abMutex.Lock()
	defer abMutex.Unlock()

	// Check if buffer already exists
	if _, exists := analysisBuffers[sourceID]; exists {
		ab.Reset() // Clean up the new buffer since we won't use it
		enhancedErr := errors.Newf("analysis buffer already exists for source: %s", sourceID).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "allocate_analysis_buffer").
			Context("source", sourceID).
			Build()

		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferAllocation("analysis", sourceID, "error")
			m.RecordBufferAllocationError("analysis", sourceID, "already_exists")
		}
		return enhancedErr
	}

	// Initialize maps if they don't exist
	if analysisBuffers == nil {
		analysisBuffers = make(map[string]*ringbuffer.RingBuffer)
	}
	if prevData == nil {
		prevData = make(map[string][]byte)
	}
	if warningCounter == nil {
		warningCounter = make(map[string]int)
	}

	analysisBuffers[sourceID] = ab
	prevData[sourceID] = nil
	warningCounter[sourceID] = 0

	// Create overwrite tracker for this source
	overwriteTrackersMu.Lock()
	overwriteTrackers[sourceID] = &overwriteTracker{
		windowStart: time.Now(),
	}
	overwriteTrackersMu.Unlock()

	// Acquire reference to this source using the migrated ID
	registry := GetRegistry()
	// Guard against nil registry during initialization to prevent panic
	if registry != nil {
		registry.AcquireSourceReference(sourceID)
	} else {
		log := GetLogger()
		log.Warn("registry not available during analysis buffer allocation",
			logger.String("source_id", sourceID),
			logger.String("operation", "allocate_analysis_buffer"))
	}

	// Record successful allocation metrics
	if m := getAnalysisMetrics(); m != nil {
		duration := time.Since(start).Seconds()
		m.RecordBufferAllocation("analysis", sourceID, "success")
		m.RecordBufferAllocationDuration("analysis", sourceID, duration)
		m.UpdateBufferCapacity("analysis", sourceID, capacity)
		m.UpdateBufferSize("analysis", sourceID, 0) // Empty at start
		m.UpdateBufferUtilization("analysis", sourceID, 0.0)
	}

	// Log the buffer creation for debugging
	//log.Printf("✅ Created analysis buffer for %s with capacity %d bytes", source, ab.Capacity())

	return nil
}

// RemoveAnalysisBuffer safely removes and cleans up a ring buffer for a single source ID.
func RemoveAnalysisBuffer(sourceID string) error {

	abMutex.Lock()
	ab, exists := analysisBuffers[sourceID]
	if !exists {
		abMutex.Unlock()
		return fmt.Errorf("no ring buffer found for source ID: %s", sourceID)
	}

	// Clean up the buffer
	ab.Reset()

	// Remove from all maps
	delete(analysisBuffers, sourceID)
	delete(prevData, sourceID)
	delete(warningCounter, sourceID)

	// Remove overwrite tracker for this source
	overwriteTrackersMu.Lock()
	delete(overwriteTrackers, sourceID)
	overwriteTrackersMu.Unlock()

	// Clean up buffer pool if this was the last buffer (prevents memory leak)
	if len(analysisBuffers) == 0 {
		bufferPoolInitMu.Lock()
		if readBufferPool != nil {
			// Clear the buffer pool to release all cached buffers
			readBufferPool.Clear()
			readBufferPool = nil
			overlapSize = 0
			readSize = 0
		}
		bufferPoolInitMu.Unlock()
	}
	abMutex.Unlock() // Release lock before calling registry

	// Release reference to this source - registry will auto-remove if count reaches zero
	registry := GetRegistry()
	log := GetLogger()
	// Guard against nil registry during shutdown to prevent panic
	if registry != nil {
		if err := registry.ReleaseSourceReference(sourceID); err != nil {
			// Log but don't fail - buffer removal succeeded
			if !errors.Is(err, ErrSourceNotFound) {
				log.Warn("failed to release source reference",
					logger.String("source_id", sourceID),
					logger.Error(err))
			}
		}
	} else {
		log.Warn("registry not available during analysis buffer cleanup",
			logger.String("source_id", sourceID),
			logger.String("operation", "remove_analysis_buffer"))
	}

	return nil
}

// InitAnalysisBuffers initializes the ring buffers for each audio source with a given capacity.
// It returns an error if memory allocation fails or if the inputs are invalid.
func InitAnalysisBuffers(capacity int, sources []string) error {
	// Validate inputs
	if capacity <= 0 {
		return fmt.Errorf("invalid capacity: %d, must be greater than 0", capacity)
	}
	if len(sources) == 0 {
		return fmt.Errorf("no audio sources provided")
	}

	// Try to initialize each buffer
	var initErrors []string
	for _, source := range sources {
		if err := AllocateAnalysisBuffer(capacity, source); err != nil {
			initErrors = append(initErrors, fmt.Sprintf("source %s: %v", source, err))
		}
	}

	// If there were any errors, return them all
	if len(initErrors) > 0 {
		return fmt.Errorf("failed to initialize some ring buffers: %s", strings.Join(initErrors, "; "))
	}

	return nil
}

// WriteToAnalysisBuffer writes audio data into the ring buffer for a given source ID.
func WriteToAnalysisBuffer(sourceID string, data []byte) error {
	log := GetLogger()

	// Get source info for enhanced logging (ID + DisplayName)
	var displayName string
	if registry := GetRegistry(); registry != nil {
		if source, exists := registry.GetSourceByID(sourceID); exists {
			displayName = source.DisplayName
		}
	}
	// Default fallback if registry lookup fails
	if displayName == "" {
		displayName = sourceID
	}

	start := time.Now()

	abMutex.RLock()
	ab, exists := analysisBuffers[sourceID]
	abMutex.RUnlock()

	if !exists {
		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferWrite("analysis", sourceID, "error")
			m.RecordBufferWriteError("analysis", sourceID, "buffer_not_found")
		}
		return fmt.Errorf("%w: source ID %s (%s)", ErrBufferNotFound, sourceID, displayName)
	}

	// Get buffer capacity information
	capacity := ab.Capacity()
	if capacity == 0 {
		enhancedErr := errors.Newf("analysis buffer for source ID %s (%s) has zero capacity", sourceID, displayName).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "write_to_analysis_buffer").
			Context("source_id", sourceID).
			Context("display_name", displayName).
			Context("data_size", len(data)).
			Build()

		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferWrite("analysis", sourceID, "error")
			m.RecordBufferWriteError("analysis", sourceID, "zero_capacity")
		}
		return enhancedErr
	}

	// Check buffer capacity and update metrics before writing
	currentLength := ab.Length()
	capacityUsed := float64(currentLength) / float64(capacity)

	if m := getAnalysisMetrics(); m != nil {
		m.UpdateBufferUtilization("analysis", sourceID, capacityUsed)
		m.UpdateBufferSize("analysis", sourceID, currentLength)
	}

	// Detect if this write will overwrite old data (buffer has no free space)
	willOverwrite := ab.Free() == 0

	// Write data to the ring buffer.
	// With overwrite mode enabled, writes always succeed by discarding oldest data
	// when the buffer is full, so no retry logic is needed.
	var n int
	err := func() error {
		abMutex.Lock()
		defer abMutex.Unlock()

		var writeErr error
		n, writeErr = ab.Write(data)
		return writeErr
	}()

	// Track overwrite events for sustained overload detection
	trackOverwrite(sourceID, willOverwrite)

	if err != nil {
		// Unexpected error (overwrite mode should prevent ErrIsFull)
		log.Error("unexpected error writing to analysis buffer",
			logger.String("display_name", displayName),
			logger.String("source_id", sourceID),
			logger.Error(err),
			logger.Int("capacity", capacity),
			logger.Int("used_bytes", ab.Length()),
			logger.Int("free_bytes", ab.Free()))

		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferWrite("analysis", sourceID, "error")
			m.RecordBufferWriteError("analysis", sourceID, "write_failed")
		}

		return errors.New(err).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "write_to_analysis_buffer").
			Context("source_id", sourceID).
			Context("data_size", len(data)).
			Context("buffer_capacity", capacity).
			Context("capacity_utilization", capacityUsed).
			Build()
	}

	// Record successful write metrics
	if m := getAnalysisMetrics(); m != nil {
		duration := time.Since(start).Seconds()
		m.RecordBufferWrite("analysis", sourceID, "success")
		m.RecordBufferWriteDuration("analysis", sourceID, duration)
		m.RecordBufferWriteBytes("analysis", sourceID, n)
		m.RecordAudioDataSize(sourceID, n)
	}

	// Log if the buffer was near capacity (data was likely overwritten)
	if capacityUsed > warningCapacityThreshold {
		warningCounterMutex.Lock()
		warningCounter[sourceID]++
		shouldLog := warningCounter[sourceID]%32 == 1
		warningCounterMutex.Unlock()

		if shouldLog {
			log.Warn("analysis buffer overwriting oldest data due to high utilization",
				logger.String("display_name", displayName),
				logger.String("source_id", sourceID),
				logger.Float64("capacity_percent", capacityUsed*100),
				logger.Int("used_bytes", ab.Length()),
				logger.Int("capacity_bytes", capacity))
		}

		if m := getAnalysisMetrics(); m != nil && capacityUsed > 0.95 {
			m.RecordBufferOverflow("analysis", sourceID)
		}
	}

	return nil
}

// ReadFromAnalysisBuffer reads a sliding chunk of audio data from the ring buffer for a given source ID.
func ReadFromAnalysisBuffer(sourceID string) ([]byte, error) {
	start := time.Now()

	// Get source info for enhanced logging (ID + DisplayName) - do this outside mutex
	var displayName string
	if registry := GetRegistry(); registry != nil {
		if source, exists := registry.GetSourceByID(sourceID); exists {
			displayName = source.DisplayName
		}
	}
	if displayName == "" {
		displayName = sourceID // Default fallback
	}

	abMutex.Lock()
	defer abMutex.Unlock()

	// Get the ring buffer for the given source ID
	ab, exists := analysisBuffers[sourceID]
	if !exists {
		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferRead("analysis", sourceID, "error")
			m.RecordBufferReadError("analysis", sourceID, "buffer_not_found")
		}
		return nil, fmt.Errorf("%w: source ID %s (%s)", ErrBufferNotFound, sourceID, displayName)
	}

	// Calculate the number of bytes written to the buffer
	bytesWritten := ab.Length() - ab.Free()
	if bytesWritten < readSize {
		// Not enough data available - record metrics but return nil (not an error)
		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferRead("analysis", sourceID, "insufficient_data")
			m.RecordBufferUnderrun("analysis", sourceID)
		}
		return nil, nil
	}

	// Get a buffer from the pool instead of allocating new
	var data []byte
	if readBufferPool != nil {
		data = readBufferPool.Get()
	} else {
		// Fallback if pool not initialized
		data = make([]byte, readSize)
	}

	// Read data from the ring buffer
	bytesRead, err := ab.Read(data)
	if err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "read_from_analysis_buffer").
			Context("source_id", sourceID).
			Context("requested_bytes", readSize).
			Context("bytes_read", bytesRead).
			Context("buffer_length", ab.Length()).
			Context("buffer_free", ab.Free()).
			Build()

		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferRead("analysis", sourceID, "error")
			m.RecordBufferReadError("analysis", sourceID, "read_failed")
		}

		// Return buffer to pool on error
		if readBufferPool != nil {
			readBufferPool.Put(data)
		}
		return nil, enhancedErr
	}

	// Join with previous data to ensure we're processing chunkSize bytes
	var fullData []byte
	prevData[sourceID] = append(prevData[sourceID], data...)
	fullData = prevData[sourceID]

	// Return buffer to pool after copying data
	if readBufferPool != nil {
		readBufferPool.Put(data)
	}
	if len(fullData) >= conf.BufferSize {
		// Update prevData for the next iteration
		prevData[sourceID] = fullData[readSize:]
		fullData = fullData[:conf.BufferSize]

		// Record successful read metrics
		if m := getAnalysisMetrics(); m != nil {
			duration := time.Since(start).Seconds()
			m.RecordBufferRead("analysis", sourceID, "success")
			m.RecordBufferReadDuration("analysis", sourceID, duration)
			m.RecordBufferReadBytes("analysis", sourceID, len(fullData))
		}

		//log.Printf("✅ Read %d bytes from analysis buffer for source ID %s (%s)", len(fullData), sourceID, displayName)
		return fullData, nil
	} else {
		// If there isn't enough data even after appending, update prevData and return nil
		prevData[sourceID] = fullData

		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferRead("analysis", sourceID, "insufficient_data")
		}
		return nil, nil
	}
}

// AnalysisBufferExists checks if an analysis buffer exists for the given source
// Accepts either original source string or migrated source ID
// This is a thread-safe exported function that encapsulates access to the internal buffer map
func AnalysisBufferExists(sourceID string) bool {

	abMutex.RLock()
	defer abMutex.RUnlock()
	_, exists := analysisBuffers[sourceID]
	return exists
}

// AnalysisBufferMonitor monitors the buffer and processes audio data when enough data is present.
// Note: This function is called from within a wg.Go() goroutine, so WaitGroup tracking is handled by the caller.
func AnalysisBufferMonitor(_ *sync.WaitGroup, bn *birdnet.BirdNET, quitChan chan struct{}, sourceID string) {
	log := GetLogger()

	// This is the offset to subtract from the begin time of the data to account for BirdNET prediction and
	// processing delays, goal is to ensure that captured audio clip contains detection sound.
	const detectionOffset = 10 * time.Second

	// Creating a ticker that ticks every 100ms
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-quitChan:
			// Quit signal received, stop the buffer monitor
			return

		case <-ticker.C: // Wait for the next tick
			data, err := ReadFromAnalysisBuffer(sourceID)
			if err != nil {
				// If the buffer was removed (e.g., stream stopped), exit gracefully
				// instead of retrying forever and flooding logs
				if errors.Is(err, ErrBufferNotFound) {
					log.Info("analysis buffer removed, stopping monitor",
						logger.String("source_id", sourceID))
					return
				}

				log.Error("buffer read error",
					logger.String("source_id", sourceID),
					logger.Error(err))

				if m := getAnalysisMetrics(); m != nil {
					m.RecordAnalysisBufferPoll(sourceID, "error")
				}

				time.Sleep(1 * time.Second) // Wait for 1 second before trying again
				continue
			}

			// if buffer has 3 seconds of data, process it
			if len(data) == conf.BufferSize {
				if m := getAnalysisMetrics(); m != nil {
					m.RecordAnalysisBufferPoll(sourceID, "data_available")
				}

				// Calculate the offset dynamically to pick up runtime configuration changes
				// This includes the configured pre-capture duration plus an additional detection offset to
				// account for BirdNET prediction delay
				beginTimeOffset := time.Duration(conf.Setting().Realtime.Audio.Export.PreCapture)*time.Second + detectionOffset
				startTime := time.Now().Add(-beginTimeOffset)
				processingStart := time.Now()

				err := ProcessData(bn, data, startTime, sourceID)

				if m := getAnalysisMetrics(); m != nil {
					processingDuration := time.Since(processingStart).Seconds()
					m.RecordAnalysisBufferProcessingDuration(sourceID, processingDuration)
				}

				if err != nil {
					log.Error("error processing data",
						logger.String("source_id", sourceID),
						logger.Error(err))
				}
			} else if m := getAnalysisMetrics(); m != nil {
				m.RecordAnalysisBufferPoll(sourceID, "insufficient_data")
			}
		}
	}
}

// trackOverwrite records a write event and checks whether the overwrite rate
// exceeds the threshold over the current sliding window. If the threshold is
// exceeded and the cooldown period has elapsed, it fires a notification
// asynchronously.
func trackOverwrite(sourceID string, wasOverwrite bool) {
	overwriteTrackersMu.RLock()
	tracker, exists := overwriteTrackers[sourceID]
	overwriteTrackersMu.RUnlock()

	if !exists {
		return
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	now := time.Now()

	// Reset window if expired
	if now.Sub(tracker.windowStart) > overwriteWindowDuration {
		tracker.totalWrites = 0
		tracker.overwriteCount = 0
		tracker.windowStart = now
	}

	tracker.totalWrites++
	if wasOverwrite {
		tracker.overwriteCount++
	}

	// Check if threshold exceeded with sufficient sample size
	if tracker.totalWrites >= overwriteMinWrites {
		dropRate := float64(tracker.overwriteCount) / float64(tracker.totalWrites) * 100
		if dropRate >= overwriteRateThreshold && now.Sub(tracker.lastNotified) >= overwriteNotifyCooldown {
			tracker.lastNotified = now
			// Send notification outside lock via goroutine
			go sendOverloadNotification(sourceID, dropRate)
		}
	}
}

// sendOverloadNotification dispatches a warning notification indicating that
// the system is unable to keep up with audio analysis for the given source.
func sendOverloadNotification(sourceID string, dropRate float64) {
	if !notification.IsInitialized() {
		return
	}

	svc := notification.GetService()
	if svc == nil {
		return
	}

	// Get display name from registry
	displayName := sourceID
	if reg := GetRegistry(); reg != nil {
		if src, exists := reg.GetSourceByID(sourceID); exists {
			displayName = src.DisplayName
		}
	}

	title := fmt.Sprintf("Audio Analysis Overloaded — %s", displayName)
	message := fmt.Sprintf("Dropping %.0f%% of audio samples. Reduce BirdNET overlap or upgrade CPU.", dropRate)

	params := map[string]any{
		"dropRate": fmt.Sprintf("%.0f", dropRate),
	}

	notif := notification.NewNotification(notification.TypeWarning, notification.PriorityHigh, title, message).
		WithComponent("system").
		WithTitleKey(notification.MsgBufferOverloadTitle, params).
		WithMessageKey(notification.MsgBufferOverloadMessage, params)

	if err := svc.CreateWithMetadata(notif); err != nil {
		GetLogger().Warn("failed to create buffer overload notification",
			logger.String("source_id", sourceID),
			logger.Error(err))
	}
}
