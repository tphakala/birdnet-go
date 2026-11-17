package errors

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-sql-driver/mysql"
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

// bindMockTransport points the process-wide Sentry hub at a recording
// transport and restores the previous client when the test ends. Tests that
// use it must not run in parallel.
func bindMockTransport(t *testing.T) *TransportMock {
	t.Helper()
	hub := sentry.CurrentHub()
	original := hub.Client()
	t.Cleanup(func() { hub.BindClient(original) })
	transport := &TransportMock{}
	require.NoError(t, sentry.Init(sentry.ClientOptions{
		Dsn:       "https://examplePublicKey@o0.ingest.sentry.io/0",
		Transport: transport,
	}))
	return transport
}

// alwaysAllowed is a reporter gate that permits every report.
func alwaysAllowed() bool { return true }

func TestReportError_IncludesStacktrace(t *testing.T) {
	transport := bindMockTransport(t)

	reporter := NewSentryReporter(alwaysAllowed)
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

func TestShouldReportToSentry_FiltersEBirdTaxonomyNotFound(t *testing.T) {
	t.Parallel()
	// Non-bird species (e.g., Canis latrans) are expected to be absent from eBird
	ee := New(fmt.Errorf("species not found in eBird taxonomy: Canis latrans")).
		Component("ebird").
		Category(CategoryNotFound).
		Build()
	assert.False(t, shouldReportToSentry(ee))
}

func TestShouldReportToSentry_FiltersDynamicThresholdNotFound(t *testing.T) {
	t.Parallel()
	// A user querying the dynamic threshold for a species that has none is a
	// benign 404, not a code bug. Both the v2only and legacy datastore backends
	// produce this CategoryNotFound error, so it must not reach Sentry (#1068).
	ee := New(fmt.Errorf("dynamic threshold not found")).
		Component("datastore").
		Category(CategoryNotFound).
		Build()
	assert.False(t, shouldReportToSentry(ee))
}

// TestShouldReportToSentry_ConnectivityNoise pins which failures are treated
// as connectivity noise and dropped, and which near neighbours (HTTP statuses,
// config errors, circuit breakers, job timeouts) keep reporting. Each message
// is built in the shape the producer emits.
func TestShouldReportToSentry_ConnectivityNoise(t *testing.T) {
	t.Parallel()

	const (
		birdweatherURL = "https://app.birdweather.com/api/v1/stations/x/detections"
		ffmpegStartup  = "FFmpeg process failed to start properly: "
	)

	tests := []struct {
		name       string
		component  string
		category   ErrorCategory
		err        error
		wantReport bool
	}{
		// Dropped.
		{"mqtt no route to host", "mqtt", CategoryMQTTConnection, fmt.Errorf("dial tcp 192.168.1.1:1883: connect: no route to host"), false},
		{"network dns server misbehaving", "imageprovider", CategoryNetwork, fmt.Errorf("dial tcp: lookup en.wikipedia.org: server misbehaving"), false},
		{"http connection refused", "weather", CategoryHTTP, fmt.Errorf("dial tcp 10.0.0.1:8080: connection refused"), false},
		{"birdweather http2 connection lost", "birdweather", CategoryNetwork,
			fmt.Errorf("BirdWeather detection post network error: %w",
				&url.Error{Op: "Post", URL: birdweatherURL, Err: NewStd("http2: client connection lost")}), false},
		{"birdweather client timeout", "birdweather", CategoryNetwork,
			fmt.Errorf("BirdWeather soundscape upload timeout: %w",
				&url.Error{Op: "Post", URL: birdweatherURL, Err: NewStd("net/http: request canceled (Client.Timeout exceeded while awaiting headers)")}), false},
		{"mqtt not connected after wait", "mqtt", CategoryMQTTConnection, fmt.Errorf("mqtt connection failed post-wait, client not connected"), false},
		{"mqtt connect attempt timed out", "mqtt", CategoryMQTTConnection, fmt.Errorf("mqtt connection attempt actively timed out"), false},
		{"mqtt connection lost before publish", "mqtt", CategoryMQTTPublish, fmt.Errorf("connection lost before Publish completed"), false},
		{"mqtt publish while disconnected", "mqtt", CategoryMQTTPublish, fmt.Errorf("not connected to MQTT broker"), false},
		{"mqtt broker closed the connection", "mqtt", CategoryMQTTConnection, fmt.Errorf("read tcp 10.0.0.2:41234->10.0.0.9:1883: EOF"), false},
		{"mqtt publish timeout", "mqtt", CategoryMQTTPublish, fmt.Errorf("publish timeout after %v", 10*time.Second), false},
		{"sound level publish deadline", "analysis.soundlevel", CategoryMQTTPublish, context.DeadlineExceeded, false},
		{"rtsp host is down", "ffmpeg-stream", CategoryRTSP,
			fmt.Errorf("%s[tcp @ 0x5581] Connection to tcp://[REDACTED]:8554?timeout=0 failed: Host is down", ffmpegStartup), false},
		{"rtsp network unreachable", "ffmpeg-stream", CategoryRTSP, fmt.Errorf("%sNetwork unreachable", ffmpegStartup), false},
		{"rtsp connection timed out", "ffmpeg-stream", CategoryRTSP, fmt.Errorf("%sConnection timed out", ffmpegStartup), false},

		// Reported.
		{"network code bug", "imageprovider", CategoryNetwork, fmt.Errorf("unexpected status code 500 from API"), true},
		{"weather upstream 502 after retries", "weather", CategoryNetwork, fmt.Errorf("received non-OK response (%d) after %d retries", 502, 3), true},
		{"http error text that merely contains eof", "weather", CategoryHTTP, fmt.Errorf("station lookup failed for Geoffroy: unexpected response shape"), true},
		{"network error text with typeof", "imageprovider", CategoryNetwork, fmt.Errorf("provider returned typeof mismatch in payload"), true},
		{"rtsp stream not found", "ffmpeg-stream", CategoryRTSP, fmt.Errorf("%sRTSP stream not found", ffmpegStartup), true},
		{"rtsp circuit breaker", "ffmpeg-stream", CategoryRTSP, fmt.Errorf("stream circuit breaker activated: immediate connection failures"), true},
		{"rtsp code bug", "ffmpeg-stream", CategoryRTSP, fmt.Errorf("ffmpeg produced invalid frame header at offset 42"), true},
		{"mqtt short timeout config", "mqtt", CategoryMQTTConnection, fmt.Errorf("mqtt connection timeout - connection failed with short timeout (500ms)"), true},
		{"openweather key missing", "weather", CategoryConfiguration, fmt.Errorf("OpenWeather API key not configured"), true},
		{"sqlite start failure", "datastore", CategoryDatabase,
			fmt.Errorf("open_database: database file cannot be created: %w", &os.PathError{Op: "mkdir", Path: "/data", Err: syscall.EACCES}), true},
		{"typed refused dial in an ungated category", "birdweather", CategoryIntegration, refusedDialError(syscall.ECONNREFUSED), true},
		{"job queue deadline", "analysis.jobqueue", CategoryTimeout, context.DeadlineExceeded, true},
		{"composite action timeout", "analysis.processor", CategoryTimeout, fmt.Errorf("action timed out after %v", 10*time.Second), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ee := New(tt.err).Component(tt.component).Category(tt.category).Build()
			assert.Equal(t, tt.wantReport, shouldReportToSentry(ee))
		})
	}
}

// TestShouldReportToSentry_FiltersWikipediaRateLimitNoise verifies that the
// imageprovider Wikipedia rate-limit (HTTP 429) and circuit-breaker-open
// failures are suppressed. These are built with CategoryNetwork (see
// internal/imageprovider/wikipedia.go: checkCircuitBreaker, handleHTTPStatusError,
// handleCircuitBreaker, and the diagnostic rate-limit path). The circuit breaker
// already throttles requests when Wikipedia rate-limits the client, so forwarding
// a Sentry event for every rejected request is pure noise. The exact wordings
// asserted here are real substrings of the messages those code paths produce,
// lowercased to match how shouldReportToSentry compares against
// connectivitySignatures.
func TestShouldReportToSentry_FiltersWikipediaRateLimitNoise(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantReport bool
	}{
		{
			// Direct HTTP 429 from handleHTTPStatusError:
			// "Wikipedia API returned status %d: %s". This is the original,
			// highest-volume noise (the breaker has not opened yet). 429 is
			// purely environmental throttling, so every occurrence is suppressed.
			name:       "direct HTTP 429 is suppressed",
			err:        fmt.Errorf("Wikipedia API returned status 429: too many requests"),
			wantReport: false,
		},
		{
			// Produced by checkCircuitBreaker: "Wikipedia API circuit breaker open: %s"
			// (repeated per-call rejection while the breaker is open).
			name:       "circuit breaker open rejection is suppressed",
			err:        fmt.Errorf("Wikipedia API circuit breaker open: Rate limited (HTTP 429): too many requests"),
			wantReport: false,
		},
		{
			// Produced by the diagnostic path: "Wikipedia rate limit exceeded: %s"
			name:       "rate limit exceeded diagnostic error is suppressed",
			err:        fmt.Errorf("Wikipedia rate limit exceeded: Rate limit exceeded (HTTP 429)"),
			wantReport: false,
		},
		{
			// Regression guard: a genuine server-side failure in the same
			// category must still reach Sentry so real bugs stay visible.
			name:       "genuine HTTP 500 is still reported",
			err:        fmt.Errorf("Wikipedia API returned status 500: internal server error"),
			wantReport: true,
		},
		{
			// Regression guard: a 403 user-agent policy violation is a real,
			// actionable signal (not benign throttling) and must still report.
			// This pins that the suppression is scoped to 429, not all statuses.
			name:       "HTTP 403 policy violation is still reported",
			err:        fmt.Errorf("Wikipedia API returned status 403: user-agent policy violation"),
			wantReport: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ee := New(tt.err).
				Component("imageprovider").
				Category(CategoryNetwork).
				Context("operation", "api_error").
				Build()
			got := shouldReportToSentry(ee)
			assert.Equal(t, tt.wantReport, got,
				"shouldReportToSentry(%q) = %v, want %v", tt.err, got, tt.wantReport)
		})
	}
}

// TestShouldReportToSentry_CategoryLimitNotificationOnly verifies that the
// CategoryLimit suppression is scoped to the notification component. The
// notification.PushCircuitBreaker's ErrCircuitBreakerOpen sentinel is tagged
// component="notification", and that single sentinel is what every caller
// (notification push providers AND birdweather uploads, which reuse the
// same breaker) receives, so the notification-only filter suppresses both
// in practice. Other CategoryLimit producers (eBird API quota, analysis job
// queue full, spectrogram pre-render memory limits) are legitimate
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

func TestShouldReportToSentry_RateLimitsDiskFull(t *testing.T) {
	// Not parallel: mutates package-level state
	diskFullLimiter.last.Store(0)
	t.Cleanup(func() { diskFullLimiter.last.Store(0) })

	ee1 := New(fmt.Errorf("database or disk is full")).
		Component("datastore").Category(CategoryDatabase).Build()
	ee2 := New(fmt.Errorf("database or disk is full")).
		Component("myaudio").Category(CategorySystem).Build()

	assert.True(t, shouldReportToSentry(ee1), "first disk-full should be reported")
	assert.False(t, shouldReportToSentry(ee2), "second disk-full within cooldown should be suppressed")
}

func TestShouldReportToSentry_DiskFullCooldownExpiry(t *testing.T) {
	// Not parallel: mutates package-level state
	// Simulate a disk-full report from past the cooldown window
	diskFullLimiter.last.Store(time.Now().Add(-diskFullCooldown).Unix() - 1)
	t.Cleanup(func() { diskFullLimiter.last.Store(0) })

	ee := New(fmt.Errorf("database or disk is full")).
		Component("datastore").Category(CategoryDatabase).Build()

	assert.True(t, shouldReportToSentry(ee), "disk-full should be reported after cooldown expires")
}

// TestShouldReportToSentry_FiltersDatabaseContextCancellation verifies that
// context.Canceled and context.DeadlineExceeded errors from CategoryDatabase
// operations are not forwarded to Sentry. These happen routinely during
// graceful shutdown and request-timeout conditions (e.g. HTTP client
// disconnects mid-query), and they are not code bugs. Prior behavior forwarded
// every row-iterator cancellation to Sentry, creating hundreds of duplicate
// "[database] context canceled" events per day.
func TestShouldReportToSentry_FiltersDatabaseContextCancellation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantReport bool
	}{
		{
			name:       "context.Canceled wrapped in dbError is suppressed",
			err:        fmt.Errorf("iterate rows: %w", context.Canceled),
			wantReport: false,
		},
		{
			name:       "context.DeadlineExceeded wrapped in dbError is suppressed",
			err:        fmt.Errorf("query timed out: %w", context.DeadlineExceeded),
			wantReport: false,
		},
		{
			name:       "plain context.Canceled is suppressed",
			err:        context.Canceled,
			wantReport: false,
		},
		{
			name:       "plain context.DeadlineExceeded is suppressed",
			err:        context.DeadlineExceeded,
			wantReport: false,
		},
		{
			name:       "real database error is still reported",
			err:        fmt.Errorf("no such table: notes"),
			wantReport: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ee := New(tt.err).
				Component("datastore").
				Category(CategoryDatabase).
				Context("operation", "iterate_species_summary_rows").
				Build()
			got := shouldReportToSentry(ee)
			assert.Equal(t, tt.wantReport, got,
				"shouldReportToSentry for %q = %v, want %v", tt.name, got, tt.wantReport)
		})
	}
}

// TestShouldReportToSentry_AllowsNonDatabaseContextCancellation verifies that
// the database-specific context-cancellation filter does not leak into other
// categories where a canceled context may indicate a real bug (e.g. a
// worker's supervisor cancelling at an unexpected time).
func TestShouldReportToSentry_AllowsNonDatabaseContextCancellation(t *testing.T) {
	t.Parallel()
	ee := New(fmt.Errorf("worker loop exited: %w", context.Canceled)).
		Component("birdnet").
		Category(CategoryWorker).
		Build()
	assert.True(t, shouldReportToSentry(ee),
		"context.Canceled outside CategoryDatabase should still be forwarded to Sentry")
}

// TestShouldReportToSentry_FiltersLowPrioritySystemInterruptions verifies that a
// CategorySystem error explicitly tagged PriorityLow is suppressed even when its
// surface error is not a context sentinel. The spectrogram renderer tags
// context-driven render interruptions this way using the governing context,
// because an interruption need not surface a context sentinel in its error chain,
// so isContextCancellation alone misses them.
//
// PriorityLow is honored ONLY for CategorySystem. CategoryDatabase + PriorityLow is
// deliberately NOT suppressed: the datastore tags genuine query/scan/cleanup failures
// PriorityLow for user-notification routing, and a real DB fault (corruption, lock
// contention) during such a low-priority job must still reach Sentry.
func TestShouldReportToSentry_FiltersLowPrioritySystemInterruptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		component  string
		category   ErrorCategory
		priority   string
		err        error
		wantReport bool
	}{
		{
			name:       "Windows exit-status-1 system error tagged PriorityLow is suppressed",
			component:  "spectrogram",
			category:   CategorySystem,
			priority:   PriorityLow,
			err:        fmt.Errorf("exit status 1"),
			wantReport: false,
		},
		{
			name:       "signal-killed system error tagged PriorityLow is suppressed",
			component:  "spectrogram",
			category:   CategorySystem,
			priority:   PriorityLow,
			err:        fmt.Errorf("process failed: signal: killed"),
			wantReport: false,
		},
		{
			name:       "wrapped PriorityLow system error is suppressed",
			component:  "spectrogram",
			category:   CategorySystem,
			priority:   PriorityLow,
			err:        fmt.Errorf("generate spectrogram: %w", fmt.Errorf("exit status 1")),
			wantReport: false,
		},
		{
			// Regression guard: a genuine DB fault must reach Sentry even though the
			// datastore tags it PriorityLow for notification routing.
			name:       "genuine database error tagged PriorityLow is still reported",
			component:  "datastore",
			category:   CategoryDatabase,
			priority:   PriorityLow,
			err:        fmt.Errorf("database disk image is malformed"),
			wantReport: true,
		},
		{
			name:       "database scan failure tagged PriorityLow is still reported",
			component:  "datastore",
			category:   CategoryDatabase,
			priority:   PriorityLow,
			err:        fmt.Errorf("sql: Scan error on column index 0"),
			wantReport: true,
		},
		{
			// The pre-existing context-sentinel suppression still applies to
			// CategoryDatabase (independent of priority).
			name:       "database PriorityLow context cancellation is still suppressed",
			component:  "datastore",
			category:   CategoryDatabase,
			priority:   PriorityLow,
			err:        fmt.Errorf("iterate rows: %w", context.Canceled),
			wantReport: false,
		},
		{
			name:       "system error without an explicit priority is reported",
			component:  "spectrogram",
			category:   CategorySystem,
			priority:   "",
			err:        fmt.Errorf("exit status 1"),
			wantReport: true,
		},
		{
			// Pins the predicate as == PriorityLow, not != "": a high-priority
			// system error must NOT be suppressed.
			name:       "high-priority system error is reported",
			component:  "spectrogram",
			category:   CategorySystem,
			priority:   PriorityHigh,
			err:        fmt.Errorf("exit status 1"),
			wantReport: true,
		},
		{
			name:       "PriorityLow in a non-operational category is reported",
			component:  "birdnet",
			category:   CategoryWorker,
			priority:   PriorityLow,
			err:        fmt.Errorf("exit status 1"),
			wantReport: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := New(tt.err).Component(tt.component).Category(tt.category)
			if tt.priority != "" {
				b = b.Priority(tt.priority)
			}
			ee := b.Build()
			got := shouldReportToSentry(ee)
			assert.Equal(t, tt.wantReport, got,
				"shouldReportToSentry for %q = %v, want %v", tt.name, got, tt.wantReport)
		})
	}
}

// dialTimeoutError mimics the timeout error the net package returns from a
// dial: it reads "i/o timeout" and is also context.DeadlineExceeded.
type dialTimeoutError struct{}

func (dialTimeoutError) Error() string        { return "i/o timeout" }
func (dialTimeoutError) Timeout() bool        { return true }
func (dialTimeoutError) Temporary() bool      { return true }
func (dialTimeoutError) Is(target error) bool { return target == context.DeadlineExceeded }

// TestReportError_DropsConnectivityAndMarksReported pins that connectivity
// failures never become events, that the drop marks the error reported so a
// re-wrap is not evaluated again, and that code errors keep their own level,
// origin and fingerprint.
func TestReportError_DropsConnectivityAndMarksReported(t *testing.T) {
	transport := bindMockTransport(t)

	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: dialTimeoutError{}}
	require.ErrorIs(t, dialErr, context.DeadlineExceeded, "the fixture is a context deadline like a real dial timeout")

	dial := New(dialErr).Component("datastore").Category(CategoryDatabase).Context("operation", "mysql_open").Build()
	invalidConn := New(mysql.ErrInvalidConn).Component("datastore").Category(CategoryState).Context("operation", "read_state").Build()
	codeErr := New(fmt.Errorf("state row is inconsistent")).
		Component("datastore").Category(CategoryState).Context("operation", "read_state").Build()

	reporter := NewSentryReporter(alwaysAllowed)
	reporter.ReportError(dial)
	reporter.ReportError(invalidConn)
	reporter.ReportError(codeErr)
	sentry.Flush(time.Second)

	assert.True(t, dial.IsReported(), "a dropped dial timeout is marked reported")
	assert.True(t, invalidConn.IsReported(), "a dropped invalid connection is marked reported")

	events := transport.Events()
	require.Len(t, events, 1, "only the code error is sent")
	assert.Equal(t, getErrorLevel(CategoryState), events[0].Level)
	assert.Equal(t, "code", events[0].Tags["error_origin"])
	require.GreaterOrEqual(t, len(events[0].Fingerprint), 4)
	assert.Equal(t, []string{"datastore", "state", "read_state", "error"}, events[0].Fingerprint[:4])
}

// TestBuildReportsOnlyWhileGateAllows pins that the reporter consults its gate
// per report and that RefreshActiveReporting feeds Build's fast path.
func TestBuildReportsOnlyWhileGateAllows(t *testing.T) {
	transport := bindMockTransport(t)

	var allowed atomic.Bool
	previous := GetTelemetryReporter()
	SetTelemetryReporter(NewSentryReporter(allowed.Load))
	t.Cleanup(func() {
		SetTelemetryReporter(previous)
	})

	build := func(msg string) {
		_ = New(fmt.Errorf("%s", msg)).
			Component("datastore").Category(CategoryDatabase).
			Context("operation", "gate_test").Build()
		sentry.Flush(time.Second)
	}

	allowed.Store(false)
	RefreshActiveReporting()
	build("gate closed")
	assert.Empty(t, transport.Events(), "a closed gate must not report")

	allowed.Store(true)
	RefreshActiveReporting()
	build("gate open")
	assert.Len(t, transport.Events(), 1, "an open gate with a refreshed flag must report")

	// The cached flag is now stale-true; the live check in ReportError must
	// still stop the report.
	allowed.Store(false)
	build("gate closed again")
	assert.Len(t, transport.Events(), 1, "a gate closed without a refresh must not report")
}
