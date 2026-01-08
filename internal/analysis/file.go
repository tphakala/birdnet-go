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
	"github.com/tphakala/birdnet-go/internal/logger"
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
			// Add structured logging
			GetLogger().Info("Writing partial results before exiting due to error",
				logger.String("component", "analysis.file"),
				logger.Int("notes_count", len(notes)),
				logger.Error(err),
				logger.String("operation", "write_partial_results"))
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
	file, err := os.Open(filePath) //nolint:gosec // G304: filePath is from CLI args or directory walking
	if err != nil {
		return fmt.Errorf("\033[31m❌ Error opening file %s: %w\033[0m", filepath.Base(filePath), err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			GetLogger().Warn("failed to close audio file",
				logger.String("component", "analysis.file"),
				logger.Error(err),
				logger.String("file_path", filePath),
				logger.String("operation", "close_audio_file"))
		}
	}()

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
	availableSpace = max(availableSpace, 10)

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

	for workerID := range numWorkers {
		go func(workerID int) {
			log := GetLogger()
			log.Debug("Worker started", logger.Int("worker_id", workerID), logger.String("component", "analysis.file"), logger.String("operation", "worker_start"))
			defer func() {
				log.Debug("Worker finished", logger.Int("worker_id", workerID), logger.String("component", "analysis.file"), logger.String("operation", "worker_finish"))
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
					log.Warn("Worker encountered error", logger.Int("worker_id", workerID), logger.Error(err), logger.String("component", "analysis.file"), logger.String("operation", "process_chunk"))
					return
				}
			}
		}(workerID)
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

	GetLogger().Debug("Starting analysis",
		logger.Int("total_chunks", totalChunks),
		logger.Int("num_workers", numWorkers),
		logger.String("file", filename))

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

	GetLogger().Debug("Finished reading audio file")
	close(processingChannels.chunkChan)

	GetLogger().Debug("Waiting for processing to complete")
	<-processingChannels.doneChan // Wait for processing to complete

	// Handle errors
	if err := handleProcessingErrors(err, errHolder.getError()); err != nil {
		return allNotes, err
	}

	// Display results
	displayProcessingResults(filename, duration, chunkCount, startTime)

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
	expectedChunks int,
	chunkCount *int64,
	eofReached *int32,
	channels processingChannels,
	allNotes *[]datastore.Note,
	errHolder *errorHolder,
	shutdown func(),
) {
	GetLogger().Debug("Result collector started")
	defer shutdown()

	for i := 1; i <= expectedChunks; i++ {
		select {
		case <-ctx.Done():
			errHolder.setError(ctx.Err())
			return
		case notes := <-channels.resultChan:
			// Sample logging: only log every 10th chunk to reduce overhead
			currentChunkNum := atomic.LoadInt64(chunkCount)
			if currentChunkNum%10 == 0 || currentChunkNum == 1 {
				GetLogger().Debug("Received results for chunk", logger.Int64("chunk_number", currentChunkNum))
			}
			*allNotes = append(*allNotes, notes...)
			atomic.AddInt64(chunkCount, 1)

			// If EOF was reached and we've processed all chunks we've sent, we're done
			if atomic.LoadInt32(eofReached) == 1 &&
				atomic.LoadInt64(chunkCount) > int64(i) {
				GetLogger().Debug("EOF reached and all chunks processed",
					logger.Int("actual_chunks", i),
					logger.Int("expected_chunks", expectedChunks))
				return
			}

			if atomic.LoadInt64(chunkCount) > int64(expectedChunks) {
				return
			}
		case err := <-channels.errorChan:
			GetLogger().Warn("Collector received error", logger.Error(err))
			errHolder.setError(err)
			return
		case <-channels.eofChan:
			handleEOFSignal(eofReached, chunkCount, i)
		case <-time.After(5 * time.Second):
			handleTimeout(eofReached, chunkCount, i, expectedChunks, errHolder)
			return
		}
	}
	GetLogger().Debug("Collector finished normally")
}

// handleEOFSignal processes EOF notification
func handleEOFSignal(eofReached *int32, chunkCount *int64, currentChunk int) bool {
	GetLogger().Debug("Received EOF signal, waiting for remaining chunks to process")
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
	eofReached *int32,
	chunkCount *int64,
	currentChunk int,
	totalChunks int,
	errHolder *errorHolder,
) {
	// If EOF was reached but we're still waiting for chunks, something is wrong
	if atomic.LoadInt32(eofReached) == 1 {
		GetLogger().Warn("Timeout waiting after EOF", logger.Int("processed_chunks", currentChunk-1))
		return
	}

	GetLogger().Warn("Timeout waiting for chunk results", logger.Int("chunk_number", currentChunk))
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
		currentPosition := filePosition
		delta := 3.0 - settings.BirdNET.Overlap
		filePosition = filePosition.Add(time.Duration(delta * float64(time.Second)))

		return handleAudioChunk(
			ctx,
			chunkData,
			isEOF,
			currentPosition,
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
	channels processingChannels,
	errHolder *errorHolder,
) error {
	// If this is just an EOF signal with no data, notify and return
	if isEOF && len(chunkData) == 0 {
		select {
		case channels.eofChan <- struct{}{}:
			GetLogger().Debug("Sent EOF signal without data")
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
					GetLogger().Debug("Sent EOF signal with data")
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
func handleProcessingErrors(err, processingError error) error {
	if err != nil {
		GetLogger().Error("File processing error", logger.Error(err))
		if errors.Is(err, context.Canceled) {
			return ErrAnalysisCanceled
		}
		return fmt.Errorf("error processing audio: %w", err)
	}

	if processingError != nil {
		GetLogger().Error("Processing error encountered", logger.Error(processingError))
		if errors.Is(processingError, context.Canceled) {
			return ErrAnalysisCanceled
		}
		return processingError
	}

	return nil
}

// displayProcessingResults shows final processing statistics
func displayProcessingResults(filename string, duration time.Duration, chunkCount int64, startTime time.Time) {
	GetLogger().Info("Analysis completed successfully",
		logger.String("file", filename),
		logger.Duration("duration", duration),
		logger.Duration("processing_time", time.Since(startTime)))

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
	// Add structured logging
	GetLogger().Info("File analysis completed",
		logger.String("component", "analysis.file"),
		logger.String("filename", filename),
		logger.String("duration", duration.String()),
		logger.Int64("duration_ms", duration.Milliseconds()),
		logger.Int("chunks_processed", actualChunks),
		logger.String("processing_time", totalTime.String()),
		logger.Int64("processing_time_ms", totalTime.Milliseconds()),
		logger.Float64("avg_chunks_per_sec", avgChunksPerSec),
		logger.String("operation", "file_analysis_complete"))
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
