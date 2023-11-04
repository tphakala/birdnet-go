package analysis

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tphakala/go-birdnet/pkg/birdnet"
	"github.com/tphakala/go-birdnet/pkg/config"
	"github.com/tphakala/go-birdnet/pkg/myaudio"
	"github.com/tphakala/go-birdnet/pkg/observation"
)

// executeFileAnalysis conducts an analysis of an audio file and outputs the results.
// It reads an audio file, analyzes it for bird sounds, and prints the results based on the provided configuration.
func FileAnalysis(cfg *config.Settings) error {
	// Load and analyze the input audio file.
	audioData, err := myaudio.ReadAudioFile(cfg)
	if err != nil {
		// Use log.Fatalf to log the error and exit the program.
		log.Fatalf("error while reading input audio: %v", err)
	}

	// Analyze the loaded audio data for bird sounds.
	notes, err := birdnet.AnalyzeAudio(audioData, cfg)
	if err != nil {
		// Corrected typo "eailed" to "failed" and used log.Fatalf to log the error and exit.
		log.Fatalf("failed to analyze audio data: %v", err)
	}

	// Prepare the output file path if OutputDir is specified in the configuration.
	var outputFile string
	if cfg.OutputDir != "" {
		// Safely concatenate file paths using filepath.Join to avoid cross-platform issues.
		outputFile = filepath.Join(cfg.OutputDir, filepath.Base(cfg.InputFile))
	}

	// Output the notes based on the desired output type in the configuration.
	// If OutputType is not specified or if it's set to "table", output as a table format.
	if cfg.OutputFormat == "" || cfg.OutputFormat == "table" {
		if err := observation.WriteNotesTable(cfg, notes, outputFile); err != nil {
			log.Fatalf("failed to write notes table: %v", err)
		}
	}
	// If OutputType is set to "csv", output as CSV format.
	if cfg.OutputFormat == "csv" {
		if err := observation.WriteNotesCsv(cfg, notes, outputFile); err != nil {
			log.Fatalf("failed to write notes CSV: %v", err)
		}
	}
	return nil
}

// DirectoryAnalysis processes all .wav files in the given directory for analysis.
func DirectoryAnalysis(cfg *config.Settings) error {
	analyzeFunc := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Return the error to stop the walking process.
			return err
		}

		if d.IsDir() {
			// If recursion is not enabled and this is a subdirectory, skip it.
			if !cfg.Recursive && path != cfg.InputDirectory {
				return filepath.SkipDir
			}
			// If it's the root directory or recursion is enabled, continue walking.
			return nil
		}

		if strings.HasSuffix(d.Name(), ".wav") {
			fmt.Println("Analyzing file:", path)
			cfg.InputFile = path
			if err := FileAnalysis(cfg); err != nil {
				// If FileAnalysis returns an error log it and continue
				log.Printf("Error analyzing file '%s': %v", path, err)
				return nil
			}
		}
		return nil
	}

	// Start walking through the directory
	err := filepath.WalkDir(cfg.InputDirectory, analyzeFunc)
	if err != nil {
		log.Fatalf("Failed to walk directory: %v", err)
	}

	return nil
}

// executeRealtimeAnalysis initiates the BirdNET Analyzer in real-time mode and waits for a termination signal.
func RealtimeAnalysis(cfg *config.Settings) error {
	fmt.Println("Starting BirdNET Analyzer in realtime mode")

	// Start necessary routines for real-time analysis.
	myaudio.StartGoRoutines(cfg)

	// Channel to receive OS signals.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	// Block until a signal is received.
	<-c

	// Delete tflite interpreter
	birdnet.DeleteInterpreter()

	// Close the QuitChannel to signal termination
	close(myaudio.QuitChannel)

	return nil
}
