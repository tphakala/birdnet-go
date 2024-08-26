// buffers.go: The file contains the implementation of the buffer monitor that reads audio data from the ring buffer and processes it when enough data is present.
package myaudio

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	"github.com/smallnest/ringbuffer"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	chunkSize    = 288000 // 3 seconds of 16-bit PCM data at 48 kHz
	pollInterval = time.Millisecond * 10
)

var (
	overlapSize int                               // overlapSize is the number of bytes to overlap between chunks
	readSize    int                               // readSize is the number of bytes to read from the ring buffer
	ringBuffers map[string]*ringbuffer.RingBuffer // ringBuffers is a map to store ring buffers for each audio source
	prevData    map[string][]byte                 // prevData is a map to store the previous data for each audio source
	rbMutex     sync.RWMutex                      // Mutex to protect access to the ringBuffers and prevData maps
)

// ConvertSecondsToBytes converts overlap in seconds to bytes
func ConvertSecondsToBytes(seconds float64) int {
	const sampleRate = 48000 // 48 kHz
	const bytesPerSample = 2 // 16-bit PCM data (2 bytes per sample)
	return int(seconds * sampleRate * bytesPerSample)
}

// InitRingBuffers initializes the ring buffers for each audio source with a given capacity.
func InitRingBuffers(capacity int, sources []string) {
	//rbMutex.Lock()
	//defer rbMutex.Unlock()

	settings := conf.Setting()

	// Set overlapSize based on user setting in seconds
	overlapSize = ConvertSecondsToBytes(settings.BirdNET.Overlap)
	readSize = chunkSize - overlapSize

	// Initialize ring buffers and prevData map for each source
	ringBuffers = make(map[string]*ringbuffer.RingBuffer)
	prevData = make(map[string][]byte)
	for _, source := range sources {
		ringBuffers[source] = ringbuffer.New(capacity)
		prevData[source] = nil
	}
}

// WriteToBuffer writes audio data into the ring buffer for a given stream.
func WriteToAnalysisBuffer(stream string, data []byte) {
	rbMutex.RLock()
	rb, exists := ringBuffers[stream]
	rbMutex.RUnlock()

	if !exists {
		log.Printf("No ring buffer found for stream: %s", stream)
		return
	}

	// Write data to the ring buffer
	_, err := rb.Write(data)
	if err != nil {
		// Capture system resource utilization
		debugInfo := captureSystemInfo()
		log.Printf("Error writing to ring buffer for stream %s: %v\n%s", stream, err, debugInfo)
	}
}

// readFromBuffer reads a sliding chunk of audio data from the ring buffer for a given stream.
func readFromBuffer(stream string) []byte {
	rbMutex.Lock()
	defer rbMutex.Unlock()

	rb, exists := ringBuffers[stream]
	if !exists {
		log.Printf("No ring buffer found for stream: %s", stream)
		return nil
	}

	bytesWritten := rb.Length() - rb.Free()
	if bytesWritten < readSize {
		return nil
	}

	data := make([]byte, readSize)
	_, err := rb.Read(data)
	if err != nil {
		return nil
	}

	// Join with previous data to ensure we're processing chunkSize bytes
	fullData := append(prevData[stream], data...)
	if len(fullData) > chunkSize {
		// Update prevData for the next iteration
		prevData[stream] = fullData[readSize:]
		fullData = fullData[:chunkSize]
	} else {
		// If there isn't enough data even after appending, update prevData and return nil
		prevData[stream] = fullData
		return nil
	}

	return fullData
}

// BufferMonitor monitors the buffer and processes audio data when enough data is present.
func BufferMonitor(wg *sync.WaitGroup, bn *birdnet.BirdNET, quitChan chan struct{}, source string) {
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
			if len(data) == chunkSize {

				/*if err := validatePCMData(data); err != nil {
					log.Printf("Invalid PCM data for source %s: %v", source, err)
					continue
				}*/

				startTime := time.Now().Add(-4500 * time.Millisecond)
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

// captureSystemInfo captures system information and returns it as a string
func captureSystemInfo() string {
	var info strings.Builder

	// Add a clear separator at the beginning
	separator := "======== DEBUG INFO START ========"
	info.WriteString(fmt.Sprintf("%s\n", separator))

	// CPU Utilization
	cpuPercent, err := cpu.Percent(time.Second, false)
	if err == nil {
		info.WriteString(fmt.Sprintf("CPU Utilization: %.2f%%\n", cpuPercent[0]))
	}

	// RAM Usage
	vmStat, err := mem.VirtualMemory()
	if err == nil {
		info.WriteString(fmt.Sprintf("RAM Usage: %.2f%%\n", vmStat.UsedPercent))
	}

	// Page File Usage (Swap)
	swapStat, err := mem.SwapMemory()
	if err == nil {
		info.WriteString(fmt.Sprintf("Page File Usage: %.2f%%\n", swapStat.UsedPercent))
	}

	// Run 'ps axuw' command
	cmd := exec.Command("ps", "axuww")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Error running 'ps axuw': %v", err)
	} else {
		info.WriteString("\nProcess List (ps axuw):\n")
		info.Write(output)
	}

	// Go runtime statistics
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	info.WriteString(fmt.Sprintf("Go Runtime: Alloc = %v MiB, TotalAlloc = %v MiB, Sys = %v MiB, NumGC = %v\n",
		bToMb(m.Alloc), bToMb(m.TotalAlloc), bToMb(m.Sys), m.NumGC))

	// Add a clear separator at the end
	info.WriteString(fmt.Sprintf("%s\n", strings.ReplaceAll(separator, "START", "END")))

	return info.String()
}

// bToMb converts bytes to megabytes
func bToMb(b uint64) uint64 {
	return b / 1024 / 1024
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
