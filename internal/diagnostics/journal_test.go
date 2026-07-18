package diagnostics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestJournal returns a Journal backed by a file in t.TempDir().
func newTestJournal(t *testing.T) *Journal {
	t.Helper()
	return NewJournal(filepath.Join(t.TempDir(), "journal.jsonl"))
}

// makeBoot builds a minimal BootRecord for journal tests.
func makeBoot(version string) *BootRecord {
	return &BootRecord{
		RecordHeader: NewRecordHeader(RecordTypeBoot),
		App:          AppInfo{Version: version},
	}
}

func TestJournalAppendAndLastBoot(t *testing.T) {
	t.Parallel()
	j := newTestJournal(t)

	require.NoError(t, j.Append(makeBoot("20260716")))
	RecordShutdown(j, true)
	require.NoError(t, j.Append(makeBoot("20260717")))

	got, ok := j.LastBoot()
	require.True(t, ok, "LastBoot must find the newest boot record")
	assert.Equal(t, "20260717", got.App.Version)
	assert.Equal(t, RecordTypeBoot, got.Type)
	assert.Equal(t, SchemaVersion, got.SchemaVersion)
	assert.False(t, got.Timestamp.IsZero())
}

func TestJournalLastBootEmptyOrMissingFile(t *testing.T) {
	t.Parallel()
	j := newTestJournal(t)
	_, ok := j.LastBoot()
	assert.False(t, ok, "missing file yields no boot record")

	require.NoError(t, os.WriteFile(j.Path(), nil, 0o600))
	_, ok = j.LastBoot()
	assert.False(t, ok, "empty file yields no boot record")
}

func TestJournalToleratesMalformedLines(t *testing.T) {
	t.Parallel()
	j := newTestJournal(t)
	require.NoError(t, j.Append(makeBoot("20260716")))

	// Simulate a torn write and junk lines after the good record.
	f, err := os.OpenFile(j.Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("{\"type\":\"boot\",\"truncat\nnot json at all\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	got, ok := j.LastBoot()
	require.True(t, ok)
	assert.Equal(t, "20260716", got.App.Version)
}

func TestJournalTrimByRecordCount(t *testing.T) {
	t.Parallel()
	j := newTestJournal(t)
	total := maxJournalRecords + 50
	for i := range total {
		require.NoError(t, j.Append(&ShutdownRecord{
			RecordHeader: NewRecordHeader(RecordTypeShutdown),
			Clean:        i%2 == 0,
		}))
	}
	require.NoError(t, j.TrimIfNeeded())

	data, err := os.ReadFile(j.Path())
	require.NoError(t, err)
	lines := nonEmptyLines(string(data))
	assert.Len(t, lines, maxJournalRecords, "trim keeps exactly the newest cap of records")
}

func TestJournalTrimByByteSize(t *testing.T) {
	t.Parallel()
	j := newTestJournal(t)
	// Few records, each large, exceeding the byte cap.
	big := strings.Repeat("x", 128*1024)
	const count = 12 // 12 * 128 KiB = 1.5 MiB > maxJournalBytes (1 MiB)
	for range count {
		require.NoError(t, j.Append(&FreshDBRecord{
			RecordHeader: NewRecordHeader(RecordTypeDBFreshCreated),
			Path:         big,
			Reason:       "test",
		}))
	}
	require.NoError(t, j.TrimIfNeeded())

	fi, err := os.Stat(j.Path())
	require.NoError(t, err)
	assert.LessOrEqual(t, fi.Size(), int64(maxJournalBytes), "trim enforces the byte cap")
	data, err := os.ReadFile(j.Path())
	require.NoError(t, err)
	assert.NotEmpty(t, nonEmptyLines(string(data)), "trim must keep the newest records")
}

func TestJournalConcurrentAppendSafe(t *testing.T) {
	t.Parallel()
	j := newTestJournal(t)
	const goroutines = 20
	const perGoroutine = 10
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				// Errors intentionally ignored: assert on the final line count.
				_ = j.Append(makeBoot("20260716"))
			}
		})
	}
	wg.Wait()

	data, err := os.ReadFile(j.Path())
	require.NoError(t, err)
	lines := nonEmptyLines(string(data))
	assert.Len(t, lines, goroutines*perGoroutine)
	for _, line := range lines {
		var probe map[string]any
		assert.NoError(t, json.Unmarshal([]byte(line), &probe), "every appended line is valid JSON")
	}
}

func TestLifecycleEmitHelpersWriteTypedRecords(t *testing.T) {
	t.Parallel()
	j := newTestJournal(t)
	RecordFreshDB(j, "/data/birdnet.db", "fresh_install")
	RecordMigration(j, "completed", "cutover", "completed", 12345)
	RecordConsolidation(j, "/data/birdnet_v2.db", "/data/birdnet.db", "/data/birdnet.db.20260718-100000.old", "success")
	RecordConfigDefaulted(j, "/config/config.yaml")
	RecordShutdown(j, false)

	data, err := os.ReadFile(j.Path())
	require.NoError(t, err)
	lines := nonEmptyLines(string(data))
	require.Len(t, lines, 5)

	wantTypes := []string{
		RecordTypeDBFreshCreated, RecordTypeMigration, RecordTypeConsolidation,
		RecordTypeConfigDefaulted, RecordTypeShutdown,
	}
	for i, line := range lines {
		var probe struct {
			SchemaVersion int       `json:"schema_version"`
			Type          string    `json:"type"`
			Timestamp     time.Time `json:"timestamp"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &probe))
		assert.Equal(t, wantTypes[i], probe.Type)
		assert.Equal(t, SchemaVersion, probe.SchemaVersion)
		assert.False(t, probe.Timestamp.IsZero())
	}

	var mig MigrationRecord
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &mig))
	assert.Equal(t, int64(12345), mig.Records)
	assert.Equal(t, "cutover", mig.From)

	var sd ShutdownRecord
	require.NoError(t, json.Unmarshal([]byte(lines[4]), &sd))
	assert.False(t, sd.Clean)
	assert.GreaterOrEqual(t, sd.UptimeSeconds, int64(0))
}

func TestJournalAppendUnwritablePathReturnsError(t *testing.T) {
	t.Parallel()
	// Parent directory of the journal path does not exist and cannot be
	// created because a FILE occupies the parent path segment.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	j := NewJournal(filepath.Join(blocker, "sub", "journal.jsonl"))
	assert.Error(t, j.Append(makeBoot("20260716")), "append must fail, not panic")
}

// nonEmptyLines splits s on newlines and drops empty entries.
func nonEmptyLines(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
