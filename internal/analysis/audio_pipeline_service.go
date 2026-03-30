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
	"github.com/tphakala/birdnet-go/internal/audiocore/schedule"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
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

	bufferMgr           *BufferManager
	ctrlMonitor         *ControlMonitor
	quietHoursScheduler *schedule.QuietHoursScheduler
	soundLevelChan      chan soundlevel.SoundLevelData
	restartChan         chan struct{}
	done                chan struct{}
	doneOnce            sync.Once
	wg                  sync.WaitGroup
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

	// Set the primary model ID on the engine so that analysis buffers are
	// allocated with the correct model key instead of a hardcoded constant.
	p.engine.SetPrimaryModelID(bn.ModelInfo.ID)

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
	// fine — shrinking to 5 added unnecessary backpressure with no benefit.

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
		GetLogger().Warn("starting without active audio sources",
			logger.Int("rtsp_streams", len(settings.Realtime.RTSP.Streams)),
			logger.Int("audio_sources", len(settings.Realtime.Audio.Sources)),
			logger.String("operation", "startup_audio_check"))
	}

	// Register watchdog reset callback so analysis monitors are recreated
	// when the watchdog force-resets a stuck stream.
	p.engine.FFmpegManager().SetOnStreamReset(func(newSourceID string) {
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

	// Initialize quiet hours scheduler for stream and sound card management.
	// Uses audiocore/schedule — scheduler is independent of the audio capture pipeline.
	p.quietHoursScheduler = schedule.NewQuietHoursScheduler(schedule.QuietHoursConfig{
		SunCalc:     p.apiService.SunCalc(),
		ControlChan: p.apiService.ControlChan(),
	})
	p.engine.SetScheduler(p.quietHoursScheduler)
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
	// The reconfigure callback diffs current vs desired stream configs and only
	// tears down/recreates sources that actually changed.
	reconfigureFn := func() {
		p.reconfigureChangedSources(apiAudioLevelChan)
	}
	apiController := p.apiService.APIController()
	p.ctrlMonitor = NewControlMonitor(&p.wg, p.apiService.ControlChan(), p.done, p.restartChan, p.bufferMgr, proc, apiAudioLevelChan, p.soundLevelChan, apiController, metrics, p.quietHoursScheduler, p.engine, reconfigureFn)
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
	GetLogger().Info("restarting audio capture",
		logger.String("operation", "restart_audio_capture"))

	// Remove all existing sources.
	p.removeAllSources("restart")

	// Re-add sources, register consumers, and update buffer monitors.
	audioLevelChan := p.apiService.AudioLevelChan()
	p.setupAudioSources(audioLevelChan, "restart")
}

// removeAllSources removes all audio sources from the engine.
// The operation parameter is used for log messages to distinguish callers.
func (p *AudioPipelineService) removeAllSources(operation string) {
	for _, src := range p.engine.Registry().List() {
		if err := p.engine.RemoveSource(src.ID); err != nil {
			GetLogger().Warn("failed to remove source",
				logger.String("source_id", src.ID),
				logger.Error(err),
				logger.String("operation", operation))
		}
	}
}

// setupAudioSources builds source configs from current settings, adds them to
// the engine, registers buffer and audio level consumers on the router, and
// updates buffer monitors. Returns the IDs of successfully added sources.
// The audioLevelChan receives bridged audio level data for the API SSE endpoint.
// The operation parameter is used in log messages to distinguish callers.
func (p *AudioPipelineService) setupAudioSources(audioLevelChan chan audiocore.AudioLevelData, operation string) []string {
	log := GetLogger()

	// Add audio sources via engine — this registers sources, allocates buffers,
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
func (p *AudioPipelineService) registerSoundLevelConsumers(sourceIDs []string, operation string) {
	log := GetLogger()
	settings := conf.Setting()
	slInterval := settings.Realtime.Audio.SoundLevel.Interval
	if slInterval <= 0 {
		slInterval = 10 // default 10-second aggregation window
	}
	for _, sid := range sourceIDs {
		slProc, slErr := soundlevel.NewProcessor(sid, sid, conf.SampleRate, slInterval)
		if slErr != nil {
			log.Warn("failed to create sound level processor",
				logger.String("source_id", sid),
				logger.Error(slErr),
				logger.String("operation", operation))
			continue
		}
		slc, slOutCh, slcErr := NewSoundLevelConsumer("soundlevel_"+sid, slProc, conf.SampleRate, conf.BitDepth, 1)
		if slcErr != nil {
			log.Warn("failed to create sound level consumer",
				logger.String("source_id", sid),
				logger.Error(slcErr),
				logger.String("operation", operation))
			continue
		}
		if routeErr := p.engine.Router().AddRoute(sid, slc, conf.SampleRate); routeErr != nil {
			log.Warn("failed to add sound level route",
				logger.String("source_id", sid),
				logger.Error(routeErr),
				logger.String("operation", operation))
			continue
		}
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

// registerConsumersForSources registers BufferConsumer and AudioLevelConsumer
// on the AudioRouter for each source ID. The sourceModelMap carries the
// config-level model IDs for each source so that buffer consumers fan out to
// only the models assigned to that source. When a source has no configured
// models (empty slice), the primary model is used as a fallback.
func (p *AudioPipelineService) registerConsumersForSources(sourceIDs []string, sourceModelMap map[string][]string, audioLevelChan chan audiocore.AudioLevelData, operation string) {
	log := GetLogger()

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

	for _, sid := range sourceIDs {
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
			spec := modelInfos[i].Spec
			clipBytes := spec.SampleRate * int(spec.ClipLength.Seconds()) * conf.NumChannels * (conf.BitDepth / 8)
			overlapBytes := clipBytes / 2 // 50% overlap, matching primary model ratio
			readSize := clipBytes - overlapBytes
			if allocErr := bufMgr.AllocateAnalysis(sid, modelInfos[i].ID, clipBytes, overlapBytes, readSize); allocErr != nil {
				log.Warn("failed to allocate analysis buffer for secondary model",
					logger.String("source_id", sid),
					logger.String("model_id", modelInfos[i].ID),
					logger.Error(allocErr),
					logger.String("operation", operation))
				continue
			}
			allocatedModels[modelInfos[i].ID] = true
		}

		// Convert to ModelTarget for the buffer consumer, excluding
		// models whose buffer allocation failed.
		targets := make([]ModelTarget, 0, len(modelInfos))
		for i := range modelInfos {
			if allocatedModels[modelInfos[i].ID] {
				targets = append(targets, ModelTarget{ModelID: modelInfos[i].ID, SampleRate: modelInfos[i].Spec.SampleRate})
			}
		}

		bc, bcErr := NewBufferConsumer(
			fmt.Sprintf("buffer_%s", sid),
			p.engine.BufferManager(),
			conf.SampleRate, conf.BitDepth, 1,
			targets,
		)
		if bcErr != nil {
			log.Warn("failed to create buffer consumer",
				logger.String("source_id", sid), logger.Error(bcErr), logger.String("operation", operation))
			continue
		}
		if routeErr := p.engine.Router().AddRoute(sid, bc, conf.SampleRate); routeErr != nil {
			log.Warn("failed to add buffer route",
				logger.String("source_id", sid), logger.Error(routeErr), logger.String("operation", operation))
		}

		alc, alcOutCh := NewAudioLevelConsumer("audio_level_"+sid, conf.SampleRate, conf.BitDepth, 1)
		if routeErr := p.engine.Router().AddRoute(sid, alc, conf.SampleRate); routeErr != nil {
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

// reconfigureChangedSources diffs the currently running sources against the
// desired config from settings. Only sources that were added, removed, or
// changed are touched — unchanged streams keep their capture buffers and
// source IDs intact.
func (p *AudioPipelineService) reconfigureChangedSources(audioLevelChan chan audiocore.AudioLevelData) {
	log := GetLogger()

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
	alreadyRunning := make(map[string]string) // connStr → sourceID (for sources that stay)
	sourceModelMap := make(map[string][]string)
	var newSourceIDs []string
	var keptCount int

	for connStr, scm := range desired {
		if src, found := registry.GetByConnection(connStr); found {
			// Source already running — keep it.
			alreadyRunning[connStr] = src.ID
			sourceModelMap[src.ID] = scm.modelIDs
			keptCount++
		} else {
			// New source — add it.
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
		if !keepIDs[src.ID] {
			removedCount++
			log.Info("removing stream no longer in config",
				logger.String("source_id", src.ID),
				logger.String("operation", "reconfigure_diff"))
			if err := p.engine.RemoveSource(src.ID); err != nil {
				log.Warn("failed to remove source during reconfigure",
					logger.String("source_id", src.ID),
					logger.Error(err))
			}
		}
	}

	// Register consumers and monitors only for newly added sources.
	if len(newSourceIDs) > 0 {
		p.registerConsumersForSources(newSourceIDs, sourceModelMap, audioLevelChan, "reconfigure_diff")
		p.registerSoundLevelConsumers(newSourceIDs, "reconfigure_diff")
	}

	// Sync monitors for ALL active sources (kept + new) so UpdateMonitors
	// receives the full desired state and removes stale monitors correctly.
	allActiveIDs := slices.Collect(maps.Values(alreadyRunning))
	allActiveIDs = append(allActiveIDs, newSourceIDs...)
	if len(allActiveIDs) > 0 {
		monitorMap := p.buildMonitorConfigs(sourceModelMap, allActiveIDs)
		if monErr := p.bufferMgr.UpdateMonitors(monitorMap); monErr != nil {
			log.Warn("buffer monitor update failed during reconfigure", logger.Error(monErr))
		}
	}

	log.Info("stream reconfiguration complete",
		logger.Int("kept", keptCount),
		logger.Int("added", len(newSourceIDs)),
		logger.Int("removed", removedCount),
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
	for i := range settings.Realtime.RTSP.Streams {
		stream := &settings.Realtime.RTSP.Streams[i]
		if stream.URL == "" {
			continue
		}
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
		result = append(result, sourceConfigWithModels{
			config: &audiocore.SourceConfig{
				DisplayName:      src.Name,
				Type:             audiocore.SourceTypeAudioCard,
				ConnectionString: src.Device,
				SampleRate:       conf.SampleRate,
				BitDepth:         conf.BitDepth,
				Channels:         1,
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
			spec := infos[i].Spec
			clipLenSec := int(spec.ClipLength.Seconds())
			readSize := spec.SampleRate * clipLenSec * conf.NumChannels * (conf.BitDepth / 8)
			configs[i] = monitorConfig{
				sourceID: sid,
				modelID:  infos[i].ID,
				spec:     spec,
				readSize: readSize,
			}
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

// cleanupHLSWithTimeout runs HLS cleanup asynchronously with a timeout to prevent blocking shutdown
func cleanupHLSWithTimeout(ctx context.Context) {
	// Create a channel to signal completion
	cleanupDone := make(chan error, 1)

	// Run cleanup in a goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				panicErr := fmt.Errorf("panic during HLS cleanup: %v", r)
				// Log panic but don't block shutdown
				GetLogger().Error("panic during HLS cleanup",
					logger.Any("panic", r))
				_ = errors.New(panicErr).
					Component("analysis.audio_pipeline").
					Category(errors.CategorySystem).
					Context("operation", "hls_cleanup_panic").
					Priority(errors.PriorityCritical).
					Build()
				cleanupDone <- panicErr
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
