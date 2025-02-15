package analysis

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// cleanupProcessingFiles removes all .processing files from the output directory
func cleanupProcessingFiles(outputPath string) {
	pattern := filepath.Join(outputPath, "*.processing")
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

// isProcessed checks if a file has already been processed
func isProcessed(path, outputPath string, processedFiles map[string]bool) bool {
	// Check if we have already processed this file in memory
	if processedFiles[path] {
		return true
	}

	// Get the base filename without extension
	baseName := filepath.Base(path)

	// Check for output files
	outputPathCSV := filepath.Join(outputPath, baseName+".csv")
	outputPathTable := filepath.Join(outputPath, baseName+".txt")
	outputPathProcessing := filepath.Join(outputPath, baseName+".processing")

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
	if info, err := os.Stat(outputPathProcessing); err == nil {
		age := time.Since(info.ModTime())
		log.Printf("Found lock file %s, age: %v", outputPathProcessing, age.Round(time.Second))
		// If lock file exists but is stale, remove it
		if age > 60*time.Minute {
			log.Printf("Lock file is stale (older than 60 minutes), removing: %s", outputPathProcessing)
			os.Remove(outputPathProcessing)
			return false
		}
		return true // File is being processed by another instance
	}

	return false
}

// isFileLocked checks if a file is currently being written to
func isFileLocked(path string) bool {
	// Try to open file with shared read access
	var flag int
	if runtime.GOOS == "windows" {
		flag = os.O_RDONLY
	} else {
		flag = os.O_RDONLY | syscall.O_NONBLOCK
	}

	file, err := os.OpenFile(path, flag, 0o666)
	if err != nil {
		// File is probably locked by another process
		return true
	}
	file.Close()

	// On Windows, also try write access to be sure
	if runtime.GOOS == "windows" {
		file, err = os.OpenFile(path, os.O_WRONLY, 0o666)
		if err != nil {
			return true
		}
		file.Close()
	}

	return false
}

// isFileSizeStable checks if a file size remains constant over multiple checks
func isFileSizeStable(path string, ctx context.Context) (bool, error) {
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

		// Check for cancellation before sleeping
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(2 * time.Second):
			// Continue with next check
		}
	}
	return true, nil
}

// isFileReadyForProcessing checks if a file is ready to be processed
func isFileReadyForProcessing(path string, ctx context.Context) (bool, error) {
	log.Printf("Checking if file %s is ready for processing", filepath.Base(path))
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
	stable, err := isFileSizeStable(path, ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false, context.Canceled
		}
		return false, fmt.Errorf("error checking file size stability for %s: %w", filepath.Base(path), err)
	}
	if !stable {
		log.Printf("File %s size is not stable yet", filepath.Base(path))
		return false, nil
	}

	return true, nil
}

// processFile handles the analysis of a single audio file
func processFile(path string, settings *conf.Settings, processedFiles map[string]bool, ctx context.Context) (bool, error) {
	if isProcessed(path, settings.Output.File.Path, processedFiles) {
		return false, nil // File was already processed
	}

	// Check if file is ready for processing
	ready, err := isFileReadyForProcessing(path, ctx)
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
	f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
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
		if errors.Is(analysisErr, context.Canceled) {
			return false, context.Canceled
		}
		return false, fmt.Errorf("error analyzing file '%s': %w", path, analysisErr)
	}

	// Mark as processed
	processedFiles[path] = true
	return true, nil
}

// scanDirectory scans a directory for audio files and processes them
func scanDirectory(watchDir string, settings *conf.Settings, processedFiles map[string]bool, ctx context.Context) error {
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
			wasProcessed, err := processFile(path, settings, processedFiles, ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return context.Canceled
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

	if errors.Is(err, context.Canceled) {
		return context.Canceled
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

// watchDirectory continuously monitors a directory for new files
func watchDirectory(watchDir string, settings *conf.Settings, processedFiles map[string]bool, ctx context.Context) error {
	log.Printf("Starting directory watch on %s (Press Ctrl+C to stop)", watchDir)
	watchStartTime := time.Now()

	timer := time.NewTimer(0) // Start first scan immediately
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			watchDuration := time.Since(watchStartTime).Round(time.Second)
			log.Printf("Directory watch stopped after %v", watchDuration)
			cleanupProcessingFiles(settings.Output.File.Path)
			return context.Canceled

		case <-timer.C:
			// Do the scan
			if err := scanDirectory(watchDir, settings, processedFiles, ctx); err != nil {
				if errors.Is(err, context.Canceled) {
					cleanupProcessingFiles(settings.Output.File.Path)
					return context.Canceled
				}
				log.Printf("Directory scan error: %v", err)
			}

			// Reset timer for next scan with random interval
			sleepTime := 30 + rand.Intn(15)
			timer.Reset(time.Duration(sleepTime) * time.Second)
		}
	}
}

// DirectoryAnalysis processes all audio files in the given directory.
func DirectoryAnalysis(settings *conf.Settings, ctx context.Context) error {
	// Initialize BirdNET interpreter
	if err := initializeBirdNET(settings); err != nil {
		log.Printf("Failed to initialize BirdNET: %v", err)
		return err
	}

	// Ensure output directory exists
	if settings.Output.File.Path == "" {
		settings.Output.File.Path = "."
	}
	if err := os.MkdirAll(settings.Output.File.Path, 0o755); err != nil {
		log.Printf("Failed to create output directory: %v", err)
		return err
	}

	// Create a map to track processed files
	processedFiles := make(map[string]bool)

	// Do initial scan
	log.Printf("Performing initial directory scan...")
	if err := scanDirectory(settings.Input.Path, settings, processedFiles, ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		log.Printf("Initial scan failed: %v", err)
		return err
	}

	// If watch mode is not enabled, return
	if !settings.Input.Watch {
		return nil
	}

	// Start watching directory
	return watchDirectory(settings.Input.Path, settings, processedFiles, ctx)
}
