package notification

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// enhancedProvider wraps a provider with circuit breaker and metrics.
type enhancedProvider struct {
	prov           Provider
	circuitBreaker *PushCircuitBreaker
	filter         conf.PushFilterConfig
	name           string
}

// initializeEnhancedProviders creates enhanced providers with circuit breakers and metrics.
func (d *pushDispatcher) initializeEnhancedProviders(settings *conf.Settings, notificationMetrics *metrics.NotificationMetrics) []enhancedProvider {
	var enhanced []enhancedProvider

	// Get circuit breaker config from settings or use defaults
	cbConfig := DefaultCircuitBreakerConfig()
	if settings.Notification.Push.CircuitBreaker.Enabled {
		cbConfig.MaxFailures = settings.Notification.Push.CircuitBreaker.MaxFailures
		cbConfig.Timeout = settings.Notification.Push.CircuitBreaker.Timeout
		cbConfig.HalfOpenMaxRequests = settings.Notification.Push.CircuitBreaker.HalfOpenMaxRequests
	}

	for _, pc := range settings.Notification.Push.Providers {
		prov := buildProvider(pc)
		if prov == nil {
			continue
		}

		if err := prov.ValidateConfig(); err != nil {
			if d.log != nil {
				d.log.Error("push provider config invalid", "name", pc.Name, "type", pc.Type, "error", err)
			}
			continue
		}

		if prov.IsEnabled() {
			name := prov.GetName()

			// Create circuit breaker for this provider
			var cb *PushCircuitBreaker
			if settings.Notification.Push.CircuitBreaker.Enabled {
				cb = NewPushCircuitBreaker(cbConfig, notificationMetrics, name)
			}

			ep := enhancedProvider{
				prov:           prov,
				circuitBreaker: cb,
				filter:         pc.Filter,
				name:           name,
			}

			enhanced = append(enhanced, ep)

			if d.log != nil {
				d.log.Debug("registered enhanced push provider",
					"name", name,
					"circuit_breaker", cb != nil,
					"types", pc.Filter.Types,
					"priorities", pc.Filter.Priorities)
			}
		}
	}

	return enhanced
}

// dispatchEnhanced dispatches notifications with metrics and circuit breaker support.
func (d *pushDispatcher) dispatchEnhanced(ctx context.Context, notif *Notification, ep enhancedProvider, notificationMetrics *metrics.NotificationMetrics) {
	// Increment dispatch total
	if notificationMetrics != nil {
		notificationMetrics.IncrementDispatchTotal()
		notificationMetrics.SetDispatchActive(1) // This should be properly tracked with a counter
		defer notificationMetrics.SetDispatchActive(0)
	}

	attempts := 0
	notifType := string(notif.Type)

	for {
		attempts++

		// Set timeout per attempt
		attemptCtx := ctx
		var cancel context.CancelFunc
		if deadline := d.defaultTimeout; deadline > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, deadline)
		}

		// Start timer for metrics
		timer := time.Now()

		// Send through circuit breaker if enabled
		var err error
		if ep.circuitBreaker != nil {
			err = ep.circuitBreaker.Call(attemptCtx, func(ctx context.Context) error {
				return ep.prov.Send(ctx, notif)
			})
		} else {
			err = ep.prov.Send(attemptCtx, notif)
		}

		duration := time.Since(timer)

		// Release timeout context
		if cancel != nil {
			cancel()
		}

		// Record metrics
		if notificationMetrics != nil {
			if err == nil {
				notificationMetrics.RecordDelivery(ep.name, notifType, "success", duration)
				if attempts > 1 {
					notificationMetrics.RecordRetrySuccess(ep.name)
				}
			} else if errors.Is(err, ErrCircuitBreakerOpen) {
				// Circuit breaker is open - don't count as delivery failure
				notificationMetrics.RecordDelivery(ep.name, notifType, "circuit_open", duration)
			} else if ctx.Err() == context.DeadlineExceeded {
				notificationMetrics.RecordDelivery(ep.name, notifType, "timeout", duration)
				notificationMetrics.RecordTimeout(ep.name)
				notificationMetrics.RecordDeliveryError(ep.name, notifType, "timeout")
			} else {
				notificationMetrics.RecordDelivery(ep.name, notifType, "error", duration)
				errorCategory := categorizeError(err)
				notificationMetrics.RecordDeliveryError(ep.name, notifType, errorCategory)
			}
		}

		// Success case
		if err == nil {
			if d.log != nil {
				d.log.Info("push sent",
					"provider", ep.name,
					"id", notif.ID,
					"type", notifType,
					"priority", string(notif.Priority),
					"attempt", attempts,
					"elapsed", duration)
			}
			return
		}

		// Circuit breaker open - don't retry
		if errors.Is(err, ErrCircuitBreakerOpen) {
			if d.log != nil {
				d.log.Warn("push blocked by circuit breaker",
					"provider", ep.name,
					"id", notif.ID)
			}
			return
		}

		// Classify error for retry
		var perr *providerError
		retryable := true
		if errors.As(err, &perr) {
			retryable = perr.Retryable
		}

		// Check if we should retry
		if !retryable || attempts > d.maxRetries {
			if d.log != nil {
				d.log.Error("push send failed",
					"provider", ep.name,
					"attempts", attempts,
					"error", err,
					"retryable", retryable)
			}
			return
		}

		// Record retry attempt
		if notificationMetrics != nil {
			notificationMetrics.RecordRetryAttempt(ep.name)
		}

		// Wait for retry delay with context cancellation check
		select {
		case <-ctx.Done():
			if d.log != nil {
				d.log.Debug("retry cancelled due to context cancellation",
					"provider", ep.name,
					"attempts", attempts)
			}
			return
		case <-time.After(d.retryDelay):
			// Continue to next retry
		}
	}
}

// categorizeError categorizes errors for metrics.
func categorizeError(err error) string {
	if err == nil {
		return "none"
	}

	errStr := err.Error()

	// Check for common error patterns
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case containsAny(errStr, "network", "connection", "dial", "lookup"):
		return "network"
	case containsAny(errStr, "validation", "invalid", "malformed"):
		return "validation"
	case containsAny(errStr, "permission", "unauthorized", "forbidden"):
		return "permission"
	case containsAny(errStr, "not found", "404"):
		return "not_found"
	default:
		return "provider_error"
	}
}

// containsAny checks if a string contains any of the given substrings.
func containsAny(s string, substrs ...string) bool {
	for _, substr := range substrs {
		if substr != "" && len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}

// matchesFilterEnhanced checks if notification matches filter and records metrics.
func matchesFilterEnhanced(filter *conf.PushFilterConfig, notif *Notification, providerName string, notificationMetrics *metrics.NotificationMetrics) bool {
	if filter == nil {
		return true
	}

	// Type filter
	if len(filter.Types) > 0 {
		found := false
		for _, t := range filter.Types {
			if t == string(notif.Type) {
				found = true
				break
			}
		}
		if !found {
			if notificationMetrics != nil {
				notificationMetrics.RecordFilterRejection(providerName, "type_mismatch")
			}
			return false
		}
		if notificationMetrics != nil {
			notificationMetrics.RecordFilterMatch(providerName, "type")
		}
	}

	// Priority filter
	if len(filter.Priorities) > 0 {
		found := false
		for _, p := range filter.Priorities {
			if p == string(notif.Priority) {
				found = true
				break
			}
		}
		if !found {
			if notificationMetrics != nil {
				notificationMetrics.RecordFilterRejection(providerName, "priority_mismatch")
			}
			return false
		}
		if notificationMetrics != nil {
			notificationMetrics.RecordFilterMatch(providerName, "priority")
		}
	}

	// Component filter
	if len(filter.Components) > 0 {
		found := false
		for _, c := range filter.Components {
			if c == notif.Component {
				found = true
				break
			}
		}
		if !found {
			if notificationMetrics != nil {
				notificationMetrics.RecordFilterRejection(providerName, "component_mismatch")
			}
			return false
		}
		if notificationMetrics != nil {
			notificationMetrics.RecordFilterMatch(providerName, "component")
		}
	}

	// Metadata filters
	if len(filter.MetadataFilters) > 0 {
		if notificationMetrics != nil {
			notificationMetrics.RecordFilterMatch(providerName, "metadata")
		}
	}

	return true
}
