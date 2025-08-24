package analysis

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/audiocore/adapter"
	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/monitor"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/privacy"
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

// RealtimeAnalysis initiates the BirdNET Analyzer in real-time mode and waits for a termination signal.
//
//nolint:gocognit,gocyclo // This is the main orchestration function that coordinates multiple subsystems during startup and shutdown
func RealtimeAnalysis(settings *conf.Settings, notificationChan chan handlers.Notification) error {
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

	// Initialize database access.
	dataStore := datastore.New(settings)

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
		// Add structured logging
		GetLogger().Warn("Audio source initialization warning",
			"error", err,
			"operation", "initialize_audio_sources")
		log.Printf("⚠️  Audio source initialization warning: %v", err)
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

	// Open a connection to the database and handle possible errors.
	if err := dataStore.Open(); err != nil {
		return err // Return error to stop execution if database connection fails.
	}
	// Ensure the database connection is closed when the function returns.
	defer closeDataStore(dataStore)

	// Note: datastore monitoring is automatically started when the database is opened

	// Initialize bird image cache if needed
	birdImageCache := initializeBirdImageCacheIfNeeded(settings, dataStore, metrics)

	// Initialize processor
	proc := processor.New(settings, dataStore, bn, metrics, birdImageCache)

	// Initialize Backup system
	backupLogger := logging.ForService("backup") // Get logger first
	if backupLogger == nil {
		// Add structured logging
		GetLogger().Error("Backup logger is nil, logging may not be initialized",
			"operation", "backup_logger_init")
		log.Println("Error: Backup logger is nil. Logging may not be initialized.")
		backupLogger = slog.Default() // Use default as fallback
	}
	backupManager, backupScheduler, err := initializeBackupSystem(settings, backupLogger)
	if err != nil {
		// Log the specific error from initialization
		backupLogger.Error("Failed to initialize backup system", "error", err)
		// Don't make this fatal - continue without backup system
		// Add structured logging
		GetLogger().Warn("Backup system initialization failed",
			"error", err,
			"operation", "backup_system_init")
		log.Printf("Warning: Backup system initialization failed: %v", err)
	} else {
		// Store backup manager and scheduler in the processor for access by control monitor
		proc.SetBackupManager(backupManager)
		proc.SetBackupScheduler(backupScheduler)
	}

	// Initialize async services (event bus, notification workers, telemetry workers)
	if err := telemetry.InitializeAsyncSystems(); err != nil {
		// Add structured logging
		GetLogger().Error("Failed to initialize critical async services (event bus, notifications, telemetry)",
			"error", err,
			"operation", "initialize_async_systems")
		log.Printf("Error: Failed to initialize critical async services: %v", err)
		return errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategorySystem).
			Context("operation", "initialize_async_systems").
			Build()
	}

	// Initialize system monitor if monitoring is enabled
	systemMonitor := initializeSystemMonitor(settings)

	// Initialize and start the HTTP server
	httpServer := httpcontroller.New(settings, dataStore, birdImageCache, audioLevelChan, controlChan, proc, metrics)
	httpServer.Start()

	// Initialize the wait group to wait for all goroutines to finish
	var wg sync.WaitGroup

	// Initialize the buffer manager
	bufferManager := MustNewBufferManager(bn, quitChan, &wg)

	// Start buffer monitors for each audio source only if we have active sources
	if len(settings.Realtime.RTSP.URLs) > 0 || settings.Realtime.Audio.Source != "" {
		if err := bufferManager.UpdateMonitors(sources); err != nil {
			// Use structured logging to improve error visibility and triage
			logger := GetLogger()

			// Extract error details from the enhanced error if available
			errorStr := err.Error()
			logger.Warn("Buffer monitor setup completed with errors",
				"error", errorStr,
				"source_count", len(sources),
				"sources", sources,
				"component", "analysis.realtime",
				"operation", "buffer_monitor_setup")

			// Also log to console for immediate visibility during startup
			log.Printf("⚠️  Warning: Buffer monitor setup completed with errors: %v", err)
			// Note: We continue execution as buffer monitoring errors are not critical for startup
		}
	} else {
		// Add structured logging
		GetLogger().Warn("Starting without active audio sources",
			"rtsp_urls", len(settings.Realtime.RTSP.URLs),
			"audio_source", settings.Realtime.Audio.Source,
			"operation", "startup_audio_check")
		log.Println("⚠️  Starting without active audio sources. You can configure audio devices or RTSP streams through the web interface.")
	}

	// start audio capture
	startAudioCapture(&wg, settings, quitChan, restartChan, audioLevelChan, soundLevelChan)

	// Sound level monitoring is now managed by the control monitor for hot reload support.
	// The control monitor will start sound level monitoring if enabled in settings.

	// RTSP health monitoring is now built into the FFmpeg manager
	if len(settings.Realtime.RTSP.URLs) > 0 {
		// Add structured logging
		GetLogger().Info("RTSP streams will be monitored by FFmpeg manager",
			"stream_count", len(settings.Realtime.RTSP.URLs),
			"operation", "rtsp_monitoring_setup")
		log.Println("🔍 RTSP streams will be monitored by FFmpeg manager")
	}

	// start cleanup of clips
	if conf.Setting().Realtime.Audio.Export.Retention.Policy != "none" {
		startClipCleanupMonitor(&wg, quitChan, dataStore)
	}

	// start weather polling
	if settings.Realtime.Weather.Provider != "none" {
		startWeatherPolling(&wg, settings, dataStore, metrics, quitChan)
	}

	// Telemetry endpoint initialization is now handled by control monitor for hot reload support.
	// Unlike other services that start directly here, telemetry is managed by the control monitor
	// to allow users to dynamically enable/disable metrics and change the listen address without
	// restarting the application. The control monitor will start the endpoint if enabled.
	// startTelemetryEndpoint(&wg, settings, metrics, quitChan) // Moved to control monitor

	// start control monitor for hot reloads
	ctrlMonitor := startControlMonitor(&wg, controlChan, quitChan, restartChan, notificationChan, bufferManager, proc, httpServer, metrics)

	// start shutdown signal monitor
	monitorShutdownSignals(quitChan)

	// Track the HTTP server, system monitor and control monitor for clean shutdown
	httpServerRef := httpServer
	systemMonitorRef := systemMonitor
	ctrlMonitorRef := ctrlMonitor

	// loop to monitor quit and restart channels
	for {
		select {
		case <-quitChan:
			// Add structured logging
			GetLogger().Info("Initiating graceful shutdown sequence",
				"shutdown_timeout_seconds", shutdownTimeout.Seconds(),
				"operation", "graceful_shutdown")
			log.Println("🛑 Initiating graceful shutdown sequence...")
			shutdownStart := time.Now()

			// Create context with timeout for the entire shutdown process
			ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)

			// Execute shutdown with context
			shutdownComplete := make(chan struct{})
			go func() {
				defer close(shutdownComplete)

				// Step 1: Signal shutdown started (but don't close controlChan yet)
				// Add structured logging
				GetLogger().Info("Shutdown step 1: Beginning shutdown sequence",
					"step", 1,
					"operation", "shutdown_begin")
				log.Println("  1️⃣ Beginning shutdown sequence...")

				// Check context cancellation between steps
				if ctx.Err() != nil {
					// Add structured logging
					GetLogger().Warn("Shutdown context cancelled after step 1",
						"step", 1,
						"error", ctx.Err(),
						"operation", "shutdown_timeout")
					log.Printf("  ⚠️ Shutdown context cancelled after step 1")
					return
				}

				// Step 2: Stop control monitor
				if ctrlMonitorRef != nil {
					// Add structured logging
					GetLogger().Info("Shutdown step 2: Stopping control monitor",
						"step", 2,
						"operation", "shutdown_control_monitor")
					log.Println("  2️⃣ Stopping control monitor...")
					ctrlMonitorRef.Stop()
				}

				if ctx.Err() != nil {
					// Add structured logging
					GetLogger().Warn("Shutdown context cancelled after step 2",
						"step", 2,
						"error", ctx.Err(),
						"operation", "shutdown_timeout")
					log.Printf("  ⚠️ Shutdown context cancelled after step 2")
					return
				}

				// Step 3: Stop analysis buffer monitors
				// Add structured logging
				GetLogger().Info("Shutdown step 3: Stopping analysis buffer monitors",
					"step", 3,
					"operation", "shutdown_buffer_monitors")
				log.Println("  3️⃣ Stopping analysis buffer monitors...")
				bufferManager.RemoveAllMonitors()

				if ctx.Err() != nil {
					// Add structured logging
					GetLogger().Warn("Shutdown context cancelled after step 3",
						"step", 3,
						"error", ctx.Err(),
						"operation", "shutdown_timeout")
					log.Printf("  ⚠️ Shutdown context cancelled after step 3")
					return
				}

				// Step 4: Clean up HLS resources asynchronously with timeout
				// Add structured logging
				GetLogger().Info("Shutdown step 4: Cleaning up HLS resources",
					"step", 4,
					"operation", "shutdown_hls_cleanup")
				log.Println("  4️⃣ Cleaning up HLS resources...")
				cleanupHLSWithTimeout(ctx)

				if ctx.Err() != nil {
					// Add structured logging
					GetLogger().Warn("Shutdown context cancelled after step 4",
						"step", 4,
						"error", ctx.Err(),
						"operation", "shutdown_timeout")
					log.Printf("  ⚠️ Shutdown context cancelled after step 4")
					return
				}

				// Step 5: Shutdown HTTP server
				if httpServerRef != nil {
					// Add structured logging
					GetLogger().Info("Shutdown step 5: Shutting down HTTP server",
						"step", 5,
						"operation", "shutdown_http_server")
					log.Println("  5️⃣ Shutting down HTTP server...")
					if err := httpServerRef.Shutdown(); err != nil {
						// Add structured logging
						GetLogger().Warn("Error shutting down HTTP server",
							"error", err,
							"step", 5,
							"operation", "shutdown_http_server")
						log.Printf("  ⚠️ Warning: Error shutting down HTTP server: %v", err)
					}
				}

				// Now it's safe to close controlChan after HTTP server is down
				// Add structured logging
				GetLogger().Info("Closing control channel after producers shutdown",
					"operation", "close_control_channel")
				close(controlChan)

				if ctx.Err() != nil {
					// Add structured logging
					GetLogger().Warn("Shutdown context cancelled after step 5",
						"step", 5,
						"error", ctx.Err(),
						"operation", "shutdown_timeout")
					log.Printf("  ⚠️ Shutdown context cancelled after step 5")
					return
				}

				// Step 6: Wait for all goroutines
				// Add structured logging
				GetLogger().Info("Shutdown step 6: Waiting for goroutines to finish",
					"step", 6,
					"operation", "shutdown_wait_goroutines")
				log.Println("  6️⃣ Waiting for goroutines to finish...")
				wg.Wait()

				if ctx.Err() != nil {
					// Add structured logging
					GetLogger().Warn("Shutdown context cancelled after step 6",
						"step", 6,
						"error", ctx.Err(),
						"operation", "shutdown_timeout")
					log.Printf("  ⚠️ Shutdown context cancelled after step 6")
					return
				}

				// Step 7: Stop system monitor
				if systemMonitorRef != nil {
					// Add structured logging
					GetLogger().Info("Shutdown step 7: Stopping system monitor",
						"step", 7,
						"operation", "shutdown_system_monitor")
					log.Println("  7️⃣ Stopping system monitor...")
					systemMonitorRef.Stop()
				}

				if ctx.Err() != nil {
					// Add structured logging
					GetLogger().Warn("Shutdown context cancelled after step 7",
						"step", 7,
						"error", ctx.Err(),
						"operation", "shutdown_timeout")
					log.Printf("  ⚠️ Shutdown context cancelled after step 7")
					return
				}

				// Step 8: Stop notification service
				if notification.IsInitialized() {
					// Add structured logging
					GetLogger().Info("Shutdown step 8: Stopping notification service",
						"step", 8,
						"operation", "shutdown_notification_service")
					log.Println("  8️⃣ Stopping notification service...")
					if service := notification.GetService(); service != nil {
						service.Stop()
					}
				}

				if ctx.Err() != nil {
					// Add structured logging
					GetLogger().Warn("Shutdown context cancelled after step 8",
						"step", 8,
						"error", ctx.Err(),
						"operation", "shutdown_timeout")
					log.Printf("  ⚠️ Shutdown context cancelled after step 8")
					return
				}

				// Step 9: Delete BirdNET interpreter
				// Add structured logging
				GetLogger().Info("Shutdown step 9: Cleaning up BirdNET interpreter",
					"step", 9,
					"operation", "shutdown_birdnet_cleanup")
				log.Println("  9️⃣ Cleaning up BirdNET interpreter...")
				bn.Delete()

				// Add structured logging
				GetLogger().Info("Graceful shutdown completed",
					"duration_ms", time.Since(shutdownStart).Milliseconds(),
					"operation", "shutdown_complete")
				log.Printf("✅ Graceful shutdown completed in %v", time.Since(shutdownStart))
			}()

			// Wait for shutdown to complete or context timeout
			select {
			case <-shutdownComplete:
				// Shutdown completed successfully
				cancel()
				return nil
			case <-ctx.Done():
				// Add structured logging
				GetLogger().Warn("Shutdown timeout exceeded, forcing exit",
					"timeout_seconds", shutdownTimeout.Seconds(),
					"operation", "shutdown_forced_exit")
				log.Printf("⚠️ Shutdown timeout exceeded (%v), forcing exit", shutdownTimeout)
				cancel()
				return nil
			}

		case <-restartChan:
			// Handle the restart signal.
			// Add structured logging
			GetLogger().Info("Restarting audio capture",
				"operation", "restart_audio_capture")
			fmt.Println("🔄 Restarting audio capture")
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
	if settings.Realtime.Audio.UseAudioCore {
		// Use new audiocore implementation
		go func() {
			// Add structured logging
			GetLogger().Info("Using new audiocore audio capture system",
				"audio_system", "audiocore",
				"operation", "start_audio_capture")
			log.Println("🎵 Using new audiocore audio capture system")
			adapter.StartAudioCoreCapture(settings, wg, quitChan, restartChan, unifiedAudioChan)
		}()
	} else {
		// Use existing myaudio implementation
		go myaudio.CaptureAudio(settings, wg, quitChan, restartChan, unifiedAudioChan)
	}
}

// startClipCleanupMonitor initializes and starts the clip cleanup monitoring routine in a new goroutine.
func startClipCleanupMonitor(wg *sync.WaitGroup, quitChan chan struct{}, dataStore datastore.Interface) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		clipCleanupMonitor(quitChan, dataStore)
	}()
}

// startWeatherPolling initializes and starts the weather polling routine in a new goroutine.
func startWeatherPolling(wg *sync.WaitGroup, settings *conf.Settings, dataStore datastore.Interface, metrics *observability.Metrics, quitChan chan struct{}) {
	// Create new weather service
	weatherService, err := weather.NewService(settings, dataStore, metrics.Weather)
	if err != nil {
		// Add structured logging
		GetLogger().Error("Failed to initialize weather service",
			"error", err,
			"operation", "initialize_weather_service")
		log.Printf("⛈️ Failed to initialize weather service: %v", err)
		return
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		weatherService.StartPolling(quitChan)
	}()
}

func startTelemetryEndpoint(wg *sync.WaitGroup, settings *conf.Settings, metrics *observability.Metrics, quitChan chan struct{}) {
	// Initialize Prometheus metrics endpoint if enabled
	if settings.Realtime.Telemetry.Enabled {
		// Initialize metrics endpoint
		telemetryEndpoint, err := observability.NewEndpoint(settings, metrics)
		if err != nil {
			// Add structured logging
			GetLogger().Error("Failed to initialize telemetry endpoint",
				"error", err,
				"operation", "initialize_telemetry_endpoint")
			log.Printf("Error initializing telemetry endpoint: %v", err)
			return
		}

		// Start metrics server
		telemetryEndpoint.Start(wg, quitChan)
	}
}

// monitorShutdownSignals listens for shutdown signals (SIGINT, SIGTERM) and triggers the application shutdown process.
func monitorShutdownSignals(quitChan chan struct{}) {
	go func() {
		sigChan := make(chan os.Signal, 1)
		// Register to receive both SIGINT (Ctrl+C) and SIGTERM (Docker/systemd stop)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigChan) // Stop signal delivery when done to prevent leaks

		sig := <-sigChan // Block until a signal is received

		// Add structured logging
		GetLogger().Info("Received shutdown signal",
			"signal", sig.String(),
			"operation", "shutdown_signal_received")
		log.Printf("Received %s signal, initiating graceful shutdown", sig)
		close(quitChan) // Close the quit channel to signal other goroutines to stop
	}()
}

// closeDataStore attempts to close the database connection and logs the result.
func closeDataStore(store datastore.Interface) {
	// If this is an SQLite store, perform WAL checkpoint before closing
	if sqliteStore, ok := store.(*datastore.SQLiteStore); ok {
		// Add structured logging
		GetLogger().Info("Performing SQLite WAL checkpoint",
			"operation", "wal_checkpoint_before_shutdown")
		log.Println("📝 Performing SQLite WAL checkpoint before shutdown...")
		if err := sqliteStore.CheckpointWAL(); err != nil {
			// Enhanced error handling - check for specific error conditions
			errStr := err.Error()
			if strings.Contains(errStr, "database is closed") || strings.Contains(errStr, "nil pointer") {
				// Database is likely already closed or connection is nil
				// Add structured logging
				GetLogger().Warn("Database already closed during WAL checkpoint",
					"operation", "wal_checkpoint",
					"error_type", "database_closed")
				log.Printf("⚠️ Warning: Database already closed or invalid state during WAL checkpoint")
			} else {
				// Other checkpoint failures - log but continue with shutdown
				// Add structured logging
				GetLogger().Warn("WAL checkpoint failed",
					"error", err,
					"operation", "wal_checkpoint",
					"continuing_shutdown", true)
				log.Printf("⚠️ Warning: WAL checkpoint failed (continuing shutdown): %v", err)
			}
		}
	}

	// Close the database connection
	if err := store.Close(); err != nil {
		// Add structured logging
		GetLogger().Error("Failed to close database",
			"error", err,
			"operation", "close_database")
		log.Printf("Failed to close database: %v", err)
	} else {
		// Add structured logging
		GetLogger().Info("Successfully closed database",
			"operation", "close_database")
		log.Println("Successfully closed database")
	}
}

// ClipCleanupMonitor monitors the database and deletes clips that meet the retention policy.
// It also performs periodic cleanup of log deduplicator states to prevent memory growth.
func clipCleanupMonitor(quitChan chan struct{}, dataStore datastore.Interface) {
	// Create a ticker that triggers every five minutes to perform cleanup
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop() // Ensure the ticker is stopped to prevent leaks

	// Get the shared disk manager logger
	diskManagerLogger := diskmanager.GetLogger()

	policy := conf.Setting().Realtime.Audio.Export.Retention.Policy
	// Add structured logging
	GetLogger().Info("Clip cleanup monitor initialized",
		"policy", policy,
		"operation", "clip_cleanup_init")
	log.Println("Clip retention policy:", policy)
	diskManagerLogger.Info("Cleanup timer started",
		"policy", policy,
		"interval_minutes", 5,
		"timestamp", time.Now().Format(time.RFC3339))

	for {
		select {
		case <-quitChan:
			// Handle quit signal to stop the monitor
			diskManagerLogger.Info("Cleanup timer stopped",
				"reason", "quit signal received",
				"timestamp", time.Now().Format(time.RFC3339))
			// Ensure clean shutdown
			if err := diskmanager.CloseLogger(); err != nil {
				diskManagerLogger.Error("Failed to close diskmanager logger", "error", err)
			}
			return

		case t := <-ticker.C:
			// Add structured logging
			GetLogger().Info("Starting clip cleanup task",
				"timestamp", t.Format(time.RFC3339),
				"policy", conf.Setting().Realtime.Audio.Export.Retention.Policy,
				"operation", "clip_cleanup_task")
			log.Println("🧹 Running clip cleanup task")
			diskManagerLogger.Info("Cleanup timer triggered",
				"timestamp", t.Format(time.RFC3339),
				"policy", conf.Setting().Realtime.Audio.Export.Retention.Policy)

			// age based cleanup method
			if conf.Setting().Realtime.Audio.Export.Retention.Policy == "age" {
				diskManagerLogger.Debug("Starting age-based cleanup via timer")
				result := diskmanager.AgeBasedCleanup(quitChan, dataStore)
				if result.Err != nil {
					// Add structured logging
					GetLogger().Error("Age-based cleanup failed",
						"error", result.Err,
						"operation", "age_based_cleanup")
					log.Printf("Error during age-based cleanup: %v", result.Err)
					diskManagerLogger.Error("Age-based cleanup failed",
						"error", result.Err,
						"timestamp", time.Now().Format(time.RFC3339))
				} else {
					// Add structured logging
					GetLogger().Info("Age-based cleanup completed successfully",
						"clips_removed", result.ClipsRemoved,
						"disk_utilization_percent", result.DiskUtilization,
						"operation", "age_based_cleanup")
					log.Printf("🧹 Age-based cleanup completed successfully, clips removed: %d, current disk utilization: %d%%", result.ClipsRemoved, result.DiskUtilization)
					diskManagerLogger.Info("Age-based cleanup completed via timer",
						"clips_removed", result.ClipsRemoved,
						"disk_utilization", result.DiskUtilization,
						"timestamp", time.Now().Format(time.RFC3339))
				}
			}

			// priority based cleanup method
			if conf.Setting().Realtime.Audio.Export.Retention.Policy == "usage" {
				diskManagerLogger.Debug("Starting usage-based cleanup via timer")
				result := diskmanager.UsageBasedCleanup(quitChan, dataStore)
				if result.Err != nil {
					// Add structured logging
					GetLogger().Error("Usage-based cleanup failed",
						"error", result.Err,
						"operation", "usage_based_cleanup")
					log.Printf("Error during usage-based cleanup: %v", result.Err)
					diskManagerLogger.Error("Usage-based cleanup failed",
						"error", result.Err,
						"timestamp", time.Now().Format(time.RFC3339))
				} else {
					// Add structured logging
					GetLogger().Info("Usage-based cleanup completed successfully",
						"clips_removed", result.ClipsRemoved,
						"disk_utilization_percent", result.DiskUtilization,
						"operation", "usage_based_cleanup")
					log.Printf("🧹 Usage-based cleanup completed successfully, clips removed: %d, current disk utilization: %d%%", result.ClipsRemoved, result.DiskUtilization)
					diskManagerLogger.Info("Usage-based cleanup completed via timer",
						"clips_removed", result.ClipsRemoved,
						"disk_utilization", result.DiskUtilization,
						"timestamp", time.Now().Format(time.RFC3339))
				}
			}
		}
	}
}

// NOTE: Potential Race Condition: If multiple goroutines call this function concurrently,
// especially during initial startup, there's a risk of race conditions during provider
// registration (checking Get then Register is not atomic). Consider using sync.Once
// or ensuring this is called only once during a deterministic startup phase (e.g., in main).
// setupImageProviderRegistry initializes or retrieves the global image provider registry
// and registers the default providers (Wikimedia, AviCommons).
func setupImageProviderRegistry(ds datastore.Interface, metrics *observability.Metrics) (*imageprovider.ImageProviderRegistry, error) {
	// Use the global registry if available, otherwise create a new one
	var registry *imageprovider.ImageProviderRegistry
	if httpcontroller.ImageProviderRegistry != nil {
		registry = httpcontroller.ImageProviderRegistry
		// Add structured logging
		GetLogger().Info("Using existing image provider registry",
			"operation", "setup_image_registry")
		log.Println("Using global image provider registry")
	} else {
		registry = imageprovider.NewImageProviderRegistry()
		httpcontroller.ImageProviderRegistry = registry // Assign back to global
		// Add structured logging
		GetLogger().Info("Created new image provider registry",
			"operation", "setup_image_registry")
		log.Println("Created new image provider registry")
	}

	var errs []error // Slice to collect errors

	// Attempt to register Wikimedia
	if _, ok := registry.GetCache("wikimedia"); !ok {
		wikiCache, err := imageprovider.CreateDefaultCache(metrics, ds)
		if err != nil {
			// Add structured logging
			GetLogger().Error("Failed to create WikiMedia image cache",
				"error", err,
				"provider", "wikimedia",
				"operation", "create_image_cache")
			log.Printf("Failed to create WikiMedia image cache: %v", err)
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategoryImageProvider).
				Context("operation", "create_wikimedia_cache").
				Context("provider", "wikimedia").
				Build())
			// Continue even if one provider fails
		} else {
			if err := registry.Register("wikimedia", wikiCache); err != nil {
				// Add structured logging
				GetLogger().Error("Failed to register WikiMedia image provider",
					"error", err,
					"provider", "wikimedia",
					"operation", "register_image_provider")
				log.Printf("Failed to register WikiMedia image provider: %v", err)
				errs = append(errs, errors.New(err).
					Component("realtime-analysis").
					Category(errors.CategoryImageProvider).
					Context("operation", "register_wikimedia_provider").
					Context("provider", "wikimedia").
					Build())
			} else {
				// Add structured logging
				GetLogger().Info("Successfully registered image provider",
					"provider", "wikimedia",
					"operation", "register_image_provider")
				log.Println("Registered WikiMedia image provider")
			}
		}
	} else {
		// Add structured logging
		GetLogger().Info("Using existing image provider",
			"provider", "wikimedia",
			"operation", "setup_image_provider")
		log.Println("Using existing WikiMedia image provider")
	}

	// Attempt to register AviCommons
	if _, ok := registry.GetCache("avicommons"); !ok {
		// Add structured logging
		GetLogger().Info("Attempting to register AviCommons provider",
			"provider", "avicommons",
			"operation", "register_image_provider")
		log.Println("Attempting to register AviCommons provider...")

		// Debug logging for embedded filesystem if enabled
		if conf.Setting().Realtime.Dashboard.Thumbnails.Debug {
			// Add structured logging
			GetLogger().Debug("Listing embedded filesystem contents",
				"operation", "debug_filesystem")
			log.Println("Embedded filesystem contents:")
			if err := fs.WalkDir(httpcontroller.ImageDataFs, ".", func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					// Add structured logging
					GetLogger().Debug("Error walking filesystem path",
						"path", path,
						"error", err,
						"operation", "debug_filesystem")
					log.Printf("  Error walking path %s: %v", path, err)
					return nil
				}
				// Add structured logging
				GetLogger().Debug("Filesystem entry found",
					"path", path,
					"is_dir", d.IsDir(),
					"operation", "debug_filesystem")
				log.Printf("  %s (%v)", path, d.IsDir())
				return nil
			}); err != nil {
				// Add structured logging
				GetLogger().Error("Error walking embedded filesystem",
					"error", err,
					"operation", "debug_filesystem")
				log.Printf("Error walking embedded filesystem: %v", err)
			}
		}

		if err := imageprovider.RegisterAviCommonsProvider(registry, httpcontroller.ImageDataFs, metrics, ds); err != nil {
			// Add structured logging
			GetLogger().Error("Failed to register AviCommons provider",
				"error", err,
				"provider", "avicommons",
				"operation", "register_image_provider")
			log.Printf("Failed to register AviCommons provider: %v", err)
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategoryImageProvider).
				Context("operation", "register_avicommons_provider").
				Context("provider", "avicommons").
				Build())
			// Check if we can read the data file for debugging
			if _, errRead := fs.ReadFile(httpcontroller.ImageDataFs, "internal/imageprovider/data/latest.json"); errRead != nil {
				// Add structured logging
				GetLogger().Error("Error reading AviCommons data file",
					"error", errRead,
					"provider", "avicommons",
					"file_path", "internal/imageprovider/data/latest.json",
					"operation", "read_data_file")
				log.Printf("Error reading AviCommons data file: %v", errRead)
			} else {
				// Add structured logging
				GetLogger().Warn("AviCommons data file exists but registration failed",
					"provider", "avicommons",
					"operation", "register_image_provider")
				log.Println("AviCommons data file exists but provider registration failed.")
			}
		} else {
			// Add structured logging
			GetLogger().Info("Successfully registered image provider",
				"provider", "avicommons",
				"operation", "register_image_provider")
			log.Println("Successfully registered AviCommons image provider")
		}
	} else {
		// Add structured logging
		GetLogger().Info("Using existing image provider",
			"provider", "avicommons",
			"operation", "setup_image_provider")
		log.Println("Using existing AviCommons image provider")
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
	preferredProvider := conf.Setting().Realtime.Dashboard.Thumbnails.ImageProvider
	var defaultCache *imageprovider.BirdImageCache

	if preferredProvider == "auto" {
		// Use wikimedia as the default provider in auto mode, if available
		defaultCache, _ = registry.GetCache("wikimedia")
		// Add structured logging
		GetLogger().Info("Selected default image provider",
			"provider", "wikimedia",
			"mode", "auto",
			"operation", "select_default_provider")
		log.Println("Using WikiMedia as the default image provider (auto mode)")
	} else {
		// User has specified a specific provider
		if cache, ok := registry.GetCache(preferredProvider); ok {
			defaultCache = cache
			// Add structured logging
			GetLogger().Info("Selected preferred image provider",
				"provider", preferredProvider,
				"operation", "select_default_provider")
			log.Printf("Using %s as the preferred image provider", preferredProvider)
		} else {
			// Fallback to wikimedia if preferred provider doesn't exist or isn't registered
			defaultCache, _ = registry.GetCache("wikimedia")
			// Add structured logging
			GetLogger().Warn("Preferred provider not available, falling back",
				"preferred_provider", preferredProvider,
				"fallback_provider", "wikimedia",
				"operation", "select_default_provider")
			log.Printf("Preferred provider '%s' not available, falling back to WikiMedia (if available)", preferredProvider)
		}
	}

	// If we still don't have a default cache (e.g., wikimedia failed registration), try any available provider.
	if defaultCache == nil {
		// Add structured logging
		GetLogger().Warn("No default image provider found, searching for alternatives",
			"operation", "select_default_provider")
		log.Println("No default image provider assigned yet, checking for any registered provider")
		registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
			defaultCache = cache
			// Add structured logging
			GetLogger().Info("Selected fallback default image provider",
				"provider", name,
				"operation", "select_default_provider")
			log.Printf("Using %s as the fallback default image provider", name)
			return false // Stop at the first provider found
		})
	}

	return defaultCache
}

// warmUpImageCacheInBackground fetches existing cache data and starts background tasks
// to fetch images for species not yet cached by any provider.
func warmUpImageCacheInBackground(ds datastore.Interface, registry *imageprovider.ImageProviderRegistry, defaultCache *imageprovider.BirdImageCache, speciesList []datastore.Note) {
	// Add structured logging
	GetLogger().Info("Starting background image cache warm-up",
		"species_count", len(speciesList),
		"operation", "image_cache_warmup")
	log.Println("Starting background image cache warm-up...")

	// Pre-fetch all cached image records from the database per provider
	allCachedImages := make(map[string]map[string]bool) // providerName -> scientificName -> exists
	if ds != nil {
		registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
			providerCache, err := ds.GetAllImageCaches(name)
			if err != nil {
				// Add structured logging
				GetLogger().Warn("Failed to get cached images for provider",
					"provider", name,
					"error", err,
					"operation", "image_cache_warmup")
				log.Printf("Warning: Failed to get cached images for provider '%s': %v", name, err)
				return true // Continue to next provider
			}
			allCachedImages[name] = make(map[string]bool)
			for i := range providerCache {
				allCachedImages[name][providerCache[i].ScientificName] = true
			}
			// Add structured logging
			GetLogger().Info("Pre-fetched cached image records",
				"provider", name,
				"cached_count", len(providerCache),
				"operation", "image_cache_warmup")
			log.Printf("Pre-fetched %d cached image records for provider '%s'", len(providerCache), name)
			return true // Continue ranging
		})
	} else {
		// Add structured logging
		GetLogger().Warn("Datastore is nil, cannot pre-fetch cached images",
			"operation", "image_cache_warmup")
		log.Println("Warning: Datastore is nil, cannot pre-fetch cached images.")
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
				// Add structured logging
				GetLogger().Warn("Skipping empty scientific name during warm-up",
					"operation", "image_cache_warmup")
				log.Printf("Warning: Skipping empty scientific name during image cache warm-up")
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
			// defaultCache.Initializing.Store(sciName, struct{}{}) // REMOVED - Incorrect usage

			go func(name string) {
				defer wg.Done()
				// The tryInitialize function called by Get handles mutex cleanup.
				// defer defaultCache.Initializing.Delete(name) // REMOVED - Handled by tryInitialize
				sem <- struct{}{}
				defer func() { <-sem }()

				// Skip empty scientific names (double check)
				if name == "" {
					// Add structured logging
					GetLogger().Warn("Empty scientific name in fetch goroutine",
						"operation", "image_cache_warmup")
					log.Printf("Warning: Caught empty scientific name in fetch goroutine")
					return
				}

				if _, err := defaultCache.Get(name); err != nil {
					// Add structured logging
					GetLogger().Debug("Failed to fetch image during warm-up",
						"species", name,
						"error", err,
						"operation", "image_cache_warmup")
					log.Printf("Failed to fetch image for %s during warm-up: %v", name, err)
				}
			}(sciName) // Pass the captured name
		}

		if needsImage > 0 {
			// Add structured logging
			GetLogger().Info("Cache warm-up: species require image fetching",
				"species_needing_images", needsImage,
				"operation", "image_cache_warmup")
			log.Printf("Cache warm-up: %d species require image fetching.", needsImage)
			wg.Wait()
			// Add structured logging
			GetLogger().Info("BirdImageCache initialization complete",
				"species_fetched", needsImage,
				"operation", "image_cache_warmup")
			log.Printf("Finished initializing BirdImageCache (%d species fetched/attempted)", needsImage)
		} else {
			// Add structured logging
			GetLogger().Info("BirdImageCache initialized",
				"status", "all_images_cached",
				"operation", "image_cache_warmup")
			log.Println("BirdImageCache initialized (all species images already present in DB cache)")
		}
	}()
}

// initBirdImageCache initializes the bird image cache by setting up providers,
// selecting a default, and starting a background warm-up process.
func initBirdImageCache(ds datastore.Interface, metrics *observability.Metrics) *imageprovider.BirdImageCache {
	// 1. Set up the registry and register known providers
	registry, regErr := setupImageProviderRegistry(ds, metrics)
	if regErr != nil {
		// Log errors encountered during provider registration
		// Add structured logging
		GetLogger().Warn("Image provider registry initialization encountered errors",
			"error", regErr,
			"operation", "init_image_cache")
		log.Printf("Warning: Image provider registry initialization encountered errors: %v", regErr)
		// Note: We continue even if some providers fail, as others might succeed.
		// The selectDefaultImageProvider logic will handle finding an available provider.
	}

	// Defensive check: Ensure registry is not nil before proceeding.
	if registry == nil {
		// Add structured logging
		GetLogger().Error("Image provider registry could not be initialized",
			"operation", "init_image_cache")
		log.Println("Error: Image provider registry could not be initialized.")
		return nil
	}

	// 2. Select the default cache based on settings and availability
	defaultCache := selectDefaultImageProvider(registry)

	// If no provider could be initialized or selected, return nil
	if defaultCache == nil {
		// Add structured logging
		GetLogger().Error("No image providers available or could be initialized",
			"operation", "init_image_cache")
		log.Println("Error: No image providers available or could be initialized.")
		return nil
	}

	// 3. Get the list of all detected species
	speciesList, err := ds.GetAllDetectedSpecies()
	if err != nil {
		// Add structured logging
		GetLogger().Warn("Failed to get detected species list",
			"error", err,
			"operation", "init_image_cache")
		log.Printf("Failed to get detected species list: %v. Cache warm-up may be incomplete.", err)
		// Continue with an empty list if DB fails, warm-up won't happen
		speciesList = []datastore.Note{}
	}

	// Filter out any species with empty scientific names
	validSpeciesList := make([]datastore.Note, 0, len(speciesList))
	for i := range speciesList {
		if speciesList[i].ScientificName != "" {
			validSpeciesList = append(validSpeciesList, speciesList[i])
		} else {
			// Add structured logging
			GetLogger().Warn("Found species entry with empty scientific name",
				"operation", "init_image_cache")
			log.Printf("Warning: Found species entry with empty scientific name in database, skipping for image cache")
		}
	}

	if len(validSpeciesList) < len(speciesList) {
		// Add structured logging
		GetLogger().Info("Filtered species entries with empty scientific names",
			"filtered_count", len(speciesList)-len(validSpeciesList),
			"total_count", len(speciesList),
			"valid_count", len(validSpeciesList),
			"operation", "init_image_cache")
		log.Printf("Filtered %d species entries with empty scientific names from warm-up list", len(speciesList)-len(validSpeciesList))
	}

	// 4. Start the background cache warm-up process with validated species list
	warmUpImageCacheInBackground(ds, registry, defaultCache, validSpeciesList)

	return defaultCache
}

// startControlMonitor handles various control signals for realtime analysis mode
func startControlMonitor(wg *sync.WaitGroup, controlChan chan string, quitChan, restartChan chan struct{}, notificationChan chan handlers.Notification, bufferManager *BufferManager, proc *processor.Processor, httpServer *httpcontroller.Server, metrics *observability.Metrics) *ControlMonitor {
	ctrlMonitor := NewControlMonitor(wg, controlChan, quitChan, restartChan, notificationChan, bufferManager, proc, audioLevelChan, soundLevelChan, metrics)
	ctrlMonitor.httpServer = httpServer
	ctrlMonitor.Start()
	return ctrlMonitor
}

// initializeBuffers handles initialization of all audio-related buffers
func initializeBuffers(sources []string) error {
	var initErrors []string

	// Initialize analysis buffers
	if err := myaudio.InitAnalysisBuffers(conf.BufferSize*3, sources); err != nil { // 3x buffer size to avoid underruns
		initErrors = append(initErrors, fmt.Sprintf("failed to initialize analysis buffers: %v", err))
	}

	// Initialize capture buffers
	if err := myaudio.InitCaptureBuffers(60, conf.SampleRate, conf.BitDepth/8, sources); err != nil {
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

	select {
	case err := <-cleanupDone:
		if err != nil {
			log.Printf("  ⚠️ Warning: Failed to clean up HLS streaming files: %v", err)
		}
	case <-cleanupCtx.Done():
		log.Printf("  ⚠️ Warning: HLS cleanup timeout exceeded (2s), continuing shutdown")
	}
}

// cleanupHLSStreamingFiles removes any leftover HLS streaming files and directories
// from previous runs of the application to avoid accumulation of unused files.
func cleanupHLSStreamingFiles() error {
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
			// Add structured logging
			GetLogger().Info("Removing HLS stream directory",
				"path", path,
				"operation", "cleanup_hls_files")
			log.Printf("🧹 Removing HLS stream directory: %s", path)

			// Remove the directory and all its contents
			if err := os.RemoveAll(path); err != nil {
				log.Printf("⚠️ Warning: Failed to remove HLS stream directory %s: %v", path, err)
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
	if err != nil {
		// Add structured logging
		GetLogger().Warn("Failed to clean up HLS streaming files",
			"error", err,
			"operation", "cleanup_hls_files")
		log.Printf("⚠️ Warning: Failed to clean up HLS streaming files: %v", err)
	} else {
		// Add structured logging
		GetLogger().Info("Cleaned up leftover HLS streaming files",
			"operation", "cleanup_hls_files")
		log.Println("🧹 Cleaned up leftover HLS streaming files")
	}
}

// initializeBackupSystem sets up the backup manager and scheduler.
func initializeBackupSystem(settings *conf.Settings, backupLogger *slog.Logger) (*backup.Manager, *backup.Scheduler, error) {
	backupLogger.Info("Initializing backup system...")

	stateManager, err := backup.NewStateManager(backupLogger)
	if err != nil {
		return nil, nil, errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategorySystem).
			Context("operation", "initialize_backup_state_manager").
			Build()
	}

	// TODO: Get runtime version from context - using fallback for now
	// This should be passed from the caller during the configuration separation refactoring
	appVersion := "unknown"
	if currentSettings := conf.GetSettings(); currentSettings != nil {
		// Try to get version from global settings as a temporary fallback
		// This access pattern will be removed during the complete refactoring
		appVersion = "birdnet-go" // Default app name as fallback
	}
	backupManager, err := backup.NewManager(settings, backupLogger, stateManager, appVersion)
	if err != nil {
		return nil, nil, errors.New(err).
			Component("analysis.realtime").
			Category(errors.CategorySystem).
			Context("operation", "initialize_backup_manager").
			Build()
	}
	backupScheduler, err := backup.NewScheduler(backupManager, backupLogger, stateManager)
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
		backupLogger.Info("Loading backup schedule from configuration")
		if err := backupScheduler.LoadFromConfig(&settings.Backup); err != nil {
			// Log the error but don't necessarily stop initialization
			backupLogger.Error("Failed to load backup schedule from config", "error", err)
		}
	case settings.Backup.Enabled:
		// This case is reached if backup is enabled but no schedules are defined.
		backupLogger.Info("Backup enabled, but no schedules configured.")
	default:
		// This case is reached if backup is disabled.
		backupLogger.Info("Backup system is disabled.")
	}

	// Start backupManager and backupScheduler if backup is enabled
	if settings.Backup.Enabled {
		backupLogger.Info("Starting backup manager")
		if err := backupManager.Start(); err != nil {
			// Log the error but don't necessarily stop initialization
			backupLogger.Error("Failed to start backup manager", "error", err)
		}
		backupLogger.Info("Starting backup scheduler")
		backupScheduler.Start() // Start the scheduler
	}

	backupLogger.Info("Backup system initialized.")
	return backupManager, backupScheduler, nil
}

// initializeSystemMonitor initializes and starts the system resource monitor if enabled
func initializeSystemMonitor(settings *conf.Settings) *monitor.SystemMonitor {
	logging.Info("initializeSystemMonitor called",
		"monitoring_enabled", settings.Realtime.Monitoring.Enabled,
		"check_interval", settings.Realtime.Monitoring.CheckInterval,
	)

	if !settings.Realtime.Monitoring.Enabled {
		logging.Warn("System monitoring is disabled in settings")
		return nil
	}

	logging.Info("Creating system monitor instance")
	systemMonitor := monitor.NewSystemMonitor(settings)
	if systemMonitor == nil {
		logging.Error("Failed to create system monitor instance")
		return nil
	}

	logging.Info("Starting system monitor")
	systemMonitor.Start()

	logging.Info("System resource monitoring initialized",
		"component", "monitor",
		"interval", settings.Realtime.Monitoring.CheckInterval)
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
	log.Println("Initializing bird image cache for settings UI (thumbnails disabled)")
	return initBirdImageCache(dataStore, metrics)
}

// initializeAudioSources prepares and validates audio sources
func initializeAudioSources(settings *conf.Settings) ([]string, error) {
	var sources []string
	if len(settings.Realtime.RTSP.URLs) > 0 || settings.Realtime.Audio.Source != "" {
		if len(settings.Realtime.RTSP.URLs) > 0 {
			// Register RTSP sources in the registry and get their source IDs
			registry := myaudio.GetRegistry()
			if registry == nil {
				return nil, fmt.Errorf("audio source registry not available")
			}
			
			var failedSources []string
			for _, url := range settings.Realtime.RTSP.URLs {
				if url == "" {
					log.Printf("⚠️ Skipping empty RTSP URL")
					continue
				}
				
				// Register the source
				source, err := registry.RegisterSource(url, myaudio.SourceConfig{
					ID:          "", // Let registry generate UUID
					DisplayName: "", // Let auto-generation use SafeString
					Type:        myaudio.SourceTypeRTSP,
				})
				if err != nil {
					safeURL := privacy.SanitizeRTSPUrl(url)
					log.Printf("❌ Failed to register RTSP source %s: %v", safeURL, err)
					failedSources = append(failedSources, safeURL)
					continue
				}
				
				sources = append(sources, source.ID)
			}

			// If some sources failed to register, log a summary
			if len(failedSources) > 0 {
				log.Printf("⚠️  %d out of %d RTSP sources failed to register: %v",
					len(failedSources), len(settings.Realtime.RTSP.URLs), failedSources)
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
				log.Printf("⚠️  Failed to register audio device source: %v", err)
			} else {
				sources = append(sources, source.ID)
			}
		}

		// Initialize buffers for all audio sources
		if err := initializeBuffers(sources); err != nil {
			// If buffer initialization fails, log the error but continue
			// Some sources might still work
			log.Printf("⚠️  Error initializing buffers: %v", err)
			log.Println("⚠️  Some audio sources might not be available.")
		}
	} else {
		log.Println("⚠️  Starting without active audio sources. You can configure audio devices or RTSP streams through the web interface.")
	}
	return sources, nil
}

// printSystemDetails prints system information and analyzer configuration
func printSystemDetails(settings *conf.Settings) {
	// Get system details with gopsutil
	info, err := host.Info()
	if err != nil {
		fmt.Printf("❌ Error retrieving host info: %v\n", err)
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

	// Print platform, OS etc. details
	fmt.Printf("System details: %s %s %s on %s hardware\n", info.OS, info.Platform, info.PlatformVersion, hwModel)

	// Log the start of BirdNET-Go Analyzer in realtime mode and its configurations.
	fmt.Printf("Starting analyzer in realtime mode. Threshold: %v, overlap: %v, sensitivity: %v, interval: %v\n",
		settings.BirdNET.Threshold,
		settings.BirdNET.Overlap,
		settings.BirdNET.Sensitivity,
		settings.Realtime.Interval)
}
