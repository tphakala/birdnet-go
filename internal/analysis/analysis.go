package analysis

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/config"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observation"
	"github.com/tphakala/birdnet-go/pkg/birdnet"
)

var once sync.Once

// executeFileAnalysis conducts an analysis of an audio file and outputs the results.
// It reads an audio file, analyzes it for bird sounds, and prints the results based on the provided configuration.
func FileAnalysis(ctx *config.Context) error {
	// Load and analyze the input audio file.
	audioData, err := myaudio.ReadAudioFile(ctx)
	if err != nil {
		// Use log.Fatalf to log the error and exit the program.
		log.Fatalf("error while reading input audio: %v", err)
	}

	// Analyze the loaded audio data for bird sounds.
	notes, err := birdnet.AnalyzeAudio(audioData, ctx)
	if err != nil {
		// Corrected typo "eailed" to "failed" and used log.Fatalf to log the error and exit.
		log.Fatalf("failed to analyze audio data: %v", err)
	}

	// Prepare the output file path if OutputDir is specified in the configuration.
	var outputFile string
	if ctx.Settings.OutputDir != "" {
		// Safely concatenate file paths using filepath.Join to avoid cross-platform issues.
		outputFile = filepath.Join(ctx.Settings.OutputDir, filepath.Base(ctx.Settings.InputFile))
	}

	// Output the notes based on the desired output type in the configuration.
	// If OutputType is not specified or if it's set to "table", output as a table format.
	if ctx.Settings.OutputFormat == "" || ctx.Settings.OutputFormat == "table" {
		if err := observation.WriteNotesTable(ctx.Settings, notes, outputFile); err != nil {
			log.Fatalf("failed to write notes table: %v", err)
		}
	}
	// If OutputType is set to "csv", output as CSV format.
	if ctx.Settings.OutputFormat == "csv" {
		if err := observation.WriteNotesCsv(ctx.Settings, notes, outputFile); err != nil {
			log.Fatalf("failed to write notes CSV: %v", err)
		}
	}
	return nil
}

// DirectoryAnalysis processes all .wav files in the given directory for analysis.
func DirectoryAnalysis(ctx *config.Context) error {
	analyzeFunc := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Return the error to stop the walking process.
			return err
		}

		if d.IsDir() {
			// If recursion is not enabled and this is a subdirectory, skip it.
			if !ctx.Settings.Recursive && path != ctx.Settings.InputDirectory {
				return filepath.SkipDir
			}
			// If it's the root directory or recursion is enabled, continue walking.
			return nil
		}

		if strings.HasSuffix(d.Name(), ".wav") {
			fmt.Println("Analyzing file:", path)
			ctx.Settings.InputFile = path
			if err := FileAnalysis(ctx); err != nil {
				// If FileAnalysis returns an error log it and continue
				log.Printf("Error analyzing file '%s': %v", path, err)
				return nil
			}
		}
		return nil
	}

	// Start walking through the directory
	err := filepath.WalkDir(ctx.Settings.InputDirectory, analyzeFunc)
	if err != nil {
		log.Fatalf("Failed to walk directory: %v", err)
	}

	return nil
}

// executeRealtimeAnalysis initiates the BirdNET Analyzer in real-time mode and waits for a termination signal.
func RealtimeAnalysis(ctx *config.Context) error {
	// Do not report repeated observations within this interval
	const OccurrenceInterval = 15 // seconds

	// initialize occurrence monitor to filter out repeated observations
	ctx.OccurrenceMonitor = config.NewOccurrenceMonitor(OccurrenceInterval * time.Second)

	fmt.Printf("Starting BirdNET-Go Analyzer in realtime mode, threshold: %.2f, sensitivity: %.2f\n",
		ctx.Settings.Threshold, ctx.Settings.Sensitivity)

	// Start necessary routines for real-time analysis.
	myaudio.StartGoRoutines(ctx)

	// Block until QuitChannel is closed.
	<-myaudio.QuitChannel

	// Perform cleanup using sync.Once to ensure it happens only once.
	once.Do(func() {
		birdnet.DeleteInterpreter(ctx)
		//close(myaudio.QuitChannel)
	})

	return nil
}
