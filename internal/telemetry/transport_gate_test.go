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
	// gateTestWaitBudget only guards against a hang; no test asserts that work finishes faster.
	gateTestWaitBudget = 10 * time.Second
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
	require.NotErrorIs(t, err, errDeliverySuspended, "request at the deadline is the probe")
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
	block := make(chan struct{})
	enter := make(chan struct{}, gateTestConcurrency)
	inner.mu.Lock()
	inner.err, inner.status = nil, http.StatusOK
	inner.block, inner.enter = block, enter
	inner.mu.Unlock()
	// Release any probe still held if an assertion below fails.
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(block) }) })

	results := make(chan error, gateTestConcurrency)
	for range gateTestConcurrency {
		req := gateRequest(t)
		go func() {
			resp, err := g.RoundTrip(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			results <- err
		}()
	}

	select {
	case <-enter: // the probe is inside the inner round tripper
	case <-time.After(gateTestWaitBudget):
		require.FailNow(t, "no request reached the inner round tripper")
	}
	suspended := 0
	for range gateTestConcurrency - 1 {
		select {
		case err := <-results:
			require.ErrorIs(t, err, errDeliverySuspended)
			suspended++
		case <-time.After(gateTestWaitBudget):
			require.FailNow(t, "a request was neither refused nor finished while the probe was held")
		}
	}
	assert.Equal(t, gateTestConcurrency-1, suspended)
	assert.Equal(t, before+1, inner.callCount())

	release.Do(func() { close(block) })
	select {
	case err := <-results:
		require.NoError(t, err)
	case <-time.After(gateTestWaitBudget):
		require.FailNow(t, "the probe did not finish after release")
	}

	// Probe succeeded: gate is closed again.
	inner.mu.Lock()
	inner.block, inner.enter = nil, nil
	inner.mu.Unlock()
	require.NoError(t, send(t, g))
}

func TestDeliveryGate_CancelledRequestDoesNotCountTowardThreshold(t *testing.T) {
	t.Parallel()
	g, inner, _ := newTestGate(t)

	inner.set(errGateDial, 0)
	for range deliveryFailureThreshold - 1 {
		require.NotErrorIs(t, send(t, g), errDeliverySuspended)
	}
	inner.set(context.Canceled, 0)
	require.ErrorIs(t, send(t, g), context.Canceled)

	// Still closed: the next request reaches the inner round tripper.
	before := inner.callCount()
	require.NotErrorIs(t, send(t, g), errDeliverySuspended)
	assert.Equal(t, before+1, inner.callCount())
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
	require.True(t, client.Flush(gateTestWaitBudget))
	assert.Equal(t, deliveryFailureThreshold, inner.callCount())

	clock.Advance(deliveryInitialBackoff)
	inner.set(nil, http.StatusOK)
	const afterRecovery = 10
	for range afterRecovery {
		client.CaptureMessage("gate test", nil, sentry.NewScope())
	}
	require.True(t, client.Flush(gateTestWaitBudget))
	assert.Equal(t, deliveryFailureThreshold+afterRecovery, inner.callCount())
}

// scriptedInner answers each call from a per-call channel, so a test can
// finish overlapping requests in a chosen order.
type scriptedInner struct {
	mu      sync.Mutex
	entered chan int
	results []chan error
	next    int
}

func newScriptedInner(calls int) *scriptedInner {
	s := &scriptedInner{entered: make(chan int, calls)}
	for range calls {
		s.results = append(s.results, make(chan error, 1))
	}
	return s
}

func (s *scriptedInner) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	idx := s.next
	s.next++
	s.mu.Unlock()
	s.entered <- idx
	if err := <-s.results[idx]; err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

func TestDeliveryGate_StaleSuccessDoesNotLetFailedProbeReopenGate(t *testing.T) {
	t.Parallel()
	// Calls: 0 slow request admitted while closed, 1 to 3 failures that open
	// the gate, 4 the probe, 5 a request after the probe resolves.
	const (
		slowCall  = 0
		probeCall = deliveryFailureThreshold + 1
		lastCall  = probeCall + 1
	)
	inner := newScriptedInner(lastCall + 1)
	clock := NewFakeTimeSource(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	g := newDeliveryGate(inner, clock)
	// Release any request still held in the inner round tripper if an assertion fails.
	t.Cleanup(func() {
		for _, ch := range inner.results {
			select {
			case ch <- nil:
			default:
			}
		}
	})

	var wg sync.WaitGroup
	start := func() <-chan error {
		done := make(chan error, 1)
		req := gateRequest(t)
		wg.Go(func() {
			resp, err := g.RoundTrip(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			done <- err
		})
		return done
	}

	slowDone := start()
	require.Equal(t, slowCall, recvWithin(t, inner.entered))
	for i := 1; i <= deliveryFailureThreshold; i++ {
		done := start()
		require.Equal(t, i, recvWithin(t, inner.entered))
		inner.results[i] <- errGateDial
		require.Error(t, recvWithin(t, done))
	}

	clock.Advance(deliveryInitialBackoff)
	probeDone := start()
	require.Equal(t, probeCall, recvWithin(t, inner.entered))

	// The stale request succeeds while the probe is still in flight, then the probe fails.
	inner.results[slowCall] <- nil
	require.NoError(t, recvWithin(t, slowDone))
	inner.results[probeCall] <- errGateDial
	require.Error(t, recvWithin(t, probeDone))

	// One failure after the reset is below the threshold: the gate is still
	// closed, so this request reaches the inner round tripper.
	inner.results[lastCall] <- nil
	resp, err := g.RoundTrip(gateRequest(t))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	wg.Wait()
}

// recvWithin receives from ch and fails the test instead of hanging when
// nothing arrives within the hang guard.
func recvWithin[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(gateTestWaitBudget):
		require.FailNow(t, "timed out waiting for the gate or the inner round tripper")
	}
	var zero T
	return zero
}

func TestDeliveryGate_LateOutcomeOfSupersededProbeIsIgnored(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		lateA error
	}{
		{"late failure", errGateDial},
		{"late success", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Calls: 0 stale request admitted while closed, 1 to 3 failures
			// that open the gate, 4 probe A, 5 to 7 failures that reopen the
			// gate after a stale success closed it, 8 probe B.
			const (
				staleCall  = 0
				probeACall = deliveryFailureThreshold + 1
				reopenFrom = probeACall + 1
				probeBCall = reopenFrom + deliveryFailureThreshold
			)
			inner := newScriptedInner(probeBCall + 1)
			clock := NewFakeTimeSource(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			g := newDeliveryGate(inner, clock)
			t.Cleanup(func() {
				for _, ch := range inner.results {
					select {
					case ch <- nil:
					default:
					}
				}
			})

			var wg sync.WaitGroup
			start := func(call int) <-chan error {
				done := make(chan error, 1)
				req := gateRequest(t)
				wg.Go(func() {
					resp, err := g.RoundTrip(req)
					if resp != nil {
						_ = resp.Body.Close()
					}
					done <- err
				})
				require.Equal(t, call, recvWithin(t, inner.entered))
				return done
			}
			fail := func(call int) {
				done := start(call)
				inner.results[call] <- errGateDial
				require.Error(t, recvWithin(t, done))
			}
			state := func() (gateState, time.Duration) {
				g.mu.Lock()
				defer g.mu.Unlock()
				return g.state, g.backoff
			}

			staleDone := start(staleCall)
			for i := 1; i <= deliveryFailureThreshold; i++ {
				fail(i)
			}
			clock.Advance(deliveryInitialBackoff)
			aDone := start(probeACall)

			// A stale success closes the gate while probe A is in flight, then
			// failures reopen it and probe B is admitted.
			inner.results[staleCall] <- nil
			require.NoError(t, recvWithin(t, staleDone))
			for i := reopenFrom; i < reopenFrom+deliveryFailureThreshold; i++ {
				fail(i)
			}
			clock.Advance(deliveryInitialBackoff)
			bDone := start(probeBCall)

			// A's late outcome must not touch B's window.
			inner.results[probeACall] <- tt.lateA
			if tt.lateA == nil {
				require.NoError(t, recvWithin(t, aDone))
			} else {
				require.Error(t, recvWithin(t, aDone))
			}
			gotState, gotBackoff := state()
			assert.Equal(t, gateHalfOpen, gotState)
			assert.Equal(t, deliveryInitialBackoff, gotBackoff)

			// B alone decides: its failure doubles the backoff once.
			inner.results[probeBCall] <- errGateDial
			require.Error(t, recvWithin(t, bDone))
			gotState, gotBackoff = state()
			assert.Equal(t, gateOpen, gotState)
			assert.Equal(t, deliveryInitialBackoff*deliveryBackoffFactor, gotBackoff)
			wg.Wait()
		})
	}
}
