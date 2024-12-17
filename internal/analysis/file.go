package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observation"
)

// executeFileAnalysis conducts an analysis of an audio file and outputs the results.
// It reads an audio file, analyzes it for bird sounds, and prints the results based on the provided configuration.
func FileAnalysis(settings *conf.Settings) error {
	// Initialize the BirdNET interpreter.
	bn, err := birdnet.NewBirdNET(settings)
	if err != nil {
		return fmt.Errorf("failed to initialize BirdNET: %w", err)
	}

	fileInfo, err := os.Stat(settings.Input.Path)
	if err != nil {
		return fmt.Errorf("error accessing the path: %w", err)
	}

	// Check if it's a file (not a directory)
	if fileInfo.IsDir() {
		return fmt.Errorf("the path is a directory, not a file")
	}

	// Get audio file information
	audioInfo, err := myaudio.GetAudioInfo(settings.Input.Path)
	if err != nil {
		return fmt.Errorf("error getting audio info: %w", err)
	}

	// Calculate total chunks
	totalChunks := myaudio.GetTotalChunks(
		audioInfo.SampleRate,
		audioInfo.TotalSamples,
		settings.BirdNET.Overlap,
	)

	var allNotes []datastore.Note
	startTime := time.Now()
	chunkCount := 0

	// Process audio chunks as they're read
	err = myaudio.ReadAudioFileBuffered(settings, func(chunk []float32) error {
		chunkCount++
		fmt.Printf("\r\033[KAnalyzing chunk %d/%d %s",
			chunkCount,
			totalChunks,
			birdnet.EstimateTimeRemaining(startTime, chunkCount, totalChunks))

		notes, err := bn.ProcessChunk(chunk, float64(chunkCount-1)*(3-settings.BirdNET.Overlap))
		if err != nil {
			return err
		}
		allNotes = append(allNotes, notes...)
		return nil
	})

	if err != nil {
		return fmt.Errorf("error processing audio: %w", err)
	}

	// Add a newline to the console
	fmt.Println()

	// Prepare the output file path if OutputDir is specified in the configuration.
	var outputFile string
	if settings.Output.File.Path != "" {
		// Safely concatenate file paths using filepath.Join to avoid cross-platform issues.
		outputFile = filepath.Join(settings.Output.File.Path, filepath.Base(settings.Input.Path))
	}

	// Output the notes based on the desired output type in the configuration.
	// If OutputType is not specified or if it's set to "table", output as a table format.
	if settings.Output.File.Type == "" || settings.Output.File.Type == "table" {
		if err := observation.WriteNotesTable(settings, allNotes, outputFile); err != nil {
			return fmt.Errorf("failed to write notes table: %w", err)
		}
	}
	// If OutputType is set to "csv", output as CSV format.
	if settings.Output.File.Type == "csv" {
		if err := observation.WriteNotesCsv(settings, allNotes, outputFile); err != nil {
			return fmt.Errorf("failed to write notes CSV: %w", err)
		}
	}
	return nil
}
