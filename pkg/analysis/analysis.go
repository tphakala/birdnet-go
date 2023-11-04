package analysis

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
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

	// Print a newline for better readability in the output.
	fmt.Println()

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

// executeDirectoryAnalysis processes all .wav files in the given directory for analysis.
func DirectoryAnalysis(cfg *config.Settings) error {
	// Read the list of files from the input directory.
	files, err := os.ReadDir(cfg.InputDirectory)
	if err != nil {
		log.Fatalf("Failed to open directory: %v", err)
	}

	for _, file := range files {
		// Check if the file has a .wav extension.
		if filepath.Ext(file.Name()) == ".wav" {
			inputFilePath := filepath.Join(cfg.InputDirectory, file.Name())
			fmt.Println("Analyzing file:", inputFilePath)
			// Create a copy of the config struct and set the input audio file path.
			//cfgCopy := *cfg
			cfg.InputFile = inputFilePath
			FileAnalysis(cfg)
		}
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
