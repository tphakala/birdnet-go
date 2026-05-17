package analysis

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/audiocore/equalizer"
	"github.com/tphakala/birdnet-go/internal/audiocore/schedule"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/weather"
)

// audioPipelineServiceName is the service name used for logging and diagnostics.
const audioPipelineServiceName = "audio-pipeline"

// hlsCleanupTimeout is the maximum time allowed for HLS file cleanup during shutdown.
const hlsCleanupTimeout = 2 * time.Second

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
	engine     *engine.AudioEngine

	watchdog            *audiocore.LivenessWatchdog
	bufferMgr           *BufferManager
	ctrlMonitor         *ControlMonitor
	quietHoursScheduler *schedule.QuietHoursScheduler
	soundLevelChan      chan soundlevel.SoundLevelData
	restartChan         chan struct{}
	done                chan struct{}
	doneOnce            sync.Once
	wg                  sync.WaitGroup

	// sourcesMu serializes operations that mutate the set of active audio
	// sources: restartAudioCapture (restart loop goroutine), RestartSource
	// (watchdog goroutine), and reconfigureChangedSources (control monitor
	// goroutine). Without this, concurrent execution can cause duplicate
	// source initialization and conflicting router states.
	sourcesMu sync.Mutex

	// soundLevelMu guards soundLevelConsumers. It is held only while mutating
	// the map; router and engine calls happen outside the critical section so
	// that Router.RemoveRoute (which itself takes a write lock and waits for
	// the drainer to stop) cannot deadlock against a concurrent reconfigure.
	soundLevelMu sync.Mutex
	// soundLevelConsumers maps sourceID to the soundlevel consumer ID currently
	// registered on the router. A missing entry means no soundlevel route is
	// active for that source. Populated by registerSoundLevelConsumers, drained
	// by removeAllSoundLevelConsumers.
	soundLevelConsumers map[string]string
}

// NewAudioPipelineService creates a new AudioPipelineService with the given dependencies.
// The service is not started; call Start() to initialize the audio pipeline.
func NewAudioPipelineService(settings *conf.Settings, bnAnalyzer *BirdNETAnalyzer, dbService *DatabaseService, apiService *APIServerService, audioEngine *engine.AudioEngine) *AudioPipelineService {
	return &AudioPipelineService{
		settings:   settings,
		bnAnalyzer: bnAnalyzer,
		dbService:  dbService,
		apiService: apiService,
		engine:     audioEngine,
	}
}

// Name returns a human-readable identifier for logging and diagnostics.
func (p *AudioPipelineService) Name() string {
	return audioPipelineServiceName
}

// buildLivenessConfig creates a LivenessConfig from user settings, falling back
// to production defaults for any zero-valued field.
func buildLivenessConfig(ws conf.WatchdogSettings) audiocore.LivenessConfig {
	cfg := audiocore.DefaultLivenessConfig()
	if ws.CheckInterval > 0 {
		cfg.CheckInterval = time.Duration(ws.CheckInterval) * time.Second
	}
	if ws.SilenceThreshold > 0 {
		cfg.SilenceThreshold = time.Duration(ws.SilenceThreshold) * time.Second
	}
	if ws.MaxRetries > 0 {
		cfg.MaxRetries = ws.MaxRetries
	}
	if ws.RetryBackoff > 0 {
		cfg.RetryBackoff = time.Duration(ws.RetryBackoff) * time.Second
	}
	if ws.Cooldown > 0 {
		cfg.CooldownAfterRecov = time.Duration(ws.Cooldown) * time.Second
	}
	if ws.EscalationTimeout > 0 {
		cfg.EscalationTimeout = time.Duration(ws.EscalationTimeout) * time.Second
	}
	return cfg
}

// Watchdog returns the audio liveness watchdog, or nil if not started.
func (p *AudioPipelineService) Watchdog() *audiocore.LivenessWatchdog {
	return p.watchdog
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

	// Set the primary model ID and buffer dimensions on the engine so that
	// analysis buffers are allocated from the model's spec, not hardcoded
	// constants. This matches the secondary model allocation path.
	clipBytes, overlapBytes, readSize := bn.ModelInfo.Spec.BufferDimensions()
	p.engine.SetPrimaryModel(bn.ModelInfo.ID, clipBytes, overlapBytes, readSize)

	// Register all loaded models in the ai_models database table so they
	// appear even before any detections are saved.
	log := GetLogger()
	modelInfos := bn.ModelInfos()
	for i := range modelInfos {
		detInfo := modelInfos[i].ToDetectionModelInfo()
		if err := dataStore.EnsureModelRegistered(detInfo); err != nil {
			log.Warn("failed to register model in database",
				logger.String("model_id", modelInfos[i].ID),
				logger.String("detection_name", detInfo.Name),
				logger.Error(err),
				logger.String("operation", "startup_model_registration"))
		} else {
			log.Info("registered model in database",
				logger.String("model_id", modelInfos[i].ID),
				logger.String("detection_name", detInfo.Name),
				logger.String("detection_version", detInfo.Version),
				logger.String("operation", "startup_model_registration"))
		}
	}

	// Clean up any leftover HLS streaming files from previous runs.
	if err := cleanupHLSStreamingFiles(); err != nil {
		logHLSCleanup(err)
	} else {
		logHLSCleanup(nil)
	}

	// Initialize channels.
	p.soundLevelChan = make(chan soundlevel.SoundLevelData, 100)
	p.restartChan = make(chan struct{}, 10)
	p.done = make(chan struct{})

	// NOTE: Previously called birdnet.ResizeQueue(5) here, but this caused a race
	// condition: the detection processor goroutine (started by APIServerService)
	// ranges over birdnet.ResultsQueue, and ResizeQueue closes the old channel
	// and creates a new one. The processor's range loop exits on the closed
	// channel, killing the detection pipeline. The default queue size of 100 is
	// fine; shrinking to 5 added unnecessary backpressure with no benefit.

	// Initialize the buffer manager using the engine's buffer manager.
	quitChan := p.done // buffer manager uses this to know when to stop
	p.bufferMgr = MustNewBufferManager(bn, p.engine.BufferManager(), quitChan, &p.wg)

	// Inject the buffer manager and registry into the processor, then start
	// its background goroutines. This order is critical: BufferMgr and Registry
	// must be set BEFORE Start() so detections can access capture buffers.
	proc := p.apiService.Processor()
	proc.BufferMgr = p.engine.BufferManager()
	proc.SetRegistry(p.engine.Registry())
	proc.Start()

	// Add audio sources, register consumers, and start buffer monitors.
	apiAudioLevelChan := p.apiService.AudioLevelChan()
	sourceIDs := p.setupAudioSources(apiAudioLevelChan, "start")

	if len(sourceIDs) == 0 {
		audiocore.GetLogger().Warn("starting without active audio sources",
			logger.Int("rtsp_streams", len(settings.Realtime.RTSP.Streams)),
			logger.Int("audio_sources", len(settings.Realtime.Audio.Sources)),
			logger.String("operation", "startup_audio_check"))
	}

	// Register watchdog reset callback so analysis monitors are recreated
	// when the watchdog force-resets a stuck stream.
	p.engine.FFmpegManager().SetOnStreamReset(func(newSourceID string) {
		if err := p.bufferMgr.AddMonitor(newSourceID); err != nil {
			audiocore.GetLogger().Warn("failed to add monitor after watchdog stream reset",
				logger.String("source_id", newSourceID),
				logger.Error(err),
				logger.String("operation", "watchdog_add_monitor"))
		} else {
			audiocore.GetLogger().Info("started analysis monitor after watchdog stream reset",
				logger.String("source_id", newSourceID),
				logger.String("operation", "watchdog_add_monitor"))
		}
	})

	// Initialize quiet hours scheduler for stream and sound card management.
	// Uses audiocore/schedule: scheduler is independent of the audio capture pipeline.
	p.quietHoursScheduler = schedule.NewQuietHoursScheduler(schedule.QuietHoursConfig{
		SunCalc:     p.apiService.SunCalc(),
		ControlChan: p.apiService.ControlChan(),
	})
	p.engine.SetScheduler(p.quietHoursScheduler)
	p.quietHoursScheduler.Start()

	// Start audio liveness watchdog for detecting silent capture deaths.
	notifSvc := notification.GetService()
	watchdogCallbacks := audiocore.LivenessCallbacks{
		RestartSource: p.RestartSource,
		Escalate: func(_ string) {
			select {
			case p.restartChan <- struct{}{}:
			default:
			}
		},
		Notify: func(sourceID string, state audiocore.LivenessState, msg string) {
			if notifSvc == nil {
				return
			}
			priority := notification.PriorityHigh
			title := "Audio source " + msg
			body := "Source " + sourceID + ": " + msg
			if state == audiocore.StateFailed || state == audiocore.StateEscalated {
				priority = notification.PriorityCritical
			}
			if _, err := notifSvc.CreateWithComponent(
				notification.TypeSystem, priority,
				title, body, "audiocore.liveness",
			); err != nil {
				audiocore.GetLogger().Warn("failed to send liveness notification",
					logger.Error(err),
					logger.String("operation", "liveness_notify"))
			}
		},
		IsQuietHours: func(sourceID string) bool {
			if p.quietHoursScheduler == nil {
				return false
			}
			src, ok := p.engine.Registry().Get(sourceID)
			if ok && src.Type == audiocore.SourceTypeAudioCard {
				return p.quietHoursScheduler.IsSoundCardSuppressed()
			}
			return p.quietHoursScheduler.IsStreamSuppressed(sourceID)
		},
	}
	p.watchdog = audiocore.NewLivenessWatchdog(
		buildLivenessConfig(settings.Realtime.Audio.Watchdog),
		p.engine.Router(),
		watchdogCallbacks,
	)
	p.watchdog.Start()

	// Expose the watchdog and source restarter to the API controller.
	if ctrl := p.apiService.APIController(); ctrl != nil {
		ctrl.SetAudioWatchdog(p.watchdog)
		ctrl.SetSourceRestarter(p.RestartSource)
	}

	// Inject suncalc into the orchestrator for bat nighttime scheduling.
	bn.SetSunCalc(p.apiService.SunCalc())

	// Publish application started alert event.
	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeApplication,
		EventName:  alerting.EventApplicationStarted,
		Properties: map[string]any{},
	})

	// RTSP health monitoring is built into the FFmpeg manager.
	if len(settings.Realtime.RTSP.Streams) > 0 {
		audiocore.GetLogger().Info("RTSP streams will be monitored by FFmpeg manager",
			logger.Int("stream_count", len(settings.Realtime.RTSP.Streams)),
			logger.String("operation", "rtsp_monitoring_setup"))
	}

	// Start clip cleanup monitor.
	// Uses conf.Setting() instead of local settings for hot-reload support:
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
	// The reconfigure callback diffs current vs desired stream configs and only
	// tears down/recreates sources that actually changed.
	reconfigureFn := func() {
		p.reconfigureChangedSources(apiAudioLevelChan)
	}
	reconfigureSoundLevelFn := p.ReconfigureSoundLevel
	apiController := p.apiService.APIController()
	p.ctrlMonitor = NewControlMonitor(&p.wg, p.apiService.ControlChan(), p.done, p.restartChan, p.bufferMgr, proc, apiAudioLevelChan, p.soundLevelChan, apiController, metrics, p.quietHoursScheduler, p.engine, reconfigureFn, reconfigureSoundLevelFn)
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

	// NOTE: FFmpeg manager shutdown is handled by engine.Stop() in serve.go.

	// Stop liveness watchdog before quiet hours scheduler so that
	// IsQuietHours callbacks do not race with scheduler teardown.
	if p.watchdog != nil {
		p.watchdog.Stop()
	}

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

// restartAudioCapture restarts the audio capture by removing and re-adding
// all sources via the AudioEngine.
func (p *AudioPipelineService) restartAudioCapture() {
	p.sourcesMu.Lock()
	defer p.sourcesMu.Unlock()

	audiocore.GetLogger().Info("restarting audio capture",
		logger.String("operation", "restart_audio_capture"))

	// Remove all existing sources.
	p.removeAllSources("restart")

	// Re-add sources, register consumers, and update buffer monitors.
	audioLevelChan := p.apiService.AudioLevelChan()
	p.setupAudioSources(audioLevelChan, "restart")
}

// RestartSource tears down and reinitializes a single audio source.
// Follows the same cleanup pattern as reconfigureChangedSources: remove routes,
// clean up overrun trackers, untrack sound level, stop capture, then re-add.
func (p *AudioPipelineService) RestartSource(sourceID string) error {
	p.sourcesMu.Lock()
	defer p.sourcesMu.Unlock()

	log := audiocore.GetLogger()
	log.Info("restarting single audio source",
		logger.String("source_id", sourceID),
		logger.String("operation", "restart_source"))

	registry := p.engine.Registry()

	// Save connection string before removal (needed to look up config).
	// ConnectionStringByID reads the private field directly from the registry,
	// unlike Get() which returns a copy with the field cleared for safety.
	connStr, ok := registry.ConnectionStringByID(sourceID)
	if !ok {
		return fmt.Errorf("restart source: source %s not found in registry", sourceID)
	}

	// 1. Clean up overrun tracker state.
	RemoveOverrunTrackers(sourceID)

	// 2. Untrack sound level consumer (engine.RemoveSource removes the route).
	p.untrackSoundLevelConsumer(sourceID)

	// 3. Remove source from engine (stops capture, removes routes, deallocates buffers, unregisters).
	if err := p.engine.RemoveSource(sourceID); err != nil {
		log.Error("failed to remove source during restart",
			logger.String("source_id", sourceID),
			logger.Error(err),
			logger.String("operation", "restart_source"))
		return fmt.Errorf("restart source: remove failed: %w", err)
	}

	// 5. Rebuild source config from current settings.
	sourceConfigs := p.buildSourceConfigsWithModels()
	var targetConfig *sourceConfigWithModels
	for i := range sourceConfigs {
		if sourceConfigs[i].config.ConnectionString == connStr {
			targetConfig = &sourceConfigs[i]
			break
		}
	}
	if targetConfig == nil {
		log.Warn("source config no longer in settings after removal",
			logger.String("source_id", sourceID),
			logger.String("operation", "restart_source"))
		return fmt.Errorf("restart source: config for %s no longer exists in settings", sourceID)
	}

	// 6. Re-add source via engine.
	if err := p.engine.AddSource(targetConfig.config); err != nil {
		log.Error("failed to re-add source during restart",
			logger.String("source_id", sourceID),
			logger.Error(err),
			logger.String("operation", "restart_source"))
		return fmt.Errorf("restart source: add failed: %w", err)
	}

	// The source may get a new ID from the registry. Look it up.
	newSrc, found := registry.GetByConnection(connStr)
	if !found {
		return fmt.Errorf("restart source: source re-added but not found in registry")
	}
	newSourceID := newSrc.ID

	// 7. Re-register consumers and monitors.
	audioLevelChan := p.apiService.AudioLevelChan()
	sourceModelMap := map[string][]string{newSourceID: targetConfig.modelIDs}
	p.registerConsumersForSources([]string{newSourceID}, sourceModelMap, audioLevelChan, "restart_source")
	p.registerSoundLevelConsumers([]string{newSourceID}, "restart_source")

	// Update buffer monitors.
	if monErr := p.bufferMgr.AddMonitor(newSourceID); monErr != nil {
		log.Warn("buffer monitor update failed during source restart",
			logger.String("source_id", newSourceID),
			logger.Error(monErr),
			logger.String("operation", "restart_source"))
	}

	// Reset dispatch timestamp so watchdog starts fresh.
	p.engine.Router().ResetDispatchTime(newSourceID)

	log.Info("single source restart complete",
		logger.String("old_source_id", sourceID),
		logger.String("new_source_id", newSourceID),
		logger.String("operation", "restart_source"))

	return nil
}

// removeAllSources removes all audio sources from the engine.
// The operation parameter is used for log messages to distinguish callers.
func (p *AudioPipelineService) removeAllSources(operation string) {
	for _, src := range p.engine.Registry().List() {
		if err := p.engine.RemoveSource(src.ID); err != nil {
			audiocore.GetLogger().Warn("failed to remove source",
				logger.String("source_id", src.ID),
				logger.Error(err),
				logger.String("operation", operation))
		}
	}
	// engine.RemoveSource removes router routes but has no knowledge of the
	// soundlevel tracking map. Clear the map to keep it in sync with actual
	// router state so the next registerSoundLevelConsumers call (e.g. after
	// restartAudioCapture) does not skip sources due to stale entries.
	p.untrackAllSoundLevelConsumers()
	ResetOverrunTrackers()
}

// setupAudioSources builds source configs from current settings, adds them to
// the engine, registers buffer and audio level consumers on the router, and
// updates buffer monitors. Returns the IDs of successfully added sources.
// The audioLevelChan receives bridged audio level data for the API SSE endpoint.
// The operation parameter is used in log messages to distinguish callers.
func (p *AudioPipelineService) setupAudioSources(audioLevelChan chan audiocore.AudioLevelData, operation string) []string {
	log := audiocore.GetLogger()

	// Add audio sources via engine: this registers sources, allocates buffers,
	// and starts capture (FFmpeg streams or device capture).
	sourceConfigs := p.buildSourceConfigsWithModels()
	sourceModelMap := make(map[string][]string, len(sourceConfigs))
	var sourceIDs []string
	for _, scm := range sourceConfigs {
		if addErr := p.engine.AddSource(scm.config); addErr != nil {
			log.Error("failed to add audio source",
				logger.String("source_id", scm.config.ID),
				logger.String("source_type", string(scm.config.Type)),
				logger.String("connection", privacy.SanitizeStreamUrl(scm.config.ConnectionString)),
				logger.Error(addErr),
				logger.String("operation", operation))
			continue
		}
		if src, ok := p.engine.Registry().GetByConnection(scm.config.ConnectionString); ok {
			sourceIDs = append(sourceIDs, src.ID)
			sourceModelMap[src.ID] = scm.modelIDs
		} else {
			log.Warn("source added but not found in registry by connection string",
				logger.String("connection", privacy.SanitizeStreamUrl(scm.config.ConnectionString)),
				logger.String("operation", operation))
		}
	}

	// Register buffer, audio level, and sound level consumers for all sources.
	p.registerConsumersForSources(sourceIDs, sourceModelMap, audioLevelChan, operation)
	p.registerSoundLevelConsumers(sourceIDs, operation)

	// Update buffer monitors for the new sources.
	if len(sourceIDs) > 0 {
		sourceMonitorConfigs := p.buildMonitorConfigs(sourceModelMap, sourceIDs)
		if monErr := p.bufferMgr.UpdateMonitors(sourceMonitorConfigs); monErr != nil {
			log.Warn("buffer monitor update completed with errors",
				logger.Error(monErr),
				logger.Int("source_count", len(sourceIDs)),
				logger.String("component", "analysis.audio_pipeline"),
				logger.String("operation", operation))
		}
	}

	return sourceIDs
}

// registerSoundLevelConsumers creates and registers a SoundLevelConsumer on the
// AudioRouter for each source ID, bridging output to the pipeline's soundLevelChan.
//
// The function is a no-op when Realtime.Audio.SoundLevel.Enabled is false so that
// the DSP pipeline (27 biquad filters per source, per-second float64 accumulator
// windows) is not allocated or exercised when sound level monitoring is off. It
// is also idempotent: sources that already have a tracked consumer are skipped,
// so the caller may include previously registered source IDs.
func (p *AudioPipelineService) registerSoundLevelConsumers(sourceIDs []string, operation string) {
	log := audiocore.GetLogger()
	settings := conf.Setting()
	if !settings.Realtime.Audio.SoundLevel.Enabled {
		return
	}
	slInterval := settings.Realtime.Audio.SoundLevel.Interval
	if slInterval <= 0 {
		slInterval = 10 // default 10-second aggregation window
	}
	audioSettings := &settings.Realtime.Audio
	for _, sid := range sourceIDs {
		// Skip sources that already have a registered soundlevel consumer so
		// the function is safe to call with overlapping source ID sets across
		// startup, reconfigure_diff and hot-reload enable paths.
		p.soundLevelMu.Lock()
		_, already := p.soundLevelConsumers[sid]
		p.soundLevelMu.Unlock()
		if already {
			continue
		}

		// Look up per-source gain from the registry.
		gainDB, _ := p.engine.Registry().GetGain(sid)

		// Resolve per-source/per-stream EQ filter chain using the registry's
		// DisplayName, which matches the config Name for both audio sources and streams.
		src, _ := p.engine.Registry().Get(sid)
		sourceName := sid
		if src != nil {
			sourceName = src.DisplayName
		}
		override := settings.ResolveEQOverride(sourceName)

		// Use per-source sample rate when available; fall back to global constant.
		sourceSampleRate := conf.SampleRate
		if src != nil && src.SampleRate > 0 {
			sourceSampleRate = src.SampleRate
		}

		eqChain := equalizer.BuildFilterChainWithOverride(override, audioSettings.Equalizer, sourceName, sourceSampleRate)

		slProc, slErr := soundlevel.NewProcessor(sid, sid, sourceSampleRate, slInterval)
		if slErr != nil {
			log.Warn("failed to create sound level processor",
				logger.String("source_id", sid),
				logger.Error(slErr),
				logger.String("operation", operation))
			continue
		}
		consumerID := "soundlevel_" + sid
		slc, slOutCh, slcErr := NewSoundLevelConsumer(consumerID, slProc, sourceSampleRate, conf.BitDepth, 1)
		if slcErr != nil {
			log.Warn("failed to create sound level consumer",
				logger.String("source_id", sid),
				logger.Error(slcErr),
				logger.String("operation", operation))
			continue
		}
		if routeErr := p.engine.Router().AddRoute(sid, slc, sourceSampleRate, gainDB, eqChain); routeErr != nil {
			log.Warn("failed to add sound level route",
				logger.String("source_id", sid),
				logger.Error(routeErr),
				logger.String("operation", operation))
			continue
		}
		// Re-check SoundLevel.Enabled while holding the mutex to close a
		// narrow window: a concurrent ReconfigureSoundLevel disable path
		// could have drained the map between AddRoute above and this
		// insert. Without the re-check the route would become orphaned
		// because removeAllSoundLevelConsumers already ran before we
		// inserted our entry. If the flag flipped to false, we remove the
		// route immediately and do not track it.
		p.soundLevelMu.Lock()
		if !conf.Setting().Realtime.Audio.SoundLevel.Enabled {
			p.soundLevelMu.Unlock()
			p.engine.Router().RemoveRoute(sid, consumerID)
			log.Debug("sound level disabled mid-register, dropped route",
				logger.String("source_id", sid),
				logger.String("consumer_id", consumerID),
				logger.String("operation", operation))
			continue
		}
		if _, raced := p.soundLevelConsumers[sid]; raced {
			p.soundLevelMu.Unlock()
			log.Debug("sound level consumer already registered concurrently, skipping",
				logger.String("source_id", sid),
				logger.String("consumer_id", consumerID),
				logger.String("operation", operation))
			continue
		}
		if p.soundLevelConsumers == nil {
			p.soundLevelConsumers = make(map[string]string)
		}
		p.soundLevelConsumers[sid] = consumerID
		p.soundLevelMu.Unlock()
		// Bridge sound level data to the pipeline's sound level channel.
		p.wg.Go(func() {
			for {
				select {
				case data, ok := <-slOutCh:
					if !ok {
						return
					}
					select {
					case p.soundLevelChan <- data:
					default:
					}
				case <-p.done:
					return
				}
			}
		})
		log.Debug("registered sound level consumer",
			logger.String("source_id", sid),
			logger.Int("interval_seconds", slInterval),
			logger.String("operation", operation))
	}
}

// ReconfigureSoundLevel reconciles the sound level pipeline with the current
// Realtime.Audio.SoundLevel settings. When enabled, it rebuilds the pipeline
// for every active source so that changes to settings baked into the
// processor (e.g. Interval) take effect; when disabled, it tears down all
// tracked consumers by removing their router routes (which closes the
// consumer and stops its drainer goroutine).
//
// This is the entry point for hot-reload. ControlMonitor.handleReconfigureSoundLevel
// invokes it before restarting the downstream publisher so that the DSP
// pipeline is either fully constructed (enable) or fully torn down (disable)
// by the time the publisher side (SoundLevelManager) reconfigures.
func (p *AudioPipelineService) ReconfigureSoundLevel() {
	settings := conf.Setting()
	if settings.Realtime.Audio.SoundLevel.Enabled {
		// Enable path: rebuild by tearing down any existing consumers first
		// so settings such as Interval (captured at Processor construction
		// time) propagate on hot-reload. registerSoundLevelConsumers is
		// idempotent but does not recreate existing processors, so the
		// teardown is required to apply interval changes.
		p.removeAllSoundLevelConsumers("hot_reload_rebuild")
		registry := p.engine.Registry()
		active := registry.List()
		sourceIDs := make([]string, 0, len(active))
		for i := range active {
			sourceIDs = append(sourceIDs, active[i].ID)
		}
		if len(sourceIDs) > 0 {
			p.registerSoundLevelConsumers(sourceIDs, "hot_reload_enable")
		}
		return
	}
	// Disable path: remove every tracked consumer. The router closes the
	// consumer, which closes its output channel and unblocks the bridge
	// goroutine registered in registerSoundLevelConsumers.
	p.removeAllSoundLevelConsumers("hot_reload_disable")
}

// untrackSoundLevelConsumer removes the tracking entry for sid without
// calling Router.RemoveRoute. Callers use this when they have already removed
// the router route via engine.RemoveSource or Router.RemoveAllRoutes so the
// tracking map stays in sync with actual router state.
func (p *AudioPipelineService) untrackSoundLevelConsumer(sid string) {
	p.soundLevelMu.Lock()
	delete(p.soundLevelConsumers, sid)
	p.soundLevelMu.Unlock()
}

// untrackAllSoundLevelConsumers clears the tracking map without calling
// Router.RemoveRoute. Callers use this when they have already removed every
// source (and therefore every route) from the engine, so that a subsequent
// registerSoundLevelConsumers call does not skip sources due to stale
// entries.
func (p *AudioPipelineService) untrackAllSoundLevelConsumers() {
	p.soundLevelMu.Lock()
	p.soundLevelConsumers = nil
	p.soundLevelMu.Unlock()
}

// removeAllSoundLevelConsumers removes every tracked soundlevel route from the
// router. Safe to call when no routes are tracked. The operation label is
// included in the route-removed log entry via the router's own logging.
func (p *AudioPipelineService) removeAllSoundLevelConsumers(operation string) {
	p.soundLevelMu.Lock()
	if len(p.soundLevelConsumers) == 0 {
		p.soundLevelMu.Unlock()
		return
	}
	// Drain the map into a local copy so RemoveRoute calls happen outside the
	// critical section. Clearing the map first ensures a concurrent call
	// observes no tracked consumers.
	toRemove := p.soundLevelConsumers
	p.soundLevelConsumers = make(map[string]string)
	p.soundLevelMu.Unlock()

	log := audiocore.GetLogger()
	router := p.engine.Router()
	for sid, cid := range toRemove {
		router.RemoveRoute(sid, cid)
		log.Debug("removed sound level consumer",
			logger.String("source_id", sid),
			logger.String("consumer_id", cid),
			logger.String("operation", operation))
	}
}

// registerConsumersForSources registers BufferConsumer and AudioLevelConsumer
// on the AudioRouter for each source ID. The sourceModelMap carries the
// config-level model IDs for each source so that buffer consumers fan out to
// only the models assigned to that source. When a source has no configured
// models (empty slice), the primary model is used as a fallback.
func (p *AudioPipelineService) registerConsumersForSources(sourceIDs []string, sourceModelMap map[string][]string, audioLevelChan chan audiocore.AudioLevelData, operation string) {
	log := audiocore.GetLogger()

	// Build a lookup of all loaded model infos keyed by registry ID.
	modelInfoSlice := p.bnAnalyzer.BirdNET().ModelInfos()
	allModelInfos := make(map[string]classifier.ModelInfo, len(modelInfoSlice))
	for i := range modelInfoSlice {
		allModelInfos[modelInfoSlice[i].ID] = modelInfoSlice[i]
	}

	// Primary model fallback targets for sources with no model config.
	primaryInfo := &p.bnAnalyzer.BirdNET().ModelInfo
	primaryTargets := []classifier.ModelInfo{*primaryInfo}

	bufMgr := p.engine.BufferManager()
	currentSettings := conf.Setting()
	audioSettings := &currentSettings.Realtime.Audio

	for _, sid := range sourceIDs {
		// Look up per-source gain from the registry.
		gainDB, _ := p.engine.Registry().GetGain(sid)

		// Resolve per-source/per-stream EQ config for building per-route filter
		// chains. Each route needs its own FilterChain because biquad filters
		// have mutable state (in1/in2/out1/out2); sharing would cause a data race.
		src, _ := p.engine.Registry().Get(sid)
		sourceName := sid
		if src != nil {
			sourceName = src.DisplayName
		}
		eqOverride := currentSettings.ResolveEQOverride(sourceName)

		// Resolve per-source model targets. Fall back to primary if the
		// source has no configured models or none could be resolved.
		modelInfos := resolveModelTargets(sourceModelMap[sid], allModelInfos)
		if len(modelInfos) == 0 {
			modelInfos = primaryTargets
		}

		// Allocate analysis buffers for secondary models. The engine
		// already allocates a buffer for the primary model in AddSource(),
		// so only non-primary models need allocation here. Track which
		// models have usable buffers so we only create targets for them.
		allocatedModels := make(map[string]bool, len(modelInfos))
		allocatedModels[primaryInfo.ID] = true // pre-allocated by engine
		for i := range modelInfos {
			if modelInfos[i].ID == primaryInfo.ID {
				continue
			}
			// If the analysis buffer already exists (e.g., gain-only reconfigure),
			// skip allocation and mark the model as usable.
			if bufMgr.HasAnalysis(sid, modelInfos[i].ID) {
				allocatedModels[modelInfos[i].ID] = true
				continue
			}
			clipBytes, overlapBytes, readSize := modelInfos[i].Spec.BufferDimensions()
			if allocErr := bufMgr.AllocateAnalysis(sid, modelInfos[i].ID, clipBytes, overlapBytes, readSize); allocErr != nil {
				log.Warn("failed to allocate analysis buffer for secondary model",
					logger.String("source_id", sid),
					logger.String("model_id", modelInfos[i].ID),
					logger.Error(allocErr),
					logger.String("operation", operation))
				continue
			}
			allocatedModels[modelInfos[i].ID] = true
			log.Info("allocated analysis buffer for secondary model",
				logger.String("source_id", sid),
				logger.String("model_id", modelInfos[i].ID),
				logger.Int("clip_bytes", clipBytes),
				logger.Int("overlap_bytes", overlapBytes),
				logger.String("operation", operation))
		}

		// Convert to ModelTarget for the buffer consumer, excluding
		// models whose buffer allocation failed.
		targets := make([]ModelTarget, 0, len(modelInfos))
		for i := range modelInfos {
			if allocatedModels[modelInfos[i].ID] {
				targets = append(targets, ModelTarget{ModelID: modelInfos[i].ID, SampleRate: modelInfos[i].Spec.EffectiveSampleRate()})
			}
		}

		// Use per-source sample rate when available; fall back to global constant.
		sourceSampleRate := conf.SampleRate
		if src != nil && src.SampleRate > 0 {
			sourceSampleRate = src.SampleRate
		}

		bc, bcErr := NewBufferConsumer(
			fmt.Sprintf("buffer_%s", sid),
			p.engine.BufferManager(),
			sourceSampleRate, conf.BitDepth, 1,
			targets,
		)
		if bcErr != nil {
			log.Warn("failed to create buffer consumer",
				logger.String("source_id", sid), logger.Error(bcErr), logger.String("operation", operation))
			continue
		}
		bcChain := equalizer.BuildFilterChainWithOverride(eqOverride, audioSettings.Equalizer, sourceName, sourceSampleRate)
		if routeErr := p.engine.Router().AddRoute(sid, bc, sourceSampleRate, gainDB, bcChain); routeErr != nil {
			log.Warn("failed to add buffer route",
				logger.String("source_id", sid), logger.Error(routeErr), logger.String("operation", operation))
		}

		alc, alcOutCh := NewAudioLevelConsumer("audio_level_"+sid, sourceSampleRate, conf.BitDepth, 1)
		alcChain := equalizer.BuildFilterChainWithOverride(eqOverride, audioSettings.Equalizer, sourceName, sourceSampleRate)
		if routeErr := p.engine.Router().AddRoute(sid, alc, sourceSampleRate, gainDB, alcChain); routeErr != nil {
			log.Warn("failed to add audio level route",
				logger.String("source_id", sid), logger.Error(routeErr), logger.String("operation", operation))
			continue
		}
		p.wg.Go(func() {
			for {
				select {
				case lvl, ok := <-alcOutCh:
					if !ok {
						return
					}
					select {
					case audioLevelChan <- audiocore.AudioLevelData(lvl):
					default:
					}
				case <-p.done:
					return
				}
			}
		})
	}
}

// sourceNeedsReconfigure reports whether the running source's audio parameters
// differ from the desired config, requiring a full reconfigure (stop + restart).
func sourceNeedsReconfigure(running *audiocore.AudioSource, desired *audiocore.SourceConfig) bool {
	return running.SampleRate != desired.SampleRate ||
		running.BitDepth != desired.BitDepth ||
		running.Channels != desired.Channels
}

// reconfigureChangedSources diffs the currently running sources against the
// desired config from settings. Only sources that were added, removed, or
// changed are touched - unchanged streams keep their capture buffers and
// source IDs intact.
func (p *AudioPipelineService) reconfigureChangedSources(audioLevelChan chan audiocore.AudioLevelData) {
	p.sourcesMu.Lock()
	defer p.sourcesMu.Unlock()

	log := audiocore.GetLogger()

	// Build desired config keyed by connection string, including model IDs.
	desiredConfigs := p.buildSourceConfigsWithModels()
	desired := make(map[string]sourceConfigWithModels, len(desiredConfigs))
	for _, scm := range desiredConfigs {
		desired[scm.config.ConnectionString] = scm
	}

	// Determine which desired configs already have a running source.
	// Registry.List() returns copies with cleared connectionStrings for
	// security, so we look up sources via GetByConnection on the desired
	// connection strings instead.
	registry := p.engine.Registry()
	alreadyRunning := make(map[string]string) // connStr -> sourceID (for sources that stay)
	sourceModelMap := make(map[string][]string)
	var newSourceIDs []string
	var gainChangedIDs []string
	var reconfiguredIDs []string
	var keptCount int

	for connStr, scm := range desired {
		if src, found := registry.GetByConnection(connStr); found {
			// Source already running - keep it.
			alreadyRunning[connStr] = src.ID
			sourceModelMap[src.ID] = scm.modelIDs
			keptCount++

			// Detect audio parameter changes (sample rate, bit depth, channels)
			// that require a full source reconfigure (stop + route removal +
			// buffer realloc + restart). ReconfigureSource handles everything,
			// so skip the gain-only route rebuild for this source.
			if sourceNeedsReconfigure(src, scm.config) {
				log.Info("audio parameters changed, reconfiguring source",
					logger.String("source_id", src.ID),
					logger.Int("old_sample_rate", src.SampleRate),
					logger.Int("new_sample_rate", scm.config.SampleRate),
					logger.Int("old_bit_depth", src.BitDepth),
					logger.Int("new_bit_depth", scm.config.BitDepth),
					logger.String("operation", "reconfigure_diff"))
				if src.Gain != scm.config.Gain {
					registry.UpdateGain(src.ID, scm.config.Gain)
				}
				// ReconfigureSource removes all routes; clear the sound level
				// tracking entry so re-registration is not blocked by the
				// idempotency check.
				p.untrackSoundLevelConsumer(src.ID)
				if err := p.engine.ReconfigureSource(src.ID, scm.config); err != nil {
					log.Error("failed to reconfigure source",
						logger.String("source_id", src.ID),
						logger.Error(err),
						logger.String("operation", "reconfigure_diff"))
				} else {
					reconfiguredIDs = append(reconfiguredIDs, src.ID)
				}
			} else if src.Gain != scm.config.Gain {
				// Gain-only change: update registry and rebuild routes (no restart needed).
				log.Info("gain changed for kept source, rebuilding routes",
					logger.String("source_id", src.ID),
					logger.Float64("old_gain_db", src.Gain),
					logger.Float64("new_gain_db", scm.config.Gain),
					logger.String("operation", "reconfigure_diff"))
				registry.UpdateGain(src.ID, scm.config.Gain)
				gainChangedIDs = append(gainChangedIDs, src.ID)
			}

			// Sync display name if the config name changed (e.g., stream renamed in UI).
			if src.DisplayName != scm.config.DisplayName {
				registry.UpdateDisplayName(src.ID, scm.config.DisplayName)
			}
		} else {
			// New source - add it.
			log.Info("adding new stream from config",
				logger.String("connection", privacy.SanitizeStreamUrl(connStr)),
				logger.String("operation", "reconfigure_diff"))
			if err := p.engine.AddSource(scm.config); err != nil {
				log.Warn("failed to add source during reconfigure",
					logger.String("connection", privacy.SanitizeStreamUrl(connStr)),
					logger.Error(err))
				continue
			}
			if src, ok := registry.GetByConnection(connStr); ok {
				newSourceIDs = append(newSourceIDs, src.ID)
				sourceModelMap[src.ID] = scm.modelIDs
			}
		}
	}

	// Remove sources that are running but no longer in config.
	// Use the registry's full source list (by ID) and check which IDs
	// are not in the alreadyRunning set.
	keepIDs := make(map[string]bool, len(alreadyRunning))
	for _, id := range alreadyRunning {
		keepIDs[id] = true
	}
	for _, id := range newSourceIDs {
		keepIDs[id] = true
	}
	var removedCount int
	for _, src := range registry.List() {
		if keepIDs[src.ID] {
			continue
		}
		removedCount++
		log.Info("removing stream no longer in config",
			logger.String("source_id", src.ID),
			logger.String("operation", "reconfigure_diff"))
		if err := p.engine.RemoveSource(src.ID); err != nil {
			log.Warn("failed to remove source during reconfigure",
				logger.String("source_id", src.ID),
				logger.Error(err))
		}
		// engine.RemoveSource also removes the soundlevel route. Drop the
		// tracking entry so the idempotency check in
		// registerSoundLevelConsumers does not skip this ID if the same
		// source is re-added later.
		p.untrackSoundLevelConsumer(src.ID)
		RemoveOverrunTrackers(src.ID)
	}

	// Register consumers and monitors only for newly added sources.
	if len(newSourceIDs) > 0 {
		p.registerConsumersForSources(newSourceIDs, sourceModelMap, audioLevelChan, "reconfigure_diff")
		p.registerSoundLevelConsumers(newSourceIDs, "reconfigure_diff")
	}

	// Rebuild routes for sources whose audio params changed. ReconfigureSource
	// removed all routes and reallocated buffers; consumers must be re-created.
	if len(reconfiguredIDs) > 0 {
		p.registerConsumersForSources(reconfiguredIDs, sourceModelMap, audioLevelChan, "reconfigure_params")
		p.registerSoundLevelConsumers(reconfiguredIDs, "reconfigure_params")
	}

	// Rebuild routes for sources whose gain changed. The capture device
	// stays running; only the routes are torn down and re-created so
	// drainRoute picks up the new gainLinear value.
	if len(gainChangedIDs) > 0 {
		for _, sid := range gainChangedIDs {
			p.engine.Router().RemoveAllRoutes(sid)
			// RemoveAllRoutes also removes the soundlevel route, so drop the
			// tracking entry. Without this, the registerSoundLevelConsumers
			// call below would skip the source (idempotency check) and leave
			// it permanently without sound level monitoring.
			p.untrackSoundLevelConsumer(sid)
		}
		p.registerConsumersForSources(gainChangedIDs, sourceModelMap, audioLevelChan, "gain_change")
		p.registerSoundLevelConsumers(gainChangedIDs, "gain_change")
	}

	// Sync monitors for ALL active sources (kept + new) so UpdateMonitors
	// receives the full desired state and removes stale monitors correctly.
	allActiveIDs := slices.Collect(maps.Values(alreadyRunning))
	allActiveIDs = append(allActiveIDs, newSourceIDs...)
	// Always call UpdateMonitors (even with an empty slice) so stale
	// monitors are torn down when the last active stream is disabled.
	if p.bufferMgr != nil {
		monitorMap := p.buildMonitorConfigs(sourceModelMap, allActiveIDs)
		if monErr := p.bufferMgr.UpdateMonitors(monitorMap); monErr != nil {
			log.Warn("buffer monitor update failed during reconfigure", logger.Error(monErr))
		}
	}

	log.Info("audio source reconfiguration complete",
		logger.Int("kept", keptCount),
		logger.Int("added", len(newSourceIDs)),
		logger.Int("removed", removedCount),
		logger.Int("gain_changed", len(gainChangedIDs)),
		logger.String("operation", "reconfigure_diff"))
}

// sourceConfigWithModels pairs an audiocore.SourceConfig with the config-level
// model IDs assigned to that source. This allows the pipeline to build
// per-source model targets when registering buffer consumers.
type sourceConfigWithModels struct {
	config   *audiocore.SourceConfig
	modelIDs []string // config-level IDs, e.g., ["birdnet", "perch_v2"]
}

// buildSourceConfigsWithModels constructs audiocore.SourceConfig entries from
// the current settings, paired with their configured model IDs.
func (p *AudioPipelineService) buildSourceConfigsWithModels() []sourceConfigWithModels {
	settings := conf.Setting()
	var result []sourceConfigWithModels

	// RTSP streams.
	for _, stream := range settings.Realtime.RTSP.EnabledStreams() {
		result = append(result, sourceConfigWithModels{
			config: &audiocore.SourceConfig{
				DisplayName:      stream.Name,
				Type:             audiocore.StreamTypeToSourceType(stream.Type),
				ConnectionString: stream.URL,
				SampleRate:       conf.SampleRate,
				BitDepth:         conf.BitDepth,
				Channels:         1,
			},
			modelIDs: stream.Models,
		})
	}

	// Local audio cards (multi-source).
	for i := range settings.Realtime.Audio.Sources {
		src := &settings.Realtime.Audio.Sources[i]
		if src.Device == "" {
			continue
		}
		sampleRate := conf.SampleRate
		if src.SampleRate > 0 {
			sampleRate = src.SampleRate
		}
		result = append(result, sourceConfigWithModels{
			config: &audiocore.SourceConfig{
				DisplayName:      src.Name,
				Type:             audiocore.SourceTypeAudioCard,
				ConnectionString: src.Device,
				SampleRate:       sampleRate,
				BitDepth:         conf.BitDepth,
				Channels:         1,
				Gain:             src.Gain,
			},
			modelIDs: src.Models,
		})
	}

	return result
}

// buildMonitorConfigs builds the map[sourceID][]monitorConfig needed by
// UpdateMonitors. It resolves per-source model IDs to full ModelInfo so that
// monitorConfig gets the correct spec (sample rate + clip length).
func (p *AudioPipelineService) buildMonitorConfigs(sourceModelMap map[string][]string, sourceIDs []string) map[string][]monitorConfig {
	// Build lookup of loaded models by registry ID.
	modelInfoSlice := p.bnAnalyzer.BirdNET().ModelInfos()
	loadedModels := make(map[string]classifier.ModelInfo, len(modelInfoSlice))
	for i := range modelInfoSlice {
		loadedModels[modelInfoSlice[i].ID] = modelInfoSlice[i]
	}

	primaryInfo := p.bnAnalyzer.BirdNET().ModelInfo
	result := make(map[string][]monitorConfig, len(sourceIDs))

	for _, sid := range sourceIDs {
		// Resolve config-level model IDs to ModelInfo entries.
		var infos []classifier.ModelInfo
		for _, configID := range sourceModelMap[sid] {
			registryID, known := classifier.ResolveConfigModelID(configID)
			if !known {
				continue
			}
			if info, loaded := loadedModels[registryID]; loaded {
				infos = append(infos, info)
			}
		}
		if len(infos) == 0 {
			infos = []classifier.ModelInfo{primaryInfo}
		}

		configs := make([]monitorConfig, len(infos))
		for i := range infos {
			configs[i] = buildMonitorConfig(sid, &infos[i])
		}
		result[sid] = configs
	}

	return result
}

// resolveModelTargets converts config-level model IDs to ModelTarget entries
// using the loaded model registry. Unknown or unloaded models are skipped
// with a warning log.
func resolveModelTargets(configModelIDs []string, loadedModels map[string]classifier.ModelInfo) []classifier.ModelInfo {
	if len(configModelIDs) == 0 {
		return nil
	}
	targets := make([]classifier.ModelInfo, 0, len(configModelIDs))
	for _, configID := range configModelIDs {
		registryID, known := classifier.ResolveConfigModelID(configID)
		if !known {
			GetLogger().Warn("unknown model ID in source config, skipping",
				logger.String("config_id", configID))
			continue
		}
		info, loaded := loadedModels[registryID]
		if !loaded {
			GetLogger().Warn("model configured for source but not loaded",
				logger.String("config_id", configID),
				logger.String("registry_id", registryID))
			continue
		}
		targets = append(targets, info)
	}
	return targets
}

// startWeatherPolling initializes and starts the weather polling routine.
func (p *AudioPipelineService) startWeatherPolling(metrics *observability.Metrics) {
	weatherService, err := weather.NewService(p.settings, p.dbService.DataStore(), metrics.Weather)
	if err != nil {
		// ErrWeatherDisabled is expected when provider is empty/unrecognized
		if errors.Is(err, weather.ErrWeatherDisabled) {
			return
		}
		GetLogger().Error("failed to initialize weather service",
			logger.Error(err),
			logger.String("operation", "initialize_weather_service"))
		return
	}

	p.wg.Go(func() {
		weatherService.StartPolling(p.done)
	})
}

// clipCleanupMonitor monitors the database and deletes clips that meet the retention policy.
func clipCleanupMonitor(quitChan chan struct{}, dataStore datastore.Interface) {
	log := GetLogger()

	// Read initial interval for the startup log message.
	retention := conf.Setting().Realtime.Audio.Export.Retention
	checkInterval := retention.CheckInterval
	if checkInterval <= 0 {
		checkInterval = conf.DefaultCleanupCheckInterval
	}
	log.Info("clip cleanup monitor initialized",
		logger.String("policy", retention.Policy),
		logger.Int("check_interval_minutes", checkInterval),
		logger.String("operation", "clip_cleanup_init"))

	for {
		// Re-read interval each iteration so hot-reload takes effect.
		interval := conf.Setting().Realtime.Audio.Export.Retention.CheckInterval
		if interval <= 0 {
			interval = conf.DefaultCleanupCheckInterval
		}
		timer := time.NewTimer(time.Duration(interval) * time.Minute)

		select {
		case <-quitChan:
			timer.Stop()
			log.Debug("clip cleanup monitor stopped",
				logger.String("operation", "clip_cleanup_stop"))
			return

		case t := <-timer.C:
			currentPolicy := conf.Setting().Realtime.Audio.Export.Retention.Policy
			log.Info("starting clip cleanup task",
				logger.String("timestamp", t.Format(time.RFC3339)),
				logger.String("policy", currentPolicy),
				logger.String("operation", "clip_cleanup_task"))

			if currentPolicy == "age" {
				result := diskmanager.AgeBasedCleanup(quitChan, dataStore)
				if result.Err != nil {
					log.Error("age-based cleanup failed",
						logger.Error(result.Err),
						logger.String("operation", "age_based_cleanup"))
				} else {
					log.Info("age-based cleanup completed",
						logger.Int("clips_removed", result.ClipsRemoved),
						logger.Int("disk_utilization_percent", result.DiskUtilization),
						logger.String("operation", "age_based_cleanup"))
				}
			}

			if currentPolicy == "usage" {
				currentRetention := conf.Setting().Realtime.Audio.Export.Retention
				baseDir := conf.Setting().Realtime.Audio.Export.Path

				skip, utilization, err := diskmanager.ShouldSkipUsageBasedCleanup(&currentRetention, baseDir)
				if err != nil {
					log.Warn("failed to check disk usage",
						logger.Error(err),
						logger.Bool("continuing_with_cleanup", true),
						logger.String("operation", "usage_based_cleanup"))
				} else if skip {
					log.Debug("disk usage below threshold, skipping cleanup",
						logger.Int("disk_utilization_percent", utilization),
						logger.String("operation", "usage_based_cleanup"))
					continue
				}

				result := diskmanager.UsageBasedCleanup(quitChan, dataStore)
				if result.Err != nil {
					log.Error("usage-based cleanup failed",
						logger.Error(result.Err),
						logger.String("operation", "usage_based_cleanup"))
				} else {
					log.Info("usage-based cleanup completed",
						logger.Int("clips_removed", result.ClipsRemoved),
						logger.Int("disk_utilization_percent", result.DiskUtilization),
						logger.String("operation", "usage_based_cleanup"))
				}
			}
		}
	}
}

// cleanupHLSWithTimeout runs HLS cleanup asynchronously with a timeout to prevent blocking shutdown
func cleanupHLSWithTimeout(ctx context.Context) {
	// Create a channel to signal completion
	cleanupDone := make(chan error, 1)

	// Run cleanup in a goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				GetLogger().Error("panic during HLS cleanup",
					logger.Any("panic", r))
				cleanupDone <- errors.Newf("panic during HLS cleanup: %v", r).
					Component("analysis.audio_pipeline").
					Category(errors.CategorySystem).
					Context("operation", "hls_cleanup_panic").
					Priority(errors.PriorityCritical).
					Build()
			}
		}()
		cleanupDone <- cleanupHLSStreamingFiles()
	}()

	cleanupCtx, cancel := context.WithTimeout(ctx, hlsCleanupTimeout)
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
			logger.Duration("timeout", hlsCleanupTimeout),
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
			Component("analysis.audio_pipeline").
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
			Component("analysis.audio_pipeline").
			Category(errors.CategoryFileIO).
			Context("operation", "check_hls_directory").
			Context("hls_dir", hlsDir).
			Build()
	}

	// Read the directory entries
	entries, err := os.ReadDir(hlsDir)
	if err != nil {
		return errors.New(err).
			Component("analysis.audio_pipeline").
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
			Component("analysis.audio_pipeline").
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
