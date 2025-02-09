package myaudio

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// FFmpegMonitor handles monitoring and cleanup of FFmpeg processes
type FFmpegMonitor struct {
	mu            sync.Mutex
	monitorTicker *time.Ticker
	done          chan struct{}
}

// NewFFmpegMonitor creates a new FFmpeg process monitor
func NewFFmpegMonitor() *FFmpegMonitor {
	return &FFmpegMonitor{
		done: make(chan struct{}),
	}
}

// Start begins monitoring FFmpeg processes
func (m *FFmpegMonitor) Start() {
	m.mu.Lock()
	if m.monitorTicker != nil {
		m.mu.Unlock()
		return
	}
	m.monitorTicker = time.NewTicker(30 * time.Second)
	m.mu.Unlock()

	go m.monitorLoop()
}

// Stop stops the FFmpeg process monitor
func (m *FFmpegMonitor) Stop() {
	m.mu.Lock()
	if m.monitorTicker != nil {
		m.monitorTicker.Stop()
		m.monitorTicker = nil
	}
	m.mu.Unlock()
	close(m.done)
}

// monitorLoop is the main monitoring loop
func (m *FFmpegMonitor) monitorLoop() {
	for {
		select {
		case <-m.done:
			return
		case <-m.monitorTicker.C:
			m.checkProcesses()
		}
	}
}

// checkProcesses verifies running FFmpeg processes against configuration
func (m *FFmpegMonitor) checkProcesses() {
	// Get configured URLs
	settings := conf.Setting()
	configuredURLs := make(map[string]bool)
	for _, url := range settings.Realtime.RTSP.URLs {
		configuredURLs[url] = true
	}

	// Check running processes against configuration
	ffmpegProcesses.Range(func(key, value interface{}) bool {
		url := key.(string)
		process := value.(*FFmpegProcess)

		// If URL is not in configuration, clean up the process
		if !configuredURLs[url] {
			log.Printf("🧹 Found orphaned FFmpeg process for URL %s, cleaning up", url)
			process.Cleanup(url)
		}
		return true
	})

	// Find and clean up any orphaned FFmpeg processes
	if err := cleanupOrphanedProcesses(); err != nil {
		log.Printf("⚠️ Error cleaning up orphaned FFmpeg processes: %v", err)
	}
}

// cleanupOrphanedProcesses finds and terminates orphaned FFmpeg processes
func cleanupOrphanedProcesses() error {
	// Get list of all FFmpeg processes
	processes, err := findFFmpegProcesses()
	if err != nil {
		return fmt.Errorf("error finding FFmpeg processes: %w", err)
	}

	// Get list of known process IDs
	knownPIDs := make(map[int]bool)
	ffmpegProcesses.Range(func(key, value interface{}) bool {
		if process, ok := value.(*FFmpegProcess); ok && process.cmd != nil && process.cmd.Process != nil {
			knownPIDs[process.cmd.Process.Pid] = true
		}
		return true
	})

	// Clean up any processes not in our known list
	for _, pid := range processes {
		if !knownPIDs[pid] {
			log.Printf("🧹 Found orphaned FFmpeg process with PID %d, terminating", pid)
			if err := terminateProcess(pid); err != nil {
				log.Printf("⚠️ Error terminating FFmpeg process %d: %v", pid, err)
			}
		}
	}

	return nil
}

// findFFmpegProcesses returns a list of FFmpeg process IDs
func findFFmpegProcesses() ([]int, error) {
	var pids []int
	var cmd *exec.Cmd

	switch {
	case isWindows():
		// Use tasklist on Windows
		cmd = exec.Command("tasklist", "/FI", "IMAGENAME eq ffmpeg.exe", "/NH", "/FO", "CSV")
	default:
		// Use pgrep on Unix systems
		cmd = exec.Command("pgrep", "ffmpeg")
	}

	output, err := cmd.Output()
	if err != nil {
		// If the command returns no processes, that's not an error
		if strings.Contains(err.Error(), "exit status 1") {
			return nil, nil
		}
		return nil, fmt.Errorf("error running process list command: %w", err)
	}

	// Parse the output based on OS
	if isWindows() {
		// Parse Windows CSV output
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
						pids = append(pids, pid)
					}
				}
			}
		}
	} else {
		// Parse Unix pgrep output
		for _, line := range strings.Split(string(output), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				var pid int
				if _, err := fmt.Sscanf(line, "%d", &pid); err == nil {
					pids = append(pids, pid)
				}
			}
		}
	}

	return pids, nil
}

// terminateProcess terminates a process by its PID
func terminateProcess(pid int) error {
	if isWindows() {
		// Use taskkill on Windows
		cmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(pid))
		return cmd.Run()
	}
	// Use kill on Unix systems
	cmd := exec.Command("kill", "-9", fmt.Sprint(pid))
	return cmd.Run()
}

// isWindows returns true if running on Windows
func isWindows() bool {
	return conf.GetFfmpegBinaryName() == "ffmpeg.exe"
}
