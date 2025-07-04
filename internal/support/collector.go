package support

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
		"id", "apikey", "username", "broker", "topic", "urls",
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
		dump.SystemInfo = c.collectSystemInfo()
		serviceLogger.Debug("support: system information collected",
			"os", dump.SystemInfo.OS,
			"arch", dump.SystemInfo.Architecture,
			"cpu_count", dump.SystemInfo.CPUCount,
			"memory_mb", dump.SystemInfo.MemoryMB)
	}

	// Collect configuration (scrubbed)
	if opts.IncludeConfig {
		serviceLogger.Debug("support: collecting configuration", "scrub_sensitive", opts.ScrubSensitive)
		config, err := c.collectConfig(opts.ScrubSensitive)
		if err != nil {
			serviceLogger.Error("support: failed to collect configuration", "error", err)
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryConfiguration).
				Context("operation", "collect_config").
				Context("scrub_sensitive", opts.ScrubSensitive).
				Build()
		}
		dump.Config = config
		serviceLogger.Debug("support: configuration collected successfully")
	}

	// Collect logs
	if opts.IncludeLogs {
		serviceLogger.Debug("support: collecting logs", "duration", opts.LogDuration, "max_size", opts.MaxLogSize, "anonymize_pii", opts.AnonymizePII)
		logs, err := c.collectLogs(ctx, opts.LogDuration, opts.MaxLogSize, opts.AnonymizePII)
		if err != nil {
			serviceLogger.Error("support: failed to collect logs", "error", err)
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "collect_logs").
				Context("log_duration", opts.LogDuration.String()).
				Context("max_log_size", opts.MaxLogSize).
				Context("anonymize_pii", opts.AnonymizePII).
				Build()
		}
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

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// Add metadata (keep this as JSON for easy parsing)
	serviceLogger.Debug("support: adding metadata to archive")
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
	metadata := map[string]interface{}{
		"id":        dump.ID,
		"timestamp": dump.Timestamp,
		"system_id": dump.SystemID,
		"version":   dump.Version,
		"log_count": len(dump.Logs),
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

	// Add log files in original format
	if opts.IncludeLogs {
		serviceLogger.Debug("support: adding log files to archive", "anonymize_pii", opts.AnonymizePII)
		if err := c.addLogFilesToArchive(ctx, w, opts.LogDuration, opts.MaxLogSize, opts.AnonymizePII); err != nil {
			serviceLogger.Error("support: failed to add log files to archive", "error", err)
			return nil, err
		}
	}

	// Add config file in original YAML format (scrubbed)
	if opts.IncludeConfig {
		serviceLogger.Debug("support: adding config file to archive", "scrub_sensitive", opts.ScrubSensitive)
		if err := c.addConfigToArchive(w, opts.ScrubSensitive); err != nil {
			serviceLogger.Error("support: failed to add config to archive", "error", err)
			return nil, err
		}
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
	}

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
func (c *Collector) collectLogs(ctx context.Context, duration time.Duration, maxSize int64, anonymizePII bool) ([]LogEntry, error) {
	var logs []LogEntry

	// Try to collect from journald first (systemd systems)
	journalLogs, err := c.collectJournalLogs(ctx, duration, anonymizePII)
	if err == nil && len(journalLogs) > 0 {
		logs = append(logs, journalLogs...)
	}

	// Also check for log files in the data directory
	logFiles, err := c.collectLogFiles(duration, maxSize, anonymizePII)
	if err == nil && len(logFiles) > 0 {
		logs = append(logs, logFiles...)
	}

	// Sort logs by timestamp
	sortLogsByTime(logs)

	return logs, nil
}

// collectJournalLogs collects logs from systemd journal
func (c *Collector) collectJournalLogs(ctx context.Context, duration time.Duration, anonymizePII bool) ([]LogEntry, error) {
	// Calculate since time
	since := time.Now().Add(-duration).Format("2006-01-02 15:04:05")

	// Run journalctl command
	cmd := exec.CommandContext(ctx, "journalctl",
		"-u", "birdnet-go.service",
		"--since", since,
		"--no-pager",
		"-o", "json",
		"--no-hostname")

	output, err := cmd.Output()
	if err != nil {
		// journalctl might not be available or service might not exist
		// This is not a fatal error, just means no journald logs available
		return nil, nil
	}

	// Parse JSON output line by line
	lines := strings.Split(string(output), "\n")
	// Pre-allocate logs slice based on number of lines
	logs := make([]LogEntry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}

		var entry map[string]any
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
	}

	return logs, nil
}

// collectLogFiles collects logs from files in the data directory
func (c *Collector) collectLogFiles(duration time.Duration, maxSize int64, anonymizePII bool) ([]LogEntry, error) {
	var logs []LogEntry

	// Look for log files in common locations
	// The logging package creates logs in a "logs" directory
	logPaths := []string{
		"logs",                              // Default logs directory from logging package
		filepath.Join(c.dataPath, "logs"),   // Data directory logs
		filepath.Join(c.configPath, "logs"), // Config directory logs
	}

	// Also try to find logs relative to current working directory
	if cwd, err := os.Getwd(); err == nil {
		logPaths = append(logPaths, filepath.Join(cwd, "logs"))
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
					fileLogs, size := c.parseLogFile(filepath.Join(logPath, file.Name()), cutoffTime, maxSize-totalSize, anonymizePII)
					logs = append(logs, fileLogs...)
					totalSize += size
					if totalSize >= maxSize {
						break
					}
				}
			}
		} else {
			// It's a file
			fileLogs, size := c.parseLogFile(logPath, cutoffTime, maxSize-totalSize, anonymizePII)
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
	timestamp, err := time.Parse("2006-01-02 15:04:05", parts[0]+" "+parts[1])
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

	// Process each log path
	for _, logPath := range uniquePaths {
		if lfc.totalSize >= lfc.maxSize {
			break
		}

		if err := lfc.processLogPath(w, logPath); err != nil {
			// Continue with next path on error
			continue
		}
	}

	// Add journald logs
	if err := lfc.addJournaldLogs(w, duration); err == nil {
		lfc.logsAdded++
	}

	// Add README if no logs found
	if lfc.logsAdded == 0 {
		lfc.addNoLogsNote(w)
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
	cmd := exec.CommandContext(ctx, "journalctl",
		"-u", "birdnet-go.service",
		"--since", since,
		"--no-pager")

	output, err := cmd.Output()
	if err != nil {
		return ""
	}

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
