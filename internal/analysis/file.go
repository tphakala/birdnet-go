package analysis

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observation"
)

// executeFileAnalysis conducts an analysis of an audio file and outputs the results.
// It reads an audio file, analyzes it for bird sounds, and prints the results based on the provided configuration.
func FileAnalysis(ctx *conf.Context, bn birdnet.BirdNET) error {

	fileInfo, err := os.Stat(ctx.Settings.Input.Path)
	if err != nil {
		return fmt.Errorf("error accessing the path: %v", err)
	}

	// Check if it's a file (not a directory)
	if fileInfo.IsDir() {
		return fmt.Errorf("the path is a directory, not a file")
	}

	// Load and analyze the input audio file.
	audioData, err := myaudio.ReadAudioFile(ctx)
	if err != nil {
		// Use log.Fatalf to log the error and exit the program.
		log.Fatalf("error while reading input audio: %v", err)
	}

	// Analyze the loaded audio data for bird sounds.
	notes, err := bn.AnalyzeAudio(audioData)
	if err != nil {
		// Corrected typo "eailed" to "failed" and used log.Fatalf to log the error and exit.
		log.Fatalf("failed to analyze audio data: %v", err)
	}

	// Prepare the output file path if OutputDir is specified in the configuration.
	var outputFile string
	if ctx.Settings.Output.File.Path != "" {
		// Safely concatenate file paths using filepath.Join to avoid cross-platform issues.
		outputFile = filepath.Join(ctx.Settings.Output.File.Path, filepath.Base(ctx.Settings.Input.Path))
	}

	// Output the notes based on the desired output type in the configuration.
	// If OutputType is not specified or if it's set to "table", output as a table format.
	if ctx.Settings.Output.File.Type == "" || ctx.Settings.Output.File.Type == "table" {
		if err := observation.WriteNotesTable(ctx, notes, outputFile); err != nil {
			log.Fatalf("failed to write notes table: %v", err)
		}
	}
	// If OutputType is set to "csv", output as CSV format.
	if ctx.Settings.Output.File.Type == "csv" {
		if err := observation.WriteNotesCsv(ctx, notes, outputFile); err != nil {
			log.Fatalf("failed to write notes CSV: %v", err)
		}
	}
	return nil
}
