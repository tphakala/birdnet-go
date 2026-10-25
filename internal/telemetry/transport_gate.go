package telemetry

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// deliveryFailureThreshold is the number of consecutive transport errors
	// that suspends delivery to the Sentry ingest host.
	deliveryFailureThreshold = 3
	// deliveryInitialBackoff is the first suspension window.
	deliveryInitialBackoff = time.Minute
	// deliveryMaxBackoff caps the suspension window.
	deliveryMaxBackoff = time.Hour
	// deliveryBackoffFactor multiplies the window after each failed probe.
	deliveryBackoffFactor = 2
)

// Failure kinds logged when delivery is suspended. The raw error is never
// logged because it can carry the ingest URL and the resolver address.
const (
	failureKindDNS        = "dns"
	failureKindTimeout    = "timeout"
	failureKindConnection = "connection"
	failureKindOther      = "other"
)

// errDeliverySuspended is returned for requests refused while the gate is open.
// It is a plain sentinel and must never be built with Build(), which would
// publish it to telemetry from inside the telemetry request path.
var errDeliverySuspended = errors.NewStd("sentry delivery suspended while the ingest host is unreachable")

// sentryDeliveryGate is shared by every sentry.Init call so that the main and
// the minimal support client use the same state. The inner transport mirrors
// the one sentry-go builds by default for options without HTTPProxy,
// HTTPSProxy or CaCerts; adding any of those options means building this
// transport from them.
var sentryDeliveryGate = newDeliveryGate(&http.Transport{Proxy: http.ProxyFromEnvironment}, RealTimeSource{})

// gateState is the circuit state of a deliveryGate.
type gateState int

const (
	gateClosed   gateState = iota // requests pass through
	gateOpen                      // requests are refused until retryAt
	gateHalfOpen                  // one probe request is in flight
)

// deliveryGate is an http.RoundTripper that stops dialing the Sentry ingest
// host after repeated network errors. sentry-go has no backoff for network
// errors, so without it every queued event costs fresh DNS lookups (GitHub
// #4813). Any HTTP response, whatever its status, counts as reachable.
// Transitions are lazy: no timer or goroutine, and the lock is never held
// across the inner round trip or a log call.
type deliveryGate struct {
	next  http.RoundTripper
	clock TimeSource

	mu                  sync.Mutex
	state               gateState
	consecutiveFailures int
	backoff             time.Duration
	retryAt             time.Time
	openedAt            time.Time
	dropped             uint64
}

// newDeliveryGate wraps next with a closed gate.
func newDeliveryGate(next http.RoundTripper, clock TimeSource) *deliveryGate {
	return &deliveryGate{next: next, clock: clock, backoff: deliveryInitialBackoff}
}

// RoundTrip implements http.RoundTripper.
func (g *deliveryGate) RoundTrip(req *http.Request) (*http.Response, error) {
	probe, allowed := g.admit()
	if !allowed {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, errDeliverySuspended
	}

	resp, err := g.next.RoundTrip(req)
	g.record(err, probe)
	return resp, err
}

// admit decides whether a request may reach the network and whether it is the
// probe that tests an open gate.
func (g *deliveryGate) admit() (probe, allowed bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch g.state {
	case gateOpen:
		if g.clock.Now().Before(g.retryAt) {
			g.dropped++
			return false, false
		}
		g.state = gateHalfOpen
		return true, true
	case gateHalfOpen:
		g.dropped++
		return false, false
	default:
		return false, true
	}
}

// record applies the outcome of an inner round trip and logs at most one
// transition after releasing the lock.
func (g *deliveryGate) record(err error, probe bool) {
	var logFn func()

	g.mu.Lock()
	now := g.clock.Now()
	// A success from an earlier request can close the gate while the probe is
	// still in flight; the probe's outcome then counts like any other request.
	probe = probe && g.state == gateHalfOpen
	switch {
	case err == nil:
		if g.state != gateClosed {
			suspendedFor, dropped := now.Sub(g.openedAt), g.dropped
			g.state = gateClosed
			logFn = func() {
				GetLogger().Info("Sentry ingest host reachable again, telemetry delivery resumed",
					logger.Duration("suspended_for", suspendedFor),
					logger.Uint64("dropped_requests", dropped))
			}
		}
		g.consecutiveFailures = 0
		g.backoff = deliveryInitialBackoff
	case errors.Is(err, context.Canceled):
		// Not a reachability signal; let the next request probe again.
		if probe {
			g.state = gateOpen
			g.retryAt = now
		}
	case probe:
		g.backoff = min(g.backoff*deliveryBackoffFactor, deliveryMaxBackoff)
		g.state = gateOpen
		g.retryAt = now.Add(g.backoff)
		kind, retryIn := deliveryFailureKind(err), g.backoff
		logFn = func() {
			GetLogger().Debug("Sentry ingest host still unreachable",
				logger.String("failure_kind", kind),
				logger.Duration("retry_in", retryIn))
		}
	case g.state == gateClosed:
		g.consecutiveFailures++
		if g.consecutiveFailures >= deliveryFailureThreshold {
			g.state = gateOpen
			g.backoff = deliveryInitialBackoff
			g.retryAt = now.Add(g.backoff)
			g.openedAt = now
			g.dropped = 0
			kind, failures, retryIn := deliveryFailureKind(err), g.consecutiveFailures, g.backoff
			logFn = func() {
				GetLogger().Warn("Sentry ingest host unreachable, pausing telemetry delivery",
					logger.String("failure_kind", kind),
					logger.Int("consecutive_failures", failures),
					logger.Duration("retry_in", retryIn))
			}
		}
	}
	g.mu.Unlock()

	if logFn != nil {
		logFn()
	}
}

// expedite makes the next request through an open gate a probe. A
// user-requested upload should test the host now instead of waiting out a
// backoff left by earlier failures. It does nothing while the gate is closed.
func (g *deliveryGate) expedite() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == gateOpen {
		g.retryAt = g.clock.Now()
	}
}

// deliveryFailureKind classifies a transport error for logging without
// exposing its text.
func deliveryFailureKind(err error) string {
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return failureKindDNS
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failureKindTimeout
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return failureKindTimeout
	}
	if _, ok := errors.AsType[*net.OpError](err); ok {
		return failureKindConnection
	}
	return failureKindOther
}
