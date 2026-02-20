package analysis

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/alerting"
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
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"github.com/tphakala/birdnet-go/internal/weather"
)

// Constants for system operations
const (
	// shutdownTimeout is the maximum time allowed for graceful shutdown (9s for Docker's 10s default)
	shutdownTimeout = 9 * time.Second
)

// audioLevelChan is a channel to send audio level updates
var audioLevelChan = make(chan myaudio.AudioLevelData, 100)

// soundLevelChan is a channel to send sound level updates
var soundLevelChan = make(chan myaudio.SoundLevelData, 100)

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

// Global audio demux manager instance
var audioDemuxManager = NewAudioDemuxManager()

// v2DatabaseManager holds the v2 database manager for shutdown cleanup.
// This is set by initializeMigrationInfrastructure and cleared on shutdown.
var v2DatabaseManager datastoreV2.Manager
var v2DatabaseManagerMu sync.Mutex

// GetV2DatabaseManager returns the v2 database manager.
// Returns nil if the migration infrastructure is not initialized.
func GetV2DatabaseManager() datastoreV2.Manager {
	v2DatabaseManagerMu.Lock()
	defer v2DatabaseManagerMu.Unlock()
	return v2DatabaseManager
}

// RealtimeAnalysis initiates the BirdNET Analyzer in real-time mode and waits for a termination signal.
//
//nolint:gocognit,gocyclo // This is the main orchestration function that coordinates multiple subsystems during startup and shutdown
func RealtimeAnalysis(settings *conf.Settings) error {
	// Initialize BirdNET interpreter
	if err := initializeBirdNET(settings); err != nil {
		// Model initialization failures are not retryable because they indicate:
		// - Missing or corrupted model files
		// - Insufficient system resources (memory, GPU)
		// - Incompatible TensorFlow runtime
		// These require manual intervention to resolve
		return errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategoryModelInit).
			Context("operation", "initialize_birdnet").
			Context("model_path", settings.BirdNET.ModelPath).
			Context("label_path", settings.BirdNET.LabelPath).
			Context("overlap", settings.BirdNET.Overlap).
			Context("retryable", false). // Model initialization failure is not retryable
			Build()
	}

	// Clean up any leftover HLS streaming files from previous runs
	if err := cleanupHLSStreamingFiles(); err != nil {
		logHLSCleanup(err)
	} else {
		logHLSCleanup(nil)
	}

	// Initialize occurrence monitor to filter out repeated observations.
	// TODO FIXME
	//ctx.OccurrenceMonitor = conf.NewOccurrenceMonitor(time.Duration(ctx.Settings.Realtime.Interval) * time.Second)

	// Print system details and configuration
	printSystemDetails(settings)

	// Check for and perform database consolidation if needed (SQLite only)
	// This moves the v2 database from migration path to configured path after migration completes
	if settings.Output.SQLite.Enabled {
		datastoreLog := logger.Global().Module("datastore")
		consolidated, err := datastoreV2.CheckAndConsolidateAtStartup(settings.Output.SQLite.Path, datastoreLog)
		if err != nil {
			datastoreLog.Error("database consolidation failed", logger.Error(err))
			// Continue with normal startup - consolidation can be retried
		} else if consolidated {
			datastoreLog.Info("database consolidation completed, continuing startup")
		}
	}

	// Check migration state before initializing database
	// This allows us to skip the legacy database when migration is complete
	startupState := datastoreV2.CheckMigrationStateBeforeStartup(settings)
	v2OnlyMode := startupState.MigrationStatus == entities.MigrationStatusCompleted && startupState.V2Available
	freshInstall := startupState.FreshInstall

	// Log startup mode detection - use datastore module for database mode messages
	datastoreLog := logger.Global().Module("datastore")
	switch {
	case v2OnlyMode:
		datastoreLog.Info("migration completed, starting in enhanced database mode",
			logger.String("migration_status", string(startupState.MigrationStatus)),
			logger.String("operation", "startup_mode_check"))
	case freshInstall:
		GetLogger().Info("fresh installation detected, initializing v2 schema",
			logger.String("database_path", settings.Output.SQLite.Path),
			logger.String("operation", "startup_mode_check"))
	default:
		GetLogger().Debug("migration state check completed",
			logger.String("migration_status", string(startupState.MigrationStatus)),
			logger.Bool("v2_available", startupState.V2Available),
			logger.Bool("legacy_required", startupState.LegacyRequired),
			logger.String("operation", "startup_mode_check"))
	}

	// Initialize database access based on startup state
	var dataStore datastore.Interface
	var v2OnlyDatastore *v2only.Datastore

	switch {
	case v2OnlyMode:
		// Post-migration: use birdnet_v2.db with V2OnlyDatastore
		var err error
		v2OnlyDatastore, err = initializeV2OnlyMode(settings)
		if err != nil {
			// Enhanced database mode failed, fall back to legacy startup
			datastoreLog.Warn("enhanced database mode initialization failed, falling back to legacy mode",
				logger.Error(err),
				logger.String("operation", "initialize_enhanced_database_mode"))
			dataStore = datastore.New(settings)
			v2OnlyMode = false
		} else {
			dataStore = v2OnlyDatastore
			// Set global enhanced database flag
			datastoreV2.SetEnhancedDatabaseMode()
			// Notify the API layer that we're in v2-only mode
			apiv2.SetV2OnlyMode()
			// Set the v2 database manager for API access
			v2DatabaseManagerMu.Lock()
			v2DatabaseManager = v2OnlyDatastore.Manager()
			v2DatabaseManagerMu.Unlock()
		}

	case freshInstall:
		// Fresh install: create at configured path with v2 schema
		var err error
		v2OnlyDatastore, err = v2only.InitializeFreshInstall(settings, GetLogger())
		if err != nil {
			// Fresh install failed, fall back to legacy mode
			GetLogger().Warn("fresh install failed, falling back to legacy mode",
				logger.Error(err),
				logger.String("operation", "initialize_fresh_install"))
			dataStore = datastore.New(settings)
		} else {
			dataStore = v2OnlyDatastore
			// Fresh install is now effectively v2-only mode
			v2OnlyMode = true
			// Set global enhanced database flag
			datastoreV2.SetEnhancedDatabaseMode()
			// Notify the API layer that we're in v2-only mode
			apiv2.SetV2OnlyMode()
			// Set the v2 database manager for API access
			v2DatabaseManagerMu.Lock()
			v2DatabaseManager = v2OnlyDatastore.Manager()
			v2DatabaseManagerMu.Unlock()
		}

	default:
		// Legacy mode: use legacy datastore
		dataStore = datastore.New(settings)
	}

	// Initialize the control channel for restart control.
	controlChan := make(chan string, 1)
	// Initialize the restart channel for capture restart control.
	restartChan := make(chan struct{}, 10) // Increased buffer to prevent dropped restart signals
	// quitChannel is used to signal the goroutines to stop.
	quitChan := make(chan struct{})

	// audioLevelChan and soundLevelChan are already initialized as global variables at package level

	// Initialize audio sources
	sources, err := initializeAudioSources(settings)
	if err != nil {
		// Non-fatal error, continue with available sources
		GetLogger().Warn("audio source initialization warning",
			logger.Error(err),
			logger.String("operation", "initialize_audio_sources"))
	}

	// Queue is now initialized at package level in birdnet package
	// Resize the queue based on processing needs
	// TODO: Make this configurable via settings
	const defaultQueueSize = 5
	birdnet.ResizeQueue(defaultQueueSize)

	// Initialize Prometheus metrics manager
	metrics, err := initializeMetrics()
	if err != nil {
		return errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategorySystem).
			Context("operation", "initialize_metrics").
			Build()
	}

	// Update BirdNET model loaded metric now that metrics are available
	UpdateBirdNETModelLoadedMetric(metrics.BirdNET)

	// Connect metrics to datastore before opening
	dataStore.SetMetrics(metrics.Datastore)
	dataStore.SetSunCalcMetrics(metrics.SunCalc)

	// Only validate disk space and open for legacy mode (v2-only mode already opened)
	if !v2OnlyMode {
		// Validate disk space before attempting to open the database
		// This prevents startup failures due to insufficient disk space
		// ValidateStartupDiskSpace already returns a fully structured error, so we return it directly
		if err := datastore.ValidateStartupDiskSpace(settings.Output.SQLite.Path); err != nil {
			return err
		}

		// Open a connection to the database and handle possible errors.
		if err := dataStore.Open(); err != nil {
			return err // Return error to stop execution if database connection fails.
		}
	}
	// Ensure the database connection is closed when the function returns.
	defer closeDataStore(dataStore)

	// Note: datastore monitoring is automatically started when the database is opened

	// Initialize bird image cache if needed
	birdImageCache := initializeBirdImageCacheIfNeeded(settings, dataStore, metrics)

	// Initialize processor with analysis logger for hierarchical logging
	proc := processor.New(settings, dataStore, bn, metrics, birdImageCache, GetLogger())

	// Initialize Backup system using centralized logger
	backupLog := logger.Global().Module("backup")
	backupManager, backupScheduler, err := initializeBackupSystem(settings, backupLog)
	if err != nil {
		// Log the specific error from initialization
		backupLog.Error("Failed to initialize backup system", logger.Error(err))
		// Don't make this fatal - continue without backup system
		GetLogger().Warn("backup system initialization failed",
			logger.Error(err),
			logger.String("operation", "backup_system_init"))
	} else {
		// Store backup manager and scheduler in the processor for access by control monitor
		proc.SetBackupManager(backupManager)
		proc.SetBackupScheduler(backupScheduler)
	}

	// Initialize async services (event bus, notification workers, telemetry workers)
	if err := telemetry.InitializeAsyncSystems(); err != nil {
		GetLogger().Error("failed to initialize critical async services",
			logger.Error(err),
			logger.String("operation", "initialize_async_systems"))
		return errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategorySystem).
			Context("operation", "initialize_async_systems").
			Build()
	}

	// Initialize system monitor if monitoring is enabled
	systemMonitor := initializeSystemMonitor(settings)

	// Initialize v2 migration infrastructure only if not in enhanced database mode
	// In enhanced database mode, migration is already complete - no need for migration infrastructure
	if !v2OnlyMode {
		// This sets up the StateManager and Worker for the database migration API
		if err := initializeMigrationInfrastructure(settings, dataStore); err != nil {
			// Migration infrastructure is optional - log warning but continue
			GetLogger().Warn("migration infrastructure initialization failed",
				logger.Error(err),
				logger.String("operation", "initialize_migration_infrastructure"))
		}
	} else {
		datastoreLog.Debug("skipping migration infrastructure in enhanced database mode",
			logger.String("operation", "initialize_migration_infrastructure"))
	}
	// Ensure v2 database is closed on shutdown (handles nil case gracefully)
	defer closeV2Database()

	// Initialize and start the HTTP server
	GetLogger().Info("starting HTTP server")
	oauth2Server := security.NewOAuth2Server()
	sunCalc := suncalc.NewSunCalc(settings.BirdNET.Latitude, settings.BirdNET.Longitude)
	apiServer, err := api.New(
		settings,
		api.WithDataStore(dataStore),
		api.WithBirdImageCache(birdImageCache),
		api.WithProcessor(proc),
		api.WithMetrics(metrics),
		api.WithControlChannel(controlChan),
		api.WithAudioLevelChannel(audioLevelChan),
		api.WithOAuth2Server(oauth2Server),
		api.WithSunCalc(sunCalc),
		api.WithV2Manager(GetV2DatabaseManager()),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP server: %w", err)
	}
	apiServer.Start()

	// Initialize the wait group to wait for all goroutines to finish
	var wg sync.WaitGroup

	// Initialize the buffer manager
	bufferManager := MustNewBufferManager(bn, quitChan, &wg)

	// Start buffer monitors for each audio source only if we have active sources
	if len(settings.Realtime.RTSP.Streams) > 0 || settings.Realtime.Audio.Source != "" {
		if err := bufferManager.UpdateMonitors(sources); err != nil {
			// Use structured logging to improve error visibility and triage
			// Extract error details from the enhanced error if available
			errorStr := err.Error()
			GetLogger().Warn("buffer monitor setup completed with errors",
				logger.String("error", errorStr),
				logger.Int("source_count", len(sources)),
				logger.Any("sources", sources),
				logger.String("component", "analysis.realtime"),
				logger.String("operation", "buffer_monitor_setup"))
			// Note: We continue execution as buffer monitoring errors are not critical for startup
		}
	} else {
		GetLogger().Warn("starting without active audio sources",
			logger.Int("rtsp_streams", len(settings.Realtime.RTSP.Streams)),
			logger.String("audio_source", settings.Realtime.Audio.Source),
			logger.String("operation", "startup_audio_check"))
	}

	// start audio capture
	startAudioCapture(&wg, settings, quitChan, restartChan, audioLevelChan, soundLevelChan)

	// Publish application started alert event
	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeApplication,
		EventName:  alerting.EventApplicationStarted,
		Properties: map[string]any{},
	})

	// Sound level monitoring is now managed by the control monitor for hot reload support.
	// The control monitor will start sound level monitoring if enabled in settings.

	// RTSP health monitoring is now built into the FFmpeg manager
	if len(settings.Realtime.RTSP.Streams) > 0 {
		GetLogger().Info("RTSP streams will be monitored by FFmpeg manager",
			logger.Int("stream_count", len(settings.Realtime.RTSP.Streams)),
			logger.String("operation", "rtsp_monitoring_setup"))
	}

	// start cleanup of clips
	if conf.Setting().Realtime.Audio.Export.Retention.Policy != "none" {
		startClipCleanupMonitor(&wg, quitChan, dataStore)
	}

	// start weather polling
	if settings.Realtime.Weather.Provider != "none" {
		startWeatherPolling(&wg, settings, dataStore, metrics, quitChan)
	}

	// Telemetry endpoint initialization is handled by control monitor for hot reload support.
	// Unlike other services that start directly here, telemetry is managed by the control monitor
	// to allow users to dynamically enable/disable metrics and change the listen address without
	// restarting the application. The control monitor will start the endpoint if enabled.

	// start control monitor for hot reloads
	ctrlMonitor := startControlMonitor(&wg, controlChan, quitChan, restartChan, bufferManager, proc, apiServer.APIController(), metrics)

	// start shutdown signal monitor
	monitorShutdownSignals(quitChan)

	// Track the HTTP server, system monitor and control monitor for clean shutdown
	httpServerRef := apiServer
	systemMonitorRef := systemMonitor
	ctrlMonitorRef := ctrlMonitor

	// loop to monitor quit and restart channels
	for {
		select {
		case <-quitChan:
			log := GetLogger()

			// Publish application stopped alert event
			alerting.TryPublish(&alerting.AlertEvent{
				ObjectType: alerting.ObjectTypeApplication,
				EventName:  alerting.EventApplicationStopped,
				Properties: map[string]any{},
			})

			log.Info("initiating graceful shutdown sequence",
				logger.Float64("shutdown_timeout_seconds", shutdownTimeout.Seconds()),
				logger.String("operation", "graceful_shutdown"))
			shutdownStart := time.Now()

			// Stop migration worker FIRST - before any other shutdown steps
			// This ensures the worker stops even if later steps timeout
			log.Info("stopping migration worker early in shutdown",
				logger.String("operation", "shutdown_migration_worker_early"))
			apiv2.StopMigrationWorker()

			// Create context with timeout for the entire shutdown process
			ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)

			// Execute shutdown with context
			shutdownComplete := make(chan struct{})
			go func() {
				defer close(shutdownComplete)

				// Step 1: Signal shutdown started (but don't close controlChan yet)
				log.Info("shutdown step 1: beginning shutdown sequence",
					logger.Int("step", 1),
					logger.String("operation", "shutdown_begin"))

				// Check context cancellation between steps
				if ctx.Err() != nil {
					log.Warn("shutdown context cancelled after step 1",
						logger.Int("step", 1),
						logger.Error(ctx.Err()),
						logger.String("operation", "shutdown_timeout"))
					return
				}

				// Step 2: Stop control monitor
				if ctrlMonitorRef != nil {
					log.Info("shutdown step 2: stopping control monitor",
						logger.Int("step", 2),
						logger.String("operation", "shutdown_control_monitor"))
					ctrlMonitorRef.Stop()
				}

				if ctx.Err() != nil {
					log.Warn("shutdown context cancelled after step 2",
						logger.Int("step", 2),
						logger.Error(ctx.Err()),
						logger.String("operation", "shutdown_timeout"))
					return
				}

				// Step 3: Stop analysis buffer monitors
				log.Info("shutdown step 3: stopping analysis buffer monitors",
					logger.Int("step", 3),
					logger.String("operation", "shutdown_buffer_monitors"))
				bufferManager.RemoveAllMonitors()

				if ctx.Err() != nil {
					log.Warn("shutdown context cancelled after step 3",
						logger.Int("step", 3),
						logger.Error(ctx.Err()),
						logger.String("operation", "shutdown_timeout"))
					return
				}

				// Step 4: Clean up HLS resources asynchronously with timeout
				log.Info("shutdown step 4: cleaning up HLS resources",
					logger.Int("step", 4),
					logger.String("operation", "shutdown_hls_cleanup"))
				cleanupHLSWithTimeout(ctx)

				if ctx.Err() != nil {
					log.Warn("shutdown context cancelled after step 4",
						logger.Int("step", 4),
						logger.Error(ctx.Err()),
						logger.String("operation", "shutdown_timeout"))
					return
				}

				// Step 5: Shutdown HTTP server
				if httpServerRef != nil {
					log.Info("shutdown step 5: shutting down HTTP server",
						logger.Int("step", 5),
						logger.String("operation", "shutdown_http_server"))
					if err := httpServerRef.Shutdown(); err != nil {
						log.Warn("error shutting down HTTP server",
							logger.Error(err),
							logger.Int("step", 5),
							logger.String("operation", "shutdown_http_server"))
					}
				}

				// Now it's safe to close controlChan after HTTP server is down
				log.Info("closing control channel after producers shutdown",
					logger.String("operation", "close_control_channel"))
				close(controlChan)

				if ctx.Err() != nil {
					log.Warn("shutdown context cancelled after step 5",
						logger.Int("step", 5),
						logger.Error(ctx.Err()),
						logger.String("operation", "shutdown_timeout"))
					return
				}

				// Step 6: Wait for all goroutines
				log.Info("shutdown step 6: waiting for goroutines to finish",
					logger.Int("step", 6),
					logger.String("operation", "shutdown_wait_goroutines"))
				wg.Wait()

				if ctx.Err() != nil {
					log.Warn("shutdown context cancelled after step 6",
						logger.Int("step", 6),
						logger.Error(ctx.Err()),
						logger.String("operation", "shutdown_timeout"))
					return
				}

				// Step 7: Stop system monitor
				if systemMonitorRef != nil {
					log.Info("shutdown step 7: stopping system monitor",
						logger.Int("step", 7),
						logger.String("operation", "shutdown_system_monitor"))
					systemMonitorRef.Stop()
				}

				if ctx.Err() != nil {
					log.Warn("shutdown context cancelled after step 7",
						logger.Int("step", 7),
						logger.Error(ctx.Err()),
						logger.String("operation", "shutdown_timeout"))
					return
				}

				// Step 8: Stop notification service
				if notification.IsInitialized() {
					log.Info("shutdown step 8: stopping notification service",
						logger.Int("step", 8),
						logger.String("operation", "shutdown_notification_service"))
					if service := notification.GetService(); service != nil {
						service.Stop()
					}
				}

				if ctx.Err() != nil {
					log.Warn("shutdown context cancelled after step 8",
						logger.Int("step", 8),
						logger.Error(ctx.Err()),
						logger.String("operation", "shutdown_timeout"))
					return
				}

				// Step 9: Delete BirdNET interpreter
				log.Info("shutdown step 9: cleaning up BirdNET interpreter",
					logger.Int("step", 9),
					logger.String("operation", "shutdown_birdnet_cleanup"))
				bn.Delete()

				// Step 10: Stop migration worker (before closing databases)
				log.Info("shutdown step 10: stopping migration worker",
					logger.Int("step", 10),
					logger.String("operation", "shutdown_migration_worker"))
				apiv2.StopMigrationWorker()

				// Step 11: Close v2 database (before legacy database closes via deferred closeDataStore)
				log.Info("shutdown step 11: closing v2 database",
					logger.Int("step", 11),
					logger.String("operation", "shutdown_v2_database"))
				closeV2Database()

				log.Info("graceful shutdown completed",
					logger.Int64("duration_ms", time.Since(shutdownStart).Milliseconds()),
					logger.String("operation", "shutdown_complete"))
			}()

			// Wait for shutdown to complete or context timeout
			select {
			case <-shutdownComplete:
				// Shutdown completed successfully
				cancel()
				return nil
			case <-ctx.Done():
				GetLogger().Warn("shutdown timeout exceeded, forcing exit",
					logger.Float64("timeout_seconds", shutdownTimeout.Seconds()),
					logger.String("operation", "shutdown_forced_exit"))
				// Ensure migration worker is stopped on timeout
				apiv2.StopMigrationWorker()
				cancel()
				return nil
			}

		case <-restartChan:
			// Handle the restart signal.
			GetLogger().Info("restarting audio capture",
				logger.String("operation", "restart_audio_capture"))
			startAudioCapture(&wg, settings, quitChan, restartChan, audioLevelChan, soundLevelChan)
		}
	}
}

// startAudioCapture initializes and starts the audio capture routine in a new goroutine.
func startAudioCapture(wg *sync.WaitGroup, settings *conf.Settings, quitChan, restartChan chan struct{}, audioLevelChan chan myaudio.AudioLevelData, soundLevelChan chan myaudio.SoundLevelData) {
	// Stop previous demultiplexing goroutine if it exists
	audioDemuxManager.Stop()

	// Start new demux goroutine
	doneChan := audioDemuxManager.Start()

	// Create a unified audio channel
	unifiedAudioChan := make(chan myaudio.UnifiedAudioData, 100)
	go func() {
		defer audioDemuxManager.Done()

		// Convert unified audio data back to separate channels for existing handlers
		for {
			select {
			case <-doneChan:
				// Exit when signaled
				return
			case <-quitChan:
				// Exit when quit signal received
				return
			case unifiedData, ok := <-unifiedAudioChan:
				if !ok {
					// Channel closed, exit
					return
				}

				// Send audio level data to existing audio level channel with safety check
				select {
				case <-doneChan:
					return
				case <-quitChan:
					return
				case audioLevelChan <- unifiedData.AudioLevel:
				default:
					// Channel full, drop data
				}

				// Send sound level data to existing sound level channel if present
				if unifiedData.SoundLevel != nil {
					select {
					case <-doneChan:
						return
					case <-quitChan:
						return
					case soundLevelChan <- *unifiedData.SoundLevel:
					default:
						// Channel full, drop data
					}
				}
			}
		}
	}()

	// waitgroup is managed within CaptureAudio
	go myaudio.CaptureAudio(settings, wg, quitChan, restartChan, unifiedAudioChan)
}

// startClipCleanupMonitor initializes and starts the clip cleanup monitoring routine in a new goroutine.
func startClipCleanupMonitor(wg *sync.WaitGroup, quitChan chan struct{}, dataStore datastore.Interface) {
	wg.Go(func() {
		clipCleanupMonitor(quitChan, dataStore)
	})
}

// startWeatherPolling initializes and starts the weather polling routine in a new goroutine.
func startWeatherPolling(wg *sync.WaitGroup, settings *conf.Settings, dataStore datastore.Interface, metrics *observability.Metrics, quitChan chan struct{}) {
	// Create new weather service
	weatherService, err := weather.NewService(settings, dataStore, metrics.Weather)
	if err != nil {
		GetLogger().Error("failed to initialize weather service",
			logger.Error(err),
			logger.String("operation", "initialize_weather_service"))
		return
	}

	wg.Go(func() {
		weatherService.StartPolling(quitChan)
	})
}

// monitorShutdownSignals listens for shutdown signals (SIGINT, SIGTERM) and triggers the application shutdown process.
func monitorShutdownSignals(quitChan chan struct{}) {
	go func() {
		sigChan := make(chan os.Signal, 1)
		// Register to receive both SIGINT (Ctrl+C) and SIGTERM (Docker/systemd stop)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigChan) // Stop signal delivery when done to prevent leaks

		sig := <-sigChan // Block until a signal is received

		GetLogger().Info("received shutdown signal",
			logger.String("signal", sig.String()),
			logger.String("operation", "shutdown_signal_received"))
		close(quitChan) // Close the quit channel to signal other goroutines to stop
	}()
}

// closeDataStore attempts to close the database connection and logs the result.
func closeDataStore(store datastore.Interface) {
	log := GetLogger()
	// If this is an SQLite store, perform WAL checkpoint before closing
	if sqliteStore, ok := store.(*datastore.SQLiteStore); ok {
		log.Info("performing SQLite WAL checkpoint",
			logger.String("operation", "wal_checkpoint_before_shutdown"))
		if err := sqliteStore.CheckpointWAL(); err != nil {
			// Enhanced error handling - check for specific error conditions
			errStr := err.Error()
			if strings.Contains(errStr, "database is closed") || strings.Contains(errStr, "nil pointer") {
				// Database is likely already closed or connection is nil
				log.Warn("database already closed during WAL checkpoint",
					logger.String("operation", "wal_checkpoint"),
					logger.String("error_type", "database_closed"))
			} else {
				// Other checkpoint failures - log but continue with shutdown
				log.Warn("WAL checkpoint failed",
					logger.Error(err),
					logger.String("operation", "wal_checkpoint"),
					logger.Bool("continuing_shutdown", true))
			}
		}
	}

	// Close the database connection
	if err := store.Close(); err != nil {
		log.Error("failed to close database",
			logger.Error(err),
			logger.String("operation", "close_database"))
	} else {
		log.Info("successfully closed database",
			logger.String("operation", "close_database"))
	}
}

// closeV2Database closes the v2 database with proper WAL checkpoint.
func closeV2Database() {
	v2DatabaseManagerMu.Lock()
	manager := v2DatabaseManager
	v2DatabaseManager = nil
	v2DatabaseManagerMu.Unlock()

	if manager == nil {
		return
	}

	log := GetLogger()

	// Determine database type for logging
	dbType := "SQLite"
	if manager.IsMySQL() {
		dbType = "MySQL"
	}

	// Perform WAL checkpoint before closing (SQLite only, no-op for MySQL)
	if !manager.IsMySQL() {
		log.Info("performing v2 SQLite WAL checkpoint",
			logger.String("operation", "v2_wal_checkpoint_before_shutdown"))

		if err := manager.CheckpointWAL(); err != nil {
			log.Warn("v2 WAL checkpoint failed",
				logger.Error(err),
				logger.String("operation", "v2_wal_checkpoint"))
		}
	}

	// Close the database
	log.Info("closing v2 database",
		logger.String("type", dbType),
		logger.String("path", manager.Path()),
		logger.String("operation", "v2_database_close"))

	if err := manager.Close(); err != nil {
		log.Error("failed to close v2 database",
			logger.Error(err),
			logger.String("type", dbType),
			logger.String("operation", "v2_database_close"))
	} else {
		log.Info("v2 database closed successfully",
			logger.String("type", dbType),
			logger.String("path", manager.Path()))
	}
}

// ClipCleanupMonitor monitors the database and deletes clips that meet the retention policy.
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
// and registers the default providers (Wikimedia, AviCommons).
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

	// Attempt to register AviCommons
	if _, ok := registry.GetCache("avicommons"); !ok {
		log.Info("attempting to register AviCommons provider",
			logger.String("provider", "avicommons"),
			logger.String("operation", "register_image_provider"))

		// Debug logging for embedded filesystem if enabled
		if conf.Setting().Realtime.Dashboard.Thumbnails.Debug {
			log.Debug("listing embedded filesystem contents",
				logger.String("operation", "debug_filesystem"))
			if err := fs.WalkDir(api.ImageDataFs, ".", func(path string, d fs.DirEntry, err error) error {
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
			}); err != nil {
				log.Error("error walking embedded filesystem",
					logger.Error(err),
					logger.String("operation", "debug_filesystem"))
			}
		}

		if err := imageprovider.RegisterAviCommonsProvider(registry, api.ImageDataFs, metrics, ds); err != nil {
			log.Error("failed to register AviCommons provider",
				logger.Error(err),
				logger.String("provider", "avicommons"),
				logger.String("operation", "register_image_provider"))
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategoryImageProvider).
				Context("operation", "register_avicommons_provider").
				Context("provider", "avicommons").
				Build())
			// Check if we can read the data file for debugging
			if _, errRead := fs.ReadFile(api.ImageDataFs, "internal/imageprovider/data/latest.json"); errRead != nil {
				log.Error("error reading AviCommons data file",
					logger.Error(errRead),
					logger.String("provider", "avicommons"),
					logger.String("file_path", "internal/imageprovider/data/latest.json"),
					logger.String("operation", "read_data_file"))
			} else {
				log.Warn("AviCommons data file exists but registration failed",
					logger.String("provider", "avicommons"),
					logger.String("operation", "register_image_provider"))
			}
		} else {
			log.Info("successfully registered image provider",
				logger.String("provider", "avicommons"),
				logger.String("operation", "register_image_provider"))
		}
	} else {
		log.Info("using existing image provider",
			logger.String("provider", "avicommons"),
			logger.String("operation", "setup_image_provider"))
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
		// Use wikimedia as the default provider in auto mode, if available
		defaultCache, _ = registry.GetCache("wikimedia")
		log.Info("selected default image provider",
			logger.String("provider", "wikimedia"),
			logger.String("mode", "auto"),
			logger.String("operation", "select_default_provider"))
	} else {
		// User has specified a specific provider
		if cache, ok := registry.GetCache(preferredProvider); ok {
			defaultCache = cache
			log.Info("selected preferred image provider",
				logger.String("provider", preferredProvider),
				logger.String("operation", "select_default_provider"))
		} else {
			// Fallback to wikimedia if preferred provider doesn't exist or isn't registered
			defaultCache, _ = registry.GetCache("wikimedia")
			log.Warn("preferred provider not available, falling back",
				logger.String("preferred_provider", preferredProvider),
				logger.String("fallback_provider", "wikimedia"),
				logger.String("operation", "select_default_provider"))
		}
	}

	// If we still don't have a default cache (e.g., wikimedia failed registration), try any available provider.
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

// startControlMonitor handles various control signals for realtime analysis mode
func startControlMonitor(wg *sync.WaitGroup, controlChan chan string, quitChan, restartChan chan struct{}, bufferManager *BufferManager, proc *processor.Processor, apiController *apiv2.Controller, metrics *observability.Metrics) *ControlMonitor {
	ctrlMonitor := NewControlMonitor(wg, controlChan, quitChan, restartChan, bufferManager, proc, audioLevelChan, soundLevelChan, apiController, metrics)
	ctrlMonitor.Start()
	return ctrlMonitor
}

// initializeBuffers handles initialization of all audio-related buffers
func initializeBuffers(sources []string) error {
	var initErrors []string

	// Initialize analysis buffers
	const analysisBufferSize = conf.BufferSize * 6 // 6x buffer size to avoid underruns
	if err := myaudio.InitAnalysisBuffers(analysisBufferSize, sources); err != nil {
		initErrors = append(initErrors, fmt.Sprintf("failed to initialize analysis buffers: %v", err))
	}

	// Initialize capture buffers
	const captureBufferSize = 120 // Capture buffer size of 120 seconds
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

// initializeMetrics initializes the Prometheus metrics manager
func initializeMetrics() (*observability.Metrics, error) {
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
				return nil, fmt.Errorf("audio source registry not available")
			}

			var failedSources []string
			for _, stream := range settings.Realtime.RTSP.Streams {
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
				Type:        myaudio.SourceTypeAudioCard,
				DisplayName: settings.Realtime.Audio.Source,
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

// printSystemDetails prints system information and analyzer configuration
func printSystemDetails(settings *conf.Settings) {
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
		return fmt.Errorf("failed to get species label type: %w", err)
	}

	var avesClass entities.TaxonomicClass
	if err := v2DB.Where("name = ?", "Aves").FirstOrCreate(&avesClass, entities.TaxonomicClass{Name: "Aves"}).Error; err != nil {
		return fmt.Errorf("failed to get Aves taxonomic class: %w", err)
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
		return fmt.Errorf("failed to get default model: %w", err)
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
	})
	if err != nil {
		return fmt.Errorf("failed to create migration worker: %w", err)
	}

	// Store manager for shutdown cleanup - only after successful worker creation
	// to prevent GetV2DatabaseManager() from returning a partially initialized manager
	v2DatabaseManagerMu.Lock()
	v2DatabaseManager = cfg.manager
	v2DatabaseManagerMu.Unlock()

	// Inject dependencies into the API layer
	apiv2.SetMigrationDependencies(stateManager, worker)

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

		// Resume worker if migration was in progress
		if state.State == entities.MigrationStatusDualWrite ||
			state.State == entities.MigrationStatusMigrating {
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
func initializeMigrationInfrastructure(settings *conf.Settings, ds datastore.Interface) error {
	log := GetLogger()

	// Get the database directory from the legacy database path
	var dataDir string
	switch {
	case settings.Output.SQLite.Enabled:
		dataDir = datastoreV2.GetDataDirFromLegacyPath(settings.Output.SQLite.Path)
	case settings.Output.MySQL.Enabled:
		// MySQL uses v2_ prefixed tables in the same database
		return initializeMySQLMigrationInfrastructure(settings, ds, log)
	default:
		log.Debug("no database configured, skipping migration infrastructure",
			logger.String("operation", "initialize_migration_infrastructure"))
		return nil
	}

	// Check if dataDir is empty (in-memory database)
	if dataDir == "" {
		log.Debug("in-memory database detected, skipping migration infrastructure",
			logger.String("operation", "initialize_migration_infrastructure"))
		return nil
	}

	// Create v2 database manager
	// Use ConfiguredPath to properly derive v2 migration path from configured filename
	v2Manager, err := datastoreV2.NewSQLiteManager(datastoreV2.Config{
		ConfiguredPath: settings.Output.SQLite.Path,
		Debug:          settings.Debug,
		Logger:         log,
	})
	if err != nil {
		return fmt.Errorf("failed to create v2 database manager: %w", err)
	}

	// Initialize the v2 database schema
	if err := v2Manager.Initialize(); err != nil {
		if closeErr := v2Manager.Close(); closeErr != nil {
			log.Warn("failed to close v2 manager after initialization failure",
				logger.Error(closeErr),
				logger.String("operation", "initialize_migration_infrastructure"))
		}
		return fmt.Errorf("failed to initialize v2 database: %w", err)
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
		return err
	}

	return nil
}

// initializeMySQLMigrationInfrastructure sets up migration infrastructure for MySQL.
// Unlike SQLite which uses a separate file, MySQL shares the same database.
// V2 tables have different names from legacy (detections vs notes), so no prefix needed.
func initializeMySQLMigrationInfrastructure(settings *conf.Settings, ds datastore.Interface, log logger.Logger) error {
	// Create v2 MySQL manager - no prefix needed since v2 table names differ from legacy
	// (Entity TableName() methods override GORM NamingStrategy anyway)
	v2Manager, err := datastoreV2.NewMySQLManager(&datastoreV2.MySQLConfig{
		Host:        settings.Output.MySQL.Host,
		Port:        settings.Output.MySQL.Port,
		Username:    settings.Output.MySQL.Username,
		Password:    settings.Output.MySQL.Password,
		Database:    settings.Output.MySQL.Database,
		UseV2Prefix: false, // No prefix - v2 tables have different names from legacy
		Debug:       settings.Debug,
	})
	if err != nil {
		return fmt.Errorf("failed to create MySQL v2 manager: %w", err)
	}

	// Initialize the v2 schema (creates tables without prefix)
	if err := v2Manager.Initialize(); err != nil {
		if closeErr := v2Manager.Close(); closeErr != nil {
			log.Warn("failed to close MySQL v2 manager after initialization failure",
				logger.Error(closeErr),
				logger.String("operation", "initialize_mysql_migration"))
		}
		return fmt.Errorf("failed to initialize MySQL v2 schema: %w", err)
	}

	// Setup the migration worker using the common helper
	// Note: useV2Prefix is false because entity TableName() methods override
	// GORM's NamingStrategy, creating tables without prefix. Since v2 tables
	// have different names from legacy (detections vs notes), prefix isn't needed.
	if err := setupMigrationWorker(&migrationSetupConfig{
		manager:     v2Manager,
		ds:          ds,
		log:         log,
		useV2Prefix: false, // Tables created without prefix due to TableName() methods
		opName:      "initialize_mysql_migration",
	}); err != nil {
		if closeErr := v2Manager.Close(); closeErr != nil {
			log.Warn("failed to close MySQL v2 manager after worker setup failure",
				logger.Error(closeErr),
				logger.String("operation", "initialize_mysql_migration"))
		}
		return err
	}

	return nil
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
		return nil, fmt.Errorf("no database configured")
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create v2 database manager: %w", err)
	}

	// Initialize the v2 database schema (ensures auxiliary tables exist)
	if err := v2Manager.Initialize(); err != nil {
		_ = v2Manager.Close()
		return nil, fmt.Errorf("failed to initialize v2 database: %w", err)
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

	// Create V2OnlyDatastore
	ds, err := v2only.New(&v2only.Config{
		Manager:      v2Manager,
		Detection:    detectionRepo,
		Label:        labelRepo,
		Model:        modelRepo,
		Source:       sourceRepo,
		Weather:      weatherRepo,
		ImageCache:   imageCacheRepo,
		Threshold:    thresholdRepo,
		Notification: notificationRepo,
		Logger:       log,
		Timezone:     time.Local,
		Labels:       settings.BirdNET.Labels, // For GetThresholdEvents workaround (#1907)
	})
	if err != nil {
		_ = v2Manager.Close()
		return nil, fmt.Errorf("failed to create v2-only datastore: %w", err)
	}

	// Store manager for shutdown cleanup
	v2DatabaseManagerMu.Lock()
	v2DatabaseManager = v2Manager
	v2DatabaseManagerMu.Unlock()

	log.Info("enhanced database mode initialized successfully",
		logger.String("operation", "initialize_enhanced_database_mode"))

	return ds, nil
}
