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

// Default buffer parameters used when allocating analysis and capture buffers.
// These match the values used by the existing BirdNET analysis pipeline.
const (
	// defaultAnalysisCapacity is the ring buffer size in bytes.
	// 288000 bytes = 3 seconds of 16-bit 48 kHz mono audio.
	defaultAnalysisCapacity = 288000

	// defaultAnalysisOverlap is the overlap in bytes between consecutive reads.
	// 144000 bytes = 1.5 seconds of 16-bit 48 kHz mono audio.
	defaultAnalysisOverlap = 144000

	// defaultAnalysisReadSize is the number of fresh bytes per read.
	// 144000 bytes = 1.5 seconds of 16-bit 48 kHz mono audio.
	defaultAnalysisReadSize = 144000

	// defaultCaptureBufferSeconds is the ring buffer capacity in seconds.
	// This determines how much audio history is retained for clip export.
	// Must be large enough to cover the export length + detection window
	// + pre-capture offset. Matches conf.DefaultCaptureBufferSeconds.
	defaultCaptureBufferSeconds = conf.DefaultCaptureBufferSeconds

	// defaultBytesPerSample is the default PCM bytes per sample (16-bit).
	defaultBytesPerSample = 2

	// defaultSampleRate is used when a source config has no sample rate set.
	defaultSampleRate = 48000
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
	// buffers. Set via SetPrimaryModelID before adding sources.
	primaryModelID string
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
	if scheduler != nil {
		e.scheduler.Store(scheduler)
	}
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
}

// SetPrimaryModelID sets the model identifier used when allocating analysis
// buffers. This must be called before AddSource to ensure buffers are keyed
// to the correct model. The value should come from the Orchestrator's
// ModelInfo.ID.
func (e *AudioEngine) SetPrimaryModelID(id string) {
	e.primaryModelID = id
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

	// 2. Allocate analysis buffer.
	if err := e.bufferMgr.AllocateAnalysis(
		sourceID,
		e.primaryModelID,
		defaultAnalysisCapacity,
		defaultAnalysisOverlap,
		defaultAnalysisReadSize,
	); err != nil {
		return errors.New(err).
			Component("audiocore.engine").
			Category(errors.CategoryBuffer).
			Context("operation", "allocate_analysis_buffer").
			Context("source_id", sourceID).
			Build()
	}

	// 3. Allocate capture buffer.
	sampleRate := cfg.SampleRate
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}
	if err := e.bufferMgr.AllocateCapture(
		sourceID,
		e.captureBufferSeconds,
		sampleRate,
		defaultBytesPerSample,
	); err != nil {
		// Roll back analysis buffer on failure.
		e.bufferMgr.DeallocateSource(sourceID)
		return errors.New(err).
			Component("audiocore.engine").
			Category(errors.CategoryBuffer).
			Context("operation", "allocate_capture_buffer").
			Context("source_id", sourceID).
			Build()
	}

	// 4. Start capture based on source type.
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
		logger.String("type", cfg.Type.String()))

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
	src, ok := e.registry.Get(sourceID)
	if !ok {
		return fmt.Errorf("reconfigure source: %w: %s", audiocore.ErrSourceNotFound, sourceID)
	}

	// 1. Stop existing capture.
	if isStreamType(src.Type) {
		_ = e.ffmpegMgr.StopStream(sourceID)
	} else if src.Type == audiocore.SourceTypeAudioCard {
		_ = e.deviceMgr.StopCapture(sourceID)
	}

	// 2. Deallocate old buffers.
	e.bufferMgr.DeallocateSource(sourceID)

	// 3. Allocate new buffers.
	sampleRate := newCfg.SampleRate
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}
	if err := e.bufferMgr.AllocateAnalysis(
		sourceID,
		e.primaryModelID,
		defaultAnalysisCapacity,
		defaultAnalysisOverlap,
		defaultAnalysisReadSize,
	); err != nil {
		return errors.New(err).
			Component("audiocore.engine").
			Category(errors.CategoryBuffer).
			Context("operation", "reallocate_analysis_buffer").
			Context("source_id", sourceID).
			Build()
	}
	if err := e.bufferMgr.AllocateCapture(
		sourceID,
		e.captureBufferSeconds,
		sampleRate,
		defaultBytesPerSample,
	); err != nil {
		e.bufferMgr.DeallocateSource(sourceID)
		return errors.New(err).
			Component("audiocore.engine").
			Category(errors.CategoryBuffer).
			Context("operation", "reallocate_capture_buffer").
			Context("source_id", sourceID).
			Build()
	}

	// 4. Restart capture with new config.
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
			// Source stays registered — mark it as errored so callers can see the failure.
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
			// Source stays registered — mark it as errored so callers can see the failure.
			_ = e.registry.UpdateState(sourceID, audiocore.SourceError)
			return errors.New(err).
				Component("audiocore.engine").
				Category(errors.CategoryAudioSource).
				Context("operation", "restart_device_capture").
				Context("source_id", sourceID).
				Build()
		}
	}

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
