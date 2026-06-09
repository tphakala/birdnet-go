package embedding

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestStore opens a Store backed by a fresh SQLite file in the test's
// temporary directory and registers cleanup. Each test gets an isolated
// database file so tests can run in parallel without contention.
func newTestStore(t *testing.T, opts ...Option) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "embeddings.db")
	s, err := NewStore(path, opts...)
	require.NoError(t, err, "store must open")
	t.Cleanup(func() {
		require.NoError(t, s.Close(), "store must close cleanly")
	})
	return s
}

// makeRecord builds a Record with sensible defaults, applying any overrides.
// CapturedAt defaults to a fixed UTC instant so time comparisons are stable.
func makeRecord(t *testing.T, opts ...func(*Record)) *Record {
	t.Helper()

	rec := &Record{
		DetectionID: "det-1",
		Model:       "birdnet_v3",
		Source:      "rtsp://camera",
		CapturedAt:  time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC),
		Format:      FormatFP16,
		Dim:         4,
		Version:     "3.0",
		Vector:      []float32{0.5, -0.25, 2, -4}, // exactly representable in fp16
	}
	for _, opt := range opts {
		opt(rec)
	}
	return rec
}
