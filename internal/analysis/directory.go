package analysis

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// DirectoryAnalysis processes all .wav files in the given directory for analysis.
func DirectoryAnalysis(settings *conf.Settings) error {
	// Initialize BirdNET interpreter
	if err := initializeBirdNET(settings); err != nil {
		log.Printf("Failed to initialize BirdNET: %v", err)
		return err
	}

	// Store the original directory path
	watchDir := settings.Input.Path

	// Create a map to track processed files
	processedFiles := make(map[string]bool)

	// Function to check if a lock file is stale (older than 60 minutes)
	isLockFileStale := func(path string) bool {
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		// Consider lock files older than 60 minutes as stale
		return time.Since(info.ModTime()) > 60*time.Minute
	}

	// Function to clean up .processing files
	cleanupProcessingFiles := func() {
		pattern := filepath.Join(settings.Output.File.Path, "*.processing")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			log.Printf("Error finding .processing files: %v", err)
			return
		}
		for _, file := range matches {
			if err := os.Remove(file); err != nil {
				log.Printf("Error removing processing file %s: %v", file, err)
			} else {
				log.Printf("Cleaned up lock file: %s", file)
			}
		}
	}

	// Function to check if a file has been processed
	isProcessed := func(path string) bool {
		// Check if we have already processed this file in memory
		if processedFiles[path] {
			return true
		}

		// Get the base filename without extension
		baseName := filepath.Base(path)

		// Check for output files
		outputPathCSV := filepath.Join(settings.Output.File.Path, baseName+".csv")
		outputPathTable := filepath.Join(settings.Output.File.Path, baseName+".txt")
		outputPathProcessing := filepath.Join(settings.Output.File.Path, baseName+".processing")

		// Check if any of the output files exist
		if _, err := os.Stat(outputPathCSV); err == nil {
			processedFiles[path] = true
			return true
		}
		if _, err := os.Stat(outputPathTable); err == nil {
			processedFiles[path] = true
			return true
		}

		// Check for processing lock file
		if _, err := os.Stat(outputPathProcessing); err == nil {
			// If lock file exists but is stale, remove it
			if isLockFileStale(outputPathProcessing) {
				log.Printf("Removing stale lock file: %s", outputPathProcessing)
				os.Remove(outputPathProcessing)
				return false
			}
			return true // File is being processed by another instance
		}

		return false
	}

	// Function to check if file is locked (being written to)
	isFileLocked := func(path string) bool {
		// Try to open file with shared read access
		var flag int
		if runtime.GOOS == "windows" {
			flag = os.O_RDONLY
		} else {
			flag = os.O_RDONLY | syscall.O_NONBLOCK
		}

		file, err := os.OpenFile(path, flag, 0666)
		if err != nil {
			// File is probably locked by another process
			return true
		}
		file.Close()

		// On Windows, also try write access to be sure
		if runtime.GOOS == "windows" {
			file, err = os.OpenFile(path, os.O_WRONLY, 0666)
			if err != nil {
				return true
			}
			file.Close()
		}

		return false
	}

	// Function to verify WAV/FLAC file headers and size
	verifyAudioFile := func(path string) (bool, string, error) {
		file, err := os.Open(path)
		if err != nil {
			return false, "", err
		}
		defer file.Close()

		// Get actual file size
		info, err := os.Stat(path)
		if err != nil {
			return false, "", err
		}
		actualSize := info.Size()

		// Read first 4 bytes to identify format
		header := make([]byte, 4)
		n, err := file.Read(header)
		if err != nil || n != 4 {
			return false, "", fmt.Errorf("failed to read header: %v", err)
		}

		// Check if it's a WAV file (case insensitive)
		if strings.EqualFold(string(header), "RIFF") {
			// WAV header structure:
			// Offset  Size  Name        Description
			// 0-3     4    ChunkID     Contains "RIFF" (already read)
			// 4-7     4    ChunkSize   File size - 8
			// 8-11    4    Format      Contains "WAVE"
			// ... subchunks follow

			// Read chunk size (4 bytes, little-endian)
			var chunkSize uint32
			if err := binary.Read(file, binary.LittleEndian, &chunkSize); err != nil {
				return false, "", fmt.Errorf("failed to read WAV chunk size: %v", err)
			}

			// Read format (4 bytes)
			format := make([]byte, 4)
			if n, err := file.Read(format); err != nil || n != 4 {
				return false, "", fmt.Errorf("failed to read WAV format: %v", err)
			}

			if !strings.EqualFold(string(format), "WAVE") {
				return false, "invalid WAV format", nil
			}

			// Calculate expected file size
			// ChunkSize is file size - 8 bytes (as per WAV spec)
			expectedSize := uint64(chunkSize) + 8

			// Convert actual size to uint64 for comparison
			actualSizeU64 := uint64(actualSize)

			// If file is smaller than header indicates, it's definitely incomplete
			if actualSizeU64 < expectedSize {
				return false, fmt.Sprintf("incomplete WAV file: expected %d bytes, got %d", expectedSize, actualSizeU64), nil
			}

			// Allow for small differences (up to 1KB) due to block sizes
			sizeDiff := actualSizeU64 - expectedSize
			if sizeDiff > 1024 {
				return false, fmt.Sprintf("WAV file size mismatch: expected %d bytes, got %d (difference too large: %d bytes)",
					expectedSize, actualSizeU64, sizeDiff), nil
			}
		} else if strings.EqualFold(string(header), "fLaC") {
			// FLAC format:
			// - 4 bytes: "fLaC" marker
			// - METADATA_BLOCK_HEADER (4 bytes)
			//   - 1 bit: Last-metadata-block flag
			//   - 7 bits: BLOCK_TYPE
			//   - 24 bits: Length of metadata to follow
			// - METADATA_BLOCK_DATA (variable size)

			// Read METADATA_BLOCK_HEADER
			var metaHeader struct {
				Flags  uint8  // Last-metadata-block flag (1 bit) + BLOCK_TYPE (7 bits)
				Length uint32 // 24-bit length, will read into 32-bit uint
			}

			// Read flags byte
			if err := binary.Read(file, binary.BigEndian, &metaHeader.Flags); err != nil {
				return false, "", fmt.Errorf("failed to read FLAC metadata flags: %v", err)
			}

			// Read 24-bit length (3 bytes)
			lengthBytes := make([]byte, 3)
			if n, err := file.Read(lengthBytes); err != nil || n != 3 {
				return false, "", fmt.Errorf("failed to read FLAC metadata length: %v", err)
			}
			// Convert 3 bytes to uint32 (FLAC uses big-endian)
			metaHeader.Length = uint32(lengthBytes[0])<<16 | uint32(lengthBytes[1])<<8 | uint32(lengthBytes[2])

			if metaHeader.Length != 34 {
				return false, fmt.Sprintf("invalid FLAC STREAMINFO block size: %d", metaHeader.Length), nil
			}

			// Read STREAMINFO block
			streamInfo := make([]byte, metaHeader.Length)
			n, err := file.Read(streamInfo)
			if err != nil || uint32(n) != metaHeader.Length {
				return false, "failed to read FLAC STREAMINFO block", nil
			}

			// Minimum valid FLAC size = marker(4) + header(4) + streaminfo(34) = 42 bytes
			minSize := int64(42)
			if actualSize < minSize {
				return false, fmt.Sprintf("incomplete FLAC file: size %d bytes is smaller than minimum %d", actualSize, minSize), nil
			}

			// FLAC files don't store total file size in header, but we can verify basic structure
			// Additional checks could be added here for sample rate, channels, etc.
		} else {
			return false, fmt.Sprintf("unknown audio format (neither WAV nor FLAC)"), nil
		}

		return true, "", nil
	}

	// Function to check file size stability with multiple checks
	isFileSizeStable := func(path string) (bool, error) {
		var lastSize int64
		for i := 0; i < 3; i++ {
			info, err := os.Stat(path)
			if err != nil {
				return false, err
			}
			if info.Size() == 0 {
				return false, nil
			}

			if i > 0 && info.Size() != lastSize {
				log.Printf("File %s size changed from %d to %d, still copying...",
					filepath.Base(path), lastSize, info.Size())
				return false, nil
			}
			lastSize = info.Size()

			time.Sleep(2 * time.Second)
		}
		return true, nil
	}

	// Function to check if file is ready for processing
	isFileReadyForProcessing := func(path string) (bool, error) {
		// First verify the audio file headers and size
		valid, reason, err := verifyAudioFile(path)
		if err != nil {
			return false, fmt.Errorf("error verifying audio file %s: %w", filepath.Base(path), err)
		}
		if !valid {
			log.Printf("File %s is not ready: %s", filepath.Base(path), reason)
			return false, nil
		}

		// Check if file is locked
		if isFileLocked(path) {
			log.Printf("File %s is locked, skipping...", filepath.Base(path))
			return false, nil
		}

		// Check modification time
		info, err := os.Stat(path)
		if err != nil {
			return false, fmt.Errorf("error checking modification time for %s: %w", filepath.Base(path), err)
		}
		if time.Since(info.ModTime()) < 30*time.Second {
			log.Printf("File %s was modified too recently, waiting...", filepath.Base(path))
			return false, nil
		}

		// Check file size stability
		stable, err := isFileSizeStable(path)
		if err != nil {
			return false, fmt.Errorf("error checking file size stability for %s: %w", filepath.Base(path), err)
		}
		if !stable {
			log.Printf("File %s size is not stable yet", filepath.Base(path))
			return false, nil
		}

		return true, nil
	}

	// Function to process a single file
	processFile := func(path string, ctx context.Context) (bool, error) {
		if isProcessed(path) {
			return false, nil // File was already processed
		}

		// Check if file is ready for processing
		ready, err := isFileReadyForProcessing(path)
		if err != nil {
			return false, fmt.Errorf("error checking file readiness: %w", err)
		}
		if !ready {
			return false, nil
		}

		// Check for context cancellation
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}

		// Create processing lock file
		outputPath := filepath.Join(settings.Output.File.Path, filepath.Base(path))
		ext := strings.ToLower(filepath.Ext(outputPath))
		if ext == ".wav" {
			outputPath = outputPath[:len(outputPath)-4]
		} else if ext == ".flac" {
			outputPath = outputPath[:len(outputPath)-5]
		}
		lockFile := outputPath + ".processing"

		// Try to create lock file
		f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
		if err != nil {
			// Another instance is processing this file
			return false, nil
		}
		f.Close()

		// Save the original path and restore it after processing
		origPath := settings.Input.Path
		settings.Input.Path = path

		// Create a new context with cancellation for FileAnalysis
		analysisCtx, cancelAnalysis := context.WithCancel(ctx)
		defer cancelAnalysis()

		// Run FileAnalysis in a goroutine so we can handle interruption
		analysisDone := make(chan error)
		go func() {
			analysisDone <- FileAnalysis(settings, analysisCtx)
		}()

		// Wait for either completion or interruption
		var analysisErr error
		select {
		case analysisErr = <-analysisDone:
			// Analysis completed normally
		case <-ctx.Done():
			// Cancellation requested
			cancelAnalysis()             // Signal FileAnalysis to stop
			analysisErr = <-analysisDone // Wait for FileAnalysis to clean up
		}

		settings.Input.Path = origPath

		// Remove lock file regardless of processing result
		if removeErr := os.Remove(lockFile); removeErr != nil {
			log.Printf("Warning: failed to remove lock file %s: %v", lockFile, removeErr)
		}

		if analysisErr != nil {
			if analysisErr == context.Canceled {
				log.Printf("Analysis of file '%s' was interrupted", path)
				return false, nil
			}
			return false, fmt.Errorf("error analyzing file '%s': %w", path, analysisErr)
		}

		// Mark as processed
		processedFiles[path] = true
		return true, nil // File was successfully processed
	}

	// Function to scan directory for files
	scanDir := func(ctx context.Context) error {
		log.Printf("Scanning directory: %s", watchDir)
		startTime := time.Now()
		filesAnalyzed := 0

		err := filepath.WalkDir(watchDir, func(path string, d os.DirEntry, err error) error {
			// Check for context cancellation
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if err != nil {
				return fmt.Errorf("error accessing path %s: %w", path, err)
			}

			if d.IsDir() {
				// If recursion is not enabled and this is a subdirectory, skip it
				if !settings.Input.Recursive && path != watchDir {
					return filepath.SkipDir
				}
				return nil
			}

			// Check for both .wav and .flac files (case-insensitive)
			ext := strings.ToLower(filepath.Ext(d.Name()))
			if ext == ".wav" || ext == ".flac" {
				wasProcessed, err := processFile(path, ctx)
				if err != nil {
					if err == context.Canceled {
						return err // Propagate cancellation
					}
					// Log other errors but continue processing other files
					log.Printf("Error processing file '%s': %v", path, err)
					return nil
				}
				if wasProcessed {
					filesAnalyzed++
				}
			}
			return nil
		})

		if err == context.Canceled {
			log.Printf("Directory scan interrupted")
			return nil
		}

		if err != nil {
			return fmt.Errorf("error walking directory: %w", err)
		}

		if filesAnalyzed > 0 {
			scanDuration := time.Since(startTime)
			log.Printf("Directory analysis completed, processed %d new file(s) in %v", filesAnalyzed, scanDuration)
		} else {
			log.Printf("Directory scan completed, no new files to analyze")
		}
		return nil
	}

	// Ensure output directory exists if specified
	if settings.Output.File.Path == "" {
		// If no output path specified, use current directory
		settings.Output.File.Path = "."
	}

	if err := os.MkdirAll(settings.Output.File.Path, 0755); err != nil {
		log.Printf("Failed to create output directory: %v", err)
		return err
	}

	// Do initial scan before starting watch mode
	log.Printf("Performing initial directory scan...")
	if err := scanDir(context.Background()); err != nil {
		log.Printf("Initial scan failed: %v", err)
		return err
	}

	// If watch mode is not enabled, return
	if !settings.Input.Watch {
		return nil
	}

	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Handle shutdown in a separate goroutine
	go func() {
		sig := <-sigChan
		fmt.Print("\n") // Add newline before the interrupt message
		log.Printf("Received signal %v, initiating graceful shutdown...", sig)
		cleanupProcessingFiles()
		cancel()
	}()

	// Ensure cleanup happens even on panic or early return
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovering from panic: %v", r)
			cleanupProcessingFiles()
			panic(r) // re-panic after cleanup
		}
		// Cancel context if not already done
		cancel()
		// Clean up any remaining processing files
		cleanupProcessingFiles()
	}()

	// Watch mode - continuously scan with random intervals
	log.Printf("Starting directory watch on %s (Press Ctrl+C to stop)", watchDir)
	watchStartTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			watchDuration := time.Since(watchStartTime)
			log.Printf("Directory watch stopped after %v", watchDuration)
			cleanupProcessingFiles() // One final cleanup before returning
			return nil
		default:
			// Random sleep between 30-45 seconds
			sleepTime := 30 + rand.Intn(15)

			// Use a timer instead of time.Sleep to make it interruptible
			timer := time.NewTimer(time.Duration(sleepTime) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				watchDuration := time.Since(watchStartTime)
				log.Printf("Directory watch stopped after %v", watchDuration)
				cleanupProcessingFiles() // One final cleanup before returning
				return nil
			case <-timer.C:
				if err := scanDir(ctx); err != nil {
					log.Printf("Directory scan error: %v", err)
					// Don't exit on scan errors, just log them
				}
			}
		}
	}
}
