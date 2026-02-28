// Package reader provides a reusable JSONL log reader that preserves all fields
// from structured log entries produced by Go's slog.JSONHandler.
package reader

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Level constants define the log level hierarchy for filtering.
const (
	LevelDebug = "DEBUG"
	LevelInfo  = "INFO"
	LevelWarn  = "WARN"
	LevelError = "ERROR"
)

// levelRank maps log level strings to numeric ranks for comparison.
// Higher rank means more severe.
var levelRank = map[string]int{
	LevelDebug: 0,
	LevelInfo:  1,
	LevelWarn:  2,
	LevelError: 3,
}

// knownFields lists the JSON keys that are parsed into typed LogEntry fields.
// All other keys are collected into the Fields map.
var knownFields = map[string]bool{
	"time":      true,
	"level":     true,
	"msg":       true,
	"module":    true,
	"operation": true,
}

// scannerBufferSize is the maximum buffer size for bufio.Scanner (1 MB).
// Log lines can be long when they contain embedded data.
const scannerBufferSize = 1024 * 1024

// LogEntry represents a parsed JSONL log line with all fields preserved.
type LogEntry struct {
	Time      time.Time      `json:"time"`
	Level     string         `json:"level"`
	Msg       string         `json:"msg"`
	Module    string         `json:"module"`
	Operation string         `json:"operation"`
	Fields    map[string]any // All remaining fields not in the typed struct members.
}

// ReadOptions controls filtering during log file scanning.
type ReadOptions struct {
	Date       time.Time // Required: only return entries for this date (compared as UTC date).
	Operations []string  // Optional: filter to these operation values.
	Level      string    // Optional: minimum level (DEBUG, INFO, WARN, ERROR).
	Module     string    // Optional: filter to this module prefix.
}

// ReadFile reads a JSONL log file and returns entries matching the options.
// Malformed lines are silently skipped. The returned entries are not sorted;
// use ReadFiles for merged, sorted, deduplicated results across multiple files.
func ReadFile(path string, opts *ReadOptions) ([]LogEntry, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from caller / FindLogFiles
	if err != nil {
		return nil, fmt.Errorf("opening log file %s: %w", path, err)
	}
	defer func() {
		_ = f.Close()
	}()

	var entries []LogEntry

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, scannerBufferSize), scannerBufferSize)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		entry, ok := parseLine(line)
		if !ok {
			continue
		}

		if matchesOptions(&entry, opts) {
			entries = append(entries, entry)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning log file %s: %w", path, err)
	}

	return entries, nil
}

// ReadFiles reads multiple log files (active + rotated), deduplicates,
// sorts by timestamp, and returns merged results. Deduplication uses a
// composite key of timestamp + message + operation.
func ReadFiles(paths []string, opts *ReadOptions) ([]LogEntry, error) {
	// Estimate capacity: assume ~100 entries per file as a starting point.
	const estimatedEntriesPerFile = 100
	allEntries := make([]LogEntry, 0, len(paths)*estimatedEntriesPerFile)

	for _, path := range paths {
		entries, err := ReadFile(path, opts)
		if err != nil {
			return nil, err
		}
		allEntries = append(allEntries, entries...)
	}

	// Deduplicate by composite key: timestamp (UnixNano) + msg + operation.
	seen := make(map[string]struct{}, len(allEntries))
	deduplicated := make([]LogEntry, 0, len(allEntries))

	for i := range allEntries {
		key := deduplicationKey(&allEntries[i])
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		deduplicated = append(deduplicated, allEntries[i])
	}

	// Sort by timestamp ascending.
	slices.SortFunc(deduplicated, func(a, b LogEntry) int {
		return a.Time.Compare(b.Time)
	})

	return deduplicated, nil
}

// FindLogFiles finds the active log file and any rotated files that
// could contain entries for the target date (based on file naming convention).
// Rotated files follow the lumberjack pattern: <basename>-<timestamp>.<ext>
// (e.g., actions-2024-01-15T10-30-00Z.log). Compressed (.gz) files are excluded
// since they require decompression.
func FindLogFiles(basePath string) ([]string, error) {
	// Start with the active log file if it exists.
	var files []string

	if _, err := os.Stat(basePath); err == nil {
		files = append(files, basePath)
	}

	// Build glob pattern for rotated files: <base>-*<ext>
	ext := filepath.Ext(basePath)
	base := strings.TrimSuffix(basePath, ext)
	pattern := fmt.Sprintf("%s-*%s", base, ext)

	rotated, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("globbing rotated log files with pattern %s: %w", pattern, err)
	}

	// Filter out compressed files and the active file.
	for _, path := range rotated {
		if strings.HasSuffix(path, ".gz") {
			continue
		}
		// Avoid duplicating the active file.
		if path == basePath {
			continue
		}
		files = append(files, path)
	}

	return files, nil
}

// parseLine parses a single JSONL line into a LogEntry.
// Returns false if the line is malformed or missing required fields.
func parseLine(line []byte) (LogEntry, bool) {
	// First pass: unmarshal everything into a generic map.
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return LogEntry{}, false
	}

	// Extract and parse the time field (required).
	timeStr, ok := raw["time"].(string)
	if !ok {
		return LogEntry{}, false
	}

	t, err := time.Parse(time.RFC3339Nano, timeStr)
	if err != nil {
		return LogEntry{}, false
	}

	entry := LogEntry{
		Time: t,
	}

	// Extract typed fields.
	if v, ok := raw["level"].(string); ok {
		entry.Level = v
	}
	if v, ok := raw["msg"].(string); ok {
		entry.Msg = v
	}
	if v, ok := raw["module"].(string); ok {
		entry.Module = v
	}
	if v, ok := raw["operation"].(string); ok {
		entry.Operation = v
	}

	// Collect remaining fields.
	extra := make(map[string]any, len(raw)-len(knownFields))
	for k, v := range raw {
		if !knownFields[k] {
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		entry.Fields = extra
	}

	return entry, true
}

// matchesOptions checks whether a log entry passes all the filters in opts.
func matchesOptions(entry *LogEntry, opts *ReadOptions) bool {
	// Date filter (required): compare UTC dates.
	if !opts.Date.IsZero() {
		entryDate := entry.Time.UTC().Truncate(24 * time.Hour)
		targetDate := opts.Date.UTC().Truncate(24 * time.Hour)
		if !entryDate.Equal(targetDate) {
			return false
		}
	}

	// Level filter: entry must be at or above the minimum level.
	if opts.Level != "" {
		minRank, minOK := levelRank[strings.ToUpper(opts.Level)]
		entryRank, entryOK := levelRank[strings.ToUpper(entry.Level)]
		if minOK && entryOK && entryRank < minRank {
			return false
		}
	}

	// Operation filter: entry operation must be in the allowed set.
	if len(opts.Operations) > 0 {
		if !slices.Contains(opts.Operations, entry.Operation) {
			return false
		}
	}

	// Module filter: entry module must have the specified prefix.
	if opts.Module != "" {
		if !strings.HasPrefix(entry.Module, opts.Module) {
			return false
		}
	}

	return true
}

// deduplicationKey creates a composite key from a LogEntry for deduplication.
// Uses nanosecond timestamp + message + operation to identify unique entries.
func deduplicationKey(entry *LogEntry) string {
	return fmt.Sprintf("%d|%s|%s", entry.Time.UnixNano(), entry.Msg, entry.Operation)
}
