package support

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gopkg.in/yaml.v3"
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
	// Set defaults for empty paths
	if configPath == "" {
		configPath = "."
	}
	if dataPath == "" {
		dataPath = "."
	}
	
	return &Collector{
		configPath: configPath,
		dataPath:   dataPath,
		systemID:   systemID,
		version:    version,
	}
}

// Collect gathers support data based on the provided options
func (c *Collector) Collect(ctx context.Context, opts CollectorOptions) (*SupportDump, error) {
	// Validate options
	if !opts.IncludeLogs && !opts.IncludeConfig && !opts.IncludeSystemInfo {
		return nil, errors.Newf("at least one data type must be included in support dump").
			Component("support").
			Category(errors.CategoryValidation).
			Context("operation", "validate_collect_options").
			Build()
	}

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
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryConfiguration).
				Context("operation", "collect_config").
				Context("scrub_sensitive", opts.ScrubSensitive).
				Build()
		}
		dump.Config = config
	}

	// Collect logs
	if opts.IncludeLogs {
		logs, err := c.collectLogs(opts.LogDuration, opts.MaxLogSize)
		if err != nil {
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "collect_logs").
				Context("log_duration", opts.LogDuration.String()).
				Context("max_log_size", opts.MaxLogSize).
				Build()
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
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "create_metadata_file").
			Context("archive_action", "create_file").
			Build()
	}
	if err := json.NewEncoder(metadataFile).Encode(dump); err != nil {
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "write_metadata").
			Context("dump_id", dump.ID).
			Build()
	}

	// Add logs as separate file
	if opts.IncludeLogs && len(dump.Logs) > 0 {
		logsFile, err := w.Create("logs.json")
		if err != nil {
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "create_logs_file").
				Context("log_count", len(dump.Logs)).
				Build()
		}
		if err := json.NewEncoder(logsFile).Encode(dump.Logs); err != nil {
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "write_logs").
				Context("log_count", len(dump.Logs)).
				Build()
		}
	}

	// Add config as separate file
	if opts.IncludeConfig && dump.Config != nil {
		configFile, err := w.Create("config.json")
		if err != nil {
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "create_config_file").
				Build()
		}
		if err := json.NewEncoder(configFile).Encode(dump.Config); err != nil {
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "write_config").
				Build()
		}
	}

	// Add system info
	if opts.IncludeSystemInfo {
		sysInfoFile, err := w.Create("system_info.json")
		if err != nil {
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "create_system_info_file").
				Build()
		}
		if err := json.NewEncoder(sysInfoFile).Encode(dump.SystemInfo); err != nil {
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "write_system_info").
				Build()
		}
	}

	if err := w.Close(); err != nil {
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "close_archive").
			Context("archive_size", buf.Len()).
			Build()
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

	// Add Raspberry Pi detection
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		if content, err := os.ReadFile("/proc/device-tree/model"); err == nil {
			model := strings.TrimSpace(string(content))
			if strings.Contains(model, "Raspberry Pi") {
				info.DiskInfo["raspberry_pi_model"] = 1 // Just to indicate it's a Pi
				// You could parse the model string for more details
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
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "read_config_file").
			Context("config_path", configPath).
			Build()
	}

	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryConfiguration).
			Context("operation", "parse_config_yaml").
			Context("file_size", len(data)).
			Build()
	}

	if scrub {
		config = c.scrubConfig(config)
	}

	return config, nil
}

// scrubConfig removes sensitive information from configuration
func (c *Collector) scrubConfig(config map[string]any) map[string]any {
	// List of sensitive keys to redact (expanded list)
	sensitiveKeys := []string{
		"password", "token", "secret", "key", "api_key", "api_token",
		"client_id", "client_secret", "webhook_url", "mqtt_password",
		"id", "apikey", "username", "broker", "topic", "urls",
		"mqtt_username", "mqtt_topic", "birdweather_id",
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

	// Try to collect from journald first (systemd systems)
	journalLogs, err := c.collectJournalLogs(duration)
	if err == nil && len(journalLogs) > 0 {
		logs = append(logs, journalLogs...)
	}

	// Also check for log files in the data directory
	logFiles, err := c.collectLogFiles(duration, maxSize)
	if err == nil && len(logFiles) > 0 {
		logs = append(logs, logFiles...)
	}

	// Sort logs by timestamp
	sortLogsByTime(logs)

	return logs, nil
}

// collectJournalLogs collects logs from systemd journal
func (c *Collector) collectJournalLogs(duration time.Duration) ([]LogEntry, error) {
	var logs []LogEntry

	// Calculate since time
	since := time.Now().Add(-duration).Format("2006-01-02 15:04:05")
	
	// Run journalctl command
	cmd := exec.Command("journalctl", 
		"-u", "birdnet-go.service",
		"--since", since,
		"--no-pager",
		"-o", "json",
		"--no-hostname")
	
	output, err := cmd.Output()
	if err != nil {
		// journalctl might not be available or service might not exist
		// This is not a fatal error, just means no journald logs available
		return logs, nil
	}

	// Parse JSON output line by line
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Skip malformed JSON lines silently
			continue
		}

		// Extract fields
		message, _ := entry["MESSAGE"].(string)
		priority, _ := entry["PRIORITY"].(string)
		timestamp, _ := entry["__REALTIME_TIMESTAMP"].(string)
		
		// Convert timestamp (microseconds since epoch)
		var ts time.Time
		if timestamp != "" {
			if usec, err := strconv.ParseInt(timestamp, 10, 64); err == nil {
				ts = time.Unix(0, usec*1000)
			}
		}

		// Map priority to log level
		level := "INFO"
		switch priority {
		case "0", "1", "2", "3":
			level = "ERROR"
		case "4":
			level = "WARNING"
		case "5", "6":
			level = "INFO"
		case "7":
			level = "DEBUG"
		}

		logs = append(logs, LogEntry{
			Timestamp: ts,
			Level:     level,
			Message:   message,
			Source:    "journald",
		})
	}

	return logs, nil
}

// collectLogFiles collects logs from files in the data directory
func (c *Collector) collectLogFiles(duration time.Duration, maxSize int64) ([]LogEntry, error) {
	var logs []LogEntry
	
	// Look for log files in common locations
	// The logging package creates logs in a "logs" directory
	logPaths := []string{
		"logs",                                    // Default logs directory from logging package
		filepath.Join(c.dataPath, "logs"),        // Legacy location
		filepath.Join(c.dataPath, "birdnet.log"), // Legacy location
		filepath.Join(c.configPath, "logs"),      // Config directory logs
	}

	cutoffTime := time.Now().Add(-duration)
	totalSize := int64(0)

	for _, logPath := range logPaths {
		// Check if path exists
		info, err := os.Stat(logPath)
		if err != nil {
			continue
		}

		// If it's a directory, look for log files
		if info.IsDir() {
			files, err := os.ReadDir(logPath)
			if err != nil {
				continue
			}

			for _, file := range files {
				if strings.HasSuffix(file.Name(), ".log") {
					fileLogs, size := c.parseLogFile(filepath.Join(logPath, file.Name()), cutoffTime, maxSize-totalSize)
					logs = append(logs, fileLogs...)
					totalSize += size
					if totalSize >= maxSize {
						break
					}
				}
			}
		} else {
			// It's a file
			fileLogs, size := c.parseLogFile(logPath, cutoffTime, maxSize-totalSize)
			logs = append(logs, fileLogs...)
			totalSize += size
		}

		if totalSize >= maxSize {
			break
		}
	}

	return logs, nil
}

// parseLogFile parses a log file and extracts entries
func (c *Collector) parseLogFile(path string, cutoffTime time.Time, maxSize int64) ([]LogEntry, int64) {
	var logs []LogEntry
	var totalSize int64

	file, err := os.Open(path)
	if err != nil {
		// Log file might not exist or be inaccessible, which is fine
		return logs, 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		totalSize += int64(len(line))
		
		if totalSize > maxSize {
			break
		}

		// Simple log parsing - adjust based on actual log format
		entry := c.parseLogLine(line)
		if entry != nil && entry.Timestamp.After(cutoffTime) {
			logs = append(logs, *entry)
		}
	}

	return logs, totalSize
}

// parseLogLine parses a single log line
func (c *Collector) parseLogLine(line string) *LogEntry {
	// First try to parse as JSON (from slog)
	var jsonLog map[string]interface{}
	if err := json.Unmarshal([]byte(line), &jsonLog); err == nil {
		// Extract fields from JSON log
		entry := &LogEntry{
			Source: "file",
		}
		
		// Parse timestamp
		if timeStr, ok := jsonLog["time"].(string); ok {
			if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
				entry.Timestamp = t
			}
		}
		
		// Parse level
		if level, ok := jsonLog["level"].(string); ok {
			entry.Level = strings.ToUpper(level)
		}
		
		// Parse message
		if msg, ok := jsonLog["msg"].(string); ok {
			entry.Message = msg
		}
		
		// Add service info if available
		if service, ok := jsonLog["service"].(string); ok {
			entry.Source = service
		}
		
		if entry.Message != "" {
			return entry
		}
	}
	
	// Fallback to simple text format
	// Format: 2024-01-20 15:04:05 [LEVEL] message
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 4 {
		return nil
	}

	// Parse timestamp
	timestamp, err := time.Parse("2006-01-02 15:04:05", parts[0]+" "+parts[1])
	if err != nil {
		return nil
	}

	// Extract level
	level := strings.Trim(parts[2], "[]")
	
	return &LogEntry{
		Timestamp: timestamp,
		Level:     level,
		Message:   parts[3],
		Source:    "file",
	}
}

// sortLogsByTime sorts log entries by timestamp
func sortLogsByTime(logs []LogEntry) {
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].Timestamp.Before(logs[j].Timestamp)
	})
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