package telemetry

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// gateTestURL is a placeholder request target; the fake inner round tripper never dials it.
	gateTestURL = "https://sentry.invalid/api/1/envelope/"
	// gateTestFlushBudget is the upper bound for a Flush against a suspended gate.
	gateTestFlushBudget = 500 * time.Millisecond
	// gateTestConcurrency is the number of simultaneous requests in the probe race test.
	gateTestConcurrency = 16
	// gateTestEventCount is the number of events captured in the client wiring test.
	gateTestEventCount = 50
)

// fakeInner is a scriptable inner round tripper.
type fakeInner struct {
	mu     sync.Mutex
	calls  int
	err    error
	status int
	block  chan struct{}
	enter  chan struct{}
}

func (f *fakeInner) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.calls++
	err, status, block, enter := f.err, f.status, f.block, f.enter
	f.mu.Unlock()
	if enter != nil {
		enter <- struct{}{}
	}
	if block != nil {
		<-block
	}
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func (f *fakeInner) set(err error, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err, f.status = err, status
}

func (f *fakeInner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func gateRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, gateTestURL, http.NoBody)
	require.NoError(t, err)
	return req
}

// send performs one request through the gate and closes any response body.
func send(t *testing.T, g *deliveryGate) error {
	t.Helper()
	resp, err := g.RoundTrip(gateRequest(t))
	if resp != nil {
		require.NoError(t, resp.Body.Close())
	}
	return err
}

var errGateDial = &net.OpError{Op: "dial", Net: "tcp", Err: io.ErrUnexpectedEOF}

func newTestGate(t *testing.T) (g *deliveryGate, inner *fakeInner, clock *FakeTimeSource) {
	t.Helper()
	inner = &fakeInner{status: http.StatusOK}
	clock = NewFakeTimeSource(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	return newDeliveryGate(inner, clock), inner, clock
}

// failToOpen drives the gate through deliveryFailureThreshold failures.
func failToOpen(t *testing.T, g *deliveryGate, inner *fakeInner) {
	t.Helper()
	inner.set(errGateDial, 0)
	for range deliveryFailureThreshold {
		require.NotErrorIs(t, send(t, g), errDeliverySuspended)
	}
}

func TestDeliveryGate_OpensAfterThresholdAndRefusesUntilWindowEnds(t *testing.T) {
	t.Parallel()
	g, inner, clock := newTestGate(t)

	inner.set(errGateDial, 0)
	for range deliveryFailureThreshold - 1 {
		require.Error(t, send(t, g))
	}
	assert.Equal(t, deliveryFailureThreshold-1, inner.callCount())
	require.Error(t, send(t, g))
	opened := inner.callCount()
	assert.Equal(t, deliveryFailureThreshold, opened)

	clock.Advance(deliveryInitialBackoff - time.Second)
	for range 100 {
		require.ErrorIs(t, send(t, g), errDeliverySuspended)
	}
	assert.Equal(t, opened, inner.callCount(), "no inner call while the window is open")

	clock.Advance(time.Second)
	err := send(t, g)
	require.Error(t, err)
	assert.NotErrorIs(t, err, errDeliverySuspended, "request at the deadline is the probe")
	assert.Equal(t, opened+1, inner.callCount())
}

func TestDeliveryGate_BackoffDoublesToCap(t *testing.T) {
	t.Parallel()
	g, inner, clock := newTestGate(t)
	failToOpen(t, g, inner)

	window := deliveryInitialBackoff
	for range 8 {
		clock.Advance(window - time.Second)
		before := inner.callCount()
		require.ErrorIs(t, send(t, g), errDeliverySuspended)
		assert.Equal(t, before, inner.callCount())

		clock.Advance(time.Second)
		require.NotErrorIs(t, send(t, g), errDeliverySuspended)
		assert.Equal(t, before+1, inner.callCount())

		window = min(window*deliveryBackoffFactor, deliveryMaxBackoff)
	}
	assert.Equal(t, deliveryMaxBackoff, window)

	// The next window is the cap, not a further doubling.
	clock.Advance(deliveryMaxBackoff - time.Second)
	require.ErrorIs(t, send(t, g), errDeliverySuspended)
	clock.Advance(time.Second)
	require.NotErrorIs(t, send(t, g), errDeliverySuspended)
}

func TestDeliveryGate_SuccessResetsBackoff(t *testing.T) {
	t.Parallel()
	g, inner, clock := newTestGate(t)
	failToOpen(t, g, inner)

	// Failed probe: window doubles.
	clock.Advance(deliveryInitialBackoff)
	require.NotErrorIs(t, send(t, g), errDeliverySuspended)
	clock.Advance(deliveryInitialBackoff*deliveryBackoffFactor - time.Second)
	require.ErrorIs(t, send(t, g), errDeliverySuspended)
	clock.Advance(time.Second)

	// Successful probe closes the gate.
	inner.set(nil, http.StatusOK)
	require.NoError(t, send(t, g))
	require.NoError(t, send(t, g))

	// Reopening starts from the initial window again.
	failToOpen(t, g, inner)
	clock.Advance(deliveryInitialBackoff)
	before := inner.callCount()
	require.NotErrorIs(t, send(t, g), errDeliverySuspended)
	assert.Equal(t, before+1, inner.callCount())
}

func TestDeliveryGate_HTTPStatusesCountAsReachable(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			g, inner, _ := newTestGate(t)
			inner.set(nil, status)
			for range deliveryFailureThreshold * 2 {
				require.NoError(t, send(t, g))
			}
			assert.Equal(t, deliveryFailureThreshold*2, inner.callCount())
		})
	}
}

func TestDeliveryGate_SuccessBetweenFailuresResetsCount(t *testing.T) {
	t.Parallel()
	g, inner, _ := newTestGate(t)

	for range deliveryFailureThreshold - 1 {
		inner.set(errGateDial, 0)
		require.Error(t, send(t, g))
	}
	inner.set(nil, http.StatusOK)
	require.NoError(t, send(t, g))
	inner.set(errGateDial, 0)
	for range deliveryFailureThreshold - 1 {
		require.NotErrorIs(t, send(t, g), errDeliverySuspended)
	}
	// Still closed: the next call reaches the inner round tripper.
	before := inner.callCount()
	require.NotErrorIs(t, send(t, g), errDeliverySuspended)
	assert.Equal(t, before+1, inner.callCount())
}

func TestDeliveryGate_ExpediteTurnsNextRequestIntoProbe(t *testing.T) {
	t.Parallel()
	g, inner, clock := newTestGate(t)

	// Closed: expedite changes nothing.
	g.expedite()
	inner.set(nil, http.StatusOK)
	require.NoError(t, send(t, g))

	failToOpen(t, g, inner)
	clock.Advance(5 * time.Second)
	require.ErrorIs(t, send(t, g), errDeliverySuspended)

	g.expedite()
	before := inner.callCount()
	require.NotErrorIs(t, send(t, g), errDeliverySuspended)
	assert.Equal(t, before+1, inner.callCount())
}

func TestDeliveryGate_CancelledProbeDoesNotLengthenBackoff(t *testing.T) {
	t.Parallel()
	g, inner, clock := newTestGate(t)
	failToOpen(t, g, inner)

	clock.Advance(deliveryInitialBackoff)
	inner.set(context.Canceled, 0)
	require.ErrorIs(t, send(t, g), context.Canceled)

	// The next request probes at once, and its failure uses the second window.
	inner.set(errGateDial, 0)
	before := inner.callCount()
	require.NotErrorIs(t, send(t, g), errDeliverySuspended)
	assert.Equal(t, before+1, inner.callCount())

	clock.Advance(deliveryInitialBackoff*deliveryBackoffFactor - time.Second)
	require.ErrorIs(t, send(t, g), errDeliverySuspended)
	clock.Advance(time.Second)
	require.NotErrorIs(t, send(t, g), errDeliverySuspended)
}

func TestDeliveryGate_SingleProbeUnderConcurrency(t *testing.T) {
	t.Parallel()
	g, inner, clock := newTestGate(t)
	failToOpen(t, g, inner)
	clock.Advance(deliveryInitialBackoff)

	before := inner.callCount()
	inner.mu.Lock()
	inner.err, inner.status = nil, http.StatusOK
	inner.block = make(chan struct{})
	inner.enter = make(chan struct{}, gateTestConcurrency)
	block := inner.block
	enter := inner.enter
	inner.mu.Unlock()

	results := make(chan error, gateTestConcurrency)
	var wg sync.WaitGroup
	for range gateTestConcurrency {
		wg.Go(func() { results <- send(t, g) })
	}

	<-enter // the probe is inside the inner round tripper
	suspended := 0
	for range gateTestConcurrency - 1 {
		require.ErrorIs(t, <-results, errDeliverySuspended)
		suspended++
	}
	close(block)
	wg.Wait()
	require.NoError(t, <-results)

	assert.Equal(t, gateTestConcurrency-1, suspended)
	assert.Equal(t, before+1, inner.callCount())

	// Probe succeeded: gate is closed again.
	inner.mu.Lock()
	inner.block, inner.enter = nil, nil
	inner.mu.Unlock()
	require.NoError(t, send(t, g))
}

func TestDeliveryFailureKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"dns", &net.DNSError{IsNotFound: true}, failureKindDNS},
		{"timeout", &net.DNSError{IsTimeout: true}, failureKindDNS},
		{"client timeout", context.DeadlineExceeded, failureKindTimeout},
		{"net timeout", &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}, failureKindTimeout},
		{"connection", errGateDial, failureKindConnection},
		{"other", io.ErrUnexpectedEOF, failureKindOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, deliveryFailureKind(tt.err))
		})
	}
}

func TestDeliveryGate_SentryClientDeliversThroughGate(t *testing.T) {
	t.Parallel()
	g, inner, clock := newTestGate(t)
	inner.set(&net.DNSError{IsNotFound: true}, 0)

	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:                    "https://public@sentry.invalid/1",
		DisableTelemetryBuffer: true,
		HTTPTransport:          g,
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	for range gateTestEventCount {
		client.CaptureMessage("gate test", nil, sentry.NewScope())
	}
	start := time.Now()
	require.True(t, client.Flush(time.Second))
	assert.Less(t, time.Since(start), gateTestFlushBudget)
	assert.Equal(t, deliveryFailureThreshold, inner.callCount())

	clock.Advance(deliveryInitialBackoff)
	inner.set(nil, http.StatusOK)
	const afterRecovery = 10
	for range afterRecovery {
		client.CaptureMessage("gate test", nil, sentry.NewScope())
	}
	require.True(t, client.Flush(time.Second))
	assert.Equal(t, deliveryFailureThreshold+afterRecovery, inner.callCount())
}
