package telemetry

import (
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
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
	
	initLog := GetLogger().With(logger.String("component", "telemetry-init"))

	// Step 1: Initialize event bus if not already done
	if !events.IsInitialized() {
		initLog.Warn("event bus not initialized, cannot enable telemetry integration")
		return nil
	}

	// Step 2: Set up the event publisher in errors package
	eventBus := events.GetEventBus()
	if eventBus == nil {
		initLog.Warn("event bus is nil, cannot enable telemetry integration")
		return nil
	}

	// Create and set the event publisher adapter
	adapter := events.NewEventPublisherAdapter(eventBus)
	errors.SetEventPublisher(adapter)

	initLog.Info("enabled error package event bus integration")

	// Step 3: Initialize telemetry worker and register it
	if err := InitializeEventBusIntegration(); err != nil {
		initLog.Error("failed to initialize telemetry event bus integration", logger.Error(err))
		return err
	}

	telemetryInitialized.Store(true)
	initLog.Info("telemetry event bus integration completed successfully")
	
	return nil
}

// IsTelemetryEventBusEnabled returns true if telemetry is using event bus
func IsTelemetryEventBusEnabled() bool {
	return telemetryInitialized.Load()
}