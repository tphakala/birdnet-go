package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDate is the reference date used across tests.
var testDate = time.Date(2025, 2, 28, 0, 0, 0, 0, time.UTC)

// writeJSONLFile writes lines to a temporary JSONL file and returns its path.
func writeJSONLFile(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	err := os.WriteFile(path, []byte(content), 0o644)
	require.NoError(t, err, "writing test JSONL file")
	return path
}

// sampleLine builds a JSONL line for the given timestamp, level, message, module, operation, and optional extra JSON fields.
func sampleLine(ts time.Time, level, msg, module, operation, extra string) string {
	base := fmt.Sprintf(`{"time":%q,"level":%q,"msg":%q,"module":%q,"operation":%q`,
		ts.Format(time.RFC3339Nano), level, msg, module, operation)
	if extra != "" {
		base += "," + extra
	}
	base += "}"
	return base
}

func TestReadFile_BasicFiltering(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	targetDay := testDate
	otherDay := testDate.AddDate(0, 0, -1) // day before

	lines := []string{
		sampleLine(targetDay.Add(10*time.Hour), "INFO", "bird detected", "analysis", "detect", ""),
		sampleLine(targetDay.Add(11*time.Hour), "INFO", "bird approved", "analysis", "approve", ""),
		sampleLine(otherDay.Add(5*time.Hour), "INFO", "old entry", "analysis", "detect", ""),
	}

	path := writeJSONLFile(t, dir, "test.log", lines)

	entries, err := ReadFile(path, &ReadOptions{Date: targetDay})
	require.NoError(t, err)
	assert.Len(t, entries, 2, "should only return entries for the target date")
	assert.Equal(t, "bird detected", entries[0].Msg)
	assert.Equal(t, "bird approved", entries[1].Msg)
}

func TestReadFile_OperationFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	ts := testDate.Add(10 * time.Hour)
	lines := []string{
		sampleLine(ts, "INFO", "detected", "analysis", "detect", ""),
		sampleLine(ts.Add(time.Minute), "INFO", "approved", "analysis", "approve", ""),
		sampleLine(ts.Add(2*time.Minute), "INFO", "rejected", "analysis", "reject", ""),
	}

	path := writeJSONLFile(t, dir, "test.log", lines)

	entries, err := ReadFile(path, &ReadOptions{
		Date:       testDate,
		Operations: []string{"detect", "reject"},
	})
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, "detected", entries[0].Msg)
	assert.Equal(t, "rejected", entries[1].Msg)
}

func TestReadFile_LevelFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	ts := testDate.Add(10 * time.Hour)
	lines := []string{
		sampleLine(ts, "DEBUG", "debug msg", "core", "op1", ""),
		sampleLine(ts.Add(time.Second), "INFO", "info msg", "core", "op2", ""),
		sampleLine(ts.Add(2*time.Second), "WARN", "warn msg", "core", "op3", ""),
		sampleLine(ts.Add(3*time.Second), "ERROR", "error msg", "core", "op4", ""),
	}

	path := writeJSONLFile(t, dir, "test.log", lines)

	tests := []struct {
		name     string
		level    string
		wantMsgs []string
	}{
		{
			name:     "minimum INFO excludes DEBUG",
			level:    "INFO",
			wantMsgs: []string{"info msg", "warn msg", "error msg"},
		},
		{
			name:     "minimum WARN excludes DEBUG and INFO",
			level:    "WARN",
			wantMsgs: []string{"warn msg", "error msg"},
		},
		{
			name:     "minimum ERROR excludes everything below",
			level:    "ERROR",
			wantMsgs: []string{"error msg"},
		},
		{
			name:     "minimum DEBUG includes all",
			level:    "DEBUG",
			wantMsgs: []string{"debug msg", "info msg", "warn msg", "error msg"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entries, err := ReadFile(path, &ReadOptions{
				Date:  testDate,
				Level: tt.level,
			})
			require.NoError(t, err)
			require.Len(t, entries, len(tt.wantMsgs))
			for i, wantMsg := range tt.wantMsgs {
				assert.Equal(t, wantMsg, entries[i].Msg)
			}
		})
	}
}

func TestReadFile_ModuleFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	ts := testDate.Add(10 * time.Hour)
	lines := []string{
		sampleLine(ts, "INFO", "msg1", "analysis.processor", "op1", ""),
		sampleLine(ts.Add(time.Second), "INFO", "msg2", "analysis.filter", "op2", ""),
		sampleLine(ts.Add(2*time.Second), "INFO", "msg3", "weather.fetch", "op3", ""),
	}

	path := writeJSONLFile(t, dir, "test.log", lines)

	entries, err := ReadFile(path, &ReadOptions{
		Date:   testDate,
		Module: "analysis",
	})
	require.NoError(t, err)
	assert.Len(t, entries, 2, "should match modules with 'analysis' prefix")
	assert.Equal(t, "msg1", entries[0].Msg)
	assert.Equal(t, "msg2", entries[1].Msg)
}

func TestReadFile_PreservesAllFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	ts := testDate.Add(10 * time.Hour)
	lines := []string{
		sampleLine(ts, "INFO", "Detection approved", "analysis.processor", "approve_detection",
			`"species":"Parus major","confidence":0.85,"source":"BirdNET-1"`),
	}

	path := writeJSONLFile(t, dir, "test.log", lines)

	entries, err := ReadFile(path, &ReadOptions{Date: testDate})
	require.NoError(t, err)
	require.Len(t, entries, 1)

	entry := entries[0]
	assert.Equal(t, "Detection approved", entry.Msg)
	assert.Equal(t, "analysis.processor", entry.Module)
	assert.Equal(t, "approve_detection", entry.Operation)

	// Verify extra fields are preserved in the Fields map.
	require.NotNil(t, entry.Fields)
	assert.Equal(t, "Parus major", entry.Fields["species"])
	assert.InDelta(t, 0.85, entry.Fields["confidence"], 0.001)
	assert.Equal(t, "BirdNET-1", entry.Fields["source"])

	// Known fields should NOT appear in the Fields map.
	_, hasTime := entry.Fields["time"]
	_, hasLevel := entry.Fields["level"]
	_, hasMsg := entry.Fields["msg"]
	assert.False(t, hasTime, "time should not be in Fields")
	assert.False(t, hasLevel, "level should not be in Fields")
	assert.False(t, hasMsg, "msg should not be in Fields")
}

func TestReadFile_MalformedLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	ts := testDate.Add(10 * time.Hour)
	lines := []string{
		"this is not json",
		"",
		`{"time":"invalid-time","level":"INFO","msg":"bad time"}`,
		`{"level":"INFO","msg":"missing time field"}`,
		sampleLine(ts, "INFO", "valid entry", "core", "op1", ""),
		`{broken json`,
	}

	path := writeJSONLFile(t, dir, "test.log", lines)

	entries, err := ReadFile(path, &ReadOptions{Date: testDate})
	require.NoError(t, err, "malformed lines should be skipped, not cause errors")
	assert.Len(t, entries, 1, "only the valid entry should be returned")
	assert.Equal(t, "valid entry", entries[0].Msg)
}

func TestReadFiles_Deduplication(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	ts1 := testDate.Add(10 * time.Hour)
	ts2 := testDate.Add(11 * time.Hour)

	// Same entry appears in both files (simulating rotation overlap).
	duplicateLine := sampleLine(ts1, "INFO", "duplicate entry", "core", "op1", "")
	uniqueLine1 := sampleLine(ts1.Add(time.Minute), "INFO", "unique to file1", "core", "op2", "")
	uniqueLine2 := sampleLine(ts2, "INFO", "unique to file2", "core", "op3", "")

	path1 := writeJSONLFile(t, dir, "actions.log", []string{duplicateLine, uniqueLine1})
	path2 := writeJSONLFile(t, dir, "actions-2025-02-28T09-00-00Z.log", []string{duplicateLine, uniqueLine2})

	entries, err := ReadFiles([]string{path1, path2}, &ReadOptions{Date: testDate})
	require.NoError(t, err)
	assert.Len(t, entries, 3, "duplicate should appear only once")
}

func TestReadFiles_SortsByTimestamp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	ts1 := testDate.Add(12 * time.Hour) // noon
	ts2 := testDate.Add(8 * time.Hour)  // morning
	ts3 := testDate.Add(18 * time.Hour) // evening

	// File 1 has the noon entry.
	path1 := writeJSONLFile(t, dir, "file1.log", []string{
		sampleLine(ts1, "INFO", "noon", "core", "op1", ""),
	})
	// File 2 has morning and evening entries (out of order across files).
	path2 := writeJSONLFile(t, dir, "file2.log", []string{
		sampleLine(ts3, "INFO", "evening", "core", "op3", ""),
		sampleLine(ts2, "INFO", "morning", "core", "op2", ""),
	})

	entries, err := ReadFiles([]string{path1, path2}, &ReadOptions{Date: testDate})
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, "morning", entries[0].Msg)
	assert.Equal(t, "noon", entries[1].Msg)
	assert.Equal(t, "evening", entries[2].Msg)
}

func TestFindLogFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create an active log file and some rotated files.
	activeFile := filepath.Join(dir, "actions.log")
	rotated1 := filepath.Join(dir, "actions-2025-02-27T10-00-00Z.log")
	rotated2 := filepath.Join(dir, "actions-2025-02-28T08-00-00Z.log")
	compressed := filepath.Join(dir, "actions-2025-02-26T10-00-00Z.log.gz")
	unrelated := filepath.Join(dir, "other.log")

	for _, path := range []string{activeFile, rotated1, rotated2, compressed, unrelated} {
		err := os.WriteFile(path, []byte("{}"), 0o644)
		require.NoError(t, err)
	}

	files, err := FindLogFiles(activeFile)
	require.NoError(t, err)

	// Should find active + 2 rotated, but NOT the .gz or unrelated file.
	assert.Len(t, files, 3)
	assert.Contains(t, files, activeFile)
	assert.Contains(t, files, rotated1)
	assert.Contains(t, files, rotated2)
	assert.NotContains(t, files, compressed, "compressed files should be excluded")
	assert.NotContains(t, files, unrelated, "unrelated files should be excluded")
}

func TestFindLogFiles_NoActiveFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Only rotated files exist, no active file.
	rotated := filepath.Join(dir, "actions-2025-02-28T08-00-00Z.log")
	err := os.WriteFile(rotated, []byte("{}"), 0o644)
	require.NoError(t, err)

	activeFile := filepath.Join(dir, "actions.log")
	files, err := FindLogFiles(activeFile)
	require.NoError(t, err)

	assert.Len(t, files, 1)
	assert.Contains(t, files, rotated)
}

func TestLevelParsing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		entryLevel string
		minLevel   string
		shouldPass bool
	}{
		{name: "DEBUG >= DEBUG", entryLevel: "DEBUG", minLevel: "DEBUG", shouldPass: true},
		{name: "INFO >= DEBUG", entryLevel: "INFO", minLevel: "DEBUG", shouldPass: true},
		{name: "WARN >= DEBUG", entryLevel: "WARN", minLevel: "DEBUG", shouldPass: true},
		{name: "ERROR >= DEBUG", entryLevel: "ERROR", minLevel: "DEBUG", shouldPass: true},
		{name: "DEBUG < INFO", entryLevel: "DEBUG", minLevel: "INFO", shouldPass: false},
		{name: "INFO >= INFO", entryLevel: "INFO", minLevel: "INFO", shouldPass: true},
		{name: "DEBUG < WARN", entryLevel: "DEBUG", minLevel: "WARN", shouldPass: false},
		{name: "INFO < WARN", entryLevel: "INFO", minLevel: "WARN", shouldPass: false},
		{name: "WARN >= WARN", entryLevel: "WARN", minLevel: "WARN", shouldPass: true},
		{name: "ERROR >= WARN", entryLevel: "ERROR", minLevel: "WARN", shouldPass: true},
		{name: "DEBUG < ERROR", entryLevel: "DEBUG", minLevel: "ERROR", shouldPass: false},
		{name: "WARN < ERROR", entryLevel: "WARN", minLevel: "ERROR", shouldPass: false},
		{name: "ERROR >= ERROR", entryLevel: "ERROR", minLevel: "ERROR", shouldPass: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entry := LogEntry{
				Time:  testDate.Add(10 * time.Hour),
				Level: tt.entryLevel,
			}
			opts := ReadOptions{
				Date:  testDate,
				Level: tt.minLevel,
			}
			prep := prepareOptions(&opts)
			result := matchesOptions(&entry, &prep)
			assert.Equal(t, tt.shouldPass, result)
		})
	}
}

func TestReadFile_EmptyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	path := writeJSONLFile(t, dir, "empty.log", []string{})

	entries, err := ReadFile(path, &ReadOptions{Date: testDate})
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestReadFile_NonExistentFile(t *testing.T) {
	t.Parallel()

	_, err := ReadFile("/nonexistent/path/file.log", &ReadOptions{Date: testDate})
	require.Error(t, err)
}

func TestReadFile_DateFilterRespectsLocation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Simulate a UTC+10 timezone (e.g., Australia/Brisbane)
	loc := time.FixedZone("UTC+10", 10*60*60)

	// Entry at UTC 2025-02-28 23:30:00 = Mar 1 09:30 in UTC+10
	entryTime := time.Date(2025, 2, 28, 23, 30, 0, 0, time.UTC)
	path := writeJSONLFile(t, dir, "tz-test.log", []string{
		sampleLine(entryTime, "INFO", "late night entry", "test", "test_op", ""),
	})

	// In UTC+10, this entry is on Mar 1. Query for Mar 1 in UTC+10.
	mar1InLoc := time.Date(2025, 3, 1, 0, 0, 0, 0, loc)

	// With Location=loc, should find the entry (it's Mar 1 in UTC+10)
	entries, err := ReadFile(path, &ReadOptions{Date: mar1InLoc, Location: loc})
	require.NoError(t, err)
	assert.Len(t, entries, 1, "should find entry that is Mar 1 in UTC+10")

	// Query for Mar 1 in UTC — the entry is Feb 28 in UTC, so it should NOT match.
	mar1UTC := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	entries, err = ReadFile(path, &ReadOptions{Date: mar1UTC, Location: time.UTC})
	require.NoError(t, err)
	assert.Empty(t, entries, "should not find entry when comparing in UTC (entry is Feb 28 UTC)")

	// With Location=nil (backward compat, defaults to UTC), same result.
	entries, err = ReadFile(path, &ReadOptions{Date: mar1UTC})
	require.NoError(t, err)
	assert.Empty(t, entries, "nil Location should default to UTC for backward compat")
}

func TestReadFiles_EmptyPaths(t *testing.T) {
	t.Parallel()

	entries, err := ReadFiles([]string{}, &ReadOptions{Date: testDate})
	require.NoError(t, err)
	assert.Empty(t, entries)
}
