package errors

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TransportMock is a mock Sentry transport that captures events for inspection.
type TransportMock struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *TransportMock) Flush(_ time.Duration) bool              { return true }
func (t *TransportMock) FlushWithContext(_ context.Context) bool { return true }
func (t *TransportMock) Configure(_ sentry.ClientOptions)        {} //nolint:gocritic // hugeParam: signature required by sentry.Transport interface
func (t *TransportMock) Close()                                  {}
func (t *TransportMock) SendEvent(event *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, event)
}

// Events returns the captured events.
func (t *TransportMock) Events() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.events
}

func TestReportError_IncludesStacktrace(t *testing.T) {
	transport := &TransportMock{}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:       "https://examplePublicKey@o0.ingest.sentry.io/0",
		Transport: transport,
	})
	require.NoError(t, err)

	reporter := NewSentryReporter(true)
	ee := New(fmt.Errorf("test error")).
		Component("test").
		Category(CategoryDatabase).
		Context("operation", "test_op").
		Build()

	reporter.ReportError(ee)
	sentry.Flush(2 * time.Second)

	require.NotEmpty(t, transport.Events(), "should have captured an event")
	event := transport.Events()[0]
	require.NotEmpty(t, event.Exception, "event should have exceptions")
	assert.NotNil(t, event.Exception[0].Stacktrace, "exception should have a stacktrace")
	assert.NotEmpty(t, event.Exception[0].Stacktrace.Frames, "stacktrace should have frames")
}

func TestShouldReportToSentry_FiltersNoRouteToHost(t *testing.T) {
	t.Parallel()
	ee := New(fmt.Errorf("dial tcp 192.168.1.1:1883: connect: no route to host")).
		Component("mqtt").
		Category(CategoryMQTTConnection).
		Build()
	assert.False(t, shouldReportToSentry(ee))
}

func TestShouldReportToSentry_FiltersDNSErrors(t *testing.T) {
	t.Parallel()
	ee := New(fmt.Errorf("dial tcp: lookup en.wikipedia.org: server misbehaving")).
		Component("imageprovider").
		Category(CategoryNetwork).
		Build()
	assert.False(t, shouldReportToSentry(ee))
}

func TestShouldReportToSentry_FiltersConnectionRefused(t *testing.T) {
	t.Parallel()
	ee := New(fmt.Errorf("dial tcp 10.0.0.1:8080: connection refused")).
		Component("weather").
		Category(CategoryHTTP).
		Build()
	assert.False(t, shouldReportToSentry(ee))
}

func TestShouldReportToSentry_AllowsCodeErrors(t *testing.T) {
	t.Parallel()
	ee := New(fmt.Errorf("species not found in taxonomy")).
		Component("birdnet").
		Category(CategoryNotFound).
		Build()
	assert.True(t, shouldReportToSentry(ee))
}

func TestShouldReportToSentry_FiltersNoteNotFound(t *testing.T) {
	t.Parallel()
	// "note not found" with CategoryNotFound is a transient race condition, not a code bug
	ee := New(fmt.Errorf("note not found")).
		Component("datastore").
		Category(CategoryNotFound).
		Build()
	assert.False(t, shouldReportToSentry(ee))
}

func TestShouldReportToSentry_AllowsNetworkCategoryCodeBugs(t *testing.T) {
	t.Parallel()
	// Network category error that is NOT environmental noise should still report
	ee := New(fmt.Errorf("unexpected status code 500 from API")).
		Component("imageprovider").
		Category(CategoryNetwork).
		Build()
	assert.True(t, shouldReportToSentry(ee))
}

// TestShouldReportToSentry_CategoryLimitNotificationOnly verifies that the
// CategoryLimit suppression is scoped to the notification component only.
// The notification circuit breaker produces high-volume [limit] state noise
// that is already covered by the dedicated CircuitBreakerStateTransition
// telemetry path. Other CategoryLimit producers (eBird API quota, analysis
// job queue full, spectrogram pre-render memory limits) are legitimate
// operational signals that ops needs to see and must still reach Sentry.
func TestShouldReportToSentry_CategoryLimitNotificationOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		component  string
		err        error
		wantReport bool
	}{
		{
			name:       "notification circuit breaker open is suppressed",
			component:  "notification",
			err:        fmt.Errorf("circuit breaker is open"),
			wantReport: false,
		},
		{
			name:       "notification circuit breaker half-open too many requests is suppressed",
			component:  "notification",
			err:        fmt.Errorf("circuit breaker is half-open, too many requests"),
			wantReport: false,
		},
		{
			name:       "ebird API quota exhaustion is reported",
			component:  "ebird",
			err:        fmt.Errorf("ebird API quota exceeded"),
			wantReport: true,
		},
		{
			name:       "analysis job queue full is reported",
			component:  "jobqueue",
			err:        fmt.Errorf("job queue is full"),
			wantReport: true,
		},
		{
			name:       "spectrogram prerender queue full is reported",
			component:  "spectrogram",
			err:        fmt.Errorf("pre-render queue full"),
			wantReport: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ee := New(tt.err).
				Component(tt.component).
				Category(CategoryLimit).
				Build()
			got := shouldReportToSentry(ee)
			if tt.wantReport {
				assert.Truef(t, got,
					"%s: CategoryLimit from %q must still be forwarded to Sentry",
					tt.name, tt.component)
			} else {
				assert.Falsef(t, got,
					"%s: CategoryLimit from %q must not be forwarded to Sentry",
					tt.name, tt.component)
			}
		})
	}
}

// TestShouldReportToSentry_FiltersRTSPSilenceTimeout verifies that the RTSP
// silence-timeout warning emitted by the ffmpeg stream layer is not
// forwarded to Sentry. These are transient network glitches, not bugs.
// The full, stable signature from stream.go must match; a loose substring
// without "for 90 seconds" is intentionally NOT suppressed so that future
// RTSP failures worded similarly still reach Sentry.
func TestShouldReportToSentry_FiltersRTSPSilenceTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		errMsg     string
		wantReport bool
	}{
		{
			name:       "exact silence-timeout signature is suppressed",
			errMsg:     "stream stopped producing data for 90 seconds",
			wantReport: false,
		},
		{
			name:       "silence-timeout signature with wrapping context is suppressed",
			errMsg:     "[rtsp-connection] stream stopped producing data for 90 seconds, restarting",
			wantReport: false,
		},
		{
			name:       "loose substring without full signature is reported",
			errMsg:     "[rtsp-connection] stream stopped producing data",
			wantReport: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ee := New(fmt.Errorf("%s", tt.errMsg)).
				Component("ffmpeg-stream").
				Category(CategoryRTSP).
				Context("operation", "silence_timeout").
				Build()
			got := shouldReportToSentry(ee)
			assert.Equal(t, tt.wantReport, got,
				"shouldReportToSentry(%q) = %v, want %v", tt.errMsg, got, tt.wantReport)
		})
	}
}

// TestShouldReportToSentry_AllowsRTSPCodeBugs is a positive control that
// pins the behavior for RTSP category errors that do NOT match the
// known-noisy patterns. These must still reach Sentry so real bugs in the
// RTSP stack remain visible.
func TestShouldReportToSentry_AllowsRTSPCodeBugs(t *testing.T) {
	t.Parallel()
	ee := New(fmt.Errorf("ffmpeg produced invalid frame header at offset 42")).
		Component("ffmpeg-stream").
		Category(CategoryRTSP).
		Build()
	assert.True(t, shouldReportToSentry(ee),
		"non-noise RTSP errors must still be forwarded to Sentry")
}

func TestShouldReportToSentry_RateLimitsDiskFull(t *testing.T) {
	// Not parallel — mutates package-level state
	lastDiskFullReport.Store(0)
	t.Cleanup(func() { lastDiskFullReport.Store(0) })

	ee1 := New(fmt.Errorf("database or disk is full")).
		Component("datastore").Category(CategoryDatabase).Build()
	ee2 := New(fmt.Errorf("database or disk is full")).
		Component("myaudio").Category(CategorySystem).Build()

	assert.True(t, shouldReportToSentry(ee1), "first disk-full should be reported")
	assert.False(t, shouldReportToSentry(ee2), "second disk-full within cooldown should be suppressed")
}

func TestShouldReportToSentry_DiskFullCooldownExpiry(t *testing.T) {
	// Not parallel — mutates package-level state
	// Simulate a disk-full report from past the cooldown window
	lastDiskFullReport.Store(time.Now().Unix() - diskFullCooldown - 1)
	t.Cleanup(func() { lastDiskFullReport.Store(0) })

	ee := New(fmt.Errorf("database or disk is full")).
		Component("datastore").Category(CategoryDatabase).Build()

	assert.True(t, shouldReportToSentry(ee), "disk-full should be reported after cooldown expires")
}
