// buffers.go: The file contains the implementation of the buffer monitor that reads audio data from the ring buffer and processes it when enough data is present.
package myaudio

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"errors"

	"github.com/smallnest/ringbuffer"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
)

const (
	pollInterval             = time.Millisecond * 10
	maxRetries               = 3
	retryDelay               = time.Millisecond * 10
	warningCapacityThreshold = 0.9 // 90% full
)

var (
	overlapSize     int                               // overlapSize is the number of bytes to overlap between chunks
	readSize        int                               // readSize is the number of bytes to read from the ring buffer
	analysisBuffers map[string]*ringbuffer.RingBuffer // analysisBuffers is a map to store ring buffers for each audio source
	prevData        map[string][]byte                 // prevData is a map to store the previous data for each audio source
	abMutex         sync.RWMutex                      // Mutex to protect access to the analysisBuffers and prevData maps
	warningCounter  map[string]int
)

// init initializes the warningCounter map
func init() {
	warningCounter = make(map[string]int)
}

// SecondsToBytes converts overlap in seconds to bytes
func SecondsToBytes(seconds float64) int {
	return int(seconds * float64(conf.SampleRate) * float64(conf.BitDepth/8))
}

// AllocateAnalysisBuffer initializes a ring buffer for a single audio source.
// It returns an error if memory allocation fails or if the input is invalid.
func AllocateAnalysisBuffer(capacity int, source string) error {
	// Validate inputs
	if capacity <= 0 {
		return fmt.Errorf("invalid capacity: %d, must be greater than 0", capacity)
	}
	if source == "" {
		return fmt.Errorf("empty source name provided")
	}

	settings := conf.Setting()

	// Set overlapSize based on user setting in seconds if not already set
	if overlapSize == 0 {
		overlapSize = SecondsToBytes(settings.BirdNET.Overlap)
		readSize = conf.BufferSize - overlapSize
	}

	// Initialize the analysis ring buffer
	ab := ringbuffer.New(capacity)
	if ab == nil {
		return fmt.Errorf("failed to allocate ring buffer for source: %s", source)
	}
	// Update global variables safely
	abMutex.Lock()
	defer abMutex.Unlock()

	// Check if buffer already exists
	if _, exists := analysisBuffers[source]; exists {
		ab.Reset() // Clean up the new buffer since we won't use it
		return fmt.Errorf("ring buffer already exists for source: %s", source)
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

	analysisBuffers[source] = ab
	prevData[source] = nil
	warningCounter[source] = 0

	// Log the buffer creation for debugging
	//log.Printf("✅ Created analysis buffer for %s with capacity %d bytes", source, ab.Capacity())

	return nil
}

// RemoveAnalysisBuffer safely removes and cleans up a ring buffer for a single source.
func RemoveAnalysisBuffer(source string) error {
	abMutex.Lock()
	defer abMutex.Unlock()

	ab, exists := analysisBuffers[source]
	if !exists {
		return fmt.Errorf("no ring buffer found for source: %s", source)
	}

	// Clean up the buffer
	ab.Reset()

	// Remove from all maps
	delete(analysisBuffers, source)
	delete(prevData, source)
	delete(warningCounter, source)

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
	abMutex.RLock()
	ab, exists := analysisBuffers[stream]
	abMutex.RUnlock()

	if !exists {
		return fmt.Errorf("no analysis buffer found for stream: %s", stream)
	}

	// Get buffer capacity information
	capacity := ab.Capacity()
	if capacity == 0 {
		return fmt.Errorf("analysis buffer for stream %s has zero capacity", stream)
	}

	// Check buffer capacity
	capacityUsed := float64(ab.Length()) / float64(capacity)
	if capacityUsed > warningCapacityThreshold {
		warningCounter[stream]++
		if warningCounter[stream]%32 == 1 {
			log.Printf("⚠️ Analysis buffer for stream %s is %.2f%% full (used: %d/%d bytes)",
				stream, capacityUsed*100, ab.Length(), capacity)
		}
	}

	// Write data to the ring buffer
	for retry := 0; retry < maxRetries; retry++ {
		abMutex.Lock()           // Lock the mutex to prevent other goroutines from reading or writing to the buffer
		n, err := ab.Write(data) // Write data to the ring buffer
		abMutex.Unlock()         // Unlock the mutex

		if err == nil {
			if n < len(data) {
				log.Printf("⚠️ Only wrote %d of %d bytes to buffer for stream %s (capacity: %d, free: %d)",
					n, len(data), stream, capacity, ab.Free())
			}

			return nil
		}

		// Log detailed buffer state
		log.Printf("⚠️ Analysis buffer for stream %s has %d/%d bytes free (%d bytes used), tried to write %d bytes",
			stream, ab.Free(), capacity, ab.Length(), len(data))

		if errors.Is(err, ringbuffer.ErrIsFull) {
			log.Printf("⚠️ Analysis buffer for stream %s is full. Waiting before retry %d/%d", stream, retry+1, maxRetries)
		} else {
			log.Printf("❌ Unexpected error writing to analysis buffer for stream %s: %v", stream, err)
		}

		// Capture system resource utilization
		diagnostics.CaptureSystemInfo(fmt.Sprintf("Buffer write error for stream %s", stream))

		if retry < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	// If we've reached this point, we've failed all retries
	log.Printf("❌ Failed to write to analysis buffer for stream %s after %d attempts. Dropping %d bytes of PCM data. Buffer state: capacity=%d, used=%d, free=%d",
		stream, maxRetries, len(data), capacity, ab.Length(), ab.Free())

	return fmt.Errorf("failed to write to analysis buffer for stream %s after %d attempts", stream, maxRetries)
}

// ReadFromAnalysisBuffer reads a sliding chunk of audio data from the ring buffer for a given stream.
func ReadFromAnalysisBuffer(stream string) ([]byte, error) {
	abMutex.Lock()
	defer abMutex.Unlock()

	// Get the ring buffer for the given stream
	ab, exists := analysisBuffers[stream]
	if !exists {
		return nil, fmt.Errorf("no analysis buffer found for stream: %s", stream)
	}

	// Calculate the number of bytes written to the buffer
	bytesWritten := ab.Length() - ab.Free()
	if bytesWritten < readSize {
		return nil, nil
	}

	// Create a slice to hold the data we're going to read
	data := make([]byte, readSize)
	// Read data from the ring buffer
	bytesRead, err := ab.Read(data)
	if err != nil {
		return nil, fmt.Errorf("error reading %d bytes from analysis buffer for stream: %s", bytesRead, stream)
	}

	// Join with previous data to ensure we're processing chunkSize bytes
	var fullData []byte
	prevData[stream] = append(prevData[stream], data...)
	fullData = prevData[stream]
	if len(fullData) >= conf.BufferSize {
		// Update prevData for the next iteration
		prevData[stream] = fullData[readSize:]
		fullData = fullData[:conf.BufferSize]
	} else {
		// If there isn't enough data even after appending, update prevData and return nil
		prevData[stream] = fullData
		return nil, nil

	}

	//log.Printf("✅ Read %d bytes from analysis buffer for stream %s", len(fullData), stream)
	return fullData, nil
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
				time.Sleep(1 * time.Second) // Wait for 1 second before trying again
				continue
			}
			// if buffer has 3 seconds of data, process it
			if len(data) == conf.BufferSize {

				/*if err := validatePCMData(data); err != nil {
					log.Printf("Invalid PCM data for source %s: %v", source, err)
					continue
				}*/

				startTime := time.Now().Add(preRecordingTime)
				// DEBUG
				//log.Printf("Processing data for source %s", source)
				err := ProcessData(bn, data, startTime, source)
				if err != nil {
					log.Printf("❌ Error processing data for source %s: %v", source, err)
				}
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
