package myaudio

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Global variables - make ffmpegProcesses a pointer to sync.Map
var ffmpegProcesses = &sync.Map{}

// ProcessInfo contains information about a system process
type ProcessInfo struct {
	PID  int
	Name string
}

// CleanupSettings contains settings for process cleanup
type CleanupSettings struct {
	Enabled bool
	Timeout time.Duration
}

// ProcessManager defines operations for managing system processes
type ProcessManager interface {
	FindProcesses() ([]ProcessInfo, error)
	TerminateProcess(pid int) error
	IsProcessRunning(pid int) bool
}

// ProcessCleaner defines an interface for objects that can clean up processes
type ProcessCleaner interface {
	Cleanup(url string)
}

// ConfigProvider defines operations for accessing configuration
type ConfigProvider interface {
	GetConfiguredURLs() []string
	GetMonitoringInterval() time.Duration
	GetProcessCleanupSettings() CleanupSettings
}

// Clock abstracts time-related operations
type Clock interface {
	Now() time.Time
	NewTicker(duration time.Duration) Ticker
	Sleep(duration time.Duration)
}

// Ticker abstracts a time ticker
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// ProcessRepository manages the storage and retrieval of FFmpeg processes
type ProcessRepository interface {
	ForEach(callback func(key, value any) bool)
}

// CommandExecutor defines operations for executing system commands
type CommandExecutor interface {
	ExecuteCommand(name string, args ...string) (output []byte, err error)
}

// RealClock implements Clock using the standard time package
type RealClock struct{}

// Now returns the current time
func (c *RealClock) Now() time.Time {
	return time.Now()
}

// NewTicker creates a new ticker
func (c *RealClock) NewTicker(duration time.Duration) Ticker {
	return &RealTicker{ticker: time.NewTicker(duration)}
}

// Sleep pauses execution for the specified duration
func (c *RealClock) Sleep(duration time.Duration) {
	time.Sleep(duration)
}

// RealTicker implements Ticker using the standard time.Ticker
type RealTicker struct {
	ticker *time.Ticker
}

// C returns the ticker channel
func (t *RealTicker) C() <-chan time.Time {
	return t.ticker.C
}

// Stop stops the ticker
func (t *RealTicker) Stop() {
	t.ticker.Stop()
}

// SyncMapProcessRepository is a wrapper around the global ffmpegProcesses map
type SyncMapProcessRepository struct{}

// ForEach iterates over all processes
func (r *SyncMapProcessRepository) ForEach(callback func(key, value any) bool) {
	ffmpegProcesses.Range(callback)
}

// DefaultCommandExecutor implements CommandExecutor using os/exec
type DefaultCommandExecutor struct{}

// ExecuteCommand executes a command and returns its output
func (e *DefaultCommandExecutor) ExecuteCommand(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.Output()
}

// UnixProcessManager implements ProcessManager for Unix systems
type UnixProcessManager struct {
	cmdExecutor CommandExecutor
}

// FindProcesses finds all FFmpeg processes in the system
func (pm *UnixProcessManager) FindProcesses() ([]ProcessInfo, error) {
	output, err := pm.cmdExecutor.ExecuteCommand("pgrep", "ffmpeg")
	if err != nil {
		// If the command returns no processes, that's not an error
		if strings.Contains(err.Error(), "exit status 1") {
			return nil, nil
		}
		return nil, fmt.Errorf("error running pgrep command: %w", err)
	}

	var processes []ProcessInfo
	for _, line := range strings.Split(string(output), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			var pid int
			if _, err := fmt.Sscanf(line, "%d", &pid); err == nil {
				processes = append(processes, ProcessInfo{PID: pid, Name: "ffmpeg"})
			}
		}
	}
	return processes, nil
}

// TerminateProcess terminates a process by its PID
func (pm *UnixProcessManager) TerminateProcess(pid int) error {
	_, err := pm.cmdExecutor.ExecuteCommand("kill", "-9", fmt.Sprint(pid))
	if err != nil {
		return fmt.Errorf("failed to terminate process %d: %w", pid, err)
	}
	return nil
}

// IsProcessRunning checks if a process is running
func (pm *UnixProcessManager) IsProcessRunning(pid int) bool {
	_, err := pm.cmdExecutor.ExecuteCommand("kill", "-0", fmt.Sprint(pid))
	return err == nil
}

// WindowsProcessManager implements ProcessManager for Windows systems
type WindowsProcessManager struct {
	cmdExecutor CommandExecutor
}

// FindProcesses finds all FFmpeg processes in the system
func (pm *WindowsProcessManager) FindProcesses() ([]ProcessInfo, error) {
	output, err := pm.cmdExecutor.ExecuteCommand("tasklist", "/FI", "IMAGENAME eq ffmpeg.exe", "/NH", "/FO", "CSV")
	if err != nil {
		return nil, fmt.Errorf("error running tasklist command: %w", err)
	}

	var processes []ProcessInfo
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "ffmpeg.exe") {
			fields := strings.Split(line, ",")
			if len(fields) >= 2 {
				// Remove quotes and convert to PID
				pidStr := strings.Trim(fields[1], "\" \r\n")
				var pid int
				_, err := fmt.Sscanf(pidStr, "%d", &pid)
				if err == nil {
					processes = append(processes, ProcessInfo{PID: pid, Name: "ffmpeg.exe"})
				}
			}
		}
	}
	return processes, nil
}

// TerminateProcess terminates a process by its PID
func (pm *WindowsProcessManager) TerminateProcess(pid int) error {
	_, err := pm.cmdExecutor.ExecuteCommand("taskkill", "/F", "/T", "/PID", fmt.Sprint(pid))
	if err != nil {
		return fmt.Errorf("failed to terminate process %d: %w", pid, err)
	}
	return nil
}

// IsProcessRunning checks if a process is running
func (pm *WindowsProcessManager) IsProcessRunning(pid int) bool {
	output, err := pm.cmdExecutor.ExecuteCommand("tasklist", "/FI", "PID eq "+fmt.Sprint(pid), "/NH")
	if err != nil {
		return false
	}
	return strings.Contains(string(output), fmt.Sprint(pid))
}

// SettingsBasedConfigProvider implements ConfigProvider using conf.Setting
type SettingsBasedConfigProvider struct{}

// GetConfiguredURLs returns the configured RTSP URLs
func (cp *SettingsBasedConfigProvider) GetConfiguredURLs() []string {
	return conf.Setting().Realtime.RTSP.URLs
}

// GetMonitoringInterval returns the monitoring interval
func (cp *SettingsBasedConfigProvider) GetMonitoringInterval() time.Duration {
	// If there's a specific setting for this in conf, we could use that instead
	return 30 * time.Second
}

// GetProcessCleanupSettings returns the process cleanup settings
func (cp *SettingsBasedConfigProvider) GetProcessCleanupSettings() CleanupSettings {
	return CleanupSettings{
		Enabled: true,
		Timeout: 5 * time.Minute,
	}
}

// Global instances of dependencies
var (
	clock          Clock             = &RealClock{}
	processRepo    ProcessRepository = &SyncMapProcessRepository{}
	cmdExecutor    CommandExecutor   = &DefaultCommandExecutor{}
	configProvider ConfigProvider    = &SettingsBasedConfigProvider{}
	processManager ProcessManager
)

// init initializes the appropriate ProcessManager based on the platform
func init() {
	if isWindows() {
		processManager = &WindowsProcessManager{cmdExecutor: cmdExecutor}
	} else {
		processManager = &UnixProcessManager{cmdExecutor: cmdExecutor}
	}
}

// FFmpegMonitor handles monitoring and cleanup of FFmpeg processes
type FFmpegMonitor struct {
	monitorTicker  Ticker
	done           chan struct{}
	running        atomic.Bool
	config         ConfigProvider
	processManager ProcessManager
	processRepo    ProcessRepository
	clock          Clock
}

// NewFFmpegMonitor creates a new FFmpeg process monitor with explicit dependencies
func NewFFmpegMonitor(
	config ConfigProvider,
	procMgr ProcessManager,
	repo ProcessRepository,
	clk Clock,
) *FFmpegMonitor {
	return &FFmpegMonitor{
		done:           make(chan struct{}),
		config:         config,
		processManager: procMgr,
		processRepo:    repo,
		clock:          clk,
	}
}

// NewDefaultFFmpegMonitor creates a new FFmpeg process monitor with default dependencies
func NewDefaultFFmpegMonitor() *FFmpegMonitor {
	return NewFFmpegMonitor(
		configProvider,
		processManager,
		processRepo,
		clock,
	)
}

// Start begins monitoring FFmpeg processes
func (m *FFmpegMonitor) Start() {
	if m.running.Load() {
		return
	}

	interval := m.config.GetMonitoringInterval()
	m.monitorTicker = m.clock.NewTicker(interval)
	m.running.Store(true)

	go m.monitorLoop()
	log.Printf("🔍 FFmpeg process monitor started with interval %s", interval)
}

// Stop stops the FFmpeg process monitor
func (m *FFmpegMonitor) Stop() {
	if !m.running.Load() {
		return
	}

	if m.monitorTicker != nil {
		m.monitorTicker.Stop()
		m.monitorTicker = nil
	}

	close(m.done)
	m.running.Store(false)
	log.Printf("🛑 FFmpeg process monitor stopped")
}

// IsRunning returns whether the monitor is currently running
func (m *FFmpegMonitor) IsRunning() bool {
	return m.running.Load()
}

// monitorLoop handles periodic checking of processes
func (m *FFmpegMonitor) monitorLoop() {
	for {
		select {
		case <-m.done:
			return
		default:
			// Safe ticker access
			if m.monitorTicker == nil {
				time.Sleep(10 * time.Millisecond)
				continue
			}

			select {
			case <-m.monitorTicker.C():
				if err := m.checkProcesses(); err != nil {
					log.Printf("⚠️ Error during process check: %v", err)
				}
			case <-m.done:
				return
			}
		}
	}
}

// checkProcesses checks for and cleans up processes
func (m *FFmpegMonitor) checkProcesses() error {
	// Get configured URLs
	configuredURLs := make(map[string]bool)
	for _, url := range m.config.GetConfiguredURLs() {
		configuredURLs[url] = true
	}

	// Check running processes against configuration
	m.processRepo.ForEach(func(key, value any) bool {
		url := key.(string)

		// Use type assertion to check if value implements the ProcessCleaner interface
		if process, ok := value.(ProcessCleaner); ok {
			// If URL is not in configuration, clean up the process
			if !configuredURLs[url] {
				log.Printf("🧹 Found orphaned FFmpeg process for URL %s, cleaning up", url)
				process.Cleanup(url)
			}
		} else {
			log.Printf("⚠️ Process for URL %s doesn't implement ProcessCleaner interface", url)
		}
		return true
	})

	// Find and clean up any orphaned FFmpeg processes
	if err := m.cleanupOrphanedProcesses(); err != nil {
		return fmt.Errorf("error cleaning up orphaned FFmpeg processes: %w", err)
	}

	return nil
}

func (m *FFmpegMonitor) cleanupOrphanedProcesses() error {
	// Get system processes
	processes, err := m.processManager.FindProcesses()
	if err != nil {
		return fmt.Errorf("error finding FFmpeg processes: %w", err)
	}

	// Map system PIDs
	systemPIDs := make(map[int]bool)
	for _, proc := range processes {
		systemPIDs[proc.PID] = true
	}

	// Process our managed processes
	knownPIDs := make(map[int]string)           // Map PID to URL
	invalidProcessURLs := make(map[string]bool) // Track URLs of invalid processes

	m.processRepo.ForEach(func(key, value any) bool {
		url, ok := key.(string)
		if !ok {
			return true // Skip invalid key
		}

		process, ok := value.(*FFmpegProcess)
		if !ok || process == nil {
			invalidProcessURLs[url] = true
			return true
		}

		// Check if process reference is valid
		if process.cmd == nil || process.cmd.Process == nil {
			invalidProcessURLs[url] = true
			return true
		}

		pid := process.cmd.Process.Pid
		knownPIDs[pid] = url

		// Check if process is actually running in the system
		if !systemPIDs[pid] || !m.processManager.IsProcessRunning(pid) {
			log.Printf("🧹 Process for %s (PID %d) not running, marking for cleanup", url, pid)
			invalidProcessURLs[url] = true
		}

		return true
	})

	// Clean up invalid processes
	for url := range invalidProcessURLs {
		if process, exists := ffmpegProcesses.Load(url); exists {
			if cleaner, ok := process.(ProcessCleaner); ok {
				log.Printf("🧹 Cleaning up process for %s", url)
				cleaner.Cleanup(url)
			} else {
				log.Printf("⚠️ Process for %s doesn't implement ProcessCleaner", url)
				ffmpegProcesses.Delete(url)
			}
		}
	}

	// Clean up orphaned system processes
	for _, proc := range processes {
		if _, exists := knownPIDs[proc.PID]; !exists {
			log.Printf("🧹 Found orphaned FFmpeg process with PID %d, terminating", proc.PID)
			if err := m.processManager.TerminateProcess(proc.PID); err != nil {
				log.Printf("⚠️ Error terminating process %d: %v", proc.PID, err)
			}
		}
	}

	return nil
}

// isWindows returns true if running on Windows
func isWindows() bool {
	return conf.GetFfmpegBinaryName() == "ffmpeg.exe"
}
