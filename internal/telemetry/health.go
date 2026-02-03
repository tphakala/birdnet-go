package telemetry

import (
	"encoding/json"
	"fmt"
	"math/bits"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// Health check constants
const (
	// minEventsForFailureCheck is the minimum events before checking failure rate
	minEventsForFailureCheck = 100
	// maxFailureRateThreshold is the maximum acceptable failure rate (10%)
	maxFailureRateThreshold = 0.1
	// failureRatePercentMultiplier converts rate to percentage
	failureRatePercentMultiplier = 100
)

// HealthCheckHandler provides HTTP health check endpoint for telemetry
type HealthCheckHandler struct {
	coordinator *InitCoordinator
}

// NewHealthCheckHandler creates a new health check handler
func NewHealthCheckHandler() *HealthCheckHandler {
	return &HealthCheckHandler{
		coordinator: globalInitCoordinator,
	}
}

// ServeHTTP implements http.Handler for health checks
func (h *HealthCheckHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Use request context for potential timeout handling
	ctx := r.Context()

	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		w.WriteHeader(http.StatusRequestTimeout)
		return
	default:
	}

	if h.coordinator == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"status": "error",
			"error":  "telemetry not initialized",
		}); err != nil {
			// Log the error but response headers are already set
			GetLogger().Error("failed to encode health check error response", logger.Error(err))
		}
		return
	}

	status := h.coordinator.HealthCheck()

	// Determine HTTP status code
	httpStatus := http.StatusOK
	if !status.Healthy {
		httpStatus = http.StatusServiceUnavailable
	}

	// Convert to JSON-friendly format
	components := make(map[string]any)
	response := map[string]any{
		"status":     getOverallStatus(status),
		"timestamp":  status.Timestamp.Format(time.RFC3339),
		"components": components,
	}

	// Add component details
	for name, health := range status.Components {
		componentInfo := map[string]any{
			"state":   health.State.String(),
			"healthy": health.Healthy,
		}
		if health.Error != "" {
			componentInfo["error"] = health.Error
		}
		components[name] = componentInfo
	}

	// Add worker statistics if available
	if worker := GetTelemetryWorker(); worker != nil {
		stats := worker.GetStats()
		response["statistics"] = map[string]any{
			"events_processed": stats.EventsProcessed,
			"events_dropped":   stats.EventsDropped,
			"events_failed":    stats.EventsFailed,
			"circuit_state":    stats.CircuitState,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		// Log the error but response headers are already set
		GetLogger().Error("failed to encode health check response", logger.Error(err))
	}
}

// getOverallStatus returns a string status based on health
func getOverallStatus(status HealthStatus) string {
	if status.Healthy {
		return "healthy"
	}

	// Check if any critical components failed
	for name, health := range status.Components {
		if name == ComponentErrorIntegration && health.State == InitStateFailed {
			return "critical"
		}
		if health.State == InitStateFailed {
			return "degraded"
		}
	}

	return "initializing"
}

// RegisterHealthCheck registers the telemetry health check endpoint
func RegisterHealthCheck(mux *http.ServeMux, path string) {
	handler := NewHealthCheckHandler()
	mux.Handle(path, handler)
}

// PeriodicHealthCheck runs periodic health checks and logs warnings
func PeriodicHealthCheck(interval time.Duration, stopChan <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	healthLog := GetLogger().With(logger.String("component", "health-check"))

	for {
		select {
		case <-ticker.C:
			if globalInitCoordinator != nil {
				status := globalInitCoordinator.HealthCheck()
				if !status.Healthy {
					healthLog.Warn("telemetry health check failed",
						logger.String("status", getOverallStatus(status)),
						logger.String("components", formatUnhealthyComponents(status)))
				}
			}
		case <-stopChan:
			return
		}
	}
}

// formatUnhealthyComponents returns a formatted string of unhealthy components
func formatUnhealthyComponents(status HealthStatus) string {
	var unhealthy []string
	for name, health := range status.Components {
		if !health.Healthy && health.State != InitStateNotStarted {
			unhealthy = append(unhealthy, fmt.Sprintf("[%s:%s]", name, health.State))
		}
	}
	if len(unhealthy) == 0 {
		return "none"
	}
	// Sort for deterministic output (map iteration order is random)
	slices.Sort(unhealthy)
	return strings.Join(unhealthy, " ")
}

// WorkerHealthCheck checks the health of the telemetry worker specifically
func WorkerHealthCheck() error {
	worker := GetTelemetryWorker()
	if worker == nil {
		return fmt.Errorf("telemetry worker not initialized")
	}

	stats := worker.GetStats()

	// Check circuit breaker state
	if stats.CircuitState == circuitStateOpen {
		return fmt.Errorf("circuit breaker open, telemetry reporting suspended")
	}

	// Check failure rate only after sufficient events
	// Use checked addition to detect overflow (unlikely but possible with uint64)
	total, carry := bits.Add64(stats.EventsProcessed, stats.EventsFailed, 0)
	if carry != 0 {
		// Overflow occurred - treat as unhealthy since we can't calculate rate
		return fmt.Errorf("event counter overflow detected")
	}
	if total > minEventsForFailureCheck {
		failureRate := float64(stats.EventsFailed) / float64(total)
		if failureRate > maxFailureRateThreshold {
			return fmt.Errorf("high failure rate: %.2f%%", failureRate*failureRatePercentMultiplier)
		}
	}

	return nil
}
