package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fatih/color"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observation"
)

var bn *birdnet.BirdNET

// executeFileAnalysis conducts an analysis of an audio file and outputs the results.
// It reads an audio file, analyzes it for bird sounds, and prints the results based on the provided configuration.
func FileAnalysis(settings *conf.Settings) error {
	// Initialize the BirdNET interpreter only if not already initialized
	if bn == nil {
		var err error
		bn, err = birdnet.NewBirdNET(settings)
		if err != nil {
			return fmt.Errorf("failed to initialize BirdNET: %w", err)
		}
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

	// Calculate audio duration
	duration := time.Duration(float64(audioInfo.TotalSamples) / float64(audioInfo.SampleRate) * float64(time.Second))

	var allNotes []datastore.Note
	startTime := time.Now()
	chunkCount := 0

	// Get filename and truncate if necessary (showing max 30 chars)
	filename := filepath.Base(settings.Input.Path)
	if len(filename) > 30 {
		filename = filename[:27] + "..."
	}

	// Lets set predStart to 0 time
	predStart := time.Time{}

	// Add color functions
	white := color.New(color.FgWhite)
	yellow := color.New(color.FgYellow)
	green := color.New(color.FgGreen)

	// Process audio chunks as they're read
	err = myaudio.ReadAudioFileBuffered(settings, func(chunk []float32) error {
		chunkCount++
		fmt.Printf("\r\033[K") // Clear line
		white.Printf("📄 %s [%s]", filename, duration.Round(time.Second))
		fmt.Print(" | ")
		yellow.Printf("🔍 Analyzing chunk %d/%d ", chunkCount, totalChunks)
		fmt.Print(birdnet.EstimateTimeRemaining(startTime, chunkCount, totalChunks))

		//notes, err := bn.ProcessChunk(chunk, float64(chunkCount-1)*(3-settings.BirdNET.Overlap))
		notes, err := bn.ProcessChunk(chunk, predStart)
		if err != nil {
			return err
		}
		allNotes = append(allNotes, notes...)

		// advance predStart by 3 seconds - overlap
		predStart = predStart.Add(time.Duration((3.0 - bn.Settings.BirdNET.Overlap) * float64(time.Second)))

		return nil
	})

	if err != nil {
		return fmt.Errorf("error processing audio: %w", err)
	}

	// Show total time taken for analysis
	fmt.Printf("\r\033[K") // Clear line
	white.Printf("📄 %s [%s]", filename, duration.Round(time.Second))
	fmt.Print(" | ")
	green.Printf("✅ Analysis completed in %s\n", birdnet.FormatDuration(time.Since(startTime)))

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
