package ffmpeg

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// manager implements the Manager interface
type manager struct {
	config        ManagerConfig
	processes     map[string]*managedProcess
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	healthChecker HealthChecker
	wg            sync.WaitGroup
}

// managedProcess wraps a process with restart logic
type managedProcess struct {
	process       Process
	config        *ProcessConfig
	restartPolicy RestartPolicy
	restartCount  int
	lastRestart   time.Time
	nextDelay     time.Duration
	mu            sync.Mutex
}

// NewManager creates a new FFmpeg process manager
func NewManager(config ManagerConfig) Manager {
	return &manager{
		config:        config,
		processes:     make(map[string]*managedProcess),
		healthChecker: NewHealthChecker(),
	}
}

// CreateProcess creates a new managed FFmpeg process
func (m *manager) CreateProcess(config *ProcessConfig) (Process, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if process already exists
	if _, exists := m.processes[config.ID]; exists {
		return nil, errors.New(fmt.Errorf("process already exists")).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("process_id", config.ID).
			Build()
	}

	// Check max processes limit
	if m.config.MaxProcesses > 0 && len(m.processes) >= m.config.MaxProcesses {
		return nil, errors.New(fmt.Errorf("max processes limit reached")).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("limit", fmt.Sprintf("%d", m.config.MaxProcesses)).
			Build()
	}

	// Create the process
	process := NewProcess(config)
	
	// Wrap with managed process
	mp := &managedProcess{
		process:       process,
		config:        config,
		restartPolicy: m.config.RestartPolicy,
		nextDelay:     m.config.RestartPolicy.InitialDelay,
	}

	m.processes[config.ID] = mp

	// Start monitoring if manager is running
	if m.ctx != nil {
		m.wg.Add(1)
		go m.monitorProcess(mp)
	}

	return process, nil
}

// GetProcess returns a process by ID
func (m *manager) GetProcess(id string) (Process, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if mp, exists := m.processes[id]; exists {
		return mp.process, true
	}
	return nil, false
}

// ListProcesses returns all managed processes
func (m *manager) ListProcesses() []Process {
	m.mu.RLock()
	defer m.mu.RUnlock()

	processes := make([]Process, 0, len(m.processes))
	for _, mp := range m.processes {
		processes = append(processes, mp.process)
	}
	return processes
}

// RemoveProcess stops and removes a process
func (m *manager) RemoveProcess(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mp, exists := m.processes[id]
	if !exists {
		return errors.New(fmt.Errorf("process not found")).
			Component("audiocore").
			Category(errors.CategoryGeneric).
			Context("process_id", id).
			Build()
	}

	// Stop the process
	if err := mp.process.Stop(); err != nil {
		// Log error but continue with removal
		fmt.Printf("Error stopping process %s: %v\n", id, err)
	}

	delete(m.processes, id)
	return nil
}

// Start starts the manager
func (m *manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ctx != nil {
		return errors.New(fmt.Errorf("manager already started")).
			Component("audiocore").
			Category(errors.CategorySystem).
			Build()
	}

	m.ctx, m.cancel = context.WithCancel(ctx)

	// Start monitoring existing processes
	for _, mp := range m.processes {
		m.wg.Add(1)
		go m.monitorProcess(mp)
	}

	// Start health check routine
	if m.config.HealthCheckPeriod > 0 {
		m.wg.Add(1)
		go m.healthCheckRoutine()
	}

	return nil
}

// Stop stops all processes and the manager
func (m *manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
	}

	// Stop all processes
	var lastErr error
	for id, mp := range m.processes {
		if err := mp.process.Stop(); err != nil {
			lastErr = err
			fmt.Printf("Error stopping process %s: %v\n", id, err)
		}
	}

	// Wait for all goroutines to finish
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines finished
	case <-time.After(m.config.CleanupTimeout):
		// Timeout waiting for cleanup
		return errors.New(fmt.Errorf("timeout waiting for cleanup")).
			Component("audiocore").
			Category(errors.CategorySystem).
			Build()
	}

	// Clear state
	m.processes = make(map[string]*managedProcess)
	m.ctx = nil
	m.cancel = nil

	if lastErr != nil {
		return errors.New(lastErr).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "stop-manager").
			Build()
	}

	return nil
}

// HealthCheck performs a health check on all processes
func (m *manager) HealthCheck() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	unhealthy := 0
	for _, mp := range m.processes {
		if !mp.process.IsRunning() {
			unhealthy++
			continue
		}

		if err := m.healthChecker.Check(mp.process); err != nil {
			unhealthy++
		}
	}

	if unhealthy > 0 {
		return errors.New(fmt.Errorf("%d unhealthy processes", unhealthy)).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("total", fmt.Sprintf("%d", len(m.processes))).
			Context("unhealthy", fmt.Sprintf("%d", unhealthy)).
			Build()
	}

	return nil
}

// monitorProcess monitors a process and handles restarts
func (m *manager) monitorProcess(mp *managedProcess) {
	defer m.wg.Done()

	for {
		select {
		case <-m.ctx.Done():
			return
		default:
			// Start the process if not running
			if !mp.process.IsRunning() {
				if err := m.handleProcessRestart(mp); err != nil {
					fmt.Printf("Failed to restart process %s: %v\n", mp.config.ID, err)
					// Check if we've exhausted retries
					mp.mu.Lock()
					exhausted := mp.restartPolicy.MaxRetries > 0 && mp.restartCount >= mp.restartPolicy.MaxRetries
					mp.mu.Unlock()
					
					if exhausted {
						fmt.Printf("Process %s has exhausted all restart attempts\n", mp.config.ID)
						return
					}
				}
			}

			// Monitor process errors
			select {
			case err := <-mp.process.ErrorOutput():
				if err != nil {
					fmt.Printf("Process %s error: %v\n", mp.config.ID, err)
				}
			case <-time.After(5 * time.Second):
				// Periodic check
			case <-m.ctx.Done():
				return
			}
		}
	}
}

// handleProcessRestart handles restarting a process with backoff
func (m *manager) handleProcessRestart(mp *managedProcess) error {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	// Check if restart is enabled
	if !mp.restartPolicy.Enabled {
		return errors.New(fmt.Errorf("restart disabled")).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("process_id", mp.config.ID).
			Build()
	}

	// Check if we've exceeded max retries
	if mp.restartPolicy.MaxRetries > 0 && mp.restartCount >= mp.restartPolicy.MaxRetries {
		return errors.New(fmt.Errorf("max retries exceeded")).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("process_id", mp.config.ID).
			Context("retries", fmt.Sprintf("%d", mp.restartCount)).
			Build()
	}

	// Apply backoff delay
	if mp.restartCount > 0 {
		delay := mp.nextDelay
		fmt.Printf("Waiting %v before restarting process %s (attempt %d)\n", 
			delay, mp.config.ID, mp.restartCount+1)
		
		select {
		case <-time.After(delay):
			// Continue with restart
		case <-m.ctx.Done():
			return errors.New(fmt.Errorf("manager stopped")).
				Component("audiocore").
				Category(errors.CategorySystem).
				Build()
		}

		// Calculate next delay with exponential backoff
		mp.nextDelay = time.Duration(float64(mp.nextDelay) * mp.restartPolicy.BackoffMultiplier)
		if mp.nextDelay > mp.restartPolicy.MaxDelay {
			mp.nextDelay = mp.restartPolicy.MaxDelay
		}
	}

	// Attempt restart
	if err := mp.process.Start(m.ctx); err != nil {
		mp.restartCount++
		mp.lastRestart = time.Now()
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "restart-process").
			Context("process_id", mp.config.ID).
			Context("attempt", fmt.Sprintf("%d", mp.restartCount)).
			Build()
	}

	// Reset on successful restart
	mp.restartCount = 0
	mp.nextDelay = mp.restartPolicy.InitialDelay
	mp.lastRestart = time.Now()

	fmt.Printf("Successfully restarted process %s\n", mp.config.ID)
	return nil
}

// healthCheckRoutine performs periodic health checks
func (m *manager) healthCheckRoutine() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.HealthCheckPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := m.HealthCheck(); err != nil {
				fmt.Printf("Health check failed: %v\n", err)
			}
		case <-m.ctx.Done():
			return
		}
	}
}