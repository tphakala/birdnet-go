package telemetry

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// InitState represents the initialization state of a component
type InitState int32

const (
	InitStateNotStarted InitState = iota
	InitStateInProgress
	InitStateCompleted
	InitStateFailed
)

// InitManager coordinates safe initialization of telemetry components
type InitManager struct {
	// Component states
	errorIntegration atomic.Int32
	sentryClient     atomic.Int32
	eventBus         atomic.Int32
	telemetryWorker  atomic.Int32

	// Synchronization
	errorIntegrationOnce sync.Once
	sentryOnce           sync.Once
	eventBusOnce         sync.Once
	workerOnce           sync.Once

	// Error tracking
	errorIntegrationErr atomic.Value // stores error
	sentryErr           atomic.Value // stores error
	eventBusErr         atomic.Value // stores error
	workerErr           atomic.Value // stores error

	// Dependencies
	mu     sync.RWMutex
	logger *slog.Logger
}

var (
	initManager     *InitManager
	initManagerOnce sync.Once
)

// GetInitManager returns the singleton init manager
func GetInitManager() *InitManager {
	initManagerOnce.Do(func() {
		initManager = &InitManager{
			logger: getLoggerSafe("init-manager"),
		}
	})
	return initManager
}

// InitializeErrorIntegrationSafe safely initializes error integration
func (m *InitManager) InitializeErrorIntegrationSafe() error {
	var err error
	m.errorIntegrationOnce.Do(func() {
		m.errorIntegration.Store(int32(InitStateInProgress))
		m.logger.Debug("initializing error integration")

		// Call the actual initialization
		InitializeErrorIntegration()

		m.errorIntegration.Store(int32(InitStateCompleted))
		m.logger.Info("error integration initialized successfully")
	})

	// Check for stored error
	if v := m.errorIntegrationErr.Load(); v != nil {
		err = v.(error)
	}

	return err
}

// InitializeSentrySafe safely initializes Sentry with retry logic
func (m *InitManager) InitializeSentrySafe(settings interface{}) error {
	var err error
	m.sentryOnce.Do(func() {
		m.sentryClient.Store(int32(InitStateInProgress))
		m.logger.Debug("initializing Sentry client")

		// Ensure error integration is ready
		if err := m.InitializeErrorIntegrationSafe(); err != nil {
			m.sentryErr.Store(err)
			m.sentryClient.Store(int32(InitStateFailed))
			return
		}

		// Call the actual initialization with a timeout
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		done := make(chan error, 1)
		go func() {
			// Type assertion for settings
			if s, ok := settings.(*conf.Settings); ok {
				done <- InitSentry(s)
			} else {
				done <- fmt.Errorf("invalid settings type: expected *conf.Settings")
			}
		}()

		select {
		case err = <-done:
			if err != nil {
				m.sentryErr.Store(err)
				m.sentryClient.Store(int32(InitStateFailed))
				m.logger.Error("Sentry initialization failed", "error", err)
			} else {
				m.sentryClient.Store(int32(InitStateCompleted))
				m.logger.Info("Sentry initialized successfully")
			}
		case <-ctx.Done():
			err = fmt.Errorf("sentry initialization timeout")
			m.sentryErr.Store(err)
			m.sentryClient.Store(int32(InitStateFailed))
			m.logger.Error("Sentry initialization timeout")
		}
	})

	// Check for stored error
	if v := m.sentryErr.Load(); v != nil {
		err = v.(error)
	}

	return err
}

// InitializeEventBusSafe safely initializes event bus integration
func (m *InitManager) InitializeEventBusSafe() error {
	var err error
	m.eventBusOnce.Do(func() {
		m.eventBus.Store(int32(InitStateInProgress))
		m.logger.Debug("initializing event bus integration")

		// Ensure dependencies are ready
		if state := InitState(m.errorIntegration.Load()); state != InitStateCompleted {
			err = fmt.Errorf("error integration not ready: state=%d", state)
			m.eventBusErr.Store(err)
			m.eventBus.Store(int32(InitStateFailed))
			return
		}

		// Call the actual initialization
		if err = InitializeTelemetryEventBus(); err != nil {
			m.eventBusErr.Store(err)
			m.eventBus.Store(int32(InitStateFailed))
			m.logger.Error("event bus integration failed", "error", err)
			return
		}

		m.eventBus.Store(int32(InitStateCompleted))
		m.logger.Info("event bus integration initialized successfully")
	})

	// Check for stored error
	if v := m.eventBusErr.Load(); v != nil {
		err = v.(error)
	}

	return err
}

// GetComponentState returns the current state of a component
func (m *InitManager) GetComponentState(component string) InitState {
	switch component {
	case "error_integration":
		return InitState(m.errorIntegration.Load())
	case "sentry":
		return InitState(m.sentryClient.Load())
	case "event_bus":
		return InitState(m.eventBus.Load())
	case "worker":
		return InitState(m.telemetryWorker.Load())
	default:
		return InitStateNotStarted
	}
}

// WaitForComponent waits for a component to reach the desired state
func (m *InitManager) WaitForComponent(component string, desiredState InitState, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		if m.GetComponentState(component) == desiredState {
			return nil
		}
		<-ticker.C
	}

	currentState := m.GetComponentState(component)
	return fmt.Errorf("timeout waiting for %s to reach state %d, current state: %d", 
		component, desiredState, currentState)
}

// HealthCheck performs a health check on all telemetry components
func (m *InitManager) HealthCheck() HealthStatus {
	status := HealthStatus{
		Components: make(map[string]ComponentHealth),
		Timestamp:  time.Now(),
	}

	// Check each component
	components := []string{"error_integration", "sentry", "event_bus", "worker"}
	for _, comp := range components {
		state := m.GetComponentState(comp)
		health := ComponentHealth{
			State:   state,
			Healthy: state == InitStateCompleted,
		}

		// Add error if available
		switch comp {
		case "error_integration":
			if v := m.errorIntegrationErr.Load(); v != nil {
				health.Error = v.(error).Error()
			}
		case "sentry":
			if v := m.sentryErr.Load(); v != nil {
				health.Error = v.(error).Error()
			}
		case "event_bus":
			if v := m.eventBusErr.Load(); v != nil {
				health.Error = v.(error).Error()
			}
		case "worker":
			if v := m.workerErr.Load(); v != nil {
				health.Error = v.(error).Error()
			}
		}

		status.Components[comp] = health
	}

	// Overall health
	status.Healthy = true
	for _, health := range status.Components {
		if !health.Healthy && health.State != InitStateNotStarted {
			status.Healthy = false
			break
		}
	}

	return status
}

// Shutdown performs graceful shutdown of telemetry components
func (m *InitManager) Shutdown(ctx context.Context) error {
	m.logger.Info("starting telemetry shutdown")

	// Mark components as shutting down
	m.telemetryWorker.Store(int32(InitStateNotStarted))
	m.eventBus.Store(int32(InitStateNotStarted))

	// Flush Sentry with timeout
	done := make(chan struct{})
	go func() {
		Flush(2 * time.Second)
		close(done)
	}()

	select {
	case <-done:
		m.logger.Info("telemetry shutdown completed")
		return nil
	case <-ctx.Done():
		m.logger.Warn("telemetry shutdown timeout")
		return ctx.Err()
	}
}

// HealthStatus represents the health of telemetry components
type HealthStatus struct {
	Healthy    bool
	Components map[string]ComponentHealth
	Timestamp  time.Time
}

// ComponentHealth represents health of a single component
type ComponentHealth struct {
	State   InitState
	Healthy bool
	Error   string
}

// String returns string representation of InitState
func (s InitState) String() string {
	switch s {
	case InitStateNotStarted:
		return "not_started"
	case InitStateInProgress:
		return "in_progress"
	case InitStateCompleted:
		return "completed"
	case InitStateFailed:
		return "failed"
	default:
		return "unknown"
	}
}