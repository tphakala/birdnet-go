package support

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the behavior introduced by the large-log timeout fix:
// tail truncation (seekToTail), the deadline-aware graceful partial dump
// (nearDeadline), the 1 MiB scanner buffer (long structured log lines), and
// that the collection count reads the same tail the archive ships.

// TestSeekToTail verifies whole-file vs tail reads and line-boundary alignment.
func TestSeekToTail(t *testing.T) {
	t.Parallel()

	// Four 5-byte lines (incl. newline): "AAAA\nBBBB\nCCCC\nDDDD\n" = 20 bytes.
	content := []byte("AAAA\nBBBB\nCCCC\nDDDD\n")

	writeTemp := func(t *testing.T, b []byte) *os.File {
		t.Helper()
		p := filepath.Join(t.TempDir(), "f.log")
		require.NoError(t, os.WriteFile(p, b, 0o644))
		f, err := os.Open(p) //nolint:gosec // test path
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, f.Close()) })
		return f
	}

	read := func(t *testing.T, f *os.File, size, maxBytes int64) string {
		t.Helper()
		r, err := seekToTail(f, size, maxBytes)
		require.NoError(t, err)
		var buf bytes.Buffer
		_, err = buf.ReadFrom(r)
		require.NoError(t, err)
		return buf.String()
	}

	t.Run("maxBytes<=0 reads whole file", func(t *testing.T) {
		t.Parallel()
		got := read(t, writeTemp(t, content), int64(len(content)), 0)
		assert.Equal(t, string(content), got)
	})

	t.Run("file smaller than budget reads whole file", func(t *testing.T) {
		t.Parallel()
		got := read(t, writeTemp(t, content), int64(len(content)), 1000)
		assert.Equal(t, string(content), got)
	})

	t.Run("oversized reads line-aligned tail", func(t *testing.T) {
		t.Parallel()
		// maxBytes 9 -> seek to offset 11 (inside CCCC\n); the partial first line
		// is discarded, leaving the final whole line only.
		got := read(t, writeTemp(t, content), int64(len(content)), 9)
		assert.NotContains(t, got, "AAAA")
		assert.NotContains(t, got, "BBBB")
		assert.Contains(t, got, "DDDD")
		assert.True(t, strings.HasSuffix(got, "\n"))
	})

	t.Run("tail with no newline yields empty", func(t *testing.T) {
		t.Parallel()
		// One long line, no trailing newline: discarding the partial first line
		// consumes everything, so nothing is returned.
		big := bytes.Repeat([]byte("x"), 1000)
		got := read(t, writeTemp(t, big), int64(len(big)), 100)
		assert.Empty(t, got)
	})
}

// recentLogLine builds a JSON log line with a timestamp `age` before now.
func recentLogLine(age time.Duration, msg string) string {
	ts := time.Now().Add(-age).Format(time.RFC3339)
	return fmt.Sprintf(`{"time":%q,"level":"INFO","msg":%q}`, ts, msg) + "\n"
}

// TestCountLogFileReadsTailWhenOversized proves the collection count reads the
// recent TAIL of an oversized file (matching the archive), not the old head, so
// diagnostics describe the shipped bytes.
func TestCountLogFileReadsTailWhenOversized(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	logsDir := filepath.Join(tmp, "logs")
	require.NoError(t, os.MkdirAll(logsDir, 0o755))

	oldAge := 20 * 24 * time.Hour // within the 30d window, but older than the tail
	newAge := 1 * time.Hour
	head := recentLogLine(oldAge, "old head entry padding padding padding")
	tail := recentLogLine(newAge, "new tail entry")

	var b bytes.Buffer
	for b.Len() < 2<<20 { // ~2 MB of old head lines
		b.WriteString(head)
	}
	tailStart := b.Len()
	for b.Len() < tailStart+(1<<20) { // ~1 MB of new tail lines
		b.WriteString(tail)
	}
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "birdnet.log"), b.Bytes(), 0o644))

	c := NewCollector(tmp, tmp, "TAIL-TEST", "v0")
	var acc logScanAccum
	diag := &LogSourceDiagnostics{PathsSearched: []SearchedPath{}, Details: map[string]any{}}
	// Budget 400 KB << the 1 MB new-tail region, so the last 400 KB are all new.
	err := c.collectLogFilesWithDiagnostics(t.Context(), 30*24*time.Hour, 400<<10, &acc, diag)
	require.NoError(t, err)

	require.Positive(t, acc.entries, "tail entries should be counted")
	assert.LessOrEqual(t, acc.size, int64(400<<10)+int64(len(tail)), "count bounded by budget")
	oldTs := time.Now().Add(-oldAge)
	assert.True(t, acc.earliest.After(oldTs),
		"earliest counted entry should be from the recent tail, not the old head (earliest=%s)", acc.earliest)
}

// TestCountLogFileCountsLongLine guards the 1 MiB scanner buffer: a single log
// line larger than bufio.MaxScanTokenSize (64 KB) must still be scanned and
// counted, not silently dropped with bufio.ErrTooLong.
func TestCountLogFileCountsLongLine(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	logsDir := filepath.Join(tmp, "logs")
	require.NoError(t, os.MkdirAll(logsDir, 0o755))

	longMsg := strings.Repeat("x", 200_000) // ~200 KB, well over the 64 KB default
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "spectrogram.log"),
		[]byte(recentLogLine(time.Hour, longMsg)), 0o644))

	c := NewCollector(tmp, tmp, "LONGLINE-TEST", "v0")
	var acc logScanAccum
	diag := &LogSourceDiagnostics{PathsSearched: []SearchedPath{}, Details: map[string]any{}}
	err := c.collectLogFilesWithDiagnostics(t.Context(), 30*24*time.Hour, 50<<20, &acc, diag)
	require.NoError(t, err)
	assert.Equal(t, 1, acc.entries, "the >64KB line must be counted (1 MiB buffer)")
}

// TestSupportDumpGracefulNearDeadline is the safety net for the #3766 failure:
// when the context is already within the deadline safety margin, Collect and
// CreateArchive must return WITHOUT error and produce a valid (partial) archive
// rather than blowing the write deadline and closing the connection.
func TestSupportDumpGracefulNearDeadline(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	logsDir := filepath.Join(tmp, "logs")
	require.NoError(t, os.MkdirAll(logsDir, 0o755))
	var b bytes.Buffer
	for b.Len() < 8<<20 { // ~8 MB so full processing would take real time
		b.WriteString(recentLogLine(time.Hour, "detection processed"))
	}
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "birdnet.log"), b.Bytes(), 0o644))

	c := NewCollector(tmp, tmp, "DEADLINE-TEST", "v0")
	opts := CollectorOptions{
		IncludeLogs: true, IncludeSystemInfo: true,
		LogDuration: 30 * 24 * time.Hour, MaxLogSize: 50 << 20,
		ScrubSensitive: true, AnonymizePII: true,
	}
	// Deadline inside the safety margin => nearDeadline is immediately true.
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Second))
	defer cancel()

	dump, err := c.Collect(ctx, opts)
	require.NoError(t, err, "Collect must not error when near deadline")
	require.NotNil(t, dump)
	data, err := c.CreateArchive(ctx, dump, opts)
	require.NoError(t, err, "CreateArchive must not error when near deadline")

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err, "archive must be a valid zip even when the deadline is hit")
	names := zipEntryNames(zr)
	assert.Contains(t, names, "metadata.json")
	assert.Contains(t, names, "collection_diagnostics.json")
}

// TestSupportDumpArchiveTruncatesOversizedLog verifies an oversized log file is
// included as a line-aligned recent tail rather than skipped entirely.
func TestSupportDumpArchiveTruncatesOversizedLog(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	logsDir := filepath.Join(tmp, "logs")
	require.NoError(t, os.MkdirAll(logsDir, 0o755))
	var b bytes.Buffer
	for b.Len() < 3<<20 { // 3 MB source, larger than the cap below
		b.WriteString(recentLogLine(time.Hour, "detection processed species"))
	}
	srcSize := b.Len()
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "birdnet.log"), b.Bytes(), 0o644))

	c := NewCollector(tmp, tmp, "TRUNC-TEST", "v0")
	opts := CollectorOptions{
		IncludeLogs: true,
		LogDuration: 30 * 24 * time.Hour, MaxLogSize: 512 << 10, // 512 KB cap < 3 MB
		AnonymizePII: true,
	}
	dump, err := c.Collect(t.Context(), opts)
	require.NoError(t, err)
	data, err := c.CreateArchive(t.Context(), dump, opts)
	require.NoError(t, err)

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	var entry *zip.File
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "birdnet.log") {
			entry = f
		}
	}
	require.NotNil(t, entry, "oversized log must be included (tail), not skipped; entries=%v", zipEntryNames(zr))
	assert.Less(t, entry.UncompressedSize64, uint64(srcSize), "archived content should be a truncated tail")

	rc, err := entry.Open()
	require.NoError(t, err)
	defer func() { assert.NoError(t, rc.Close()) }()
	tailContent, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NotEmpty(t, tailContent)
	assert.Equal(t, byte('{'), tailContent[0], "archived tail must start on a JSON log-line boundary")
}

// TestSupportDumpArchiveCollectsAllLogsWhenOneIsHuge is the regression guard for
// issue #3902: one very large log file (access.log) that sorts alphabetically
// BEFORE the file the maintainer actually needs (audio.log) consumed the entire
// shared size budget, so the collector stopped before adding any later file.
// Every eligible log file must appear in the archive with (at least) its recent
// tail; a single large file must never starve the others.
func TestSupportDumpArchiveCollectsAllLogsWhenOneIsHuge(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	logsDir := filepath.Join(tmp, "logs")
	require.NoError(t, os.MkdirAll(logsDir, 0o755))

	// access.log: large, and sorts before audio.log. Under the old shared-budget
	// logic its tail alone filled the cap and every later file was skipped.
	var big bytes.Buffer
	for big.Len() < 4<<20 { // 4 MB, far larger than the cap below
		big.WriteString(recentLogLine(time.Hour, "web access GET /api/v2/detections"))
	}
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "access.log"), big.Bytes(), 0o644))

	// audio.log: the small file needed to diagnose the RTSP/FFmpeg failure.
	audioLine := recentLogLine(time.Hour, "ffmpeg rtsp connection failed 401 unauthorized")
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "audio.log"), []byte(audioLine), 0o644))

	c := NewCollector(tmp, tmp, "ALLLOGS-TEST", "v0")
	opts := CollectorOptions{
		IncludeLogs:  true,
		LogDuration:  30 * 24 * time.Hour,
		MaxLogSize:   512 << 10, // 512 KB, smaller than access.log alone
		AnonymizePII: false,
	}
	dump, err := c.Collect(t.Context(), opts)
	require.NoError(t, err)
	data, err := c.CreateArchive(t.Context(), dump, opts)
	require.NoError(t, err)

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	names := zipEntryNames(zr)
	assert.Contains(t, names, "logs/access.log", "large early-alphabet log should still be present as a tail")
	require.Contains(t, names, "logs/audio.log",
		"small later-alphabet log MUST NOT be starved by the large one; entries=%v", names)

	// audio.log is tiny, so it must be captured in full.
	var audioEntry *zip.File
	for _, f := range zr.File {
		if f.Name == "logs/audio.log" {
			audioEntry = f
		}
	}
	require.NotNil(t, audioEntry)
	rc, err := audioEntry.Open()
	require.NoError(t, err)
	defer func() { assert.NoError(t, rc.Close()) }()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, audioLine, string(got), "small audio.log must be captured in full, not starved")
}

// TestAllocateTailBudgets pins down the fair-share allocator that prevents one
// large log file from starving the others (issue #3902).
func TestAllocateTailBudgets(t *testing.T) {
	t.Parallel()

	sum := func(ts []logFileTarget) int64 {
		var s int64
		for i := range ts {
			s += ts[i].budget
		}
		return s
	}

	t.Run("all files larger than fair share split evenly", func(t *testing.T) {
		t.Parallel()
		// Two 10 MB files, 4 MB budget => 2 MB each; neither starves.
		targets := []logFileTarget{{size: 10 << 20}, {size: 10 << 20}}
		allocateTailBudgets(targets, 4<<20)
		assert.Equal(t, int64(2<<20), targets[0].budget)
		assert.Equal(t, int64(2<<20), targets[1].budget)
		assert.LessOrEqual(t, sum(targets), int64(4<<20), "total must not exceed budget")
	})

	t.Run("small file taken whole, surplus rolls to large file", func(t *testing.T) {
		t.Parallel()
		// Sorted ascending: a tiny file then a huge one, 1 MB budget.
		targets := []logFileTarget{{size: 100}, {size: 10 << 20}}
		allocateTailBudgets(targets, 1<<20)
		assert.Equal(t, int64(100), targets[0].budget, "tiny file taken in full")
		assert.Equal(t, int64(1<<20)-100, targets[1].budget, "large file gets the rest")
	})

	t.Run("every file gets a non-zero budget when budget covers the count", func(t *testing.T) {
		t.Parallel()
		targets := make([]logFileTarget, 8)
		for i := range targets {
			targets[i].size = 100 << 20 // all large
		}
		allocateTailBudgets(targets, 8<<20) // exactly 1 MB per file
		for i := range targets {
			assert.Positive(t, targets[i].budget, "file %d must not be starved", i)
		}
		assert.LessOrEqual(t, sum(targets), int64(8<<20))
	})

	t.Run("non-positive budget means whole files", func(t *testing.T) {
		t.Parallel()
		targets := []logFileTarget{{size: 123}, {size: 456}}
		allocateTailBudgets(targets, 0)
		assert.Equal(t, int64(123), targets[0].budget)
		assert.Equal(t, int64(456), targets[1].budget)
	})

	t.Run("empty slice does not panic", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() { allocateTailBudgets(nil, 50<<20) })
		assert.NotPanics(t, func() { allocateTailBudgets([]logFileTarget{}, 50<<20) })
	})

	t.Run("budget smaller than file count drops the smallest, never over-allocates", func(t *testing.T) {
		t.Parallel()
		// 3 equal-size files but only 2 bytes of budget: integer division gives
		// the leading (smallest) file a 0 budget (it is skipped by the caller),
		// while the budget rolls to the later files. The invariant that matters is
		// that the total never exceeds maxSize.
		targets := []logFileTarget{{size: 1000}, {size: 1000}, {size: 1000}}
		allocateTailBudgets(targets, 2)
		var total int64
		for i := range targets {
			assert.GreaterOrEqual(t, targets[i].budget, int64(0), "budget never negative")
			total += targets[i].budget
		}
		assert.LessOrEqual(t, total, int64(2), "total must never exceed maxSize")
		assert.Zero(t, targets[0].budget, "smallest file yields to the budget it cannot share")
	})
}

func zipEntryNames(zr *zip.Reader) []string {
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}
