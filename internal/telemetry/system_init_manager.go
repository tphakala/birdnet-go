package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// Default configuration values for notification service
const (
	// DefaultMaxNotifications is the default maximum number of notifications to keep in memory
	DefaultMaxNotifications = 1000
	// DefaultCleanupInterval is the default interval for cleaning up expired notifications
	DefaultCleanupInterval = 5 * time.Minute
	// DefaultRateLimitWindow is the default time window for rate limiting
	DefaultRateLimitWindow = 1 * time.Minute
	// DefaultRateLimitMaxEvents is the default maximum number of events per rate limit window
	DefaultRateLimitMaxEvents = 100
)

// SystemInitManager manages initialization of all async subsystems
type SystemInitManager struct {
	telemetryCoordinator   *InitCoordinator
	notificationInitOnce   sync.Once
	eventBusInitOnce       sync.Once
	notificationWorkerOnce sync.Once
	
	notificationErr error
	eventBusErr     error
	workerErr       error
	
	mu     sync.RWMutex
	logger *slog.Logger
}

var (
	systemInitManager     *SystemInitManager
	systemInitManagerOnce sync.Once
)

// GetSystemInitManager returns the singleton system init manager
func GetSystemInitManager() *SystemInitManager {
	systemInitManagerOnce.Do(func() {
		// Defensive check for globalInitCoordinator
		var coordinator *InitCoordinator
		if globalInitCoordinator != nil {
			coordinator = globalInitCoordinator
		}
		
		systemInitManager = &SystemInitManager{
			telemetryCoordinator: coordinator,
			logger:              getLoggerSafe("system-init"),
		}
	})
	return systemInitManager
}

// InitializeCore initializes core services (telemetry and notification)
func (m *SystemInitManager) InitializeCore(settings *conf.Settings) error {
	m.logger.Info("starting core services initialization")

	// Phase 1: Initialize telemetry (synchronous error reporting)
	if err := m.initializeTelemetry(settings); err != nil {
		return fmt.Errorf("telemetry initialization failed: %w", err)
	}

	// Phase 2: Initialize notification service
	if err := m.initializeNotification(); err != nil {
		return fmt.Errorf("notification initialization failed: %w", err)
	}

	m.logger.Info("core services initialization completed")
	return nil
}

// InitializeAsyncServices initializes async services (event bus and workers)
func (m *SystemInitManager) InitializeAsyncServices() error {
	m.logger.Info("starting async services initialization")

	// Phase 1: Initialize event bus
	if err := m.initializeEventBus(); err != nil {
		return fmt.Errorf("event bus initialization failed: %w", err)
	}

	// Phase 2: Initialize notification worker
	if err := m.initializeNotificationWorker(); err != nil {
		// Log but don't fail - notification worker is not critical
		m.logger.Error("notification worker initialization failed", "error", err)
	}

	// Phase 3: Initialize telemetry event bus integration
	if err := m.initializeTelemetryEventBus(); err != nil {
		// Log but don't fail - telemetry is not critical
		m.logger.Error("telemetry event bus initialization failed", "error", err)
	}

	m.logger.Info("async services initialization completed")
	return nil
}

// initializeTelemetry initializes the telemetry system
func (m *SystemInitManager) initializeTelemetry(settings *conf.Settings) error {
	if m.telemetryCoordinator == nil {
		// Create a new coordinator if one doesn't exist
		m.telemetryCoordinator = NewInitCoordinator()
		if m.telemetryCoordinator == nil {
			// Fallback to direct initialization
			return Initialize(settings)
		}
	}
	return m.telemetryCoordinator.InitializeAll(settings)
}

// initializeNotification initializes the notification service
func (m *SystemInitManager) initializeNotification() error {
	m.notificationInitOnce.Do(func() {
		m.logger.Debug("initializing notification service")
		
		// Get settings for debug flag
		settings := conf.GetSettings()
		debug := false
		if settings != nil {
			debug = settings.Debug
		}
		
		// Create notification service config
		config := &notification.ServiceConfig{
			Debug:              debug,
			MaxNotifications:   DefaultMaxNotifications,
			CleanupInterval:    DefaultCleanupInterval,
			RateLimitWindow:    DefaultRateLimitWindow,
			RateLimitMaxEvents: DefaultRateLimitMaxEvents,
		}
		
		// Initialize with config
		notification.Initialize(config)
		
		// Verify initialization
		if !notification.IsInitialized() {
			m.notificationErr = fmt.Errorf("notification service initialization failed")
			return
		}
		
		m.logger.Info("notification service initialized successfully", "debug", debug)
	})
	
	return m.notificationErr
}

// initializeEventBus initializes the event bus
func (m *SystemInitManager) initializeEventBus() error {
	m.eventBusInitOnce.Do(func() {
		m.logger.Debug("initializing event bus")
		
		// Get settings for debug flag
		settings := conf.GetSettings()
		debug := false
		if settings != nil {
			debug = settings.Debug
		}
		
		// Initialize event bus for async error processing
		eventBusConfig := &events.Config{
			BufferSize: 10000,
			Workers:    4,
			Enabled:    true,
			Debug:      debug,
			Deduplication: &events.DeduplicationConfig{
				Enabled:         true,
				Debug:           debug,
				TTL:             5 * time.Minute,
				MaxEntries:      1000,
				CleanupInterval: 1 * time.Minute,
			},
		}
		
		eventBus, err := events.Initialize(eventBusConfig)
		if err != nil {
			// Handle disabled event bus as non-error
			if errors.Is(err, events.ErrEventBusDisabled) {
				m.logger.Debug("Event bus disabled, skipping initialization")
				return
			}
			m.eventBusErr = fmt.Errorf("event bus initialization failed: %w", err)
			return
		}
		
		// Verify event bus is available
		if eventBus == nil {
			m.eventBusErr = fmt.Errorf("event bus is nil after initialization")
			return
		}
		
		adapter := events.NewEventPublisherAdapter(eventBus)
		errors.SetEventPublisher(adapter)
		
		m.logger.Info("event bus initialized successfully",
			"buffer_size", eventBusConfig.BufferSize,
			"workers", eventBusConfig.Workers)
	})
	
	return m.eventBusErr
}

// initializeNotificationWorker initializes the notification worker
func (m *SystemInitManager) initializeNotificationWorker() error {
	m.notificationWorkerOnce.Do(func() {
		m.logger.Debug("initializing notification worker")
		
		// Check prerequisites
		if !notification.IsInitialized() {
			m.workerErr = fmt.Errorf("notification service not initialized")
			return
		}
		
		if !events.IsInitialized() {
			m.workerErr = fmt.Errorf("event bus not initialized")
			return
		}
		
		// Initialize notification worker
		if err := notification.InitializeEventBusIntegration(); err != nil {
			m.workerErr = fmt.Errorf("notification worker initialization failed: %w", err)
			return
		}
		
		m.logger.Info("notification worker initialized successfully")
	})
	
	return m.workerErr
}

// initializeTelemetryEventBus initializes telemetry event bus integration
func (m *SystemInitManager) initializeTelemetryEventBus() error {
	if m.telemetryCoordinator == nil {
		return InitializeEventBus()
	}
	return m.telemetryCoordinator.InitializeEventBusIntegration()
}

// HealthCheck returns comprehensive health status
func (m *SystemInitManager) HealthCheck() SystemHealthStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := SystemHealthStatus{
		Timestamp: time.Now(),
		Subsystems: make(map[string]SubsystemHealth),
	}

	// Check telemetry health
	if m.telemetryCoordinator != nil {
		telemetryHealth := m.telemetryCoordinator.HealthCheck()
		status.Subsystems["telemetry"] = SubsystemHealth{
			Healthy: telemetryHealth.Healthy,
			Components: telemetryHealth.Components,
		}
	}

	// Check notification health
	notificationHealthy := notification.IsInitialized()
	status.Subsystems["notification"] = SubsystemHealth{
		Healthy: notificationHealthy,
		Components: map[string]ComponentHealth{
			"service": {
				State:   getInitStateFromBool(notificationHealthy),
				Healthy: notificationHealthy,
			},
		},
	}
	
	// Add notification worker health if available
	if worker := notification.GetNotificationWorker(); worker != nil {
		stats := worker.GetStats()
		status.Subsystems["notification"].Components["worker"] = ComponentHealth{
			State:   InitStateCompleted,
			Healthy: true,
			Error:   fmt.Sprintf("processed=%d, failed=%d", stats.EventsProcessed, stats.EventsFailed),
		}
	}

	// Check event bus health
	eventBusHealthy := events.IsInitialized()
	status.Subsystems["event_bus"] = SubsystemHealth{
		Healthy: eventBusHealthy,
		Components: map[string]ComponentHealth{
			"bus": {
				State:   getInitStateFromBool(eventBusHealthy),
				Healthy: eventBusHealthy,
			},
		},
	}

	// Overall health
	status.Healthy = true
	for _, subsystem := range status.Subsystems {
		if !subsystem.Healthy {
			status.Healthy = false
			break
		}
	}

	return status
}

// Shutdown performs graceful shutdown of all systems
func (m *SystemInitManager) Shutdown(ctx context.Context) error {
	m.logger.Info("starting system shutdown")

	var shutdownErrors []error

	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Shutdown notification service
	if notification.IsInitialized() {
		if service := notification.GetService(); service != nil {
			m.logger.Info("stopping notification service")
			service.Stop()
		}
	}

	// Check context again after notification shutdown
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Shutdown event bus
	if events.IsInitialized() {
		if eventBus := events.GetEventBus(); eventBus != nil {
			m.logger.Info("stopping event bus")
			
			// Use remaining time from context
			deadline, ok := ctx.Deadline()
			timeout := 5 * time.Second
			if ok {
				timeout = time.Until(deadline)
				if timeout <= 0 {
					return ctx.Err()
				}
				// Cap timeout at 5 seconds
				if timeout > 5*time.Second {
					timeout = 5 * time.Second
				}
			}
			
			if err := eventBus.Shutdown(timeout); err != nil {
				shutdownErrors = append(shutdownErrors, fmt.Errorf("event bus shutdown error: %w", err))
			}
		}
	}

	// Check context again
	select {
	case <-ctx.Done():
		if len(shutdownErrors) > 0 {
			return fmt.Errorf("shutdown cancelled: %w (previous errors: %v)", ctx.Err(), shutdownErrors)
		}
		return ctx.Err()
	default:
	}

	// Shutdown telemetry
	if m.telemetryCoordinator != nil {
		m.logger.Info("stopping telemetry")
		
		// Use remaining time from context
		deadline, ok := ctx.Deadline()
		timeout := 2 * time.Second
		if ok {
			timeout = time.Until(deadline)
			if timeout <= 0 {
				return ctx.Err()
			}
			// Cap timeout at 2 seconds
			if timeout > 2*time.Second {
				timeout = 2 * time.Second
			}
		}
		
		if err := m.telemetryCoordinator.Shutdown(timeout); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("telemetry shutdown error: %w", err))
		}
	}

	if len(shutdownErrors) > 0 {
		return fmt.Errorf("shutdown errors: %v", shutdownErrors)
	}

	m.logger.Info("system shutdown completed")
	return nil
}

// SystemHealthStatus represents health of all subsystems
type SystemHealthStatus struct {
	Healthy    bool
	Subsystems map[string]SubsystemHealth
	Timestamp  time.Time
}

// SubsystemHealth represents health of a subsystem
type SubsystemHealth struct {
	Healthy    bool
	Components map[string]ComponentHealth
}

// getInitStateFromBool converts a boolean to InitState
func getInitStateFromBool(initialized bool) InitState {
	if initialized {
		return InitStateCompleted
	}
	return InitStateNotStarted
}

// InitializeSystem is the main entry point for system initialization
func InitializeSystem(settings *conf.Settings) error {
	manager := GetSystemInitManager()
	return manager.InitializeCore(settings)
}

// InitializeAsyncSystems initializes async components (call after core services are ready)
func InitializeAsyncSystems() error {
	manager := GetSystemInitManager()
	return manager.InitializeAsyncServices()
}

// ShutdownSystem performs graceful system shutdown
func ShutdownSystem(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	
	manager := GetSystemInitManager()
	return manager.Shutdown(ctx)
}

// GetSystemHealth returns current system health
func GetSystemHealth() SystemHealthStatus {
	manager := GetSystemInitManager()
	return manager.HealthCheck()
}