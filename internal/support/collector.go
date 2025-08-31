package support

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"gopkg.in/yaml.v3"
)

// Package-level logger specific to support service
var (
	serviceLogger   *slog.Logger
	serviceLevelVar = new(slog.LevelVar) // Dynamic level control
	closeLogger     func() error
)

// Sentinel errors for support collector operations
var (
	ErrJournalNotAvailable = errors.NewStd("journal logs not available")
)

// isRunningInDocker detects if the application is running inside a Docker container.
// This is useful for adjusting log collection strategies, as Docker containers
// often don't have systemd/journald available.
//
// Detection methods:
//  1. Check for /.dockerenv file (standard Docker marker)
//  2. Check /proc/1/cgroup for docker references
//
// Returns true if running in Docker, false otherwise.
func isRunningInDocker() bool {
	// Check for .dockerenv file
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// Check cgroup for docker references
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		if strings.Contains(string(data), "docker") || strings.Contains(string(data), "containerd") {
			return true
		}
	}

	return false
}

func init() {
	var err error
	// Define log file path relative to working directory
	logFilePath := filepath.Join("logs", "support.log")
	initialLevel := slog.LevelDebug // Set desired initial level
	serviceLevelVar.Set(initialLevel)

	// Initialize the service-specific file logger
	serviceLogger, closeLogger, err = logging.NewFileLogger(logFilePath, "support", serviceLevelVar)
	if err != nil {
		// Fallback: Log error to standard log and potentially disable service logging
		log.Printf("FATAL: Failed to initialize support file logger at %s: %v. Service logging disabled.", logFilePath, err)
		// Set logger to a disabled handler to prevent nil panics, but respects level var
		fbHandler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: serviceLevelVar})
		serviceLogger = slog.New(fbHandler).With("service", "support")
		closeLogger = func() error { return nil } // No-op closer
	}
}

// Collector collects support data for troubleshooting
type Collector struct {
	configPath    string
	dataPath      string
	systemID      string
	version       string
	sensitiveKeys []string
}

// defaultSensitiveKeys returns the default list of sensitive configuration keys to redact
func defaultSensitiveKeys() []string {
	return []string{
		"password", "token", "secret", "key", "api_key", "api_token",
		"client_id", "client_secret", "webhook_url", "mqtt_password",
		"apikey", "username", "broker", "topic",
		"mqtt_username", "mqtt_topic", "birdweather_id",
	}
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
		configPath:    configPath,
		dataPath:      dataPath,
		systemID:      systemID,
		version:       version,
		sensitiveKeys: defaultSensitiveKeys(),
	}
}

// NewCollectorWithOptions creates a new support data collector with custom options
func NewCollectorWithOptions(configPath, dataPath, systemID, version string, sensitiveKeys []string) *Collector {
	// Set defaults for empty paths
	if configPath == "" {
		configPath = "."
	}
	if dataPath == "" {
		dataPath = "."
	}

	// Use default sensitive keys if none provided
	if len(sensitiveKeys) == 0 {
		sensitiveKeys = defaultSensitiveKeys()
	}

	return &Collector{
		configPath:    configPath,
		dataPath:      dataPath,
		systemID:      systemID,
		version:       version,
		sensitiveKeys: sensitiveKeys,
	}
}

// Collect gathers support data based on the provided options
func (c *Collector) Collect(ctx context.Context, opts CollectorOptions) (*SupportDump, error) {
	// Validate options
	if !opts.IncludeLogs && !opts.IncludeConfig && !opts.IncludeSystemInfo {
		serviceLogger.Error("support: collection validation failed: at least one data type must be included")
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
		Diagnostics: CollectionDiagnostics{
			LogCollection: LogCollectionDiagnostics{
				JournalLogs: LogSourceDiagnostics{PathsSearched: []SearchedPath{}, Details: make(map[string]any)},
				FileLogs:    LogSourceDiagnostics{PathsSearched: []SearchedPath{}, Details: make(map[string]any)},
				Summary:     DiagnosticSummary{TimeRange: TimeRange{From: time.Now().Add(-opts.LogDuration), To: time.Now()}},
			},
		},
	}

	serviceLogger.Info("support: starting collection",
		"dump_id", dump.ID,
		"include_logs", opts.IncludeLogs,
		"include_config", opts.IncludeConfig,
		"include_system_info", opts.IncludeSystemInfo,
		"log_duration", opts.LogDuration,
		"max_log_size", opts.MaxLogSize)

	// Collect system information
	if opts.IncludeSystemInfo {
		serviceLogger.Debug("support: collecting system information")
		dump.Diagnostics.SystemCollection.Attempted = true
		dump.SystemInfo = c.collectSystemInfo()
		dump.Diagnostics.SystemCollection.Successful = true
		serviceLogger.Debug("support: system information collected",
			"os", dump.SystemInfo.OS,
			"arch", dump.SystemInfo.Architecture,
			"cpu_count", dump.SystemInfo.CPUCount,
			"memory_mb", dump.SystemInfo.MemoryMB)
	}

	// Collect configuration (scrubbed)
	if opts.IncludeConfig {
		serviceLogger.Debug("support: collecting configuration", "scrub_sensitive", opts.ScrubSensitive)
		dump.Diagnostics.ConfigCollection.Attempted = true
		config, err := c.collectConfig(opts.ScrubSensitive)
		if err != nil {
			serviceLogger.Error("support: failed to collect configuration", "error", err)
			dump.Diagnostics.ConfigCollection.Error = err.Error()
			// Don't fail the entire collection - continue with other data
		} else {
			dump.Config = config
			dump.Diagnostics.ConfigCollection.Successful = true
			serviceLogger.Debug("support: configuration collected successfully")
		}
	}

	// Collect logs
	if opts.IncludeLogs {
		serviceLogger.Debug("support: collecting logs", "duration", opts.LogDuration, "max_size", opts.MaxLogSize, "anonymize_pii", opts.AnonymizePII)
		logs := c.collectLogs(ctx, opts.LogDuration, opts.MaxLogSize, opts.AnonymizePII, &dump.Diagnostics.LogCollection)
		dump.Logs = logs
		serviceLogger.Debug("support: logs collected", "log_count", len(logs))
	}

	serviceLogger.Info("support: collection completed successfully",
		"dump_id", dump.ID,
		"log_count", len(dump.Logs))

	return dump, nil
}

// CreateArchive creates a zip archive containing the support dump
func (c *Collector) CreateArchive(ctx context.Context, dump *SupportDump, opts CollectorOptions) ([]byte, error) {
	serviceLogger.Info("support: creating archive", "dump_id", dump.ID)

	// Check context early
	select {
	case <-ctx.Done():
		serviceLogger.Warn("support: context cancelled before archive creation", "error", ctx.Err())
		return nil, errors.New(ctx.Err()).
			Component("support").
			Category(errors.CategoryNetwork).
			Context("operation", "create_archive").
			Context("stage", "pre_creation").
			Build()
	default:
		// Continue
	}

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// Add metadata (keep this as JSON for easy parsing)
	serviceLogger.Debug("support: adding metadata to archive", "dump_id", dump.ID)
	metadataFile, err := w.Create("metadata.json")
	if err != nil {
		serviceLogger.Error("support: failed to create metadata file in archive", "error", err)
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "create_metadata_file").
			Context("archive_action", "create_file").
			Build()
	}
	// Only include basic metadata, not the full dump content
	metadata := map[string]any{
		"id":                      dump.ID,
		"timestamp":               dump.Timestamp,
		"system_id":               dump.SystemID,
		"version":                 dump.Version,
		"log_count":               len(dump.Logs),
		"includes_diagnostics":    true,
		"config_collection_error": dump.Diagnostics.ConfigCollection.Error != "",
		"log_collection_summary": map[string]any{
			"total_entries":      dump.Diagnostics.LogCollection.Summary.TotalEntries,
			"journal_attempted":  dump.Diagnostics.LogCollection.JournalLogs.Attempted,
			"journal_successful": dump.Diagnostics.LogCollection.JournalLogs.Successful,
			"files_attempted":    dump.Diagnostics.LogCollection.FileLogs.Attempted,
			"files_successful":   dump.Diagnostics.LogCollection.FileLogs.Successful,
		},
	}
	if err := json.NewEncoder(metadataFile).Encode(metadata); err != nil {
		serviceLogger.Error("support: failed to write metadata to archive", "error", err)
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "write_metadata").
			Context("dump_id", dump.ID).
			Build()
	}

	// Check context before adding logs
	select {
	case <-ctx.Done():
		serviceLogger.Warn("support: context cancelled before adding logs", "error", ctx.Err())
		return nil, errors.New(ctx.Err()).
			Component("support").
			Category(errors.CategoryNetwork).
			Context("operation", "create_archive").
			Context("stage", "before_logs").
			Build()
	default:
	}

	// Add log files in original format
	if opts.IncludeLogs {
		serviceLogger.Debug("support: adding log files to archive", "anonymize_pii", opts.AnonymizePII)
		if err := c.addLogFilesToArchive(ctx, w, opts.LogDuration, opts.MaxLogSize, opts.AnonymizePII); err != nil {
			serviceLogger.Error("support: failed to add log files to archive", "error", err)
			return nil, err
		}
		serviceLogger.Debug("support: log files added successfully")
	}

	// Add config file in original YAML format (scrubbed)
	if opts.IncludeConfig {
		serviceLogger.Debug("support: adding config file to archive", "scrub_sensitive", opts.ScrubSensitive)
		if err := c.addConfigToArchive(w, opts.ScrubSensitive); err != nil {
			serviceLogger.Error("support: failed to add config to archive", "error", err)
			return nil, err
		}
		serviceLogger.Debug("support: config file added successfully")
	}

	// Add system info
	if opts.IncludeSystemInfo {
		serviceLogger.Debug("support: adding system info to archive")
		sysInfoFile, err := w.Create("system_info.json")
		if err != nil {
			serviceLogger.Error("support: failed to create system info file in archive", "error", err)
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "create_system_info_file").
				Build()
		}
		if err := json.NewEncoder(sysInfoFile).Encode(dump.SystemInfo); err != nil {
			serviceLogger.Error("support: failed to write system info to archive", "error", err)
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "write_system_info").
				Build()
		}
		serviceLogger.Debug("support: system info added successfully")
	}

	// Always add diagnostics - this is crucial for troubleshooting collection issues
	serviceLogger.Debug("support: adding collection diagnostics to archive")
	diagnosticsFile, err := w.Create("collection_diagnostics.json")
	if err != nil {
		serviceLogger.Error("support: failed to create diagnostics file in archive", "error", err)
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "create_diagnostics_file").
			Build()
	}
	if err := json.NewEncoder(diagnosticsFile).Encode(dump.Diagnostics); err != nil {
		serviceLogger.Error("support: failed to write diagnostics to archive", "error", err)
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "write_diagnostics").
			Build()
	}
	serviceLogger.Debug("support: collection diagnostics added successfully")

	// Close the archive writer
	if err := w.Close(); err != nil {
		serviceLogger.Error("support: failed to close archive", "error", err)
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "close_archive").
			Context("archive_size", buf.Len()).
			Build()
	}

	archiveSize := buf.Len()
	serviceLogger.Info("support: archive created successfully",
		"dump_id", dump.ID,
		"archive_size", archiveSize)

	return buf.Bytes(), nil
}

// collectSystemInfo gathers system information
func (c *Collector) collectSystemInfo() SystemInfo {
	info := SystemInfo{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		GoVersion:    runtime.Version(),
		CPUCount:     runtime.NumCPU(),
		DiskInfo:     []DiskInfo{},
	}

	// Get system memory info using gopsutil
	memInfo, err := mem.VirtualMemory()
	if err == nil {
		info.MemoryMB = memInfo.Total / 1024 / 1024
	} else {
		// Fallback to runtime memory stats if gopsutil fails
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		info.MemoryMB = memStats.Sys / 1024 / 1024
	}

	// Collect disk information
	partitions, err := disk.Partitions(false)
	if err == nil {
		for _, partition := range partitions {
			usage, err := disk.Usage(partition.Mountpoint)
			if err != nil {
				continue
			}

			// Skip very small filesystems (like /dev, /sys, etc.)
			if usage.Total < 1024*1024*100 { // Less than 100MB
				continue
			}

			diskInfo := DiskInfo{
				Mountpoint: partition.Mountpoint,
				Total:      usage.Total,
				Used:       usage.Used,
				Free:       usage.Free,
				UsagePerc:  usage.UsedPercent,
			}
			info.DiskInfo = append(info.DiskInfo, diskInfo)
		}
	}

	// Check if running in Docker
	if _, err := os.Stat("/.dockerenv"); err == nil {
		info.DockerInfo = &DockerInfo{}
		// Try to get container ID
		if data, err := os.ReadFile("/proc/self/cgroup"); err == nil {
			for line := range strings.SplitSeq(string(data), "\n") {
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
	scrubbed := make(map[string]any)
	for k, v := range config {
		scrubbed[k] = c.scrubValue(k, v, c.sensitiveKeys)
	}

	return scrubbed
}

// scrubValue recursively scrubs sensitive values
func (c *Collector) scrubValue(key string, value any, sensitiveKeys []string) any {
	// Check if key is sensitive - if so, completely redact it
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
	case string:
		// Sanitize any RTSP URLs found in string values to remove credentials
		// This preserves URL structure while removing sensitive authentication info
		return privacy.SanitizeRTSPUrls(v)
	default:
		return value
	}
}

// collectLogs collects recent log entries and captures detailed diagnostic information.
// This method NEVER returns an error - it always returns logs (even empty) with diagnostics.
//
// Collection strategy:
//  1. First attempts journal logs (systemd) - may fail on Docker/non-systemd systems
//  2. Then attempts file logs from known paths - gracefully handles missing/inaccessible paths
//  3. Combines and sorts all logs by timestamp
//  4. Populates comprehensive diagnostics about what was attempted and what failed
//
// The diagnostics parameter is populated with detailed information about:
//   - Which log sources were attempted and their success/failure states
//   - Paths that were searched and their accessibility status
//   - Error messages for any failures
//   - Summary statistics about collected logs
func (c *Collector) collectLogs(ctx context.Context, duration time.Duration, maxSize int64, anonymizePII bool, diagnostics *LogCollectionDiagnostics) []LogEntry {
	serviceLogger.Debug("support: collectLogs started",
		"duration", duration,
		"maxSize", maxSize,
		"anonymizePII", anonymizePII)

	var logs []LogEntry
	var totalSize int64

	// Try to collect from journald first (systemd systems)
	// Skip journal collection if running in Docker as it's unlikely to be available
	if isRunningInDocker() {
		serviceLogger.Debug("support: running in Docker, skipping journal log collection")
		diagnostics.JournalLogs.Attempted = false
		diagnostics.JournalLogs.Details = map[string]any{
			"skipped_reason": "running_in_docker",
		}
	} else {
		serviceLogger.Debug("support: attempting to collect journal logs")
		diagnostics.JournalLogs.Attempted = true
		journalLogs, err := c.collectJournalLogs(ctx, duration, anonymizePII, &diagnostics.JournalLogs)
		if err != nil {
			diagnostics.JournalLogs.Error = err.Error()
			if errors.Is(err, ErrJournalNotAvailable) {
				serviceLogger.Debug("support: journal logs not available (expected on non-systemd systems)")
			} else {
				serviceLogger.Warn("support: error collecting journal logs", "error", err)
			}
		} else {
			serviceLogger.Debug("support: collected journal logs", "count", len(journalLogs))
			diagnostics.JournalLogs.Successful = true
			diagnostics.JournalLogs.EntriesFound = len(journalLogs)
			logs = append(logs, journalLogs...)
			// Estimate size for journal logs
			for _, entry := range journalLogs {
				totalSize += int64(len(entry.Message))
			}
		}
	}

	// Also check for log files in the data directory
	serviceLogger.Debug("support: attempting to collect log files")
	diagnostics.FileLogs.Attempted = true
	logFiles, fileSize, err := c.collectLogFilesWithDiagnostics(duration, maxSize-totalSize, anonymizePII, &diagnostics.FileLogs)
	if err != nil {
		diagnostics.FileLogs.Error = err.Error()
		serviceLogger.Warn("support: error collecting log files", "error", err)
	} else {
		serviceLogger.Debug("support: collected log files", "count", len(logFiles))
		diagnostics.FileLogs.Successful = true
		diagnostics.FileLogs.EntriesFound = len(logFiles)
		logs = append(logs, logFiles...)
		totalSize += fileSize
	}

	// Sort logs by timestamp
	serviceLogger.Debug("support: sorting logs by timestamp", "total_logs", len(logs))
	sortLogsByTime(logs)

	// Update summary diagnostics
	diagnostics.Summary.TotalEntries = len(logs)
	diagnostics.Summary.SizeBytes = totalSize
	diagnostics.Summary.TruncatedBySize = totalSize >= maxSize
	diagnostics.Summary.TruncatedByTime = duration > 0 // Always filtered by time if duration is specified

	// Set the actual time range of collected logs
	if len(logs) > 0 {
		diagnostics.Summary.TimeRange.From = logs[0].Timestamp
		diagnostics.Summary.TimeRange.To = logs[len(logs)-1].Timestamp
	}

	serviceLogger.Debug("support: collectLogs completed", "total_logs", len(logs))
	return logs
}

// collectJournalLogs collects logs from systemd journal with diagnostics
func (c *Collector) collectJournalLogs(ctx context.Context, duration time.Duration, anonymizePII bool, diagnostics *LogSourceDiagnostics) ([]LogEntry, error) {
	// Calculate since time
	since := time.Now().Add(-duration).Format("2006-01-02 15:04:05")

	// Limit to 5000 most recent lines to prevent timeout
	const maxJournalLines = 5000
	serviceLogger.Debug("support: running journalctl",
		"since", since,
		"maxLines", maxJournalLines)

	// Run journalctl command with line limit
	cmd := exec.CommandContext(ctx, "journalctl",
		"-u", "birdnet-go.service",
		"--since", since,
		"--no-pager",
		"-o", "json",
		"--no-hostname",
		"-n", fmt.Sprintf("%d", maxJournalLines)) // Limit number of lines

	// Capture command for diagnostics
	diagnostics.Command = cmd.String()
	diagnostics.Details["service"] = "birdnet-go.service"
	diagnostics.Details["since"] = since
	diagnostics.Details["max_lines"] = maxJournalLines
	diagnostics.Details["output_format"] = "json"

	output, err := cmd.Output()
	if err != nil {
		// journalctl might not be available or service might not exist
		// This is not a fatal error, just means no journald logs available
		serviceLogger.Debug("journalctl unavailable or service not found", "error", err)
		diagnostics.Details["error_type"] = "command_failed"
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			diagnostics.Details["exit_code"] = exitErr.ExitCode()
			diagnostics.Details["stderr"] = string(exitErr.Stderr)
		}
		return nil, ErrJournalNotAvailable
	}

	serviceLogger.Debug("support: journalctl output received", "size", len(output))

	// Parse JSON output line by line
	lines := strings.Split(string(output), "\n")
	serviceLogger.Debug("support: parsing journal entries", "lineCount", len(lines))

	// Pre-allocate logs slice based on number of lines
	logs := make([]LogEntry, 0, len(lines))
	parsedCount := 0
	skippedCount := 0

	for i, line := range lines {
		if line == "" {
			continue
		}

		// Log progress every 1000 lines
		if i > 0 && i%1000 == 0 {
			serviceLogger.Debug("support: journal parsing progress",
				"processed", i,
				"total", len(lines),
				"parsed", parsedCount,
				"skipped", skippedCount)
		}

		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Skip malformed JSON lines silently
			skippedCount++
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

		// Apply anonymization if requested
		if anonymizePII {
			message = privacy.ScrubMessage(message)
		}

		logs = append(logs, LogEntry{
			Timestamp: ts,
			Level:     level,
			Message:   message,
			Source:    "journald",
		})
		parsedCount++
	}

	serviceLogger.Debug("support: journal parsing completed",
		"totalLines", len(lines),
		"parsedEntries", parsedCount,
		"skippedEntries", skippedCount,
		"resultCount", len(logs))

	// Add parsing diagnostics
	diagnostics.Details["output_size_bytes"] = len(output)
	diagnostics.Details["total_lines"] = len(lines)
	diagnostics.Details["parsed_entries"] = parsedCount
	diagnostics.Details["skipped_entries"] = skippedCount
	diagnostics.Details["final_log_count"] = len(logs)

	return logs, nil
}

// collectLogFilesWithDiagnostics collects logs from files with detailed diagnostic information.
// This method searches multiple predefined paths for log files and captures diagnostics
// about each path searched, including:
//   - Whether the path exists
//   - Whether it's accessible (permission issues)
//   - How many log files were found
//   - Any errors encountered
//
// The method never fails completely - it will return whatever logs it can collect,
// along with diagnostic information about any issues encountered.
//
// Note: The Details map in diagnostics may be accessed concurrently if this method
// is called from multiple goroutines. Currently this is not the case, but if that
// changes in the future, synchronization should be added.
func (c *Collector) collectLogFilesWithDiagnostics(duration time.Duration, maxSize int64, anonymizePII bool, diagnostics *LogSourceDiagnostics) ([]LogEntry, int64, error) {
	var logs []LogEntry
	cutoffTime := time.Now().Add(-duration)
	totalSize := int64(0)

	// Get unique log paths and capture diagnostic information for each
	// These paths include both configured paths and common default locations
	uniquePaths := c.getUniqueLogPaths()
	// TODO: Add mutex protection if this method becomes concurrent
	diagnostics.Details["total_paths_to_search"] = len(uniquePaths)

	for _, logPath := range uniquePaths {
		searchedPath := SearchedPath{Path: logPath}

		// Check if path exists
		info, err := os.Stat(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				searchedPath.Exists = false
				searchedPath.Error = "path does not exist"
			} else {
				searchedPath.Exists = true
				searchedPath.Accessible = false
				searchedPath.Error = err.Error()
			}
			diagnostics.PathsSearched = append(diagnostics.PathsSearched, searchedPath)
			continue
		}

		searchedPath.Exists = true
		searchedPath.Accessible = true

		// Process directory or file
		if info.IsDir() {
			logCount, dirSize, err := c.processLogDirectory(logPath, cutoffTime, maxSize-totalSize, anonymizePII, &logs, &searchedPath)
			if err != nil {
				// Enhanced error context for directory processing failures
				if os.IsPermission(err) {
					searchedPath.Error = fmt.Sprintf("permission denied: %v", err)
				} else {
					searchedPath.Error = err.Error()
				}
			} else {
				searchedPath.FileCount = logCount
				totalSize += dirSize
			}
		} else if strings.HasSuffix(strings.ToLower(logPath), "log") {
			// Single file
			fileLogs, size := c.parseLogFile(logPath, cutoffTime, maxSize-totalSize, anonymizePII)
			logs = append(logs, fileLogs...)
			searchedPath.FileCount = 1
			totalSize += size
		}

		diagnostics.PathsSearched = append(diagnostics.PathsSearched, searchedPath)

		if totalSize >= maxSize {
			diagnostics.Details["stopped_reason"] = "max_size_reached"
			break
		}
	}

	diagnostics.Details["files_processed"] = len(diagnostics.PathsSearched)
	diagnostics.Details["total_size_bytes"] = totalSize

	return logs, totalSize, nil
}

// processLogDirectory processes all log files in a directory and returns count and size
// processLogDirectory processes a directory for log files.
// Returns the number of log files processed, total size of logs, and any error encountered.
// Errors are non-fatal and just mean some files couldn't be processed.
func (c *Collector) processLogDirectory(dirPath string, cutoffTime time.Time, maxSize int64, anonymizePII bool, logs *[]LogEntry, searchedPath *SearchedPath) (logFileCount int, totalSize int64, err error) {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return 0, 0, err
	}

	for _, file := range files {
		if totalSize >= maxSize {
			break
		}

		filename := file.Name()
		if !strings.HasSuffix(strings.ToLower(filename), "log") {
			continue
		}

		logFileCount++
		fullPath := filepath.Join(dirPath, filename)
		fileLogs, size := c.parseLogFile(fullPath, cutoffTime, maxSize-totalSize, anonymizePII)
		*logs = append(*logs, fileLogs...)
		totalSize += size
	}

	return logFileCount, totalSize, nil
}

// Legacy collectLogFiles method - keeping for compatibility but using new implementation
func (c *Collector) collectLogFiles(duration time.Duration, maxSize int64, anonymizePII bool) ([]LogEntry, error) {
	// Use the new diagnostics-enabled method but discard the diagnostic info
	diagnostics := &LogSourceDiagnostics{PathsSearched: []SearchedPath{}, Details: make(map[string]any)}
	logs, _, err := c.collectLogFilesWithDiagnostics(duration, maxSize, anonymizePII, diagnostics)
	return logs, err
}

// parseLogFile parses a log file and extracts entries
func (c *Collector) parseLogFile(path string, cutoffTime time.Time, maxSize int64, anonymizePII bool) (logs []LogEntry, totalSize int64) {

	file, err := os.Open(path)
	if err != nil {
		// Log file might not exist or be inaccessible, which is fine
		return logs, 0
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("Failed to close file: %v", err)
		}
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		totalSize += int64(len(line))

		if totalSize > maxSize {
			break
		}

		// Simple log parsing - adjust based on actual log format
		entry := c.parseLogLine(line, anonymizePII)
		if entry != nil && entry.Timestamp.After(cutoffTime) {
			logs = append(logs, *entry)
		}
	}

	return logs, totalSize
}

// parseLogLine parses a single log line
func (c *Collector) parseLogLine(line string, anonymizePII bool) *LogEntry {
	// First try to parse as JSON (from slog)
	var jsonLog map[string]any
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

		// Parse message and anonymize if requested
		if msg, ok := jsonLog["msg"].(string); ok {
			if anonymizePII {
				entry.Message = privacy.ScrubMessage(msg)
			} else {
				entry.Message = msg
			}
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
	// IMPORTANT: Database log entries use local time strings, parse as local time
	timestamp, err := time.ParseInLocation("2006-01-02 15:04:05", parts[0]+" "+parts[1], time.Local)
	if err != nil {
		return nil
	}

	// Extract level
	level := strings.Trim(parts[2], "[]")

	message := parts[3]
	if anonymizePII {
		message = privacy.ScrubMessage(message)
	}

	return &LogEntry{
		Timestamp: timestamp,
		Level:     level,
		Message:   message,
		Source:    "file",
	}
}

// sortLogsByTime sorts log entries by timestamp
func sortLogsByTime(logs []LogEntry) {
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].Timestamp.Before(logs[j].Timestamp)
	})
}

// addConfigToArchive adds the config file to the archive in YAML format with scrubbing
func (c *Collector) addConfigToArchive(w *zip.Writer, scrubSensitive bool) error {
	configPath := filepath.Join(c.configPath, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "read_config_for_archive").
			Context("config_path", configPath).
			Build()
	}

	// If scrubbing is enabled, parse and re-serialize the config
	if scrubSensitive {
		var config map[string]any
		if err := yaml.Unmarshal(data, &config); err != nil {
			return errors.New(err).
				Component("support").
				Category(errors.CategoryConfiguration).
				Context("operation", "parse_config_for_scrubbing").
				Build()
		}

		// Scrub sensitive data
		config = c.scrubConfig(config)

		// Re-serialize to YAML
		scrubbedData, err := yaml.Marshal(config)
		if err != nil {
			return errors.New(err).
				Component("support").
				Category(errors.CategoryConfiguration).
				Context("operation", "marshal_scrubbed_config").
				Build()
		}
		data = scrubbedData
	}

	// Add to archive
	configFile, err := w.Create("config.yaml")
	if err != nil {
		return errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "create_config_in_archive").
			Build()
	}

	if _, err := configFile.Write(data); err != nil {
		return errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "write_config_to_archive").
			Build()
	}

	return nil
}

// logFileCollector encapsulates the state for collecting log files
type logFileCollector struct {
	ctx          context.Context
	collector    *Collector
	cutoffTime   time.Time
	maxSize      int64
	totalSize    int64
	logsAdded    int
	anonymizePII bool
}

// addLogFilesToArchive adds log files to the archive in their original format
func (c *Collector) addLogFilesToArchive(ctx context.Context, w *zip.Writer, duration time.Duration, maxSize int64, anonymizePII bool) error {
	serviceLogger.Debug("support: addLogFilesToArchive started",
		"duration", duration,
		"maxSize", maxSize,
		"anonymizePII", anonymizePII)

	lfc := &logFileCollector{
		ctx:          ctx,
		collector:    c,
		cutoffTime:   time.Now().Add(-duration),
		maxSize:      maxSize,
		totalSize:    0,
		logsAdded:    0,
		anonymizePII: anonymizePII,
	}

	// Get unique log paths
	uniquePaths := c.getUniqueLogPaths()
	serviceLogger.Debug("support: unique log paths identified", "count", len(uniquePaths), "paths", uniquePaths)

	// Process each log path
	for _, logPath := range uniquePaths {
		if lfc.totalSize >= lfc.maxSize {
			serviceLogger.Debug("support: stopping log collection - max size reached",
				"totalSize", lfc.totalSize,
				"maxSize", lfc.maxSize)
			break
		}

		serviceLogger.Debug("support: processing log path for archive", "path", logPath)
		if err := lfc.processLogPath(w, logPath); err != nil {
			serviceLogger.Warn("support: error processing log path", "path", logPath, "error", err)
			// Continue with next path on error
			continue
		}
	}

	// Add journald logs
	serviceLogger.Debug("support: attempting to add journald logs to archive")
	if err := lfc.addJournaldLogs(w, duration); err == nil {
		lfc.logsAdded++
		serviceLogger.Debug("support: journald logs added successfully")
	} else {
		serviceLogger.Debug("support: could not add journald logs", "error", err)
	}

	// Add README if no logs found
	if lfc.logsAdded == 0 {
		serviceLogger.Debug("support: no logs found, adding README note")
		lfc.addNoLogsNote(w)
	} else {
		serviceLogger.Debug("support: logs added to archive", "count", lfc.logsAdded, "totalSize", lfc.totalSize)
	}

	return nil
}

// getUniqueLogPaths returns deduplicated list of log paths to check
func (c *Collector) getUniqueLogPaths() []string {
	logPaths := c.getLogSearchPaths()

	seen := make(map[string]bool)
	uniquePaths := []string{}

	for _, path := range logPaths {
		if absPath, err := filepath.Abs(path); err == nil {
			if !seen[absPath] {
				seen[absPath] = true
				uniquePaths = append(uniquePaths, absPath)
			}
		}
	}

	return uniquePaths
}

// getLogSearchPaths returns all paths where logs might be located
func (c *Collector) getLogSearchPaths() []string {
	var paths []string

	// Validate and add paths safely
	addPathIfValid := func(basePath, subPath string) {
		// Ensure basePath is not empty and doesn't contain path traversal attempts
		if basePath == "" || strings.Contains(basePath, "..") {
			return
		}

		// Clean the path to resolve any relative components
		cleanBase := filepath.Clean(basePath)
		fullPath := filepath.Join(cleanBase, subPath)

		// Ensure the final path doesn't escape the base directory
		if absBase, err := filepath.Abs(cleanBase); err == nil {
			if absPath, err := filepath.Abs(fullPath); err == nil {
				// Verify the absolute path starts with the absolute base path
				if strings.HasPrefix(absPath, absBase) {
					paths = append(paths, fullPath)
				}
			}
		}
	}

	// Add default logs directory
	paths = append(paths, "logs")

	// Add validated paths
	addPathIfValid(c.dataPath, "logs")
	addPathIfValid(c.configPath, "logs")

	// Add current working directory logs
	if cwd, err := os.Getwd(); err == nil {
		addPathIfValid(cwd, "logs")
	}

	return paths
}

// processLogPath processes a single log path (file or directory)
func (lfc *logFileCollector) processLogPath(w *zip.Writer, logPath string) error {
	info, err := os.Stat(logPath)
	if err != nil {
		// If the path doesn't exist, return a simple error (not enhanced)
		// This is expected during log path search and shouldn't create user notifications
		if os.IsNotExist(err) {
			return err // Return simple error for non-existent paths
		}
		// For other errors (permission, etc.), create enhanced error
		return errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "stat_log_path").
			Context("path", logPath).
			Build()
	}

	if info.IsDir() {
		return lfc.processLogDirectory(w, logPath)
	}

	// Process single log file
	return lfc.processSingleLogFile(w, logPath, info)
}

// processLogDirectory processes all log files in a directory
func (lfc *logFileCollector) processLogDirectory(w *zip.Writer, dirPath string) error {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "read_log_directory").
			Context("path", dirPath).
			Build()
	}

	for _, file := range files {
		if lfc.totalSize >= lfc.maxSize {
			break
		}

		if !lfc.isLogFile(file.Name()) {
			continue
		}

		if err := lfc.processLogFileEntry(w, dirPath, file); err != nil {
			// Continue with next file on error
			continue
		}
	}

	return nil
}

// processLogFileEntry processes a single log file entry from a directory
func (lfc *logFileCollector) processLogFileEntry(w *zip.Writer, dirPath string, file os.DirEntry) error {
	filePath := filepath.Join(dirPath, file.Name())
	fileInfo, err := file.Info()
	if err != nil {
		return errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "get_file_info").
			Context("file", file.Name()).
			Build()
	}

	// Check if file is within time range
	if !lfc.isFileWithinTimeRange(fileInfo) {
		return nil
	}

	// Check if adding file would exceed size limit
	if !lfc.canAddFile(fileInfo.Size()) {
		return nil
	}

	// Add file to archive with optional anonymization
	archivePath := filepath.Join("logs", file.Name())
	if err := lfc.collector.addLogFileToArchive(w, filePath, archivePath, lfc.anonymizePII); err != nil {
		return errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "add_log_to_archive").
			Context("file", file.Name()).
			Build()
	}

	lfc.totalSize += fileInfo.Size()
	lfc.logsAdded++
	return nil
}

// processSingleLogFile processes a single log file (not in a directory)
func (lfc *logFileCollector) processSingleLogFile(w *zip.Writer, logPath string, info os.FileInfo) error {
	if !lfc.canAddFile(info.Size()) {
		return nil
	}

	archivePath := filepath.Join("logs", filepath.Base(logPath))
	if err := lfc.collector.addLogFileToArchive(w, logPath, archivePath, lfc.anonymizePII); err != nil {
		return errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "add_single_log_to_archive").
			Context("file", logPath).
			Build()
	}

	lfc.totalSize += info.Size()
	lfc.logsAdded++
	return nil
}

// isLogFile checks if a file is a log file based on its suffix
func (lfc *logFileCollector) isLogFile(filename string) bool {
	return strings.HasSuffix(strings.ToLower(filename), "log")
}

// isFileWithinTimeRange checks if file modification time is within the collection range
func (lfc *logFileCollector) isFileWithinTimeRange(info os.FileInfo) bool {
	return !info.ModTime().Before(lfc.cutoffTime)
}

// canAddFile checks if a file can be added without exceeding size limit
func (lfc *logFileCollector) canAddFile(fileSize int64) bool {
	return lfc.totalSize+fileSize <= lfc.maxSize
}

// addJournaldLogs adds systemd journal logs to the archive
func (lfc *logFileCollector) addJournaldLogs(w *zip.Writer, duration time.Duration) error {
	journalLogs := lfc.collector.getJournaldLogs(lfc.ctx, duration)
	if journalLogs == "" {
		return errors.Newf("no journald logs available").
			Component("support").
			Category(errors.CategorySystem).
			Context("operation", "get_journald_logs").
			Build()
	}

	// Apply anonymization if requested
	if lfc.anonymizePII {
		lines := strings.Split(journalLogs, "\n")
		anonymizedLines := make([]string, len(lines))
		for i, line := range lines {
			anonymizedLines[i] = privacy.ScrubMessage(line)
		}
		journalLogs = strings.Join(anonymizedLines, "\n")
	}

	journalFile, err := w.Create("logs/journald.log")
	if err != nil {
		return errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "create_journald_file").
			Build()
	}

	if _, err := journalFile.Write([]byte(journalLogs)); err != nil {
		return errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "write_journald_logs").
			Build()
	}

	return nil
}

// addNoLogsNote adds a README when no logs are found
func (lfc *logFileCollector) addNoLogsNote(w *zip.Writer) {
	noteFile, err := w.Create("logs/README.txt")
	if err != nil {
		// Non-critical error, don't propagate
		return
	}

	message := "No log files were found or all logs were older than the specified duration."
	_, _ = noteFile.Write([]byte(message))
}

// addFileToArchive adds a single file to the zip archive
func (c *Collector) addFileToArchive(w *zip.Writer, sourcePath, archivePath string) error {
	file, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("Failed to close file: %v", err)
		}
	}()

	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(fileInfo)
	if err != nil {
		return err
	}
	header.Name = archivePath
	header.Method = zip.Deflate

	writer, err := w.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, file)
	return err
}

// addLogFileToArchive adds a log file to the zip archive with optional anonymization
func (c *Collector) addLogFileToArchive(w *zip.Writer, sourcePath, archivePath string, anonymizePII bool) error {
	if !anonymizePII {
		// If no anonymization needed, use the regular file copy
		return c.addFileToArchive(w, sourcePath, archivePath)
	}

	// Read the file content for anonymization
	file, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("Failed to close file: %v", err)
		}
	}()

	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(fileInfo)
	if err != nil {
		return err
	}
	header.Name = archivePath
	header.Method = zip.Deflate

	writer, err := w.CreateHeader(header)
	if err != nil {
		return err
	}

	// Process the file line by line for anonymization
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		anonymizedLine := privacy.ScrubMessage(line)

		if _, err := writer.Write([]byte(anonymizedLine + "\n")); err != nil {
			return err
		}
	}

	return scanner.Err()
}

// getJournaldLogs retrieves logs from journald as a string
func (c *Collector) getJournaldLogs(ctx context.Context, duration time.Duration) string {
	since := time.Now().Add(-duration).Format("2006-01-02 15:04:05")

	// Use same line limit as collectJournalLogs
	const maxJournalLines = 5000

	cmd := exec.CommandContext(ctx, "journalctl",
		"-u", "birdnet-go.service",
		"--since", since,
		"--no-pager",
		"-n", fmt.Sprintf("%d", maxJournalLines)) // Limit number of lines

	output, err := cmd.Output()
	if err != nil {
		serviceLogger.Debug("support: getJournaldLogs failed", "error", err)
		return ""
	}

	serviceLogger.Debug("support: getJournaldLogs retrieved", "size", len(output))
	return string(output)
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
