// Package support provides functionality for collecting and uploading support logs
package support

import (
	"time"
)

// SupportDump represents a collection of support data including logs, configuration,
// and system information used for troubleshooting and debugging BirdNET-Go issues.
// The data is privacy-scrubbed before collection to remove sensitive information.
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

// LogEntry represents a single log entry from application logs or system journals.
// It captures the timestamp, severity level, message content, and source of the log.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
}

// SystemInfo contains system information about the host environment where BirdNET-Go
// is running, including OS details, hardware specifications, and runtime information.
// This helps developers understand the deployment context when debugging issues.
type SystemInfo struct {
	OS           string      `json:"os"`
	Architecture string      `json:"architecture"`
	GoVersion    string      `json:"go_version"`
	CPUCount     int         `json:"cpu_count"`
	MemoryMB     uint64      `json:"memory_mb"`
	DiskInfo     []DiskInfo  `json:"disk_info"`
	DockerInfo   *DockerInfo `json:"docker_info,omitempty"`
}

// DiskInfo represents information about a disk or filesystem mount point.
// It includes usage statistics that help diagnose storage-related issues.
type DiskInfo struct {
	Mountpoint string  `json:"mountpoint"`
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Free       uint64  `json:"free"`
	UsagePerc  float64 `json:"usage_percent"`
}

// DockerInfo contains Docker-specific information when BirdNET-Go is running in a container.
// This includes the container ID and image details for container-specific debugging.
type DockerInfo struct {
	Version      string `json:"version"`
	ContainerID  string `json:"container_id"`
	ImageVersion string `json:"image_version"`
}

// AttachmentInfo contains metadata about a support dump attachment uploaded to Sentry.
// It tracks the filename, size, content type, and internal path for reference.
type AttachmentInfo struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	Path        string `json:"-"` // Internal use only
}

// CollectorOptions configures what data to collect in a support dump.
// It allows users to control which types of information are included based on
// their privacy preferences and the specific issue being debugged.
type CollectorOptions struct {
	IncludeLogs       bool          `json:"include_logs"`
	IncludeConfig     bool          `json:"include_config"`
	IncludeSystemInfo bool          `json:"include_system_info"`
	LogDuration       time.Duration `json:"log_duration"`
	MaxLogSize        int64         `json:"max_log_size"`
	ScrubSensitive    bool          `json:"scrub_sensitive"`
}

// DefaultCollectorOptions returns default collector options with sensible defaults:
// includes all data types, 4-week log window, 50MB max log size, and sensitive data scrubbing enabled.
func DefaultCollectorOptions() CollectorOptions {
	return CollectorOptions{
		IncludeLogs:       true,
		IncludeConfig:     true,
		IncludeSystemInfo: true,
		LogDuration:       4 * 7 * 24 * time.Hour, // 4 weeks
		MaxLogSize:        50 * 1024 * 1024,       // 50MB to accommodate more logs
		ScrubSensitive:    true,
	}
}
