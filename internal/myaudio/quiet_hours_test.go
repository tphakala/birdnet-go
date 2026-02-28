package myaudio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

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

			result := parseHHMM(tt.input, ref)
			assert.Equal(t, tt.wantHour, result.Hour(), "hour mismatch for %q", tt.input)
			assert.Equal(t, tt.wantMin, result.Minute(), "minute mismatch for %q", tt.input)
			assert.Equal(t, ref.Year(), result.Year(), "year mismatch for %q", tt.input)
			assert.Equal(t, ref.Month(), result.Month(), "month mismatch for %q", tt.input)
			assert.Equal(t, ref.Day(), result.Day(), "day mismatch for %q", tt.input)
		})
	}
}

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

	// 23:00 should be in quiet hours (overnight window)
	at2300 := time.Date(2025, 6, 15, 23, 0, 0, 0, time.Local)
	assert.True(t, s.isInQuietHours(&qh, at2300), "23:00 should be in quiet hours (22:00-06:00)")

	// 12:00 should NOT be in quiet hours
	at1200 := time.Date(2025, 6, 15, 12, 0, 0, 0, time.Local)
	assert.False(t, s.isInQuietHours(&qh, at1200), "12:00 should NOT be in quiet hours (22:00-06:00)")

	// 03:00 should be in quiet hours (early morning in overnight window)
	at0300 := time.Date(2025, 6, 15, 3, 0, 0, 0, time.Local)
	assert.True(t, s.isInQuietHours(&qh, at0300), "03:00 should be in quiet hours (22:00-06:00)")
}

func TestIsInQuietHours_SolarMode(t *testing.T) {
	t.Parallel()

	// Create a SunCalc for a known location (roughly central US)
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

	// Quiet window: sunset + 30min to sunrise - 30min (overnight)
	windowStart := sunTimes.Sunset.Add(time.Duration(qh.StartOffset) * time.Minute)
	windowEnd := sunTimes.Sunrise.Add(time.Duration(qh.EndOffset) * time.Minute)

	// Pick a time 1 hour after the window start (should be in quiet hours)
	inWindow := windowStart.Add(1 * time.Hour)
	assert.True(t, s.isInQuietHours(&qh, inWindow),
		"1 hour after quiet window start should be in quiet hours")

	// Pick a time 1 hour after the window end (should NOT be in quiet hours)
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
		Enabled:     true,
		Mode:        "solar",
		StartEvent:  "sunset",
		StartOffset: 0,
		EndEvent:    "sunrise",
		EndOffset:   0,
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

// mockManager implements streamManager for testing Evaluate().
type mockManager struct {
	activeStreams []string
	stopped       []string
	started       []struct{ url, transport string }
	stopErr       error
	startErr      error
}

func (m *mockManager) GetActiveStreams() []string {
	return m.activeStreams
}

func (m *mockManager) StopStream(url string) error {
	m.stopped = append(m.stopped, url)
	return m.stopErr
}

func (m *mockManager) StartStream(url, transport string, _ chan UnifiedAudioData) error {
	m.started = append(m.started, struct{ url, transport string }{url, transport})
	return m.startErr
}

// setTestManager overrides getManagerFunc for a test and restores it on cleanup.
func setTestManager(t *testing.T, m streamManager) {
	t.Helper()
	orig := getManagerFunc
	getManagerFunc = func() streamManager { return m }
	t.Cleanup(func() { getManagerFunc = orig })
}

// setTestAudioChan sets a test audio channel and restores nil on cleanup.
func setTestAudioChan(t *testing.T) chan UnifiedAudioData {
	t.Helper()
	ch := make(chan UnifiedAudioData, 1)
	SetCurrentAudioChan(ch)
	t.Cleanup(func() { SetCurrentAudioChan(nil) })
	return ch
}

// setTestSettings sets conf.Settings for a test and restores nil on cleanup.
func setTestSettings(t *testing.T, s *conf.Settings) {
	t.Helper()
	conf.SetTestSettings(s)
	t.Cleanup(func() { conf.SetTestSettings(conf.GetTestSettings()) })
}

func TestEvaluate_StopsStreamDuringQuietHours(t *testing.T) {
	mock := &mockManager{activeStreams: []string{"rtsp://cam1"}}
	setTestManager(t, mock)
	setTestAudioChan(t)

	settings := conf.GetTestSettings()
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name: "cam1", URL: "rtsp://cam1", Transport: "tcp",
			QuietHours: conf.QuietHoursConfig{
				Enabled:   true,
				Mode:      "fixed",
				StartTime: "00:00",
				EndTime:   "23:59", // all day = always in quiet hours
			},
		},
	}
	setTestSettings(t, settings)

	scheduler := &QuietHoursScheduler{
		controlChan: make(chan string, 1),
		suppressed:  make(map[string]bool),
	}
	scheduler.Evaluate()

	assert.Equal(t, []string{"rtsp://cam1"}, mock.stopped, "should stop stream during quiet hours")
	assert.Empty(t, mock.started, "should not start any streams")
	assert.True(t, scheduler.suppressed["rtsp://cam1"], "stream should be marked suppressed")
}

func TestEvaluate_RestartsStreamAfterQuietHours(t *testing.T) {
	mock := &mockManager{activeStreams: []string{}}
	setTestManager(t, mock)
	setTestAudioChan(t)

	settings := conf.GetTestSettings()
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name: "cam1", URL: "rtsp://cam1", Transport: "tcp",
			QuietHours: conf.QuietHoursConfig{
				Enabled:   true,
				Mode:      "fixed",
				StartTime: "03:00",
				EndTime:   "03:01", // tiny window that's almost never active
			},
		},
	}
	setTestSettings(t, settings)

	scheduler := &QuietHoursScheduler{
		controlChan: make(chan string, 1),
		suppressed:  map[string]bool{"rtsp://cam1": true}, // previously suppressed
	}
	scheduler.Evaluate()

	assert.Empty(t, mock.stopped, "should not stop any streams")
	assert.Len(t, mock.started, 1, "should restart previously suppressed stream")
	assert.Equal(t, "rtsp://cam1", mock.started[0].url)
	assert.Equal(t, "tcp", mock.started[0].transport)
	assert.False(t, scheduler.suppressed["rtsp://cam1"], "stream should no longer be suppressed")
}

func TestEvaluate_DisabledQuietHoursRestoresSuppressed(t *testing.T) {
	mock := &mockManager{activeStreams: []string{}}
	setTestManager(t, mock)
	setTestAudioChan(t)

	settings := conf.GetTestSettings()
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name: "cam1", URL: "rtsp://cam1", Transport: "tcp",
			QuietHours: conf.QuietHoursConfig{Enabled: false},
		},
	}
	setTestSettings(t, settings)

	scheduler := &QuietHoursScheduler{
		controlChan: make(chan string, 1),
		suppressed:  map[string]bool{"rtsp://cam1": true}, // was suppressed
	}
	scheduler.Evaluate()

	assert.Len(t, mock.started, 1, "should restart stream when quiet hours disabled")
	assert.Equal(t, "rtsp://cam1", mock.started[0].url)
}

func TestEvaluate_SoundCardQuietHours(t *testing.T) {
	mock := &mockManager{activeStreams: []string{}}
	setTestManager(t, mock)
	setTestAudioChan(t)

	settings := conf.GetTestSettings()
	settings.Realtime.Audio.Source = deviceDefault
	settings.Realtime.Audio.QuietHours = conf.QuietHoursConfig{
		Enabled:   true,
		Mode:      "fixed",
		StartTime: "00:00",
		EndTime:   "23:59", // always in quiet hours
	}
	setTestSettings(t, settings)

	controlChan := make(chan string, 1)
	scheduler := &QuietHoursScheduler{
		controlChan: controlChan,
		suppressed:  make(map[string]bool),
	}
	scheduler.Evaluate()

	assert.Len(t, controlChan, 1, "should send sound card signal")
	signal := <-controlChan
	assert.Equal(t, SignalQuietHoursStopSoundCard, signal)
	assert.True(t, scheduler.soundCardSuppressed, "sound card should be marked suppressed")
}

func TestEvaluate_SoundCardRestoredWhenDisabled(t *testing.T) {
	mock := &mockManager{activeStreams: []string{}}
	setTestManager(t, mock)
	setTestAudioChan(t)

	settings := conf.GetTestSettings()
	settings.Realtime.Audio.Source = deviceDefault
	settings.Realtime.Audio.QuietHours = conf.QuietHoursConfig{Enabled: false}
	setTestSettings(t, settings)

	controlChan := make(chan string, 1)
	scheduler := &QuietHoursScheduler{
		controlChan:         controlChan,
		suppressed:          make(map[string]bool),
		soundCardSuppressed: true, // was suppressed
	}
	scheduler.Evaluate()

	assert.Len(t, controlChan, 1, "should send sound card start signal")
	signal := <-controlChan
	assert.Equal(t, SignalQuietHoursStartSoundCard, signal)
	assert.False(t, scheduler.soundCardSuppressed, "sound card should no longer be suppressed")
}

func TestEvaluate_NoActionWhenNotInQuietHours(t *testing.T) {
	mock := &mockManager{activeStreams: []string{"rtsp://cam1"}}
	setTestManager(t, mock)
	setTestAudioChan(t)

	settings := conf.GetTestSettings()
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name: "cam1", URL: "rtsp://cam1", Transport: "tcp",
			QuietHours: conf.QuietHoursConfig{
				Enabled:   true,
				Mode:      "fixed",
				StartTime: "03:00",
				EndTime:   "03:01", // tiny window, almost never active
			},
		},
	}
	setTestSettings(t, settings)

	scheduler := &QuietHoursScheduler{
		controlChan: make(chan string, 1),
		suppressed:  make(map[string]bool),
	}
	scheduler.Evaluate()

	assert.Empty(t, mock.stopped, "should not stop streams outside quiet hours")
	assert.Empty(t, mock.started, "should not start streams that weren't suppressed")
}

func TestEvaluate_NilManagerReturnsEarly(t *testing.T) {
	orig := getManagerFunc
	getManagerFunc = func() streamManager { return nil }
	t.Cleanup(func() { getManagerFunc = orig })

	scheduler := &QuietHoursScheduler{
		controlChan: make(chan string, 1),
		suppressed:  make(map[string]bool),
	}
	// Should not panic
	scheduler.Evaluate()
}

func TestEvaluate_NilAudioChanAllowsStopButBlocksRestart(t *testing.T) {
	// With nil audioChan, streams can still be stopped during quiet hours
	// but cannot be restarted (audioChan is only needed for StartStream).
	mock := &mockManager{activeStreams: []string{"rtsp://cam1"}}
	setTestManager(t, mock)
	SetCurrentAudioChan(nil)

	settings := conf.GetTestSettings()
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name: "cam1", URL: "rtsp://cam1", Transport: "tcp",
			QuietHours: conf.QuietHoursConfig{
				Enabled: true, Mode: "fixed",
				StartTime: "00:00", EndTime: "23:59",
			},
		},
	}
	setTestSettings(t, settings)

	scheduler := &QuietHoursScheduler{
		controlChan: make(chan string, 1),
		suppressed:  make(map[string]bool),
	}
	scheduler.Evaluate()

	assert.Equal(t, []string{"rtsp://cam1"}, mock.stopped, "should stop streams even when audioChan is nil")
	assert.Empty(t, mock.started, "should not start streams when audioChan is nil")
	assert.True(t, scheduler.suppressed["rtsp://cam1"], "stream should be marked suppressed")
}

func TestEvaluate_NilAudioChanSkipsRestart(t *testing.T) {
	// When a stream is suppressed and quiet hours end, but audioChan is nil,
	// the stream should NOT be restarted.
	mock := &mockManager{activeStreams: []string{}}
	setTestManager(t, mock)
	SetCurrentAudioChan(nil)

	settings := conf.GetTestSettings()
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name: "cam1", URL: "rtsp://cam1", Transport: "tcp",
			QuietHours: conf.QuietHoursConfig{
				Enabled: true, Mode: "fixed",
				StartTime: "03:00", EndTime: "03:01", // tiny window, almost never active
			},
		},
	}
	setTestSettings(t, settings)

	scheduler := &QuietHoursScheduler{
		controlChan: make(chan string, 1),
		suppressed:  map[string]bool{"rtsp://cam1": true}, // previously suppressed
	}
	scheduler.Evaluate()

	assert.Empty(t, mock.stopped, "should not stop any streams")
	assert.Empty(t, mock.started, "should not restart stream when audioChan is nil")
	assert.True(t, scheduler.suppressed["rtsp://cam1"], "stream should remain suppressed when restart fails")
}
