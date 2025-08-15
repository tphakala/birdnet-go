// buffers.go: The file contains the implementation of the buffer monitor that reads audio data from the ring buffer and processes it when enough data is present.
package myaudio

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/ringbuffer"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	pollInterval             = time.Millisecond * 10
	maxRetries               = 3
	retryDelay               = time.Millisecond * 10
	warningCapacityThreshold = 0.9 // 90% full
)

var (
	overlapSize         int                               // overlapSize is the number of bytes to overlap between chunks
	readSize            int                               // readSize is the number of bytes to read from the ring buffer
	analysisBuffers     map[string]*ringbuffer.RingBuffer // analysisBuffers is a map to store ring buffers for each audio source
	prevData            map[string][]byte                 // prevData is a map to store the previous data for each audio source
	abMutex             sync.RWMutex                      // Mutex to protect access to the analysisBuffers and prevData maps
	warningCounter      map[string]int
	analysisMetrics     *metrics.MyAudioMetrics // Global metrics instance for analysis buffer operations
	analysisMetricsMutex sync.RWMutex            // Mutex for thread-safe access to analysisMetrics
	analysisMetricsOnce  sync.Once               // Ensures metrics are only set once
	readBufferPool      *BufferPool             // Global buffer pool for read operations
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

// AllocateAnalysisBuffer initializes a ring buffer for a single audio source.
// It returns an error if memory allocation fails or if the input is invalid.
func AllocateAnalysisBuffer(capacity int, source string) error {
	// Auto-migrate to get the actual source ID
	sourceID := MigrateExistingSourceToID(source)
	
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
	if source == "" {
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

	// Set overlapSize based on user setting in seconds if not already set
	if overlapSize == 0 {
		overlapSize = SecondsToBytes(settings.BirdNET.Overlap)
		readSize = conf.BufferSize - overlapSize
		
		// Initialize the read buffer pool if not already done
		if readBufferPool == nil {
			var err error
			readBufferPool, err = NewBufferPool(readSize)
			if err != nil {
				enhancedErr := errors.New(err).
					Component("myaudio").
					Category(errors.CategorySystem).
					Context("operation", "allocate_analysis_buffer").
					Context("source", sourceID).
					Context("buffer_pool_size", readSize).
					Build()
				return enhancedErr
			}
		}
	}

	// Initialize the analysis ring buffer
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
	
	// Acquire reference to this source using the migrated ID
	registry := GetRegistry()
	registry.AcquireSourceReference(sourceID)

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

// RemoveAnalysisBuffer safely removes and cleans up a ring buffer for a single source.
func RemoveAnalysisBuffer(source string) error {
	// Auto-migrate to get the actual source ID
	sourceID := MigrateExistingSourceToID(source)
	
	abMutex.Lock()
	ab, exists := analysisBuffers[sourceID]
	if !exists {
		abMutex.Unlock()
		return fmt.Errorf("no ring buffer found for source: %s (mapped to: %s)", source, sourceID)
	}

	// Clean up the buffer
	ab.Reset()

	// Remove from all maps
	delete(analysisBuffers, sourceID)
	delete(prevData, sourceID)
	delete(warningCounter, sourceID)
	
	// Clean up buffer pool if this was the last buffer (prevents memory leak)
	if len(analysisBuffers) == 0 && readBufferPool != nil {
		// Note: BufferPool doesn't have a Close method currently, but we can at least nil it
		readBufferPool = nil
		overlapSize = 0
		readSize = 0
	}
	abMutex.Unlock() // Release lock before calling registry
	
	// Release reference to this source - registry will auto-remove if count reaches zero
	registry := GetRegistry()
	if err := registry.ReleaseSourceReference(sourceID); err != nil {
		// Log but don't fail - buffer removal succeeded
		if !errors.Is(err, ErrSourceNotFound) {
			log.Printf("⚠️ Failed to release source reference: %v", err)
		}
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

// WriteToAnalysisBuffer writes audio data into the ring buffer for a given stream.
func WriteToAnalysisBuffer(stream string, data []byte) error {
	// Auto-migrate legacy source identifiers to registry IDs
	sourceID := MigrateExistingSourceToID(stream)
	
	start := time.Now()

	abMutex.RLock()
	ab, exists := analysisBuffers[sourceID]
	abMutex.RUnlock()

	if !exists {
		enhancedErr := errors.Newf("no analysis buffer found for stream: %s (mapped to: %s)", stream, sourceID).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "write_to_analysis_buffer").
			Context("stream", stream).
			Context("data_size", len(data)).
			Build()

		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferWrite("analysis", sourceID, "error")
			m.RecordBufferWriteError("analysis", sourceID, "buffer_not_found")
		}
		return enhancedErr
	}

	// Get buffer capacity information
	capacity := ab.Capacity()
	if capacity == 0 {
		enhancedErr := errors.Newf("analysis buffer for stream %s has zero capacity", stream).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "write_to_analysis_buffer").
			Context("stream", stream).
			Context("data_size", len(data)).
			Build()

		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferWrite("analysis", sourceID, "error")
			m.RecordBufferWriteError("analysis", sourceID, "zero_capacity")
		}
		return enhancedErr
	}

	// Check buffer capacity and update metrics
	currentLength := ab.Length()
	capacityUsed := float64(currentLength) / float64(capacity)

	if m := getAnalysisMetrics(); m != nil {
		m.UpdateBufferUtilization("analysis", sourceID, capacityUsed)
		m.UpdateBufferSize("analysis", sourceID, currentLength)
	}

	if capacityUsed > warningCapacityThreshold {
		warningCounter[sourceID]++
		if warningCounter[sourceID]%32 == 1 {
			log.Printf("⚠️ Analysis buffer for stream %s is %.2f%% full (used: %d/%d bytes)",
				stream, capacityUsed*100, currentLength, capacity)
		}

		if m := getAnalysisMetrics(); m != nil && capacityUsed > 0.95 {
			m.RecordBufferOverflow("analysis", sourceID)
		}
	}

	// Write data to the ring buffer with retry logic
	var lastErr error
	var n int
	for retry := 0; retry < maxRetries; retry++ {
		// Use anonymous function with defer to ensure mutex is always unlocked
		// This prevents deadlocks even if ab.Write panics
		err := func() error {
			abMutex.Lock()
			defer abMutex.Unlock() // Always unlock, even on panic
			
			var writeErr error
			n, writeErr = ab.Write(data) // Write data to the ring buffer
			return writeErr
		}()

		if err == nil {
			// Record successful write metrics
			if m := getAnalysisMetrics(); m != nil {
				duration := time.Since(start).Seconds()
				m.RecordBufferWrite("analysis", sourceID, "success")
				m.RecordBufferWriteDuration("analysis", sourceID, duration)
				m.RecordBufferWriteBytes("analysis", sourceID, n)
			}

			if n < len(data) {
				// Partial write - log and record metrics
				log.Printf("⚠️ Only wrote %d of %d bytes to buffer for stream %s (capacity: %d, free: %d)",
					n, len(data), stream, capacity, ab.Free())

				if m := getAnalysisMetrics(); m != nil {
					m.RecordBufferWrite("analysis", sourceID, "partial")
				}
			}

			return nil
		}

		lastErr = err

		// Log detailed buffer state
		log.Printf("⚠️ Analysis buffer for stream %s has %d/%d bytes free (%d bytes used), tried to write %d bytes",
			stream, ab.Free(), capacity, ab.Length(), len(data))

		// Record retry metrics
		if m := getAnalysisMetrics(); m != nil {
			if errors.Is(err, ringbuffer.ErrIsFull) {
				m.RecordBufferWriteRetry("analysis", sourceID, "buffer_full")
			} else {
				m.RecordBufferWriteRetry("analysis", sourceID, "unexpected_error")
			}
		}

		if errors.Is(err, ringbuffer.ErrIsFull) {
			log.Printf("⚠️ Analysis buffer for stream %s is full. Waiting before retry %d/%d", stream, retry+1, maxRetries)
		} else {
			log.Printf("❌ Unexpected error writing to analysis buffer for stream %s: %v", stream, err)
		}

		// System resource utilization capture disabled to prevent disk space issues

		if retry < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	// If we've reached this point, we've failed all retries
	log.Printf("❌ Failed to write to analysis buffer for stream %s after %d attempts. Dropping %d bytes of PCM data. Buffer state: capacity=%d, used=%d, free=%d",
		stream, maxRetries, len(data), capacity, ab.Length(), ab.Free())

	// Record data drop metrics
	if m := getAnalysisMetrics(); m != nil {
		m.RecordBufferWrite("analysis", sourceID, "error")
		m.RecordBufferWriteError("analysis", sourceID, "retry_exhausted")
		m.RecordAnalysisBufferDataDrop(sourceID, "retry_exhausted")
	}

	enhancedErr := errors.New(lastErr).
		Component("myaudio").
		Category(errors.CategorySystem).
		Context("operation", "write_to_analysis_buffer").
		Context("stream", stream).
		Context("data_size", len(data)).
		Context("capacity", capacity).
		Context("used_bytes", ab.Length()).
		Context("free_bytes", ab.Free()).
		Context("max_retries", maxRetries).
		Context("capacity_utilization", capacityUsed).
		Build()

	return enhancedErr
}

// ReadFromAnalysisBuffer reads a sliding chunk of audio data from the ring buffer for a given stream.
func ReadFromAnalysisBuffer(stream string) ([]byte, error) {
	start := time.Now()
	
	// Auto-migrate legacy source identifiers to registry IDs
	// Do this BEFORE acquiring the lock to avoid potential deadlocks
	sourceID := MigrateExistingSourceToID(stream)

	abMutex.Lock()
	defer abMutex.Unlock()

	// Get the ring buffer for the given stream using the migrated ID
	ab, exists := analysisBuffers[sourceID]
	if !exists {
		enhancedErr := errors.Newf("no analysis buffer found for stream: %s (mapped to: %s)", stream, sourceID).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "read_from_analysis_buffer").
			Context("stream", stream).
			Build()

		if m := getAnalysisMetrics(); m != nil {
			m.RecordBufferRead("analysis", sourceID, "error")
			m.RecordBufferReadError("analysis", sourceID, "buffer_not_found")
		}
		return nil, enhancedErr
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
			Context("stream", stream).
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

		//log.Printf("✅ Read %d bytes from analysis buffer for stream %s", len(fullData), stream)
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
func AnalysisBufferExists(source string) bool {
	// Auto-migrate to get the actual source ID
	sourceID := MigrateExistingSourceToID(source)
	
	abMutex.RLock()
	defer abMutex.RUnlock()
	_, exists := analysisBuffers[sourceID]
	return exists
}

// AnalysisBufferMonitor monitors the buffer and processes audio data when enough data is present.
func AnalysisBufferMonitor(wg *sync.WaitGroup, bn *birdnet.BirdNET, quitChan chan struct{}, source string) {
	// preRecordingTime is the time to subtract from the current time to get the start time of the detection
	const preRecordingTime = -5000 * time.Millisecond

	wg.Add(1)
	defer func() {
		wg.Done()
	}()

	// Creating a ticker that ticks every 100ms
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-quitChan:
			// Quit signal received, stop the buffer monitor
			return

		case <-ticker.C: // Wait for the next tick
			data, err := ReadFromAnalysisBuffer(source)
			if err != nil {
				log.Printf("❌ Buffer read error: %v", err)

				if m := getAnalysisMetrics(); m != nil {
					m.RecordAnalysisBufferPoll(source, "error")
				}

				time.Sleep(1 * time.Second) // Wait for 1 second before trying again
				continue
			}

			// if buffer has 3 seconds of data, process it
			if len(data) == conf.BufferSize {
				if m := getAnalysisMetrics(); m != nil {
					m.RecordAnalysisBufferPoll(source, "data_available")
				}

				/*if err := validatePCMData(data); err != nil {
					log.Printf("Invalid PCM data for source %s: %v", source, err)
					if m := getAnalysisMetrics(); m != nil {
						m.RecordAudioDataValidationError(source, "pcm_validation")
					}
					continue
				}*/

				startTime := time.Now().Add(preRecordingTime)
				processingStart := time.Now()

				// DEBUG
				//log.Printf("Processing data for source %s", source)
				err := ProcessData(bn, data, startTime, source)

				if m := getAnalysisMetrics(); m != nil {
					processingDuration := time.Since(processingStart).Seconds()
					m.RecordAnalysisBufferProcessingDuration(source, processingDuration)
				}

				if err != nil {
					log.Printf("❌ Error processing data for source %s: %v", source, err)
				}
			} else if m := getAnalysisMetrics(); m != nil {
				m.RecordAnalysisBufferPoll(source, "insufficient_data")
			}
		}
	}
}

/*func validatePCMData(data []byte) error {
	// Check if the data size is a multiple of the sample size (e.g., 2 bytes for 16-bit audio)
	if len(data)%2 != 0 {
		return fmt.Errorf("invalid PCM data size: %d", len(data))
	}

	// Expected length for 3 seconds of audio data
	expectedLength := 48000 * 2 * 3 // 48000 samples/sec * 2 bytes/sample * 3 seconds
	if len(data) != expectedLength {
		return fmt.Errorf("unexpected PCM data length: %d (expected %d)", len(data), expectedLength)
	}

	// Check for valid 16-bit signed integer ranges
	for i := 0; i < len(data); i += 2 {
		sample := int16(data[i]) | int16(data[i+1])<<8
		if sample < -32768 || sample > 32767 {
			return fmt.Errorf("invalid PCM data value at index %d: %d", i, sample)
		}
	}

	// Optional: Check for excessive silence (if all values are zero)
	silenceThreshold := 0.95 // Threshold for detecting silence, adjust as needed
	silenceCount := 0
	totalSamples := len(data) / 2

	for i := 0; i < len(data); i += 2 {
		sample := int16(data[i]) | int16(data[i+1])<<8
		if sample == 0 {
			silenceCount++
		}
	}

	if float64(silenceCount)/float64(totalSamples) > silenceThreshold {
		return fmt.Errorf("excessive silence detected in PCM data")
	}

	return nil
}*/
