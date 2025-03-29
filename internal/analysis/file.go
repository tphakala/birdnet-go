package analysis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observation"
)

// processingChannels holds all channels needed for audio processing
type processingChannels struct {
	chunkChan  chan audioChunk
	resultChan chan []datastore.Note
	errorChan  chan error
	doneChan   chan struct{}
	eofChan    chan struct{}
}

// Define audioChunk type at package level since it's used by multiple functions
type audioChunk struct {
	Data         []float32
	FilePosition time.Time
}

// Define an error holder type to avoid pointer-to-pointer issues
type errorHolder struct {
	mu  sync.Mutex
	err error
}

// setError safely sets the error
func (h *errorHolder) setError(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.err = err
}

// getError safely gets the error
func (h *errorHolder) getError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

// FileAnalysis conducts an analysis of an audio file and outputs the results.
// It reads an audio file, analyzes it for bird sounds, and prints the results based on the provided configuration.
func FileAnalysis(settings *conf.Settings, ctx context.Context) error {
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

	notes, err := processAudioFile(settings, &audioInfo, ctx)
	if err != nil {
		// Handle cancellation first
		if errors.Is(err, ErrAnalysisCanceled) {
			return nil
		}

		// For other errors with partial results, write them
		if len(notes) > 0 {
			fmt.Printf("\n\033[33m⚠️  Writing partial results before exiting due to error\033[0m\n")
			if writeErr := writeResults(settings, notes); writeErr != nil {
				return fmt.Errorf("analysis error: %w; failed to write partial results: %w", err, writeErr)
			}
		}
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

	// Check file extension (case-insensitive)
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".wav" && ext != ".flac" {
		return fmt.Errorf("\033[31m❌ Invalid audio file %s: unsupported audio format: %s\033[0m", filepath.Base(filePath), filepath.Ext(filePath))
	}

	// Open the file to check if it's a valid audio file
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

// truncateString truncates a string to fit within maxLen, adding "..." if truncated
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// formatProgressLine formats the progress line to fit within the terminal width
func formatProgressLine(filename string, duration time.Duration, chunkCount, totalChunks int, avgRate float64, timeRemaining string, termWidth int) string {
	// Base format without filename (to calculate remaining space)
	baseFormat := fmt.Sprintf(" [%s] | \033[33m🔍 Analyzing chunk %d/%d\033[0m | \033[36m%.1f chunks/sec\033[0m %s",
		duration.Round(time.Second),
		chunkCount,
		totalChunks,
		avgRate,
		timeRemaining)

	// Calculate available space for filename
	// Account for emoji (📄) and color codes
	const colorCodesLen = 45 // Approximate length of all color codes
	availableSpace := termWidth - len(baseFormat) - colorCodesLen

	// Ensure minimum width
	if availableSpace < 10 {
		availableSpace = 10
	}

	// Truncate filename if needed
	truncatedFilename := truncateString(filename, availableSpace)

	// Return the complete formatted line
	return fmt.Sprintf("\r\033[K\033[37m📄 %s%s",
		truncatedFilename,
		baseFormat)
}

// monitorProgress starts a goroutine to monitor and display analysis progress
func monitorProgress(ctx context.Context, doneChan chan struct{}, filename string, duration time.Duration,
	totalChunks int, chunkCount *int64, startTime time.Time) {

	lastChunkCount := int64(0)
	lastProgressUpdate := startTime

	// Moving average window for chunks/sec calculation
	const windowSize = 10 // Number of samples to average
	chunkRates := make([]float64, 0, windowSize)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-doneChan:
			return
		case <-ticker.C:
			currentTime := time.Now()
			timeSinceLastUpdate := currentTime.Sub(lastProgressUpdate)

			// Get current chunk count atomically
			currentCount := atomic.LoadInt64(chunkCount)

			// Calculate current chunk rate
			chunksProcessed := currentCount - lastChunkCount
			currentRate := float64(chunksProcessed) / timeSinceLastUpdate.Seconds()

			// Update moving average
			if len(chunkRates) >= windowSize {
				// Remove oldest value
				chunkRates = chunkRates[1:]
			}
			chunkRates = append(chunkRates, currentRate)

			// Calculate average rate
			var avgRate float64
			if len(chunkRates) > 0 {
				sum := 0.0
				for _, rate := range chunkRates {
					sum += rate
				}
				avgRate = sum / float64(len(chunkRates))
			}

			// Update counters for next iteration
			lastChunkCount = currentCount
			lastProgressUpdate = currentTime

			// Get terminal width
			width, _, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil {
				width = 80 // Default to 80 columns if we can't get terminal width
			}

			// Format and print the progress line
			fmt.Print(formatProgressLine(
				filename,
				duration,
				int(currentCount),
				totalChunks,
				avgRate,
				birdnet.EstimateTimeRemaining(startTime, int(currentCount), totalChunks),
				width,
			))
		}
	}
}

// processChunk handles the processing of a single audio chunk
func processChunk(ctx context.Context, chunk audioChunk, settings *conf.Settings,
	resultChan chan<- []datastore.Note, errorChan chan<- error) error {

	notes, err := bn.ProcessChunk(chunk.Data, chunk.FilePosition)
	if err != nil {
		// Block until we can send the error or context is cancelled
		select {
		case errorChan <- err:
			// Error successfully sent
		case <-ctx.Done():
			// If context is done while trying to send error, prioritize context error
			return ctx.Err()
		}
		return err
	}

	// Filter notes based on included species list
	var filteredNotes []datastore.Note
	for i := range notes {
		if settings.IsSpeciesIncluded(notes[i].ScientificName) {
			filteredNotes = append(filteredNotes, notes[i])
		}
	}

	// Block until we can send results or context is cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	case resultChan <- filteredNotes:
		return nil
	}
}

// startWorkers initializes and starts the worker goroutines for audio analysis
func startWorkers(ctx context.Context, numWorkers int, chunkChan chan audioChunk,
	resultChan chan []datastore.Note, errorChan chan error, settings *conf.Settings) {

	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			if settings.Debug {
				fmt.Printf("DEBUG: Worker %d started\n", workerID)
			}
			defer func() {
				if settings.Debug {
					fmt.Printf("DEBUG: Worker %d finished\n", workerID)
				}
			}()

			for chunk := range chunkChan {
				select {
				case <-ctx.Done():
					select {
					case errorChan <- ctx.Err():
					default:
					}
					return
				default:
				}

				if err := processChunk(ctx, chunk, settings, resultChan, errorChan); err != nil {
					if settings.Debug {
						fmt.Printf("DEBUG: Worker %d encountered error: %v\n", workerID, err)
					}
					return
				}
			}
		}(i)
	}
}

// processAudioFile conducts an analysis of an audio file and outputs the results.
func processAudioFile(settings *conf.Settings, audioInfo *myaudio.AudioInfo, ctx context.Context) ([]datastore.Note, error) {
	// Calculate total chunks
	totalChunks := myaudio.GetTotalChunks(
		audioInfo.SampleRate,
		audioInfo.TotalSamples,
		settings.BirdNET.Overlap,
	)

	// Calculate audio duration
	duration := time.Duration(float64(audioInfo.TotalSamples) / float64(audioInfo.SampleRate) * float64(time.Second))

	// Get filename and truncate if necessary
	filename := filepath.Base(settings.Input.Path)

	startTime := time.Now()
	var chunkCount int64 = 1
	var eofReached int32 = 0 // Atomic flag to indicate EOF was reached

	// Set number of workers to 1
	numWorkers := 1

	if settings.Debug {
		fmt.Printf("DEBUG: Starting analysis with %d total chunks and %d workers\n", totalChunks, numWorkers)
	}

	// Setup processing channels
	processingChannels := setupProcessingChannels()

	var allNotes []datastore.Note

	// Create a single cancel function to coordinate shutdown
	var doneChanClosed sync.Once
	shutdown := func() {
		doneChanClosed.Do(func() {
			close(processingChannels.doneChan)
		})
	}
	defer shutdown()

	// Start worker goroutines
	startWorkers(ctx, numWorkers, processingChannels.chunkChan, processingChannels.resultChan, processingChannels.errorChan, settings)

	// Start progress monitoring goroutine
	go monitorProgress(ctx, processingChannels.doneChan, filename, duration, totalChunks, &chunkCount, startTime)

	// Start result collector goroutine
	errHolder := &errorHolder{}

	// Start the collector that manages analysis results and errors
	go collectResults(
		ctx,
		settings,
		totalChunks,
		&chunkCount,
		&eofReached,
		processingChannels,
		&allNotes,
		errHolder,
		shutdown,
	)

	// Process audio data from file
	err := processAudioData(
		settings,
		ctx,
		processingChannels,
		errHolder,
	)

	if settings.Debug {
		fmt.Println("DEBUG: Finished reading audio file")
	}
	close(processingChannels.chunkChan)

	if settings.Debug {
		fmt.Println("DEBUG: Waiting for processing to complete")
	}
	<-processingChannels.doneChan // Wait for processing to complete

	// Handle errors
	if err := handleProcessingErrors(err, errHolder.getError(), settings); err != nil {
		return allNotes, err
	}

	// Display results
	displayProcessingResults(settings, filename, duration, chunkCount, startTime)

	return allNotes, nil
}

// setupProcessingChannels creates and returns all channels needed for processing
func setupProcessingChannels() processingChannels {
	return processingChannels{
		chunkChan:  make(chan audioChunk, 4),
		resultChan: make(chan []datastore.Note, 4),
		errorChan:  make(chan error, 1),
		doneChan:   make(chan struct{}),
		eofChan:    make(chan struct{}, 1),
	}
}

// collectResults collects and processes analysis results
func collectResults(
	ctx context.Context,
	settings *conf.Settings,
	expectedChunks int,
	chunkCount *int64,
	eofReached *int32,
	channels processingChannels,
	allNotes *[]datastore.Note,
	errHolder *errorHolder,
	shutdown func(),
) {
	if settings.Debug {
		fmt.Println("DEBUG: Result collector started")
	}
	defer shutdown()

	for i := 1; i <= expectedChunks; i++ {
		select {
		case <-ctx.Done():
			errHolder.setError(ctx.Err())
			return
		case notes := <-channels.resultChan:
			if settings.Debug {
				fmt.Printf("DEBUG: Received results for chunk #%d\n", atomic.LoadInt64(chunkCount))
			}
			*allNotes = append(*allNotes, notes...)
			atomic.AddInt64(chunkCount, 1)

			// If EOF was reached and we've processed all chunks we've sent, we're done
			if atomic.LoadInt32(eofReached) == 1 &&
				atomic.LoadInt64(chunkCount) > int64(i) {
				if settings.Debug {
					fmt.Printf("DEBUG: EOF reached and all chunks processed (actual chunks: %d, expected: %d)\n",
						i, expectedChunks)
				}
				return
			}

			if atomic.LoadInt64(chunkCount) > int64(expectedChunks) {
				return
			}
		case err := <-channels.errorChan:
			if settings.Debug {
				fmt.Printf("DEBUG: Collector received error: %v\n", err)
			}
			errHolder.setError(err)
			return
		case <-channels.eofChan:
			handleEOFSignal(settings, eofReached, chunkCount, i)
		case <-time.After(5 * time.Second):
			handleTimeout(settings, eofReached, chunkCount, i, expectedChunks, errHolder)
			return
		}
	}
	if settings.Debug {
		fmt.Println("DEBUG: Collector finished normally")
	}
}

// handleEOFSignal processes EOF notification
func handleEOFSignal(settings *conf.Settings, eofReached *int32, chunkCount *int64, currentChunk int) bool {
	if settings.Debug {
		fmt.Printf("DEBUG: Received EOF signal, waiting for remaining chunks to process\n")
	}
	// Mark EOF as reached - we still need to process any chunks in flight
	atomic.StoreInt32(eofReached, 1)

	// If we've already processed all chunks, we can return
	if atomic.LoadInt64(chunkCount) > int64(currentChunk) {
		return true
	}
	return false
}

// handleTimeout handles timeouts during processing
func handleTimeout(
	settings *conf.Settings,
	eofReached *int32,
	chunkCount *int64,
	currentChunk int,
	totalChunks int,
	errHolder *errorHolder,
) {
	// If EOF was reached but we're still waiting for chunks, something is wrong
	if atomic.LoadInt32(eofReached) == 1 {
		if settings.Debug {
			fmt.Printf("DEBUG: Timeout waiting after EOF, processed %d chunks\n", currentChunk-1)
		}
		return
	}

	if settings.Debug {
		fmt.Printf("DEBUG: Timeout waiting for chunk %d results\n", currentChunk)
	}
	currentCount := atomic.LoadInt64(chunkCount)
	errHolder.setError(fmt.Errorf("timeout waiting for analysis results (processed %d/%d chunks)", currentCount, totalChunks))
}

// processAudioData reads and processes audio file data
func processAudioData(
	settings *conf.Settings,
	ctx context.Context,
	channels processingChannels,
	errHolder *errorHolder,
) error {
	// Initialize filePosition before the loop
	filePosition := time.Time{}

	// Read and send audio chunks with timing information
	return myaudio.ReadAudioFileBuffered(settings, func(chunkData []float32, isEOF bool) error {
		return handleAudioChunk(
			ctx,
			chunkData,
			isEOF,
			filePosition,
			settings,
			channels,
			errHolder,
		)
	})
}

// handleAudioChunk processes a single audio chunk
func handleAudioChunk(
	ctx context.Context,
	chunkData []float32,
	isEOF bool,
	filePosition time.Time,
	settings *conf.Settings,
	channels processingChannels,
	errHolder *errorHolder,
) error {
	// If this is just an EOF signal with no data, notify and return
	if isEOF && len(chunkData) == 0 {
		select {
		case channels.eofChan <- struct{}{}:
			if settings.Debug {
				fmt.Println("DEBUG: Sent EOF signal without data")
			}
		default:
			// Channel full, that's okay
		}
		return nil
	}

	// Process the chunk data if we have any
	if len(chunkData) > 0 {
		chunk := audioChunk{
			Data:         chunkData,
			FilePosition: filePosition,
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case channels.chunkChan <- chunk:
			// If this is the last chunk, also send EOF signal
			if isEOF {
				select {
				case channels.eofChan <- struct{}{}:
					if settings.Debug {
						fmt.Println("DEBUG: Sent EOF signal with data")
					}
				default:
					// Channel full, that's okay
				}
			}
			return nil
		case <-channels.doneChan:
			err := errHolder.getError()
			if err != nil {
				return err
			}
			return ctx.Err() // Return context error if no processing error
		case <-time.After(5 * time.Second):
			return fmt.Errorf("timeout sending chunk to processing")
		}
	}

	return nil
}

// handleProcessingErrors processes errors from audio analysis
func handleProcessingErrors(err, processingError error, settings *conf.Settings) error {
	if err != nil {
		if settings.Debug {
			fmt.Printf("DEBUG: File processing error: %v\n", err)
		}
		if errors.Is(err, context.Canceled) {
			return ErrAnalysisCanceled
		}
		return fmt.Errorf("error processing audio: %w", err)
	}

	if processingError != nil {
		if settings.Debug {
			fmt.Printf("DEBUG: Processing error encountered: %v\n", processingError)
		}
		if errors.Is(processingError, context.Canceled) {
			return ErrAnalysisCanceled
		}
		return processingError
	}

	return nil
}

// displayProcessingResults shows final processing statistics
func displayProcessingResults(settings *conf.Settings, filename string, duration time.Duration, chunkCount int64, startTime time.Time) {
	if settings.Debug {
		fmt.Println("DEBUG: Analysis completed successfully")
	}

	// Calculate actual processed chunks
	actualChunks := int(atomic.LoadInt64(&chunkCount)) - 1

	// Update final statistics
	totalTime := time.Since(startTime)
	avgChunksPerSec := float64(actualChunks) / totalTime.Seconds()

	// Get terminal width for final status line
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		width = 80 // Default to 80 columns if we can't get terminal width
	}

	// Format and print the final status line
	fmt.Print(formatProgressLine(
		filename,
		duration,
		actualChunks,
		actualChunks, // Use actual chunks processed instead of calculated total
		avgChunksPerSec,
		fmt.Sprintf("in %s", birdnet.FormatDuration(totalTime)),
		width,
	))
	fmt.Println() // Add newline after completion
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
