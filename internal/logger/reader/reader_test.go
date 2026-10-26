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

	"github.com/tphakala/birdnet-go/internal/testutil"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

// testDate is the reference date used across tests.
var testDate = time.Date(2025, 2, 28, 0, 0, 0, 0, time.UTC)

// testWallDate is testDate as a calendar date, the form ReadOptions.Date takes.
var testWallDate = wallclock.DateOf(testDate)

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

	entries, err := ReadFile(path, &ReadOptions{Date: wallclock.DateOf(targetDay)})
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
		Date:       testWallDate,
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
				Date:  testWallDate,
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
		Date:   testWallDate,
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

	entries, err := ReadFile(path, &ReadOptions{Date: testWallDate})
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

	entries, err := ReadFile(path, &ReadOptions{Date: testWallDate})
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

	entries, err := ReadFiles([]string{path1, path2}, &ReadOptions{Date: testWallDate})
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

	entries, err := ReadFiles([]string{path1, path2}, &ReadOptions{Date: testWallDate})
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
				Date:  testWallDate,
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

	entries, err := ReadFile(path, &ReadOptions{Date: testWallDate})
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestReadFile_NonExistentFile(t *testing.T) {
	t.Parallel()

	_, err := ReadFile("/nonexistent/path/file.log", &ReadOptions{Date: testWallDate})
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
	entries, err := ReadFile(path, &ReadOptions{Date: wallclock.DateOf(mar1InLoc), Location: loc})
	require.NoError(t, err)
	assert.Len(t, entries, 1, "should find entry that is Mar 1 in UTC+10")

	// Query for Mar 1 in UTC — the entry is Feb 28 in UTC, so it should NOT match.
	mar1UTC := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	entries, err = ReadFile(path, &ReadOptions{Date: wallclock.DateOf(mar1UTC), Location: time.UTC})
	require.NoError(t, err)
	assert.Empty(t, entries, "should not find entry when comparing in UTC (entry is Feb 28 UTC)")

	// With Location=nil (backward compat, defaults to UTC), same result.
	entries, err = ReadFile(path, &ReadOptions{Date: wallclock.DateOf(mar1UTC)})
	require.NoError(t, err)
	assert.Empty(t, entries, "nil Location should default to UTC for backward compat")
}

func TestReadFile_DateFilterRespectsNegativeOffset(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Simulate a UTC-5 timezone (e.g., US Eastern)
	loc := time.FixedZone("UTC-5", -5*60*60)

	// Entry at UTC 2025-03-01 03:00:00 = Feb 28 22:00 in UTC-5
	entryTime := time.Date(2025, 3, 1, 3, 0, 0, 0, time.UTC)
	path := writeJSONLFile(t, dir, "tz-neg-test.log", []string{
		sampleLine(entryTime, "INFO", "early morning UTC entry", "test", "test_op", ""),
	})

	// In UTC-5, this entry is on Feb 28. Query for Mar 1 in UTC-5 should NOT find it.
	mar1InLoc := time.Date(2025, 3, 1, 0, 0, 0, 0, loc)
	entries, err := ReadFile(path, &ReadOptions{Date: wallclock.DateOf(mar1InLoc), Location: loc})
	require.NoError(t, err)
	assert.Empty(t, entries, "entry is Feb 28 in UTC-5, should not match Mar 1 query")

	// Query for Feb 28 in UTC-5 should find it.
	feb28InLoc := time.Date(2025, 2, 28, 0, 0, 0, 0, loc)
	entries, err = ReadFile(path, &ReadOptions{Date: wallclock.DateOf(feb28InLoc), Location: loc})
	require.NoError(t, err)
	assert.Len(t, entries, 1, "entry is Feb 28 in UTC-5, should match Feb 28 query")

	// In UTC, this entry is Mar 1. Query for Mar 1 in UTC should find it.
	mar1UTC := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	entries, err = ReadFile(path, &ReadOptions{Date: wallclock.DateOf(mar1UTC), Location: time.UTC})
	require.NoError(t, err)
	assert.Len(t, entries, 1, "entry is Mar 1 in UTC, should match Mar 1 UTC query")
}

func TestReadFiles_EmptyPaths(t *testing.T) {
	t.Parallel()

	entries, err := ReadFiles([]string{}, &ReadOptions{Date: testWallDate})
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestReadFile_DateFilterSkippedMidnight pins that a date selects the entries whose local
// calendar date it is, also in a zone whose local midnight does not exist on that date.
func TestReadFile_DateFilterSkippedMidnight(t *testing.T) {
	t.Parallel()

	zones := testutil.ToolchainZones(t)
	tests := []struct {
		zone       string
		before     time.Time // late on the day before the skipped midnight
		after      time.Time // early on the day that has no midnight
		targetDate wallclock.Date
	}{
		{
			zone:       "America/Havana",
			before:     time.Date(2026, time.March, 7, 23, 30, 0, 0, zones["America/Havana"]),
			after:      time.Date(2026, time.March, 8, 1, 30, 0, 0, zones["America/Havana"]),
			targetDate: wallclock.Date{Year: 2026, Month: time.March, Day: 8},
		},
		{
			zone:       "America/Santiago",
			before:     time.Date(2026, time.September, 5, 23, 30, 0, 0, zones["America/Santiago"]),
			after:      time.Date(2026, time.September, 6, 1, 30, 0, 0, zones["America/Santiago"]),
			targetDate: wallclock.Date{Year: 2026, Month: time.September, Day: 6},
		},
	}
	for _, tt := range tests {
		t.Run(tt.zone, func(t *testing.T) {
			t.Parallel()
			loc := zones[tt.zone]
			require.NotNil(t, loc)
			path := writeJSONLFile(t, t.TempDir(), "skipped.log", []string{
				sampleLine(tt.before, "INFO", "before", "test", "test_op", ""),
				sampleLine(tt.after, "INFO", "after", "test", "test_op", ""),
			})

			entries, err := ReadFile(path, &ReadOptions{Date: tt.targetDate, Location: loc})
			require.NoError(t, err)
			require.Len(t, entries, 1, "only the entry of the requested local date")
			assert.Equal(t, "after", entries[0].Msg)

			entries, err = ReadFile(path, &ReadOptions{Date: tt.targetDate.AddDays(-1), Location: loc})
			require.NoError(t, err)
			require.Len(t, entries, 1, "the day before has the other entry")
			assert.Equal(t, "before", entries[0].Msg)
		})
	}
}

// TestReadFile_DateFilterNormalisesDate pins that a Date with out-of-range fields selects the
// date it normalises to, as time.Date does, instead of matching no entry.
func TestReadFile_DateFilterNormalisesDate(t *testing.T) {
	t.Parallel()

	path := writeJSONLFile(t, t.TempDir(), "normalise.log", []string{
		sampleLine(time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC), "INFO", "april first", "test", "test_op", ""),
		sampleLine(time.Date(2026, time.March, 31, 12, 0, 0, 0, time.UTC), "INFO", "march last", "test", "test_op", ""),
	})

	// March 32 is April 1.
	entries, err := ReadFile(path, &ReadOptions{Date: wallclock.Date{Year: 2026, Month: time.March, Day: 32}})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "april first", entries[0].Msg)
}

// TestReadFile_ZeroDateDisablesDateFilter pins that an unset Date returns entries of every day.
func TestReadFile_ZeroDateDisablesDateFilter(t *testing.T) {
	t.Parallel()

	path := writeJSONLFile(t, t.TempDir(), "nodate.log", []string{
		sampleLine(testDate.Add(10*time.Hour), "INFO", "day one", "test", "test_op", ""),
		sampleLine(testDate.AddDate(0, 0, 5).Add(10*time.Hour), "INFO", "day six", "test", "test_op", ""),
	})

	entries, err := ReadFile(path, &ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, entries, 2, "no Date means no date filtering")
}
