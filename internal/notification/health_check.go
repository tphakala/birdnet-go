package notification

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// ProviderHealth represents the health status of a push provider.
type ProviderHealth struct {
	ProviderName        string
	Healthy             bool
	LastCheckTime       time.Time
	LastSuccessTime     time.Time
	LastFailureTime     time.Time
	ConsecutiveFailures int
	TotalAttempts       int
	TotalSuccesses      int
	TotalFailures       int
	CircuitBreakerState CircuitState
	ErrorMessage        string
}

// HealthChecker periodically checks the health of push providers.
type HealthChecker struct {
	providers map[string]*healthCheckEntry
	interval  time.Duration
	timeout   time.Duration
	log       logger.Logger
	metrics   *metrics.NotificationMetrics
	mu        sync.RWMutex
	cancel    context.CancelFunc
	baseCtx   context.Context // Parent context for deriving check contexts
}

type healthCheckEntry struct {
	provider       Provider
	circuitBreaker *PushCircuitBreaker
	health         ProviderHealth
	mu             sync.RWMutex
}

// HealthCheckConfig holds configuration for the health checker.
type HealthCheckConfig struct {
	Enabled  bool
	Interval time.Duration
	Timeout  time.Duration
}

// DefaultHealthCheckConfig returns default health check configuration.
func DefaultHealthCheckConfig() HealthCheckConfig {
	return HealthCheckConfig{
		Enabled:  true,
		Interval: DefaultHealthCheckInterval,
		Timeout:  DefaultHealthCheckTimeout,
	}
}

// Validate checks if the health check configuration is valid.
func (c HealthCheckConfig) Validate() error {
	if c.Interval < time.Second {
		return fmt.Errorf("interval must be at least 1 second, got %v", c.Interval)
	}
	if c.Timeout < time.Second {
		return fmt.Errorf("timeout must be at least 1 second, got %v", c.Timeout)
	}
	if c.Timeout >= c.Interval {
		return fmt.Errorf("timeout (%v) must be less than interval (%v)", c.Timeout, c.Interval)
	}
	return nil
}

// NewHealthChecker creates a new HealthChecker.
func NewHealthChecker(config HealthCheckConfig, log logger.Logger, notificationMetrics *metrics.NotificationMetrics) *HealthChecker {
	return &HealthChecker{
		providers: make(map[string]*healthCheckEntry),
		interval:  config.Interval,
		timeout:   config.Timeout,
		log:       log,
		metrics:   notificationMetrics,
	}
}

// RegisterProvider registers a provider for health checking.
func (hc *HealthChecker) RegisterProvider(provider Provider, circuitBreaker *PushCircuitBreaker) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	name := provider.GetName()
	hc.providers[name] = &healthCheckEntry{
		provider:       provider,
		circuitBreaker: circuitBreaker,
		health: ProviderHealth{
			ProviderName:    name,
			Healthy:         true,
			LastCheckTime:   time.Now(),
			LastSuccessTime: time.Now(),
		},
	}

	if hc.log != nil {
		hc.log.Debug("registered provider for health checking", logger.String("provider", name))
	}
}

// Start begins periodic health checks.
func (hc *HealthChecker) Start(ctx context.Context) error {
	if hc.cancel != nil {
		return fmt.Errorf("health checker already started")
	}

	ctx, cancel := context.WithCancel(ctx)
	hc.cancel = cancel
	hc.baseCtx = ctx // Store parent context for deriving check contexts

	// Perform initial health check
	hc.checkAll()

	// Start periodic checks
	ticker := time.NewTicker(hc.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				hc.checkAll()
			case <-ctx.Done():
				return
			}
		}
	}()

	if hc.log != nil {
		hc.log.Info("health checker started",
			logger.Duration("interval", hc.interval),
			logger.Int("providers", len(hc.providers)))
	}

	return nil
}

// Stop stops the health checker.
func (hc *HealthChecker) Stop() {
	if hc.cancel != nil {
		hc.cancel()
		hc.cancel = nil
	}
}

// checkAll performs health checks on all registered providers.
func (hc *HealthChecker) checkAll() {
	hc.mu.RLock()
	entries := slices.Collect(maps.Values(hc.providers))
	hc.mu.RUnlock()

	// Check providers concurrently
	var wg sync.WaitGroup
	for _, entry := range entries {
		wg.Add(1)
		go func(e *healthCheckEntry) {
			defer wg.Done()
			hc.checkProvider(e)
		}(entry)
	}
	wg.Wait()
}

// checkProvider performs a health check on a single provider.
// This method is optimized to avoid holding the entry lock during provider calls,
// which could block GetProviderHealth() calls for extended periods.
func (hc *HealthChecker) checkProvider(entry *healthCheckEntry) {
	// Step 1: Lock briefly to snapshot necessary data and pre-increment counters
	entry.mu.Lock()
	providerName := entry.provider.GetName()
	provider := entry.provider
	circuitBreaker := entry.circuitBreaker
	entry.health.LastCheckTime = time.Now()
	entry.health.TotalAttempts++
	entry.mu.Unlock()

	// Step 2: Perform health check WITHOUT holding lock (allows concurrent reads)
	err := hc.executeHealthCheck(provider, circuitBreaker)

	// Step 3: Lock briefly to update health status based on check result
	entry.mu.Lock()
	defer entry.mu.Unlock()

	hc.updateHealthStatus(entry, providerName, err)
	hc.updateCircuitBreakerState(entry, circuitBreaker)
	hc.logHealthCheckResult(entry, providerName)
}

// executeHealthCheck performs the actual health check with optional circuit breaker.
func (hc *HealthChecker) executeHealthCheck(provider Provider, circuitBreaker *PushCircuitBreaker) error {
	ctx, cancel := context.WithTimeout(hc.baseCtx, hc.timeout)
	defer cancel()

	if circuitBreaker != nil {
		return circuitBreaker.Call(ctx, func(_ context.Context) error {
			return provider.ValidateConfig()
		})
	}
	return provider.ValidateConfig()
}

// updateHealthStatus updates the health entry based on the check result.
// Circuit breaker gating (open/half-open) is treated as neutral - not counted as failure or success.
func (hc *HealthChecker) updateHealthStatus(entry *healthCheckEntry, providerName string, err error) {
	if hc.isCircuitBreakerGating(err) {
		return // Neutral - don't update health status
	}

	if err == nil {
		hc.recordHealthSuccess(entry, providerName)
	} else {
		hc.recordHealthFailure(entry, providerName, err)
	}
}

// isCircuitBreakerGating checks if the error indicates circuit breaker gating.
func (hc *HealthChecker) isCircuitBreakerGating(err error) bool {
	return errors.Is(err, ErrCircuitBreakerOpen) || errors.Is(err, ErrTooManyRequests)
}

// recordHealthSuccess updates health status for a successful check.
func (hc *HealthChecker) recordHealthSuccess(entry *healthCheckEntry, providerName string) {
	entry.health.Healthy = true
	entry.health.LastSuccessTime = time.Now()
	entry.health.TotalSuccesses++
	entry.health.ConsecutiveFailures = 0
	entry.health.ErrorMessage = ""

	if hc.metrics != nil {
		hc.metrics.UpdateHealthStatus(providerName, true)
	}
}

// recordHealthFailure updates health status for a failed check.
func (hc *HealthChecker) recordHealthFailure(entry *healthCheckEntry, providerName string, err error) {
	entry.health.Healthy = false
	entry.health.LastFailureTime = time.Now()
	entry.health.TotalFailures++
	entry.health.ConsecutiveFailures++
	entry.health.ErrorMessage = err.Error()

	if hc.metrics != nil {
		hc.metrics.UpdateHealthStatus(providerName, false)
	}

	if hc.log != nil {
		hc.log.Warn("provider health check failed",
			logger.String("provider", providerName),
			logger.Error(err),
			logger.Int("consecutive_failures", entry.health.ConsecutiveFailures))
	}
}

// updateCircuitBreakerState syncs the circuit breaker state to the health entry.
func (hc *HealthChecker) updateCircuitBreakerState(entry *healthCheckEntry, circuitBreaker *PushCircuitBreaker) {
	if circuitBreaker != nil {
		entry.health.CircuitBreakerState = circuitBreaker.State()
	}
}

// logHealthCheckResult logs successful health checks at debug level.
func (hc *HealthChecker) logHealthCheckResult(entry *healthCheckEntry, providerName string) {
	if entry.health.Healthy && hc.log != nil {
		hc.log.Debug("provider health check passed",
			logger.String("provider", providerName),
			logger.Int("total_successes", entry.health.TotalSuccesses),
			logger.Int("total_failures", entry.health.TotalFailures))
	}
}

// GetProviderHealth returns the health status of a specific provider.
func (hc *HealthChecker) GetProviderHealth(providerName string) (ProviderHealth, bool) {
	hc.mu.RLock()
	entry, exists := hc.providers[providerName]
	hc.mu.RUnlock()

	if !exists {
		return ProviderHealth{}, false
	}

	entry.mu.RLock()
	defer entry.mu.RUnlock()

	// Return a copy to avoid race conditions
	return entry.health, true
}

// GetAllProviderHealth returns health status for all providers.
func (hc *HealthChecker) GetAllProviderHealth() []ProviderHealth {
	hc.mu.RLock()
	entries := slices.Collect(maps.Values(hc.providers))
	hc.mu.RUnlock()

	results := make([]ProviderHealth, 0, len(entries))
	for _, entry := range entries {
		entry.mu.RLock()
		results = append(results, entry.health)
		entry.mu.RUnlock()
	}

	return results
}

// IsHealthy returns true if all enabled providers are healthy.
func (hc *HealthChecker) IsHealthy() bool {
	health := hc.GetAllProviderHealth()
	for i := range health {
		if !health[i].Healthy && health[i].CircuitBreakerState != StateOpen {
			// Provider is unhealthy and not just circuit-broken
			return false
		}
	}
	return true
}

// GetHealthSummary returns a summary of overall health.
func (hc *HealthChecker) GetHealthSummary() map[string]any {
	health := hc.GetAllProviderHealth()

	totalProviders := len(health)
	healthyProviders := 0
	openCircuits := 0

	for i := range health {
		if health[i].Healthy {
			healthyProviders++
		}
		if health[i].CircuitBreakerState == StateOpen {
			openCircuits++
		}
	}

	return map[string]any{
		"total_providers":   totalProviders,
		"healthy_providers": healthyProviders,
		"open_circuits":     openCircuits,
		"overall_healthy":   hc.IsHealthy(),
		"last_check":        time.Now(),
	}
}
