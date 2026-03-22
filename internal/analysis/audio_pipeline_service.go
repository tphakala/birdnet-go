package analysis

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/weather"
)

// audioPipelineServiceName is the service name used for logging and diagnostics.
const audioPipelineServiceName = "audio-pipeline"

// policyNone is the sentinel value indicating no retention/provider policy is configured.
const policyNone = "none"

// AudioPipelineService manages the audio capture pipeline, buffer management,
// and control monitor as an app.Service. It coordinates HLS cleanup, audio source
// initialization, sound level monitoring, quiet hours scheduling, clip cleanup,
// weather polling, and the restart loop for audio capture.
type AudioPipelineService struct {
	settings   *conf.Settings
	bnAnalyzer *BirdNETAnalyzer
	dbService  *DatabaseService
	apiService *APIServerService

	bufferMgr           *BufferManager
	demuxMgr            *AudioDemuxManager
	ctrlMonitor         *ControlMonitor
	quietHoursScheduler *myaudio.QuietHoursScheduler
	soundLevelChan      chan myaudio.SoundLevelData
	restartChan         chan struct{}
	done                chan struct{}
	doneOnce            sync.Once
	wg                  sync.WaitGroup
}

// NewAudioPipelineService creates a new AudioPipelineService with the given dependencies.
// The service is not started; call Start() to initialize the audio pipeline.
func NewAudioPipelineService(settings *conf.Settings, bnAnalyzer *BirdNETAnalyzer, dbService *DatabaseService, apiService *APIServerService) *AudioPipelineService {
	return &AudioPipelineService{
		settings:   settings,
		bnAnalyzer: bnAnalyzer,
		dbService:  dbService,
		apiService: apiService,
	}
}

// Name returns a human-readable identifier for logging and diagnostics.
func (p *AudioPipelineService) Name() string {
	return audioPipelineServiceName
}

// Start initializes and starts the audio capture pipeline, buffer management,
// and all supporting subsystems (sound level, quiet hours, clip cleanup, weather,
// control monitor, and the restart loop).
//
//nolint:gocognit // Orchestration function that coordinates multiple subsystems during startup.
func (p *AudioPipelineService) Start(_ context.Context) error {
	// If Start fails after creating resources, clean up to prevent leaks.
	// The App framework only calls Stop() on services that started successfully,
	// so the failing service must clean up after itself.
	startSucceeded := false
	defer func() {
		if !startSucceeded {
			// Best-effort cleanup. Stop is safe on a partially initialized service.
			_ = p.Stop(context.Background())
		}
	}()

	// Fail fast: verify dependencies are initialized by upstream services.
	if p.dbService == nil || p.dbService.DataStore() == nil {
		return errors.Newf("audio-pipeline requires an initialized datastore; database service must be started first").
			Component("analysis.audio_pipeline").
			Category(errors.CategorySystem).
			Context("operation", "start_precondition_check").
			Build()
	}
	if p.bnAnalyzer == nil || p.bnAnalyzer.BirdNET() == nil {
		return errors.Newf("audio-pipeline requires an initialized birdnet model; birdnet-analyzer service must be started first").
			Component("analysis.audio_pipeline").
			Category(errors.CategorySystem).
			Context("operation", "start_precondition_check").
			Build()
	}
	if p.apiService == nil || p.apiService.Processor() == nil {
		return errors.Newf("audio-pipeline requires an initialized processor; api-server service must be started first").
			Component("analysis.audio_pipeline").
			Category(errors.CategorySystem).
			Context("operation", "start_precondition_check").
			Build()
	}

	settings := p.settings
	bn := p.bnAnalyzer.BirdNET()
	dataStore := p.dbService.DataStore()
	metrics := p.apiService.Metrics()

	// Clean up any leftover HLS streaming files from previous runs.
	if err := cleanupHLSStreamingFiles(); err != nil {
		logHLSCleanup(err)
	} else {
		logHLSCleanup(nil)
	}

	// Initialize channels.
	p.soundLevelChan = make(chan myaudio.SoundLevelData, 100)
	p.restartChan = make(chan struct{}, 10)
	p.done = make(chan struct{})

	// Initialize audio sources.
	sources, err := initializeAudioSources(settings)
	if err != nil {
		// Non-fatal error, continue with available sources.
		GetLogger().Warn("audio source initialization warning",
			logger.Error(err),
			logger.String("operation", "initialize_audio_sources"))
	}

	// Resize BirdNET queue based on processing needs.
	const defaultQueueSize = 5
	birdnet.ResizeQueue(defaultQueueSize)

	// Initialize the buffer manager.
	quitChan := p.done // buffer manager uses this to know when to stop
	p.bufferMgr = MustNewBufferManager(bn, quitChan, &p.wg)

	// Start buffer monitors for each audio source only if we have active sources.
	if len(settings.Realtime.RTSP.Streams) > 0 || settings.Realtime.Audio.Source != "" {
		if err := p.bufferMgr.UpdateMonitors(sources); err != nil {
			errorStr := err.Error()
			GetLogger().Warn("buffer monitor setup completed with errors",
				logger.String("error", errorStr),
				logger.Int("source_count", len(sources)),
				logger.Any("sources", sources),
				logger.String("component", "analysis.realtime"),
				logger.String("operation", "buffer_monitor_setup"))
		}
	} else {
		GetLogger().Warn("starting without active audio sources",
			logger.Int("rtsp_streams", len(settings.Realtime.RTSP.Streams)),
			logger.String("audio_source", settings.Realtime.Audio.Source),
			logger.String("operation", "startup_audio_check"))
	}

	// Register watchdog reset callback so analysis monitors are recreated
	// when the watchdog force-resets a stuck stream.
	myaudio.SetOnStreamReset(func(newSourceID string) {
		if err := p.bufferMgr.AddMonitor(newSourceID); err != nil {
			GetLogger().Warn("failed to add monitor after watchdog stream reset",
				logger.String("source_id", newSourceID),
				logger.Error(err),
				logger.String("operation", "watchdog_add_monitor"))
		} else {
			GetLogger().Info("started analysis monitor after watchdog stream reset",
				logger.String("source_id", newSourceID),
				logger.String("operation", "watchdog_add_monitor"))
		}
	})

	// Register sound level processors before starting audio capture to avoid
	// a race where audio chunks arrive before processors are registered.
	if settings.Realtime.Audio.SoundLevel.Enabled {
		if err := registerSoundLevelProcessorsForActiveSources(settings); err != nil {
			GetLogger().Warn("early sound level processor registration completed with errors",
				logger.Error(err),
				logger.String("operation", "early_sound_level_registration"))
		}
	}

	// Start audio capture.
	p.demuxMgr = NewAudioDemuxManager()
	unifiedAudioChan := p.startAudioCapture()
	myaudio.SetCurrentAudioChan(unifiedAudioChan)

	// Initialize quiet hours scheduler for stream and sound card management.
	p.quietHoursScheduler = myaudio.NewQuietHoursScheduler(p.apiService.SunCalc(), p.apiService.ControlChan())
	myaudio.SetGlobalScheduler(p.quietHoursScheduler)
	p.quietHoursScheduler.Start()

	// Publish application started alert event.
	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeApplication,
		EventName:  alerting.EventApplicationStarted,
		Properties: map[string]any{},
	})

	// RTSP health monitoring is built into the FFmpeg manager.
	if len(settings.Realtime.RTSP.Streams) > 0 {
		GetLogger().Info("RTSP streams will be monitored by FFmpeg manager",
			logger.Int("stream_count", len(settings.Realtime.RTSP.Streams)),
			logger.String("operation", "rtsp_monitoring_setup"))
	}

	// Start clip cleanup monitor.
	// Uses conf.Setting() instead of local settings for hot-reload support —
	// retention policy can be changed at runtime via the web UI.
	if conf.Setting().Realtime.Audio.Export.Retention.Policy != policyNone {
		p.wg.Go(func() {
			clipCleanupMonitor(p.done, dataStore)
		})
	}

	// Start weather polling.
	if settings.Realtime.Weather.Provider != policyNone {
		p.startWeatherPolling(metrics)
	}

	// Start control monitor for hot reloads.
	proc := p.apiService.Processor()
	audioLevelChan := p.apiService.AudioLevelChan()
	apiController := p.apiService.APIController()
	p.ctrlMonitor = NewControlMonitor(&p.wg, p.apiService.ControlChan(), p.done, p.restartChan, p.bufferMgr, proc, audioLevelChan, p.soundLevelChan, apiController, metrics, p.quietHoursScheduler)
	p.ctrlMonitor.Start()

	// Start restart loop goroutine.
	p.wg.Go(func() {
		for {
			select {
			case <-p.done:
				return
			case <-p.restartChan:
				p.restartAudioCapture()
			}
		}
	})

	startSucceeded = true
	return nil
}

// Stop gracefully shuts down the audio pipeline and all owned subsystems.
// It is safe to call before Start() or multiple times.
func (p *AudioPipelineService) Stop(ctx context.Context) error {
	log := GetLogger()

	// Publish application stopped alert event.
	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeApplication,
		EventName:  alerting.EventApplicationStopped,
		Properties: map[string]any{},
	})

	log.Info("initiating audio pipeline shutdown",
		logger.String("operation", "graceful_shutdown"))

	// Stop control monitor.
	if p.ctrlMonitor != nil {
		log.Info("stopping control monitor",
			logger.String("operation", "shutdown_control_monitor"))
		p.ctrlMonitor.Stop()
		p.ctrlMonitor = nil
	}

	// Stop analysis buffer monitors.
	if p.bufferMgr != nil {
		log.Info("stopping analysis buffer monitors",
			logger.String("operation", "shutdown_buffer_monitors"))
		p.bufferMgr.RemoveAllMonitors()
	}

	// Clean up HLS resources.
	log.Info("cleaning up HLS resources",
		logger.String("operation", "shutdown_hls_cleanup"))
	cleanupHLSWithTimeout(ctx)

	// Shutdown FFmpeg manager.
	log.Info("shutting down FFmpeg manager",
		logger.String("operation", "shutdown_ffmpeg_manager"))
	myaudio.ShutdownFFmpegManagerWithContext(ctx)

	// Stop quiet hours scheduler.
	if p.quietHoursScheduler != nil {
		p.quietHoursScheduler.Stop()
		p.quietHoursScheduler = nil
	}

	// Close done channel to signal restart loop and clip cleanup goroutines.
	// Protected by sync.Once to prevent panic on double-close.
	p.doneOnce.Do(func() {
		if p.done != nil {
			close(p.done)
		}
	})

	// Stop the audio demux manager explicitly. The demux goroutine is tracked by
	// demuxMgr (not p.wg), so we must wait for it here to prevent writes to the
	// already-closed audioLevelChan owned by APIServerService.
	if p.demuxMgr != nil {
		p.demuxMgr.Stop()
	}

	// Wait for goroutines with context deadline.
	log.Info("waiting for goroutines to finish",
		logger.String("operation", "shutdown_wait_goroutines"))
	waitStart := time.Now()
	waitDone := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		log.Info("all goroutines finished",
			logger.Duration("elapsed", time.Since(waitStart)),
			logger.String("operation", "shutdown_goroutines_done"))
	case <-ctx.Done():
		log.Warn("goroutine wait timed out",
			logger.Duration("elapsed", time.Since(waitStart)),
			logger.String("operation", "shutdown_wait_goroutines"))
	}

	return nil
}

// startAudioCapture initializes and starts the audio capture routine.
// It uses the service's demux manager and fields instead of package-level globals.
func (p *AudioPipelineService) startAudioCapture() chan myaudio.UnifiedAudioData {
	// Stop previous demultiplexing goroutine if it exists.
	p.demuxMgr.Stop()

	// Start new demux goroutine.
	doneChan := p.demuxMgr.Start()

	// Create a unified audio channel.
	unifiedAudioChan := make(chan myaudio.UnifiedAudioData, 100)
	go func() {
		defer p.demuxMgr.Done()

		// Demultiplex unified audio data into separate channels.
		for {
			select {
			case <-doneChan:
				return
			case <-p.done:
				return
			case unifiedData, ok := <-unifiedAudioChan:
				if !ok {
					return
				}

				// Send audio level data to the API service's channel.
				audioLevelChan := p.apiService.AudioLevelChan()
				select {
				case <-doneChan:
					return
				case <-p.done:
					return
				case audioLevelChan <- unifiedData.AudioLevel:
				default:
					// Channel full, drop data.
				}

				// Send sound level data to the service's channel if present.
				if unifiedData.SoundLevel != nil {
					select {
					case <-doneChan:
						return
					case <-p.done:
						return
					case p.soundLevelChan <- *unifiedData.SoundLevel:
					default:
						// Channel full, drop data.
					}
				}
			}
		}
	}()

	// CaptureAudio manages its own waitgroup internally.
	go myaudio.CaptureAudio(p.settings, &p.wg, p.done, p.restartChan, unifiedAudioChan)

	return unifiedAudioChan
}

// restartAudioCapture restarts the audio capture, used by the restart loop.
func (p *AudioPipelineService) restartAudioCapture() {
	GetLogger().Info("restarting audio capture",
		logger.String("operation", "restart_audio_capture"))
	unifiedAudioChan := p.startAudioCapture()
	myaudio.SetCurrentAudioChan(unifiedAudioChan)
}

// startWeatherPolling initializes and starts the weather polling routine.
func (p *AudioPipelineService) startWeatherPolling(metrics *observability.Metrics) {
	weatherService, err := weather.NewService(p.settings, p.dbService.DataStore(), metrics.Weather)
	if err != nil {
		GetLogger().Error("failed to initialize weather service",
			logger.Error(err),
			logger.String("operation", "initialize_weather_service"))
		return
	}

	p.wg.Go(func() {
		weatherService.StartPolling(p.done)
	})
}

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
