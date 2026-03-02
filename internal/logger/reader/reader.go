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
	Date       time.Time      // Optional: only return entries for this date. If zero, no date filtering is applied.
	Location   *time.Location // Optional: timezone for date comparison. If nil, defaults to UTC.
	Operations []string       // Optional: filter to these operation values.
	Level      string         // Optional: minimum level (DEBUG, INFO, WARN, ERROR).
	Module     string         // Optional: filter to this module prefix.
}

// ReadFile reads a JSONL log file and returns entries matching the options.
// Malformed lines are silently skipped. The returned entries are not sorted;
// use ReadFiles for merged, sorted, deduplicated results across multiple files.
// If opts is nil, all entries are returned without filtering.
func ReadFile(path string, opts *ReadOptions) ([]LogEntry, error) {
	if opts == nil {
		opts = &ReadOptions{}
	}
	f, err := os.Open(path) //nolint:gosec // path comes from caller / FindLogFiles
	if err != nil {
		return nil, fmt.Errorf("opening log file %s: %w", path, err)
	}
	defer func() {
		_ = f.Close()
	}()

	var entries []LogEntry
	prep := prepareOptions(opts)

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

		if matchesOptions(&entry, &prep) {
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

	// Deduplicate in-place by composite key: timestamp (UnixNano) + msg + operation.
	seen := make(map[dedupKey]struct{}, len(allEntries))
	n := 0
	for i := range allEntries {
		key := deduplicationKey(&allEntries[i])
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			allEntries[n] = allEntries[i]
			n++
		}
	}
	deduplicated := allEntries[:n]

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
	delete(raw, "time")

	// Extract typed fields, removing them from the map.
	if v, ok := raw["level"].(string); ok {
		entry.Level = v
	}
	delete(raw, "level")

	if v, ok := raw["msg"].(string); ok {
		entry.Msg = v
	}
	delete(raw, "msg")

	if v, ok := raw["module"].(string); ok {
		entry.Module = v
	}
	delete(raw, "module")

	if v, ok := raw["operation"].(string); ok {
		entry.Operation = v
	}
	delete(raw, "operation")

	// Remaining keys are extra fields.
	if len(raw) > 0 {
		entry.Fields = raw
	}

	return entry, true
}

// preparedOptions holds precomputed filter values to avoid repeated work per entry.
type preparedOptions struct {
	targetYear  int
	targetMonth time.Month
	targetDay   int
	location    *time.Location
	hasDate     bool
	minRank     int
	hasLevel    bool
	operations  []string
	module      string
}

// prepareOptions precomputes filter values from ReadOptions.
func prepareOptions(opts *ReadOptions) preparedOptions {
	p := preparedOptions{
		operations: opts.Operations,
		module:     opts.Module,
	}
	if !opts.Date.IsZero() {
		p.hasDate = true
		loc := opts.Location
		if loc == nil {
			loc = time.UTC
		}
		p.location = loc
		p.targetYear, p.targetMonth, p.targetDay = opts.Date.In(loc).Date()
	}
	if opts.Level != "" {
		if rank, ok := levelRank[strings.ToUpper(opts.Level)]; ok {
			p.hasLevel = true
			p.minRank = rank
		}
	}
	return p
}

// matchesOptions checks whether a log entry passes all the precomputed filters.
func matchesOptions(entry *LogEntry, prep *preparedOptions) bool {
	// Date filter: compare dates in the configured timezone.
	if prep.hasDate {
		y, m, d := entry.Time.In(prep.location).Date()
		if y != prep.targetYear || m != prep.targetMonth || d != prep.targetDay {
			return false
		}
	}

	// Level filter: entry must be at or above the minimum level.
	// Entries with unknown levels are excluded when a level filter is active.
	if prep.hasLevel {
		entryRank, entryOK := levelRank[strings.ToUpper(entry.Level)]
		if !entryOK || entryRank < prep.minRank {
			return false
		}
	}

	// Operation filter: entry operation must be in the allowed set.
	if len(prep.operations) > 0 {
		if !slices.Contains(prep.operations, entry.Operation) {
			return false
		}
	}

	// Module filter: entry module must have the specified prefix.
	if prep.module != "" {
		if !strings.HasPrefix(entry.Module, prep.module) {
			return false
		}
	}

	return true
}

// dedupKey is a composite key for deduplication, avoiding string allocation.
type dedupKey struct {
	timeNano  int64
	msg       string
	operation string
}

// deduplicationKey creates a composite key from a LogEntry for deduplication.
func deduplicationKey(entry *LogEntry) dedupKey {
	return dedupKey{
		timeNano:  entry.Time.UnixNano(),
		msg:       entry.Msg,
		operation: entry.Operation,
	}
}
