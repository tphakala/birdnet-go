package analysis

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/config"
	"github.com/tphakala/birdnet-go/internal/controller"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/model"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observation"
	"github.com/tphakala/birdnet-go/pkg/birdnet"
)

var once sync.Once

// executeFileAnalysis conducts an analysis of an audio file and outputs the results.
// It reads an audio file, analyzes it for bird sounds, and prints the results based on the provided configuration.
func FileAnalysis(ctx *config.Context) error {

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
	notes, err := birdnet.AnalyzeAudio(audioData, ctx)
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

// DirectoryAnalysis processes all .wav files in the given directory for analysis.
func DirectoryAnalysis(ctx *config.Context) error {
	analyzeFunc := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Return the error to stop the walking process.
			return err
		}

		if d.IsDir() {
			// If recursion is not enabled and this is a subdirectory, skip it.
			if !ctx.Settings.Input.Recursive && path != ctx.Settings.Input.Path {
				return filepath.SkipDir
			}
			// If it's the root directory or recursion is enabled, continue walking.
			return nil
		}

		if strings.HasSuffix(d.Name(), ".wav") {
			fmt.Println("Analyzing file:", path)
			ctx.Settings.Input.Path = path
			if err := FileAnalysis(ctx); err != nil {
				// If FileAnalysis returns an error log it and continue
				log.Printf("Error analyzing file '%s': %v", path, err)
				return nil
			}
		}
		return nil
	}

	// Start walking through the directory
	err := filepath.WalkDir(ctx.Settings.Input.Path, analyzeFunc)
	if err != nil {
		log.Fatalf("Failed to walk directory: %v", err)
	}

	return nil
}

// RealtimeAnalysis initiates the BirdNET Analyzer in real-time mode and waits for a termination signal.
func RealtimeAnalysis(ctx *config.Context) error {
	// Initialize occurrence monitor to filter out repeated observations.
	ctx.OccurrenceMonitor = config.NewOccurrenceMonitor(time.Duration(ctx.Settings.Realtime.Interval) * time.Second)

	// Initialize birdweather client if enabled in settings.
	if ctx.Settings.Realtime.Birdweather.Enabled {
		ctx.BirdweatherClient = birdweather.NewClient(
			ctx.Settings.Realtime.Birdweather.ID,
			ctx.Settings.BirdNET.Latitude,
			ctx.Settings.BirdNET.Longitude)
	}

	// Log the start of BirdNET-Go Analyzer in realtime mode and its configurations.
	fmt.Println("Starting BirdNET-Go Analyzer in realtime mode")
	fmt.Printf("Threshold: %v, sensitivity: %v, interval: %v\n",
		ctx.Settings.BirdNET.Threshold,
		ctx.Settings.BirdNET.Sensitivity,
		ctx.Settings.Realtime.Interval)

	// Initialize database access.
	var dataStore model.StoreInterface

	// Initialize the specific store based on settings.
	if ctx.Settings.Output.SQLite.Enabled {
		dataStore = &model.SQLiteStore{}
	} else if ctx.Settings.Output.MySQL.Enabled {
		dataStore = &model.MySQLStore{}
	}

	// Check if a database was selected for output and exit if not.
	if dataStore == nil {
		return fmt.Errorf("no database selected for output")
	}

	// Open a connection to the database and handle possible errors.
	if err := dataStore.Open(ctx); err != nil {
		logger.Error("main", "Failed to open database: %v", err)
		return err // Return error to stop execution if database connection fails.
	} else {
		logger.Info("main", "Successfully opened database")
		// Ensure the database connection is closed when the function returns.
		defer func() {
			if err := dataStore.Close(); err != nil {
				logger.Error("main", "Failed to close database: %v", err)
			} else {
				logger.Info("main", "Successfully closed database")
			}
		}()
	}

	// Start web server goroutine.
	if err := controller.Start(ctx, dataStore); err != nil {
		logger.Error("main", "Failed to start the controller: %v", err)
		return err // Return error if controller fails to start.
	}

	// Start audio capture and buffer monitor goroutines.
	myaudio.StartGoRoutines(ctx)

	// Block until QuitChannel is closed, indicating termination signal.
	<-myaudio.QuitChannel

	// Perform cleanup using sync.Once to ensure it happens only once.
	once.Do(func() {
		birdnet.DeleteInterpreter(ctx)
	})

	return nil
}
