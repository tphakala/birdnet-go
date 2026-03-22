package analysis

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/tphakala/birdnet-go/internal/api"
	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/datastore/v2only"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/monitor"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// AudioDemuxManager manages the lifecycle of audio demultiplexing goroutines
type AudioDemuxManager struct {
	doneChan chan struct{}
	mutex    sync.Mutex
	wg       sync.WaitGroup
}

// NewAudioDemuxManager creates a new AudioDemuxManager
func NewAudioDemuxManager() *AudioDemuxManager {
	return &AudioDemuxManager{}
}

// Stop signals the current demux goroutine to stop and waits for it to exit
func (m *AudioDemuxManager) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.doneChan != nil {
		close(m.doneChan)
		m.wg.Wait() // Wait for goroutine to exit
		m.doneChan = nil
	}
}

// Start creates a new done channel and increments the wait group
func (m *AudioDemuxManager) Start() chan struct{} {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.doneChan = make(chan struct{})
	m.wg.Add(1)
	return m.doneChan
}

// Done should be called when the goroutine exits
func (m *AudioDemuxManager) Done() {
	m.wg.Done()
}

// clipCleanupMonitor monitors the database and deletes clips that meet the retention policy.
// It also performs periodic cleanup of log deduplicator states to prevent memory growth.
func clipCleanupMonitor(quitChan chan struct{}, dataStore datastore.Interface) {
	// Get configurable cleanup check interval, with fallback to default
	retention := conf.Setting().Realtime.Audio.Export.Retention
	checkInterval := retention.CheckInterval
	if checkInterval <= 0 {
		checkInterval = conf.DefaultCleanupCheckInterval
	}

	// Create a ticker that triggers at the configured interval to perform cleanup
	ticker := time.NewTicker(time.Duration(checkInterval) * time.Minute)
	defer ticker.Stop() // Ensure the ticker is stopped to prevent leaks

	// Get the shared disk manager logger
	diskManagerLogger := diskmanager.GetLogger()

	policy := retention.Policy
	GetLogger().Info("clip cleanup monitor initialized",
		logger.String("policy", policy),
		logger.Int("check_interval_minutes", checkInterval),
		logger.String("operation", "clip_cleanup_init"))
	diskManagerLogger.Info("Cleanup timer started",
		logger.String("policy", policy),
		logger.Int("interval_minutes", checkInterval),
		logger.String("timestamp", time.Now().Format(time.RFC3339)))

	for {
		select {
		case <-quitChan:
			// Handle quit signal to stop the monitor
			diskManagerLogger.Info("Cleanup timer stopped",
				logger.String("reason", "quit signal received"),
				logger.String("timestamp", time.Now().Format(time.RFC3339)))
			// Ensure clean shutdown
			if err := diskmanager.CloseLogger(); err != nil {
				diskManagerLogger.Error("Failed to close diskmanager logger", logger.Error(err))
			}
			return

		case t := <-ticker.C:
			GetLogger().Info("starting clip cleanup task",
				logger.String("timestamp", t.Format(time.RFC3339)),
				logger.String("policy", conf.Setting().Realtime.Audio.Export.Retention.Policy),
				logger.String("operation", "clip_cleanup_task"))
			diskManagerLogger.Info("Cleanup timer triggered",
				logger.String("timestamp", t.Format(time.RFC3339)),
				logger.String("policy", conf.Setting().Realtime.Audio.Export.Retention.Policy))

			// age based cleanup method
			if conf.Setting().Realtime.Audio.Export.Retention.Policy == "age" {
				diskManagerLogger.Debug("Starting age-based cleanup via timer")
				result := diskmanager.AgeBasedCleanup(quitChan, dataStore)
				if result.Err != nil {
					GetLogger().Error("age-based cleanup failed",
						logger.Error(result.Err),
						logger.String("operation", "age_based_cleanup"))
					diskManagerLogger.Error("Age-based cleanup failed",
						logger.Error(result.Err),
						logger.String("timestamp", time.Now().Format(time.RFC3339)))
				} else {
					GetLogger().Info("age-based cleanup completed successfully",
						logger.Int("clips_removed", result.ClipsRemoved),
						logger.Int("disk_utilization_percent", result.DiskUtilization),
						logger.String("operation", "age_based_cleanup"))
					diskManagerLogger.Info("Age-based cleanup completed via timer",
						logger.Int("clips_removed", result.ClipsRemoved),
						logger.Int("disk_utilization", result.DiskUtilization),
						logger.String("timestamp", time.Now().Format(time.RFC3339)))
				}
			}

			// priority based cleanup method
			if conf.Setting().Realtime.Audio.Export.Retention.Policy == "usage" {
				retention := conf.Setting().Realtime.Audio.Export.Retention
				baseDir := conf.Setting().Realtime.Audio.Export.Path

				// Check if we can skip cleanup
				skip, utilization, err := diskmanager.ShouldSkipUsageBasedCleanup(&retention, baseDir)

				if err != nil {
					diskManagerLogger.Warn("Failed to check disk usage for early exit via timer",
						logger.Error(err),
						logger.Bool("continuing_with_cleanup", true))
				} else if skip {
					diskManagerLogger.Info("Disk usage below threshold via timer, skipping cleanup",
						logger.Int("current_usage", utilization),
						logger.String("timestamp", time.Now().Format(time.RFC3339)))
					continue // Skip to next timer tick
				}

				// Proceed with cleanup
				diskManagerLogger.Debug("Starting usage-based cleanup via timer")
				result := diskmanager.UsageBasedCleanup(quitChan, dataStore)
				if result.Err != nil {
					GetLogger().Error("usage-based cleanup failed",
						logger.Error(result.Err),
						logger.String("operation", "usage_based_cleanup"))
					diskManagerLogger.Error("Usage-based cleanup failed",
						logger.Error(result.Err),
						logger.String("timestamp", time.Now().Format(time.RFC3339)))
				} else {
					GetLogger().Info("usage-based cleanup completed successfully",
						logger.Int("clips_removed", result.ClipsRemoved),
						logger.Int("disk_utilization_percent", result.DiskUtilization),
						logger.String("operation", "usage_based_cleanup"))
					diskManagerLogger.Info("Usage-based cleanup completed via timer",
						logger.Int("clips_removed", result.ClipsRemoved),
						logger.Int("disk_utilization", result.DiskUtilization),
						logger.String("timestamp", time.Now().Format(time.RFC3339)))
				}
			}
		}
	}
}

// setupImageProviderRegistry initializes or retrieves the global image provider registry
// and registers the available providers (AviCommons, Wikimedia).
// Uses atomic GetOrRegister to eliminate race conditions between concurrent calls.
func setupImageProviderRegistry(ds datastore.Interface, metrics *observability.Metrics) (*imageprovider.ImageProviderRegistry, error) {
	// Use the global registry if available, otherwise create a new one
	log := GetLogger()
	var registry *imageprovider.ImageProviderRegistry
	if api.ImageProviderRegistry != nil {
		registry = api.ImageProviderRegistry
		log.Info("using existing image provider registry",
			logger.String("operation", "setup_image_registry"))
	} else {
		registry = imageprovider.NewImageProviderRegistry()
		api.ImageProviderRegistry = registry // Assign back to global
		log.Info("created new image provider registry",
			logger.String("operation", "setup_image_registry"))
	}

	var errs []error // Slice to collect errors

	// Use atomic GetOrRegister to eliminate race condition between GetCache and Register
	_, err := registry.GetOrRegister("wikimedia", func() (*imageprovider.BirdImageCache, error) {
		return imageprovider.CreateDefaultCache(metrics, ds)
	})
	if err != nil {
		log.Error("failed to register WikiMedia image provider",
			logger.Error(err),
			logger.String("provider", "wikimedia"),
			logger.String("operation", "register_image_provider"))
		errs = append(errs, errors.New(err).
			Component("realtime-analysis").
			Category(errors.CategoryImageProvider).
			Context("operation", "register_wikimedia_provider").
			Context("provider", "wikimedia").
			Build())
		// Continue even if one provider fails
	} else {
		log.Info("successfully registered image provider",
			logger.String("provider", "wikimedia"),
			logger.String("operation", "register_image_provider"))
	}

	// Debug logging for embedded filesystem if enabled (outside GetOrRegister to avoid holding registry lock)
	if conf.Setting().Realtime.Dashboard.Thumbnails.Debug {
		log.Debug("listing embedded filesystem contents",
			logger.String("operation", "debug_filesystem"))
		if walkErr := fs.WalkDir(api.ImageDataFs, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				log.Debug("error walking filesystem path",
					logger.String("path", path),
					logger.Error(err),
					logger.String("operation", "debug_filesystem"))
				return nil
			}
			log.Debug("filesystem entry found",
				logger.String("path", path),
				logger.Bool("is_dir", d.IsDir()),
				logger.String("operation", "debug_filesystem"))
			return nil
		}); walkErr != nil {
			log.Error("error walking embedded filesystem",
				logger.Error(walkErr),
				logger.String("operation", "debug_filesystem"))
		}
	}

	// Use atomic GetOrRegister to eliminate race condition for AviCommons
	_, err = registry.GetOrRegister("avicommons", func() (*imageprovider.BirdImageCache, error) {
		return imageprovider.CreateAviCommonsCache(api.ImageDataFs, metrics, ds)
	})
	if err != nil {
		log.Error("failed to register AviCommons image provider",
			logger.Error(err),
			logger.String("provider", "avicommons"),
			logger.String("operation", "register_image_provider"))
		errs = append(errs, errors.New(err).
			Component("realtime-analysis").
			Category(errors.CategoryImageProvider).
			Context("operation", "register_avicommons_provider").
			Context("provider", "avicommons").
			Build())
		// Continue even if one provider fails
	} else {
		log.Info("successfully registered image provider",
			logger.String("provider", "avicommons"),
			logger.String("operation", "register_image_provider"))
	}

	// Set the registry in each provider for fallback support
	registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
		cache.SetRegistry(registry)
		return true // Continue ranging
	})

	// Return joined errors if any occurred
	if len(errs) > 0 {
		return registry, errors.Join(errs...)
	}

	return registry, nil // No errors during setup
}

// selectDefaultImageProvider determines the default image provider based on configuration
func selectDefaultImageProvider(registry *imageprovider.ImageProviderRegistry) *imageprovider.BirdImageCache {
	log := GetLogger()
	preferredProvider := conf.Setting().Realtime.Dashboard.Thumbnails.ImageProvider
	var defaultCache *imageprovider.BirdImageCache

	if preferredProvider == "auto" {
		// Use avicommons as the default provider in auto mode, if available
		if cache, ok := registry.GetCache("avicommons"); ok {
			defaultCache = cache
			log.Info("selected default image provider",
				logger.String("provider", "avicommons"),
				logger.String("mode", "auto"),
				logger.String("operation", "select_default_provider"))
		}
	} else {
		// User has specified a specific provider
		if cache, ok := registry.GetCache(preferredProvider); ok {
			defaultCache = cache
			log.Info("selected preferred image provider",
				logger.String("provider", preferredProvider),
				logger.String("operation", "select_default_provider"))
		} else if cache, ok := registry.GetCache("avicommons"); ok {
			// Fallback to avicommons if preferred provider doesn't exist or isn't registered
			defaultCache = cache
			log.Warn("preferred provider not available, falling back",
				logger.String("preferred_provider", preferredProvider),
				logger.String("fallback_provider", "avicommons"),
				logger.String("operation", "select_default_provider"))
		}
	}

	// If we still don't have a default cache (e.g., avicommons failed registration), try any available provider.
	if defaultCache == nil {
		log.Warn("no default image provider found, searching for alternatives",
			logger.String("operation", "select_default_provider"))
		registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
			defaultCache = cache
			log.Info("selected fallback default image provider",
				logger.String("provider", name),
				logger.String("operation", "select_default_provider"))
			return false // Stop at the first provider found
		})
	}

	return defaultCache
}

// warmUpImageCacheInBackground fetches existing cache data and starts background tasks
// to fetch images for species not yet cached by any provider.
func warmUpImageCacheInBackground(ds datastore.Interface, registry *imageprovider.ImageProviderRegistry, defaultCache *imageprovider.BirdImageCache, speciesList []datastore.Note) {
	log := GetLogger()
	log.Info("starting background image cache warm-up",
		logger.Int("species_count", len(speciesList)),
		logger.String("operation", "image_cache_warmup"))

	// Pre-fetch all cached image records from the database per provider
	allCachedImages := make(map[string]map[string]bool) // providerName -> scientificName -> exists
	if ds != nil {
		registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
			providerCache, err := ds.GetAllImageCaches(name)
			if err != nil {
				log.Warn("failed to get cached images for provider",
					logger.String("provider", name),
					logger.Error(err),
					logger.String("operation", "image_cache_warmup"))
				return true // Continue to next provider
			}
			allCachedImages[name] = make(map[string]bool)
			for i := range providerCache {
				allCachedImages[name][providerCache[i].ScientificName] = true
			}
			log.Info("pre-fetched cached image records",
				logger.String("provider", name),
				logger.Int("cached_count", len(providerCache)),
				logger.String("operation", "image_cache_warmup"))
			return true // Continue ranging
		})
	} else {
		log.Warn("datastore is nil, cannot pre-fetch cached images",
			logger.String("operation", "image_cache_warmup"))
	}

	// Start background fetching of images for species not found in any cache
	go func() {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 5) // Limit to 5 concurrent fetches
		needsImage := 0

		for i := range speciesList {
			// Use direct access to name to avoid pointer allocation
			sciName := speciesList[i].ScientificName

			// Skip empty scientific names
			if sciName == "" {
				log.Warn("skipping empty scientific name during warm-up",
					logger.String("operation", "image_cache_warmup"))
				continue
			}

			// Check if already cached by *any* provider
			alreadyCached := false
			for providerName := range allCachedImages {
				if _, exists := allCachedImages[providerName][sciName]; exists {
					alreadyCached = true
					break
				}
			}

			if alreadyCached {
				continue
			}

			needsImage++
			wg.Add(1)
			// The defaultCache.Get call below will handle initialization and locking.
			// No need to manually manipulate the Initializing map here.

			go func(name string) {
				defer wg.Done()
				// The tryInitialize function called by Get handles mutex cleanup.
				sem <- struct{}{}
				defer func() { <-sem }()

				// Skip empty scientific names (double check)
				if name == "" {
					log.Warn("empty scientific name in fetch goroutine",
						logger.String("operation", "image_cache_warmup"))
					return
				}

				if _, err := defaultCache.Get(name); err != nil {
					log.Debug("failed to fetch image during warm-up",
						logger.String("species", name),
						logger.Error(err),
						logger.String("operation", "image_cache_warmup"))
				}
			}(sciName) // Pass the captured name
		}

		if needsImage > 0 {
			log.Info("cache warm-up: species require image fetching",
				logger.Int("species_needing_images", needsImage),
				logger.String("operation", "image_cache_warmup"))
			wg.Wait()
			log.Info("BirdImageCache initialization complete",
				logger.Int("species_fetched", needsImage),
				logger.String("operation", "image_cache_warmup"))
		} else {
			log.Info("BirdImageCache initialized",
				logger.String("status", "all_images_cached"),
				logger.String("operation", "image_cache_warmup"))
		}
	}()
}

// initBirdImageCache initializes the bird image cache by setting up providers,
// selecting a default, and starting a background warm-up process.
func initBirdImageCache(ds datastore.Interface, metrics *observability.Metrics) *imageprovider.BirdImageCache {
	log := GetLogger()
	// 1. Set up the registry and register known providers
	registry, regErr := setupImageProviderRegistry(ds, metrics)
	if regErr != nil {
		// Log errors encountered during provider registration
		log.Warn("image provider registry initialization encountered errors",
			logger.Error(regErr),
			logger.String("operation", "init_image_cache"))
		// Note: We continue even if some providers fail, as others might succeed.
		// The selectDefaultImageProvider logic will handle finding an available provider.
	}

	// Defensive check: Ensure registry is not nil before proceeding.
	if registry == nil {
		log.Error("image provider registry could not be initialized",
			logger.String("operation", "init_image_cache"))
		return nil
	}

	// 2. Select the default cache based on settings and availability
	defaultCache := selectDefaultImageProvider(registry)

	// If no provider could be initialized or selected, return nil
	if defaultCache == nil {
		log.Error("no image providers available or could be initialized",
			logger.String("operation", "init_image_cache"))
		return nil
	}

	// 3. Get the list of all detected species
	speciesList, err := ds.GetAllDetectedSpecies()
	if err != nil {
		log.Warn("failed to get detected species list",
			logger.Error(err),
			logger.String("operation", "init_image_cache"))
		// Continue with an empty list if DB fails, warm-up won't happen
		speciesList = []datastore.Note{}
	}

	// Filter out any species with empty scientific names
	validSpeciesList := make([]datastore.Note, 0, len(speciesList))
	for i := range speciesList {
		if speciesList[i].ScientificName != "" {
			validSpeciesList = append(validSpeciesList, speciesList[i])
		} else {
			log.Warn("found species entry with empty scientific name",
				logger.String("operation", "init_image_cache"))
		}
	}

	if len(validSpeciesList) < len(speciesList) {
		log.Info("filtered species entries with empty scientific names",
			logger.Int("filtered_count", len(speciesList)-len(validSpeciesList)),
			logger.Int("total_count", len(speciesList)),
			logger.Int("valid_count", len(validSpeciesList)),
			logger.String("operation", "init_image_cache"))
	}

	// 4. Start the background cache warm-up process with validated species list
	warmUpImageCacheInBackground(ds, registry, defaultCache, validSpeciesList)

	return defaultCache
}

// initializeBuffers handles initialization of all audio-related buffers
func initializeBuffers(sources []string) error {
	var initErrors []string

	// Initialize analysis buffers
	const analysisBufferSize = conf.BufferSize * 6 // 6x buffer size to avoid underruns
	if err := myaudio.InitAnalysisBuffers(analysisBufferSize, sources); err != nil {
		initErrors = append(initErrors, fmt.Sprintf("failed to initialize analysis buffers: %v", err))
	}

	// Initialize capture buffers using default or extended capture buffer size.
	// EffectiveCaptureBufferSeconds is a pure read-only method that returns the
	// correct buffer size without mutating settings.
	settings := conf.Setting()
	preCapture := settings.Realtime.Audio.Export.PreCapture
	captureBufferSize := settings.Realtime.ExtendedCapture.EffectiveCaptureBufferSeconds(preCapture)
	GetLogger().Info("initializeBuffers: requesting capture buffer allocation",
		logger.Int("capture_buffer_size_seconds", captureBufferSize),
		logger.Bool("extended_capture_enabled", settings.Realtime.ExtendedCapture.Enabled),
		logger.Int("source_count", len(sources)),
		logger.Any("sources", sources))
	if err := myaudio.InitCaptureBuffers(captureBufferSize, conf.SampleRate, conf.BitDepth/8, sources); err != nil {
		initErrors = append(initErrors, fmt.Sprintf("failed to initialize capture buffers: %v", err))
	}

	if len(initErrors) > 0 {
		// Buffer initialization errors are aggregated to provide a complete picture
		// of all failed sources. These are not retryable because they indicate:
		// - Invalid audio source configuration (wrong device names, URLs)
		// - System resource limitations (can't allocate buffer memory)
		// - Permission issues accessing audio devices
		// Context includes buffer parameters to aid in troubleshooting memory issues
		return errors.Newf("buffer initialization errors: %s", strings.Join(initErrors, "; ")).
			Component("analysis.realtime").
			Category(errors.CategoryBuffer).
			Context("operation", "initialize_buffers").
			Context("error_count", len(initErrors)).
			Context("source_count", len(sources)).
			Context("buffer_size", conf.BufferSize*3).
			Context("sample_rate", conf.SampleRate).
			Context("retryable", false). // Buffer init failure is configuration/system issue
			Build()
	}

	return nil
}

// cleanupHLSWithTimeout runs HLS cleanup asynchronously with a timeout to prevent blocking shutdown
func cleanupHLSWithTimeout(ctx context.Context) {
	// Create a channel to signal completion
	cleanupDone := make(chan error, 1)

	// Run cleanup in a goroutine
	go func() {
		cleanupDone <- cleanupHLSStreamingFiles()
	}()

	// Create a timeout context for cleanup operation (2 seconds max)
	cleanupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	log := GetLogger()
	select {
	case err := <-cleanupDone:
		if err != nil {
			log.Warn("failed to clean up HLS streaming files",
				logger.Error(err),
				logger.String("operation", "cleanup_hls_files"))
		}
	case <-cleanupCtx.Done():
		log.Warn("HLS cleanup timeout exceeded, continuing shutdown",
			logger.Duration("timeout", 2*time.Second),
			logger.String("operation", "cleanup_hls_files"))
	}
}

// cleanupHLSStreamingFiles removes any leftover HLS streaming files and directories
// from previous runs of the application to avoid accumulation of unused files.
func cleanupHLSStreamingFiles() error {
	log := GetLogger()
	// Get the HLS directory where all streaming files are stored
	hlsDir, err := conf.GetHLSDirectory()
	if err != nil {
		return errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategoryConfiguration).
			Context("operation", "get_hls_directory").
			Build()
	}

	// Check if the directory exists
	_, err = os.Stat(hlsDir)
	if os.IsNotExist(err) {
		// Directory doesn't exist yet, nothing to clean up
		return nil
	} else if err != nil {
		return errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategoryFileIO).
			Context("operation", "check_hls_directory").
			Context("hls_dir", hlsDir).
			Build()
	}

	// Read the directory entries
	entries, err := os.ReadDir(hlsDir)
	if err != nil {
		return errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategoryFileIO).
			Context("operation", "read_hls_directory").
			Context("hls_dir", hlsDir).
			Build()
	}

	var cleanupErrors []string

	// Remove all stream directories
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "stream_") {
			path := filepath.Join(hlsDir, entry.Name())
			log.Info("removing HLS stream directory",
				logger.String("path", path),
				logger.String("operation", "cleanup_hls_files"))

			// Remove the directory and all its contents
			if err := os.RemoveAll(path); err != nil {
				log.Warn("failed to remove HLS stream directory",
					logger.String("path", path),
					logger.Error(err),
					logger.String("operation", "cleanup_hls_files"))
				cleanupErrors = append(cleanupErrors, fmt.Sprintf("%s: %v", path, err))
				// Continue with other directories
			}
		}
	}

	// Return a combined error if any cleanup operations failed
	if len(cleanupErrors) > 0 {
		return errors.Newf("failed to remove some HLS stream directories: %s", strings.Join(cleanupErrors, "; ")).
			Component("analysis.realtime").
			Category(errors.CategoryFileIO).
			Context("operation", "cleanup_hls_directories").
			Context("hls_dir", hlsDir).
			Context("failed_cleanup_count", len(cleanupErrors)).
			Build()
	}

	return nil
}

// logHLSCleanup logs the result of HLS cleanup operation consistently
func logHLSCleanup(err error) {
	log := GetLogger()
	if err != nil {
		log.Warn("failed to clean up HLS streaming files",
			logger.Error(err),
			logger.String("operation", "cleanup_hls_files"))
	} else {
		log.Info("cleaned up leftover HLS streaming files",
			logger.String("operation", "cleanup_hls_files"))
	}
}

// initializeBackupSystem sets up the backup manager and scheduler.
func initializeBackupSystem(settings *conf.Settings, backupLog logger.Logger) (*backup.Manager, *backup.Scheduler, error) {
	backupLog.Info("Initializing backup system...")

	stateManager, err := backup.NewStateManager(backupLog)
	if err != nil {
		return nil, nil, errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategorySystem).
			Context("operation", "initialize_backup_state_manager").
			Build()
	}

	// Use settings.Version for the app version
	backupManager, err := backup.NewManager(settings, backupLog, stateManager, settings.Version)
	if err != nil {
		return nil, nil, errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategorySystem).
			Context("operation", "initialize_backup_manager").
			Build()
	}
	backupScheduler, err := backup.NewScheduler(backupManager, backupLog, stateManager)
	if err != nil {
		return nil, nil, errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategorySystem).
			Context("operation", "initialize_backup_scheduler").
			Build()
	}

	// Load schedule for backupScheduler if backup is enabled
	switch {
	case settings.Backup.Enabled && len(settings.Backup.Schedules) > 0:
		backupLog.Info("Loading backup schedule from configuration")
		if err := backupScheduler.LoadFromConfig(&settings.Backup); err != nil {
			// Log the error but don't necessarily stop initialization
			backupLog.Error("Failed to load backup schedule from config", logger.Error(err))
		}
	case settings.Backup.Enabled:
		// This case is reached if backup is enabled but no schedules are defined.
		backupLog.Info("Backup enabled, but no schedules configured.")
	default:
		// This case is reached if backup is disabled.
		backupLog.Info("Backup system is disabled.")
	}

	// Start backupManager and backupScheduler if backup is enabled
	if settings.Backup.Enabled {
		backupLog.Info("Starting backup manager")
		if err := backupManager.Start(); err != nil {
			// Log the error but don't necessarily stop initialization
			backupLog.Error("Failed to start backup manager", logger.Error(err))
		}
		backupLog.Info("Starting backup scheduler")
		backupScheduler.Start() // Start the scheduler
	}

	backupLog.Info("Backup system initialized.")
	return backupManager, backupScheduler, nil
}

// initializeSystemMonitor initializes and starts the system resource monitor if enabled
func initializeSystemMonitor(settings *conf.Settings) *monitor.SystemMonitor {
	GetLogger().Info("initializeSystemMonitor called",
		logger.Bool("monitoring_enabled", settings.Realtime.Monitoring.Enabled),
		logger.Int("check_interval", settings.Realtime.Monitoring.CheckInterval),
	)

	if !settings.Realtime.Monitoring.Enabled {
		GetLogger().Warn("System monitoring is disabled in settings")
		return nil
	}

	GetLogger().Info("Creating system monitor instance")
	systemMonitor := monitor.NewSystemMonitor(settings)
	if systemMonitor == nil {
		GetLogger().Error("Failed to create system monitor instance")
		return nil
	}

	GetLogger().Info("Starting system monitor")
	systemMonitor.Start()

	GetLogger().Info("System resource monitoring initialized",
		logger.String("component", "monitor"),
		logger.Int("interval", settings.Realtime.Monitoring.CheckInterval))
	return systemMonitor
}

// InitializeMetrics initializes the Prometheus metrics manager.
func InitializeMetrics() (*observability.Metrics, error) {
	metrics, err := observability.NewMetrics()
	if err != nil {
		return nil, errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategorySystem).
			Context("operation", "initialize_metrics").
			Build()
	}
	return metrics, nil
}

// initializeBirdImageCacheIfNeeded initializes the bird image cache if thumbnails are enabled
// or if we need it for the settings UI to show available providers
func initializeBirdImageCacheIfNeeded(settings *conf.Settings, dataStore datastore.Interface, metrics *observability.Metrics) *imageprovider.BirdImageCache {
	if settings.Realtime.Dashboard.Thumbnails.Summary || settings.Realtime.Dashboard.Thumbnails.Recent {
		return initBirdImageCache(dataStore, metrics)
	}
	// Always initialize the cache so the settings UI can show available providers
	// even when thumbnails are disabled - the cache will just not be used for actual image fetching
	GetLogger().Debug("initializing bird image cache for settings UI (thumbnails disabled)")
	return initBirdImageCache(dataStore, metrics)
}

// initializeAudioSources prepares and validates audio sources
func initializeAudioSources(settings *conf.Settings) ([]string, error) {
	log := GetLogger()
	var sources []string
	if len(settings.Realtime.RTSP.Streams) > 0 || settings.Realtime.Audio.Source != "" {
		if len(settings.Realtime.RTSP.Streams) > 0 {
			// Register RTSP sources in the registry and get their source IDs
			registry := myaudio.GetRegistry()
			if registry == nil {
				return nil, errors.Newf("audio source registry not available").
					Component("analysis").
					Category(errors.CategorySystem).
					Context("operation", "initialize_audio_sources").
					Build()
			}

			var failedSources []string
			for i := range settings.Realtime.RTSP.Streams {
				stream := &settings.Realtime.RTSP.Streams[i]
				if stream.URL == "" {
					log.Warn("skipping stream with empty URL",
						logger.String("stream_name", stream.Name))
					continue
				}

				// Register the source with stream name as display name
				source, err := registry.RegisterSource(stream.URL, myaudio.SourceConfig{
					ID:          "", // Let registry generate UUID
					DisplayName: stream.Name,
					Type:        myaudio.StreamTypeToSourceType(stream.Type),
				})
				if err != nil {
					safeURL := privacy.SanitizeStreamUrl(stream.URL)
					log.Error("failed to register stream source",
						logger.String("stream_name", stream.Name),
						logger.String("stream_url", safeURL),
						logger.Error(err))
					failedSources = append(failedSources, stream.Name)
					continue
				}

				sources = append(sources, source.ID)
			}

			// If some sources failed to register, log a summary
			if len(failedSources) > 0 {
				log.Warn("some stream sources failed to register",
					logger.Int("failed_count", len(failedSources)),
					logger.Int("total_count", len(settings.Realtime.RTSP.Streams)),
					logger.Any("failed_sources", failedSources))
			}
		}
		if settings.Realtime.Audio.Source != "" {
			// Register the audio device in the source registry and use its ID
			// This ensures consistent UUID-based IDs like RTSP sources
			registry := myaudio.GetRegistry()
			source, err := registry.RegisterSource(settings.Realtime.Audio.Source, myaudio.SourceConfig{
				Type: myaudio.SourceTypeAudioCard,
			})
			if err != nil {
				log.Warn("failed to register audio device source",
					logger.String("source", settings.Realtime.Audio.Source),
					logger.Error(err))
			} else {
				sources = append(sources, source.ID)
			}
		}

		// Initialize buffers for all audio sources
		if err := initializeBuffers(sources); err != nil {
			// If buffer initialization fails, log the error but continue
			// Some sources might still work
			log.Warn("error initializing buffers, some audio sources might not be available",
				logger.Error(err))
		}
	} else {
		log.Warn("starting without active audio sources, configure audio devices or RTSP streams through the web interface")
	}
	return sources, nil
}

// PrintSystemDetails prints system information and analyzer configuration.
func PrintSystemDetails(settings *conf.Settings) {
	log := GetLogger()

	// Get system details with gopsutil
	info, err := host.Info()
	if err != nil {
		log.Warn("Failed to retrieve host info", logger.Error(err))
	}

	var hwModel string
	// Print SBC hardware details
	if conf.IsLinuxArm64() {
		hwModel = conf.GetBoardModel()
		// remove possible new line from hwModel
		hwModel = strings.TrimSpace(hwModel)
	} else {
		hwModel = "unknown"
	}

	// Log system details
	log.Info("System details",
		logger.String("os", info.OS),
		logger.String("platform", info.Platform),
		logger.String("platform_version", info.PlatformVersion),
		logger.String("hardware", hwModel))

	// Log the start of BirdNET-Go Analyzer in realtime mode and its configurations.
	log.Info("Starting analyzer in realtime mode",
		logger.Float64("threshold", settings.BirdNET.Threshold),
		logger.Float64("overlap", settings.BirdNET.Overlap),
		logger.Float64("sensitivity", settings.BirdNET.Sensitivity),
		logger.Int("interval", settings.Realtime.Interval))
}

// migrationSetupConfig holds configuration for migration infrastructure setup.
type migrationSetupConfig struct {
	manager     datastoreV2.Manager // Satisfies both SQLite and MySQL managers
	ds          datastore.Interface
	log         logger.Logger
	useV2Prefix bool   // true for MySQL migration (v2_ prefix), false for SQLite (separate file)
	opName      string // For log messages: "initialize_migration_infrastructure" or "initialize_mysql_migration"
}

// setupMigrationWorker performs the common setup after manager creation and initialization.
// It creates repositories, the migration worker, stores the manager for cleanup, and handles state recovery.
// The caller should close the manager if this function returns an error.
func setupMigrationWorker(cfg *migrationSetupConfig) error {
	// Get dialect from manager interface (avoids redundant parameter)
	isMySQL := cfg.manager.IsMySQL()

	// Create the state manager
	stateManager := datastoreV2.NewStateManager(cfg.manager.DB())

	// Create repositories for the migration worker
	v2DB := cfg.manager.DB()

	// Look up required lookup table IDs (seeded during Manager.Initialize())
	var speciesLabelType entities.LabelType
	if err := v2DB.Where("name = ?", "species").FirstOrCreate(&speciesLabelType, entities.LabelType{Name: "species"}).Error; err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_label_type").
			Build()
	}

	var avesClass entities.TaxonomicClass
	if err := v2DB.Where("name = ?", "Aves").FirstOrCreate(&avesClass, entities.TaxonomicClass{Name: "Aves"}).Error; err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "get_aves_taxonomic_class").
			Build()
	}

	// Get default model for related data migration (uses detection package constants)
	var defaultModel entities.AIModel
	if err := v2DB.Where("name = ? AND version = ? AND variant = ?",
		detection.DefaultModelName, detection.DefaultModelVersion, detection.DefaultModelVariant).
		FirstOrCreate(&defaultModel, entities.AIModel{
			Name:      detection.DefaultModelName,
			Version:   detection.DefaultModelVersion,
			Variant:   detection.DefaultModelVariant,
			ModelType: entities.ModelTypeBird,
		}).Error; err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "get_default_model").
			Build()
	}

	labelRepo := repository.NewLabelRepository(v2DB, cfg.useV2Prefix, isMySQL)
	modelRepo := repository.NewModelRepository(v2DB, cfg.useV2Prefix, isMySQL)
	sourceRepo := repository.NewAudioSourceRepository(v2DB, cfg.useV2Prefix, isMySQL)
	v2DetectionRepo := repository.NewDetectionRepository(v2DB, cfg.useV2Prefix, isMySQL)

	// Create repositories for auxiliary data migration
	weatherRepo := repository.NewWeatherRepository(v2DB, cfg.useV2Prefix, isMySQL)
	imageCacheRepo := repository.NewImageCacheRepository(v2DB, labelRepo, cfg.useV2Prefix, isMySQL)
	thresholdRepo := repository.NewDynamicThresholdRepository(v2DB, labelRepo, cfg.useV2Prefix, isMySQL)
	notificationRepo := repository.NewNotificationHistoryRepository(v2DB, labelRepo, cfg.useV2Prefix, isMySQL)

	// Create the legacy detection repository
	legacyRepo := datastore.NewDetectionRepository(cfg.ds, time.Local)

	// Determine batch size and sleep duration based on database type
	// MySQL handles larger batches and concurrent access better than SQLite
	batchSize := migration.DefaultBatchSize
	sleepBetweenBatches := migration.DefaultSleepBetweenBatches
	if isMySQL {
		batchSize = migration.MySQLBatchSize
		sleepBetweenBatches = migration.MySQLSleepBetweenBatches
	}

	// Use datastore logger for migration components (not analysis logger)
	migrationLogger := datastore.GetLogger()

	// Create the related data migrator for reviews, comments, locks, predictions
	// Use half of detection batch size since related data tables are typically smaller
	relatedDataBatchSize := batchSize / 2
	relatedMigrator := migration.NewRelatedDataMigrator(&migration.RelatedDataMigratorConfig{
		LegacyStore:        cfg.ds,
		DetectionRepo:      v2DetectionRepo,
		LabelRepo:          labelRepo,
		StateManager:       stateManager,
		Logger:             migrationLogger,
		BatchSize:          relatedDataBatchSize,
		DefaultModelID:     defaultModel.ID,
		SpeciesLabelTypeID: speciesLabelType.ID,
		AvesClassID:        &avesClass.ID,
	})

	// Create the auxiliary data migrator for weather, thresholds, image cache, notifications
	auxiliaryMigrator := migration.NewAuxiliaryMigrator(&migration.AuxiliaryMigratorConfig{
		LegacyStore:        cfg.ds,
		LabelRepo:          labelRepo,
		WeatherRepo:        weatherRepo,
		ImageCacheRepo:     imageCacheRepo,
		ThresholdRepo:      thresholdRepo,
		NotificationRepo:   notificationRepo,
		Logger:             migrationLogger,
		DefaultModelID:     defaultModel.ID,
		SpeciesLabelTypeID: speciesLabelType.ID,
		AvesClassID:        &avesClass.ID,
	})

	// Determine database type for telemetry
	dbType := "sqlite"
	if isMySQL {
		dbType = "mysql"
	}
	migrationTelemetry := migration.NewMigrationTelemetry(dbType)

	// Create the migration worker
	worker, err := migration.NewWorker(&migration.WorkerConfig{
		Legacy:              legacyRepo,
		V2Detection:         v2DetectionRepo,
		LabelRepo:           labelRepo,
		ModelRepo:           modelRepo,
		SourceRepo:          sourceRepo,
		StateManager:        stateManager,
		RelatedMigrator:     relatedMigrator,
		AuxiliaryMigrator:   auxiliaryMigrator,
		Logger:              migrationLogger,
		BatchSize:           batchSize,
		SleepBetweenBatches: sleepBetweenBatches,
		Timezone:            time.Local,
		UseBatchMode:        isMySQL, // Use efficient batch inserts for MySQL
		SpeciesLabelTypeID:  speciesLabelType.ID,
		AvesClassID:         &avesClass.ID,
		Telemetry:           migrationTelemetry,
	})
	if err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "create_migration_worker").
			Build()
	}

	// Inject dependencies into the API layer
	apiv2.SetMigrationDependencies(stateManager, worker)
	apiv2.SetMigrationTelemetry(migrationTelemetry)

	// Check for state recovery - resume migration if it was in progress
	state, err := stateManager.GetState()
	if err != nil {
		migrationLogger.Warn("failed to get migration state for recovery",
			logger.Error(err),
			logger.String("operation", cfg.opName))
	} else {
		migrationLogger.Info("migration infrastructure initialized",
			logger.String("state", string(state.State)),
			logger.Int64("migrated_records", state.MigratedRecords),
			logger.Int64("total_records", state.TotalRecords),
			logger.String("operation", cfg.opName))

		// Resume worker if migration was in progress, or if migration completed
		// but we're running in legacy mode due to unmigrated records from a crash.
		// In the COMPLETED case, the worker enters tail sync to drain stragglers.
		if state.State == entities.MigrationStatusDualWrite ||
			state.State == entities.MigrationStatusMigrating ||
			state.State == entities.MigrationStatusCompleted {
			migrationLogger.Info("resuming migration worker after restart",
				logger.String("state", string(state.State)),
				logger.String("operation", cfg.opName))
			// Create cancellable context for the worker - this allows graceful shutdown
			// to stop the worker by cancelling this context
			workerCtx, workerCancel := context.WithCancel(context.Background())
			apiv2.SetMigrationWorkerCancel(workerCancel)
			if startErr := worker.Start(workerCtx); startErr != nil {
				workerCancel() // Clean up on failure
				migrationLogger.Warn("failed to resume migration worker",
					logger.Error(startErr),
					logger.String("operation", cfg.opName))
			}
		}
	}

	return nil
}

// initializeMigrationInfrastructure sets up the v2 database migration infrastructure.
// This creates the StateManager and Worker instances needed for the migration API.
// The function handles state recovery on restart and resumes migration if it was in progress.
func initializeMigrationInfrastructure(settings *conf.Settings, ds datastore.Interface) (datastoreV2.Manager, error) {
	log := GetLogger()

	// Get the database directory from the legacy database path
	var dataDir string
	switch {
	case settings.Output.SQLite.Enabled:
		dataDir = datastoreV2.GetDataDirFromLegacyPath(settings.Output.SQLite.Path)
	case settings.Output.MySQL.Enabled:
		// MySQL uses v2_ prefixed tables in the same database
		mgr, err := initializeMySQLMigrationInfrastructure(settings, ds, log)
		return mgr, err
	default:
		log.Debug("no database configured, skipping migration infrastructure",
			logger.String("operation", "initialize_migration_infrastructure"))
		return nil, nil //nolint:nilnil // nil manager is valid when no database is configured
	}

	// Check if dataDir is empty (in-memory database)
	if dataDir == "" {
		log.Debug("in-memory database detected, skipping migration infrastructure",
			logger.String("operation", "initialize_migration_infrastructure"))
		return nil, nil //nolint:nilnil // nil manager is valid for in-memory databases
	}

	// Create v2 database manager
	// Use ConfiguredPath to properly derive v2 migration path from configured filename
	v2Manager, err := datastoreV2.NewSQLiteManager(datastoreV2.Config{
		ConfiguredPath: settings.Output.SQLite.Path,
		Debug:          settings.Debug,
		Logger:         log,
	})
	if err != nil {
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "create_v2_database_manager").
			Build()
	}

	// Initialize the v2 database schema
	if err := v2Manager.Initialize(); err != nil {
		if closeErr := v2Manager.Close(); closeErr != nil {
			log.Warn("failed to close v2 manager after initialization failure",
				logger.Error(closeErr),
				logger.String("operation", "initialize_migration_infrastructure"))
		}
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "initialize_v2_database").
			Build()
	}

	// Setup the migration worker using the common helper
	if err := setupMigrationWorker(&migrationSetupConfig{
		manager:     v2Manager,
		ds:          ds,
		log:         log,
		useV2Prefix: false, // SQLite uses separate file, not prefixed tables
		opName:      "initialize_migration_infrastructure",
	}); err != nil {
		if closeErr := v2Manager.Close(); closeErr != nil {
			log.Warn("failed to close v2 manager after worker setup failure",
				logger.Error(closeErr),
				logger.String("operation", "initialize_migration_infrastructure"))
		}
		return nil, err
	}

	return v2Manager, nil
}

// initializeMySQLMigrationInfrastructure sets up migration infrastructure for MySQL.
// Unlike SQLite which uses a separate file, MySQL shares the same database.
// V2 tables use the v2_ prefix to avoid collisions with legacy auxiliary tables
// (e.g., dynamic_thresholds, image_caches) that share the same base names.
func initializeMySQLMigrationInfrastructure(settings *conf.Settings, ds datastore.Interface, log logger.Logger) (datastoreV2.Manager, error) {
	// Create v2 MySQL manager with v2_ prefix to avoid collisions with legacy
	// auxiliary tables that share the same base names. TableName() methods have
	// been removed so NamingStrategy.TablePrefix now takes effect.
	v2Manager, err := datastoreV2.NewMySQLManager(&datastoreV2.MySQLConfig{
		Host:        settings.Output.MySQL.Host,
		Port:        settings.Output.MySQL.Port,
		Username:    settings.Output.MySQL.Username,
		Password:    settings.Output.MySQL.Password,
		Database:    settings.Output.MySQL.Database,
		UseV2Prefix: true, // v2_ prefix avoids collisions with legacy auxiliary tables
		Debug:       settings.Debug,
	})
	if err != nil {
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "create_mysql_v2_manager").
			Build()
	}

	// Initialize the v2 schema (creates tables with v2_ prefix)
	if err := v2Manager.Initialize(); err != nil {
		if closeErr := v2Manager.Close(); closeErr != nil {
			log.Warn("failed to close MySQL v2 manager after initialization failure",
				logger.Error(closeErr),
				logger.String("operation", "initialize_mysql_migration"))
		}
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "initialize_mysql_v2_schema").
			Build()
	}

	// Setup the migration worker using the common helper.
	// useV2Prefix is true so the migration worker creates v2_ prefixed tables,
	// avoiding collisions with legacy auxiliary tables that share the same base names.
	if err := setupMigrationWorker(&migrationSetupConfig{
		manager:     v2Manager,
		ds:          ds,
		log:         log,
		useV2Prefix: true, // v2_ prefix avoids collisions with legacy auxiliary tables
		opName:      "initialize_mysql_migration",
	}); err != nil {
		if closeErr := v2Manager.Close(); closeErr != nil {
			log.Warn("failed to close MySQL v2 manager after worker setup failure",
				logger.Error(closeErr),
				logger.String("operation", "initialize_mysql_migration"))
		}
		return nil, err
	}

	return v2Manager, nil
}

// initializeV2OnlyMode creates a V2OnlyDatastore when migration is complete.
// This allows the application to run without opening the legacy database.
// It handles both:
//   - Fresh installs: v2 schema at configured path (no _v2 suffix, no v2_ prefix)
//   - Post-migration: v2 schema at migration path (_v2 suffix, v2_ prefix)
func initializeV2OnlyMode(settings *conf.Settings) (*v2only.Datastore, error) {
	log := logger.Global().Module("datastore")
	log.Info("initializing enhanced database mode",
		logger.String("operation", "initialize_enhanced_database_mode"))

	// Determine configuration based on database type
	var v2Manager datastoreV2.Manager
	var useV2Prefix bool
	var err error

	switch {
	case settings.Output.SQLite.Enabled:
		configuredPath := settings.Output.SQLite.Path
		migrationPath := datastoreV2.V2MigrationPathFromConfigured(configuredPath)

		// Determine if v2 schema is at configured path (fresh/post-consolidation) or migration path
		if datastoreV2.CheckSQLiteHasV2Schema(configuredPath) {
			// Fresh install restart or post-consolidation: use configured path directly
			log.Debug("v2 schema found at configured path",
				logger.String("path", configuredPath))
			v2Manager, err = datastoreV2.NewSQLiteManager(datastoreV2.Config{
				DirectPath: configuredPath,
				Debug:      settings.Debug,
				Logger:     log,
			})
			useV2Prefix = false
		} else {
			// Migration mode: use derived v2 migration path
			log.Debug("using migration v2 database path",
				logger.String("path", migrationPath))
			v2Manager, err = datastoreV2.NewSQLiteManager(datastoreV2.Config{
				ConfiguredPath: configuredPath,
				Debug:          settings.Debug,
				Logger:         log,
			})
			useV2Prefix = false
		}

	case settings.Output.MySQL.Enabled:
		// Check if fresh v2 tables exist (no prefix) or migration tables (v2_ prefix)
		isFreshV2 := datastoreV2.CheckMySQLHasFreshV2Schema(settings)
		useV2Prefix = !isFreshV2

		log.Debug("MySQL v2 mode configuration",
			logger.Bool("use_v2_prefix", useV2Prefix),
			logger.Bool("is_fresh_v2", isFreshV2))

		v2Manager, err = datastoreV2.NewMySQLManager(&datastoreV2.MySQLConfig{
			Host:        settings.Output.MySQL.Host,
			Port:        settings.Output.MySQL.Port,
			Username:    settings.Output.MySQL.Username,
			Password:    settings.Output.MySQL.Password,
			Database:    settings.Output.MySQL.Database,
			UseV2Prefix: useV2Prefix,
			Debug:       settings.Debug,
		})

	default:
		return nil, errors.Newf("no database configured").
			Component("analysis").
			Category(errors.CategoryConfiguration).
			Context("operation", "initialize_v2_only_mode").
			Build()
	}

	if err != nil {
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "create_v2_database_manager").
			Build()
	}

	// Initialize the v2 database schema (ensures auxiliary tables exist)
	if err := v2Manager.Initialize(); err != nil {
		_ = v2Manager.Close()
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "initialize_v2_database").
			Build()
	}

	// Create repositories
	v2DB := v2Manager.DB()
	isMySQL := settings.Output.MySQL.Enabled // Determine dialect from settings
	detectionRepo := repository.NewDetectionRepository(v2DB, useV2Prefix, isMySQL)
	labelRepo := repository.NewLabelRepository(v2DB, useV2Prefix, isMySQL)
	modelRepo := repository.NewModelRepository(v2DB, useV2Prefix, isMySQL)
	sourceRepo := repository.NewAudioSourceRepository(v2DB, useV2Prefix, isMySQL)
	weatherRepo := repository.NewWeatherRepository(v2DB, useV2Prefix, isMySQL)
	imageCacheRepo := repository.NewImageCacheRepository(v2DB, labelRepo, useV2Prefix, isMySQL)
	thresholdRepo := repository.NewDynamicThresholdRepository(v2DB, labelRepo, useV2Prefix, isMySQL)
	notificationRepo := repository.NewNotificationHistoryRepository(v2DB, labelRepo, useV2Prefix, isMySQL)

	// Load eBird taxonomy for species code lookups in analytics endpoints.
	_, scientificIndex, taxonomyErr := birdnet.LoadTaxonomyData("")
	if taxonomyErr != nil {
		log.Warn("failed to load taxonomy data for species codes",
			logger.String("error", taxonomyErr.Error()))
	}

	// Create V2OnlyDatastore
	ds, err := v2only.New(&v2only.Config{
		Manager:        v2Manager,
		Detection:      detectionRepo,
		Label:          labelRepo,
		Model:          modelRepo,
		Source:         sourceRepo,
		Weather:        weatherRepo,
		ImageCache:     imageCacheRepo,
		Threshold:      thresholdRepo,
		Notification:   notificationRepo,
		Logger:         log,
		Timezone:       time.Local,
		Labels:         settings.BirdNET.Labels, // For GetThresholdEvents workaround (#1907)
		SpeciesCodeMap: scientificIndex,
	})
	if err != nil {
		_ = v2Manager.Close()
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "create_v2_only_datastore").
			Build()
	}

	log.Info("enhanced database mode initialized successfully",
		logger.String("operation", "initialize_enhanced_database_mode"))

	return ds, nil
}
