package telemetry

import (
	"sync"
	"sync/atomic"
	
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
)

var (
	// deferredInitMutex protects deferred initialization
	deferredInitMutex sync.Mutex
	
	// telemetryInitialized tracks if telemetry event bus integration is done
	telemetryInitialized atomic.Bool
)

// InitializeTelemetryEventBus initializes the telemetry event bus integration
// This should be called after all core services are initialized to avoid deadlocks
func InitializeTelemetryEventBus() error {
	deferredInitMutex.Lock()
	defer deferredInitMutex.Unlock()
	
	// Check if already initialized
	if telemetryInitialized.Load() {
		return nil
	}
	
	logger := getLoggerSafe("telemetry-init")
	
	// Step 1: Initialize event bus if not already done
	if !events.IsInitialized() {
		logger.Warn("event bus not initialized, cannot enable telemetry integration")
		return nil
	}
	
	// Step 2: Set up the event publisher in errors package
	eventBus := events.GetEventBus()
	if eventBus == nil {
		logger.Warn("event bus is nil, cannot enable telemetry integration")
		return nil
	}
	
	// Create and set the event publisher adapter
	adapter := events.NewEventPublisherAdapter(eventBus)
	errors.SetEventPublisher(adapter)
	
	logger.Info("enabled error package event bus integration")
	
	// Step 3: Initialize telemetry worker and register it
	if err := InitializeEventBusIntegration(); err != nil {
		logger.Error("failed to initialize telemetry event bus integration", "error", err)
		return err
	}
	
	telemetryInitialized.Store(true)
	logger.Info("telemetry event bus integration completed successfully")
	
	return nil
}

// IsTelemetryEventBusEnabled returns true if telemetry is using event bus
func IsTelemetryEventBusEnabled() bool {
	return telemetryInitialized.Load()
}