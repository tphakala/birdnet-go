// policy_common.go - shared code for cleanup policies
package diskmanager

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/formatutil"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"github.com/tphakala/birdnet-go/internal/spectrogram/specfile"
)

// errFileAlreadyGone is returned by deleteAudioFile when the audio file no
// longer exists, for example because it was removed between the scan and the
// deletion attempt. It is not a failure of the cleanup: the clip is gone.
var errFileAlreadyGone = errors.NewStd("audio file already gone")

// configKeyRetentionMaxUsage is the config key for the retention max usage percentage setting
const configKeyRetentionMaxUsage = "retention.max_usage"

// defaultMaxUsagePercent is the fallback when the user's config has an empty MaxUsage value.
// Matches the viper default in conf/defaults.go.
const defaultMaxUsagePercent = "80%"

// Package-level metrics with explicit synchronization
var (
	// Thread-safe diskMetrics with explicit synchronization
	diskMetrics     *metrics.DiskManagerMetrics // Package-level metrics
	diskMetricsMu   sync.RWMutex                // Protects diskMetrics access
	metricsInitOnce sync.Once                   // Ensures SetMetrics is called only once
)

// GetLogger returns the package logger for the diskmanager module
func GetLogger() logger.Logger {
	return logger.Global().Module("diskmanager")
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
	GetLogger().Debug("Building species subdirectory count map",
		logger.String("policy", "diskmanager"), // Generic policy name since this function is called by both policies
		logger.Int("file_count", len(files)))
	speciesCount := make(map[string]map[string]int)
	for _, file := range files {
		subDir := filepath.Dir(file.Path)
		if _, exists := speciesCount[file.Species]; !exists {
			speciesCount[file.Species] = make(map[string]int)
		}
		speciesCount[file.Species][subDir]++
	}
	GetLogger().Debug("Species subdirectory count map built",
		logger.String("policy", "diskmanager"),
		logger.Int("species_count", len(speciesCount)))
	return speciesCount
}

// buildSpeciesTotalCountMap creates a map to track the total number of files per species across all subdirectories.
func buildSpeciesTotalCountMap(files []FileInfo) map[string]int {
	GetLogger().Debug("Building species total count map",
		logger.String("policy", "diskmanager"),
		logger.Int("file_count", len(files)))
	speciesTotalCount := make(map[string]int)
	for _, file := range files {
		speciesTotalCount[file.Species]++
	}
	GetLogger().Debug("Species total count map built",
		logger.String("policy", "diskmanager"),
		logger.Int("species_count", len(speciesTotalCount)))
	return speciesTotalCount
}

// checkLocked checks if a file should be skipped because it's locked.
func checkLocked(file *FileInfo) bool {
	if file.Locked {
		log := GetLogger()
		log.Debug("Skipping locked file",
			logger.String("path", file.Path),
			logger.String("species", file.Species))
		return true // Indicates the file should be skipped
	}
	return false // Indicates the file should NOT be skipped
}

// checkMinClips checks if a file can be deleted based on the minimum clips per species constraint.
// Returns true if deletion is allowed, false otherwise.
func checkMinClips(file *FileInfo, subDir string, speciesCount map[string]map[string]int,
	minClipsPerSpecies int, policy string) bool {

	log := GetLogger()

	// Ensure the species and subdirectory exist in the map
	if speciesMap, ok := speciesCount[file.Species]; ok {
		if count, ok := speciesMap[subDir]; ok {
			if count <= minClipsPerSpecies {
				log.Debug("Species count at minimum threshold, skipping deletion",
					logger.String("policy", policy),
					logger.String("species", file.Species),
					logger.String("subdirectory", subDir),
					logger.Int("count", count),
					logger.Int("min_threshold", minClipsPerSpecies),
					logger.String("path", file.Path))
				return false // Cannot delete
			}
		} else {
			// Should not happen if map is built correctly, but handle defensively
			log.Warn("Subdirectory not found in species count map",
				logger.String("policy", policy),
				logger.String("subdirectory", subDir),
				logger.String("species", file.Species),
				logger.String("path", file.Path))
			return false // Cannot determine count, safer not to delete
		}
	} else {
		// Should not happen if map is built correctly, but handle defensively
		log.Warn("Species not found in count map",
			logger.String("policy", policy),
			logger.String("species", file.Species),
			logger.String("path", file.Path))
		return false // Cannot determine count, safer not to delete
	}

	return true // Can delete
}

// deleteAudioFile removes a file from the filesystem with enhanced error handling and metrics.
func deleteAudioFile(file *FileInfo, policy string) error {
	log := GetLogger()

	log.Debug("Deleting audio file",
		logger.String("policy", policy),
		logger.String("path", file.Path),
		logger.Int64("size", file.Size),
		logger.String("species", file.Species))

	// Record metrics before attempting deletion
	if m := getMetrics(); m != nil {
		m.RecordFileProcessed(policy, "delete_attempt")
	}

	err := os.Remove(file.Path)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		log.Debug("Audio file already gone, nothing to delete",
			logger.String("policy", policy),
			logger.String("path", file.Path))
		return errFileAlreadyGone
	}
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

		log.Error("Failed to delete audio file",
			logger.String("policy", policy),
			logger.String("path", file.Path),
			logger.Error(enhancedErr),
			logger.String("error_category", enhancedErr.GetCategory()))

		// Record error metrics
		if m := getMetrics(); m != nil {
			m.RecordCleanupError(policy, "file_deletion")
			m.RecordFileProcessed(policy, "error")
		}

		return enhancedErr
	}

	log.Debug("Audio file deleted successfully",
		logger.String("policy", policy),
		logger.String("path", file.Path))

	// Record successful deletion metrics
	if m := getMetrics(); m != nil {
		m.RecordFilesDeleted(policy, 1)
		m.RecordBytesFreed(policy, float64(file.Size))
		m.RecordFileProcessed(policy, "deleted")
	}

	return nil
}

// tryDeleteSpectrogram attempts to delete a spectrogram file at the given path.
// Returns 1 if deleted successfully, 0 if file didn't exist or deletion failed.
// Logs warnings and records metrics for actual deletion failures (not file-not-found).
func tryDeleteSpectrogram(pngPath, variant, policy string, log logger.Logger) int {
	if err := os.Remove(pngPath); err != nil {
		if !os.IsNotExist(err) {
			enhancedErr := errors.New(err).
				Component("diskmanager").
				Category(errors.CategoryFileIO).
				Context("policy", policy).
				Context("operation", "delete_spectrogram").
				Context("variant", variant).
				FileContext(pngPath, 0).
				Build()

			log.Warn("Failed to remove associated spectrogram",
				logger.String("policy", policy),
				logger.String("variant", variant),
				logger.String("path", pngPath),
				logger.Error(enhancedErr),
				logger.String("error_category", enhancedErr.GetCategory()))

			if m := getMetrics(); m != nil {
				m.RecordCleanupError(policy, "spectrogram_deletion")
			}
		}
		return 0
	}
	log.Debug("Deleted associated spectrogram",
		logger.String("policy", policy),
		logger.String("variant", variant),
		logger.String("path", pngPath))
	return 1
}

// deleteFileAndOptionalSpectrogram handles the deletion of the audio file
// and its associated spectrogram with enhanced error handling, metrics, and timing.
// It returns errFileAlreadyGone, without touching the spectrogram, when the audio file no longer exists.
func deleteFileAndOptionalSpectrogram(file *FileInfo, reason string, keepSpectrograms bool, policy string) error {
	log := GetLogger()

	// Start timing the operation
	startTime := time.Now()

	// Log intent before deleting
	log.Debug("Deleting file based on policy",
		logger.String("policy", policy),
		logger.String("reason", reason),
		logger.String("path", file.Path),
		logger.Int64("size", file.Size),
		logger.String("species", file.Species),
		logger.Bool("keep_spectrograms", keepSpectrograms))

	// Delete the audio file (reuse common helper)
	if err := deleteAudioFile(file, policy); err != nil {
		if errors.Is(err, errFileAlreadyGone) {
			return err
		}
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

		log.Debug("Checking for associated spectrograms",
			logger.String("policy", policy),
			logger.String("lower_case", pngPathLower),
			logger.String("upper_case", pngPathUpper))

		spectrogramsDeleted = tryDeleteSpectrogram(pngPathLower, "lowercase", policy, log)

		// Only try uppercase if lowercase didn't delete (handles case-insensitive FS)
		if spectrogramsDeleted == 0 {
			spectrogramsDeleted += tryDeleteSpectrogram(pngPathUpper, "uppercase", policy, log)
		} else {
			// Check if uppercase is a different file that also exists
			if _, statErr := os.Stat(pngPathUpper); statErr == nil {
				spectrogramsDeleted += tryDeleteSpectrogram(pngPathUpper, "uppercase", policy, log)
			}
		}

		// Record spectrogram deletion metrics
		if m := getMetrics(); m != nil && spectrogramsDeleted > 0 {
			m.RecordFilesDeleted(policy, float64(spectrogramsDeleted))
		}
	}

	// Record operation timing
	if m := getMetrics(); m != nil {
		duration := time.Since(startTime).Seconds()
		m.RecordCleanupDuration(policy, duration)
	}

	log.Debug("File deletion completed",
		logger.String("policy", policy),
		logger.String("path", file.Path),
		logger.String("reason", reason),
		logger.Int("spectrograms_deleted", spectrogramsDeleted),
		logger.Int64("duration_ms", time.Since(startTime).Milliseconds()))

	return nil // Deletion successful
}

// handleDeletionErrorInLoop manages error counting and logging for deletion errors within processing loops
// with enhanced error handling and metrics collection.
func handleDeletionErrorInLoop(filePath string, delErr error, errorCount *int, maxErrors int, policy string) (shouldStop bool, loopErr error) {
	log := GetLogger()

	*errorCount++ // Increment the error count via pointer

	// Extract error category if it's an enhanced error
	errorCategory := "unknown"
	if enhancedErr, ok := errors.AsType[*errors.EnhancedError](delErr); ok {
		errorCategory = enhancedErr.GetCategory()
	}

	log.Error("Failed to remove file during cleanup loop",
		logger.String("policy", policy),
		logger.String("path", filePath),
		logger.Error(delErr),
		logger.String("error_category", errorCategory),
		logger.Int("error_count", *errorCount),
		logger.Int("max_errors", maxErrors))

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
		categoryForLog := "unknown"
		if enhancedLoopErr, ok := errors.AsType[*errors.EnhancedError](loopErr); ok {
			categoryForLog = enhancedLoopErr.GetCategory()
		}

		log.Error("Cleanup loop stopping due to too many errors",
			logger.String("policy", policy),
			logger.Int("error_count", *errorCount),
			logger.Int("max_errors", maxErrors),
			logger.Error(delErr),
			logger.String("enhanced_error_category", categoryForLog))

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
		return "nested-path"
	}
	return "simple-filename"
}

// releaseDeletedClipPaths updates the database references of audio clips the
// retention policy deleted from disk. deletedPaths contains absolute file paths;
// baseDir is the audio export root used to compute relative paths matching the
// clip_name format in the database.
//
// A non-empty clip_name means "the audio exists", so a deleted clip's clip_name is
// cleared. When keepSpectrograms is on and a spectrogram render of the clip survived
// on disk, the clip name is moved into spectrogram_clip_name instead, so the kept
// image stays reachable. When it cannot be told whether a render survived (the
// directory or an entry could not be read), or the move fails, the row is left
// untouched: a cleared row is never visited by the reconcile pass again, while an
// untouched one is re-linked or cleared by it on its next run.
//
// It returns how many rows were retained and how many were cleared.
func releaseDeletedClipPaths(db Interface, deletedPaths []string, baseDir, policy string, keepSpectrograms bool) (retained, cleared int64) {
	if len(deletedPaths) == 0 {
		return 0, 0
	}

	log := GetLogger()

	// Convert absolute file paths to relative clip names matching database format
	var retain, drop []string
	uncertain := 0
	dirCache := make(map[string][]os.DirEntry)
	for _, absPath := range deletedPaths {
		relPath, err := filepath.Rel(baseDir, absPath)
		if err != nil {
			log.Debug("Failed to compute relative path for clip",
				logger.String("policy", policy),
				logger.String("path", absPath),
				logger.Error(err))
			continue
		}
		clipName := filepath.ToSlash(relPath)
		state := renderAbsent
		if keepSpectrograms {
			state = hasKeptSpectrogram(absPath, dirCache)
		}
		switch state {
		case renderPresent:
			retain = append(retain, clipName)
		case renderIndeterminate:
			uncertain++ // neither retain nor clear: the reconcile pass decides
		case renderAbsent:
			drop = append(drop, clipName)
		}
	}

	if len(retain) > 0 {
		var err error
		retained, err = db.RetainNoteSpectrogramsByClipNames(retain)
		if err != nil {
			// The rows keep clip_name, so the reconcile pass re-links the renders or
			// clears the rows on its next run.
			log.Warn("Failed to retain spectrogram references for deleted files, leaving them for the reconcile pass",
				logger.String("policy", policy),
				logger.Int("deleted_files", len(retain)),
				logger.Error(err))
			retained = 0
		}
	}

	if len(drop) > 0 {
		var err error
		cleared, err = db.ClearNoteClipPathsByNames(drop)
		if err != nil {
			log.Warn("Failed to clear clip paths for deleted files",
				logger.String("policy", policy),
				logger.Int("deleted_files", len(drop)),
				logger.Error(err))
			cleared = 0
		}
	}

	if uncertain > 0 {
		log.Warn("Could not tell whether a spectrogram render survived, leaving references for the reconcile pass",
			logger.String("policy", policy),
			logger.Int("deleted_files", uncertain))
	}

	if cleared > 0 || retained > 0 {
		// Runs once per batch, so the totals go to the run summary at Info.
		log.Debug("Updated clip path references for deleted files",
			logger.String("policy", policy),
			logger.Int64("records_retained", retained),
			logger.Int64("records_cleared", cleared),
			logger.Int("files_deleted", len(deletedPaths)))
	}
	return retained, cleared
}

// hasKeptSpectrogram reports whether a non-empty spectrogram render of the deleted
// audio file audioPath is still on disk: renderPresent, renderAbsent, or
// renderIndeterminate when the directory could not be listed or a matching entry
// could not be inspected and no other render was found. dirCache holds one
// directory listing per directory for the duration of a run. A missing directory
// has no render; any other listing failure is indeterminate and is not cached.
func hasKeptSpectrogram(audioPath string, dirCache map[string][]os.DirEntry) renderState {
	dir := filepath.Dir(audioPath)
	entries, seen := dirCache[dir]
	if !seen {
		read, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return renderIndeterminate
		}
		entries = read
		dirCache[dir] = entries
	}
	clipBase := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	found, indeterminate := specfile.Renders(entries, clipBase)
	switch {
	case len(found) > 0:
		return renderPresent
	case indeterminate:
		return renderIndeterminate
	default:
		return renderAbsent
	}
}

// ShouldSkipUsageBasedCleanup checks if cleanup can be skipped based on current disk usage.
// Returns:
//   - skip: true if cleanup should be skipped (usage below threshold)
//   - utilization: current disk usage as integer percentage
//   - err: error if check failed (nil on success)
func ShouldSkipUsageBasedCleanup(retention *conf.RetentionSettings, baseDir string) (skip bool, utilization int, err error) {
	// Apply default if the config value is empty (e.g. user cleared the field via the UI).
	maxUsage := strings.TrimSpace(retention.MaxUsage)
	if maxUsage == "" {
		maxUsage = defaultMaxUsagePercent
	}

	// Parse the threshold percentage
	usageThresholdFloat, parseErr := conf.ParsePercentage(maxUsage, configKeyRetentionMaxUsage)
	if parseErr != nil {
		return false, 0, parseErr
	}

	// Get current disk usage percentage
	currentUsage, usageErr := GetDiskUsage(baseDir)
	if usageErr != nil {
		return false, 0, usageErr
	}

	utilization = int(currentUsage)

	// Update metrics with actual disk space info (not placeholder)
	spaceInfo, err := GetDetailedDiskUsage(baseDir)
	if err == nil {
		updateDiskUsageMetrics(spaceInfo)
	} else {
		GetLogger().Warn("Failed to get detailed disk usage for metrics",
			logger.String("base_dir", baseDir),
			logger.Error(err))
	}

	// Check if below threshold
	if currentUsage < usageThresholdFloat {
		GetLogger().Info("Disk usage below threshold, skipping cleanup",
			logger.Int("current_usage", utilization),
			logger.Int("threshold", int(usageThresholdFloat)),
			logger.String("base_dir", baseDir))
		return true, utilization, nil
	}

	return false, utilization, nil
}

// prepareInitialCleanup fetches settings, audio files, and performs initial checks.
// It returns the files, base directory, retention settings, and a boolean indicating if cleanup should proceed.
// If proceed is false, it also returns a completed CleanupResult.
// The directory walk is cancelled when quit closes; that is reported as a
// no-action result, not as an error.
func prepareInitialCleanup(quit <-chan struct{}, db Interface) (files []FileInfo, baseDir string, retention conf.RetentionSettings, proceed bool, result CleanupResult) {
	settings := conf.Setting()
	baseDir = settings.Realtime.Audio.Export.Path
	retention = settings.Realtime.Audio.Export.Retention // Return the whole retention struct

	GetLogger().Info("Preparing initial cleanup",
		logger.String("base_dir", baseDir),
		logger.String("policy", retention.Policy))

	// OPTIMIZATION: For usage-based policy, check disk usage BEFORE scanning all files
	// This avoids wasting CPU/IO scanning thousands of files when cleanup isn't needed
	if retention.Policy == "usage" {
		skip, utilization, err := ShouldSkipUsageBasedCleanup(&retention, baseDir)
		if err != nil {
			GetLogger().Warn("Failed to check disk usage for early exit",
				logger.String("policy", "usage"),
				logger.Error(err),
				logger.Bool("continuing_with_scan", true))
		} else if skip {
			result = CleanupResult{Err: nil, ClipsRemoved: 0, DiskUtilization: utilization}
			return nil, baseDir, retention, false, result
		}
	}

	scanCtx, cancelScan := quitContext(quit)
	defer cancelScan()
	files, err := GetAudioFilesContext(scanCtx, baseDir, allowedFileTypes, db)
	if errors.Is(err, context.Canceled) {
		GetLogger().Info("cleanup interrupted by shutdown during scan",
			logger.String("policy", retention.Policy),
			logger.String("base_dir", baseDir))
		return nil, baseDir, retention, false, CleanupResult{}
	}
	if err != nil {
		// Try to get current disk usage for the result even if file listing failed
		currentUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(currentUsage)
		}
		result = CleanupResult{Err: fmt.Errorf("failed to get audio files for cleanup: %w", err), ClipsRemoved: 0, DiskUtilization: utilization}

		GetLogger().Error("Failed to get audio files for cleanup",
			logger.String("policy", retention.Policy),
			logger.String("base_dir", baseDir),
			logger.Error(err),
			logger.Int("disk_utilization", utilization))
		return nil, baseDir, retention, false, result
	}

	GetLogger().Info("Retrieved audio files for cleanup consideration",
		logger.String("policy", retention.Policy),
		logger.Int("file_count", len(files)),
		logger.String("base_dir", baseDir))

	if len(files) == 0 {
		// Get current disk utilization even if no files were processed
		currentUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(currentUsage)
		}
		result = CleanupResult{Err: nil, ClipsRemoved: 0, DiskUtilization: utilization}

		GetLogger().Info("No eligible audio files found for cleanup",
			logger.String("policy", retention.Policy),
			logger.String("base_dir", baseDir),
			logger.Int("disk_utilization", utilization))
		return nil, baseDir, retention, false, result
	}

	// If we got here, proceed with cleanup
	GetLogger().Info("Proceeding with cleanup process",
		logger.String("policy", retention.Policy),
		logger.Int("file_count", len(files)),
		logger.String("base_dir", baseDir))
	return files, baseDir, retention, true, CleanupResult{}
}

// CleanupResult contains the results of a cleanup operation
type CleanupResult struct {
	Err             error // Any error that occurred during cleanup
	ClipsRemoved    int   // Number of clips that were removed
	DiskUtilization int   // Current disk utilization percentage after cleanup
	// MoreWork reports that the run stopped before finishing (the retention
	// settings changed, or its time budget ran out after it deleted at least one
	// clip with deletable candidates left). The caller should schedule the next
	// run soon instead of waiting the full check interval.
	MoreWork bool
}

// cleanupStats accumulates per-run, per-file outcome counts for a cleanup pass.
// Apart from MoreWork, which feeds CleanupResult.MoreWork, it is observational:
// nothing here changes deletion behavior. It exists so a cleanup run that
// deletes nothing can be diagnosed from an INFO-level log line (or a support
// dump) without asking the user to enable Debug logging and reproduce, e.g.
// GitHub #3892 and #4059 where zero deletions had no visible explanation in the
// default log output.
//
// The buckets need not sum to Scanned, and the exact semantics differ per
// policy. The age policy sets Scanned to the total candidate count up front, so
// files past an early stop (time budget, settings change or a quit signal) are
// counted in Scanned but in none of the outcome buckets. The usage policy
// increments Scanned only for files it actually examines, and on an early stop
// once disk usage drops below the threshold it tallies the unexamined remainder
// into NotEligible, so for the usage policy the buckets do sum to Scanned.
type cleanupStats struct {
	Scanned         int   // age: total candidate files this run. usage: files actually examined before an early stop
	Deleted         int   // files actually deleted
	LockedSkipped   int   // skipped because the clip is locked/protected
	MinClipsBlocked int   // skipped because deletion would violate the minimum-clips-per-species guard
	NotEligible     int   // age: not old enough. usage: usage already below threshold when reached
	AlreadyGone     int   // audio file no longer existed when the run reached it (removed elsewhere)
	Errors          int   // deletion attempts that failed
	BytesFreed      int64 // total size of the audio files actually deleted this run
	// StopReason says why the run ended (the stop* constants).
	StopReason string
	// MoreWork reports that the run stopped on a settings change, or on its time
	// budget after deleting at least one clip with candidates left, so a
	// follow-up run is worth starting soon. A remaining candidate can still be
	// individually locked or min-clips-blocked; a budget stop in a run that
	// deleted nothing never sets it.
	MoreWork bool
	// Batches is the number of deletion batches whose database references were
	// released.
	Batches int
	// RecordsRetained and RecordsCleared total the database rows updated for
	// the deleted clips across all batches.
	RecordsRetained, RecordsCleared int64
}

// addRunTotals copies the batch and release totals of run into the stats.
func (s *cleanupStats) addRunTotals(run *deletionRun) {
	s.Batches = run.batches
	s.RecordsRetained = run.recordsRetained
	s.RecordsCleared = run.recordsCleared
}

// unknownUsagePercent marks a disk-usage percentage that could not be measured
// (the usage lookup failed) or that does not apply to a policy. It keeps those
// fields out of the summary line rather than logging a misleading 0%.
const unknownUsagePercent = -1

// cleanupSummary bundles everything logCleanupSummary needs to describe one
// completed cleanup run. It is purely observational; none of these fields feed
// back into deletion behavior.
type cleanupSummary struct {
	policy           string
	stats            cleanupStats
	duration         time.Duration
	keepSpectrograms bool // when true, .png spectrograms are left on disk beside deleted audio (GitHub #4059)
	usageBefore      int  // disk usage % at run start; unknownUsagePercent if not measured/applicable
	usageAfter       int  // disk usage % at run end; unknownUsagePercent if not measured
	usageThreshold   int  // target usage %; <= 0 means not applicable (age policy)
}

// notKeepingUp reports that a usage-based run used its whole time budget and
// the disk usage did not fall during it: clips may be arriving as fast as the
// paced deletion removes them. Only meaningful for the usage policy, and only
// when both usage values were measured.
func (s *cleanupSummary) notKeepingUp() bool {
	return s.usageThreshold > 0 && s.stats.StopReason == stopTimeBudget &&
		s.usageBefore != unknownUsagePercent && s.usageAfter != unknownUsagePercent &&
		s.usageAfter >= s.usageBefore
}

// usageStillOverTarget reports that a usage-based run finished with the disk
// still at or above its configured target. It is only meaningful for the usage
// policy (usageThreshold > 0) and when the final usage was actually measured.
func (s *cleanupSummary) usageStillOverTarget() bool {
	return s.usageThreshold > 0 && s.usageAfter != unknownUsagePercent && s.usageAfter >= s.usageThreshold
}

// appendUsageFields adds the disk-usage percentage fields to a log field slice,
// skipping any that were not measured or do not apply (unknownUsagePercent /
// non-positive threshold), so neither the INFO summary nor a WARN logs a
// misleading -1 for a policy that has no usage target (e.g. the age policy).
func appendUsageFields(fields []logger.Field, s *cleanupSummary) []logger.Field {
	if s.usageBefore != unknownUsagePercent {
		fields = append(fields, logger.Int("usage_before_pct", s.usageBefore))
	}
	if s.usageAfter != unknownUsagePercent {
		fields = append(fields, logger.Int("usage_after_pct", s.usageAfter))
	}
	if s.usageThreshold > 0 {
		fields = append(fields, logger.Int("usage_threshold_pct", s.usageThreshold))
	}
	return fields
}

// logCleanupSummary emits a single INFO-level line summarizing a completed
// cleanup run, so the guard (if any) that prevented deletion is visible without
// enabling Debug logging. When a usage-based run spent its whole time budget
// without lowering disk usage, or finished with the disk still at or above the
// configured target and no follow-up run pending, it additionally emits a WARN
// so the "cleanup ran but disk stays full" condition (GitHub #4059, #3892) is
// loud in default logs and in a support dump rather than requiring a live Debug
// reproduction.
func logCleanupSummary(s *cleanupSummary) {
	log := GetLogger()

	fields := []logger.Field{
		logger.String("policy", s.policy),
		logger.Int("files_scanned", s.stats.Scanned),
		logger.Int("files_deleted", s.stats.Deleted),
		logger.Int64("bytes_freed", s.stats.BytesFreed),
		logger.String("bytes_freed_human", formatutil.Bytes(s.stats.BytesFreed)),
		logger.Int("locked_skipped", s.stats.LockedSkipped),
		logger.Int("min_clips_blocked", s.stats.MinClipsBlocked),
		logger.Int("not_eligible", s.stats.NotEligible),
		logger.Int("already_gone", s.stats.AlreadyGone),
		logger.Int("errors", s.stats.Errors),
		logger.Bool("keep_spectrograms", s.keepSpectrograms),
		logger.String("stop_reason", s.stats.StopReason),
		logger.Bool("more_work", s.stats.MoreWork),
		logger.Int("batches", s.stats.Batches),
		logger.Int64("records_retained", s.stats.RecordsRetained),
		logger.Int64("records_cleared", s.stats.RecordsCleared),
		logger.Duration("duration", s.duration),
	}
	log.Info("cleanup run summary", appendUsageFields(fields, s)...)

	// At most one WARN fires. Not-keeping-up takes precedence over
	// still-over-target because it names a concrete cause, whereas over-target is
	// the more general symptom.
	switch {
	case s.notKeepingUp():
		warnFields := []logger.Field{
			logger.String("policy", s.policy),
			logger.Int("files_deleted", s.stats.Deleted),
			logger.Bool("keep_spectrograms", s.keepSpectrograms),
		}
		log.Warn("retention cleanup is deleting at its paced rate but disk usage did not fall during this run; clips may be arriving faster than they can be removed",
			appendUsageFields(warnFields, s)...)
	case s.usageStillOverTarget() && !s.stats.MoreWork && s.stats.StopReason != stopQuit:
		// The run ended for good (nothing pending, not interrupted) with the disk
		// still at or above target. Something is preventing deletions (all
		// remaining clips locked, min-clips guard, or spectrograms retained under
		// keep_spectrograms).
		warnFields := []logger.Field{
			logger.String("policy", s.policy),
			logger.Int("files_deleted", s.stats.Deleted),
			logger.Int("locked_skipped", s.stats.LockedSkipped),
			logger.Int("min_clips_blocked", s.stats.MinClipsBlocked),
			logger.Bool("keep_spectrograms", s.keepSpectrograms),
		}
		log.Warn("usage-based cleanup finished with disk still at or above the configured target",
			appendUsageFields(warnFields, s)...)
	}
}

// CloseLogger is a no-op for backwards compatibility.
// The central logger manages its own lifecycle.
func CloseLogger() error {
	return nil
}
