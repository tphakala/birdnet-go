package myaudio

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/tphakala/birdnet-go/internal/config"
	"github.com/tphakala/birdnet-go/internal/observation"
	"github.com/tphakala/birdnet-go/pkg/birdnet"
)

// processData processes the given audio data to detect bird species, logs the detected species
// and optionally saves the audio clip if a bird species is detected above the configured threshold.
func processData(data []byte, ctx *config.Context) error {
	// temp assignment, fix later
	const defaultBitDepth = 16

	startTime := time.Now()

	sampleData, err := ConvertToFloat32(data, defaultBitDepth)
	if err != nil {
		return fmt.Errorf("error converting to float32: %w", err)
	}

	results, err := predictSpecies(sampleData, ctx)
	if err != nil {
		return err
	}

	elapsedTime := logProcessingTime(startTime, ctx)

	if err := processPredictionResults(results, data, ctx, elapsedTime); err != nil {
		return err
	}

	return nil
}

func predictSpecies(sampleData [][]float32, ctx *config.Context) ([]birdnet.Result, error) {
	results, err := birdnet.Predict(sampleData, ctx)
	if err != nil {
		return nil, fmt.Errorf("error predicting: %w", err)
	}
	return results, nil
}

func logProcessingTime(startTime time.Time, ctx *config.Context) time.Duration {
	if ctx.Settings.Realtime.ProcessingTime || ctx.Settings.Debug {
		elapsedTime := time.Since(startTime)
		fmt.Printf("\r\033[Kprocessing time %v ms", elapsedTime.Milliseconds())
		return elapsedTime
	}
	return 0
}

func processPredictionResults(results []birdnet.Result, data []byte, ctx *config.Context, elapsedTime time.Duration) error {
	if len(results) == 0 {
		return nil
	}

	_, species, _ := observation.ParseSpeciesString(results[0].Species)
	confidence := results[0].Confidence

	// Use custom confidence threshold if it exists for the species, otherwise use the global threshold
	confidenceThreshold, exists := ctx.CustomConfidence.Thresholds[species]
	if !exists {
		confidenceThreshold = float32(ctx.Settings.BirdNET.Threshold)
	} else {
		if ctx.Settings.Debug {
			fmt.Printf("\nUsing confidence threshold of %.2f for %s\n", confidenceThreshold, species)
		}
	}

	if confidence <= confidenceThreshold {
		// if debug print confidence
		if ctx.Settings.Debug {
			fmt.Printf("\nConfidence %.2f below threshold %.2f, skipping processing\n", confidence, confidenceThreshold)
		}
		return nil
	}

	// match against location based filter
	if !isSpeciesIncluded(results[0].Species, ctx.IncludedSpeciesList) {
		if ctx.Settings.Debug {
			fmt.Printf("\nSpecies not on included list: %s\n", species)
		}
		return nil
	}

	// match against occurence monitor to filter too frequent observations for same species
	if ctx.OccurrenceMonitor.TrackSpecies(species) {
		if ctx.Settings.Debug {
			fmt.Printf("\nDuplicate occurrence detected: %s, skipping processing\n", species)
		}
		return nil
	}

	var clipName string

	if ctx.Settings.Realtime.AudioExport.Enabled {
		// save audio clip
		clipName = saveAudioClip(data, ctx)
	}

	return logObservation(ctx, results[0], clipName, elapsedTime)
}

func saveAudioClip(data []byte, ctx *config.Context) string {
	if ctx.Settings.Realtime.AudioExport.Path == "" {
		return ""
	}

	baseFileName := strconv.FormatInt(time.Now().Unix(), 10)
	clipName := fmt.Sprintf("%s/%s.%s", ctx.Settings.Realtime.AudioExport.Path, baseFileName, ctx.Settings.Realtime.AudioExport.Type)

	var err error
	switch ctx.Settings.Realtime.AudioExport.Type {
	case "wav":
		err = savePCMDataToWAV(clipName, data)
	case "flac":
		err = savePCMDataToFlac(clipName, data)
	case "mp3":
		err = savePCMDataToMP3(clipName, data)
	}

	if err != nil {
		fmt.Printf("error saving audio clip to %s: %s\n", ctx.Settings.Realtime.AudioExport.Type, err)
		return ""
	} else if ctx.Settings.Debug {
		fmt.Printf("Saved audio clip to %s\n", clipName)
	}

	return clipName
}

func logObservation(ctx *config.Context, result birdnet.Result, clipName string, elapsedTime time.Duration) error {
	beginTime, endTime := 0.0, 0.0 // temporary assignments
	note := observation.New(ctx, beginTime, endTime, result.Species, float64(result.Confidence), clipName, elapsedTime)

	if err := observation.LogNote(ctx, note); err != nil {
		fmt.Printf("error logging note: %s\n", err)
		return err
	}

	fmt.Printf("%s %s %.2f\n", note.Time, note.CommonName, note.Confidence)
	return nil
}

// isSpeciesIncluded checks if the given species is in the included species list.
// It returns true if the species is in the list, or if the list is empty (no filtering).
func isSpeciesIncluded(species string, includedList []string) bool {
	if len(includedList) == 0 {
		return true // no filtering applied when the list is empty
	}
	for _, s := range includedList {
		if species == s {
			return true
		}
	}

	return false
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
		return nil, errors.New("unsupported audio bit depth")
	}
}

// convert16BitToFloat32 converts 16-bit sample to float32 values.
func convert16BitToFloat32(sample []byte) []float32 {
	length := len(sample) / 2
	float32Data := make([]float32, length)
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
