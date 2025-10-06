package notification

import (
	"errors"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

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
	providers         map[string]*healthCheckEntry
	interval          time.Duration
	timeout           time.Duration
	log               *slog.Logger
	metrics           *metrics.NotificationMetrics
	mu                sync.RWMutex
	cancel            context.CancelFunc
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
		Interval: 60 * time.Second,
		Timeout:  10 * time.Second,
	}
}

// NewHealthChecker creates a new HealthChecker.
func NewHealthChecker(config HealthCheckConfig, log *slog.Logger, notificationMetrics *metrics.NotificationMetrics) *HealthChecker {
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
		hc.log.Debug("registered provider for health checking", "provider", name)
	}
}

// Start begins periodic health checks.
func (hc *HealthChecker) Start(ctx context.Context) error {
	if hc.cancel != nil {
		return fmt.Errorf("health checker already started")
	}

	ctx, cancel := context.WithCancel(ctx)
	hc.cancel = cancel

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
		hc.log.Info("health checker started", "interval", hc.interval, "providers", len(hc.providers))
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
	entries := make([]*healthCheckEntry, 0, len(hc.providers))
	for _, entry := range hc.providers {
		entries = append(entries, entry)
	}
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
func (hc *HealthChecker) checkProvider(entry *healthCheckEntry) {
	entry.mu.Lock()
	defer entry.mu.Unlock()

	providerName := entry.provider.GetName()
	ctx, cancel := context.WithTimeout(context.Background(), hc.timeout)
	defer cancel()

	// Update check time
	entry.health.LastCheckTime = time.Now()
	entry.health.TotalAttempts++

	// Create a minimal test notification
	testNotif := &Notification{
		Type:      TypeSystem,
		Priority:  PriorityLow,
		Title:     "Health Check",
		Message:   "Provider health check test",
		Component: "health_check",
		Metadata:  map[string]any{"health_check": true},
	}

	// Attempt to send through circuit breaker
	var err error
	if entry.circuitBreaker != nil {
		err = entry.circuitBreaker.Call(ctx, func(ctx context.Context) error {
			// For health checks, we don't actually send - we just validate the provider is responsive
			// Providers can implement a lightweight health check method if available
			return entry.provider.ValidateConfig()
		})
	} else {
		// Fallback if no circuit breaker
		err = entry.provider.ValidateConfig()
	}

	// Update health status
	if err == nil || errors.Is(err, ErrCircuitBreakerOpen) {
		// Circuit breaker open is not a provider failure, it's a protective measure
		if !errors.Is(err, ErrCircuitBreakerOpen) {
			entry.health.Healthy = true
			entry.health.LastSuccessTime = time.Now()
			entry.health.TotalSuccesses++
			entry.health.ConsecutiveFailures = 0
			entry.health.ErrorMessage = ""

			if hc.metrics != nil {
				hc.metrics.UpdateHealthStatus(providerName, true)
			}
		}
	} else {
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
				"provider", providerName,
				"error", err,
				"consecutive_failures", entry.health.ConsecutiveFailures)
		}
	}

	// Update circuit breaker state
	if entry.circuitBreaker != nil {
		entry.health.CircuitBreakerState = entry.circuitBreaker.State()
	}

	// Log successful checks at debug level
	if entry.health.Healthy && hc.log != nil {
		hc.log.Debug("provider health check passed",
			"provider", providerName,
			"total_successes", entry.health.TotalSuccesses,
			"total_failures", entry.health.TotalFailures)
	}

	// Attempt to send test notification for real connectivity test
	// This is optional and only done if provider is enabled and healthy
	if entry.health.Healthy && entry.provider.IsEnabled() {
		testCtx, testCancel := context.WithTimeout(context.Background(), hc.timeout)
		defer testCancel()

		// Only send test notification if provider supports it (not for all types)
		// This is skipped for now to avoid spamming notification channels
		_ = testCtx
		_ = testNotif
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
	entries := make([]*healthCheckEntry, 0, len(hc.providers))
	for _, entry := range hc.providers {
		entries = append(entries, entry)
	}
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
func (hc *HealthChecker) GetHealthSummary() map[string]interface{} {
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

	return map[string]interface{}{
		"total_providers":   totalProviders,
		"healthy_providers": healthyProviders,
		"open_circuits":     openCircuits,
		"overall_healthy":   hc.IsHealthy(),
		"last_check":        time.Now(),
	}
}
