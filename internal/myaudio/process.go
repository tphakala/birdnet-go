package myaudio

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/tphakala/BirdNET-Go/internal/config"
	"github.com/tphakala/BirdNET-Go/internal/observation"
	"github.com/tphakala/BirdNET-Go/pkg/birdnet"
)

// processData processes the given audio data to detect bird species, logs the detected species
// and optionally saves the audio clip if a bird species is detected above the configured threshold.
func processData(data []byte, ctx *config.Context) error {

	const defaultBitDepth = 16

	// Start timestamp for processing time measurement.
	startTime := time.Now()

	// Convert raw audio data to float32 format.
	sampleData, err := ConvertToFloat32(data, defaultBitDepth)
	if err != nil {
		return fmt.Errorf("error converting to float32: %w", err)
	}

	// Use the birdnet model to predict the bird species from the audio sample.
	results, err := birdnet.Predict(sampleData, ctx)
	if err != nil {
		return fmt.Errorf("error predicting: %w", err)
	}

	// Print processing time if required.
	var elapsedTime time.Duration
	if ctx.Settings.ProcessingTime || ctx.Settings.Debug {
		elapsedTime = time.Since(startTime)
		fmt.Printf("\r\033[Kprocessing time %v ms", elapsedTime.Milliseconds())
	}

	// Check if the prediction confidence is above the threshold.
	if results[0].Confidence <= float32(ctx.Settings.Threshold) {
		return nil
	}

	species, _, _ := observation.ParseSpeciesString(results[0].Species)

	// Check if the species is in the included list
	if !isSpeciesIncluded(results[0].Species, ctx.IncludedSpeciesList) {
		if ctx.Settings.Debug {
			fmt.Printf("\nSpecies not included: %s, skipping processing\n", species)
		}
		return nil
	}

	// Check if the species is in the excluded list
	if isSpeciesExcluded(species, ctx.ExcludedSpeciesList) {
		if ctx.Settings.Debug {
			fmt.Printf("\nExcluded species detected: %s, skipping processing\n", species)
		}
		return nil
	}

	// check if it is same species as previous and if so, check if it is too soon to report
	filter := ctx.OccurrenceMonitor.TrackSpecies(species)

	if filter {
		// Skip further processing if TrackSpecies returned true
		if ctx.Settings.Debug {
			fmt.Printf("\nDuplicate occurrence detected: %s, skipping processing\n", species)
		}
		return nil
	}

	var clipName string = ""

	// If CapturePath is set save audio clip to disk
	if ctx.Settings.ClipPath != "" {
		// Construct the filename for saving the audio sample.
		baseFileName := strconv.FormatInt(time.Now().Unix(), 10)
		var clipName string
		var err error

		if ctx.Settings.Debug {
			fmt.Printf("\nPreparing to save audio clip\n")
		}

		switch ctx.Settings.ClipType {
		case "wav":
			clipName = fmt.Sprintf("%s/%s.wav", ctx.Settings.ClipPath, baseFileName)
			err = savePCMDataToWAV(clipName, data)

		case "flac":
			clipName = fmt.Sprintf("%s/%s.flac", ctx.Settings.ClipPath, baseFileName)
			err = savePCMDataToFlac(clipName, data)

		case "mp3":
			clipName = fmt.Sprintf("%s/%s.mp3", ctx.Settings.ClipPath, baseFileName)
			err = savePCMDataToMP3(clipName, data)
		}

		if err != nil {
			fmt.Printf("error saving audio clip to %s: %s\n", ctx.Settings.ClipType, err)
		} else if ctx.Settings.Debug {
			fmt.Printf("Saved audio clip to %s\n", clipName)
		}
	}

	// temporary assignments
	var beginTime float64 = 0.0
	var endTime float64 = 0.0

	// Create an observation.Note from the prediction result.
	note := observation.New(ctx.Settings, beginTime, endTime, results[0].Species, float64(results[0].Confidence), clipName, elapsedTime)

	// Log the observation to the specified log file.
	if err := observation.LogNote(ctx.Settings, note); err != nil {
		fmt.Printf("error logging note: %s\n", err)
	}

	fmt.Printf("%s %s %.2f\n", note.Time, note.CommonName, note.Confidence)

	return nil
}

// isSpeciesIncluded checks if the given species is in the included species list.
func isSpeciesIncluded(species string, includedList []string) bool {
	for _, s := range includedList {
		if species == s {
			return true
		}
	}
	return false
}

// isSpeciesExcluded checks if the given species is in the excluded list.
func isSpeciesExcluded(species string, excludedList []string) bool {
	for _, excludedSpecies := range excludedList {
		if species == excludedSpecies {
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
