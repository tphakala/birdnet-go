package notification

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// pushDispatcher routes notifications to enabled providers based on filters
// It subscribes to the notification service and forwards notifications asynchronously.
type pushDispatcher struct {
	providers      []enhancedProvider
	log            *slog.Logger
	enabled        bool
	maxRetries     int
	retryDelay     time.Duration
	defaultTimeout time.Duration
	cancel         context.CancelFunc
	mu             sync.RWMutex
	metrics        *metrics.NotificationMetrics
	healthChecker  *HealthChecker
	rateLimiter    *PushRateLimiter
}

// enhancedProvider wraps a provider with circuit breaker and metrics.
type enhancedProvider struct {
	prov           Provider
	circuitBreaker *PushCircuitBreaker
	filter         conf.PushFilterConfig
	name           string
}

var (
	globalPushDispatcher *pushDispatcher
	dispatcherOnce       sync.Once
)

// InitializePushFromConfig builds and starts the push dispatcher using app settings.
// The notificationMetrics parameter is optional and can be nil for backward compatibility.
func InitializePushFromConfig(settings *conf.Settings) error {
	return InitializePushFromConfigWithMetrics(settings, nil)
}

// InitializePushFromConfigWithMetrics builds and starts the push dispatcher using app settings.
// It accepts an optional metrics instance to avoid import cycles.
func InitializePushFromConfigWithMetrics(settings *conf.Settings, notificationMetrics *metrics.NotificationMetrics) error {
	var initErr error
	dispatcherOnce.Do(func() {
		if settings == nil || !settings.Notification.Push.Enabled {
			return
		}

		pd := &pushDispatcher{
			log:            getFileLogger(settings.Debug),
			enabled:        settings.Notification.Push.Enabled,
			maxRetries:     settings.Notification.Push.MaxRetries,
			retryDelay:     settings.Notification.Push.RetryDelay,
			defaultTimeout: settings.Notification.Push.DefaultTimeout,
			metrics:        notificationMetrics,
		}

		// Initialize health checker if enabled
		if settings.Notification.Push.HealthCheck.Enabled {
			hcConfig := HealthCheckConfig{
				Enabled:  settings.Notification.Push.HealthCheck.Enabled,
				Interval: settings.Notification.Push.HealthCheck.Interval,
				Timeout:  settings.Notification.Push.HealthCheck.Timeout,
			}
			pd.healthChecker = NewHealthChecker(hcConfig, pd.log, notificationMetrics)
		}

		// Initialize rate limiter if enabled
		if settings.Notification.Push.RateLimiting.Enabled {
			rlConfig := PushRateLimiterConfig{
				RequestsPerMinute: settings.Notification.Push.RateLimiting.RequestsPerMinute,
				BurstSize:         settings.Notification.Push.RateLimiting.BurstSize,
			}
			pd.rateLimiter = NewPushRateLimiter(rlConfig)
			if pd.log != nil {
				pd.log.Info("rate limiter enabled",
					"requests_per_minute", rlConfig.RequestsPerMinute,
					"burst_size", rlConfig.BurstSize)
			}
		}

		// Build enhanced providers with circuit breakers
		pd.providers = pd.initializeEnhancedProviders(settings, notificationMetrics)

		// Register providers with health checker
		if pd.healthChecker != nil {
			for i := range pd.providers {
				ep := &pd.providers[i]
				pd.healthChecker.RegisterProvider(ep.prov, ep.circuitBreaker)
			}
		}

		globalPushDispatcher = pd

		// Move start() inside Once to prevent race conditions
		if pd.enabled && len(pd.providers) > 0 {
			if err := pd.start(); err != nil {
				pd.log.Error("failed to start push dispatcher", "error", err)
				initErr = err
			}
		}
	})

	return initErr
}

// GetPushDispatcher returns the dispatcher if initialized
func GetPushDispatcher() *pushDispatcher { return globalPushDispatcher }

func (d *pushDispatcher) start() error {
	if !d.enabled {
		return nil
	}
	if d.cancel != nil {
		return nil // already started
	}
	if len(d.providers) == 0 {
		d.log.Info("push notifications enabled but no providers configured")
		return nil
	}

	service := GetService()
	if service == nil {
		return fmt.Errorf("notification service not initialized")
	}

	ch, ctx := service.Subscribe()
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel

	go func() {
		defer service.Unsubscribe(ch)
		for {
			select {
			case notif, ok := <-ch:
				if !ok || notif == nil {
					return
				}
				// Skip ephemeral toast notifications
				if isToastNotification(notif) {
					continue
				}
				// Dispatch in background
				go d.dispatch(ctx, notif)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start health checker if enabled
	if d.healthChecker != nil {
		if err := d.healthChecker.Start(ctx); err != nil {
			d.log.Error("failed to start health checker", "error", err)
			// Non-fatal, continue with dispatcher
		}
	}

	d.log.Info("push dispatcher started",
		"providers", len(d.providers),
		"health_checker", d.healthChecker != nil,
		"rate_limiter", d.rateLimiter != nil)
	return nil
}

func (d *pushDispatcher) dispatch(ctx context.Context, notif *Notification) {
	for i := range d.providers {
		ep := &d.providers[i]
		if !ep.prov.IsEnabled() || !ep.prov.SupportsType(notif.Type) {
			continue
		}
		// Apply filter with metrics tracking
		if !d.matchesFilter(ep, notif) {
			continue
		}

		// Run each provider in its own goroutine to avoid head-of-line blocking
		go d.dispatchEnhanced(ctx, notif, ep)
	}
}

// matchesFilter checks if notification matches provider filter and records metrics.
func (d *pushDispatcher) matchesFilter(ep *enhancedProvider, notif *Notification) bool {
	// Use legacy filter logic for backward compatibility
	matches := MatchesProviderFilter(&ep.filter, notif, d.log, ep.name)

	// Record filter metrics
	if d.metrics != nil {
		if matches {
			d.metrics.RecordFilterMatch(ep.name, "all")
		} else {
			d.metrics.RecordFilterRejection(ep.name, "filter_mismatch")
		}
	}

	return matches
}

// dispatchEnhanced dispatches notifications with metrics and circuit breaker support.
func (d *pushDispatcher) dispatchEnhanced(ctx context.Context, notif *Notification, ep *enhancedProvider) {
	// Apply rate limiting if enabled
	if !d.checkRateLimit(ep, notif) {
		return
	}

	// Increment dispatch total
	if d.metrics != nil {
		d.metrics.IncrementDispatchTotal()
		d.metrics.SetDispatchActive(1)
		defer d.metrics.SetDispatchActive(0)
	}

	d.retryLoop(ctx, notif, ep)
}

// checkRateLimit checks if notification is rate limited.
func (d *pushDispatcher) checkRateLimit(ep *enhancedProvider, notif *Notification) bool {
	if d.rateLimiter != nil && !d.rateLimiter.Allow() {
		if d.log != nil {
			d.log.Warn("notification rate limited",
				"provider", ep.name,
				"notification_id", notif.ID)
		}
		if d.metrics != nil {
			d.metrics.RecordFilterRejection(ep.name, "rate_limited")
		}
		return false
	}
	return true
}

// retryLoop handles the retry logic for sending notifications.
func (d *pushDispatcher) retryLoop(ctx context.Context, notif *Notification, ep *enhancedProvider) {
	attempts := 0
	notifType := string(notif.Type)

	for {
		attempts++
		duration, err := d.attemptSend(ctx, notif, ep)

		// Record metrics
		d.recordAttemptMetrics(ep.name, notifType, err, duration, attempts)

		// Handle success
		if err == nil {
			d.logSuccess(ep.name, notif, notifType, attempts, duration)
			return
		}

		// Handle circuit breaker open
		if errors.Is(err, ErrCircuitBreakerOpen) {
			d.logCircuitBreakerOpen(ep.name, notif.ID)
			return
		}

		// Check if should retry
		if !d.shouldRetry(err, attempts, ep.name) {
			return
		}

		// Wait for retry delay
		if !d.waitForRetry(ctx, ep.name, attempts) {
			return
		}
	}
}

// attemptSend attempts to send a notification.
func (d *pushDispatcher) attemptSend(ctx context.Context, notif *Notification, ep *enhancedProvider) (time.Duration, error) {
	attemptCtx := ctx
	var cancel context.CancelFunc
	if deadline := d.defaultTimeout; deadline > 0 {
		attemptCtx, cancel = context.WithTimeout(ctx, deadline)
	}
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()

	timer := time.Now()
	var err error
	if ep.circuitBreaker != nil {
		err = ep.circuitBreaker.Call(attemptCtx, func(ctx context.Context) error {
			return ep.prov.Send(ctx, notif)
		})
	} else {
		err = ep.prov.Send(attemptCtx, notif)
	}
	return time.Since(timer), err
}

// recordAttemptMetrics records metrics for an attempt.
func (d *pushDispatcher) recordAttemptMetrics(providerName, notifType string, err error, duration time.Duration, attempts int) {
	if d.metrics == nil {
		return
	}

	switch {
	case err == nil:
		d.metrics.RecordDelivery(providerName, notifType, "success", duration)
		if attempts > 1 {
			d.metrics.RecordRetrySuccess(providerName)
		}
	case errors.Is(err, ErrCircuitBreakerOpen):
		d.metrics.RecordDelivery(providerName, notifType, "circuit_open", duration)
	case errors.Is(err, context.DeadlineExceeded):
		d.metrics.RecordDelivery(providerName, notifType, "timeout", duration)
		d.metrics.RecordTimeout(providerName)
		d.metrics.RecordDeliveryError(providerName, notifType, "timeout")
	default:
		d.metrics.RecordDelivery(providerName, notifType, "error", duration)
		d.metrics.RecordDeliveryError(providerName, notifType, categorizeError(err))
	}
}

// logSuccess logs a successful delivery.
func (d *pushDispatcher) logSuccess(providerName string, notif *Notification, notifType string, attempts int, duration time.Duration) {
	if d.log != nil {
		d.log.Info("push sent",
			"provider", providerName,
			"id", notif.ID,
			"type", notifType,
			"priority", string(notif.Priority),
			"attempt", attempts,
			"elapsed", duration)
	}
}

// logCircuitBreakerOpen logs when circuit breaker blocks a request.
func (d *pushDispatcher) logCircuitBreakerOpen(providerName, notifID string) {
	if d.log != nil {
		d.log.Warn("push blocked by circuit breaker",
			"provider", providerName,
			"id", notifID)
	}
}

// shouldRetry determines if an attempt should be retried.
func (d *pushDispatcher) shouldRetry(err error, attempts int, providerName string) bool {
	var perr *providerError
	retryable := true
	if errors.As(err, &perr) {
		retryable = perr.Retryable
	}

	if !retryable || attempts > d.maxRetries {
		if d.log != nil {
			d.log.Error("push send failed",
				"provider", providerName,
				"attempts", attempts,
				"error", err,
				"retryable", retryable)
		}
		return false
	}

	if d.metrics != nil {
		d.metrics.RecordRetryAttempt(providerName)
	}
	return true
}

// waitForRetry waits for the retry delay or context cancellation.
func (d *pushDispatcher) waitForRetry(ctx context.Context, providerName string, attempts int) bool {
	select {
	case <-ctx.Done():
		if d.log != nil {
			d.log.Debug("retry cancelled due to context cancellation",
				"provider", providerName,
				"attempts", attempts)
		}
		return false
	case <-time.After(d.retryDelay):
		return true
	}
}

// ----------------- Provider construction -----------------

func buildProvider(pc *conf.PushProviderConfig) Provider {
	ptype := strings.ToLower(pc.Type)
	types := effectiveTypes(pc.Filter.Types)
	switch ptype {
	case "script":
		return NewScriptProvider(orDefault(pc.Name, "script"), pc.Enabled, pc.Command, pc.Args, pc.Environment, pc.InputFormat, types)
	case "shoutrrr":
		return NewShoutrrrProvider(orDefault(pc.Name, "shoutrrr"), pc.Enabled, pc.URLs, types, pc.Timeout)
	default:
		return nil
	}
}

func effectiveTypes(cfg []string) []string {
	if len(cfg) == 0 {
		return []string{"error", "warning", "info", "detection", "system"}
	}
	return append([]string{}, cfg...)
}

// ----------------- helpers -----------------

func orDefault[T ~string](v, d T) T {
	if strings.TrimSpace(string(v)) == "" {
		return d
	}
	return v
}

// MatchesProviderFilter applies basic filtering based on type/priority/component and simple metadata rules.
// This function is exported for testing purposes.
func MatchesProviderFilter(f *conf.PushFilterConfig, n *Notification, log *slog.Logger, providerName string) bool {
	if f == nil {
		if log != nil {
			log.Debug("no filter configured, allowing notification", "provider", providerName, "notification_id", n.ID)
		}
		return true
	}

	// Types
	if len(f.Types) > 0 {
		if log != nil {
			log.Debug("checking type filter", "provider", providerName, "allowed_types", f.Types, "notification_type", string(n.Type), "notification_id", n.ID)
		}
		if !slices.Contains(f.Types, string(n.Type)) {
			if log != nil {
				log.Debug("filter failed: type mismatch", "provider", providerName, "allowed_types", f.Types, "notification_type", string(n.Type), "notification_id", n.ID)
			}
			return false
		}
	}
	// Priorities
	if len(f.Priorities) > 0 {
		if !slices.Contains(f.Priorities, string(n.Priority)) {
			if log != nil {
				log.Debug("filter failed: priority mismatch", "provider", providerName, "allowed_priorities", f.Priorities, "notification_priority", string(n.Priority), "notification_id", n.ID)
			}
			return false
		}
	}
	// Component
	if len(f.Components) > 0 {
		if !slices.Contains(f.Components, n.Component) {
			if log != nil {
				log.Debug("filter failed: component mismatch", "provider", providerName, "allowed_components", f.Components, "notification_component", n.Component, "notification_id", n.ID)
			}
			return false
		}
	}
	// Metadata filters: support confidence > >= < <= = == and equality matches for bools/strings
	for key, val := range f.MetadataFilters {
		if log != nil {
			log.Debug("processing metadata filter", "provider", providerName, "key", key, "filter_value", val, "notification_id", n.ID)
		}
		// confidence threshold
		if key == "confidence" {
			cond, ok := val.(string)
			if !ok {
				if log != nil {
					log.Debug("filter failed: confidence filter misconfigured", "provider", providerName, "filter_value", val, "notification_id", n.ID)
				}
				return false // misconfigured filter
			}
			cond = strings.TrimSpace(cond)
			if cond == "" {
				if log != nil {
					log.Debug("filter failed: empty confidence condition", "provider", providerName, "notification_id", n.ID)
				}
				return false
			}

			// Parse operator and value
			var op string
			var valStr string
			switch {
			case len(cond) >= 2 && (cond[:2] == ">=" || cond[:2] == "<=" || cond[:2] == "=="):
				op = cond[:2]
				valStr = strings.TrimSpace(cond[2:])
			case len(cond) >= 1 && (cond[0] == '>' || cond[0] == '<' || cond[0] == '='):
				op = string(cond[0])
				valStr = strings.TrimSpace(cond[1:])
			default:
				if log != nil {
					log.Debug("filter failed: unknown confidence operator", "provider", providerName, "condition", cond, "notification_id", n.ID)
				}
				return false // unknown operator format
			}

			threshold, err := strconv.ParseFloat(valStr, 64)
			if err != nil {
				if log != nil {
					log.Debug("filter failed: invalid confidence threshold", "provider", providerName, "threshold_str", valStr, "error", err, "notification_id", n.ID)
				}
				return false
			}
			raw, exists := n.Metadata["confidence"]
			if !exists {
				if log != nil {
					log.Debug("filter failed: confidence metadata missing", "provider", providerName, "available_metadata", n.Metadata, "notification_id", n.ID)
				}
				return false // require presence
			}
			cv, ok := toFloat(raw)
			if !ok {
				if log != nil {
					log.Debug("filter failed: confidence value not parseable", "provider", providerName, "confidence_value", raw, "notification_id", n.ID)
				}
				return false // require parse success
			}
			switch op {
			case ">":
				if !(cv > threshold) {
					if log != nil {
						log.Debug("filter failed: confidence too low", "provider", providerName, "condition", cond, "actual_confidence", cv, "required", fmt.Sprintf("> %v", threshold), "notification_id", n.ID)
					}
					return false
				}
			case ">=":
				if !(cv >= threshold) {
					if log != nil {
						log.Debug("filter failed: confidence too low", "provider", providerName, "condition", cond, "actual_confidence", cv, "required", fmt.Sprintf(">= %v", threshold), "notification_id", n.ID)
					}
					return false
				}
			case "<":
				if !(cv < threshold) {
					if log != nil {
						log.Debug("filter failed: confidence too high", "provider", providerName, "condition", cond, "actual_confidence", cv, "required", fmt.Sprintf("< %v", threshold), "notification_id", n.ID)
					}
					return false
				}
			case "<=":
				if !(cv <= threshold) {
					if log != nil {
						log.Debug("filter failed: confidence too high", "provider", providerName, "condition", cond, "actual_confidence", cv, "required", fmt.Sprintf("<= %v", threshold), "notification_id", n.ID)
					}
					return false
				}
			case "=", "==":
				if !(cv == threshold) {
					if log != nil {
						log.Debug("filter failed: confidence mismatch", "provider", providerName, "condition", cond, "actual_confidence", cv, "required", fmt.Sprintf("== %v", threshold), "notification_id", n.ID)
					}
					return false
				}
			default:
				if log != nil {
					log.Debug("filter failed: unknown confidence operator", "provider", providerName, "operator", op, "notification_id", n.ID)
				}
				return false // unknown operator
			}
			continue
		}
		// exact match requires key presence
		mv, ok := n.Metadata[key]
		if !ok {
			if log != nil {
				log.Debug("filter failed: metadata key missing", "provider", providerName, "required_key", key, "available_metadata", n.Metadata, "notification_id", n.ID)
			}
			return false
		}
		if fmt.Sprint(mv) != fmt.Sprint(val) {
			if log != nil {
				log.Debug("filter failed: metadata value mismatch", "provider", providerName, "key", key, "expected", val, "actual", mv, "notification_id", n.ID)
			}
			return false
		}
	}
	return true
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float32:
		return float64(t), true
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

// providerError allows providers to mark errors as retryable/non-retryable
// (kept for backward compatibility with dispatcher retry logic)
type providerError struct {
	Err       error
	Retryable bool
}

func (e *providerError) Error() string { return e.Err.Error() }
func (e *providerError) Unwrap() error { return e.Err }

// ----------------- Enhanced Provider Initialization -----------------

// initializeEnhancedProviders creates enhanced providers with circuit breakers and metrics.
func (d *pushDispatcher) initializeEnhancedProviders(settings *conf.Settings, notificationMetrics *metrics.NotificationMetrics) []enhancedProvider {
	var enhanced []enhancedProvider

	// Get circuit breaker config from settings or use defaults
	cbConfig := DefaultCircuitBreakerConfig()
	if settings.Notification.Push.CircuitBreaker.Enabled {
		cbConfig.MaxFailures = settings.Notification.Push.CircuitBreaker.MaxFailures
		cbConfig.Timeout = settings.Notification.Push.CircuitBreaker.Timeout
		cbConfig.HalfOpenMaxRequests = settings.Notification.Push.CircuitBreaker.HalfOpenMaxRequests

		// Validate circuit breaker configuration
		if err := cbConfig.Validate(); err != nil {
			if d.log != nil {
				d.log.Error("invalid circuit breaker configuration, using defaults",
					"error", err)
			}
			cbConfig = DefaultCircuitBreakerConfig()
		}
	}

	for i := range settings.Notification.Push.Providers {
		pc := &settings.Notification.Push.Providers[i]
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

// ----------------- Error Categorization -----------------

// categorizeError categorizes errors for metrics.
// IMPORTANT: This function MUST return a bounded set of error categories to prevent
// Prometheus metric cardinality explosion. Never return dynamic error messages or
// unbounded values as categories. The fixed set of categories is:
// - "none", "timeout", "cancelled", "network", "validation", "permission",
//   "not_found", "provider_error"
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

// containsAny checks if a string contains any of the given substrings (case-insensitive).
func containsAny(s string, substrs ...string) bool {
	s = strings.ToLower(s)
	for _, substr := range substrs {
		if substr != "" && strings.Contains(s, strings.ToLower(substr)) {
			return true
		}
	}
	return false
}
