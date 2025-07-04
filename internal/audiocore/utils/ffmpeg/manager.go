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
	logger.Info("creating new FFmpeg process manager",
		"max_processes", config.MaxProcesses,
		"health_check_period", config.HealthCheckPeriod,
		"cleanup_timeout", config.CleanupTimeout,
		"restart_enabled", config.RestartPolicy.Enabled,
		"max_retries", config.RestartPolicy.MaxRetries)
	
	return &manager{
		config:        config,
		processes:     make(map[string]*managedProcess),
		healthChecker: NewHealthChecker(),
	}
}

// CreateProcess creates a new managed FFmpeg process
func (m *manager) CreateProcess(config *ProcessConfig) (Process, error) {
	logger.Info("creating new FFmpeg process",
		"process_id", config.ID,
		"input_type", func() string {
			if isRTSPURL(config.InputURL) {
				return "rtsp_stream"
			}
			return "local_file"
		}(),
		"output_format", config.OutputFormat,
		"current_process_count", len(m.processes))
	
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if process already exists
	if _, exists := m.processes[config.ID]; exists {
		logger.Error("attempted to create process that already exists",
			"process_id", config.ID)
		
		return nil, errors.New(fmt.Errorf("process already exists")).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("process_id", config.ID).
			Build()
	}

	// Check max processes limit
	if m.config.MaxProcesses > 0 && len(m.processes) >= m.config.MaxProcesses {
		logger.Error("max processes limit reached",
			"process_id", config.ID,
			"current_count", len(m.processes),
			"limit", m.config.MaxProcesses)
		
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

	logger.Info("FFmpeg process created successfully",
		"process_id", config.ID,
		"total_processes", len(m.processes))

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
	logger.Info("removing FFmpeg process", "process_id", id)
	
	m.mu.Lock()
	defer m.mu.Unlock()

	mp, exists := m.processes[id]
	if !exists {
		logger.Error("attempted to remove non-existent process", "process_id", id)
		
		return errors.New(fmt.Errorf("process not found")).
			Component("audiocore").
			Category(errors.CategoryGeneric).
			Context("process_id", id).
			Build()
	}

	// Stop the process
	if err := mp.process.Stop(); err != nil {
		logger.Error("error stopping process during removal",
			"process_id", id,
			"error", err)
		// Continue with removal even if stop failed
	}

	delete(m.processes, id)
	
	logger.Info("FFmpeg process removed successfully",
		"process_id", id,
		"remaining_processes", len(m.processes))
	
	return nil
}

// Start starts the manager
func (m *manager) Start(ctx context.Context) error {
	logger.Info("starting FFmpeg process manager",
		"existing_processes", len(m.processes),
		"health_check_enabled", m.config.HealthCheckPeriod > 0,
		"health_check_period", m.config.HealthCheckPeriod)
	
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ctx != nil {
		logger.Error("attempted to start already running manager")
		
		return errors.New(fmt.Errorf("manager already started")).
			Component("audiocore").
			Category(errors.CategorySystem).
			Build()
	}

	m.ctx, m.cancel = context.WithCancel(ctx)

	// Start monitoring existing processes
	monitoringCount := 0
	for _, mp := range m.processes {
		m.wg.Add(1)
		go m.monitorProcess(mp)
		monitoringCount++
	}

	// Start health check routine
	if m.config.HealthCheckPeriod > 0 {
		m.wg.Add(1)
		go m.healthCheckRoutine()
	}
	
	// Start metrics logging routine (every 30 seconds) if enabled
	if m.config.MetricsEnabled {
		m.wg.Add(1)
		go m.metricsLoggingRoutine()
	}

	logger.Info("FFmpeg process manager started successfully",
		"monitoring_processes", monitoringCount,
		"health_check_active", m.config.HealthCheckPeriod > 0)

	return nil
}

// Stop stops all processes and the manager
func (m *manager) Stop() error {
	stopTime := time.Now()
	processCount := len(m.processes)
	
	logger.Info("stopping FFmpeg process manager",
		"active_processes", processCount)
	
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
	}

	// Stop all processes
	var lastErr error
	stoppedCount := 0
	failedCount := 0
	
	for id, mp := range m.processes {
		if err := mp.process.Stop(); err != nil {
			lastErr = err
			failedCount++
			logger.Error("failed to stop process during manager shutdown",
				"process_id", id,
				"error", err)
		} else {
			stoppedCount++
			logger.Debug("process stopped during manager shutdown", "process_id", id)
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
		logger.Info("FFmpeg process manager stopped successfully",
			"stopped_processes", stoppedCount,
			"failed_processes", failedCount,
			"shutdown_duration_ms", time.Since(stopTime).Milliseconds())
		
	case <-time.After(m.config.CleanupTimeout):
		// Timeout waiting for cleanup
		logger.Error("timeout waiting for cleanup during manager shutdown",
			"timeout", m.config.CleanupTimeout,
			"shutdown_duration_ms", time.Since(stopTime).Milliseconds())
		
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
		logger.Error("manager shutdown completed with errors",
			"stopped_processes", stoppedCount,
			"failed_processes", failedCount,
			"last_error", lastErr)
		
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
	
	checkStart := time.Now()
	totalProcesses := len(m.processes)
	unhealthy := 0
	notRunning := 0
	failedHealthCheck := 0
	
	logger.Debug("starting health check",
		"total_processes", totalProcesses)
	
	for processID, mp := range m.processes {
		if !mp.process.IsRunning() {
			unhealthy++
			notRunning++
			logger.Debug("process not running during health check",
				"process_id", processID)
			continue
		}

		if err := m.healthChecker.Check(mp.process); err != nil {
			unhealthy++
			failedHealthCheck++
			logger.Debug("process failed health check",
				"process_id", processID,
				"error", err)
		} else {
			logger.Debug("process passed health check",
				"process_id", processID)
		}
	}
	
	checkDuration := time.Since(checkStart)
	
	if unhealthy > 0 {
		logger.Warn("health check completed with unhealthy processes",
			"total_processes", totalProcesses,
			"unhealthy_count", unhealthy,
			"not_running", notRunning,
			"failed_health_check", failedHealthCheck,
			"check_duration_ms", checkDuration.Milliseconds())
		
		return errors.New(fmt.Errorf("%d unhealthy processes", unhealthy)).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("total", fmt.Sprintf("%d", len(m.processes))).
			Context("unhealthy", fmt.Sprintf("%d", unhealthy)).
			Build()
	}
	
	logger.Debug("health check completed successfully",
		"total_processes", totalProcesses,
		"check_duration_ms", checkDuration.Milliseconds())

	return nil
}

// monitorProcess monitors a process and handles restarts
func (m *manager) monitorProcess(mp *managedProcess) {
	defer m.wg.Done()
	
	logger.Debug("starting process monitoring",
		"process_id", mp.config.ID,
		"restart_enabled", mp.restartPolicy.Enabled,
		"max_retries", mp.restartPolicy.MaxRetries)

	for {
		select {
		case <-m.ctx.Done():
			logger.Debug("process monitoring stopped due to manager shutdown",
				"process_id", mp.config.ID)
			return
		default:
			// Start the process if not running
			if !mp.process.IsRunning() {
				logger.Debug("detected stopped process, attempting restart",
					"process_id", mp.config.ID)
				
				if err := m.handleProcessRestart(mp); err != nil {
					logger.Error("process restart failed",
						"process_id", mp.config.ID,
						"error", err)
					
					// Check if we've exhausted retries
					mp.mu.Lock()
					exhausted := mp.restartPolicy.MaxRetries > 0 && mp.restartCount >= mp.restartPolicy.MaxRetries
					mp.mu.Unlock()
					
					if exhausted {
						logger.Error("process has exhausted all restart attempts, stopping monitoring",
							"process_id", mp.config.ID,
							"final_restart_count", mp.restartCount)
						return
					}
				}
			}

			// Monitor process errors
			select {
			case err := <-mp.process.ErrorOutput():
				if err != nil {
					logger.Warn("FFmpeg process error received",
						"process_id", mp.config.ID,
						"error", err)
				}
			case <-time.After(5 * time.Second):
				// Periodic check - log at trace level to avoid spam
				logger.Debug("periodic process health check",
					"process_id", mp.config.ID,
					"is_running", mp.process.IsRunning())
			case <-m.ctx.Done():
				logger.Debug("process monitoring stopped during error monitoring",
					"process_id", mp.config.ID)
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
		logger.Debug("restart disabled for process", "process_id", mp.config.ID)
		
		return errors.New(fmt.Errorf("restart disabled")).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("process_id", mp.config.ID).
			Build()
	}

	// Check if we've exceeded max retries
	if mp.restartPolicy.MaxRetries > 0 && mp.restartCount >= mp.restartPolicy.MaxRetries {
		logger.Error("process has exceeded maximum restart attempts",
			"process_id", mp.config.ID,
			"restart_count", mp.restartCount,
			"max_retries", mp.restartPolicy.MaxRetries)
		
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
		logger.Info("applying restart backoff delay",
			"process_id", mp.config.ID,
			"attempt", mp.restartCount+1,
			"delay_ms", delay.Milliseconds(),
			"backoff_multiplier", mp.restartPolicy.BackoffMultiplier)
		
		select {
		case <-time.After(delay):
			logger.Debug("backoff delay completed, proceeding with restart",
				"process_id", mp.config.ID)
		case <-m.ctx.Done():
			logger.Info("manager shutdown during restart backoff",
				"process_id", mp.config.ID,
				"delay_remaining_ms", delay.Milliseconds())
			
			return errors.New(fmt.Errorf("manager stopped")).
				Component("audiocore").
				Category(errors.CategorySystem).
				Build()
		}

		// Calculate next delay with exponential backoff
		newDelay := time.Duration(float64(mp.nextDelay) * mp.restartPolicy.BackoffMultiplier)
		if newDelay > mp.restartPolicy.MaxDelay {
			newDelay = mp.restartPolicy.MaxDelay
		}
		
		logger.Debug("calculated next backoff delay",
			"process_id", mp.config.ID,
			"current_delay_ms", mp.nextDelay.Milliseconds(),
			"next_delay_ms", newDelay.Milliseconds(),
			"max_delay_ms", mp.restartPolicy.MaxDelay.Milliseconds())
		
		mp.nextDelay = newDelay
	}

	// Attempt restart
	restartTime := time.Now()
	logger.Info("attempting to restart FFmpeg process",
		"process_id", mp.config.ID,
		"attempt", mp.restartCount+1)
	
	if err := mp.process.Start(m.ctx); err != nil {
		mp.restartCount++
		mp.lastRestart = time.Now()
		
		logger.Error("FFmpeg process restart failed",
			"process_id", mp.config.ID,
			"attempt", mp.restartCount,
			"error", err,
			"restart_duration_ms", time.Since(restartTime).Milliseconds())
		
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

	logger.Info("FFmpeg process restarted successfully",
		"process_id", mp.config.ID,
		"restart_duration_ms", time.Since(restartTime).Milliseconds(),
		"backoff_reset", true)
	
	return nil
}

// healthCheckRoutine performs periodic health checks
func (m *manager) healthCheckRoutine() {
	defer m.wg.Done()
	
	logger.Info("starting health check routine",
		"check_period", m.config.HealthCheckPeriod)

	ticker := time.NewTicker(m.config.HealthCheckPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			logger.Debug("performing periodic health check")
			
			if err := m.HealthCheck(); err != nil {
				logger.Error("periodic health check failed",
					"error", err,
					"total_processes", len(m.processes))
			} else {
				logger.Debug("periodic health check completed successfully",
					"total_processes", len(m.processes))
			}
		case <-m.ctx.Done():
			logger.Info("health check routine stopped due to manager shutdown")
			return
		}
	}
}

// metricsLoggingRoutine logs process metrics periodically for operational visibility
func (m *manager) metricsLoggingRoutine() {
	defer m.wg.Done()
	
	logger.Debug("starting metrics logging routine",
		"log_interval", "30s")

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.logProcessMetrics()
		case <-m.ctx.Done():
			logger.Debug("metrics logging routine stopped due to manager shutdown")
			return
		}
	}
}

// logProcessMetrics logs detailed metrics for all managed processes
func (m *manager) logProcessMetrics() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	metricsStart := time.Now()
	totalProcesses := len(m.processes)
	runningProcesses := 0
	totalBytesRead := int64(0)
	totalFramesRead := int64(0)
	totalRestarts := 0
	
	for processID, mp := range m.processes {
		isRunning := mp.process.IsRunning()
		if isRunning {
			runningProcesses++
		}
		
		metrics := mp.process.Metrics()
		totalBytesRead += metrics.BytesRead
		totalFramesRead += metrics.FramesRead
		totalRestarts += metrics.RestartCount
		
		// Log individual process metrics
		logger.Debug("process metrics",
			"process_id", processID,
			"is_running", isRunning,
			"bytes_read", metrics.BytesRead,
			"frames_read", metrics.FramesRead,
			"restart_count", metrics.RestartCount,
			"uptime_ms", func() int64 {
				if isRunning && !metrics.StartTime.IsZero() {
					return time.Since(metrics.StartTime).Milliseconds()
				}
				return 0
			}(),
			"last_restart", func() string {
				if !metrics.LastRestart.IsZero() {
					return metrics.LastRestart.Format(time.RFC3339)
				}
				return "never"
			}())
	}
	
	// Log aggregate metrics
	logger.Info("FFmpeg manager metrics summary",
		"total_processes", totalProcesses,
		"running_processes", runningProcesses,
		"stopped_processes", totalProcesses-runningProcesses,
		"total_bytes_read", totalBytesRead,
		"total_frames_read", totalFramesRead,
		"total_restarts", totalRestarts,
		"metrics_collection_ms", time.Since(metricsStart).Milliseconds())
}