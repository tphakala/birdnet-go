// buffers.go: The file contains the implementation of the buffer monitor that reads audio data from the ring buffer and processes it when enough data is present.
package myaudio

import (
	"fmt"
	"log"
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
	overlapSize    int                               // overlapSize is the number of bytes to overlap between chunks
	readSize       int                               // readSize is the number of bytes to read from the ring buffer
	ringBuffers    map[string]*ringbuffer.RingBuffer // ringBuffers is a map to store ring buffers for each audio source
	prevData       map[string][]byte                 // prevData is a map to store the previous data for each audio source
	rbMutex        sync.RWMutex                      // Mutex to protect access to the ringBuffers and prevData maps
	warningCounter map[string]int
)

// init initializes the warningCounter map
func init() {
	warningCounter = make(map[string]int)
}

// SecondsToBytes converts overlap in seconds to bytes
func SecondsToBytes(seconds float64) int {
	return int(seconds * float64(conf.SampleRate) * float64(conf.BitDepth/8))
}

// InitRingBuffers initializes the ring buffers for each audio source with a given capacity.
func InitRingBuffers(capacity int, sources []string) {
	settings := conf.Setting()

	// Set overlapSize based on user setting in seconds
	overlapSize = SecondsToBytes(settings.BirdNET.Overlap)
	readSize = conf.BufferSize - overlapSize

	// Initialize ring buffers and prevData map for each source
	ringBuffers = make(map[string]*ringbuffer.RingBuffer)
	prevData = make(map[string][]byte)
	for _, source := range sources {
		ringBuffers[source] = ringbuffer.New(capacity)
		prevData[source] = nil
	}
}

// WriteToAnalysisBuffer writes audio data into the ring buffer for a given stream.
func WriteToAnalysisBuffer(stream string, data []byte) {
	rbMutex.RLock()
	rb, exists := ringBuffers[stream]
	rbMutex.RUnlock()

	if !exists {
		log.Printf("No ring buffer found for stream: %s", stream)
		return
	}

	// Check buffer capacity
	capacityUsed := float64(rb.Length()) / float64(rb.Capacity())
	if capacityUsed > warningCapacityThreshold {
		warningCounter[stream]++
		if warningCounter[stream]%32 == 1 {
			log.Printf("Warning: Buffer for stream %s is %.2f%% full", stream, capacityUsed*100)
		}
	}

	// Write data to the ring buffer
	for retry := 0; retry < maxRetries; retry++ {
		n, err := rb.Write(data)
		if err == nil {
			if n < len(data) {
				log.Printf("Warning: Only wrote %d of %d bytes to buffer for stream %s", n, len(data), stream)
			}
			return
		}
		// print available free space in the buffer and amount we tried to write
		log.Printf("Buffer for stream %s has %d bytes free, tried to write %d bytes", stream, rb.Free(), len(data))

		if errors.Is(err, ringbuffer.ErrIsFull) {
			log.Printf("Buffer for stream %s is full. Waiting before retry %d/%d", stream, retry+1, maxRetries)
		} else {
			log.Printf("Unexpected error writing to buffer for stream %s: %v", stream, err)
		}

		// Capture system resource utilization
		diagnostics.CaptureSystemInfo(fmt.Sprintf("Buffer write error for stream %s", stream))

		if retry < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	// If we've reached this point, we've failed all retries
	log.Printf("Failed to write to ring buffer for stream %s after %d attempts. Dropping %d bytes of PCM data.", stream, maxRetries, len(data))
}

// readFromBuffer reads a sliding chunk of audio data from the ring buffer for a given stream.
func readFromBuffer(stream string) []byte {
	rbMutex.Lock()
	defer rbMutex.Unlock()

	// Get the ring buffer for the given stream
	rb, exists := ringBuffers[stream]
	if !exists {
		// If the ring buffer doesn't exist, log an error and return nil
		log.Printf("No ring buffer found for stream: %s", stream)
		return nil
	}

	// Calculate the number of bytes written to the buffer
	bytesWritten := rb.Length() - rb.Free()
	if bytesWritten < readSize {
		// If there's not enough data to read, return nil
		return nil
	}

	// Create a slice to hold the data we're going to read
	data := make([]byte, readSize)
	// Read data from the ring buffer
	bytesRead, err := rb.Read(data)
	if err != nil {
		// If there is an error reading from the buffer, log error and return nil
		log.Printf("Error reading from ring buffer for stream %s, got %d bytes: %v", stream, bytesRead, err)
		return nil
	}

	// Join with previous data to ensure we're processing chunkSize bytes
	fullData := append(prevData[stream], data...)
	if len(fullData) > conf.BufferSize {
		// Update prevData for the next iteration
		prevData[stream] = fullData[readSize:]
		fullData = fullData[:conf.BufferSize]
	} else {
		// If there isn't enough data even after appending, update prevData and return nil
		prevData[stream] = fullData
		return nil
	}

	return fullData
}

// BufferMonitor monitors the buffer and processes audio data when enough data is present.
func BufferMonitor(wg *sync.WaitGroup, bn *birdnet.BirdNET, quitChan chan struct{}, source string) {
	// preRecordingTime is the time to subtract from the current time to get the start time of the detection
	const preRecordingTime = -5000 * time.Millisecond

	defer wg.Done()

	// Creating a ticker that ticks every 100ms
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-quitChan:
			// Quit signal received, stop the buffer monitor
			return

		case <-ticker.C: // Wait for the next tick
			data := readFromBuffer(source)
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
					log.Printf("Error processing data for source %s: %v", source, err)
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
