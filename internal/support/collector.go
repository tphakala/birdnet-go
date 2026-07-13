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
	"github.com/tphakala/birdnet-go/internal/sysinfo"
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

	// logScannerBufferBytes is the maximum single-line token size for log
	// scanning. The default bufio.Scanner limit (64KB) errors on long structured
	// log lines (bufio.ErrTooLong), which previously truncated a whole log file
	// silently mid-collection. Mirrors logger/reader.scannerBufferSize (1 MiB).
	logScannerBufferBytes = 1 << 20 // 1 MiB
	// logDeadlineCheckLines is how many scanned lines pass between context
	// deadline checks inside a single large file.
	logDeadlineCheckLines = 4096

	// Memory and disk thresholds
	bytesPerMB                = 1024 * 1024
	minDiskSizeMB             = 100 // Skip disks smaller than 100MB
	microsecondsToNanoseconds = 1000

	// File permissions
	defaultDirPermissions  = 0o755
	defaultFilePermissions = 0o644

	// SystemD and journal constants
	systemdServiceName = "birdnet-go.service"
	journalTimeFormat  = "2006-01-02 15:04:05"
	logTimeFormat      = "2006-01-02 15:04:05"

	// Archive file names
	diagnosticsFileName    = "collection_diagnostics.json"
	metadataFileName       = "metadata.json"
	configYAMLFileName     = "config.yaml"
	systemInfoFileName     = "system_info.json"
	databaseInfoFileName   = "database_info.json"
	deploymentInfoFileName = "deployment_info.json"
	appEventsFileName      = "app_events.json"
	logReadmeFileName      = "logs/README.txt"

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

// getLogger returns the support package logger.
// Fetched dynamically to ensure it uses the current centralized logger.
func getLogger() logger.Logger {
	return logger.Global().Module("support")
}

// logCollectionSafetyMargin reserves wall-clock time before the context
// deadline so the dump can be archived and the HTTP response written within the
// handler's window. Without this margin a system with very large logs could
// spend the entire timeout reading logs, blow the write deadline, and have the
// connection closed before any response is sent (observed as a generic
// "server error" in the UI). This must stay comfortably smaller than the
// caller's context timeout (currently supportDumpTimeout = 120s in
// internal/api/v2/support): if the deadline were ever set below this margin,
// nearDeadline would report true from the start and collection would yield an
// empty dump.
const logCollectionSafetyMargin = 15 * time.Second

// nearDeadline reports whether ctx is cancelled or within the safety margin of
// its deadline, signalling that log collection/archiving should stop early and
// finalize a partial dump rather than risk exceeding the handler timeout. A nil
// context (no production caller passes one, but a test might) is treated as
// having no deadline.
func nearDeadline(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	if ctx.Err() != nil {
		return true
	}
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= logCollectionSafetyMargin {
		return true
	}
	return false
}

// logScanAccum accumulates lightweight statistics while counting log entries
// during the collection phase. The entry content is intentionally discarded:
// the raw (scrubbed) log files are what ship in the archive, so the collection
// phase only needs counts and the observed time range for diagnostics. The raw
// files are still re-read when the archive is built; what this avoids is
// retaining a parsed, scrubbed copy of every log line in memory (the old code
// kept them all in SupportDump.Logs, which grew with total log volume).
type logScanAccum struct {
	entries   int
	size      int64
	earliest  time.Time
	latest    time.Time
	truncated bool // set when at least one file's tail was cut to fit its budget
}

// track folds a parsed entry timestamp into the accumulator's time range.
func (a *logScanAccum) track(t time.Time) {
	if t.IsZero() {
		return
	}
	if a.earliest.IsZero() || t.Before(a.earliest) {
		a.earliest = t
	}
	if a.latest.IsZero() || t.After(a.latest) {
		a.latest = t
	}
}

// seekToTail positions f to read at most maxBytes from the end of a file of the
// given size, starting at the next line boundary. When maxBytes <= 0 or the
// file already fits, the whole file is read from the start (so maxBytes == 0
// means unbounded, not "read nothing"). Returns a reader for the remaining
// content. Reading the tail (rather than skipping an oversized file entirely)
// keeps the most recent, most relevant log lines.
func seekToTail(f *os.File, size, maxBytes int64) (io.Reader, error) {
	if maxBytes <= 0 || size <= maxBytes {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		return f, nil
	}
	if _, err := f.Seek(size-maxBytes, io.SeekStart); err != nil {
		return nil, err
	}
	// Discard the partial first line so the archived tail starts cleanly on a
	// line boundary. Best effort: a file with no newline in the tail yields no
	// output, which is acceptable.
	br := bufio.NewReader(f)
	_, _ = br.ReadBytes('\n')
	return br, nil
}

// Collector collects support data for troubleshooting
type Collector struct {
	configPath        string
	dataPath          string
	systemID          string
	version           string
	sensitiveKeys     []string
	dbInfoProvider    DatabaseInfoProvider
	appEventsProvider AppEventsProvider
}

// DefaultSensitiveKeys returns the default list of sensitive configuration keys to redact.
func DefaultSensitiveKeys() []string {
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

// MatchesSensitiveKey checks if a dot-separated key path contains any sensitive key pattern.
func MatchesSensitiveKey(key string, sensitiveKeys []string) bool {
	lowerKey := strings.ReplaceAll(strings.ToLower(key), ".", "_")
	for _, sensitive := range sensitiveKeys {
		if isSensitiveKey(lowerKey, sensitive) {
			return true
		}
	}
	return false
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
		sensitiveKeys: DefaultSensitiveKeys(),
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
		sensitiveKeys = DefaultSensitiveKeys()
	}

	return &Collector{
		configPath:    configPath,
		dataPath:      dataPath,
		systemID:      systemID,
		version:       version,
		sensitiveKeys: sensitiveKeys,
	}
}

// SetDatabaseInfoProvider configures the database diagnostics provider.
func (c *Collector) SetDatabaseInfoProvider(provider DatabaseInfoProvider) {
	c.dbInfoProvider = provider
}

// SetAppEventsProvider configures the application events provider.
func (c *Collector) SetAppEventsProvider(provider AppEventsProvider) {
	c.appEventsProvider = provider
}

// Collect gathers support data based on the provided options
func (c *Collector) Collect(ctx context.Context, opts CollectorOptions) (*SupportDump, error) {
	// Validate options
	if !opts.IncludeLogs && !opts.IncludeConfig && !opts.IncludeSystemInfo && !opts.IncludeDatabaseInfo && !opts.IncludeDeploymentInfo && !opts.IncludeAppEvents {
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
		logger.Bool("include_database_info", opts.IncludeDatabaseInfo),
		logger.Bool("include_app_events", opts.IncludeAppEvents),
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
		getLogger().Debug("support: logs collected",
			logger.Int("log_count", dump.Diagnostics.LogCollection.Summary.TotalEntries))
	}

	// Collect database information
	if opts.IncludeDatabaseInfo && c.dbInfoProvider != nil {
		getLogger().Debug("support: collecting database information")
		dbInfo, err := c.dbInfoProvider.CollectDatabaseInfo(ctx)
		if err != nil {
			getLogger().Error("support: failed to collect database information", logger.Error(err))
		} else {
			dump.DatabaseInfo = dbInfo
			getLogger().Debug("support: database information collected",
				logger.String("dialect", dbInfo.Dialect),
				logger.Int("table_count", len(dbInfo.Tables)))
		}
	}

	// Collect deployment context
	if opts.IncludeDeploymentInfo {
		getLogger().Debug("support: collecting deployment information")
		dump.DeploymentInfo = c.collectDeploymentInfo(ctx, opts.AnonymizePII)
		getLogger().Debug("support: deployment information collected")
	}

	// Collect app events
	if opts.IncludeAppEvents && c.appEventsProvider != nil {
		getLogger().Debug("support: collecting app events")
		appEvents, err := c.appEventsProvider.GetRecentAppEvents(ctx, supportDumpEventLimit)
		if err != nil {
			getLogger().Error("support: failed to collect app events", logger.Error(err))
		} else {
			dump.AppEvents = appEvents
			getLogger().Debug("support: app events collected",
				logger.Int("event_count", len(appEvents)))
		}
	}

	getLogger().Info("support: collection completed successfully",
		logger.String("dump_id", dump.ID),
		logger.Int("log_count", dump.Diagnostics.LogCollection.Summary.TotalEntries))

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
		"log_count":               dump.Diagnostics.LogCollection.Summary.TotalEntries,
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

	// Add database info if collected
	if dump.DatabaseInfo != nil {
		getLogger().Debug("support: adding database info to archive")
		dbInfoFile, err := w.Create(databaseInfoFileName)
		if err != nil {
			getLogger().Error("support: failed to create database info file in archive", logger.Error(err))
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "create_database_info_file").
				Build()
		}
		if err := json.NewEncoder(dbInfoFile).Encode(dump.DatabaseInfo); err != nil {
			getLogger().Error("support: failed to write database info to archive", logger.Error(err))
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "write_database_info").
				Build()
		}
		getLogger().Debug("support: database info added successfully")
	}

	// Add deployment info if collected
	if dump.DeploymentInfo != nil {
		getLogger().Debug("support: adding deployment info to archive")
		deployFile, err := w.Create(deploymentInfoFileName)
		if err != nil {
			getLogger().Error("support: failed to create deployment info file in archive", logger.Error(err))
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "create_deployment_info_file").
				Build()
		}
		if err := json.NewEncoder(deployFile).Encode(dump.DeploymentInfo); err != nil {
			getLogger().Error("support: failed to write deployment info to archive", logger.Error(err))
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "write_deployment_info").
				Build()
		}
		getLogger().Debug("support: deployment info added successfully")
	}

	// Add app events if collected
	if len(dump.AppEvents) > 0 {
		getLogger().Debug("support: adding app events to archive")
		eventsFile, err := w.Create(appEventsFileName)
		if err != nil {
			getLogger().Error("support: failed to create app events file in archive", logger.Error(err))
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "create_app_events_file").
				Build()
		}
		if err := json.NewEncoder(eventsFile).Encode(dump.AppEvents); err != nil {
			getLogger().Error("support: failed to write app events to archive", logger.Error(err))
			return nil, errors.New(err).
				Component("support").
				Category(errors.CategoryFileIO).
				Context("operation", "write_app_events").
				Build()
		}
		getLogger().Debug("support: app events added successfully")
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
// This method NEVER returns an error - it always returns the (bounded) journal
// log slice, even empty, with diagnostics populated.
//
// Collection strategy:
//  1. First attempts journal logs (systemd) - may fail on Docker/non-systemd systems
//  2. Then COUNTS file-log entries from known paths (gracefully handling missing/inaccessible paths). File-log content is not retained here, only counted for diagnostics, because the archive ships the raw files separately.
//  3. Folds journal and file counts, sizes and time ranges into the diagnostics summary and sorts only the (small) journal slice
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

	// journalLogs holds only the (bounded) journal entries. File log entries are
	// counted, not retained, because the raw files ship in the archive; keeping
	// parsed copies here would read every log file twice.
	var journalLogs []LogEntry
	var acc logScanAccum

	// Try to collect from journald first (systemd systems).
	// Skip journal collection in container runtimes (Docker, Podman, LXC,
	// systemd-nspawn) where journald is typically unavailable. Uses
	// sysinfo.IsContainer() so the check matches the same gate that
	// addJournaldLogs uses on the archive path — keeping both journald
	// collection paths consistent and removing the now-redundant local
	// Docker-only helper.
	if sysinfo.IsContainer() {
		getLogger().Debug("support: running in a container runtime, skipping journal log collection")
		diagnostics.JournalLogs.Attempted = false
		diagnostics.JournalLogs.Details = map[string]any{
			"skipped_reason": "running_in_container",
		}
	} else {
		getLogger().Debug("support: attempting to collect journal logs")
		diagnostics.JournalLogs.Attempted = true
		jl, err := c.collectJournalLogs(ctx, duration, anonymizePII, &diagnostics.JournalLogs)
		if err != nil {
			diagnostics.JournalLogs.Error = err.Error()
			if errors.Is(err, ErrJournalNotAvailable) {
				getLogger().Debug("support: journal logs not available (expected on non-systemd systems)")
			} else {
				getLogger().Warn("support: error collecting journal logs", logger.Error(err))
			}
		} else {
			getLogger().Debug("support: collected journal logs", logger.Int("count", len(jl)))
			diagnostics.JournalLogs.Successful = true
			diagnostics.JournalLogs.EntriesFound = len(jl)
			journalLogs = jl
			acc.entries += len(jl)
			for i := range jl {
				acc.size += int64(len(jl[i].Message))
				acc.track(jl[i].Timestamp)
			}
		}
	}

	// Count entries in on-disk log files. Content is discarded (only counts and
	// the time range feed diagnostics); the raw files are added to the archive
	// separately by addLogFilesToArchive. A separate accumulator keeps the file
	// size budget independent of the journal size already consumed.
	getLogger().Debug("support: attempting to count log files")
	diagnostics.FileLogs.Attempted = true
	// Count file logs using the same fair per-file budget the archive path uses
	// (gatherLogFileTargets + allocateTailBudgets), so the counted tail boundary
	// aligns with the archived one and no single large file starves the count of
	// the others. A separate accumulator keeps the file byte budget independent
	// of the journal size already consumed.
	var fileAcc logScanAccum
	if err := c.collectLogFilesWithDiagnostics(ctx, duration, maxSize, &fileAcc, &diagnostics.FileLogs); err != nil {
		diagnostics.FileLogs.Error = err.Error()
		getLogger().Warn("support: error counting log files", logger.Error(err))
	} else {
		getLogger().Debug("support: counted log files", logger.Int("count", fileAcc.entries))
		diagnostics.FileLogs.Successful = true
		diagnostics.FileLogs.EntriesFound = fileAcc.entries
		acc.entries += fileAcc.entries
		acc.size += fileAcc.size
		acc.track(fileAcc.earliest)
		acc.track(fileAcc.latest)
	}

	// Sort the (small) journal slice by timestamp for stable output.
	sortLogsByTime(journalLogs)

	// Update summary diagnostics from the accumulator.
	diagnostics.Summary.TotalEntries = acc.entries
	diagnostics.Summary.SizeBytes = acc.size
	// Report size-truncation only when a file's tail was actually cut to fit its
	// budget. Journald is line-limited, not size-limited, and with fair per-file
	// budgets the total counted size can stay below maxSize even when a file was
	// truncated, so key the flag off the file accumulator's truncated marker.
	diagnostics.Summary.TruncatedBySize = fileAcc.truncated

	// Set TruncatedByTime only when entries were likely filtered out by the time
	// window (earliest observed entry is within a minute of the cutoff).
	if duration > 0 && !acc.earliest.IsZero() {
		cutoff := time.Now().Add(-duration)
		if acc.earliest.Sub(cutoff).Abs() <= time.Minute {
			diagnostics.Summary.TruncatedByTime = true
		}
	}

	// Set the actual time range of collected logs.
	if !acc.earliest.IsZero() {
		diagnostics.Summary.TimeRange.From = acc.earliest
	}
	if !acc.latest.IsZero() {
		diagnostics.Summary.TimeRange.To = acc.latest
	}

	getLogger().Debug("support: collectLogs completed", logger.Int("total_entries", acc.entries))
	return journalLogs
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
func (c *Collector) collectLogFilesWithDiagnostics(ctx context.Context, duration time.Duration, maxSize int64, acc *logScanAccum, diagnostics *LogSourceDiagnostics) error {
	cutoffTime := time.Now().Add(-duration)

	// Get unique log paths and capture diagnostic information for each.
	// These paths include both configured paths and common default locations.
	uniquePaths := c.getUniqueLogPaths()
	// TODO: Add mutex protection if this method becomes concurrent
	diagnostics.Details["total_paths_to_search"] = len(uniquePaths)

	// First pass: record accessibility diagnostics for each search path. This is
	// a cheap listing only; entry counting happens in the second pass so the
	// per-file byte budget can be shared fairly across every file.
	for _, logPath := range uniquePaths {
		if nearDeadline(ctx) {
			diagnostics.Details["stopped_reason"] = "deadline"
			break
		}

		searchedPath := SearchedPath{Path: logPath}

		// Check if path exists
		info, statErr := os.Stat(logPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				searchedPath.Exists = false
				searchedPath.Error = "path does not exist"
			} else {
				searchedPath.Exists = true
				searchedPath.Accessible = false
				searchedPath.Error = statErr.Error()
			}
			diagnostics.PathsSearched = append(diagnostics.PathsSearched, searchedPath)
			continue
		}

		searchedPath.Exists = true
		searchedPath.Accessible = true

		if fileCount, dirErr := c.countLogFilesInPath(logPath, info); dirErr != nil {
			// Enhanced error context for directory listing failures
			if os.IsPermission(dirErr) {
				searchedPath.Error = fmt.Sprintf("permission denied: %v", dirErr)
			} else {
				searchedPath.Error = dirErr.Error()
			}
		} else {
			searchedPath.FileCount = fileCount
		}

		diagnostics.PathsSearched = append(diagnostics.PathsSearched, searchedPath)
	}

	// Second pass: count entries against the SAME fair per-file budgets the
	// archive path uses (gatherLogFileTargets + allocateTailBudgets). This
	// guarantees a single large file cannot stop the count of the others, so the
	// diagnostics describe every shipped file rather than a starved subset.
	targets := c.gatherLogFileTargets(uniquePaths, cutoffTime)
	allocateTailBudgets(targets, maxSize)
	filesProcessed := 0
	for i := range targets {
		if nearDeadline(ctx) {
			diagnostics.Details["stopped_reason"] = "deadline"
			break
		}
		t := &targets[i]
		if t.budget <= 0 {
			continue
		}
		c.countLogFile(ctx, t.sourcePath, cutoffTime, t.budget, acc)
		filesProcessed++
	}

	// Report the files actually counted, not len(targets): a deadline break or a
	// zero-budget skip means fewer files were processed, and an inflated count
	// would mislead an operator reading the diagnostics.
	diagnostics.Details["files_processed"] = filesProcessed
	diagnostics.Details["total_size_bytes"] = acc.size

	return nil
}

// countLogFilesInPath returns how many log files a search path contributes: 1
// for a single log file, or the number of ".log" files in a directory. It only
// lists names for diagnostics and never reads file contents. Directory listing
// errors (e.g. permission denied) are returned so the caller can record them.
func (c *Collector) countLogFilesInPath(logPath string, info os.FileInfo) (int, error) {
	if !info.IsDir() {
		if hasLogSuffix(logPath) && info.Mode().IsRegular() {
			return 1, nil
		}
		return 0, nil
	}

	files, err := os.ReadDir(logPath)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, file := range files {
		// Match the regular-file filter gatherLogFileTargets applies, so the
		// reported count reflects the files actually collectible rather than
		// counting a subdirectory or device named "*.log".
		if hasLogSuffix(file.Name()) && file.Type().IsRegular() {
			count++
		}
	}
	return count, nil
}

// countLogFile scans up to remaining bytes of a log file, counting entries
// within the retention window and folding size and time range into acc. The
// parsed content is discarded (the raw file ships in the archive), so lines are
// parsed without PII scrubbing. A large scanner buffer avoids bufio.ErrTooLong
// on long structured log lines, and the context deadline is honored so a very
// large file cannot consume the whole timeout.
func (c *Collector) countLogFile(ctx context.Context, path string, cutoffTime time.Time, remaining int64, acc *logScanAccum) {
	if remaining <= 0 {
		return
	}

	file, err := os.Open(path) //nolint:gosec // G304: path is from known log file locations
	if err != nil {
		// Log file might not exist or be inaccessible, which is fine.
		return
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			getLogger().Warn("Failed to close log file", logger.String("path", path), logger.Error(cerr))
		}
	}()

	// Align with the archive path (seekToTail): when the file is larger than the
	// remaining budget, count its recent TAIL rather than its head, so the
	// diagnostics entry count and time range describe the same bytes the archive
	// keeps instead of an older window that is discarded before shipping.
	var reader io.Reader = file
	if info, statErr := file.Stat(); statErr == nil && info.Size() > remaining {
		acc.truncated = true // the file is larger than its budget, so we ship a tail
		if r, seekErr := seekToTail(file, info.Size(), remaining); seekErr == nil {
			reader = r
		}
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), logScannerBufferBytes)

	var scanned int64
	lines := 0
	for scanner.Scan() {
		line := scanner.Text()
		lineBytes := int64(len(line)) + 1 // include the newline
		if scanned+lineBytes > remaining {
			// Exclude the overflowing line so acc.size stays within the budget.
			break
		}
		scanned += lineBytes
		lines++
		if lines%logDeadlineCheckLines == 0 && nearDeadline(ctx) {
			break
		}

		if entry := c.parseLogLine(line, false); entry != nil && entry.Timestamp.After(cutoffTime) {
			acc.entries++
			acc.track(entry.Timestamp)
		}
	}

	acc.size += scanned

	if serr := scanner.Err(); serr != nil {
		getLogger().Debug("support: log file scan stopped early",
			logger.String("path", path), logger.Error(serr))
	}
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
	totalSize    int64
	logsAdded    int
	anonymizePII bool
}

// addLogFilesToArchive adds log files to the archive in their original format.
//
// Every eligible log file is included with (at least) its recent tail. The size
// budget is split fairly across all files (see allocateTailBudgets) so a single
// very large file (for example a bloated access.log) can never consume the whole
// budget and starve the others (for example the audio.log needed to diagnose an
// RTSP failure). This is the fix for issue #3902, where a huge access.log caused
// the collector to stop before it reached audio.log.
func (c *Collector) addLogFilesToArchive(ctx context.Context, w *zip.Writer, duration time.Duration, maxSize int64, anonymizePII bool) error {
	getLogger().Debug("support: addLogFilesToArchive started",
		logger.Duration("duration", duration),
		logger.Int64("maxSize", maxSize),
		logger.Bool("anonymizePII", anonymizePII))

	lfc := &logFileCollector{
		ctx:          ctx,
		collector:    c,
		cutoffTime:   time.Now().Add(-duration),
		totalSize:    0,
		logsAdded:    0,
		anonymizePII: anonymizePII,
	}

	// Enumerate every eligible log file across all search paths, then split the
	// budget fairly so each file is guaranteed a share for its recent tail.
	uniquePaths := c.getUniqueLogPaths()
	targets := c.gatherLogFileTargets(uniquePaths, lfc.cutoffTime)
	allocateTailBudgets(targets, maxSize)
	getLogger().Debug("support: log file targets planned",
		logger.Int("path_count", len(uniquePaths)),
		logger.Int("file_count", len(targets)),
		logger.Int64("maxSize", maxSize))

	// Add files smallest-first so that, if the deadline is hit partway through,
	// the greatest number of files still make it into the (partial) dump.
	for i := range targets {
		t := &targets[i]
		if t.budget <= 0 {
			continue
		}

		// Stop before the handler deadline so the dump can still be finalized and
		// returned. This is now purely a time guard: the per-file budgets already
		// prevent any single file from exhausting the total, so reaching the size
		// cap no longer aborts collection of the remaining files.
		if nearDeadline(lfc.ctx) {
			getLogger().Warn("support: stopping log archive early - deadline approaching",
				logger.Int64("totalSize", lfc.totalSize),
				logger.Int("logs_added", lfc.logsAdded),
				logger.Int("logs_remaining", len(targets)-i))
			break
		}

		getLogger().Debug("support: adding log file to archive",
			logger.String("path", t.sourcePath),
			logger.Int64("budget", t.budget),
			logger.Int64("size", t.size))
		if err := c.addLogFileToArchive(lfc.ctx, w, t.sourcePath, t.archiveName, anonymizePII, t.budget); err != nil {
			getLogger().Warn("support: error adding log file to archive",
				logger.String("path", t.sourcePath), logger.Error(err))
			// Continue with the next file on error
			continue
		}
		lfc.totalSize += t.budget
		lfc.logsAdded++
	}

	// Add journald logs when running on a host where systemd-journald is
	// actually available. In container runtimes (Docker, Podman, LXC,
	// systemd-nspawn, generic) the journalctl binary either does not exist
	// or returns an error, which previously surfaced as a recurring Sentry
	// event every time a support dump was generated. Use the shared
	// sysinfo.IsContainer() detector so we cover all container flavours,
	// not just Docker.
	switch {
	case sysinfo.IsContainer():
		getLogger().Debug("support: skipping journald collection (container runtime)")
	case nearDeadline(lfc.ctx):
		// journalctl runs an external command that can be slow; skip it when the
		// deadline is near so it cannot overrun the handler window.
		getLogger().Warn("support: skipping journald collection - deadline approaching")
	default:
		getLogger().Debug("support: attempting to add journald logs to archive")
		if err := lfc.addJournaldLogs(w, duration); err == nil {
			lfc.logsAdded++
			getLogger().Debug("support: journald logs added successfully")
		} else {
			getLogger().Debug("support: could not add journald logs", logger.Error(err))
		}
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

// logFileTarget is a single log file selected for inclusion in the support dump,
// together with the tail-byte budget allocated to it by allocateTailBudgets.
type logFileTarget struct {
	sourcePath  string // absolute path of the source log file on disk
	archiveName string // destination path inside the archive, e.g. "logs/audio.log"
	size        int64  // full size of the source file in bytes
	budget      int64  // max bytes of the recent tail to include (line-aligned)
}

// gatherLogFileTargets enumerates every eligible log file across the given search
// paths and returns them sorted by size ascending (the order allocateTailBudgets
// expects). A file is eligible when its name ends in "log" (case-insensitive)
// and its modification time is within the retention window. Targets are
// de-duplicated by archive name so the same log is never added twice (which
// would otherwise create a duplicate zip entry). Missing or unreadable paths are
// skipped silently, matching the best-effort nature of support collection.
func (c *Collector) gatherLogFileTargets(paths []string, cutoff time.Time) []logFileTarget {
	var targets []logFileTarget
	seen := make(map[string]bool)

	add := func(sourcePath, archiveName string, modTime time.Time, size int64) {
		if modTime.Before(cutoff) {
			return // outside the retention window
		}
		if seen[archiveName] {
			return
		}
		seen[archiveName] = true
		targets = append(targets, logFileTarget{
			sourcePath:  sourcePath,
			archiveName: archiveName,
			size:        size,
		})
	}

	for _, logPath := range paths {
		info, err := os.Stat(logPath)
		if err != nil {
			continue // missing/inaccessible paths are expected during the search
		}
		switch {
		case info.IsDir():
			entries, err := os.ReadDir(logPath)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				name := entry.Name()
				// Only take regular files. Skipping directories, symlinks, FIFOs,
				// sockets and devices matters for robustness: a FIFO or device
				// named "*.log" would block the later os.Open indefinitely (hanging
				// the whole dump), and a subdirectory named "*.log" would fail with
				// EISDIR and waste budget. A single odd entry must never stall
				// collection of the real logs.
				if !hasLogSuffix(name) || !entry.Type().IsRegular() {
					continue
				}
				fi, err := entry.Info()
				if err != nil {
					continue
				}
				// Archive (zip) entry names must use forward slashes on every OS
				// (the ZIP format and archive/zip require it); filepath.Join would
				// emit "logs\name" on Windows, producing non-conformant entries
				// inconsistent with the hardcoded "logs/journald.log". name is a
				// bare basename, so "logs/"+name cannot traverse out of logs/.
				add(filepath.Join(logPath, name), "logs/"+name, fi.ModTime(), fi.Size())
			}
		case hasLogSuffix(logPath) && info.Mode().IsRegular():
			add(logPath, "logs/"+filepath.Base(logPath), info.ModTime(), info.Size())
		}
	}

	// Sort by size ascending, with archive name as a stable tiebreaker so equal
	// sized files (and therefore the contents of a near-deadline partial dump)
	// are deterministic.
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].size != targets[j].size {
			return targets[i].size < targets[j].size
		}
		return targets[i].archiveName < targets[j].archiveName
	})
	return targets
}

// allocateTailBudgets distributes maxSize across targets (which must be sorted by
// size ascending) so every file receives a fair share of the budget for its
// recent tail. Files that fit within their fair share are taken whole and their
// unused surplus rolls forward to the larger files (water-filling). This
// guarantees that no file is dropped because an earlier, larger file consumed the
// entire budget: the fix for issue #3902, where a huge access.log starved
// audio.log. Because targets are processed smallest-first, every file is granted
// at least min(size, maxSize/len(targets)) bytes.
//
// maxSize <= 0 means "no cap": every file is taken whole (matching seekToTail,
// where a non-positive limit reads the whole file).
func allocateTailBudgets(targets []logFileTarget, maxSize int64) {
	if maxSize <= 0 {
		for i := range targets {
			targets[i].budget = targets[i].size
		}
		return
	}
	remainingBudget := maxSize
	remainingCount := int64(len(targets))
	for i := range targets {
		if remainingCount <= 0 {
			break
		}
		share := remainingBudget / remainingCount
		grant := min(targets[i].size, share)
		targets[i].budget = grant
		remainingBudget -= grant
		remainingCount--
	}
}

// hasLogSuffix reports whether a filename looks like a log file, i.e. it ends in
// "log" (case-insensitive). Compressed rotations (".log.gz") end in "gz" and are
// intentionally excluded.
func hasLogSuffix(filename string) bool {
	return strings.HasSuffix(strings.ToLower(filename), "log")
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
// addFileToArchive copies sourcePath into the archive. When maxBytes > 0 and the
// file is larger, only the most recent maxBytes (aligned to a line boundary) are
// written; the zip.Writer computes the stored size from the bytes actually
// written, so a truncated tail produces a valid entry.
func (c *Collector) addFileToArchive(w *zip.Writer, sourcePath, archivePath string, maxBytes int64) error {
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

	reader, err := seekToTail(file, fileInfo.Size(), maxBytes)
	if err != nil {
		return err
	}

	// No intra-file deadline check here: this non-anonymized copy is bounded by
	// maxBytes (<= the 50MB MaxLogSize cap), so one file cannot overrun the
	// safety margin; the directory loop checks nearDeadline between files.
	_, err = io.Copy(writer, reader)
	return err
}

// addLogFileToArchive adds a log file to the zip archive with optional
// anonymization. When maxBytes > 0 and the file is larger, only the most recent
// maxBytes (aligned to a line boundary) are written. The context deadline is
// honored so anonymizing a large file cannot consume the whole handler window.
func (c *Collector) addLogFileToArchive(ctx context.Context, w *zip.Writer, sourcePath, archivePath string, anonymizePII bool, maxBytes int64) error {
	if !anonymizePII {
		// If no anonymization needed, use the regular file copy.
		return c.addFileToArchive(w, sourcePath, archivePath, maxBytes)
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

	reader, err := seekToTail(file, fileInfo.Size(), maxBytes)
	if err != nil {
		return err
	}

	// Process the file line by line for anonymization. A large buffer avoids
	// bufio.ErrTooLong on long structured log lines (which previously truncated
	// the file silently). Writes go through a buffered writer so each line does
	// not allocate a new []byte (avoids the per-line "line + \n" concatenation
	// and conversion across hundreds of thousands of lines).
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), logScannerBufferBytes)
	bw := bufio.NewWriter(writer)
	lines := 0
	for scanner.Scan() {
		if _, err := bw.WriteString(privacy.ScrubMessage(scanner.Text())); err != nil {
			return err
		}
		if err := bw.WriteByte('\n'); err != nil {
			return err
		}
		lines++
		if lines%logDeadlineCheckLines == 0 && nearDeadline(ctx) {
			getLogger().Warn("support: stopping log anonymization early - deadline approaching",
				logger.String("source_path", sourcePath))
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return bw.Flush()
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
