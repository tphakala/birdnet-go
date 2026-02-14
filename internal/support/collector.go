package support

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
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
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"gopkg.in/yaml.v3"
)

// Sentinel errors for support collector operations
var (
	ErrJournalNotAvailable = errors.NewStd("journal logs not available")
)

// Constants for support collector operations
const (
	// Log collection limits and thresholds
	maxJournalLogLines      = 5000 // Maximum journal log lines to prevent timeout
	logProgressInterval     = 1000 // Report progress every N lines during parsing
	defaultMaxLogSizeMB     = 50   // Default maximum log size in MB
	defaultLogDurationWeeks = 4    // Default log collection duration in weeks

	// Memory and disk thresholds
	bytesPerMB                = 1024 * 1024
	minDiskSizeMB             = 100 // Skip disks smaller than 100MB
	microsecondsToNanoseconds = 1000

	// File permissions
	defaultDirPermissions  = 0o755
	defaultFilePermissions = 0o644

	// Container and system detection
	dockerEnvFile       = "/.dockerenv"
	procCGroupFile      = "/proc/1/cgroup"
	containerDocker     = "docker"
	containerContainerd = "containerd"

	// SystemD and journal constants
	systemdServiceName = "birdnet-go.service"
	journalTimeFormat  = "2006-01-02 15:04:05"
	logTimeFormat      = "2006-01-02 15:04:05"

	// Archive file names
	diagnosticsFileName = "collection_diagnostics.json"
	metadataFileName    = "metadata.json"
	configYAMLFileName  = "config.yaml"
	systemInfoFileName  = "system_info.json"
	logReadmeFileName   = "logs/README.txt"

	// Redaction and privacy
	redactedPlaceholder      = "[redacted]"
	redactedUserPlaceholder  = "[user]"
	redactedPassPlaceholder  = "[pass]"
	redactedHostPlaceholder  = "[host]"
	redactedPathPlaceholder  = "[path]"
	redactedQueryPlaceholder = "[query]"
	urlSchemeDelimiter       = "://"
	logReadmeContent         = "No log files were found or all logs were older than the specified duration."

	// Journal command flags
	journalFlagUnit       = "-u"
	journalFlagSince      = "--since"
	journalFlagNoPager    = "--no-pager"
	journalFlagOutput     = "-o"
	journalFlagJSON       = "json"
	journalFlagNoHostname = "--no-hostname"
	journalFlagLines      = "-n"

	// Journal priority levels (syslog severity)
	priorityEmergency = "0" // System is unusable
	priorityAlert     = "1" // Action must be taken immediately
	priorityCritical  = "2" // Critical conditions
	priorityError     = "3" // Error conditions
	priorityWarning   = "4" // Warning conditions
	priorityNotice    = "5" // Normal but significant condition
	priorityInfo      = "6" // Informational messages
	priorityDebug     = "7" // Debug-level messages

	// Exit codes for error context
	exitCodeGeneralFailure  = 1
	exitCodeCommandNotFound = 127
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
	if _, err := os.Stat(dockerEnvFile); err == nil {
		return true
	}

	// Check cgroup for docker references
	if data, err := os.ReadFile(procCGroupFile); err == nil {
		if strings.Contains(string(data), containerDocker) || strings.Contains(string(data), containerContainerd) {
			return true
		}
	}

	return false
}

// getLogger returns the support package logger.
// Fetched dynamically to ensure it uses the current centralized logger.
func getLogger() logger.Logger {
	return logger.Global().Module("support")
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
		// Authentication credentials (snake_case and camelCase variants)
		"password", "pass", "token", "secret", "key", "api_key", "api_token",
		"client_id", "client_secret", "apikey", "apitoken",
		"clientid", "clientsecret", // camelCase variants
		"accesskeyid", "secretaccesskey", "encryptionkey",
		"sessionsecret", // session secret (camelCase)

		// MQTT credentials
		"mqtt_password", "mqtt_username", "mqtt_topic", "broker", "topic",

		// Service identifiers (including generic "id" for nested identifiers like birdweather.id)
		"birdweather_id", "birdweatherid", "username", "user", "userid", "id",

		// Location data (privacy sensitive)
		"latitude", "longitude", "stationid",

		// URLs (handled specially for structural redaction)
		"url", "urls", "endpoint", "webhook_url",

		// Network topology
		"subnet",

		// Secret file paths
		"privatekeypath", "sshkeypath", "credentialspath",
		"tokenfile", "passfile", "userfile", "valuefile",
	}
}

// isURLValue checks if a string value appears to be a URL
func isURLValue(s string) bool {
	return strings.Contains(s, urlSchemeDelimiter)
}

// isSensitiveKey checks if a key matches a sensitive key pattern using word boundaries.
// The sensitive pattern must appear as a complete word within the key:
//   - At start: "password", "password1", "password_hash" match "password"
//   - After underscore: "mqtt_password", "api_key" match their suffix
//
// Prevents false positives where sensitive patterns appear mid-word:
//   - "monkey" does NOT match "key" (no word boundary before "key")
//   - "tokenizer" does NOT match "token" (letter follows "token")
//   - "passwordhash" does NOT match "password" (letter follows)
func isSensitiveKey(lowerKey, sensitive string) bool {
	idx := strings.Index(lowerKey, sensitive)
	if idx == -1 {
		return false
	}

	endIdx := idx + len(sensitive)

	// Start boundary: at start of string OR preceded by underscore
	validStart := idx == 0 || lowerKey[idx-1] == '_'
	if !validStart {
		return false
	}

	// End boundary: at end of string OR followed by underscore/digit
	// Allows: password, mqtt_password, password_hash, password1
	// Rejects: passwordhash, tokenizer (letter follows)
	if endIdx == len(lowerKey) {
		return true
	}
	nextChar := lowerKey[endIdx]
	return nextChar == '_' || (nextChar >= '0' && nextChar <= '9')
}

// isDefaultValue checks if a value is a default/empty value that doesn't need redaction
func isDefaultValue(value any) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case string:
		return v == ""
	case float64:
		return v == 0.0
	case float32:
		return v == 0.0
	case int:
		return v == 0
	case int64:
		return v == 0
	case int32:
		return v == 0
	case bool:
		return false // booleans are never "default" for redaction purposes
	default:
		return false
	}
}

// redactURLStructurally preserves URL structure while replacing sensitive components.
// It keeps the scheme and port (useful for debugging) while replacing credentials,
// host, path, and query with placeholders.
func redactURLStructurally(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" {
		// url.Parse is lenient - empty scheme means it wasn't a proper URL
		return redactedPlaceholder
	}

	var result strings.Builder

	// Preserve scheme
	result.WriteString(parsed.Scheme)
	result.WriteString(urlSchemeDelimiter)

	// Handle credentials
	if u := parsed.User; u != nil {
		username := u.Username()
		_, hasPass := u.Password()

		// Only add user/pass placeholders if there's something to redact
		if username != "" || hasPass {
			result.WriteString(redactedUserPlaceholder)
			if hasPass {
				result.WriteString(":")
				result.WriteString(redactedPassPlaceholder)
			}
			result.WriteString("@")
		}
	}

	// Replace host
	result.WriteString(redactedHostPlaceholder)

	// Preserve port if present
	if parsed.Port() != "" {
		result.WriteString(":")
		result.WriteString(parsed.Port())
	}

	// Replace path
	if parsed.Path != "" && parsed.Path != "/" {
		result.WriteString("/")
		result.WriteString(redactedPathPlaceholder)
	}

	// Replace query
	if parsed.RawQuery != "" {
		result.WriteString("?")
		result.WriteString(redactedQueryPlaceholder)
	}

	return result.String()
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
		getLogger().Error("support: collection validation failed: at least one data type must be included")
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

	getLogger().Info("support: starting collection",
		logger.String("dump_id", dump.ID),
		logger.Bool("include_logs", opts.IncludeLogs),
		logger.Bool("include_config", opts.IncludeConfig),
		logger.Bool("include_system_info", opts.IncludeSystemInfo),
		logger.Duration("log_duration", opts.LogDuration),
		logger.Int64("max_log_size", opts.MaxLogSize))

	// Collect system information
	if opts.IncludeSystemInfo {
		getLogger().Debug("support: collecting system information")
		dump.Diagnostics.SystemCollection.Attempted = true
		dump.SystemInfo = c.collectSystemInfo()
		dump.Diagnostics.SystemCollection.Successful = true
		getLogger().Debug("support: system information collected",
			logger.String("os", dump.SystemInfo.OS),
			logger.String("arch", dump.SystemInfo.Architecture),
			logger.Int("cpu_count", dump.SystemInfo.CPUCount),
			logger.Any("memory_mb", dump.SystemInfo.MemoryMB))
	}

	// Collect configuration (scrubbed)
	if opts.IncludeConfig {
		getLogger().Debug("support: collecting configuration", logger.Bool("scrub_sensitive", opts.ScrubSensitive))
		dump.Diagnostics.ConfigCollection.Attempted = true
		config, err := c.collectConfig(opts.ScrubSensitive)
		if err != nil {
			getLogger().Error("support: failed to collect configuration", logger.Error(err))
			dump.Diagnostics.ConfigCollection.Error = err.Error()
			// Don't fail the entire collection - continue with other data
		} else {
			dump.Config = config
			dump.Diagnostics.ConfigCollection.Successful = true
			getLogger().Debug("support: configuration collected successfully")
		}
	}

	// Collect logs
	if opts.IncludeLogs {
		getLogger().Debug("support: collecting logs", logger.Duration("duration", opts.LogDuration), logger.Int64("max_size", opts.MaxLogSize), logger.Bool("anonymize_pii", opts.AnonymizePII))
		logs := c.collectLogs(ctx, opts.LogDuration, opts.MaxLogSize, opts.AnonymizePII, &dump.Diagnostics.LogCollection)
		dump.Logs = logs
		getLogger().Debug("support: logs collected", logger.Int("log_count", len(logs)))
	}

	getLogger().Info("support: collection completed successfully",
		logger.String("dump_id", dump.ID),
		logger.Int("log_count", len(dump.Logs)))

	return dump, nil
}

// CreateArchive creates a zip archive containing the support dump
func (c *Collector) CreateArchive(ctx context.Context, dump *SupportDump, opts CollectorOptions) ([]byte, error) {
	getLogger().Info("support: creating archive", logger.String("dump_id", dump.ID))

	// Check context early
	select {
	case <-ctx.Done():
		getLogger().Warn("support: context cancelled before archive creation", logger.Error(ctx.Err()))
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
	getLogger().Debug("support: adding metadata to archive", logger.String("dump_id", dump.ID))
	metadataFile, err := w.Create(metadataFileName)
	if err != nil {
		getLogger().Error("support: failed to create metadata file in archive", logger.Error(err))
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
		getLogger().Error("support: failed to write metadata to archive", logger.Error(err))
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
		getLogger().Warn("support: context cancelled before adding logs", logger.Error(ctx.Err()))
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
		getLogger().Debug("support: adding log files to archive", logger.Bool("anonymize_pii", opts.AnonymizePII))
		if err := c.addLogFilesToArchive(ctx, w, opts.LogDuration, opts.MaxLogSize, opts.AnonymizePII); err != nil {
			getLogger().Error("support: failed to add log files to archive", logger.Error(err))
			return nil, err
		}
		getLogger().Debug("support: log files added successfully")
	}

	// Add config file in original YAML format (scrubbed)
	if opts.IncludeConfig {
		getLogger().Debug("support: adding config file to archive", logger.Bool("scrub_sensitive", opts.ScrubSensitive))
		if err := c.addConfigToArchive(w, opts.ScrubSensitive); err != nil {
			getLogger().Error("support: failed to add config to archive", logger.Error(err))
			return nil, err
		}
		getLogger().Debug("support: config file added successfully")
	}

	// Add system info
	if opts.IncludeSystemInfo {
		getLogger().Debug("support: adding system info to archive")
		sysInfoFile, err := w.Create(systemInfoFileName)
		if err != nil {
			getLogger().Error("support: failed to create system info file in archive", logger.Error(err))
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "create_system_info_file").
				Build()
		}
		if err := json.NewEncoder(sysInfoFile).Encode(dump.SystemInfo); err != nil {
			getLogger().Error("support: failed to write system info to archive", logger.Error(err))
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "write_system_info").
				Build()
		}
		getLogger().Debug("support: system info added successfully")
	}

	// Always add diagnostics - this is crucial for troubleshooting collection issues
	getLogger().Debug("support: adding collection diagnostics to archive")
	diagnosticsFile, err := w.Create(diagnosticsFileName)
	if err != nil {
		getLogger().Error("support: failed to create diagnostics file in archive", logger.Error(err))
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "create_diagnostics_file").
			Build()
	}
	if err := json.NewEncoder(diagnosticsFile).Encode(dump.Diagnostics); err != nil {
		getLogger().Error("support: failed to write diagnostics to archive", logger.Error(err))
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "write_diagnostics").
			Build()
	}
	getLogger().Debug("support: collection diagnostics added successfully")

	// Close the archive writer
	if err := w.Close(); err != nil {
		getLogger().Error("support: failed to close archive", logger.Error(err))
		return nil, errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Context("operation", "close_archive").
			Context("archive_size", buf.Len()).
			Build()
	}

	archiveSize := buf.Len()
	getLogger().Info("support: archive created successfully",
		logger.String("dump_id", dump.ID),
		logger.Int("archive_size", archiveSize))

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
		info.MemoryMB = memInfo.Total / bytesPerMB
	} else {
		// Fallback to runtime memory stats if gopsutil fails
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		info.MemoryMB = memStats.Sys / bytesPerMB
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
			if usage.Total < minDiskSizeMB*bytesPerMB { // Less than 100MB
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
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: configPath is from c.configPath (application config directory)
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

// scrubValue recursively scrubs sensitive values.
// For sensitive keys:
//   - Default/empty values are left unchanged
//   - URL values get structural redaction (preserving scheme and port)
//   - Other values get replaced with [redacted]
//
// For non-sensitive keys, it recursively processes nested structures
// and sanitizes RTSP URLs in string values.
func (c *Collector) scrubValue(key string, value any, sensitiveKeys []string) any {
	// Check if key is sensitive using word boundary matching
	lowerKey := strings.ToLower(key)
	isSensitive := false
	for _, sensitive := range sensitiveKeys {
		if isSensitiveKey(lowerKey, sensitive) {
			isSensitive = true
			break
		}
	}

	if isSensitive {
		return c.redactSensitiveValue(value, sensitiveKeys)
	}

	// Recursively process nested structures
	return c.processNonSensitiveValue(key, value, sensitiveKeys)
}

// scrubMap recursively processes a map, scrubbing each key-value pair.
func (c *Collector) scrubMap(m map[string]any, sensitiveKeys []string) map[string]any {
	scrubbed := make(map[string]any, len(m))
	for k, val := range m {
		scrubbed[k] = c.scrubValue(k, val, sensitiveKeys)
	}
	return scrubbed
}

// redactSensitiveValue handles redaction of values for sensitive keys.
// For scalar values (strings, numbers), it applies redaction.
// For complex types (maps, arrays), it recursively processes them
// to redact nested sensitive fields while preserving structure.
func (c *Collector) redactSensitiveValue(value any, sensitiveKeys []string) any {
	// Skip default/empty values
	if isDefaultValue(value) {
		return value
	}

	switch v := value.(type) {
	case string:
		if isURLValue(v) {
			return redactURLStructurally(v)
		}
		return redactedPlaceholder
	case float64, float32, int, int64, int32:
		return redactedPlaceholder
	case []any:
		// Recursively process arrays to preserve structure
		return c.redactSliceRecursively(v, sensitiveKeys)
	case map[string]any:
		// Recursively process maps to preserve structure
		return c.scrubMap(v, sensitiveKeys)
	default:
		return redactedPlaceholder
	}
}

// redactSliceRecursively handles redaction of slice values while preserving structure.
// For each item in the slice, it recursively processes to redact nested sensitive fields.
// Default values (empty strings, zero numbers) are preserved to maintain consistency
// with scalar sensitive value handling.
func (c *Collector) redactSliceRecursively(slice []any, sensitiveKeys []string) []any {
	if len(slice) == 0 {
		return slice
	}
	redacted := make([]any, len(slice))
	for i, item := range slice {
		// Preserve default values consistently with scalar handling
		if isDefaultValue(item) {
			redacted[i] = item
			continue
		}

		switch v := item.(type) {
		case string:
			if isURLValue(v) {
				redacted[i] = redactURLStructurally(v)
			} else {
				redacted[i] = redactedPlaceholder
			}
		case map[string]any:
			redacted[i] = c.scrubMap(v, sensitiveKeys)
		case []any:
			redacted[i] = c.redactSliceRecursively(v, sensitiveKeys)
		default:
			redacted[i] = redactedPlaceholder
		}
	}
	return redacted
}

// processNonSensitiveValue handles non-sensitive values with recursive processing.
func (c *Collector) processNonSensitiveValue(key string, value any, sensitiveKeys []string) any {
	switch v := value.(type) {
	case map[string]any:
		return c.scrubMap(v, sensitiveKeys)
	case []any:
		scrubbed := make([]any, len(v))
		for i, item := range v {
			scrubbed[i] = c.scrubValue(key, item, sensitiveKeys)
		}
		return scrubbed
	case string:
		// Sanitize RTSP URLs in all string values (existing behavior)
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
	getLogger().Debug("support: collectLogs started",
		logger.Duration("duration", duration),
		logger.Int64("maxSize", maxSize),
		logger.Bool("anonymizePII", anonymizePII))

	var logs []LogEntry
	var totalSize int64

	// Try to collect from journald first (systemd systems)
	// Skip journal collection if running in Docker as it's unlikely to be available
	if isRunningInDocker() {
		getLogger().Debug("support: running in Docker, skipping journal log collection")
		diagnostics.JournalLogs.Attempted = false
		diagnostics.JournalLogs.Details = map[string]any{
			"skipped_reason": "running_in_docker",
		}
	} else {
		getLogger().Debug("support: attempting to collect journal logs")
		diagnostics.JournalLogs.Attempted = true
		journalLogs, err := c.collectJournalLogs(ctx, duration, anonymizePII, &diagnostics.JournalLogs)
		if err != nil {
			diagnostics.JournalLogs.Error = err.Error()
			if errors.Is(err, ErrJournalNotAvailable) {
				getLogger().Debug("support: journal logs not available (expected on non-systemd systems)")
			} else {
				getLogger().Warn("support: error collecting journal logs", logger.Error(err))
			}
		} else {
			getLogger().Debug("support: collected journal logs", logger.Int("count", len(journalLogs)))
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
	getLogger().Debug("support: attempting to collect log files")
	diagnostics.FileLogs.Attempted = true
	logFiles, fileSize, err := c.collectLogFilesWithDiagnostics(duration, maxSize-totalSize, anonymizePII, &diagnostics.FileLogs)
	if err != nil {
		diagnostics.FileLogs.Error = err.Error()
		getLogger().Warn("support: error collecting log files", logger.Error(err))
	} else {
		getLogger().Debug("support: collected log files", logger.Int("count", len(logFiles)))
		diagnostics.FileLogs.Successful = true
		diagnostics.FileLogs.EntriesFound = len(logFiles)
		logs = append(logs, logFiles...)
		totalSize += fileSize
	}

	// Sort logs by timestamp
	getLogger().Debug("support: sorting logs by timestamp", logger.Int("total_logs", len(logs)))
	sortLogsByTime(logs)

	// Update summary diagnostics
	diagnostics.Summary.TotalEntries = len(logs)
	diagnostics.Summary.SizeBytes = totalSize
	diagnostics.Summary.TruncatedBySize = totalSize >= maxSize

	// Set TruncatedByTime only when entries were actually filtered out by time window
	var truncatedByTime bool
	if duration > 0 && len(logs) > 0 {
		cutoff := time.Now().Add(-duration)
		// If the earliest log is very close to the cutoff time (within 1 minute),
		// it's likely that older entries were filtered out
		earliestLog := logs[0].Timestamp
		if earliestLog.Sub(cutoff).Abs() <= time.Minute {
			truncatedByTime = true
		}
	}
	diagnostics.Summary.TruncatedByTime = truncatedByTime

	// Set the actual time range of collected logs
	if len(logs) > 0 {
		diagnostics.Summary.TimeRange.From = logs[0].Timestamp
		diagnostics.Summary.TimeRange.To = logs[len(logs)-1].Timestamp
	}

	getLogger().Debug("support: collectLogs completed", logger.Int("total_logs", len(logs)))
	return logs
}

// collectJournalLogs collects logs from systemd journal with diagnostics
func (c *Collector) collectJournalLogs(ctx context.Context, duration time.Duration, anonymizePII bool, diagnostics *LogSourceDiagnostics) ([]LogEntry, error) {
	// Calculate since time
	since := time.Now().Add(-duration).Format(journalTimeFormat)

	// Limit to prevent timeout using defined constant
	getLogger().Debug("support: running journalctl",
		logger.String("since", since),
		logger.Int("maxLines", maxJournalLogLines))

	// Run journalctl command with line limit
	cmd := exec.CommandContext(ctx, "journalctl", //nolint:gosec // G204: hardcoded command, args are constants/formatted values
		journalFlagUnit, systemdServiceName,
		journalFlagSince, since,
		journalFlagNoPager,
		journalFlagOutput, journalFlagJSON,
		journalFlagNoHostname,
		journalFlagLines, fmt.Sprintf("%d", maxJournalLogLines)) // Limit number of lines

	// Capture command for diagnostics
	diagnostics.Command = cmd.String()
	diagnostics.Details["service"] = systemdServiceName
	diagnostics.Details["since"] = since
	diagnostics.Details["max_lines"] = maxJournalLogLines
	diagnostics.Details["output_format"] = journalFlagJSON

	output, err := cmd.Output()
	if err != nil {
		// journalctl might not be available or service might not exist
		// This is not a fatal error, just means no journald logs available
		getLogger().Debug("journalctl unavailable or service not found", logger.Error(err))
		diagnostics.Details["error_type"] = "command_failed"
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			diagnostics.Details["exit_code"] = exitErr.ExitCode()
			diagnostics.Details["stderr"] = string(exitErr.Stderr)
		}
		return nil, ErrJournalNotAvailable
	}

	getLogger().Debug("support: journalctl output received", logger.Int("size", len(output)))

	// Parse JSON output line by line
	lines := strings.Split(string(output), "\n")
	getLogger().Debug("support: parsing journal entries", logger.Int("lineCount", len(lines)))

	// Pre-allocate logs slice based on number of lines
	logs := make([]LogEntry, 0, len(lines))
	parsedCount := 0
	skippedCount := 0

	for i, line := range lines {
		if line == "" {
			continue
		}

		// Log progress every N lines
		if i > 0 && i%logProgressInterval == 0 {
			getLogger().Debug("support: journal parsing progress",
				logger.Int("processed", i),
				logger.Int("total", len(lines)),
				logger.Int("parsed", parsedCount),
				logger.Int("skipped", skippedCount))
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
				ts = time.Unix(0, usec*microsecondsToNanoseconds)
			}
		}

		// Map priority to log level
		level := "INFO"
		switch priority {
		case priorityEmergency, priorityAlert, priorityCritical, priorityError:
			level = "ERROR"
		case priorityWarning:
			level = "WARNING"
		case priorityNotice, priorityInfo:
			level = "INFO"
		case priorityDebug:
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

	getLogger().Debug("support: journal parsing completed",
		logger.Int("totalLines", len(lines)),
		logger.Int("parsedEntries", parsedCount),
		logger.Int("skippedEntries", skippedCount),
		logger.Int("resultCount", len(logs)))

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

	file, err := os.Open(path) //nolint:gosec // G304: path is from known log file locations
	if err != nil {
		// Log file might not exist or be inaccessible, which is fine
		return logs, 0
	}
	defer func() {
		if err := file.Close(); err != nil {
			getLogger().Warn("Failed to close log file", logger.String("path", path), logger.Error(err))
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
	timestamp, err := time.ParseInLocation(logTimeFormat, parts[0]+" "+parts[1], time.Local)
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
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: configPath is from c.configPath (application config directory)
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
	getLogger().Debug("support: addLogFilesToArchive started",
		logger.Duration("duration", duration),
		logger.Int64("maxSize", maxSize),
		logger.Bool("anonymizePII", anonymizePII))

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
	getLogger().Debug("support: unique log paths identified", logger.Int("count", len(uniquePaths)), logger.Any("paths", uniquePaths))

	// Process each log path
	for _, logPath := range uniquePaths {
		if lfc.totalSize >= lfc.maxSize {
			getLogger().Debug("support: stopping log collection - max size reached",
				logger.Int64("totalSize", lfc.totalSize),
				logger.Int64("maxSize", lfc.maxSize))
			break
		}

		getLogger().Debug("support: processing log path for archive", logger.String("path", logPath))
		if err := lfc.processLogPath(w, logPath); err != nil {
			getLogger().Warn("support: error processing log path", logger.String("path", logPath), logger.Error(err))
			// Continue with next path on error
			continue
		}
	}

	// Add journald logs
	getLogger().Debug("support: attempting to add journald logs to archive")
	if err := lfc.addJournaldLogs(w, duration); err == nil {
		lfc.logsAdded++
		getLogger().Debug("support: journald logs added successfully")
	} else {
		getLogger().Debug("support: could not add journald logs", logger.Error(err))
	}

	// Add README if no logs found
	if lfc.logsAdded == 0 {
		getLogger().Debug("support: no logs found, adding README note")
		lfc.addNoLogsNote(w)
	} else {
		getLogger().Debug("support: logs added to archive", logger.Int("count", lfc.logsAdded), logger.Int64("totalSize", lfc.totalSize))
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
		// Ensure basePath is not empty
		// Note: basePath is from internal config (c.dataPath, c.configPath), not user input.
		// Path traversal protection happens below via absBase prefix check.
		if basePath == "" {
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
			Priority(errors.PriorityLow).
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
			Priority(errors.PriorityLow).
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
			Priority(errors.PriorityLow).
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
			Priority(errors.PriorityLow).
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
			Priority(errors.PriorityLow).
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
			Priority(errors.PriorityLow).
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
			Priority(errors.PriorityLow).
			Context("operation", "create_journald_file").
			Build()
	}

	if _, err := journalFile.Write([]byte(journalLogs)); err != nil {
		return errors.New(err).
			Component("support").
			Category(errors.CategoryFileIO).
			Priority(errors.PriorityLow).
			Context("operation", "write_journald_logs").
			Build()
	}

	return nil
}

// addNoLogsNote adds a README when no logs are found
func (lfc *logFileCollector) addNoLogsNote(w *zip.Writer) {
	noteFile, err := w.Create(logReadmeFileName)
	if err != nil {
		// Non-critical error, don't propagate
		return
	}

	message := logReadmeContent
	_, _ = noteFile.Write([]byte(message))
}

// addFileToArchive adds a single file to the zip archive
func (c *Collector) addFileToArchive(w *zip.Writer, sourcePath, archivePath string) error {
	file, err := os.Open(sourcePath) //nolint:gosec // G304: sourcePath is from known log/config file locations
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			getLogger().Warn("Failed to close file during archive creation", logger.String("source_path", sourcePath), logger.Error(err))
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
	file, err := os.Open(sourcePath) //nolint:gosec // G304: sourcePath is from known log file locations
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			getLogger().Warn("Failed to close log file during archive", logger.String("source_path", sourcePath), logger.Error(err))
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
	since := time.Now().Add(-duration).Format(journalTimeFormat)

	cmd := exec.CommandContext(ctx, "journalctl", //nolint:gosec // G204: hardcoded command, args are constants/formatted values
		"-u", "birdnet-go.service",
		"--since", since,
		"--no-pager",
		"-n", fmt.Sprintf("%d", maxJournalLogLines)) // Limit number of lines

	output, err := cmd.Output()
	if err != nil {
		getLogger().Debug("support: getJournaldLogs failed", logger.Error(err))
		return ""
	}

	getLogger().Debug("support: getJournaldLogs retrieved", logger.Int("size", len(output)))
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
