package support

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Collector collects support data for troubleshooting
type Collector struct {
	configPath string
	dataPath   string
	systemID   string
	version    string
}

// NewCollector creates a new support data collector
func NewCollector(configPath, dataPath, systemID, version string) *Collector {
	return &Collector{
		configPath: configPath,
		dataPath:   dataPath,
		systemID:   systemID,
		version:    version,
	}
}

// Collect gathers support data based on the provided options
func (c *Collector) Collect(ctx context.Context, opts CollectorOptions) (*SupportDump, error) {
	dump := &SupportDump{
		ID:        uuid.New().String(),
		Timestamp: time.Now().UTC(),
		SystemID:  c.systemID,
		Version:   c.version,
	}

	// Collect system information
	if opts.IncludeSystemInfo {
		dump.SystemInfo = c.collectSystemInfo()
	}

	// Collect configuration (scrubbed)
	if opts.IncludeConfig {
		config, err := c.collectConfig(opts.ScrubSensitive)
		if err != nil {
			return nil, fmt.Errorf("failed to collect config: %w", err)
		}
		dump.Config = config
	}

	// Collect logs
	if opts.IncludeLogs {
		logs, err := c.collectLogs(opts.LogDuration, opts.MaxLogSize)
		if err != nil {
			return nil, fmt.Errorf("failed to collect logs: %w", err)
		}
		dump.Logs = logs
	}

	return dump, nil
}

// CreateArchive creates a zip archive containing the support dump
func (c *Collector) CreateArchive(ctx context.Context, dump *SupportDump, opts CollectorOptions) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// Add metadata
	metadataFile, err := w.Create("metadata.json")
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata file: %w", err)
	}
	if err := json.NewEncoder(metadataFile).Encode(dump); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	// Add logs as separate file
	if opts.IncludeLogs && len(dump.Logs) > 0 {
		logsFile, err := w.Create("logs.json")
		if err != nil {
			return nil, fmt.Errorf("failed to create logs file: %w", err)
		}
		if err := json.NewEncoder(logsFile).Encode(dump.Logs); err != nil {
			return nil, fmt.Errorf("failed to write logs: %w", err)
		}
	}

	// Add config as separate file
	if opts.IncludeConfig && dump.Config != nil {
		configFile, err := w.Create("config.json")
		if err != nil {
			return nil, fmt.Errorf("failed to create config file: %w", err)
		}
		if err := json.NewEncoder(configFile).Encode(dump.Config); err != nil {
			return nil, fmt.Errorf("failed to write config: %w", err)
		}
	}

	// Add system info
	if opts.IncludeSystemInfo {
		sysInfoFile, err := w.Create("system_info.json")
		if err != nil {
			return nil, fmt.Errorf("failed to create system info file: %w", err)
		}
		if err := json.NewEncoder(sysInfoFile).Encode(dump.SystemInfo); err != nil {
			return nil, fmt.Errorf("failed to write system info: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("failed to close archive: %w", err)
	}

	return buf.Bytes(), nil
}

// collectSystemInfo gathers system information
func (c *Collector) collectSystemInfo() SystemInfo {
	info := SystemInfo{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		GoVersion:    runtime.Version(),
		CPUCount:     runtime.NumCPU(),
		DiskInfo:     make(map[string]uint64),
	}

	// Get memory info (simplified, platform-specific implementations would be better)
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	info.MemoryMB = memStats.Sys / 1024 / 1024

	// Check if running in Docker
	if _, err := os.Stat("/.dockerenv"); err == nil {
		info.DockerInfo = &DockerInfo{}
		// Try to get container ID
		if data, err := os.ReadFile("/proc/self/cgroup"); err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				if strings.Contains(line, "docker") {
					parts := strings.Split(line, "/")
					if len(parts) > 0 {
						info.DockerInfo.ContainerID = parts[len(parts)-1]
						break
					}
				}
			}
		}
	}

	return info
}

// collectConfig loads and scrubs the configuration
func (c *Collector) collectConfig(scrub bool) (map[string]any, error) {
	// Load config file
	configPath := filepath.Join(c.configPath, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config map[string]any
	if err := conf.UnmarshalYAML(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if scrub {
		config = c.scrubConfig(config)
	}

	return config, nil
}

// scrubConfig removes sensitive information from configuration
func (c *Collector) scrubConfig(config map[string]any) map[string]any {
	// List of sensitive keys to redact
	sensitiveKeys := []string{
		"password", "token", "secret", "key", "api_key", "api_token",
		"client_id", "client_secret", "webhook_url", "mqtt_password",
	}

	scrubbed := make(map[string]any)
	for k, v := range config {
		scrubbed[k] = c.scrubValue(k, v, sensitiveKeys)
	}

	return scrubbed
}

// scrubValue recursively scrubs sensitive values
func (c *Collector) scrubValue(key string, value any, sensitiveKeys []string) any {
	// Check if key is sensitive
	lowerKey := strings.ToLower(key)
	for _, sensitive := range sensitiveKeys {
		if strings.Contains(lowerKey, sensitive) {
			return "[REDACTED]"
		}
	}

	// Recursively scrub nested structures
	switch v := value.(type) {
	case map[string]any:
		scrubbed := make(map[string]any)
		for k, val := range v {
			scrubbed[k] = c.scrubValue(k, val, sensitiveKeys)
		}
		return scrubbed
	case []any:
		scrubbed := make([]any, len(v))
		for i, item := range v {
			scrubbed[i] = c.scrubValue(key, item, sensitiveKeys)
		}
		return scrubbed
	default:
		return value
	}
}

// collectLogs collects recent log entries
func (c *Collector) collectLogs(duration time.Duration, maxSize int64) ([]LogEntry, error) {
	var logs []LogEntry

	// For now, we'll collect journald logs if available
	// In a real implementation, this would also collect application logs
	
	// This is a placeholder - actual implementation would:
	// 1. Read from journald if available
	// 2. Read from log files in the data directory
	// 3. Parse and filter log entries
	
	// Example log entry
	logs = append(logs, LogEntry{
		Timestamp: time.Now().Add(-1 * time.Hour),
		Level:     "INFO",
		Message:   "BirdNET-Go started successfully",
		Source:    "main",
	})

	return logs, nil
}

// SanitizeFilename ensures the filename is safe for use
func SanitizeFilename(name string) string {
	// Replace unsafe characters
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
	)
	return replacer.Replace(name)
}