// internal/myaudio/quiet_hours.go

// Package myaudio provides audio capture and processing utilities.
// This file implements the quiet hours scheduler that stops and starts
// audio streams and sound card capture during configured time windows.
package myaudio

import (
	"context"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// evaluationInterval is how often the scheduler checks quiet hours state.
const evaluationInterval = 1 * time.Minute

// QuietHoursScheduler periodically evaluates quiet hours configurations
// and stops/starts audio streams accordingly to reduce CPU usage.
type QuietHoursScheduler struct {
	ctx    context.Context
	cancel context.CancelFunc

	sunCalc   *suncalc.SunCalc
	audioChan chan UnifiedAudioData

	// Control channel for sound card stop/start signals
	controlChan chan string

	// Track which streams are currently suppressed by quiet hours.
	// Key is the stream URL, value is true if currently suppressed.
	suppressed map[string]bool
	mu         sync.Mutex

	// Sound card suppression state
	soundCardSuppressed bool
}

// NewQuietHoursScheduler creates a new scheduler instance.
// sunCalc may be nil if solar mode is not needed.
// controlChan is used to send sound card stop/start signals to the control monitor.
func NewQuietHoursScheduler(sunCalc *suncalc.SunCalc, audioChan chan UnifiedAudioData, controlChan chan string) *QuietHoursScheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &QuietHoursScheduler{
		ctx:         ctx,
		cancel:      cancel,
		sunCalc:     sunCalc,
		audioChan:   audioChan,
		controlChan: controlChan,
		suppressed:  make(map[string]bool),
	}
}

// SetAudioChannel updates the audio channel used when restarting streams.
// Must be called before Start, and again whenever the unified audio channel is recreated
// (e.g., during stream reconfiguration).
func (s *QuietHoursScheduler) SetAudioChannel(ch chan UnifiedAudioData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audioChan = ch
}

// Start begins the quiet hours evaluation loop with a 1-minute tick interval.
func (s *QuietHoursScheduler) Start() {
	go s.run()
}

// Stop cancels the scheduler context and stops the evaluation loop.
func (s *QuietHoursScheduler) Stop() {
	s.cancel()
}

// run is the main scheduler loop.
func (s *QuietHoursScheduler) run() {
	ticker := time.NewTicker(evaluationInterval)
	defer ticker.Stop()

	// Perform initial evaluation immediately
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

// streamAction represents a deferred stream start/stop to execute outside the mutex.
type streamAction struct {
	url       string
	name      string
	transport string
	stop      bool // true = stop, false = start
}

// Evaluate checks all configured streams and the sound card against their
// quiet hours settings and stops/starts them as needed.
func (s *QuietHoursScheduler) Evaluate() {
	settings := conf.Setting()
	now := time.Now()
	log := getQuietHoursLogger()

	manager := getGlobalManager()
	if manager == nil {
		return
	}

	activeStreams := make(map[string]bool)
	for _, url := range manager.GetActiveStreams() {
		activeStreams[url] = true
	}

	// Phase 1: Determine actions under lock (no external calls)
	var actions []streamAction
	var soundCardSignal string // "" = no action

	s.mu.Lock()

	for _, stream := range settings.Realtime.RTSP.Streams {
		if !stream.QuietHours.Enabled {
			if s.suppressed[stream.URL] {
				actions = append(actions, streamAction{
					url: stream.URL, name: stream.Name,
					transport: stream.Transport, stop: false,
				})
			}
			continue
		}

		inQuietHours := s.isInQuietHours(stream.QuietHours, now)

		if inQuietHours && activeStreams[stream.URL] && !s.suppressed[stream.URL] {
			actions = append(actions, streamAction{
				url: stream.URL, name: stream.Name, stop: true,
			})
		} else if !inQuietHours && s.suppressed[stream.URL] {
			actions = append(actions, streamAction{
				url: stream.URL, name: stream.Name,
				transport: stream.Transport, stop: false,
			})
		}
	}

	// Clean up suppressed entries for streams no longer in config
	configuredURLs := make(map[string]bool)
	for _, stream := range settings.Realtime.RTSP.Streams {
		configuredURLs[stream.URL] = true
	}
	for url := range s.suppressed {
		if !configuredURLs[url] {
			delete(s.suppressed, url)
		}
	}

	// Determine sound card action
	if settings.Realtime.Audio.Source != "" && settings.Realtime.Audio.QuietHours.Enabled {
		inQuietHours := s.isInQuietHours(settings.Realtime.Audio.QuietHours, now)
		if inQuietHours && !s.soundCardSuppressed {
			soundCardSignal = "quiet_hours_stop_soundcard"
		} else if !inQuietHours && s.soundCardSuppressed {
			soundCardSignal = "quiet_hours_start_soundcard"
		}
	} else if s.soundCardSuppressed {
		soundCardSignal = "quiet_hours_start_soundcard"
	}

	s.mu.Unlock()

	// Phase 2: Execute actions outside the mutex
	for _, action := range actions {
		if action.stop {
			log.Info("Entering quiet hours, stopping stream",
				logger.String("stream", action.name),
				logger.String("url", privacy.SanitizeStreamUrl(action.url)))
			if err := manager.StopStream(action.url); err != nil {
				log.Error("Failed to stop stream for quiet hours",
					logger.String("stream", action.name),
					logger.Error(err))
			} else {
				s.mu.Lock()
				s.suppressed[action.url] = true
				s.mu.Unlock()
			}
		} else {
			log.Info("Exiting quiet hours, restarting stream",
				logger.String("stream", action.name),
				logger.String("url", privacy.SanitizeStreamUrl(action.url)))
			if err := manager.StartStream(action.url, action.transport, s.audioChan); err != nil {
				log.Error("Failed to restart stream after quiet hours",
					logger.String("stream", action.name),
					logger.Error(err))
			} else {
				s.mu.Lock()
				delete(s.suppressed, action.url)
				s.mu.Unlock()
			}
		}
	}

	// Execute sound card signal
	if soundCardSignal != "" {
		log.Info("Quiet hours sound card action", logger.String("signal", soundCardSignal))
		select {
		case s.controlChan <- soundCardSignal:
			s.mu.Lock()
			s.soundCardSuppressed = (soundCardSignal == "quiet_hours_stop_soundcard")
			s.mu.Unlock()
		default:
			log.Warn("Control channel full, could not send sound card signal",
				logger.String("signal", soundCardSignal))
		}
	}
}

// isInQuietHours determines whether the given time falls within the quiet hours window.
func (s *QuietHoursScheduler) isInQuietHours(qh conf.QuietHoursConfig, now time.Time) bool {
	if !qh.Enabled {
		return false
	}

	var startTime, endTime time.Time

	switch qh.Mode {
	case conf.QuietHoursModeFixed:
		startTime = parseHHMM(qh.StartTime, now)
		endTime = parseHHMM(qh.EndTime, now)

	case conf.QuietHoursModeSolar:
		if s.sunCalc == nil {
			getQuietHoursLogger().Warn("Solar quiet hours configured but sun calculator not available")
			return false
		}
		sunTimes, err := s.sunCalc.GetSunEventTimes(now)
		if err != nil {
			getQuietHoursLogger().Warn("Failed to calculate sun event times for quiet hours",
				logger.Error(err))
			return false
		}
		startTime = getSolarEventTime(sunTimes, qh.StartEvent).Add(time.Duration(qh.StartOffset) * time.Minute)
		endTime = getSolarEventTime(sunTimes, qh.EndEvent).Add(time.Duration(qh.EndOffset) * time.Minute)

	default:
		return false
	}

	return isTimeInWindow(now, startTime, endTime)
}

// parseHHMM parses a "HH:MM" string and returns a time.Time on the same date as reference.
func parseHHMM(hhmm string, reference time.Time) time.Time {
	parsed, err := time.Parse("15:04", hhmm)
	if err != nil {
		return reference // fallback, validation should have caught this
	}
	return time.Date(
		reference.Year(), reference.Month(), reference.Day(),
		parsed.Hour(), parsed.Minute(), 0, 0,
		reference.Location(),
	)
}

// getSolarEventTime returns the appropriate time from SunEventTimes based on event name.
func getSolarEventTime(sunTimes suncalc.SunEventTimes, event string) time.Time {
	switch event {
	case conf.SolarEventSunrise:
		return sunTimes.Sunrise
	case conf.SolarEventSunset:
		return sunTimes.Sunset
	default:
		return time.Time{}
	}
}

// isTimeInWindow checks if `now` falls within the time window from start to end.
// Handles overnight windows where start > end (e.g., 22:00 to 06:00).
func isTimeInWindow(now, start, end time.Time) bool {
	// Normalize to just hour:minute for comparison on the same day
	nowMinutes := now.Hour()*60 + now.Minute()
	startMinutes := start.Hour()*60 + start.Minute()
	endMinutes := end.Hour()*60 + end.Minute()

	if startMinutes <= endMinutes {
		// Same-day window (e.g., 02:00 to 06:00)
		return nowMinutes >= startMinutes && nowMinutes < endMinutes
	}
	// Overnight window (e.g., 22:00 to 06:00)
	return nowMinutes >= startMinutes || nowMinutes < endMinutes
}

// IsSoundCardSuppressed returns whether the sound card is currently suppressed by quiet hours.
func (s *QuietHoursScheduler) IsSoundCardSuppressed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.soundCardSuppressed
}

// GetSuppressedStreams returns a map of stream URLs to their suppression state.
// Only streams currently suppressed by quiet hours are included.
func (s *QuietHoursScheduler) GetSuppressedStreams() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]bool, len(s.suppressed))
	for url, suppressed := range s.suppressed {
		if suppressed {
			result[url] = true
		}
	}
	return result
}

// package-level scheduler reference for use by CaptureAudio
var (
	globalScheduler   *QuietHoursScheduler
	globalSchedulerMu sync.Mutex
)

// SetGlobalScheduler sets the package-level quiet hours scheduler reference.
func SetGlobalScheduler(s *QuietHoursScheduler) {
	globalSchedulerMu.Lock()
	defer globalSchedulerMu.Unlock()
	globalScheduler = s
}

// GetGlobalScheduler returns the package-level quiet hours scheduler reference.
func GetGlobalScheduler() *QuietHoursScheduler {
	globalSchedulerMu.Lock()
	defer globalSchedulerMu.Unlock()
	return globalScheduler
}

// IsSoundCardInQuietHours returns whether the sound card is currently suppressed
// by the global quiet hours scheduler. Safe to call when no scheduler is configured.
func IsSoundCardInQuietHours() bool {
	s := GetGlobalScheduler()
	if s == nil {
		return false
	}
	return s.IsSoundCardSuppressed()
}

// getQuietHoursLogger returns the logger for quiet hours operations.
func getQuietHoursLogger() logger.Logger {
	return getIntegrationLogger()
}
