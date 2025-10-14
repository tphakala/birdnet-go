// policy_common.go - shared code for cleanup policies
package diskmanager

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// Package-level logger specific to diskmanager service
var (
	serviceLogger   *slog.Logger
	serviceLevelVar = new(slog.LevelVar) // Dynamic level control
	closeLogger     func() error
	
	// Thread-safe diskMetrics with explicit synchronization
	diskMetrics     *metrics.DiskManagerMetrics // Package-level metrics
	diskMetricsMu   sync.RWMutex                // Protects diskMetrics access
	metricsInitOnce sync.Once                   // Ensures SetMetrics is called only once
)

func init() {
	var err error
	// Define log file path relative to working directory
	logFilePath := filepath.Join("logs", "diskmanager.log")
	initialLevel := slog.LevelDebug // Set desired initial level
	serviceLevelVar.Set(initialLevel)

	// Initialize the service-specific file logger
	// Using Debug level for file logging to capture more detail
	serviceLogger, closeLogger, err = logging.NewFileLogger(logFilePath, "diskmanager", serviceLevelVar)
	if err != nil {
		// Fallback: Log error to standard log and disable service logging
		log.Printf("FATAL: Failed to initialize diskmanager file logger at %s: %v. Service logging disabled.", logFilePath, err)
		// Set logger to a disabled handler to prevent nil panics, but respects level var
		fbHandler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: serviceLevelVar})
		serviceLogger = slog.New(fbHandler).With("service", "diskmanager")
		closeLogger = func() error { return nil } // No-op closer
	}
}

// GetLogger returns the package-level logger for the diskmanager service.
func GetLogger() *slog.Logger {
	return serviceLogger
}

// SetMetrics sets the metrics instance for the diskmanager package.
// This function is thread-safe and ensures metrics are set only once.
// Subsequent calls will be ignored to prevent race conditions.
func SetMetrics(m *metrics.DiskManagerMetrics) {
	metricsInitOnce.Do(func() {
		diskMetricsMu.Lock()
		defer diskMetricsMu.Unlock()
		diskMetrics = m
	})
}

// getMetrics safely returns the current metrics instance.
// This function is thread-safe and returns nil if metrics haven't been initialized.
func getMetrics() *metrics.DiskManagerMetrics {
	diskMetricsMu.RLock()
	defer diskMetricsMu.RUnlock()
	return diskMetrics
}

// updateDiskUsageMetrics updates disk usage metrics if metrics are available
func updateDiskUsageMetrics(info DiskSpaceInfo) {
	if m := getMetrics(); m != nil {
		m.UpdateDiskUsage(info.UsedBytes, info.TotalBytes)
	}
}

// buildSpeciesSubDirCountMap creates a map to track the number of files per species per subdirectory.
func buildSpeciesSubDirCountMap(files []FileInfo) map[string]map[string]int {
	serviceLogger.Debug("Building species subdirectory count map",
		"policy", "diskmanager", // Generic policy name since this function is called by both policies
		"file_count", len(files))
	speciesCount := make(map[string]map[string]int)
	for _, file := range files {
		subDir := filepath.Dir(file.Path)
		if _, exists := speciesCount[file.Species]; !exists {
			speciesCount[file.Species] = make(map[string]int)
		}
		speciesCount[file.Species][subDir]++
	}
	serviceLogger.Debug("Species subdirectory count map built",
		"policy", "diskmanager",
		"species_count", len(speciesCount))
	return speciesCount
}

// buildSpeciesTotalCountMap creates a map to track the total number of files per species across all subdirectories.
func buildSpeciesTotalCountMap(files []FileInfo) map[string]int {
	serviceLogger.Debug("Building species total count map",
		"policy", "diskmanager",
		"file_count", len(files))
	speciesTotalCount := make(map[string]int)
	for _, file := range files {
		speciesTotalCount[file.Species]++
	}
	serviceLogger.Debug("Species total count map built",
		"policy", "diskmanager",
		"species_count", len(speciesTotalCount))
	return speciesTotalCount
}

// checkLocked checks if a file should be skipped because it's locked.
func checkLocked(file *FileInfo, debug bool) bool {
	if file.Locked {
		if debug {
			log.Printf("Skipping locked file: %s", file.Path)
		}
		serviceLogger.Debug("Skipping locked file",
			"path", file.Path,
			"species", file.Species)
		return true // Indicates the file should be skipped
	}
	return false // Indicates the file should NOT be skipped
}

// checkMinClips checks if a file can be deleted based on the minimum clips per species constraint.
// Returns true if deletion is allowed, false otherwise.
func checkMinClips(file *FileInfo, subDir string, speciesCount map[string]map[string]int,
	minClipsPerSpecies int, debug bool, policy string) bool {

	// Ensure the species and subdirectory exist in the map
	if speciesMap, ok := speciesCount[file.Species]; ok {
		if count, ok := speciesMap[subDir]; ok {
			if count <= minClipsPerSpecies {
				if debug {
					log.Printf("Species clip count for %s in %s is at the minimum threshold (%d). Skipping file deletion.",
						file.Species, subDir, minClipsPerSpecies)
				}
				serviceLogger.Debug("Species count at minimum threshold, skipping deletion",
					"policy", policy,
					"species", file.Species,
					"subdirectory", subDir,
					"count", count,
					"min_threshold", minClipsPerSpecies,
					"path", file.Path)
				return false // Cannot delete
			}
		} else {
			// Should not happen if map is built correctly, but handle defensively
			if debug {
				log.Printf("Warning: Subdirectory %s not found in species count map for species %s.", subDir, file.Species)
			}
			serviceLogger.Warn("Subdirectory not found in species count map",
				"policy", policy,
				"subdirectory", subDir,
				"species", file.Species,
				"path", file.Path)
			return false // Cannot determine count, safer not to delete
		}
	} else {
		// Should not happen if map is built correctly, but handle defensively
		if debug {
			log.Printf("Warning: Species %s not found in species count map.", file.Species)
		}
		serviceLogger.Warn("Species not found in count map",
			"policy", policy,
			"species", file.Species,
			"path", file.Path)
		return false // Cannot determine count, safer not to delete
	}

	return true // Can delete
}

// deleteAudioFile removes a file from the filesystem with enhanced error handling and metrics.
func deleteAudioFile(file *FileInfo, debug bool, policy string) error {
	serviceLogger.Info("Deleting audio file",
		"policy", policy,
		"path", file.Path,
		"size", file.Size,
		"species", file.Species)

	// Record metrics before attempting deletion
	if m := getMetrics(); m != nil {
		m.RecordFileProcessed(policy, "delete_attempt")
	}

	err := os.Remove(file.Path)
	if err != nil {
		// Create enhanced error with proper context
		enhancedErr := errors.New(err).
			Component("diskmanager").
			Category(errors.CategoryFileIO).
			Context("policy", policy).
			Context("operation", "delete_audio_file").
			Context("file_size", file.Size).
			Context("species", file.Species).
			FileContext(file.Path, file.Size).
			Build()

		serviceLogger.Error("Failed to delete audio file",
			"policy", policy,
			"path", file.Path,
			"error", enhancedErr,
			"error_category", enhancedErr.GetCategory())

		// Record error metrics
		if m := getMetrics(); m != nil {
			m.RecordCleanupError(policy, "file_deletion")
			m.RecordFileProcessed(policy, "error")
		}

		return enhancedErr
	}

	if debug {
		log.Printf("File %s deleted", file.Path)
	}
	serviceLogger.Info("Audio file deleted successfully",
		"policy", policy,
		"path", file.Path)

	// Record successful deletion metrics
	if m := getMetrics(); m != nil {
		m.RecordFilesDeleted(policy, 1)
		m.RecordBytesFreed(policy, float64(file.Size))
		m.RecordFileProcessed(policy, "deleted")
	}

	return nil
}

// deleteFileAndOptionalSpectrogram handles the deletion of the audio file
// and its associated spectrogram with enhanced error handling, metrics, and timing.
func deleteFileAndOptionalSpectrogram(file *FileInfo, reason string, keepSpectrograms, debug bool, policy string) error {
	// Start timing the operation
	startTime := time.Now()

	// Throttle slightly before deletion
	time.Sleep(100 * time.Millisecond)

	// Log intent before deleting
	if debug {
		log.Printf("Deleting file based on policy (%s): %s (Size: %d)", reason, file.Path, file.Size)
	}
	serviceLogger.Info("Deleting file based on policy",
		"policy", policy,
		"reason", reason,
		"path", file.Path,
		"size", file.Size,
		"species", file.Species,
		"keep_spectrograms", keepSpectrograms)

	// Delete the audio file (reuse common helper)
	if err := deleteAudioFile(file, debug, policy); err != nil {
		// Record timing for failed operations
		if m := getMetrics(); m != nil {
			duration := time.Since(startTime).Seconds()
			m.RecordCleanupDuration(policy, duration)
		}
		return err // Enhanced error already created in deleteAudioFile
	}

	// Track spectrograms deleted
	spectrogramsDeleted := 0

	// Optionally delete associated spectrogram PNG file
	if !keepSpectrograms {
		basePath := strings.TrimSuffix(file.Path, filepath.Ext(file.Path))
		pngPathLower := basePath + ".png"
		pngPathUpper := basePath + ".PNG"

		serviceLogger.Debug("Checking for associated spectrograms",
			"policy", policy,
			"lower_case", pngPathLower,
			"upper_case", pngPathUpper)

		// Attempt to remove lowercase variant
		if pngErrLower := os.Remove(pngPathLower); pngErrLower != nil {
			// Handle non-existence errors gracefully
			if !os.IsNotExist(pngErrLower) {
				// Create enhanced error for actual deletion failures
				enhancedErr := errors.New(pngErrLower).
					Component("diskmanager").
					Category(errors.CategoryFileIO).
					Context("policy", policy).
					Context("operation", "delete_spectrogram").
					Context("variant", "lowercase").
					FileContext(pngPathLower, 0).
					Build()

				if debug {
					log.Printf("Warning: Failed to remove associated spectrogram %s: %v", pngPathLower, enhancedErr)
				}
				serviceLogger.Warn("Failed to remove associated spectrogram (lowercase)",
					"policy", policy,
					"path", pngPathLower,
					"error", enhancedErr,
					"error_category", enhancedErr.GetCategory())

				// Record spectrogram deletion error
				if m := getMetrics(); m != nil {
					m.RecordCleanupError(policy, "spectrogram_deletion")
				}
			}
		} else {
			spectrogramsDeleted++
			if debug {
				log.Printf("Deleted associated spectrogram %s", pngPathLower)
			}
			serviceLogger.Info("Deleted associated spectrogram (lowercase)",
				"policy", policy,
				"path", pngPathLower)
		}

		// Attempt to remove uppercase variant (handles cases like .WAV -> .PNG)
		if pngErrUpper := os.Remove(pngPathUpper); pngErrUpper != nil {
			// Handle non-existence errors gracefully
			if !os.IsNotExist(pngErrUpper) {
				// Create enhanced error for actual deletion failures
				enhancedErr := errors.New(pngErrUpper).
					Component("diskmanager").
					Category(errors.CategoryFileIO).
					Context("policy", policy).
					Context("operation", "delete_spectrogram").
					Context("variant", "uppercase").
					FileContext(pngPathUpper, 0).
					Build()

				if debug {
					log.Printf("Warning: Failed to remove associated spectrogram %s: %v", pngPathUpper, enhancedErr)
				}
				serviceLogger.Warn("Failed to remove associated spectrogram (uppercase)",
					"policy", policy,
					"path", pngPathUpper,
					"error", enhancedErr,
					"error_category", enhancedErr.GetCategory())

				// Record spectrogram deletion error
				if m := getMetrics(); m != nil {
					m.RecordCleanupError(policy, "spectrogram_deletion")
				}
			}
		} else {
			// Only count if lowercase didn't already delete the same file on case-insensitive FS
			if _, statErr := os.Stat(pngPathLower); os.IsNotExist(statErr) {
				spectrogramsDeleted++
				if debug {
					log.Printf("Deleted associated spectrogram %s", pngPathUpper)
				}
				serviceLogger.Info("Deleted associated spectrogram (uppercase)",
					"policy", policy,
					"path", pngPathUpper)
			}
		}

		// Record spectrogram deletion metrics
		if m := getMetrics(); m != nil && spectrogramsDeleted > 0 {
			m.RecordFilesDeleted(policy, float64(spectrogramsDeleted))
			// Note: We don't know spectrogram file sizes, so bytes freed is only for audio files
		}
	}

	// Record operation timing
	if m := getMetrics(); m != nil {
		duration := time.Since(startTime).Seconds()
		m.RecordCleanupDuration(policy, duration)
	}

	serviceLogger.Info("File deletion completed",
		"policy", policy,
		"path", file.Path,
		"reason", reason,
		"spectrograms_deleted", spectrogramsDeleted,
		"duration_ms", time.Since(startTime).Milliseconds())

	return nil // Deletion successful
}

// handleDeletionErrorInLoop manages error counting and logging for deletion errors within processing loops
// with enhanced error handling and metrics collection.
func handleDeletionErrorInLoop(filePath string, delErr error, errorCount *int, maxErrors int, policy string) (shouldStop bool, loopErr error) {
	*errorCount++ // Increment the error count via pointer

	// Extract error category if it's an enhanced error
	errorCategory := "unknown"
	var enhancedErr *errors.EnhancedError
	if errors.As(delErr, &enhancedErr) {
		errorCategory = enhancedErr.GetCategory()
	}

	log.Printf("Failed to remove %s: %s\n", filePath, delErr)
	serviceLogger.Error("Failed to remove file during cleanup loop",
		"policy", policy,
		"path", filePath,
		"error", delErr,
		"error_category", errorCategory,
		"error_count", *errorCount,
		"max_errors", maxErrors)

	// Record error metrics
	if m := getMetrics(); m != nil {
		m.RecordCleanupError(policy, "loop_error")
		m.RecordFileProcessed(policy, "error")
	}

	if *errorCount > maxErrors {
		// Create enhanced error for loop termination
		loopErr = errors.Newf("too many errors (%d) during cleanup, last error: %w", *errorCount, delErr).
			Component("diskmanager").
			Category(errors.CategoryDiskCleanup).
			Context("policy", policy).
			Context("operation", "cleanup_loop").
			Context("error_count", *errorCount).
			Context("max_errors", maxErrors).
			Context("last_file_path_type", categorizeFilePath(filePath)).
			Build()

		// Extract category from enhanced error for logging
		var enhancedLoopErr *errors.EnhancedError
		categoryForLog := "unknown"
		if errors.As(loopErr, &enhancedLoopErr) {
			categoryForLog = enhancedLoopErr.GetCategory()
		}

		serviceLogger.Error("Cleanup loop stopping due to too many errors",
			"policy", policy,
			"error_count", *errorCount,
			"max_errors", maxErrors,
			"last_error", delErr,
			"enhanced_error_category", categoryForLog)

		// Record critical error metric
		if m := getMetrics(); m != nil {
			m.RecordCleanupError(policy, "too_many_errors")
		}

		return true, loopErr // Stop processing
	}
	return false, nil // Continue processing
}

// categorizeFilePath anonymizes file paths for metrics while preserving structure info
func categorizeFilePath(path string) string {
	if strings.Contains(path, "/") || strings.Contains(path, "\\") {
		return "absolute-path"
	}
	return "relative-path"
}

// prepareInitialCleanup fetches settings, audio files, and performs initial checks.
// It returns the files, base directory, retention settings, and a boolean indicating if cleanup should proceed.
// If proceed is false, it also returns a completed CleanupResult.
func prepareInitialCleanup(db Interface) (files []FileInfo, baseDir string, retention conf.RetentionSettings, proceed bool, result CleanupResult) {
	settings := conf.Setting()
	debug := settings.Realtime.Audio.Export.Retention.Debug
	baseDir = settings.Realtime.Audio.Export.Path
	retention = settings.Realtime.Audio.Export.Retention // Return the whole retention struct

	serviceLogger.Info("Preparing initial cleanup",
		"base_dir", baseDir,
		"policy", retention.Policy,
		"debug", debug)

	// OPTIMIZATION: For usage-based policy, check disk usage BEFORE scanning all files
	// This avoids wasting CPU/IO scanning thousands of files when cleanup isn't needed
	if retention.Policy == "usage" {
		usageThresholdFloat, parseErr := conf.ParsePercentage(retention.MaxUsage)
		if parseErr == nil {
			currentUsage, usageErr := GetDiskUsage(baseDir)
			utilization := int(currentUsage)

			if usageErr == nil {
				updateDiskUsageMetrics(DiskSpaceInfo{
					UsedBytes:  uint64(currentUsage),
					TotalBytes: 100, // Placeholder for percentage-based update
				})

				if currentUsage < usageThresholdFloat {
					serviceLogger.Info("Disk usage below threshold, skipping file scan",
						"policy", "usage",
						"current_usage", utilization,
						"threshold", int(usageThresholdFloat),
						"base_dir", baseDir)

					if debug {
						log.Printf("Disk usage (%.1f%%) below threshold (%.1f%%), skipping cleanup", currentUsage, usageThresholdFloat)
					}

					result = CleanupResult{Err: nil, ClipsRemoved: 0, DiskUtilization: utilization}
					return nil, baseDir, retention, false, result
				}
			} else {
				serviceLogger.Warn("Failed to check disk usage for early exit",
					"policy", "usage",
					"error", usageErr,
					"continuing_with_scan", true)
			}
		}
	}

	files, err := GetAudioFiles(baseDir, allowedFileTypes, db, debug)
	if err != nil {
		// Try to get current disk usage for the result even if file listing failed
		currentUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(currentUsage)
		}
		result = CleanupResult{Err: fmt.Errorf("failed to get audio files for cleanup: %w", err), ClipsRemoved: 0, DiskUtilization: utilization}

		serviceLogger.Error("Failed to get audio files for cleanup",
			"policy", retention.Policy,
			"base_dir", baseDir,
			"error", err,
			"disk_utilization", utilization)
		return nil, baseDir, retention, false, result
	}

	serviceLogger.Info("Retrieved audio files for cleanup consideration",
		"policy", retention.Policy,
		"file_count", len(files),
		"base_dir", baseDir)

	if len(files) == 0 {
		if debug {
			log.Printf("No eligible audio files found for cleanup in %s", baseDir)
		}
		// Get current disk utilization even if no files were processed
		currentUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(currentUsage)
		}
		result = CleanupResult{Err: nil, ClipsRemoved: 0, DiskUtilization: utilization}

		serviceLogger.Info("No eligible audio files found for cleanup",
			"policy", retention.Policy,
			"base_dir", baseDir,
			"disk_utilization", utilization)
		return nil, baseDir, retention, false, result
	}

	// If we got here, proceed with cleanup
	serviceLogger.Info("Proceeding with cleanup process",
		"policy", retention.Policy,
		"file_count", len(files),
		"base_dir", baseDir)
	return files, baseDir, retention, true, CleanupResult{}
}

// CleanupResult contains the results of a cleanup operation
type CleanupResult struct {
	Err             error // Any error that occurred during cleanup
	ClipsRemoved    int   // Number of clips that were removed
	DiskUtilization int   // Current disk utilization percentage after cleanup
}

// CloseLogger closes the diskmanager service logger
func CloseLogger() error {
	if closeLogger != nil {
		serviceLogger.Debug("Closing diskmanager log file",
			"policy", "diskmanager")
		err := closeLogger()
		closeLogger = nil // Prevent multiple closes
		return err
	}
	return nil
}
