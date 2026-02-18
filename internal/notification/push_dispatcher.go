package notification

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"golang.org/x/sync/semaphore"
)

const (
	// Concurrency limits
	defaultMaxConcurrentJobs = 100                    // Default maximum concurrent notification dispatches
	jobsPerProvider          = 20                     // Concurrent dispatches allocated per provider
	semaphoreAcquireTimeout  = 100 * time.Millisecond // Timeout for acquiring semaphore slot

	// Exponential backoff constants
	maxExponentialAttempts = 31               // Maximum attempts before overflow (2^31 would overflow time.Duration)
	jitterPercent          = 25               // Jitter percentage: ±25% of delay
	defaultRetryDelay      = 1 * time.Second  // Default base retry delay if not configured
	defaultMaxRetryDelay   = 30 * time.Second // Default maximum retry delay cap

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
	log               logger.Logger
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
		initErr = initializePushDispatcher(settings, notificationMetrics)
	})
	return initErr
}

// initializePushDispatcher performs the actual dispatcher initialization.
func initializePushDispatcher(settings *conf.Settings, notificationMetrics *metrics.NotificationMetrics) error {
	if settings == nil || !settings.Notification.Push.Enabled {
		return nil
	}

	pd := createPushDispatcher(settings, notificationMetrics)
	pd.healthChecker = createHealthCheckerIfEnabled(settings, pd.log, notificationMetrics)
	pd.providers = pd.initializeEnhancedProviders(settings, notificationMetrics)

	registerProvidersWithHealthChecker(pd)
	globalPushDispatcher = pd

	warnIfLocalhostWithExternalWebhooks(pd, settings)
	return startDispatcherIfNeeded(pd)
}

// createPushDispatcher creates a new push dispatcher with the given settings.
func createPushDispatcher(settings *conf.Settings, notificationMetrics *metrics.NotificationMetrics) *pushDispatcher {
	maxConcurrentJobs := calculateMaxConcurrentJobs(settings)
	return &pushDispatcher{
		log:               GetLogger(),
		enabled:           settings.Notification.Push.Enabled,
		maxRetries:        settings.Notification.Push.MaxRetries,
		retryDelay:        settings.Notification.Push.RetryDelay.Std(),
		defaultTimeout:    settings.Notification.Push.DefaultTimeout.Std(),
		metrics:           notificationMetrics,
		concurrencySem:    semaphore.NewWeighted(maxConcurrentJobs),
		maxConcurrentJobs: maxConcurrentJobs,
	}
}

// calculateMaxConcurrentJobs calculates the max concurrent jobs based on provider count.
func calculateMaxConcurrentJobs(settings *conf.Settings) int64 {
	maxJobs := int64(defaultMaxConcurrentJobs)
	if providerCount := len(settings.Notification.Push.Providers); providerCount > 0 {
		perProviderLimit := int64(providerCount * jobsPerProvider)
		if perProviderLimit > maxJobs {
			maxJobs = perProviderLimit
		}
	}
	return maxJobs
}

// createHealthCheckerIfEnabled creates a health checker if enabled in settings.
func createHealthCheckerIfEnabled(settings *conf.Settings, log logger.Logger, notificationMetrics *metrics.NotificationMetrics) *HealthChecker {
	if !settings.Notification.Push.HealthCheck.Enabled {
		return nil
	}
	hcConfig := HealthCheckConfig{
		Enabled:  settings.Notification.Push.HealthCheck.Enabled,
		Interval: settings.Notification.Push.HealthCheck.Interval.Std(),
		Timeout:  settings.Notification.Push.HealthCheck.Timeout.Std(),
	}
	return NewHealthChecker(hcConfig, log, notificationMetrics)
}

// registerProvidersWithHealthChecker registers all providers with the health checker.
func registerProvidersWithHealthChecker(pd *pushDispatcher) {
	if pd.healthChecker == nil {
		return
	}
	for i := range pd.providers {
		ep := &pd.providers[i]
		pd.healthChecker.RegisterProvider(ep.prov, ep.circuitBreaker)
	}
}

// warnIfLocalhostWithExternalWebhooks logs a warning if localhost URLs are used with external webhooks.
// See: https://github.com/tphakala/birdnet-go/issues/1457
func warnIfLocalhostWithExternalWebhooks(pd *pushDispatcher, settings *conf.Settings) {
	baseURL := settings.Security.GetBaseURL(settings.WebServer.Port)
	if containsLocalhost(baseURL) && hasExternalWebhooks(pd.providers) {
		pd.log.Info("detection URLs use localhost with external webhooks",
			logger.String("base_url", baseURL),
			logger.String("note", "External services may not access these URLs"),
			logger.String("fix", "Set security.baseUrl, security.host, or BIRDNET_URL environment variable"))
	}
}

// startDispatcherIfNeeded starts the dispatcher if it's enabled and has providers.
func startDispatcherIfNeeded(pd *pushDispatcher) error {
	if !pd.enabled || len(pd.providers) == 0 {
		return nil
	}
	if err := pd.start(); err != nil {
		pd.log.Error("failed to start push dispatcher", logger.Error(err))
		return err
	}
	return nil
}

// GetPushDispatcher returns the dispatcher if initialized
func GetPushDispatcher() *pushDispatcher { return globalPushDispatcher }

func (d *pushDispatcher) start() error {
	service := GetService()
	if service == nil {
		return fmt.Errorf("notification service not initialized")
	}

	ch, ctx := service.Subscribe()
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel

	go d.runDispatchLoop(ctx, ch, service)
	d.startHealthChecker(ctx)

	d.log.Info("push dispatcher started",
		logger.Int("providers", len(d.providers)),
		logger.Bool("health_checker", d.healthChecker != nil),
		logger.Int64("max_concurrent_dispatches", d.maxConcurrentJobs))
	return nil
}

// runDispatchLoop runs the main notification dispatch loop.
func (d *pushDispatcher) runDispatchLoop(ctx context.Context, ch <-chan *Notification, service *Service) {
	defer service.Unsubscribe(ch)
	for {
		select {
		case notif, ok := <-ch:
			if !ok || notif == nil {
				return
			}
			if isToastNotification(notif) {
				continue
			}
			go d.dispatch(ctx, notif)
		case <-ctx.Done():
			return
		}
	}
}

// startHealthChecker starts the health checker if enabled.
func (d *pushDispatcher) startHealthChecker(ctx context.Context) {
	if d.healthChecker == nil {
		return
	}
	if err := d.healthChecker.Start(ctx); err != nil {
		d.log.Error("failed to start health checker", logger.Error(err))
	}
}

func (d *pushDispatcher) dispatch(ctx context.Context, notif *Notification) {
	for i := range d.providers {
		ep := &d.providers[i]
		if !d.shouldDispatchToProvider(ep, notif) {
			continue
		}

		if !d.acquireSemaphoreSlot(ctx, ep, notif) {
			continue
		}

		d.spawnDispatchGoroutine(ctx, ep, notif)
	}
}

// shouldDispatchToProvider checks if notification should be dispatched to provider.
func (d *pushDispatcher) shouldDispatchToProvider(ep *enhancedProvider, notif *Notification) bool {
	if !ep.prov.IsEnabled() || !ep.prov.SupportsType(notif.Type) {
		return false
	}
	return d.matchesFilter(ep, notif)
}

// acquireSemaphoreSlot attempts to acquire a semaphore slot for dispatch.
// Returns true if slot acquired or semaphore not configured, false if queue is full.
func (d *pushDispatcher) acquireSemaphoreSlot(ctx context.Context, ep *enhancedProvider, notif *Notification) bool {
	if d.concurrencySem == nil {
		return true
	}

	acquireCtx, cancel := context.WithTimeout(ctx, semaphoreAcquireTimeout)
	err := d.concurrencySem.Acquire(acquireCtx, 1)
	cancel()

	if err != nil {
		if d.log != nil {
			d.log.Warn("dispatch queue full, dropping notification",
				logger.String("provider", ep.name),
				logger.String("notification_id", notif.ID),
				logger.Error(err))
		}
		if d.metrics != nil {
			d.metrics.RecordFilterRejection(ep.name, "queue_full")
		}
		return false
	}
	return true
}

// spawnDispatchGoroutine spawns a goroutine to dispatch notification to provider.
func (d *pushDispatcher) spawnDispatchGoroutine(ctx context.Context, ep *enhancedProvider, notif *Notification) {
	go func(provider *enhancedProvider) {
		defer d.recoverFromDispatchPanic(provider, notif)
		d.dispatchEnhanced(ctx, notif, provider)
	}(ep)
}

// recoverFromDispatchPanic handles cleanup and panic recovery for dispatch goroutines.
func (d *pushDispatcher) recoverFromDispatchPanic(provider *enhancedProvider, notif *Notification) {
	if d.concurrencySem != nil {
		d.concurrencySem.Release(1)
	}
	if r := recover(); r != nil {
		if d.log != nil {
			d.log.Error("panic in dispatch goroutine",
				logger.String("provider", provider.name),
				logger.String("notification_id", notif.ID),
				logger.Any("panic", r))
		}
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
				logger.String("provider", ep.name),
				logger.String("notification_id", notif.ID))
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
	d.log.Info("push sent",
		logger.String("provider", providerName),
		logger.String("id", notif.ID),
		logger.String("type", notifType),
		logger.String("priority", string(notif.Priority)),
		logger.Int("attempt", attempts),
		logger.Duration("elapsed", duration))
}

// logCircuitBreakerOpen logs when circuit breaker blocks a request.
func (d *pushDispatcher) logCircuitBreakerOpen(providerName, notifID string) {
	d.log.Warn("push blocked by circuit breaker",
		logger.String("provider", providerName),
		logger.String("id", notifID))
}

// shouldRetry determines if an attempt should be retried.
func (d *pushDispatcher) shouldRetry(err error, attempts int, providerName string) bool {
	var perr *providerError
	retryable := true
	if errors.As(err, &perr) {
		retryable = perr.Retryable
	}

	// Don't retry timeout errors - the message may have been delivered.
	// This addresses issue #1706 where notifications are sent multiple times.
	// Timeout errors occur after the HTTP request body is sent, so the message
	// likely reached the server even if we didn't receive the response.
	if isTimeoutError(err) {
		retryable = false
	}

	if !retryable || attempts > d.maxRetries {
		if d.log != nil {
			d.log.Error("push send failed",
				logger.String("provider", providerName),
				logger.Int("attempts", attempts),
				logger.Error(err),
				logger.Bool("retryable", retryable))
		}
		return false
	}

	if d.metrics != nil {
		d.metrics.RecordRetryAttempt(providerName)
	}
	return true
}

// isTimeoutError checks if an error is a timeout-related error that should not be retried.
// Timeout errors indicate the message may have been delivered but the response was not received.
// Retrying these can cause duplicate notifications (issue #1706).
//
// Also returns true for context.Canceled since retrying during shutdown is pointless.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}

	// Check for context errors (timeout or cancellation)
	// DeadlineExceeded: timeout occurred, message may have been sent
	// Canceled: shutdown in progress, no point retrying
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	// Check for timeout-related error messages from Shoutrrr and HTTP layer.
	// Note: "timeout" matches "Gateway Timeout", "gateway time-out" catches hyphenated variant.
	errStr := strings.ToLower(err.Error())
	timeoutPatterns := []string{
		"timed out",         // Shoutrrr router timeout: "failed to send: timed out"
		"timeout",           // Generic timeout errors, also matches "Gateway Timeout"
		"status: 504",       // HTTP 504 in jsonclient errors: "got unexpected HTTP status: 504"
		"gateway time-out",  // HTTP 504 Gateway Time-out (hyphenated variant)
		"deadline exceeded", // Context deadline as string
	}

	for _, pattern := range timeoutPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
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
	// G404: Cryptographic randomness not needed for retry jitter - this is for load distribution only
	jitterRange := exponential * jitterPercent / JitterDivisor
	jitterMax := int64(jitterRange * JitterMultiplier)
	var jitter time.Duration
	if jitterMax > 0 {
		jitter = time.Duration(rand.Int64N(jitterMax)) - jitterRange //nolint:gosec // Non-cryptographic use for load distribution
	}

	// Apply jitter and ensure final delay stays within bounds
	delay := min(max(exponential+jitter, baseDelay), maxDelay)

	d.log.Debug("waiting for retry with exponential backoff",
		logger.String("provider", providerName),
		logger.Int("attempts", attempts),
		logger.Duration("delay", delay),
		logger.Duration("base_delay", baseDelay),
		logger.Duration("max_delay", maxDelay))

	select {
	case <-ctx.Done():
		d.log.Debug("retry cancelled due to context cancellation",
			logger.String("provider", providerName),
			logger.Int("attempts", attempts))
		return false
	case <-time.After(delay):
		return true
	}
}

// ----------------- Provider construction -----------------

func buildProvider(pc *conf.PushProviderConfig, log logger.Logger) Provider {
	ptype := strings.ToLower(pc.Type)
	types := effectiveTypes(pc.Filter.Types)
	switch ptype {
	case "script":
		return NewScriptProvider(orDefault(pc.Name, "script"), pc.Enabled, pc.Command, pc.Args, pc.Environment, pc.InputFormat, types)
	case "shoutrrr":
		return NewShoutrrrProvider(orDefault(pc.Name, "shoutrrr"), pc.Enabled, pc.URLs, types, pc.Timeout.Std())
	case "webhook":
		endpoints, err := convertWebhookEndpoints(pc.Endpoints, log)
		if err != nil {
			log.Error("failed to resolve webhook secrets",
				logger.String("name", pc.Name),
				logger.Error(err))
			return nil
		}
		provider, err := NewWebhookProvider(orDefault(pc.Name, "webhook"), pc.Enabled, endpoints, types, pc.Template)
		if err != nil {
			log.Error("failed to create webhook provider",
				logger.String("name", pc.Name),
				logger.Error(err))
			return nil
		}
		return provider
	default:
		log.Warn("unknown push provider type; skipping",
			logger.String("name", pc.Name),
			logger.String("type", pc.Type))
		return nil
	}
}

func effectiveTypes(cfg []string) []string {
	if len(cfg) == 0 {
		return []string{"error", "warning", "info", "detection", "system"}
	}
	return slices.Clone(cfg)
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
func MatchesProviderFilterWithReason(f *conf.PushFilterConfig, n *Notification, log logger.Logger, providerName string) (matches bool, reason string) {
	if f == nil {
		logDebug(log, "no filter configured, allowing notification",
			logger.String("provider", providerName),
			logger.String("notification_id", n.ID))
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
func logDebug(log logger.Logger, msg string, fields ...logger.Field) {
	if log != nil {
		log.Debug(msg, fields...)
	}
}

// checkTypeFilter validates the notification type against configured filter
func checkTypeFilter(f *conf.PushFilterConfig, n *Notification, log logger.Logger, providerName string) (matches bool, reason string) {
	if len(f.Types) == 0 {
		return true, ""
	}

	logDebug(log, "checking type filter",
		logger.String("provider", providerName),
		logger.Any("allowed_types", f.Types),
		logger.String("notification_type", string(n.Type)),
		logger.String("notification_id", n.ID))

	if !slices.Contains(f.Types, string(n.Type)) {
		logDebug(log, "filter failed: type mismatch",
			logger.String("provider", providerName),
			logger.Any("allowed_types", f.Types),
			logger.String("notification_type", string(n.Type)),
			logger.String("notification_id", n.ID))
		return false, filterReasonTypeMismatch
	}

	return true, ""
}

// checkPriorityFilter validates the notification priority against configured filter
func checkPriorityFilter(f *conf.PushFilterConfig, n *Notification, log logger.Logger, providerName string) (matches bool, reason string) {
	if len(f.Priorities) == 0 {
		return true, ""
	}

	if !slices.Contains(f.Priorities, string(n.Priority)) {
		logDebug(log, "filter failed: priority mismatch",
			logger.String("provider", providerName),
			logger.Any("allowed_priorities", f.Priorities),
			logger.String("notification_priority", string(n.Priority)),
			logger.String("notification_id", n.ID))
		return false, filterReasonPriorityMismatch
	}

	return true, ""
}

// checkComponentFilter validates the notification component against configured filter
func checkComponentFilter(f *conf.PushFilterConfig, n *Notification, log logger.Logger, providerName string) (matches bool, reason string) {
	if len(f.Components) == 0 {
		return true, ""
	}

	if !slices.Contains(f.Components, n.Component) {
		logDebug(log, "filter failed: component mismatch",
			logger.String("provider", providerName),
			logger.Any("allowed_components", f.Components),
			logger.String("notification_component", n.Component),
			logger.String("notification_id", n.ID))
		return false, filterReasonComponentMismatch
	}

	return true, ""
}

// checkMetadataFilters validates notification metadata against configured filters
func checkMetadataFilters(f *conf.PushFilterConfig, n *Notification, log logger.Logger, providerName string) (matches bool, reason string) {
	for key, val := range f.MetadataFilters {
		logDebug(log, "processing metadata filter",
			logger.String("provider", providerName),
			logger.String("key", key),
			logger.Any("filter_value", val),
			logger.String("notification_id", n.ID))

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
func checkConfidenceFilter(filterVal any, n *Notification, log logger.Logger, providerName string) (matches bool, reason string) {
	cond, ok := filterVal.(string)
	if !ok {
		logDebug(log, "filter failed: confidence filter misconfigured",
			logger.String("provider", providerName),
			logger.Any("filter_value", filterVal),
			logger.String("notification_id", n.ID))
		return false, filterReasonConfidenceThreshold
	}

	cond = strings.TrimSpace(cond)
	if cond == "" {
		logDebug(log, "filter failed: empty confidence condition",
			logger.String("provider", providerName),
			logger.String("notification_id", n.ID))
		return false, filterReasonConfidenceThreshold
	}

	// Parse operator and value
	op, valStr := parseConfidenceOperator(cond)
	if op == "" {
		logDebug(log, "filter failed: unknown confidence operator",
			logger.String("provider", providerName),
			logger.String("condition", cond),
			logger.String("notification_id", n.ID))
		return false, filterReasonConfidenceThreshold
	}

	threshold, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		logDebug(log, "filter failed: invalid confidence threshold",
			logger.String("provider", providerName),
			logger.String("threshold_str", valStr),
			logger.Error(err),
			logger.String("notification_id", n.ID))
		return false, filterReasonConfidenceThreshold
	}

	// Get confidence value from notification metadata
	raw, exists := n.Metadata["confidence"]
	if !exists {
		logDebug(log, "filter failed: confidence metadata missing",
			logger.String("provider", providerName),
			logger.Any("available_metadata", n.Metadata),
			logger.String("notification_id", n.ID))
		return false, filterReasonConfidenceThreshold
	}

	cv, ok := toFloat(raw)
	if !ok {
		logDebug(log, "filter failed: confidence value not parseable",
			logger.String("provider", providerName),
			logger.Any("confidence_value", raw),
			logger.String("notification_id", n.ID))
		return false, filterReasonConfidenceThreshold
	}

	// Check if confidence meets threshold
	if !compareConfidence(cv, op, threshold) {
		logDebug(log, "filter failed: confidence threshold not met",
			logger.String("provider", providerName),
			logger.String("condition", cond),
			logger.Float64("actual_confidence", cv),
			logger.String("notification_id", n.ID))
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
func checkExactMetadataMatch(key string, expectedVal any, n *Notification, log logger.Logger, providerName string) (matches bool, reason string) {
	actualVal, ok := n.Metadata[key]
	if !ok {
		logDebug(log, "filter failed: metadata key missing",
			logger.String("provider", providerName),
			logger.String("required_key", key),
			logger.Any("available_metadata", n.Metadata),
			logger.String("notification_id", n.ID))
		return false, filterReasonMetadataMismatch
	}

	if fmt.Sprint(actualVal) != fmt.Sprint(expectedVal) {
		logDebug(log, "filter failed: metadata value mismatch",
			logger.String("provider", providerName),
			logger.String("key", key),
			logger.Any("expected", expectedVal),
			logger.Any("actual", actualVal),
			logger.String("notification_id", n.ID))
		return false, filterReasonMetadataMismatch
	}

	return true, ""
}

// MatchesProviderFilter applies basic filtering based on type/priority/component and simple metadata rules.
// This function is exported for testing purposes and preserved for backward compatibility.
// New code should use MatchesProviderFilterWithReason for better observability.
func MatchesProviderFilter(f *conf.PushFilterConfig, n *Notification, log logger.Logger, providerName string) bool {
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
//
//	toFloat(0.85)      // (0.85, true)
//	toFloat(int(42))   // (42.0, true)
//	toFloat("3.14")    // (3.14, true)
//	toFloat("invalid") // (0, false)
//	toFloat(true)      // (0, false)
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
	cbConfig := d.getCircuitBreakerConfig(settings)
	var enhanced []enhancedProvider

	for i := range settings.Notification.Push.Providers {
		pc := &settings.Notification.Push.Providers[i]
		ep := d.buildEnhancedProvider(pc, cbConfig, settings, notificationMetrics)
		if ep != nil {
			enhanced = append(enhanced, *ep)
		}
	}

	return enhanced
}

// getCircuitBreakerConfig returns circuit breaker config from settings or defaults.
func (d *pushDispatcher) getCircuitBreakerConfig(settings *conf.Settings) CircuitBreakerConfig {
	cbConfig := DefaultCircuitBreakerConfig()
	if !settings.Notification.Push.CircuitBreaker.Enabled {
		return cbConfig
	}

	cbConfig.MaxFailures = settings.Notification.Push.CircuitBreaker.MaxFailures
	cbConfig.Timeout = settings.Notification.Push.CircuitBreaker.Timeout.Std()
	cbConfig.HalfOpenMaxRequests = settings.Notification.Push.CircuitBreaker.HalfOpenMaxRequests

	if err := cbConfig.Validate(); err != nil {
		d.log.Error("invalid circuit breaker configuration, using defaults", logger.Error(err))
		return DefaultCircuitBreakerConfig()
	}
	return cbConfig
}

// buildEnhancedProvider creates a single enhanced provider with all supporting components.
func (d *pushDispatcher) buildEnhancedProvider(pc *conf.PushProviderConfig, cbConfig CircuitBreakerConfig, settings *conf.Settings, notificationMetrics *metrics.NotificationMetrics) *enhancedProvider {
	prov := buildProvider(pc, d.log)
	if prov == nil {
		return nil
	}

	if err := prov.ValidateConfig(); err != nil {
		d.log.Error("push provider config invalid",
			logger.String("name", pc.Name),
			logger.String("type", pc.Type),
			logger.Error(err))
		return nil
	}

	if !prov.IsEnabled() {
		return nil
	}

	name := prov.GetName()
	cb := d.createProviderCircuitBreaker(settings, cbConfig, notificationMetrics, name)
	rl := d.createProviderRateLimiter(settings, name)
	d.injectTelemetry(prov, cb, name)

	ep := &enhancedProvider{
		prov:           prov,
		circuitBreaker: cb,
		rateLimiter:    rl,
		filter:         pc.Filter,
		name:           name,
	}

	d.log.Debug("registered enhanced push provider",
		logger.String("name", name),
		logger.Bool("circuit_breaker", cb != nil),
		logger.Any("types", pc.Filter.Types),
		logger.Any("priorities", pc.Filter.Priorities))

	return ep
}

// createProviderCircuitBreaker creates a circuit breaker for a provider if enabled.
func (d *pushDispatcher) createProviderCircuitBreaker(settings *conf.Settings, cbConfig CircuitBreakerConfig, notificationMetrics *metrics.NotificationMetrics, name string) *PushCircuitBreaker {
	if !settings.Notification.Push.CircuitBreaker.Enabled {
		return nil
	}
	return NewPushCircuitBreaker(cbConfig, notificationMetrics, name)
}

// createProviderRateLimiter creates a rate limiter for a provider if enabled.
func (d *pushDispatcher) createProviderRateLimiter(settings *conf.Settings, name string) *PushRateLimiter {
	if !settings.Notification.Push.RateLimiting.Enabled {
		return nil
	}

	rl := NewPushRateLimiter(PushRateLimiterConfig{
		RequestsPerMinute: settings.Notification.Push.RateLimiting.RequestsPerMinute,
		BurstSize:         settings.Notification.Push.RateLimiting.BurstSize,
	})

	d.log.Info("rate limiter enabled for provider",
		logger.String("provider", name),
		logger.Int("requests_per_minute", settings.Notification.Push.RateLimiting.RequestsPerMinute),
		logger.Int("burst_size", settings.Notification.Push.RateLimiting.BurstSize))

	return rl
}

// injectTelemetry injects telemetry into circuit breaker and provider.
func (d *pushDispatcher) injectTelemetry(prov Provider, cb *PushCircuitBreaker, name string) {
	service := GetService()
	if service == nil || service.GetTelemetry() == nil {
		d.log.Debug("telemetry not available for provider injection",
			logger.String("provider", name),
			logger.Bool("service_exists", service != nil))
		return
	}

	telemetry := service.GetTelemetry()

	if cb != nil {
		cb.SetTelemetry(telemetry)
		d.log.Debug("telemetry injected into circuit breaker",
			logger.String("provider", name),
			logger.Bool("telemetry_enabled", telemetry.IsEnabled()))
	}

	if webhookProv, ok := prov.(*WebhookProvider); ok {
		webhookProv.SetTelemetry(telemetry)
		d.log.Debug("telemetry injected into webhook provider",
			logger.String("provider", name),
			logger.Bool("telemetry_enabled", telemetry.IsEnabled()))
	}
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
func convertWebhookEndpoints(cfgEndpoints []conf.WebhookEndpointConfig, log logger.Logger) ([]WebhookEndpoint, error) {
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
			Timeout: cfg.Timeout.Std(),
			Auth:    *auth,
		})
	}
	return endpoints, nil
}

// containsLocalhost checks if a URL contains localhost or loopback IP address.
// Uses proper URL parsing to avoid false positives from substring matching.
func containsLocalhost(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// hasExternalWebhooks checks if any webhook providers are configured with external URLs
// (not localhost, 127.0.0.1, or private networks). Uses proper IP parsing to detect
// loopback and private addresses per RFC 1918 (IPv4) and RFC 4193 (IPv6).
func hasExternalWebhooks(providers []enhancedProvider) bool {
	for i := range providers {
		if webhookProv, ok := providers[i].prov.(*WebhookProvider); ok {
			endpoints := webhookProv.GetEndpoints()
			for j := range endpoints {
				if !isPrivateOrLocalURL(endpoints[j].URL) {
					return true
				}
			}
		}
	}
	return false
}

// isPrivateOrLocalURL checks if a URL points to localhost or a private network.
// Returns true for loopback addresses (127.0.0.0/8, ::1), private networks
// (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7), link-local addresses
// (169.254.0.0/16, fe80::/10), and CGNAT addresses (100.64.0.0/10).
func isPrivateOrLocalURL(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}

	hostname := u.Hostname()

	if strings.EqualFold(hostname, "localhost") {
		return true
	}

	if isPrivateIP(hostname) {
		return true
	}

	return hasInternalTLD(hostname)
}

// isPrivateIP checks if the hostname is a private, loopback, or link-local IP.
func isPrivateIP(hostname string) bool {
	// Strip zone ID from IPv6 addresses
	if idx := strings.IndexByte(hostname, '%'); idx >= 0 {
		hostname = hostname[:idx]
	}

	ip := net.ParseIP(hostname)
	if ip == nil {
		return false
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}

	return isCGNATAddress(ip)
}

// isCGNATAddress checks if IP is in CGNAT range (100.64.0.0/10, RFC 6598).
func isCGNATAddress(ip net.IP) bool {
	ipv4 := ip.To4()
	if ipv4 == nil {
		return false
	}
	return ipv4[0] == 100 && (ipv4[1]&SharedAddressMask) == 64
}

// hasInternalTLD checks if hostname has a common internal/private TLD.
func hasInternalTLD(hostname string) bool {
	internalTLDs := []string{
		".local", ".internal", ".lan", ".home", ".corp", ".private",
	}

	lowerHostname := strings.ToLower(hostname)
	for _, tld := range internalTLDs {
		if strings.HasSuffix(lowerHostname, tld) {
			return true
		}
	}
	return false
}
