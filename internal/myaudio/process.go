// process.go
package myaudio

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

var (
	processMetrics      *metrics.MyAudioMetrics // Global metrics instance for audio processing operations
	processMetricsMutex sync.RWMutex            // Mutex for thread-safe access to processMetrics
	processMetricsOnce  sync.Once               // Ensures metrics are only set once
	float32Pool         *Float32Pool            // Global pool for float32 conversion buffers
)

const (
	// Float32BufferSize is the number of float32 samples in a standard buffer
	// For 16-bit audio: conf.BufferSize / 2 (bytes per sample) = 144384 samples
	Float32BufferSize = conf.BufferSize / 2
)

// SetProcessMetrics sets the metrics instance for audio processing operations.
// This function is thread-safe and ensures metrics are only set once.
func SetProcessMetrics(myAudioMetrics *metrics.MyAudioMetrics) {
	processMetricsOnce.Do(func() {
		processMetricsMutex.Lock()
		defer processMetricsMutex.Unlock()
		processMetrics = myAudioMetrics
	})
}

// getProcessMetrics returns the current metrics instance in a thread-safe manner
func getProcessMetrics() *metrics.MyAudioMetrics {
	processMetricsMutex.RLock()
	defer processMetricsMutex.RUnlock()
	return processMetrics
}

// InitFloat32Pool initializes the global float32 pool for audio conversion.
// This should be called during application startup.
func InitFloat32Pool() error {
	var err error
	float32Pool, err = NewFloat32Pool(Float32BufferSize)
	if err != nil {
		return fmt.Errorf("failed to initialize float32 pool: %w", err)
	}
	
	return nil
}

// ReturnFloat32Buffer returns a float32 buffer to the pool if possible.
// This should be called after the buffer is no longer needed.
func ReturnFloat32Buffer(buffer []float32) {
	if float32Pool != nil && len(buffer) == Float32BufferSize {
		float32Pool.Put(buffer)
	}
}

// processData processes the given audio data to detect bird species, logs the detected species
// and optionally saves the audio clip if a bird species is detected above the configured threshold.
func ProcessData(bn *birdnet.BirdNET, data []byte, startTime time.Time, source string) error {
	// get current time to track processing time
	predictStart := time.Now()

	// convert audio data to float32
	sampleData, err := ConvertToFloat32(data, conf.BitDepth)
	if err != nil {
		return fmt.Errorf("error converting %v bit PCM data to float32: %w", conf.BitDepth, err)
	}

	// run BirdNET inference
	results, err := bn.Predict(sampleData)
	
	// Return float32 buffer to pool after prediction
	// This is safe because Predict copies the data to the input tensor
	if conf.BitDepth == 16 && len(sampleData) > 0 && len(sampleData[0]) == Float32BufferSize {
		ReturnFloat32Buffer(sampleData[0])
	}
	
	if err != nil {
		return fmt.Errorf("error predicting species: %w", err)
	}

	// get elapsed time
	elapsedTime := time.Since(predictStart)

	// DEBUG print all BirdNET results
	if conf.Setting().BirdNET.Debug {
		debugThreshold := float32(0) // set to 0 for now, maybe add a config option later
		hasHighConfidenceResults := false
		for _, result := range results {
			if result.Confidence > debugThreshold {
				hasHighConfidenceResults = true
				break
			}
		}

		if hasHighConfidenceResults {
			log.Println("[birdnet] results:")
			for _, result := range results {
				if result.Confidence > debugThreshold {
					log.Printf("[birdnet] %.2f %s\n", result.Confidence, result.Species)
				}
			}
		}
	}

	// Get the current settings
	settings := conf.Setting()

	// Calculate the effective buffer duration
	bufferDuration := 3 * time.Second // base duration
	overlapDuration := time.Duration(settings.BirdNET.Overlap * float64(time.Second))
	effectiveBufferDuration := bufferDuration - overlapDuration

	// Check if processing time exceeds effective buffer duration
	if elapsedTime > effectiveBufferDuration {
		log.Printf("WARNING: BirdNET processing time (%v) exceeded buffer length (%v) for source %s",
			elapsedTime, effectiveBufferDuration, source)
	}

	// Create a Results message to be sent through queue to processor
	resultsMessage := birdnet.Results{
		StartTime:   startTime,
		ElapsedTime: elapsedTime,
		PCMdata:     data,
		Results:     results,
		Source:      source,
	}

	// Send the results to the queue
	// Note: No copy needed - ownership transfers to the queue consumer
	select {
	case birdnet.ResultsQueue <- resultsMessage:
		// Results enqueued successfully
	default:
		log.Println("❌ Results queue is full!")
		// Queue is full
	}
	return nil
}

// ConvertToFloat32 converts a byte slice representing sample to a 2D slice of float32 samples.
// The function supports 16, 24, and 32 bit depths.
func ConvertToFloat32(sample []byte, bitDepth int) ([][]float32, error) {
	switch bitDepth {
	case 16:
		return [][]float32{convert16BitToFloat32(sample)}, nil
	case 24:
		return [][]float32{convert24BitToFloat32(sample)}, nil
	case 32:
		return [][]float32{convert32BitToFloat32(sample)}, nil
	default:
		return nil, errors.Newf("unsupported audio bit depth: %d", bitDepth).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "convert_to_float32").
			Context("bit_depth", bitDepth).
			Context("supported_bit_depths", "16,24,32").
			Build()
	}
}

// convert16BitToFloat32 converts 16-bit sample to float32 values.
func convert16BitToFloat32(sample []byte) []float32 {
	length := len(sample) / 2
	
	// Try to get buffer from pool if available
	var float32Data []float32
	if float32Pool != nil && length == Float32BufferSize {
		float32Data = float32Pool.Get()
	} else {
		// Fallback to allocation for non-standard sizes or if pool not initialized
		float32Data = make([]float32, length)
	}
	
	divisor := float32(32768.0)

	for i := 0; i < length; i++ {
		sample := int16(sample[i*2]) | int16(sample[i*2+1])<<8
		float32Data[i] = float32(sample) / divisor
	}

	return float32Data
}

// convert24BitToFloat32 converts 24-bit sample to float32 values.
func convert24BitToFloat32(sample []byte) []float32 {
	length := len(sample) / 3
	float32Data := make([]float32, length)
	divisor := float32(8388608.0)

	for i := 0; i < length; i++ {
		sample := int32(sample[i*3]) | int32(sample[i*3+1])<<8 | int32(sample[i*3+2])<<16
		if (sample & 0x00800000) > 0 {
			sample |= ^0x00FFFFFF // Two's complement sign extension
		}
		float32Data[i] = float32(sample) / divisor
	}

	return float32Data
}

// convert32BitToFloat32 converts 32-bit sample to float32 values.
func convert32BitToFloat32(sample []byte) []float32 {
	length := len(sample) / 4
	float32Data := make([]float32, length)
	divisor := float32(2147483648.0)

	for i := 0; i < length; i++ {
		sample := int32(sample[i*4]) | int32(sample[i*4+1])<<8 | int32(sample[i*4+2])<<16 | int32(sample[i*4+3])<<24
		float32Data[i] = float32(sample) / divisor
	}

	return float32Data
}
