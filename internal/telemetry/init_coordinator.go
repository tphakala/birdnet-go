package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// InitCoordinator provides a safe, ordered initialization of telemetry components
type InitCoordinator struct {
	manager *InitManager
}

// NewInitCoordinator creates a new initialization coordinator
func NewInitCoordinator() *InitCoordinator {
	return &InitCoordinator{
		manager: GetInitManager(),
	}
}

// InitializeAll performs complete telemetry initialization in the correct order
func (c *InitCoordinator) InitializeAll(settings *conf.Settings) error {
	logger := getLoggerSafe("init-coordinator")
	logger.Info("starting telemetry initialization sequence")

	// Phase 1: Initialize error integration (synchronous reporting)
	if err := c.manager.InitializeErrorIntegrationSafe(settings); err != nil {
		return fmt.Errorf("error integration initialization failed: %w", err)
	}

	// Phase 2: Initialize Sentry if enabled
	if settings.Sentry.Enabled {
		if err := c.manager.InitializeSentrySafe(settings); err != nil {
			// Log but don't fail - Sentry is not critical
			logger.Error("Sentry initialization failed", "error", err)
		}
	}

	// Phase 3: Event bus integration is deferred until after main services are ready
	logger.Info("telemetry initialization sequence completed (event bus integration deferred)")
	return nil
}

// InitializeEventBusIntegration should be called after all core services are initialized
func (c *InitCoordinator) InitializeEventBusIntegration(settings *conf.Settings) error {
	logger := getLoggerSafe("init-coordinator")
	
	// Check prerequisites
	if !logging.IsInitialized() {
		return fmt.Errorf("logging not initialized")
	}

	if !events.IsInitialized() {
		return fmt.Errorf("event bus not initialized")
	}

	// Initialize event bus integration
	logger.Info("initializing telemetry event bus integration")
	if settings == nil {
		return fmt.Errorf("settings not available for event bus integration")
	}
	if err := c.manager.InitializeEventBusSafe(settings); err != nil {
		return fmt.Errorf("event bus integration failed: %w", err)
	}

	return nil
}

// WaitForInitialization waits for all components to be initialized
func (c *InitCoordinator) WaitForInitialization(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	components := []struct {
		name         string
		required     bool
		waitForState InitState
	}{
		{"error_integration", true, InitStateCompleted},
		{"sentry", false, InitStateCompleted}, // Not required
		{"event_bus", false, InitStateCompleted}, // May be deferred
	}

	for _, comp := range components {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for initialization")
		default:
			state := c.manager.GetComponentState(comp.name)
			
			// Skip if not started (may be intentionally deferred)
			if state == InitStateNotStarted && !comp.required {
				continue
			}

			// Wait for completion or failure
			if err := c.manager.WaitForComponent(comp.name, comp.waitForState, 5*time.Second); err != nil {
				if comp.required {
					return fmt.Errorf("required component %s failed: %w", comp.name, err)
				}
				// Log non-required failures
				logger := getLoggerSafe("init-coordinator")
				logger.Warn("optional component initialization failed", 
					"component", comp.name, 
					"error", err)
			}
		}
	}

	return nil
}

// HealthCheck returns the health status of all telemetry components
func (c *InitCoordinator) HealthCheck() HealthStatus {
	return c.manager.HealthCheck()
}

// Shutdown performs graceful shutdown of telemetry components
func (c *InitCoordinator) Shutdown(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	
	return c.manager.Shutdown(ctx)
}

// GlobalInitCoordinator provides a global initialization coordinator
var globalInitCoordinator *InitCoordinator

// GetGlobalInitCoordinator returns the global init coordinator instance
// This is used by debug endpoints to access telemetry health status
func GetGlobalInitCoordinator() *InitCoordinator {
	return globalInitCoordinator
}

// Initialize creates the global init coordinator and performs basic initialization
func Initialize(settings *conf.Settings) error {
	if globalInitCoordinator == nil {
		globalInitCoordinator = NewInitCoordinator()
	}
	return globalInitCoordinator.InitializeAll(settings)
}

// InitializeEventBus initializes event bus integration (call after core services are ready)
// Deprecated: This function will be updated to require explicit settings parameter
func InitializeEventBus() error {
	if globalInitCoordinator == nil {
		return fmt.Errorf("telemetry not initialized")
	}
	// TODO: This function needs to be updated to accept settings parameter
	// For now, we'll return an error indicating that explicit initialization is required
	return fmt.Errorf("InitializeEventBus() is deprecated - use InitializeEventBusIntegration(settings) directly through SystemInitManager")
}

// WaitForReady waits for telemetry to be ready
func WaitForReady(timeout time.Duration) error {
	if globalInitCoordinator == nil {
		return fmt.Errorf("telemetry not initialized")
	}
	return globalInitCoordinator.WaitForInitialization(timeout)
}

// GetHealthStatus returns current health status
func GetHealthStatus() HealthStatus {
	if globalInitCoordinator == nil {
		return HealthStatus{
			Healthy:   false,
			Timestamp: time.Now(),
		}
	}
	return globalInitCoordinator.HealthCheck()
}

// Shutdown performs graceful shutdown
func Shutdown(timeout time.Duration) error {
	if globalInitCoordinator == nil {
		return nil
	}
	return globalInitCoordinator.Shutdown(timeout)
}