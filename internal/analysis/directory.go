package analysis

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
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
		file, err := os.OpenFile(path, os.O_RDWR, 0666)
		if err != nil {
			// File is probably locked by another process
			return true
		}
		file.Close()
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
			// Read rest of WAV header
			wavHeader := make([]byte, 40) // 44 - 4 already read
			n, err := file.Read(wavHeader)
			if err != nil || n != 40 {
				return false, "", fmt.Errorf("failed to read WAV header: %v", err)
			}

			if !strings.EqualFold(string(wavHeader[4:8]), "WAVE") {
				return false, "invalid WAV format", nil
			}

			// Get expected file size from header (RIFF chunk size is at bytes 4-7 of the original header)
			// Since we've already read the first 4 bytes, we need to use the first 4 bytes of wavHeader
			expectedSize := int64(wavHeader[0]) | int64(wavHeader[1])<<8 | int64(wavHeader[2])<<16 | int64(wavHeader[3])<<24
			expectedSize += 8 // Add 8 bytes for RIFF header

			// If file is smaller than header indicates, it's definitely incomplete
			if actualSize < expectedSize {
				return false, fmt.Sprintf("incomplete WAV file: expected %d bytes, got %d", expectedSize, actualSize), nil
			}

			// Allow for small differences (up to 1KB) due to block sizes
			sizeDiff := actualSize - expectedSize
			if sizeDiff > 1024 {
				return false, fmt.Sprintf("WAV file size mismatch: expected %d bytes, got %d (difference too large: %d bytes)",
					expectedSize, actualSize, sizeDiff), nil
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
			metaHeader := make([]byte, 4)
			n, err := file.Read(metaHeader)
			if err != nil || n != 4 {
				return false, "failed to read FLAC metadata header", nil
			}

			// Get length of STREAMINFO block (should be 34 bytes)
			streamInfoLength := int64(metaHeader[1])<<16 | int64(metaHeader[2])<<8 | int64(metaHeader[3])

			if streamInfoLength != 34 {
				return false, fmt.Sprintf("invalid FLAC STREAMINFO block size: %d", streamInfoLength), nil
			}

			// Read STREAMINFO block
			streamInfo := make([]byte, streamInfoLength)
			n, err = file.Read(streamInfo)
			if err != nil || int64(n) != streamInfoLength {
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
			log.Printf("Error verifying audio file %s: %v", filepath.Base(path), err)
			return false, nil
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
			return false, err
		}
		if time.Since(info.ModTime()) < 30*time.Second {
			log.Printf("File %s was modified too recently, waiting...", filepath.Base(path))
			return false, nil
		}

		// Check file size stability
		stable, err := isFileSizeStable(path)
		if err != nil || !stable {
			return false, err
		}

		return true, nil
	}

	// Function to process a single file
	processFile := func(path string) (bool, error) {
		if isProcessed(path) {
			return false, nil // File was already processed
		}

		// Check if file is ready for processing
		ready, err := isFileReadyForProcessing(path)
		if err != nil {
			log.Printf("Error checking file readiness for %s: %v", filepath.Base(path), err)
			return false, nil
		}
		if !ready {
			return false, nil
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
		err = FileAnalysis(settings)
		settings.Input.Path = origPath

		// Remove lock file regardless of processing result
		os.Remove(lockFile)

		if err != nil {
			log.Printf("Error analyzing file '%s': %v", path, err)
			return false, nil
		}

		// Mark as processed
		processedFiles[path] = true
		return true, nil // File was successfully processed
	}

	// Function to scan directory for files
	scanDir := func() error {
		log.Printf("Scanning directory: %s", watchDir)
		startTime := time.Now()
		filesAnalyzed := 0

		err := filepath.WalkDir(watchDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
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
				wasProcessed, err := processFile(path)
				if err != nil {
					return err
				}
				if wasProcessed {
					filesAnalyzed++
				}
			}
			return nil
		})

		if filesAnalyzed > 0 {
			scanDuration := time.Since(startTime)
			log.Printf("Directory analysis completed, processed %d new file(s) in %v", filesAnalyzed, scanDuration)
		} else {
			log.Printf("Directory scan completed, no new files to analyze")
		}
		return err
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
	if err := scanDir(); err != nil {
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
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	// Handle shutdown in a separate goroutine
	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, initiating graceful shutdown...", sig)
		cleanupProcessingFiles()
		cancel()
	}()

	// Watch mode - continuously scan with random intervals
	log.Printf("Starting directory watch on %s (Press Ctrl+C to stop)", watchDir)
	watchStartTime := time.Now()

	// Ensure cleanup happens even on panic
	defer cleanupProcessingFiles()

	for {
		select {
		case <-ctx.Done():
			watchDuration := time.Since(watchStartTime)
			log.Printf("Directory watch stopped after %v", watchDuration)
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
				return nil
			case <-timer.C:
				if err := scanDir(); err != nil {
					log.Printf("Directory scan error: %v", err)
					// Don't exit on scan errors, just log them
				}
			}
		}
	}
}
