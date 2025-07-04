package ffmpeg

import (
	"context"
	"time"
)

// Process represents a managed FFmpeg process
type Process interface {
	// ID returns the unique identifier for this process
	ID() string

	// Start starts the FFmpeg process
	Start(ctx context.Context) error

	// Stop gracefully stops the FFmpeg process
	Stop() error

	// Wait waits for the process to exit
	Wait() error

	// IsRunning returns true if the process is currently running
	IsRunning() bool

	// AudioOutput returns the channel for audio data output
	AudioOutput() <-chan []byte

	// ErrorOutput returns the channel for error messages
	ErrorOutput() <-chan error

	// Metrics returns current process metrics
	Metrics() ProcessMetrics
}

// ProcessMetrics contains runtime metrics for a process
type ProcessMetrics struct {
	StartTime    time.Time
	Uptime       time.Duration
	RestartCount int
	LastError    error
	LastRestart  time.Time
	BytesRead    int64
	FramesRead   int64
}

// ProcessConfig contains configuration for an FFmpeg process
type ProcessConfig struct {
	ID             string
	InputURL       string
	OutputFormat   string
	SampleRate     int
	Channels       int
	BitDepth       int
	BufferSize     int
	ExtraArgs      []string
	FFmpegPath     string
	RestartOnError bool
}

// Manager manages multiple FFmpeg processes
type Manager interface {
	// CreateProcess creates a new managed FFmpeg process
	CreateProcess(config *ProcessConfig) (Process, error)

	// GetProcess returns a process by ID
	GetProcess(id string) (Process, bool)

	// ListProcesses returns all managed processes
	ListProcesses() []Process

	// RemoveProcess stops and removes a process
	RemoveProcess(id string) error

	// Start starts the manager
	Start(ctx context.Context) error

	// Stop stops all processes and the manager
	Stop() error

	// HealthCheck performs a health check on all processes
	HealthCheck() error
}

// HealthChecker checks the health of an FFmpeg process
type HealthChecker interface {
	// Check performs a health check on the process
	Check(process Process) error

	// SetSilenceThreshold sets the silence detection threshold
	SetSilenceThreshold(threshold float32, duration time.Duration)
}

// RestartPolicy defines how processes should be restarted
type RestartPolicy struct {
	Enabled           bool
	MaxRetries        int
	InitialDelay      time.Duration
	MaxDelay          time.Duration
	BackoffMultiplier float64
}

// ManagerConfig contains configuration for the FFmpeg manager
type ManagerConfig struct {
	MaxProcesses      int
	RestartPolicy     RestartPolicy
	HealthCheckPeriod time.Duration
	CleanupTimeout    time.Duration
	MetricsEnabled    bool
}