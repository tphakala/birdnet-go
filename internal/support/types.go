// Package support provides functionality for collecting and uploading support logs
package support

import (
	"time"
)

// SupportDump represents a collection of support data
type SupportDump struct {
	ID          string           `json:"id"`
	Timestamp   time.Time        `json:"timestamp"`
	SystemID    string           `json:"system_id"`
	Version     string           `json:"version"`
	Logs        []LogEntry       `json:"logs"`
	Config      map[string]any   `json:"config"`
	SystemInfo  SystemInfo       `json:"system_info"`
	Attachments []AttachmentInfo `json:"attachments"`
}

// LogEntry represents a single log entry
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
}

// SystemInfo contains system information
type SystemInfo struct {
	OS           string            `json:"os"`
	Architecture string            `json:"architecture"`
	GoVersion    string            `json:"go_version"`
	CPUCount     int               `json:"cpu_count"`
	MemoryMB     uint64            `json:"memory_mb"`
	DiskInfo     map[string]uint64 `json:"disk_info"`
	DockerInfo   *DockerInfo       `json:"docker_info,omitempty"`
}

// DockerInfo contains Docker-specific information
type DockerInfo struct {
	Version      string `json:"version"`
	ContainerID  string `json:"container_id"`
	ImageVersion string `json:"image_version"`
}

// AttachmentInfo describes an attachment
type AttachmentInfo struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	Path        string `json:"-"` // Internal use only
}

// CollectorOptions configures the support dump collector
type CollectorOptions struct {
	IncludeLogs       bool          `json:"include_logs"`
	IncludeConfig     bool          `json:"include_config"`
	IncludeSystemInfo bool          `json:"include_system_info"`
	LogDuration       time.Duration `json:"log_duration"`
	MaxLogSize        int64         `json:"max_log_size"`
	ScrubSensitive    bool          `json:"scrub_sensitive"`
}

// DefaultCollectorOptions returns default collector options
func DefaultCollectorOptions() CollectorOptions {
	return CollectorOptions{
		IncludeLogs:       true,
		IncludeConfig:     true,
		IncludeSystemInfo: true,
		LogDuration:       24 * time.Hour,
		MaxLogSize:        10 * 1024 * 1024, // 10MB
		ScrubSensitive:    true,
	}
}
