// internal/audiocore/schedule/quiet_hours.go

// Package schedule provides time-based scheduling utilities for audio capture.
// This file implements the quiet hours scheduler that stops and starts
// audio streams and sound card capture during configured time windows.
package schedule

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// evaluationInterval is how often the scheduler checks quiet hours state.
const evaluationInterval = 1 * time.Minute

// Control signal constants for quiet hours.
const (
	// SignalReconfigureQuietHours signals that quiet hours settings have changed
	// and the scheduler should re-evaluate immediately.
	SignalReconfigureQuietHours = "reconfigure_quiet_hours"

	// SignalQuietHoursStopSoundCard signals that the sound card should be stopped
	// because quiet hours have started.
	SignalQuietHoursStopSoundCard = "quiet_hours_stop_soundcard"

	// SignalQuietHoursStartSoundCard signals that the sound card should be started
	// because quiet hours have ended.
	SignalQuietHoursStartSoundCard = "quiet_hours_start_soundcard"
)

// streamManager is the interface used by the scheduler to stop/start streams.
// Implemented by a wrapper around ffmpeg.Manager; can be replaced with a mock in tests.
//
// sourceID is the unique identifier for the stream (typically the stream URL).
// url is the stream URL. transport is the RTSP transport protocol (e.g., "tcp").
type streamManager interface {
	// GetActiveStreamIDs returns the sourceIDs of all currently active streams.
	GetActiveStreamIDs() []string

	// StopStream stops the stream identified by sourceID.
	StopStream(sourceID string) error

	// StartStream starts a stream with the given sourceID, URL, and transport.
	StartStream(sourceID, url, transport string) error
}

// QuietHoursConfig holds the dependencies needed by QuietHoursScheduler.
type QuietHoursConfig struct {
	// SunCalc provides solar event time calculations. May be nil if solar mode
	// is not needed; solar quiet hours will be skipped when nil.
	SunCalc *suncalc.SunCalc

	// ControlChan is used to send sound card stop/start signals to the control monitor.
	// Must not be nil.
	ControlChan chan string

	// Logger is used for structured logging. If nil, the global audiocore logger is used.
	Logger logger.Logger
}

// streamAction represents a deferred stream start/stop to execute outside the mutex.
type streamAction struct {
	sourceID  string
	url       string
	name      string
	transport string
	stop      bool // true = stop, false = start
}

// QuietHoursScheduler periodically evaluates quiet hours configurations
// and stops/starts audio streams accordingly to reduce CPU usage.
type QuietHoursScheduler struct {
	ctx    context.Context
	cancel context.CancelFunc

	sunCalc *suncalc.SunCalc

	// Control channel for sound card stop/start signals.
	controlChan chan string

	// logger is used for structured logging.
	log logger.Logger

	// getManager returns the stream manager used by Evaluate.
	// Overridden in tests to inject a mock.
	getManager func() streamManager

	// Track which streams are currently suppressed by quiet hours.
	// Key is the stream sourceID (URL), value is true if currently suppressed.
	suppressed map[string]bool
	mu         sync.Mutex

	// Sound card suppression state.
	soundCardSuppressed bool

	// stopped is set to 1 when Stop() is called. Evaluate() checks this
	// before sending on controlChan to avoid sending on a closed channel
	// during shutdown.
	stopped atomic.Int32
}

// NewQuietHoursScheduler creates a new scheduler instance.
//
// cfg.ControlChan must not be nil; cfg.SunCalc may be nil if solar mode is
// not configured. If cfg.Logger is nil the scheduler logs to io.Discard.
func NewQuietHoursScheduler(cfg QuietHoursConfig) *QuietHoursScheduler {
	log := cfg.Logger
	if log == nil {
		log = logger.Global().Module("audiocore").Module("schedule")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &QuietHoursScheduler{
		ctx:         ctx,
		cancel:      cancel,
		sunCalc:     cfg.SunCalc,
		controlChan: cfg.ControlChan,
		log:         log,
		suppressed:  make(map[string]bool),
	}
}

// SetStreamManager overrides the stream manager used by Evaluate.
// Intended for testing; call before Start.
func (s *QuietHoursScheduler) SetStreamManager(fn func() streamManager) {
	s.getManager = fn
}

// Start begins the quiet hours evaluation loop with a 1-minute tick interval.
func (s *QuietHoursScheduler) Start() {
	go s.run()
}

// Stop cancels the scheduler context and stops the evaluation loop.
// Stop marks the scheduler as stopped and cancels the context to exit the
// evaluation loop. The stopped flag prevents Evaluate() from sending on
// controlChan after it has been closed during shutdown.
func (s *QuietHoursScheduler) Stop() {
	s.stopped.Store(1)
	s.cancel()
}

// run is the main scheduler loop.
func (s *QuietHoursScheduler) run() {
	defer func() {
		if r := recover(); r != nil {
			panicErr := fmt.Errorf("panic in quiet hours scheduler: %v", r)
			s.log.Error("panic in quiet hours scheduler",
				logger.Any("panic", r))
			_ = errors.New(panicErr).
				Component("audiocore.schedule").
				Category(errors.CategorySystem).
				Context("operation", "quiet_hours_scheduler_panic").
				Priority(errors.PriorityCritical).
				Build()
		}
	}()
	ticker := time.NewTicker(evaluationInterval)
	defer ticker.Stop()

	// Perform initial evaluation immediately.
	s.Evaluate()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.Evaluate()
		}
	}
}

// Evaluate checks all configured streams and the sound card against their
// quiet hours settings and stops/starts them as needed.
//
// Evaluate reads the global settings on each call so that hot-reload changes
// are respected without restarting the scheduler.
func (s *QuietHoursScheduler) Evaluate() {
	settings := conf.GetSettings()
	if settings == nil {
		return
	}
	now := time.Now()

	if s.getManager == nil {
		return
	}
	manager := s.getManager()
	if manager == nil {
		return
	}

	activeStreams := make(map[string]bool)
	for _, id := range manager.GetActiveStreamIDs() {
		activeStreams[id] = true
	}

	// Phase 1: Determine actions under lock (no external calls).
	var actions []streamAction
	var soundCardSignal string // "" = no action

	s.mu.Lock()

	for i := range settings.Realtime.RTSP.Streams {
		stream := &settings.Realtime.RTSP.Streams[i]
		// Use URL as the sourceID (mirrors the old behaviour).
		sourceID := stream.URL

		if !stream.QuietHours.Enabled {
			if s.suppressed[sourceID] {
				actions = append(actions, streamAction{
					sourceID:  sourceID,
					url:       stream.URL,
					name:      stream.Name,
					transport: stream.Transport,
					stop:      false,
				})
			}
			continue
		}

		inQuietHours := s.isInQuietHours(&stream.QuietHours, now)

		if inQuietHours && activeStreams[sourceID] && !s.suppressed[sourceID] {
			actions = append(actions, streamAction{
				sourceID: sourceID,
				url:      stream.URL,
				name:     stream.Name,
				stop:     true,
			})
		} else if !inQuietHours && s.suppressed[sourceID] {
			actions = append(actions, streamAction{
				sourceID:  sourceID,
				url:       stream.URL,
				name:      stream.Name,
				transport: stream.Transport,
				stop:      false,
			})
		}
	}

	// Clean up suppressed entries for streams no longer in config.
	configuredIDs := make(map[string]bool, len(settings.Realtime.RTSP.Streams))
	for i := range settings.Realtime.RTSP.Streams {
		configuredIDs[settings.Realtime.RTSP.Streams[i].URL] = true
	}
	for id := range s.suppressed {
		if !configuredIDs[id] {
			delete(s.suppressed, id)
		}
	}

	// Determine sound card action.
	if settings.Realtime.Audio.Source != "" && settings.Realtime.Audio.QuietHours.Enabled {
		inQuietHours := s.isInQuietHours(&settings.Realtime.Audio.QuietHours, now)
		if inQuietHours && !s.soundCardSuppressed {
			soundCardSignal = SignalQuietHoursStopSoundCard
		} else if !inQuietHours && s.soundCardSuppressed {
			soundCardSignal = SignalQuietHoursStartSoundCard
		}
	} else if s.soundCardSuppressed {
		soundCardSignal = SignalQuietHoursStartSoundCard
	}

	s.mu.Unlock()

	// Phase 2: Execute actions outside the mutex.
	log := s.getLog()

	for _, action := range actions {
		if action.stop {
			log.Info("Entering quiet hours, stopping stream",
				logger.String("stream", action.name),
				logger.String("url", privacy.SanitizeStreamUrl(action.url)))
			if err := manager.StopStream(action.sourceID); err != nil {
				log.Error("Failed to stop stream for quiet hours",
					logger.String("stream", action.name),
					logger.Error(err))
			} else {
				s.mu.Lock()
				s.suppressed[action.sourceID] = true
				s.mu.Unlock()
			}
		} else {
			log.Info("Exiting quiet hours, restarting stream",
				logger.String("stream", action.name),
				logger.String("url", privacy.SanitizeStreamUrl(action.url)))
			if err := manager.StartStream(action.sourceID, action.url, action.transport); err != nil {
				log.Error("Failed to restart stream after quiet hours",
					logger.String("stream", action.name),
					logger.Error(err))
			} else {
				s.mu.Lock()
				delete(s.suppressed, action.sourceID)
				s.mu.Unlock()
			}
		}
	}

	// Execute sound card signal if needed.
	if soundCardSignal != "" && s.stopped.Load() == 0 {
		log.Info("Quiet hours sound card action", logger.String("signal", soundCardSignal))
		s.trySendSoundCardSignal(soundCardSignal, log)
	}
}

// trySendSoundCardSignal attempts to send a sound card signal on controlChan.
// The recover guard is defense-in-depth against a theoretical TOCTOU race:
// Evaluate() could pass the stopped check, then Stop() + close(controlChan)
// execute before the select runs. The window is nanoseconds but the panic is fatal.
func (s *QuietHoursScheduler) trySendSoundCardSignal(signal string, log logger.Logger) {
	defer func() {
		if r := recover(); r != nil {
			log.Warn("Recovered from send on closed controlChan during shutdown",
				logger.String("signal", signal))
		}
	}()
	select {
	case s.controlChan <- signal:
		s.mu.Lock()
		s.soundCardSuppressed = (signal == SignalQuietHoursStopSoundCard)
		s.mu.Unlock()
	default:
		log.Warn("Control channel full, could not send sound card signal",
			logger.String("signal", signal))
	}
}

// getLog returns the scheduler's logger, falling back to the global audiocore logger if nil.
// This protects tests that construct QuietHoursScheduler directly without a logger.
func (s *QuietHoursScheduler) getLog() logger.Logger {
	if s.log != nil {
		return s.log
	}
	return logger.Global().Module("audiocore").Module("schedule")
}

// isInQuietHours determines whether the given time falls within the quiet hours window.
func (s *QuietHoursScheduler) isInQuietHours(qh *conf.QuietHoursConfig, now time.Time) bool {
	if !qh.Enabled {
		return false
	}

	var startTime, endTime time.Time

	switch qh.Mode {
	case conf.QuietHoursModeFixed:
		var err error
		startTime, err = parseHHMM(qh.StartTime, now)
		if err != nil {
			s.getLog().Warn("Invalid quiet hours start time",
				logger.String("start_time", qh.StartTime),
				logger.Error(err))
			return false
		}
		endTime, err = parseHHMM(qh.EndTime, now)
		if err != nil {
			s.getLog().Warn("Invalid quiet hours end time",
				logger.String("end_time", qh.EndTime),
				logger.Error(err))
			return false
		}

	case conf.QuietHoursModeSolar:
		if s.sunCalc == nil {
			s.getLog().Warn("Solar quiet hours configured but sun calculator not available")
			return false
		}
		sunTimes, err := s.sunCalc.GetSunEventTimes(now)
		if err != nil {
			s.getLog().Warn("Failed to calculate sun event times for quiet hours",
				logger.Error(err))
			return false
		}
		startTime = getSolarEventTime(&sunTimes, qh.StartEvent).Add(time.Duration(qh.StartOffset) * time.Minute)
		endTime = getSolarEventTime(&sunTimes, qh.EndEvent).Add(time.Duration(qh.EndOffset) * time.Minute)

	default:
		return false
	}

	return isTimeInWindow(now, startTime, endTime)
}

// parseHHMM parses a "HH:MM" string and returns a time.Time on the same date
// as reference. Returns an error if the string is not valid HH:MM format.
func parseHHMM(hhmm string, reference time.Time) (time.Time, error) {
	parsed, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time format %q: expected HH:MM: %w", hhmm, err)
	}
	return time.Date(
		reference.Year(), reference.Month(), reference.Day(),
		parsed.Hour(), parsed.Minute(), 0, 0,
		reference.Location(),
	), nil
}

// getSolarEventTime returns the appropriate time from SunEventTimes based on event name.
func getSolarEventTime(sunTimes *suncalc.SunEventTimes, event string) time.Time {
	switch event {
	case conf.SolarEventSunrise:
		return sunTimes.Sunrise
	case conf.SolarEventSunset:
		return sunTimes.Sunset
	default:
		return time.Time{}
	}
}

// isTimeInWindow checks if now falls within the time window from start to end.
// Handles overnight windows where start > end (e.g., 22:00 to 06:00).
func isTimeInWindow(now, start, end time.Time) bool {
	// Normalize to just hour:minute for comparison on the same day.
	nowMinutes := now.Hour()*60 + now.Minute()
	startMinutes := start.Hour()*60 + start.Minute()
	endMinutes := end.Hour()*60 + end.Minute()

	if startMinutes <= endMinutes {
		// Same-day window (e.g., 02:00 to 06:00).
		return nowMinutes >= startMinutes && nowMinutes < endMinutes
	}
	// Overnight window (e.g., 22:00 to 06:00).
	return nowMinutes >= startMinutes || nowMinutes < endMinutes
}

// IsSoundCardSuppressed returns whether the sound card is currently suppressed by quiet hours.
func (s *QuietHoursScheduler) IsSoundCardSuppressed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.soundCardSuppressed
}

// GetSuppressedStreams returns a map of stream sourceIDs to their suppression state.
// Only streams currently suppressed by quiet hours are included.
func (s *QuietHoursScheduler) GetSuppressedStreams() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]bool, len(s.suppressed))
	for id, suppressed := range s.suppressed {
		if suppressed {
			result[id] = true
		}
	}
	return result
}
