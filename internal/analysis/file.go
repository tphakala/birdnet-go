package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observation"
)

// FileAnalysis conducts an analysis of an audio file and outputs the results.
// It reads an audio file, analyzes it for bird sounds, and prints the results based on the provided configuration.
func FileAnalysis(settings *conf.Settings) error {
	// Initialize BirdNET interpreter
	if err := initializeBirdNET(settings); err != nil {
		return err
	}

	if err := validateAudioFile(settings.Input.Path); err != nil {
		return err
	}

	// Get audio file information
	audioInfo, err := myaudio.GetAudioInfo(settings.Input.Path)
	if err != nil {
		return fmt.Errorf("error getting audio info: %w", err)
	}

	notes, err := processAudioFile(settings, &audioInfo)
	if err != nil {
		return err
	}

	return writeResults(settings, notes)
}

// validateAudioFile checks if the provided file path is a valid audio file.
func validateAudioFile(filePath string) error {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("\033[31m❌ Error accessing file %s: %w\033[0m", filepath.Base(filePath), err)
	}

	// Check if it's a file (not a directory)
	if fileInfo.IsDir() {
		return fmt.Errorf("\033[31m❌ The path %s is a directory, not a file\033[0m", filepath.Base(filePath))
	}

	// Check if file size is 0
	if fileInfo.Size() == 0 {
		return fmt.Errorf("\033[31m❌ File %s is empty (0 bytes)\033[0m", filepath.Base(filePath))
	}

	// Open the file to check if it's a valid FLAC file
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("\033[31m❌ Error opening file %s: %w\033[0m", filepath.Base(filePath), err)
	}
	defer file.Close()

	// Try to get audio info to validate the file format
	audioInfo, err := myaudio.GetAudioInfo(filePath)
	if err != nil {
		return fmt.Errorf("\033[31m❌ Invalid audio file %s: %w\033[0m", filepath.Base(filePath), err)
	}

	// Check if the audio duration is valid (greater than 0)
	if audioInfo.TotalSamples == 0 {
		return fmt.Errorf("\033[31m❌ File %s contains no samples or is still being written\033[0m", filepath.Base(filePath))
	}

	return nil
}

// processAudioFile processes the audio file and returns the notes.
func processAudioFile(settings *conf.Settings, audioInfo *myaudio.AudioInfo) ([]datastore.Note, error) {
	// Calculate total chunks
	totalChunks := myaudio.GetTotalChunks(
		audioInfo.SampleRate,
		audioInfo.TotalSamples,
		settings.BirdNET.Overlap,
	)

	// Calculate audio duration
	duration := time.Duration(float64(audioInfo.TotalSamples) / float64(audioInfo.SampleRate) * float64(time.Second))

	// Get filename and truncate if necessary
	filename := truncateFilename(settings.Input.Path)

	startTime := time.Now()
	chunkCount := 0

	// Determine number of workers (between 1 and 8)
	numWorkers := settings.BirdNET.Threads
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}
	numWorkers = clampInt(numWorkers, 1, 8) // Ensure between 1 and 8 workers

	if settings.Debug {
		fmt.Printf("DEBUG: Starting analysis with %d total chunks and %d workers\n", totalChunks, numWorkers)
	}

	// Create buffered channels for processing
	chunkChan := make(chan []float32, 4)
	resultChan := make(chan []datastore.Note, 4)
	errorChan := make(chan error, 1)
	doneChan := make(chan struct{})

	var allNotes []datastore.Note
	predStart := time.Time{}

	// Start worker goroutines for BirdNET analysis
	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			if settings.Debug {
				fmt.Printf("DEBUG: Worker %d started\n", workerID)
			}
			for chunk := range chunkChan {
				if settings.Debug {
					fmt.Printf("DEBUG: Worker %d processing chunk\n", workerID)
				}
				notes, err := bn.ProcessChunk(chunk, predStart)
				if err != nil {
					if settings.Debug {
						fmt.Printf("DEBUG: Worker %d encountered error: %v\n", workerID, err)
					}
					errorChan <- err
					return
				}

				// Filter notes based on included species list
				var filteredNotes []datastore.Note
				for _, note := range notes {
					if settings.IsSpeciesIncluded(note.ScientificName) {
						filteredNotes = append(filteredNotes, note)
					}
				}

				if settings.Debug {
					fmt.Printf("DEBUG: Worker %d sending results\n", workerID)
				}
				resultChan <- filteredNotes
			}
			if settings.Debug {
				fmt.Printf("DEBUG: Worker %d finished\n", workerID)
			}
		}(i)
	}

	// Start progress monitoring goroutine
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			select {
			case <-doneChan:
				return
			default:
				fmt.Printf("\r\033[K\033[37m📄 %s [%s]\033[0m | \033[33m🔍 Analyzing chunk %d/%d\033[0m %s",
					filename,
					duration.Round(time.Second),
					chunkCount,
					totalChunks,
					birdnet.EstimateTimeRemaining(startTime, chunkCount, totalChunks))
			}
		}
	}()

	// Start result collector goroutine
	var processingError error
	go func() {
		if settings.Debug {
			fmt.Println("DEBUG: Result collector started")
		}
		for i := 0; i < totalChunks; i++ {
			select {
			case notes := <-resultChan:
				if settings.Debug {
					fmt.Printf("DEBUG: Collector received results for chunk %d\n", chunkCount)
				}
				allNotes = append(allNotes, notes...)
				chunkCount++
			case err := <-errorChan:
				if settings.Debug {
					fmt.Printf("DEBUG: Collector received error: %v\n", err)
				}
				processingError = err
				close(doneChan)
				return
			}
		}
		if settings.Debug {
			fmt.Println("DEBUG: Collector finished")
		}
		close(doneChan)
	}()

	// Read and send audio chunks
	if settings.Debug {
		fmt.Println("DEBUG: Starting to read audio chunks")
	}
	err := myaudio.ReadAudioFileBuffered(settings, func(chunk []float32) error {
		if settings.Debug {
			fmt.Println("DEBUG: Read new chunk from file")
		}
		select {
		case chunkChan <- chunk:
			if settings.Debug {
				fmt.Println("DEBUG: Sent chunk to processing channel")
			}
			// advance predStart by 3 seconds - overlap
			predStart = predStart.Add(time.Duration((3.0 - bn.Settings.BirdNET.Overlap) * float64(time.Second)))
			return nil
		case <-doneChan:
			if settings.Debug {
				fmt.Println("DEBUG: Chunk processing interrupted")
			}
			return processingError
		}
	})

	if settings.Debug {
		fmt.Println("DEBUG: Finished reading audio file")
	}
	close(chunkChan)

	if settings.Debug {
		fmt.Println("DEBUG: Waiting for processing to complete")
	}
	<-doneChan // Wait for processing to complete

	if err != nil {
		if settings.Debug {
			fmt.Printf("DEBUG: File processing error: %v\n", err)
		}
		return nil, fmt.Errorf("error processing audio: %w", err)
	}

	if processingError != nil {
		if settings.Debug {
			fmt.Printf("DEBUG: Processing error encountered: %v\n", processingError)
		}
		return nil, processingError
	}

	if settings.Debug {
		fmt.Println("DEBUG: Analysis completed successfully")
	}
	// Show total time taken for analysis
	fmt.Printf("\r\033[K\033[37m📄 %s [%s]\033[0m | \033[32m✅ Analysis completed in %s\033[0m\n",
		filename,
		duration.Round(time.Second),
		birdnet.FormatDuration(time.Since(startTime)))

	return allNotes, nil
}

// truncateFilename truncates the filename to 30 characters if it's longer.
func truncateFilename(path string) string {
	filename := filepath.Base(path)
	if len(filename) > 30 {
		return filename[:27] + "..."
	}
	return filename
}

// writeResults writes the notes to the output file based on the configuration.
func writeResults(settings *conf.Settings, notes []datastore.Note) error {
	// Prepare the output file path if OutputDir is specified in the configuration.
	var outputFile string
	if settings.Output.File.Path != "" {
		// Safely concatenate file paths using filepath.Join to avoid cross-platform issues.
		outputFile = filepath.Join(settings.Output.File.Path, filepath.Base(settings.Input.Path))
	}

	// Output the notes based on the desired output type in the configuration.
	// If OutputType is not specified or if it's set to "table", output as a table format.
	if settings.Output.File.Type == "" || settings.Output.File.Type == "table" {
		if err := observation.WriteNotesTable(settings, notes, outputFile); err != nil {
			return fmt.Errorf("failed to write notes table: %w", err)
		}
	}
	// If OutputType is set to "csv", output as CSV format.
	if settings.Output.File.Type == "csv" {
		if err := observation.WriteNotesCsv(settings, notes, outputFile); err != nil {
			return fmt.Errorf("failed to write notes CSV: %w", err)
		}
	}
	return nil
}

// clampInt ensures a value is between min and max (inclusive)
func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
