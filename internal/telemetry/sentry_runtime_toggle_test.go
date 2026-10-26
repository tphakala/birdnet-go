package telemetry

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
)

const toggleFlushTimeout = 300 * time.Millisecond

// recordingRoundTripper counts the requests that reach the network layer.
type recordingRoundTripper struct {
	requests atomic.Int64
}

func (r *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	r.requests.Add(1)
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

// toggleHarness drives the real Sentry client and HTTPTransport against a
// recording RoundTripper so tests can assert on requests, not only on events.
type toggleHarness struct {
	t   *testing.T
	rec *recordingRoundTripper
	seq int
}

// newToggleHarness isolates all global state a runtime toggle touches and
// restores it when the test ends. Tests using it must not run in parallel.
func newToggleHarness(t *testing.T) *toggleHarness {
	t.Helper()
	t.Setenv(sentryDSNEnvVar, wiringTestDSN)

	h := &toggleHarness{t: t, rec: &recordingRoundTripper{}}

	prevSettings := conf.GetSettings()
	prevMode := testMode.Load()
	prevReporter := errors.GetTelemetryReporter()
	prevGate := sentryDeliveryGate
	deferredMutex.Lock()
	prevInitialized, prevUploader := sentryInitialized, attachmentUploader
	deferredMutex.Unlock()

	// Registered first so it runs last: puts the original hub client back.
	bindTestClient(t, nil)
	sentryDeliveryGate = newDeliveryGate(h.rec, RealTimeSource{})
	testMode.Store(testModeDisabled)
	errors.SetPrivacyScrubber(func(s string) string { return s })
	InitializeErrorIntegration()

	t.Cleanup(func() {
		clientLifecycleMu.Lock()
		if bound := sentry.CurrentHub().Client(); bound.IsEnabled() {
			closeClient(bound)
		}
		if fullClient != nil {
			closeClient(fullClient)
		}
		fullClient = nil
		clear(closedClients)
		clientLifecycleMu.Unlock()

		sentryDeliveryGate = prevGate
		testMode.Store(prevMode)
		conf.StoreSettings(prevSettings)
		errors.SetTelemetryReporter(prevReporter)
		deferredMutex.Lock()
		sentryInitialized, attachmentUploader = prevInitialized, prevUploader
		deferredMutex.Unlock()
		UpdateTelemetryEnabled()
	})

	h.set(false)
	return h
}

// set stores settings with the given opt-in and runs the same refresh the
// settings handler runs on save. It does not reconfigure the client.
func (h *toggleHarness) set(enabled bool) {
	h.t.Helper()
	conf.StoreSettings(&conf.Settings{
		Version: "test",
		Sentry:  conf.SentrySettings{Enabled: enabled},
	})
	UpdateTelemetryEnabled()
}

func (h *toggleHarness) reconfigure() {
	h.t.Helper()
	require.NoError(h.t, ReconfigureSentry())
}

func (h *toggleHarness) client() *sentry.Client { return sentry.CurrentHub().Client() }

// newError returns a fresh, unreported error that passes the reporter filters.
func (h *toggleHarness) newError() *errors.EnhancedError {
	h.seq++
	return errors.Newf("toggle test error %d", h.seq).
		Component("datastore").
		Category(errors.CategoryDatabase).
		Context("operation", "toggle_test").
		Build()
}

// report sends one error through every capture path and returns how many
// requests reached the network layer as a result.
func (h *toggleHarness) report() int64 {
	h.t.Helper()
	before := h.rec.requests.Load()

	errors.GetTelemetryReporter().ReportError(h.newError())

	worker, err := NewTelemetryWorker(reportingActive, nil)
	require.NoError(h.t, err)
	require.NoError(h.t, worker.ProcessEvent(h.newError()))

	CaptureMessage("toggle test message", sentry.LevelError, "toggle-test")

	h.client().Flush(toggleFlushTimeout)
	return h.rec.requests.Load() - before
}

// captureSupportEvent sends a support-tagged event, which BeforeSend always
// lets through, through client and returns the requests that resulted. Only a
// closed transport can stop it.
func (h *toggleHarness) captureSupportEvent(client *sentry.Client) int64 {
	h.t.Helper()
	before := h.rec.requests.Load()
	event := sentry.NewEvent()
	event.Message = "toggle test support event"
	event.Tags = map[string]string{eventTypeTagKey: supportDumpEventType}
	client.CaptureEvent(event, nil, nil)
	client.Flush(toggleFlushTimeout)
	return h.rec.requests.Load() - before
}

func TestSentryRuntimeToggle(t *testing.T) {
	tests := []struct {
		name string
		run  func(h *toggleHarness)
	}{
		{"enable then reconfigure reports", func(h *toggleHarness) {
			h.set(true)
			h.reconfigure()
			assert.True(h.t, sentryClientBound())
			assert.Positive(h.t, h.report(), "the harness must see traffic while enabled")
		}},
		{"disable without reconfigure stops every capture path", func(h *toggleHarness) {
			h.set(true)
			h.reconfigure()
			require.Positive(h.t, h.report())

			h.set(false)
			assert.Zero(h.t, h.report(), "an opted-out install must send nothing even before the client is closed")
		}},
		{"disable then reconfigure closes the transport", func(h *toggleHarness) {
			h.set(true)
			h.reconfigure()
			old := h.client()
			require.Positive(h.t, h.captureSupportEvent(old), "setup: the client must deliver before the opt-out")

			h.set(false)
			h.reconfigure()
			assert.False(h.t, sentryClientBound())
			assert.Zero(h.t, h.report())
			assert.Zero(h.t, h.captureSupportEvent(old), "the closed client must not send, even a support event")
		}},
		{"re-enable is idempotent", func(h *toggleHarness) {
			h.set(true)
			h.reconfigure()
			h.set(false)
			h.reconfigure()
			h.set(true)
			h.reconfigure()
			first := h.client()
			require.True(h.t, sentryClientBound())
			h.reconfigure()
			assert.Same(h.t, first, h.client(), "a second reconfigure must not replace the client")
			assert.Positive(h.t, h.report())
		}},
		{"out-of-order delivery converges to the latest setting", func(h *toggleHarness) {
			h.set(true)
			h.set(false)
			h.reconfigure()
			h.reconfigure()
			assert.False(h.t, sentryClientBound())
			assert.Zero(h.t, h.report())
		}},
		{"enable replaces the minimal support client", func(h *toggleHarness) {
			require.NoError(h.t, InitMinimalSentryForSupport("test-system-id", "test"))
			minimal := h.client()
			require.True(h.t, sentryClientBound())

			h.set(true)
			h.reconfigure()
			assert.NotSame(h.t, minimal, h.client(), "the bound client must be the full client")
			assert.Zero(h.t, h.captureSupportEvent(minimal), "the replaced minimal client must be closed")

			h.set(false)
			h.reconfigure()
			require.NoError(h.t, InitMinimalSentryForSupport("test-system-id", "test"), "support upload must work after a toggle")
			assert.True(h.t, sentryClientBound())
		}},
		{"support init replaces a closed client left bound on the base scope layer", func(h *toggleHarness) {
			h.set(true)
			h.reconfigure()
			stale := h.client()
			// Model an opt-out that raced a capture: the client was closed but the
			// hub still reports it as bound once the capture's scope layer pops.
			clientLifecycleMu.Lock()
			closeClient(stale)
			fullClient = nil
			clientLifecycleMu.Unlock()
			h.set(false)

			require.NoError(h.t, InitMinimalSentryForSupport("test-system-id", "test"))
			assert.NotSame(h.t, stale, h.client(), "a closed client must not count as bound for a support upload")
			assert.Positive(h.t, h.captureSupportEvent(h.client()), "the support client must deliver")
		}},
		{"disable closes a leftover minimal support client", func(h *toggleHarness) {
			require.NoError(h.t, InitMinimalSentryForSupport("test-system-id", "test"))
			minimal := h.client()

			h.set(true) // no reconfigure: the minimal client stays bound
			h.report()
			h.set(false)
			h.reconfigure()
			assert.False(h.t, sentryClientBound())
			assert.Zero(h.t, h.captureSupportEvent(minimal), "the leftover minimal client must be closed")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(newToggleHarness(t))
		})
	}
}

// TestReconfigureSentryConcurrentWithReporting is a race-detector test: it
// toggles and reconfigures while other goroutines report and use the client.
func TestReconfigureSentryConcurrentWithReporting(t *testing.T) {
	h := newToggleHarness(t)

	const rounds = 20
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var stopOnce sync.Once
	stopWorkers := func() {
		stopOnce.Do(func() { close(stop) })
		wg.Wait()
	}
	// Runs before the harness cleanup (LIFO) even when an assertion aborts the test.
	h.t.Cleanup(stopWorkers)
	for range 3 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				errors.GetTelemetryReporter().ReportError(errors.Newf("concurrent").
					Component("datastore").Category(errors.CategoryDatabase).Build())
				CaptureMessage("concurrent", sentry.LevelError, "toggle-test")
				_ = GetTelemetryWorker()
				_ = InitMinimalSentryForSupport("test-system-id", "test")
			}
		})
	}

	for i := range rounds {
		h.set(i%2 == 0)
		require.NoError(t, ReconfigureSentry())
	}
	stopWorkers()

	h.set(false)
	h.reconfigure()
	assert.False(t, sentryClientBound(), "the final state must match the final setting")
}

// TestTelemetryWorkerGateIsEvaluatedPerEvent pins that the worker consults its
// consent gate for every event instead of capturing the answer at construction.
func TestTelemetryWorkerGateIsEvaluatedPerEvent(t *testing.T) {
	var allowed atomic.Bool
	worker, err := NewTelemetryWorker(allowed.Load, nil)
	require.NoError(t, err)
	newEvent := func(msg string) *errors.EnhancedError {
		return errors.Newf("%s", msg).Component("datastore").Category(errors.CategoryDatabase).
			Context("operation", "gate_test").Build()
	}

	require.NoError(t, worker.ProcessEvent(newEvent("gate closed")))
	require.NoError(t, worker.ProcessBatch([]events.ErrorEvent{newEvent("batch closed")}))
	stats := worker.GetStats()
	assert.Zero(t, stats.EventsProcessed+stats.EventsDropped+stats.EventsFailed, "a closed gate must skip before any accounting")

	allowed.Store(true)
	require.NoError(t, worker.ProcessEvent(newEvent("gate open")))
	assert.Equal(t, uint64(1), worker.GetStats().EventsProcessed, "an open gate must process the event")

	allowed.Store(false)
	require.NoError(t, worker.ProcessEvent(newEvent("gate closed again")))
	assert.Equal(t, uint64(1), worker.GetStats().EventsProcessed, "re-closing the gate must stop processing")
}

// TestUploadSupportDumpHoldsClientLifecycleLock pins that an upload keeps the
// client lifecycle lock until it finishes, so an opt-out cannot close the client
// under a queued dump.
func TestUploadSupportDumpHoldsClientLifecycleLock(t *testing.T) {
	h := newToggleHarness(t)
	require.NoError(t, InitMinimalSentryForSupport("test-system-id", "test"))

	clientLifecycleMu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- NewAttachmentUploader(true).UploadSupportDump(t.Context(), []byte("dump"), "test-system-id", "", "")
	}()
	select {
	case err := <-done:
		clientLifecycleMu.Unlock()
		require.Failf(t, "upload ran without the lifecycle lock", "returned %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	clientLifecycleMu.Unlock()
	require.NoError(t, <-done)
	assert.Positive(t, h.rec.requests.Load(), "the dump must be delivered once the lock is released")
}
