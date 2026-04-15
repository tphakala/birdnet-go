// circuit_breaker.go provides circuit breaker integration for the BirdWeather API client.
//
// BirdWeather uploads depend on external connectivity (app.birdweather.com). Transient
// outages, DNS failures, and TLS timeouts produced hundreds of Sentry events before this
// was added because each detection triggered a full retry against an already-failing
// endpoint. The circuit breaker short-circuits those attempts after a configurable
// number of consecutive failures and lets the service recover on its own timer.
//
// The implementation reuses notification.PushCircuitBreaker so behaviour and metrics
// match the webhook push path and we only have one state machine to reason about.
package birdweather

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// Circuit breaker tuning for outbound BirdWeather calls.
//
// These values are intentionally more relaxed than the webhook push defaults:
//
//   - BirdWeather uploads are scheduled one-per-detection (potentially many per minute),
//     so MaxConsecutiveFailures=5 trips the breaker after ~5 consecutive bad uploads.
//   - ResetTimeout=10 minutes keeps the service quiet while the remote API cools down,
//     avoiding a thundering herd against a flapping endpoint while still recovering
//     quickly once the network stabilises.
const (
	// BwMaxConsecutiveFailures is the consecutive failure count that trips the breaker.
	BwMaxConsecutiveFailures = 5

	// BwResetTimeout is how long the breaker stays open before allowing a probe request.
	BwResetTimeout = 10 * time.Minute

	// BwHalfOpenMaxRequests is the probe budget allowed while half-open. Conservative
	// at 1 so we don't flood a still-recovering endpoint.
	BwHalfOpenMaxRequests = 1

	// bwCircuitBreakerProvider is the provider name used in log/metric tags.
	bwCircuitBreakerProvider = "birdweather"
)

// defaultBirdWeatherCircuitBreakerConfig returns the circuit breaker configuration
// used by BirdWeather clients unless tests override it.
func defaultBirdWeatherCircuitBreakerConfig() notification.CircuitBreakerConfig {
	return notification.CircuitBreakerConfig{
		MaxFailures:         BwMaxConsecutiveFailures,
		Timeout:             BwResetTimeout,
		HalfOpenMaxRequests: BwHalfOpenMaxRequests,
	}
}

// callWithCircuitBreaker wraps fn with the client's circuit breaker, if one is
// attached. When the breaker is open it returns ErrCircuitBreakerOpen without
// invoking fn. When the breaker is absent (legacy construction in tests), fn is
// executed directly. The error returned by fn is also returned unchanged so the
// caller can continue to classify BirdWeather-specific errors.
//
// ctx must already carry any per-operation timeout; this helper does not add one.
func (b *BwClient) callWithCircuitBreaker(ctx context.Context, fn func(context.Context) error) error {
	if b == nil || b.circuitBreaker == nil {
		return fn(ctx)
	}
	return b.circuitBreaker.Call(ctx, fn)
}

// isCircuitBreakerOpen reports whether err was produced because the breaker is
// open (or half-open and already saturated). The check unwraps wrapped errors
// so callers can detect the condition even after additional context has been
// layered on top by the telemetry integration.
func isCircuitBreakerOpen(err error) bool {
	return errors.Is(err, notification.ErrCircuitBreakerOpen) ||
		errors.Is(err, notification.ErrTooManyRequests)
}

// CircuitBreakerState returns the current circuit breaker state. Exposed for
// observability and tests. Returns StateClosed when no breaker is attached so
// callers do not have to handle a nil breaker separately.
func (b *BwClient) CircuitBreakerState() notification.CircuitState {
	if b == nil || b.circuitBreaker == nil {
		return notification.StateClosed
	}
	return b.circuitBreaker.State()
}
