package schedule

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// makeTime creates a time.Time on a fixed date for the given HH:MM string.
// Panics on invalid input — intended for test constants only.
func makeTime(hhmm string) time.Time {
	ref := time.Date(2025, 6, 15, 0, 0, 0, 0, time.Local)
	t, err := parseHHMM(hhmm, ref)
	if err != nil {
		panic(fmt.Sprintf("makeTime(%q): %v", hhmm, err))
	}
	return t
}

// inactiveQuietWindow returns a 1-minute fixed quiet hours window guaranteed
// to not contain the current time. This avoids flaky tests that break when CI
// happens to run during the hardcoded window.
func inactiveQuietWindow() (start, end string) {
	h := (time.Now().Hour() + 12) % 24 // 12 hours away from now
	return fmt.Sprintf("%02d:00", h), fmt.Sprintf("%02d:01", h)
}

// mockManager implements streamManager for testing Evaluate().
type mockManager struct {
	activeStreams []string
	stopped       []string
	started       []struct{ sourceID, url, transport string }
	stopErr       error
	startErr      error
}

func (m *mockManager) GetActiveStreamIDs() []string {
	return m.activeStreams
}

func (m *mockManager) StopStream(sourceID string) error {
	m.stopped = append(m.stopped, sourceID)
	return m.stopErr
}

func (m *mockManager) StartStream(sourceID, url, transport string) error {
	m.started = append(m.started, struct{ sourceID, url, transport string }{sourceID, url, transport})
	return m.startErr
}

// newTestScheduler creates a QuietHoursScheduler wired to the provided mock.
func newTestScheduler(t *testing.T, mock *mockManager) *QuietHoursScheduler {
	t.Helper()
	s := NewQuietHoursScheduler(QuietHoursConfig{
		ControlChan: make(chan string, 1),
	})
	s.SetStreamManager(func() streamManager { return mock })
	return s
}

// setTestSettings sets conf.Settings for a test and restores on cleanup.
func setTestSettings(t *testing.T, s *conf.Settings) {
	t.Helper()
	conf.SetTestSettings(s)
	t.Cleanup(func() { conf.SetTestSettings(conf.GetTestSettings()) })
}

// --- Signal constants ---

// TestQuietHours_SignalConstants verifies the signal string values.
func TestQuietHours_SignalConstants(t *testing.T) {
	assert.Equal(t, "reconfigure_quiet_hours", SignalReconfigureQuietHours)
	assert.Equal(t, "quiet_hours_stop_soundcard", SignalQuietHoursStopSoundCard)
	assert.Equal(t, "quiet_hours_start_soundcard", SignalQuietHoursStartSoundCard)
}

// --- isTimeInWindow ---

func TestIsTimeInWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		now      string // HH:MM
		start    string // HH:MM
		end      string // HH:MM
		expected bool
	}{
		// Same-day windows
		{"in daytime window", "14:00", "08:00", "18:00", true},
		{"before daytime window", "06:00", "08:00", "18:00", false},
		{"after daytime window", "20:00", "08:00", "18:00", false},
		{"at start of daytime window", "08:00", "08:00", "18:00", true},
		{"at end of daytime window", "18:00", "08:00", "18:00", false},

		// Overnight windows (start > end)
		{"in overnight window late", "23:00", "22:00", "06:00", true},
		{"in overnight window early", "03:00", "22:00", "06:00", true},
		{"outside overnight window", "12:00", "22:00", "06:00", false},
		{"at start of overnight window", "22:00", "22:00", "06:00", true},
		{"at end of overnight window", "06:00", "22:00", "06:00", false},
		{"just before end of overnight window", "05:59", "22:00", "06:00", true},

		// Edge cases
		{"midnight in overnight window", "00:00", "22:00", "06:00", true},
		{"noon not in overnight window", "12:00", "22:00", "06:00", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := makeTime(tt.now)
			start := makeTime(tt.start)
			end := makeTime(tt.end)

			result := isTimeInWindow(now, start, end)
			assert.Equal(t, tt.expected, result,
				"isTimeInWindow(%s, %s, %s)", tt.now, tt.start, tt.end)
		})
	}
}

func TestParseHHMM(t *testing.T) {
	t.Parallel()

	ref := time.Date(2025, 6, 15, 0, 0, 0, 0, time.Local)

	tests := []struct {
		name     string
		input    string
		wantHour int
		wantMin  int
	}{
		{"midnight", "00:00", 0, 0},
		{"morning", "06:30", 6, 30},
		{"afternoon", "14:45", 14, 45},
		{"late night", "23:59", 23, 59},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := parseHHMM(tt.input, ref)
			require.NoError(t, err, "parseHHMM(%q) should not error", tt.input)
			assert.Equal(t, tt.wantHour, result.Hour(), "hour mismatch for %q", tt.input)
			assert.Equal(t, tt.wantMin, result.Minute(), "minute mismatch for %q", tt.input)
			assert.Equal(t, ref.Year(), result.Year(), "year mismatch for %q", tt.input)
			assert.Equal(t, ref.Month(), result.Month(), "month mismatch for %q", tt.input)
			assert.Equal(t, ref.Day(), result.Day(), "day mismatch for %q", tt.input)
		})
	}
}

func TestParseHHMM_InvalidFormat(t *testing.T) {
	t.Parallel()

	ref := time.Date(2025, 6, 15, 0, 0, 0, 0, time.Local)

	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"missing colon", "2200"},
		{"invalid hour", "25:00"},
		{"invalid minute", "12:60"},
		{"extra fields", "12:30:45"},
		{"non-numeric", "ab:cd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseHHMM(tt.input, ref)
			assert.Error(t, err, "parseHHMM(%q) should return an error", tt.input)
		})
	}
}

// --- isInQuietHours ---

func TestIsInQuietHours_Disabled(t *testing.T) {
	t.Parallel()

	s := &QuietHoursScheduler{
		suppressed: make(map[string]bool),
	}

	qh := conf.QuietHoursConfig{
		Enabled: false,
	}

	assert.False(t, s.isInQuietHours(&qh, time.Now()), "disabled quiet hours should return false")
}

func TestIsInQuietHours_FixedMode(t *testing.T) {
	t.Parallel()

	s := &QuietHoursScheduler{
		suppressed: make(map[string]bool),
	}

	qh := conf.QuietHoursConfig{
		Enabled:   true,
		Mode:      "fixed",
		StartTime: "22:00",
		EndTime:   "06:00",
	}

	// 23:00 should be in quiet hours (overnight window).
	at2300 := time.Date(2025, 6, 15, 23, 0, 0, 0, time.Local)
	assert.True(t, s.isInQuietHours(&qh, at2300), "23:00 should be in quiet hours (22:00-06:00)")

	// 12:00 should NOT be in quiet hours.
	at1200 := time.Date(2025, 6, 15, 12, 0, 0, 0, time.Local)
	assert.False(t, s.isInQuietHours(&qh, at1200), "12:00 should NOT be in quiet hours (22:00-06:00)")

	// 03:00 should be in quiet hours (early morning in overnight window).
	at0300 := time.Date(2025, 6, 15, 3, 0, 0, 0, time.Local)
	assert.True(t, s.isInQuietHours(&qh, at0300), "03:00 should be in quiet hours (22:00-06:00)")
}

// TestQuietHours_SolarMode verifies sunrise/sunset based scheduling.
func TestQuietHours_SolarMode(t *testing.T) {
	t.Parallel()

	// Create a SunCalc for a known location (roughly central US).
	sc := suncalc.NewSunCalc(40.0, -90.0)

	s := &QuietHoursScheduler{
		sunCalc:    sc,
		suppressed: make(map[string]bool),
	}

	qh := conf.QuietHoursConfig{
		Enabled:     true,
		Mode:        "solar",
		StartEvent:  "sunset",
		StartOffset: 30, // 30 minutes after sunset
		EndEvent:    "sunrise",
		EndOffset:   -30, // 30 minutes before sunrise
	}

	// SunCalc returns times in the system's local timezone (via
	// conf.ConvertUTCToLocal). isTimeInWindow compares raw hour:minute,
	// so test times must also be in time.Local for the comparison to work.
	// We compute the actual quiet window from the SunCalc to pick test
	// times that are valid regardless of the system timezone.
	refDate := time.Date(2025, 6, 15, 12, 0, 0, 0, time.Local)
	sunTimes, err := sc.GetSunEventTimes(refDate)
	if err != nil {
		t.Fatalf("failed to get sun event times: %v", err)
	}

	// Quiet window: sunset + 30min to sunrise - 30min (overnight).
	windowStart := sunTimes.Sunset.Add(time.Duration(qh.StartOffset) * time.Minute)
	windowEnd := sunTimes.Sunrise.Add(time.Duration(qh.EndOffset) * time.Minute)

	// Pick a time 1 hour after the window start (should be in quiet hours).
	inWindow := windowStart.Add(1 * time.Hour)
	assert.True(t, s.isInQuietHours(&qh, inWindow),
		"1 hour after quiet window start should be in quiet hours")

	// Pick a time 1 hour after the window end (should NOT be in quiet hours).
	outsideWindow := windowEnd.Add(1 * time.Hour)
	assert.False(t, s.isInQuietHours(&qh, outsideWindow),
		"1 hour after quiet window end should NOT be in quiet hours")
}

func TestIsInQuietHours_SolarMode_NoSunCalc(t *testing.T) {
	t.Parallel()

	s := &QuietHoursScheduler{
		sunCalc:    nil,
		suppressed: make(map[string]bool),
	}

	qh := conf.QuietHoursConfig{
		Enabled:    true,
		Mode:       "solar",
		StartEvent: "sunset",
		EndEvent:   "sunrise",
	}

	assert.False(t, s.isInQuietHours(&qh, time.Now()),
		"should return false when sunCalc is nil")
}

func TestIsInQuietHours_InvalidMode(t *testing.T) {
	t.Parallel()

	s := &QuietHoursScheduler{
		suppressed: make(map[string]bool),
	}

	qh := conf.QuietHoursConfig{
		Enabled: true,
		Mode:    "invalid",
	}

	assert.False(t, s.isInQuietHours(&qh, time.Now()),
		"should return false for invalid mode")
}

func TestGetSolarEventTime(t *testing.T) {
	t.Parallel()

	sunTimes := suncalc.SunEventTimes{
		Sunrise:   time.Date(2025, 6, 15, 5, 30, 0, 0, time.Local),
		Sunset:    time.Date(2025, 6, 15, 20, 45, 0, 0, time.Local),
		CivilDawn: time.Date(2025, 6, 15, 5, 0, 0, 0, time.Local),
		CivilDusk: time.Date(2025, 6, 15, 21, 15, 0, 0, time.Local),
	}

	t.Run("sunrise", func(t *testing.T) {
		t.Parallel()
		got := getSolarEventTime(&sunTimes, "sunrise")
		assert.True(t, got.Equal(sunTimes.Sunrise),
			"getSolarEventTime(sunrise) = %v, want %v", got, sunTimes.Sunrise)
	})

	t.Run("sunset", func(t *testing.T) {
		t.Parallel()
		got := getSolarEventTime(&sunTimes, "sunset")
		assert.True(t, got.Equal(sunTimes.Sunset),
			"getSolarEventTime(sunset) = %v, want %v", got, sunTimes.Sunset)
	})

	t.Run("invalid event returns zero time", func(t *testing.T) {
		t.Parallel()
		got := getSolarEventTime(&sunTimes, "invalid")
		assert.True(t, got.IsZero(), "getSolarEventTime(invalid) should return zero time")
	})
}

// --- TestQuietHours_Evaluate: Evaluate() behaviour ---

// TestQuietHours_Evaluate verifies suppression during quiet hours.
func TestQuietHours_Evaluate(t *testing.T) {
	t.Run("stops stream during quiet hours", func(t *testing.T) {
		mock := &mockManager{activeStreams: []string{"rtsp://cam1"}}

		settings := conf.GetTestSettings()
		settings.Realtime.RTSP.Streams = []conf.StreamConfig{
			{
				Name: "cam1", URL: "rtsp://cam1", Enabled: true, Transport: "tcp",
				QuietHours: conf.QuietHoursConfig{
					Enabled:   true,
					Mode:      "fixed",
					StartTime: "00:00",
					EndTime:   "23:59", // all day = always in quiet hours
				},
			},
		}
		setTestSettings(t, settings)

		s := newTestScheduler(t, mock)
		s.Evaluate()

		assert.Equal(t, []string{"rtsp://cam1"}, mock.stopped, "should stop stream during quiet hours")
		assert.Empty(t, mock.started, "should not start any streams")
		assert.True(t, s.suppressed["rtsp://cam1"], "stream should be marked suppressed")
	})

	t.Run("restarts stream after quiet hours", func(t *testing.T) {
		mock := &mockManager{activeStreams: []string{}}

		qhStart, qhEnd := inactiveQuietWindow()
		settings := conf.GetTestSettings()
		settings.Realtime.RTSP.Streams = []conf.StreamConfig{
			{
				Name: "cam1", URL: "rtsp://cam1", Enabled: true, Transport: "tcp",
				QuietHours: conf.QuietHoursConfig{
					Enabled:   true,
					Mode:      "fixed",
					StartTime: qhStart,
					EndTime:   qhEnd,
				},
			},
		}
		setTestSettings(t, settings)

		s := newTestScheduler(t, mock)
		s.suppressed["rtsp://cam1"] = true // previously suppressed
		s.Evaluate()

		assert.Empty(t, mock.stopped, "should not stop any streams")
		assert.Len(t, mock.started, 1, "should restart previously suppressed stream")
		assert.Equal(t, "rtsp://cam1", mock.started[0].sourceID)
		assert.Equal(t, "rtsp://cam1", mock.started[0].url)
		assert.Equal(t, "tcp", mock.started[0].transport)
		assert.False(t, s.suppressed["rtsp://cam1"], "stream should no longer be suppressed")
	})

	t.Run("disabled quiet hours restores suppressed stream", func(t *testing.T) {
		mock := &mockManager{activeStreams: []string{}}

		settings := conf.GetTestSettings()
		settings.Realtime.RTSP.Streams = []conf.StreamConfig{
			{
				Name: "cam1", URL: "rtsp://cam1", Enabled: true, Transport: "tcp",
				QuietHours: conf.QuietHoursConfig{Enabled: false},
			},
		}
		setTestSettings(t, settings)

		s := newTestScheduler(t, mock)
		s.suppressed["rtsp://cam1"] = true // was suppressed
		s.Evaluate()

		assert.Len(t, mock.started, 1, "should restart stream when quiet hours disabled")
		assert.Equal(t, "rtsp://cam1", mock.started[0].sourceID)
	})

	t.Run("disabled stream is ignored and stale suppression is cleared", func(t *testing.T) {
		mock := &mockManager{activeStreams: []string{"rtsp://cam1"}}

		settings := conf.GetTestSettings()
		settings.Realtime.RTSP.Streams = []conf.StreamConfig{
			{
				Name:      "cam1",
				URL:       "rtsp://cam1",
				Enabled:   false,
				Transport: "tcp",
				QuietHours: conf.QuietHoursConfig{
					Enabled:   true,
					Mode:      "fixed",
					StartTime: "00:00",
					EndTime:   "23:59",
				},
			},
		}
		setTestSettings(t, settings)

		s := newTestScheduler(t, mock)
		s.suppressed["rtsp://cam1"] = true
		s.Evaluate()

		assert.Empty(t, mock.stopped, "disabled streams should not be stopped by quiet hours")
		assert.Empty(t, mock.started, "disabled streams should not be restarted by quiet hours")
		assert.False(t, s.suppressed["rtsp://cam1"], "stale suppression should be cleared for disabled streams")
	})

	t.Run("no action when not in quiet hours", func(t *testing.T) {
		mock := &mockManager{activeStreams: []string{"rtsp://cam1"}}

		qhStart, qhEnd := inactiveQuietWindow()
		settings := conf.GetTestSettings()
		settings.Realtime.RTSP.Streams = []conf.StreamConfig{
			{
				Name: "cam1", URL: "rtsp://cam1", Enabled: true, Transport: "tcp",
				QuietHours: conf.QuietHoursConfig{
					Enabled:   true,
					Mode:      "fixed",
					StartTime: qhStart,
					EndTime:   qhEnd,
				},
			},
		}
		setTestSettings(t, settings)

		s := newTestScheduler(t, mock)
		s.Evaluate()

		assert.Empty(t, mock.stopped, "should not stop streams outside quiet hours")
		assert.Empty(t, mock.started, "should not start streams that weren't suppressed")
	})

	t.Run("nil manager returns early", func(t *testing.T) {
		s := NewQuietHoursScheduler(QuietHoursConfig{
			ControlChan: make(chan string, 1),
		})
		s.SetStreamManager(func() streamManager { return nil })
		// Should not panic.
		s.Evaluate()
	})

	t.Run("nil getManager returns early", func(t *testing.T) {
		s := NewQuietHoursScheduler(QuietHoursConfig{
			ControlChan: make(chan string, 1),
		})
		// getManager is nil by default — Evaluate should return early.
		s.Evaluate()
	})
}

// --- Sound card tests ---

func TestEvaluate_SoundCardQuietHours(t *testing.T) {
	mock := &mockManager{activeStreams: []string{}}

	settings := conf.GetTestSettings()
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{{
		Name:   "Test Sound Card",
		Device: "default",
		QuietHours: conf.QuietHoursConfig{
			Enabled:   true,
			Mode:      "fixed",
			StartTime: "00:00",
			EndTime:   "23:59", // always in quiet hours
		},
	}}
	setTestSettings(t, settings)

	s := newTestScheduler(t, mock)
	s.Evaluate()

	assert.Len(t, s.controlChan, 1, "should send sound card signal")
	signal := <-s.controlChan
	assert.Equal(t, SignalQuietHoursStopSoundCard, signal)
	assert.True(t, s.soundCardSuppressed, "sound card should be marked suppressed")
}

func TestEvaluate_SoundCardRestoredWhenDisabled(t *testing.T) {
	mock := &mockManager{activeStreams: []string{}}

	settings := conf.GetTestSettings()
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{{
		Name:       "Test Sound Card",
		Device:     "default",
		QuietHours: conf.QuietHoursConfig{Enabled: false},
	}}
	setTestSettings(t, settings)

	s := newTestScheduler(t, mock)
	s.soundCardSuppressed = true // was suppressed
	s.Evaluate()

	assert.Len(t, s.controlChan, 1, "should send sound card start signal")
	signal := <-s.controlChan
	assert.Equal(t, SignalQuietHoursStartSoundCard, signal)
	assert.False(t, s.soundCardSuppressed, "sound card should no longer be suppressed")
}

// --- IsSoundCardSuppressed / GetSuppressedStreams ---

func TestIsSoundCardSuppressed(t *testing.T) {
	t.Parallel()

	s := &QuietHoursScheduler{suppressed: make(map[string]bool)}
	assert.False(t, s.IsSoundCardSuppressed())

	s.soundCardSuppressed = true
	assert.True(t, s.IsSoundCardSuppressed())
}

func TestGetSuppressedStreams(t *testing.T) {
	t.Parallel()

	s := &QuietHoursScheduler{
		suppressed: map[string]bool{
			"rtsp://cam1": true,
			"rtsp://cam2": false,
		},
	}

	result := s.GetSuppressedStreams()
	assert.Equal(t, map[string]bool{"rtsp://cam1": true}, result,
		"should only include suppressed streams")
}
