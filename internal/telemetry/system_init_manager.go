package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	apperrors "github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// Default configuration values for notification and event bus
const (
	// DefaultMaxNotifications is the default maximum number of notifications to keep in memory
	DefaultMaxNotifications = 1000
	// DefaultCleanupInterval is the default interval for cleaning up expired notifications
	DefaultCleanupInterval = 5 * time.Minute
	// DefaultRateLimitWindow is the default time window for rate limiting
	DefaultRateLimitWindow = 1 * time.Minute
	// DefaultRateLimitMaxEvents is the default maximum number of events per rate limit window
	DefaultRateLimitMaxEvents = 100

	// Event bus configuration
	eventBusBufferSize       = 10000
	eventBusWorkers          = 4
	deduplicationTTL         = 5 * time.Minute
	deduplicationMaxEntries  = 1000
	deduplicationCleanupInt  = 1 * time.Minute

	// Shutdown timeouts
	eventBusShutdownTimeout   = 5 * time.Second
	telemetryShutdownTimeout  = 2 * time.Second
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
	
	mu       sync.RWMutex
	sysLog   logger.Logger
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
			sysLog:               GetLogger().With(logger.String("component", "system-init")),
		}
	})
	return systemInitManager
}

// InitializeCore initializes core services (telemetry and notification)
func (m *SystemInitManager) InitializeCore(settings *conf.Settings) error {
	m.sysLog.Info("starting core services initialization")

	// Phase 1: Initialize telemetry (synchronous error reporting)
	if err := m.initializeTelemetry(settings); err != nil {
		return fmt.Errorf("telemetry initialization failed: %w", err)
	}

	// Phase 2: Initialize notification service
	if err := m.initializeNotification(); err != nil {
		return fmt.Errorf("notification initialization failed: %w", err)
	}

	m.sysLog.Info("core services initialization completed")
	return nil
}

// InitializeAsyncServices initializes async services (event bus and workers)
func (m *SystemInitManager) InitializeAsyncServices() error {
	m.sysLog.Info("starting async services initialization")

	// Phase 1: Initialize event bus
	if err := m.initializeEventBus(); err != nil {
		return fmt.Errorf("event bus initialization failed: %w", err)
	}

	// Phase 2: Initialize notification worker
	if err := m.initializeNotificationWorker(); err != nil {
		// Log but don't fail - notification worker is not critical
		m.sysLog.Error("notification worker initialization failed", logger.Error(err))
	}

	// Phase 3: Initialize telemetry event bus integration
	if err := m.initializeTelemetryEventBus(); err != nil {
		// Log but don't fail - telemetry is not critical
		m.sysLog.Error("telemetry event bus initialization failed", logger.Error(err))
	}

	m.sysLog.Info("async services initialization completed")
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

// setupNotificationTelemetry configures telemetry integration for notification service
func (m *SystemInitManager) setupNotificationTelemetry(settings *conf.Settings) {
	if settings == nil || !settings.Sentry.Enabled {
		return
	}

	m.sysLog.Debug("setting up notification telemetry integration")
	reporter := NewNotificationReporter(settings.Sentry.Enabled)
	telemetryConfig := notification.DefaultTelemetryConfig()
	telemetryIntegration := notification.NewNotificationTelemetry(&telemetryConfig, reporter)

	if service := notification.GetService(); service != nil {
		service.SetTelemetry(telemetryIntegration)
		m.sysLog.Info("notification telemetry integration enabled")
	} else {
		m.sysLog.Warn("notification service not available for telemetry integration")
	}
}

// initializeNotification initializes the notification service
func (m *SystemInitManager) initializeNotification() error {
	m.notificationInitOnce.Do(func() {
		m.sysLog.Debug("initializing notification service")

		settings := conf.GetSettings()
		debug := settings != nil && settings.Debug

		config := &notification.ServiceConfig{
			Debug:              debug,
			MaxNotifications:   DefaultMaxNotifications,
			CleanupInterval:    DefaultCleanupInterval,
			RateLimitWindow:    DefaultRateLimitWindow,
			RateLimitMaxEvents: DefaultRateLimitMaxEvents,
		}
		notification.Initialize(config)

		m.setupNotificationTelemetry(settings)

		// Initialize push notifications from config (non-fatal on error)
		if settings != nil {
			if err := notification.InitializePushFromConfig(settings); err != nil {
				m.sysLog.Error("push notification init failed", logger.Error(err))
			}
		}

		if !notification.IsInitialized() {
			m.notificationErr = fmt.Errorf("notification service initialization failed")
			return
		}

		m.sysLog.Info("notification service initialized successfully", logger.Bool("debug", debug))
	})

	return m.notificationErr
}

// initializeEventBus initializes the event bus
func (m *SystemInitManager) initializeEventBus() error {
	m.eventBusInitOnce.Do(func() {
		m.sysLog.Debug("initializing event bus")
		
		// Get settings for debug flag
		settings := conf.GetSettings()
		debug := false
		if settings != nil {
			debug = settings.Debug
		}
		
		// Initialize event bus for async error processing
		eventBusConfig := &events.Config{
			BufferSize: eventBusBufferSize,
			Workers:    eventBusWorkers,
			Enabled:    true,
			Debug:      debug,
			Deduplication: &events.DeduplicationConfig{
				Enabled:         true,
				Debug:           debug,
				TTL:             deduplicationTTL,
				MaxEntries:      deduplicationMaxEntries,
				CleanupInterval: deduplicationCleanupInt,
			},
		}
		
		eventBus, err := events.Initialize(eventBusConfig)
		if err != nil {
			// Handle disabled event bus as non-error
			if errors.Is(err, events.ErrEventBusDisabled) {
				m.sysLog.Debug("Event bus disabled, skipping initialization")
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
		apperrors.SetEventPublisher(adapter)
		
		m.sysLog.Info("event bus initialized successfully",
			logger.Int("buffer_size", eventBusConfig.BufferSize),
			logger.Int("workers", eventBusConfig.Workers))
	})
	
	return m.eventBusErr
}

// initializeNotificationWorker initializes the notification worker
func (m *SystemInitManager) initializeNotificationWorker() error {
	m.notificationWorkerOnce.Do(func() {
		m.sysLog.Debug("initializing notification worker")
		
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
		
		m.sysLog.Info("notification worker initialized successfully")
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

// cappedTimeout returns the remaining time from context, capped at maxTimeout.
// Returns 0 if context deadline has passed, or maxTimeout if no deadline is set.
func cappedTimeout(ctx context.Context, maxTimeout time.Duration) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return maxTimeout
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	if remaining > maxTimeout {
		return maxTimeout
	}
	return remaining
}

// shutdownNotification stops the notification service
func (m *SystemInitManager) shutdownNotification() {
	if !notification.IsInitialized() {
		return
	}
	if service := notification.GetService(); service != nil {
		m.sysLog.Info("stopping notification service")
		service.Stop()
	}
}

// shutdownEventBus stops the event bus and returns any error
func (m *SystemInitManager) shutdownEventBus(ctx context.Context) error {
	if !events.IsInitialized() {
		return nil
	}
	eventBus := events.GetEventBus()
	if eventBus == nil {
		return nil
	}

	m.sysLog.Info("stopping event bus")
	timeout := cappedTimeout(ctx, eventBusShutdownTimeout)
	if timeout == 0 {
		return ctx.Err()
	}
	return eventBus.Shutdown(timeout)
}

// shutdownTelemetry stops the telemetry coordinator and returns any error
func (m *SystemInitManager) shutdownTelemetry(ctx context.Context) error {
	if m.telemetryCoordinator == nil {
		return nil
	}

	m.sysLog.Info("stopping telemetry")
	timeout := cappedTimeout(ctx, telemetryShutdownTimeout)
	if timeout == 0 {
		return ctx.Err()
	}
	return m.telemetryCoordinator.Shutdown(timeout)
}

// Shutdown performs graceful shutdown of all systems
func (m *SystemInitManager) Shutdown(ctx context.Context) error {
	m.sysLog.Info("starting system shutdown")

	// Check if context is already cancelled
	if ctx.Err() != nil {
		return ctx.Err()
	}

	var shutdownErrs []error

	// Shutdown notification service (no error returned)
	m.shutdownNotification()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Shutdown event bus
	if err := m.shutdownEventBus(ctx); err != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("event bus: %w", err))
	}

	if ctx.Err() != nil {
		if len(shutdownErrs) > 0 {
			return errors.Join(append(shutdownErrs, ctx.Err())...)
		}
		return ctx.Err()
	}

	// Shutdown telemetry
	if err := m.shutdownTelemetry(ctx); err != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("telemetry: %w", err))
	}

	if len(shutdownErrs) > 0 {
		return errors.Join(shutdownErrs...)
	}

	m.sysLog.Info("system shutdown completed")
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