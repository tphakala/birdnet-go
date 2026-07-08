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

func zipEntryNames(zr *zip.Reader) []string {
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}
