package notification

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"golang.org/x/sync/semaphore"
)

const (
	// Concurrency limits
	defaultMaxConcurrentJobs = 100                // Default maximum concurrent notification dispatches
	jobsPerProvider          = 20                 // Concurrent dispatches allocated per provider
	semaphoreAcquireTimeout  = 100 * time.Millisecond // Timeout for acquiring semaphore slot

	// Exponential backoff constants
	maxExponentialAttempts = 31                   // Maximum attempts before overflow (2^31 would overflow time.Duration)
	jitterPercent          = 25                   // Jitter percentage: ±25% of delay
	defaultRetryDelay      = 1 * time.Second      // Default base retry delay if not configured
	defaultMaxRetryDelay   = 30 * time.Second     // Default maximum retry delay cap

	// Filter rejection reasons - used for metrics and observability
	filterReasonAll                 = "all"                  // Notification matched all filters
	filterReasonTypeMismatch        = "type_mismatch"        // Notification type not in allowed types
	filterReasonPriorityMismatch    = "priority_mismatch"    // Notification priority not allowed
	filterReasonComponentMismatch   = "component_mismatch"   // Notification component not allowed
	filterReasonConfidenceThreshold = "confidence_threshold" // Confidence metadata didn't meet threshold
	filterReasonMetadataMismatch    = "metadata_mismatch"    // Other metadata filter failed
)

// pushDispatcher routes notifications to enabled providers based on filters
// It subscribes to the notification service and forwards notifications asynchronously.
type pushDispatcher struct {
	providers         []enhancedProvider
	log               *slog.Logger
	enabled           bool
	maxRetries        int
	retryDelay        time.Duration
	defaultTimeout    time.Duration
	cancel            context.CancelFunc
	mu                sync.RWMutex
	metrics           *metrics.NotificationMetrics
	healthChecker     *HealthChecker
	concurrencySem    *semaphore.Weighted // Limits concurrent dispatch goroutines to prevent resource exhaustion
	maxConcurrentJobs int64               // Maximum concurrent dispatches - dynamically calculated as max(defaultMaxConcurrentJobs, providers*jobsPerProvider)
	// rateLimiter removed - now per-provider in enhancedProvider
}

// enhancedProvider wraps a provider with circuit breaker, rate limiter, and metrics.
// Each provider has its own circuit breaker and rate limiter for isolation.
type enhancedProvider struct {
	prov           Provider
	circuitBreaker *PushCircuitBreaker
	rateLimiter    *PushRateLimiter // Per-provider rate limiting
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

		// Calculate max concurrent jobs based on number of providers
		maxConcurrentJobs := int64(defaultMaxConcurrentJobs)
		if providerCount := len(settings.Notification.Push.Providers); providerCount > 0 {
			perProviderLimit := int64(providerCount * jobsPerProvider)
			if perProviderLimit > maxConcurrentJobs {
				maxConcurrentJobs = perProviderLimit
			}
		}

		pd := &pushDispatcher{
			log:               getFileLogger(settings.Debug),
			enabled:           settings.Notification.Push.Enabled,
			maxRetries:        settings.Notification.Push.MaxRetries,
			retryDelay:        settings.Notification.Push.RetryDelay,
			defaultTimeout:    settings.Notification.Push.DefaultTimeout,
			metrics:           notificationMetrics,
			concurrencySem:    semaphore.NewWeighted(maxConcurrentJobs),
			maxConcurrentJobs: maxConcurrentJobs,
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

		// Rate limiting is now per-provider (initialized in initializeEnhancedProviders)

		// Build enhanced providers with circuit breakers and rate limiters
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
		"max_concurrent_dispatches", d.maxConcurrentJobs)
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

		// Acquire semaphore slot before spawning goroutine (prevents unbounded goroutine explosion)
		// Use TryAcquire with timeout to prevent blocking the dispatch loop
		// Skip semaphore if not initialized (e.g., in tests)
		if d.concurrencySem != nil {
			acquireCtx, cancel := context.WithTimeout(ctx, semaphoreAcquireTimeout)
			err := d.concurrencySem.Acquire(acquireCtx, 1)
			cancel()
			if err != nil {
				// Failed to acquire within timeout - queue is full
				if d.log != nil {
					d.log.Warn("dispatch queue full, dropping notification",
						"provider", ep.name,
						"notification_id", notif.ID,
						"error", err)
				}
				if d.metrics != nil {
					d.metrics.RecordFilterRejection(ep.name, "queue_full")
				}
				continue
			}
		}

		// Run each provider in its own goroutine to avoid head-of-line blocking
		go func(provider *enhancedProvider) {
			// Always release semaphore and handle panics
			defer func() {
				if d.concurrencySem != nil {
					d.concurrencySem.Release(1)
				}
				if r := recover(); r != nil {
					if d.log != nil {
						d.log.Error("panic in dispatch goroutine",
							"provider", provider.name,
							"notification_id", notif.ID,
							"panic", r)
					}
				}
			}()
			d.dispatchEnhanced(ctx, notif, provider)
		}(ep)
	}
}

// matchesFilter checks if notification matches provider filter and records metrics with reason.
func (d *pushDispatcher) matchesFilter(ep *enhancedProvider, notif *Notification) bool {
	// Use enhanced filter logic that returns rejection reason
	matches, reason := MatchesProviderFilterWithReason(&ep.filter, notif, d.log, ep.name)

	// Record filter metrics with specific reason
	if d.metrics != nil {
		if matches {
			d.metrics.RecordFilterMatch(ep.name, reason)
		} else {
			d.metrics.RecordFilterRejection(ep.name, reason)
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

	// Increment dispatch total and track active dispatches
	if d.metrics != nil {
		d.metrics.IncrementDispatchTotal()
		d.metrics.IncDispatchActive()
		defer d.metrics.DecDispatchActive()
	}

	d.retryLoop(ctx, notif, ep)
}

// checkRateLimit checks if notification is rate limited.
func (d *pushDispatcher) checkRateLimit(ep *enhancedProvider, notif *Notification) bool {
	// Use per-provider rate limiter for isolation
	if ep.rateLimiter != nil && !ep.rateLimiter.Allow() {
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

// waitForRetry waits for the retry delay with exponential backoff and jitter.
// Uses capped exponential backoff: min(baseDelay * 2^(attempt-1), maxDelay) ± jitter
// This prevents thundering herd problems while maintaining reasonable wait times.
func (d *pushDispatcher) waitForRetry(ctx context.Context, providerName string, attempts int) bool {
	// Determine base delay (starting point for exponential growth)
	baseDelay := d.retryDelay
	if baseDelay <= 0 {
		baseDelay = defaultRetryDelay
	}

	// Determine max delay cap (upper bound for exponential growth)
	// If retryDelay is configured and greater than base, use it as cap
	// Otherwise use a sensible default max
	maxDelay := d.retryDelay
	if maxDelay <= 0 || maxDelay < baseDelay {
		maxDelay = defaultMaxRetryDelay
	}

	// Calculate exponential component using bit shift, with overflow protection
	exponential := baseDelay
	if attempts > 1 && attempts < maxExponentialAttempts {
		exponential = baseDelay * (1 << (attempts - 1))
	}

	// Cap at max delay (preserve exponential growth up to the cap)
	if exponential < baseDelay {
		exponential = baseDelay
	} else if exponential > maxDelay {
		exponential = maxDelay
	}

	// Add jitter: ±25% of the delay to prevent thundering herd
	// Use math/rand/v2 for thread-safe random generation (Go 1.22+)
	jitterRange := exponential * jitterPercent / 100
	jitterMax := int64(jitterRange * 2)
	var jitter time.Duration
	if jitterMax > 0 {
		jitter = time.Duration(rand.Int64N(jitterMax)) - jitterRange
	}

	// Apply jitter and ensure final delay stays within bounds
	delay := exponential + jitter
	if delay < baseDelay {
		delay = baseDelay
	}
	if delay > maxDelay {
		delay = maxDelay
	}

	if d.log != nil {
		d.log.Debug("waiting for retry with exponential backoff",
			"provider", providerName,
			"attempts", attempts,
			"delay", delay,
			"base_delay", baseDelay,
			"max_delay", maxDelay)
	}

	select {
	case <-ctx.Done():
		if d.log != nil {
			d.log.Debug("retry cancelled due to context cancellation",
				"provider", providerName,
				"attempts", attempts)
		}
		return false
	case <-time.After(delay):
		return true
	}
}

// ----------------- Provider construction -----------------

func buildProvider(pc *conf.PushProviderConfig, log *slog.Logger) Provider {
	ptype := strings.ToLower(pc.Type)
	types := effectiveTypes(pc.Filter.Types)
	switch ptype {
	case "script":
		return NewScriptProvider(orDefault(pc.Name, "script"), pc.Enabled, pc.Command, pc.Args, pc.Environment, pc.InputFormat, types)
	case "shoutrrr":
		return NewShoutrrrProvider(orDefault(pc.Name, "shoutrrr"), pc.Enabled, pc.URLs, types, pc.Timeout)
	case "webhook":
		endpoints, err := convertWebhookEndpoints(pc.Endpoints, log)
		if err != nil {
			if log != nil {
				log.Error("failed to resolve webhook secrets",
					"name", pc.Name,
					"error", err)
			}
			return nil
		}
		provider, err := NewWebhookProvider(orDefault(pc.Name, "webhook"), pc.Enabled, endpoints, types, pc.Template)
		if err != nil {
			if log != nil {
				log.Error("failed to create webhook provider",
					"name", pc.Name,
					"error", err)
			}
			return nil
		}
		return provider
	default:
		if log != nil {
			log.Warn("unknown push provider type; skipping",
				"name", pc.Name,
				"type", pc.Type)
		}
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

// MatchesProviderFilterWithReason applies filtering and returns both result and reason.
// Reason indicates why the notification matched or was rejected for better observability.
// Returns (matches, reason) where reason is one of the filterReason* constants.
func MatchesProviderFilterWithReason(f *conf.PushFilterConfig, n *Notification, log *slog.Logger, providerName string) (matches bool, reason string) {
	if f == nil {
		logDebug(log, "no filter configured, allowing notification", "provider", providerName, "notification_id", n.ID)
		return true, filterReasonAll
	}

	// Check type filter
	if matches, reason := checkTypeFilter(f, n, log, providerName); !matches {
		return false, reason
	}

	// Check priority filter
	if matches, reason := checkPriorityFilter(f, n, log, providerName); !matches {
		return false, reason
	}

	// Check component filter
	if matches, reason := checkComponentFilter(f, n, log, providerName); !matches {
		return false, reason
	}

	// Check metadata filters
	if matches, reason := checkMetadataFilters(f, n, log, providerName); !matches {
		return false, reason
	}

	return true, filterReasonAll
}

// logDebug is a helper to reduce repeated logging checks
func logDebug(log *slog.Logger, msg string, args ...any) {
	if log != nil {
		log.Debug(msg, args...)
	}
}

// checkTypeFilter validates the notification type against configured filter
func checkTypeFilter(f *conf.PushFilterConfig, n *Notification, log *slog.Logger, providerName string) (matches bool, reason string) {
	if len(f.Types) == 0 {
		return true, ""
	}

	logDebug(log, "checking type filter", "provider", providerName, "allowed_types", f.Types, "notification_type", string(n.Type), "notification_id", n.ID)

	if !slices.Contains(f.Types, string(n.Type)) {
		logDebug(log, "filter failed: type mismatch", "provider", providerName, "allowed_types", f.Types, "notification_type", string(n.Type), "notification_id", n.ID)
		return false, filterReasonTypeMismatch
	}

	return true, ""
}

// checkPriorityFilter validates the notification priority against configured filter
func checkPriorityFilter(f *conf.PushFilterConfig, n *Notification, log *slog.Logger, providerName string) (matches bool, reason string) {
	if len(f.Priorities) == 0 {
		return true, ""
	}

	if !slices.Contains(f.Priorities, string(n.Priority)) {
		logDebug(log, "filter failed: priority mismatch", "provider", providerName, "allowed_priorities", f.Priorities, "notification_priority", string(n.Priority), "notification_id", n.ID)
		return false, filterReasonPriorityMismatch
	}

	return true, ""
}

// checkComponentFilter validates the notification component against configured filter
func checkComponentFilter(f *conf.PushFilterConfig, n *Notification, log *slog.Logger, providerName string) (matches bool, reason string) {
	if len(f.Components) == 0 {
		return true, ""
	}

	if !slices.Contains(f.Components, n.Component) {
		logDebug(log, "filter failed: component mismatch", "provider", providerName, "allowed_components", f.Components, "notification_component", n.Component, "notification_id", n.ID)
		return false, filterReasonComponentMismatch
	}

	return true, ""
}

// checkMetadataFilters validates notification metadata against configured filters
func checkMetadataFilters(f *conf.PushFilterConfig, n *Notification, log *slog.Logger, providerName string) (matches bool, reason string) {
	for key, val := range f.MetadataFilters {
		logDebug(log, "processing metadata filter", "provider", providerName, "key", key, "filter_value", val, "notification_id", n.ID)

		// Special handling for confidence threshold
		if key == "confidence" {
			if matches, reason := checkConfidenceFilter(val, n, log, providerName); !matches {
				return false, reason
			}
			continue
		}

		// Exact match for other metadata keys
		if matches, reason := checkExactMetadataMatch(key, val, n, log, providerName); !matches {
			return false, reason
		}
	}

	return true, ""
}

// checkConfidenceFilter handles confidence threshold filtering with operators
func checkConfidenceFilter(filterVal any, n *Notification, log *slog.Logger, providerName string) (matches bool, reason string) {
	cond, ok := filterVal.(string)
	if !ok {
		logDebug(log, "filter failed: confidence filter misconfigured", "provider", providerName, "filter_value", filterVal, "notification_id", n.ID)
		return false, filterReasonConfidenceThreshold
	}

	cond = strings.TrimSpace(cond)
	if cond == "" {
		logDebug(log, "filter failed: empty confidence condition", "provider", providerName, "notification_id", n.ID)
		return false, filterReasonConfidenceThreshold
	}

	// Parse operator and value
	op, valStr := parseConfidenceOperator(cond)
	if op == "" {
		logDebug(log, "filter failed: unknown confidence operator", "provider", providerName, "condition", cond, "notification_id", n.ID)
		return false, filterReasonConfidenceThreshold
	}

	threshold, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		logDebug(log, "filter failed: invalid confidence threshold", "provider", providerName, "threshold_str", valStr, "error", err, "notification_id", n.ID)
		return false, filterReasonConfidenceThreshold
	}

	// Get confidence value from notification metadata
	raw, exists := n.Metadata["confidence"]
	if !exists {
		logDebug(log, "filter failed: confidence metadata missing", "provider", providerName, "available_metadata", n.Metadata, "notification_id", n.ID)
		return false, filterReasonConfidenceThreshold
	}

	cv, ok := toFloat(raw)
	if !ok {
		logDebug(log, "filter failed: confidence value not parseable", "provider", providerName, "confidence_value", raw, "notification_id", n.ID)
		return false, filterReasonConfidenceThreshold
	}

	// Check if confidence meets threshold
	if !compareConfidence(cv, op, threshold) {
		logDebug(log, "filter failed: confidence threshold not met", "provider", providerName, "condition", cond, "actual_confidence", cv, "notification_id", n.ID)
		return false, filterReasonConfidenceThreshold
	}

	return true, ""
}

// parseConfidenceOperator extracts operator and value from confidence condition string
func parseConfidenceOperator(cond string) (op, valStr string) {
	switch {
	case len(cond) >= 2 && (cond[:2] == ">=" || cond[:2] == "<=" || cond[:2] == "=="):
		return cond[:2], strings.TrimSpace(cond[2:])
	case len(cond) >= 1 && (cond[0] == '>' || cond[0] == '<' || cond[0] == '='):
		return string(cond[0]), strings.TrimSpace(cond[1:])
	default:
		return "", ""
	}
}

// compareConfidence performs the actual comparison based on operator
func compareConfidence(confidence float64, op string, threshold float64) bool {
	switch op {
	case ">":
		return confidence > threshold
	case ">=":
		return confidence >= threshold
	case "<":
		return confidence < threshold
	case "<=":
		return confidence <= threshold
	case "=", "==":
		return confidence == threshold
	default:
		return false
	}
}

// checkExactMetadataMatch validates exact metadata key-value match
func checkExactMetadataMatch(key string, expectedVal any, n *Notification, log *slog.Logger, providerName string) (matches bool, reason string) {
	actualVal, ok := n.Metadata[key]
	if !ok {
		logDebug(log, "filter failed: metadata key missing", "provider", providerName, "required_key", key, "available_metadata", n.Metadata, "notification_id", n.ID)
		return false, filterReasonMetadataMismatch
	}

	if fmt.Sprint(actualVal) != fmt.Sprint(expectedVal) {
		logDebug(log, "filter failed: metadata value mismatch", "provider", providerName, "key", key, "expected", expectedVal, "actual", actualVal, "notification_id", n.ID)
		return false, filterReasonMetadataMismatch
	}

	return true, ""
}

// MatchesProviderFilter applies basic filtering based on type/priority/component and simple metadata rules.
// This function is exported for testing purposes and preserved for backward compatibility.
// New code should use MatchesProviderFilterWithReason for better observability.
func MatchesProviderFilter(f *conf.PushFilterConfig, n *Notification, log *slog.Logger, providerName string) bool {
	// Delegate to the enhanced version and discard the reason
	matches, _ := MatchesProviderFilterWithReason(f, n, log, providerName)
	return matches
}

// toFloat converts various numeric types to float64 for confidence threshold comparisons.
//
// Supported types:
//   - Floating point: float32, float64
//   - Signed integers: int, int8, int16, int32, int64
//   - Unsigned integers: uint, uint8, uint16, uint32, uint64
//   - Strings: numeric strings parseable by strconv.ParseFloat (e.g., "0.85", "42")
//
// Unsupported types return (0, false):
//   - bool, nil, struct, slice, map, channel, function
//   - Non-numeric strings (e.g., "abc", "")
//
// Examples:
//   toFloat(0.85)      // (0.85, true)
//   toFloat(int(42))   // (42.0, true)
//   toFloat("3.14")    // (3.14, true)
//   toFloat("invalid") // (0, false)
//   toFloat(true)      // (0, false)
func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float32:
		return float64(t), true
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int8:
		return float64(t), true
	case int16:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint8:
		return float64(t), true
	case uint16:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint64:
		return float64(t), true
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
		prov := buildProvider(pc, d.log)
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

			// Create per-provider rate limiter if enabled
			var rl *PushRateLimiter
			if settings.Notification.Push.RateLimiting.Enabled {
				rl = NewPushRateLimiter(PushRateLimiterConfig{
					RequestsPerMinute: settings.Notification.Push.RateLimiting.RequestsPerMinute,
					BurstSize:         settings.Notification.Push.RateLimiting.BurstSize,
				})
				if d.log != nil {
					d.log.Info("rate limiter enabled for provider",
						"provider", name,
						"requests_per_minute", settings.Notification.Push.RateLimiting.RequestsPerMinute,
						"burst_size", settings.Notification.Push.RateLimiting.BurstSize)
				}
			}

			ep := enhancedProvider{
				prov:           prov,
				circuitBreaker: cb,
				rateLimiter:    rl,
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

// categorizeError classifies errors into bounded categories for Prometheus metrics.
//
// Error Categories (BOUNDED - prevents metric cardinality explosion):
//
//   - "timeout"         - Context deadline exceeded (network timeouts, slow APIs)
//   - "cancelled"       - Context cancelled (shutdown, user cancellation)
//   - "network"         - Network/connection issues (DNS, dial, connection refused)
//   - "validation"      - Configuration/input validation errors
//   - "permission"      - Authorization failures (API key invalid, forbidden)
//   - "not_found"       - Resource not found (404, invalid webhook URL)
//   - "provider_error"  - All other provider-specific errors (catch-all)
//
// Guidelines for Adding New Categories:
//
//  1. Only add categories for common, actionable error types
//  2. New categories should represent >5% of total errors in production
//  3. Keep total categories under 10 to prevent metric explosion
//  4. Provider-specific errors should use "provider_error" (e.g., Telegram rate limits)
//  5. Use error pattern matching, not exact strings (case-insensitive)
//
// Examples:
//   - Telegram "Too Many Requests" → "provider_error" (provider-specific)
//   - Discord "Invalid Webhook" → "validation" (common, actionable)
//   - Any connection timeout → "timeout" (common, network layer)
//
// Metric Cardinality Impact:
//   - 7 categories × 5 notification types × N providers = 35N metric series
//   - Adding 1 category = +5N series (acceptable if <10% increase)
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

// convertWebhookEndpoints converts configuration webhook endpoints to provider webhook endpoints.
// Resolves all secrets (environment variables and files) during conversion.
// Returns error if any secret resolution fails.
func convertWebhookEndpoints(cfgEndpoints []conf.WebhookEndpointConfig, log *slog.Logger) ([]WebhookEndpoint, error) {
	endpoints := make([]WebhookEndpoint, 0, len(cfgEndpoints))
	for i := range cfgEndpoints {
		cfg := &cfgEndpoints[i] // Use pointer to avoid copying

		// Resolve authentication secrets
		auth, err := resolveWebhookAuth(&cfg.Auth)
		if err != nil {
			return nil, fmt.Errorf("endpoint %d: %w", i, err)
		}

		endpoints = append(endpoints, WebhookEndpoint{
			URL:     cfg.URL,
			Method:  cfg.Method,
			Headers: cfg.Headers,
			Timeout: cfg.Timeout,
			Auth:    *auth,
		})
	}
	return endpoints, nil
}
