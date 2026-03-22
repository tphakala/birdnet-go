package analysis

import (
	"context"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
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
