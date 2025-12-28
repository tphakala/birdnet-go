package analysis

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// cleanupProcessingFiles removes all .processing files from the output directory
func cleanupProcessingFiles(outputPath string) {
	log := GetLogger()
	pattern := filepath.Join(outputPath, "*.processing")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		log.Error("error finding processing files",
			logger.String("pattern", pattern),
			logger.Error(err))
		return
	}
	for _, file := range matches {
		if err := os.Remove(file); err != nil {
			log.Error("error removing processing file",
				logger.String("file", file),
				logger.Error(err))
		} else {
			log.Info("cleaned up lock file",
				logger.String("file", file))
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
		log := GetLogger()
		age := time.Since(info.ModTime())
		log.Debug("found lock file",
			logger.String("lock_file", outputPathProcessing),
			logger.Duration("age", age.Round(time.Second)))
		// If lock file exists but is stale, remove it
		if age > 60*time.Minute {
			log.Warn("lock file is stale, removing",
				logger.String("lock_file", outputPathProcessing),
				logger.Duration("age", age))
			if err := os.Remove(outputPathProcessing); err != nil {
				log.Error("failed to remove stale lock file",
					logger.String("lock_file", outputPathProcessing),
					logger.Error(err))
			}
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

	file, err := os.OpenFile(path, flag, 0o600) //nolint:gosec // G304: path is from directory walking, not user input
	if err != nil {
		// File is probably locked by another process
		return true
	}
	if err := file.Close(); err != nil {
		GetLogger().Warn("failed to close file",
			logger.String("path", path),
			logger.Error(err))
	}

	// On Windows, also try write access to be sure
	if runtime.GOOS == "windows" {
		file, err = os.OpenFile(path, os.O_WRONLY, 0o600) //nolint:gosec // G304: path is from directory walking, not user input
		if err != nil {
			return true
		}
		if err := file.Close(); err != nil {
			GetLogger().Warn("failed to close file",
				logger.String("path", path),
				logger.Error(err))
		}
	}

	return false
}

// isFileSizeStable checks if a file size remains constant over multiple checks
func isFileSizeStable(path string, ctx context.Context) (bool, error) {
	var lastSize int64
	for i := range 3 {
		info, err := os.Stat(path)
		if err != nil {
			return false, err
		}
		if info.Size() == 0 {
			return false, nil
		}

		if i > 0 && info.Size() != lastSize {
			GetLogger().Debug("file size changed, still copying",
				logger.String("file", filepath.Base(path)),
				logger.Int64("previous_size", lastSize),
				logger.Int64("current_size", info.Size()))
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
	log := GetLogger()
	fileName := filepath.Base(path)
	log.Debug("checking if file is ready for processing",
		logger.String("file", fileName))
	// Check if file is locked
	if isFileLocked(path) {
		log.Debug("file is locked, skipping",
			logger.String("file", fileName))
		return false, nil
	}

	// Check modification time
	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("error checking modification time for %s: %w", fileName, err)
	}
	if time.Since(info.ModTime()) < 30*time.Second {
		log.Debug("file was modified too recently, waiting",
			logger.String("file", fileName),
			logger.Duration("since_modified", time.Since(info.ModTime())))
		return false, nil
	}

	// Check file size stability
	stable, err := isFileSizeStable(path, ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false, context.Canceled
		}
		return false, fmt.Errorf("error checking file size stability for %s: %w", fileName, err)
	}
	if !stable {
		log.Debug("file size is not stable yet",
			logger.String("file", fileName))
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
	switch ext {
	case myaudio.ExtWAV:
		outputPath = outputPath[:len(outputPath)-len(myaudio.ExtWAV)]
	case myaudio.ExtFLAC:
		outputPath = outputPath[:len(outputPath)-len(myaudio.ExtFLAC)]
	}
	lockFile := outputPath + ".processing"

	// Try to create lock file
	f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304: lockFile derived from settings.Output.File.Path
	if err != nil {
		// Another instance is processing this file
		return false, nil
	}
	if err := f.Close(); err != nil {
		GetLogger().Warn("failed to close lock file",
			logger.String("lock_file", lockFile),
			logger.Error(err))
	}

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
		GetLogger().Warn("failed to remove lock file",
			logger.String("lock_file", lockFile),
			logger.Error(removeErr))
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
	log := GetLogger()
	log.Debug("scanning directory",
		logger.String("directory", watchDir))
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

		// Skip temporary files that are currently being written
		if strings.HasSuffix(strings.ToLower(d.Name()), ".temp") {
			return nil
		}

		// Check for both .wav and .flac files (case-insensitive)
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext == myaudio.ExtWAV || ext == myaudio.ExtFLAC {
			wasProcessed, err := processFile(path, settings, processedFiles, ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return context.Canceled
				}
				// Log other errors but continue processing other files
				log.Error("error processing file",
					logger.String("file", path),
					logger.Error(err))
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
		log.Info("directory analysis completed",
			logger.Int("files_processed", filesAnalyzed),
			logger.Duration("duration", scanDuration))
	} else {
		log.Debug("directory scan completed, no new files to analyze")
	}
	return nil
}

// watchDirectory continuously monitors a directory for new files
func watchDirectory(watchDir string, settings *conf.Settings, processedFiles map[string]bool, ctx context.Context) error {
	log := GetLogger()
	log.Info("starting directory watch",
		logger.String("directory", watchDir))
	watchStartTime := time.Now()

	timer := time.NewTimer(0) // Start first scan immediately
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			watchDuration := time.Since(watchStartTime).Round(time.Second)
			log.Info("directory watch stopped",
				logger.Duration("duration", watchDuration))
			cleanupProcessingFiles(settings.Output.File.Path)
			return context.Canceled

		case <-timer.C:
			// Do the scan
			if err := scanDirectory(watchDir, settings, processedFiles, ctx); err != nil {
				if errors.Is(err, context.Canceled) {
					cleanupProcessingFiles(settings.Output.File.Path)
					return context.Canceled
				}
				log.Error("directory scan error",
					logger.Error(err))
			}

			// Reset timer for next scan with random interval
			sleepTime := 30 + rand.IntN(15) //nolint:gosec // G404: weak randomness acceptable for scan interval jitter, not security-critical
			timer.Reset(time.Duration(sleepTime) * time.Second)
		}
	}
}

// DirectoryAnalysis processes all audio files in the given directory.
func DirectoryAnalysis(settings *conf.Settings, ctx context.Context) error {
	log := GetLogger()
	// Initialize BirdNET interpreter
	if err := initializeBirdNET(settings); err != nil {
		log.Error("failed to initialize BirdNET",
			logger.Error(err))
		return err
	}

	// Ensure output directory exists
	if settings.Output.File.Path == "" {
		settings.Output.File.Path = "."
	}
	if err := os.MkdirAll(settings.Output.File.Path, 0o750); err != nil {
		log.Error("failed to create output directory",
			logger.String("path", settings.Output.File.Path),
			logger.Error(err))
		return err
	}

	// Create a map to track processed files
	processedFiles := make(map[string]bool)

	// Do initial scan
	log.Info("performing initial directory scan",
		logger.String("path", settings.Input.Path))
	if err := scanDirectory(settings.Input.Path, settings, processedFiles, ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		log.Error("initial scan failed",
			logger.Error(err))
		return err
	}

	// If watch mode is not enabled, return
	if !settings.Input.Watch {
		return nil
	}

	// Start watching directory
	return watchDirectory(settings.Input.Path, settings, processedFiles, ctx)
}
