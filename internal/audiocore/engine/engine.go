// Package engine provides the AudioEngine orchestrator that coordinates all
// audio subsystems: source registry, audio router, FFmpeg stream manager,
// device manager, buffer manager, and quiet hours scheduler.
package engine

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/schedule"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// ErrEngineStopped is the cause passed to the engine's context cancellation
// when Stop is called. Shutdown handlers can check for this sentinel to
// distinguish a deliberate stop from other cancellation causes.
var ErrEngineStopped = errors.Newf("AudioEngine: stop requested").
	Component("audiocore").Category(errors.CategoryState).Build()

// isStreamType reports whether the source type uses FFmpeg for capture.
func isStreamType(t audiocore.SourceType) bool {
	switch t {
	case audiocore.SourceTypeRTSP, audiocore.SourceTypeHTTP, audiocore.SourceTypeHLS,
		audiocore.SourceTypeRTMP, audiocore.SourceTypeUDP:
		return true
	default:
		return false
	}
}

// Default buffer parameters used when allocating capture buffers.
const (
	// defaultCaptureBufferSeconds is the ring buffer capacity in seconds.
	// This determines how much audio history is retained for clip export.
	// Must be large enough to cover the export length + detection window
	// + pre-capture offset. Matches conf.DefaultCaptureBufferSeconds.
	defaultCaptureBufferSeconds = conf.DefaultCaptureBufferSeconds

	// defaultBytesPerSample is the default PCM bytes per sample (16-bit).
	defaultBytesPerSample = 2

	// defaultSampleRate is used when a source config has no sample rate set.
	defaultSampleRate = 48000

	// defaultChannels is used when a source config has no channel count set.
	defaultChannels = 1
)

// Config holds the configuration needed to create an AudioEngine.
type Config struct {
	// Logger is the structured logger for engine operations.
	Logger logger.Logger

	// FFmpegPath is the absolute path to the FFmpeg binary.
	// It is passed to StreamConfig when starting stream-type sources.
	FFmpegPath string

	// SoxPath is the absolute path to the SoX binary.
	// Reserved for future use by audio processing subsystems.
	SoxPath string

	// Transport is the default RTSP transport protocol ("tcp" or "udp").
	Transport string

	// FFmpegParameters are additional FFmpeg command-line parameters
	// applied to all stream sources.
	FFmpegParameters []string

	// LogLevel is the FFmpeg log level (e.g., "error", "warning").
	LogLevel string

	// Debug enables verbose debug logging for stream capture.
	Debug bool

	// CaptureBufferSeconds is the ring buffer capacity for audio history.
	// When zero, defaults to conf.DefaultCaptureBufferSeconds (120).
	// Should be set from settings.Realtime.ExtendedCapture.EffectiveCaptureBufferSeconds()
	// to support extended capture mode.
	CaptureBufferSeconds int

	// RouterMetrics is optional; nil-safe.
	// NOTE: Not yet wired to subsystems; metrics plumbing is planned for a future PR.
	RouterMetrics audiocore.RouterMetrics

	// StreamMetrics is optional; nil-safe.
	// NOTE: Not yet wired to subsystems; metrics plumbing is planned for a future PR.
	StreamMetrics audiocore.StreamMetrics

	// BufferMetrics is optional; nil-safe.
	// NOTE: Not yet wired to subsystems; metrics plumbing is planned for a future PR.
	BufferMetrics audiocore.BufferMetrics

	// DeviceMetrics is optional; nil-safe.
	// NOTE: Not yet wired to subsystems; metrics plumbing is planned for a future PR.
	DeviceMetrics audiocore.DeviceMetrics
}

// captureBufferSecs returns v if positive, otherwise the default capture buffer size.
func captureBufferSecs(v int) int {
	if v > 0 {
		return v
	}
	return defaultCaptureBufferSeconds
}

// AudioEngine coordinates all audio subsystems: source registry, audio router,
// FFmpeg stream manager, device manager, buffer manager, and quiet hours
// scheduler. It provides a single point of control for adding, removing, and
// reconfiguring audio sources.
type AudioEngine struct {
	registry  *audiocore.SourceRegistry
	router    *audiocore.AudioRouter
	ffmpegMgr *ffmpeg.Manager
	deviceMgr *audiocore.DeviceManager
	bufferMgr *buffer.Manager
	scheduler atomic.Pointer[schedule.QuietHoursScheduler]
	logger    logger.Logger
	ctx       context.Context
	cancel    context.CancelCauseFunc

	// primaryModelID is the model identifier used when allocating analysis
	// buffers. Set via SetPrimaryModel before adding sources.
	primaryModelID string
	// primaryClipBytes, primaryOverlapBytes, primaryReadSize are the analysis
	// buffer dimensions derived from the primary model's spec. Set via
	// SetPrimaryModel before adding sources.
	primaryClipBytes    int
	primaryOverlapBytes int
	primaryReadSize     int
	// ffmpegPath is the absolute path to the FFmpeg binary.
	ffmpegPath string
	// soxPath is the absolute path to the SoX binary.
	soxPath string
	// transport is the default RTSP transport protocol.
	transport string
	// ffmpegParameters are additional FFmpeg command-line parameters.
	ffmpegParameters []string
	// logLevel is the FFmpeg log level.
	logLevel string
	// debug enables verbose debug logging for stream capture.
	debug bool
	// captureBufferSeconds is the ring buffer capacity for audio history.
	captureBufferSeconds int
}

// New creates an AudioEngine with all subsystems initialised.
// The provided context controls the engine's lifetime; cancelling it stops
// all subsystems. The scheduler parameter may be nil if quiet hours are not
// configured.
func New(ctx context.Context, cfg *Config, scheduler *schedule.QuietHoursScheduler) *AudioEngine {
	log := cfg.Logger
	if log == nil {
		log = audiocore.GetLogger()
	}

	engineCtx, cancel := context.WithCancelCause(ctx)

	bufMgr := buffer.NewManager(log)
	router := audiocore.NewAudioRouter(log, bufMgr)
	// Share bufMgr with both capture paths so stdout reads (ffmpeg) and
	// convert-on-capture (malgo) go through pooled byte slices instead of
	// allocating per-frame. The router Retain/Release path keeps pooled
	// buffers alive across fan-out subscribers.
	ffmpegMgr := ffmpeg.NewManager(engineCtx, func(frame audiocore.AudioFrame) {
		router.Dispatch(frame)
	}, nil, log, bufMgr)
	deviceMgr := audiocore.NewDeviceManager(router, bufMgr, log)

	// Probe all device capabilities at startup, before any capture begins.
	// Exclusive mode probing requires sole device access, so it must happen
	// before sources are added and capture starts.
	audiocore.ProbeAllDeviceCapabilities(log)

	e := &AudioEngine{
		registry:             audiocore.NewSourceRegistry(log),
		router:               router,
		ffmpegMgr:            ffmpegMgr,
		deviceMgr:            deviceMgr,
		bufferMgr:            bufMgr,
		logger:               log.With(logger.String("component", "audio_engine")),
		ctx:                  engineCtx,
		cancel:               cancel,
		ffmpegPath:           cfg.FFmpegPath,
		soxPath:              cfg.SoxPath,
		transport:            cfg.Transport,
		ffmpegParameters:     cfg.FFmpegParameters,
		logLevel:             cfg.LogLevel,
		debug:                cfg.Debug,
		captureBufferSeconds: captureBufferSecs(cfg.CaptureBufferSeconds),
	}
	e.SetScheduler(scheduler)
	return e
}

// Registry returns the source registry.
func (e *AudioEngine) Registry() *audiocore.SourceRegistry {
	return e.registry
}

// Router returns the audio router.
func (e *AudioEngine) Router() *audiocore.AudioRouter {
	return e.router
}

// BufferManager returns the buffer manager.
func (e *AudioEngine) BufferManager() *buffer.Manager {
	return e.bufferMgr
}

// FFmpegManager returns the FFmpeg stream manager.
func (e *AudioEngine) FFmpegManager() *ffmpeg.Manager {
	return e.ffmpegMgr
}

// DeviceManager returns the device manager.
func (e *AudioEngine) DeviceManager() *audiocore.DeviceManager {
	return e.deviceMgr
}

// Scheduler returns the quiet hours scheduler, which may be nil.
func (e *AudioEngine) Scheduler() *schedule.QuietHoursScheduler {
	return e.scheduler.Load()
}

// SetScheduler replaces the engine's quiet hours scheduler.
// This supports deferred initialization when the scheduler depends on
// resources (SunCalc, ControlChan) only available after service startup.
func (e *AudioEngine) SetScheduler(s *schedule.QuietHoursScheduler) {
	e.scheduler.Store(s)
	if s != nil {
		s.SetStreamManager(func() schedule.StreamManager { return e })
	}
}

// GetActiveStreamIDs returns the runtime source IDs currently tracked by FFmpeg.
func (e *AudioEngine) GetActiveStreamIDs() []string {
	return e.ffmpegMgr.GetActiveStreamIDs()
}

// GetActiveStreamURLs returns active runtime sourceID -> raw stream URL.
func (e *AudioEngine) GetActiveStreamURLs() map[string]string {
	ids := e.ffmpegMgr.GetActiveStreamIDs()
	urls := make(map[string]string, len(ids))
	for _, sourceID := range ids {
		if url, ok := e.registry.ConnectionStringByID(sourceID); ok {
			urls[sourceID] = url
		}
	}
	return urls
}

// StopStream stops the FFmpeg stream for sourceID while keeping the registered
// source, routes, and buffers available for a later quiet-hours restart.
func (e *AudioEngine) StopStream(sourceID string) error {
	if err := e.ffmpegMgr.StopStream(sourceID); err != nil {
		return err
	}
	_ = e.registry.UpdateState(sourceID, audiocore.SourceStopped)
	return nil
}

// StartStream restarts a quiet-hours-suppressed FFmpeg stream under its
// existing runtime sourceID.
func (e *AudioEngine) StartStream(sourceID, url, transport string) error {
	src, ok := e.registry.Get(sourceID)
	if !ok {
		return fmt.Errorf("restart stream: %w: %s", audiocore.ErrSourceNotFound, sourceID)
	}
	if transport == "" {
		transport = e.transport
	}
	sampleRate := src.SampleRate
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}
	channels := src.Channels
	if channels <= 0 {
		channels = defaultChannels
	}
	streamCfg := &ffmpeg.StreamConfig{
		SourceID:         sourceID,
		SourceName:       src.DisplayName,
		URL:              url,
		Type:             string(src.Type),
		SampleRate:       sampleRate,
		BitDepth:         src.BitDepth,
		Channels:         channels,
		FFmpegPath:       e.ffmpegPath,
		Transport:        transport,
		FFmpegParameters: e.ffmpegParameters,
		LogLevel:         e.logLevel,
		Debug:            e.debug,
	}
	if err := e.ffmpegMgr.StartStream(streamCfg); err != nil {
		_ = e.registry.UpdateState(sourceID, audiocore.SourceError)
		return err
	}
	_ = e.registry.UpdateState(sourceID, audiocore.SourceRunning)
	return nil
}

// SetPrimaryModel sets the model identifier and analysis buffer dimensions
// for the primary model. This must be called before AddSource to ensure
// buffers are allocated with the correct model key and size.
// clipBytes, overlapBytes, and readSize should be derived from the model's
// ModelSpec.BufferDimensions(), matching the secondary model allocation path.
func (e *AudioEngine) SetPrimaryModel(id string, clipBytes, overlapBytes, readSize int) {
	e.primaryModelID = id
	e.primaryClipBytes = clipBytes
	e.primaryOverlapBytes = overlapBytes
	e.primaryReadSize = readSize
	e.logger.Info("primary model buffer dimensions set",
		logger.String("model_id", id),
		logger.Int("clip_bytes", clipBytes),
		logger.Int("overlap_bytes", overlapBytes),
		logger.Int("read_size", readSize))
}

// PrimaryModelID returns the current primary model identifier.
func (e *AudioEngine) PrimaryModelID() string {
	return e.primaryModelID
}

// AddSource registers a new audio source and allocates its buffers.
// For stream-type sources (RTSP, HTTP, HLS, RTMP, UDP), the FFmpeg manager
// is started. For audio card sources, the device manager begins capture.
// File-type sources are registered but no long-running capture is started.
func (e *AudioEngine) AddSource(cfg *audiocore.SourceConfig) error {
	if e.primaryModelID == "" {
		return errors.Newf("SetPrimaryModel must be called before AddSource").
			Component("audiocore.engine").
			Category(errors.CategoryState).
			Context("source_id", cfg.ID).
			Build()
	}

	// 1. Register the source.
	src, err := e.registry.Register(cfg)
	if err != nil {
		return errors.New(err).
			Component("audiocore.engine").
			Category(errors.CategoryAudioSource).
			Context("operation", "register_source").
			Context("source_id", cfg.ID).
			Build()
	}

	sourceID := src.ID

	// 2. Allocate analysis buffer using the primary model's native dimensions.
	// BufferConsumer resamples audio to the model's target rate before writing,
	// so buffer size must match the model spec, not the source sample rate.
	if err := e.bufferMgr.AllocateAnalysis(
		sourceID,
		e.primaryModelID,
		e.primaryClipBytes,
		e.primaryOverlapBytes,
		e.primaryReadSize,
	); err != nil {
		_ = e.registry.Unregister(sourceID)
		return errors.New(err).
			Component("audiocore.engine").
			Category(errors.CategoryBuffer).
			Context("operation", "allocate_analysis_buffer").
			Context("source_id", sourceID).
			Build()
	}

	// 3. Default and validate sample rate for capture buffer and stream config.
	sampleRate := cfg.SampleRate
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}

	e.logger.Info("allocated primary analysis buffer",
		logger.String("source_id", sourceID),
		logger.String("model_id", e.primaryModelID),
		logger.Int("clip_bytes", e.primaryClipBytes),
		logger.Int("overlap_bytes", e.primaryOverlapBytes),
		logger.Int("read_size", e.primaryReadSize),
		logger.Int("source_sample_rate", sampleRate))

	// 4. Allocate capture buffer.
	if err := e.bufferMgr.AllocateCapture(
		sourceID,
		e.captureBufferSeconds,
		sampleRate,
		defaultBytesPerSample,
	); err != nil {
		e.bufferMgr.DeallocateSource(sourceID)
		_ = e.registry.Unregister(sourceID)
		return errors.New(err).
			Component("audiocore.engine").
			Category(errors.CategoryBuffer).
			Context("operation", "allocate_capture_buffer").
			Context("source_id", sourceID).
			Build()
	}

	// 5. Start capture based on source type.
	if isStreamType(cfg.Type) {
		streamCfg := &ffmpeg.StreamConfig{
			SourceID:         sourceID,
			SourceName:       src.DisplayName,
			URL:              cfg.ConnectionString,
			Type:             string(cfg.Type),
			SampleRate:       sampleRate,
			BitDepth:         cfg.BitDepth,
			Channels:         cfg.Channels,
			FFmpegPath:       e.ffmpegPath,
			Transport:        e.transport,
			FFmpegParameters: e.ffmpegParameters,
			LogLevel:         e.logLevel,
			Debug:            e.debug,
		}
		if err := e.ffmpegMgr.StartStream(streamCfg); err != nil {
			e.bufferMgr.DeallocateSource(sourceID)
			_ = e.registry.Unregister(sourceID)
			return errors.New(err).
				Component("audiocore.engine").
				Category(errors.CategoryRTSP).
				Context("operation", "start_stream").
				Context("source_id", sourceID).
				Build()
		}
	} else if cfg.Type == audiocore.SourceTypeAudioCard {
		devCfg := audiocore.DeviceConfig{
			SampleRate: sampleRate,
			BitDepth:   cfg.BitDepth,
			Channels:   cfg.Channels,
		}
		e.logger.Info("starting audio card capture",
			logger.String("source_id", sourceID),
			logger.String("device_id", cfg.ConnectionString),
			logger.Int("sample_rate", sampleRate),
			logger.Int("bit_depth", cfg.BitDepth),
			logger.Int("channels", cfg.Channels))
		if err := e.deviceMgr.StartCapture(sourceID, cfg.ConnectionString, devCfg); err != nil {
			e.logger.Error("audio card capture failed",
				logger.String("source_id", sourceID),
				logger.String("device_id", cfg.ConnectionString),
				logger.Error(err))
			e.bufferMgr.DeallocateSource(sourceID)
			_ = e.registry.Unregister(sourceID)
			return errors.New(err).
				Component("audiocore.engine").
				Category(errors.CategoryAudioSource).
				Context("operation", "start_device_capture").
				Context("source_id", sourceID).
				Build()
		}
	}
	// File-type sources: registered + buffers allocated, but no long-running capture.

	e.logger.Info("source added",
		logger.String("source_id", sourceID),
		logger.String("type", cfg.Type.String()),
		logger.Int("sample_rate", sampleRate),
		logger.String("primary_model", e.primaryModelID),
		logger.Int("analysis_clip_bytes", e.primaryClipBytes))

	return nil
}

// RemoveSource stops capture, removes all routes, deallocates buffers, and
// unregisters the source identified by sourceID.
func (e *AudioEngine) RemoveSource(sourceID string) error {
	src, ok := e.registry.Get(sourceID)
	if !ok {
		return fmt.Errorf("remove source: %w: %s", audiocore.ErrSourceNotFound, sourceID)
	}

	// 1. Stop capture.
	if isStreamType(src.Type) {
		if err := e.ffmpegMgr.StopStream(sourceID); err != nil {
			e.logger.Warn("failed to stop stream during removal",
				logger.String("source_id", sourceID),
				logger.Error(err))
		}
	} else if src.Type == audiocore.SourceTypeAudioCard {
		if err := e.deviceMgr.StopCapture(sourceID); err != nil {
			e.logger.Warn("failed to stop capture during removal",
				logger.String("source_id", sourceID),
				logger.Error(err))
		}
	}

	// 2. Remove all routes for this source.
	e.router.RemoveAllRoutes(sourceID)

	// 3. Deallocate buffers.
	e.bufferMgr.DeallocateSource(sourceID)

	// 4. Unregister.
	if err := e.registry.Unregister(sourceID); err != nil {
		return fmt.Errorf("unregister source %s: %w", sourceID, err)
	}

	e.logger.Info("source removed",
		logger.String("source_id", sourceID))

	return nil
}

// ReconfigureSource stops the existing capture for sourceID, reallocates
// buffers with the new configuration, and restarts capture.
func (e *AudioEngine) ReconfigureSource(sourceID string, newCfg *audiocore.SourceConfig) error {
	if e.primaryModelID == "" {
		return errors.Newf("SetPrimaryModel must be called before ReconfigureSource").
			Component("audiocore.engine").
			Category(errors.CategoryState).
			Context("source_id", sourceID).
			Build()
	}

	src, ok := e.registry.Get(sourceID)
	if !ok {
		return fmt.Errorf("reconfigure source: %w: %s", audiocore.ErrSourceNotFound, sourceID)
	}

	e.logger.Info("reconfiguring audio source",
		logger.String("source_id", sourceID),
		logger.String("device", newCfg.ConnectionString),
		logger.Int("sample_rate", newCfg.SampleRate),
		logger.Int("bit_depth", newCfg.BitDepth),
		logger.Int("channels", newCfg.Channels))

	// 1. Stop existing capture.
	if isStreamType(src.Type) {
		_ = e.ffmpegMgr.StopStream(sourceID)
	} else if src.Type == audiocore.SourceTypeAudioCard {
		_ = e.deviceMgr.StopCapture(sourceID)
	}

	// 2. Remove all routes so consumers don't reference deallocated buffers.
	e.router.RemoveAllRoutes(sourceID)

	// 3. Deallocate old buffers.
	e.bufferMgr.DeallocateSource(sourceID)

	// 4. Allocate new analysis buffer using the primary model's native dimensions.
	sampleRate := newCfg.SampleRate
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}
	if err := e.bufferMgr.AllocateAnalysis(
		sourceID,
		e.primaryModelID,
		e.primaryClipBytes,
		e.primaryOverlapBytes,
		e.primaryReadSize,
	); err != nil {
		_ = e.registry.UpdateState(sourceID, audiocore.SourceError)
		return errors.New(err).
			Component("audiocore.engine").
			Category(errors.CategoryBuffer).
			Context("operation", "reallocate_analysis_buffer").
			Context("source_id", sourceID).
			Build()
	}
	e.logger.Info("reallocated primary analysis buffer",
		logger.String("source_id", sourceID),
		logger.String("model_id", e.primaryModelID),
		logger.Int("clip_bytes", e.primaryClipBytes),
		logger.Int("overlap_bytes", e.primaryOverlapBytes),
		logger.Int("read_size", e.primaryReadSize),
		logger.Int("source_sample_rate", sampleRate))
	if err := e.bufferMgr.AllocateCapture(
		sourceID,
		e.captureBufferSeconds,
		sampleRate,
		defaultBytesPerSample,
	); err != nil {
		e.bufferMgr.DeallocateSource(sourceID)
		_ = e.registry.UpdateState(sourceID, audiocore.SourceError)
		return errors.New(err).
			Component("audiocore.engine").
			Category(errors.CategoryBuffer).
			Context("operation", "reallocate_capture_buffer").
			Context("source_id", sourceID).
			Build()
	}

	// 5. Restart capture with new config.
	newType := newCfg.Type
	if newType == "" || newType == audiocore.SourceTypeUnknown {
		newType = src.Type
	}

	if isStreamType(newType) {
		streamCfg := &ffmpeg.StreamConfig{
			SourceID:         sourceID,
			SourceName:       src.DisplayName,
			URL:              newCfg.ConnectionString,
			Type:             string(newType),
			SampleRate:       sampleRate,
			BitDepth:         newCfg.BitDepth,
			Channels:         newCfg.Channels,
			FFmpegPath:       e.ffmpegPath,
			Transport:        e.transport,
			FFmpegParameters: e.ffmpegParameters,
			LogLevel:         e.logLevel,
			Debug:            e.debug,
		}
		if err := e.ffmpegMgr.StartStream(streamCfg); err != nil {
			// Source stays registered; mark it as errored so callers can see the failure.
			_ = e.registry.UpdateState(sourceID, audiocore.SourceError)
			return errors.New(err).
				Component("audiocore.engine").
				Category(errors.CategoryRTSP).
				Context("operation", "restart_stream").
				Context("source_id", sourceID).
				Build()
		}
	} else if newType == audiocore.SourceTypeAudioCard {
		devCfg := audiocore.DeviceConfig{
			SampleRate: sampleRate,
			BitDepth:   newCfg.BitDepth,
			Channels:   newCfg.Channels,
		}
		if err := e.deviceMgr.StartCapture(sourceID, newCfg.ConnectionString, devCfg); err != nil {
			// Source stays registered; mark it as errored so callers can see the failure.
			_ = e.registry.UpdateState(sourceID, audiocore.SourceError)
			return errors.New(err).
				Component("audiocore.engine").
				Category(errors.CategoryAudioSource).
				Context("operation", "restart_device_capture").
				Context("source_id", sourceID).
				Build()
		}
	}

	// 6. Update registry so downstream consumers see the new audio params.
	e.registry.UpdateAudioParams(sourceID, sampleRate, newCfg.BitDepth, newCfg.Channels)

	e.logger.Info("source reconfigured",
		logger.String("source_id", sourceID),
		logger.String("type", newType.String()))

	return nil
}

// Stop gracefully shuts down all subsystems: FFmpeg manager, device manager,
// audio router, and quiet hours scheduler. It should be called once when the
// application is shutting down.
func (e *AudioEngine) Stop() {
	e.cancel(ErrEngineStopped)

	// Shut down FFmpeg streams.
	if err := e.ffmpegMgr.Shutdown(); err != nil {
		e.logger.Warn("ffmpeg manager shutdown error", logger.Error(err))
	}

	// Stop all device captures.
	if err := e.deviceMgr.Close(); err != nil {
		e.logger.Warn("device manager close error", logger.Error(err))
	}

	// Close the router (stops all drainer goroutines).
	e.router.Close()

	// Stop the scheduler if present.
	if sched := e.scheduler.Load(); sched != nil {
		sched.Stop()
	}

	e.logger.Info("audio engine stopped")
}
