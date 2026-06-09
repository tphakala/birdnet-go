package embedding

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// spyMetrics records capture outcomes for assertions.
type spyMetrics struct {
	capture map[string]int
	pruned  int
}

func newSpyMetrics() *spyMetrics { return &spyMetrics{capture: map[string]int{}} }

func (s *spyMetrics) RecordEmbeddingCapture(status string) { s.capture[status]++ }
func (s *spyMetrics) RecordEmbeddingPrune(pruned int)      { s.pruned += pruned }

func testRecord(id string, dim int) Record {
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(i) + 0.5
	}
	return Record{
		DetectionID: id,
		Model:       "birdnet",
		Source:      "test-source",
		CapturedAt:  time.Unix(1_700_000_000, 0).UTC(),
		Format:      FormatFP16,
		Dim:         dim,
		Version:     "2.4",
		Vector:      vec,
	}
}

func TestCapture_PersistsRecord(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "embeddings.db")
	spy := newSpyMetrics()

	c := NewCapture(func() (string, int) { return path, 50000 }, WithCaptureMetrics(spy))
	c.Capture(testRecord("42", 8))
	require.NoError(t, c.Close(t.Context()))

	assert.Equal(t, 1, spy.capture["persisted"])

	// Reopen the store and verify the vector round-tripped. Close it with a
	// defer registered after goleak's (LIFO => closes first) so the database/sql
	// background goroutines are gone before the leak check runs.
	store, err := NewStore(path)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	rec, err := store.Get(t.Context(), "42")
	require.NoError(t, err)
	assert.Equal(t, "42", rec.DetectionID)
	assert.Equal(t, 8, rec.Dim)
	assert.Len(t, rec.Vector, 8)
}

func TestCapture_NoOpOnEmptyVector(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "embeddings.db")
	c := NewCapture(func() (string, int) { return path, 50000 })

	c.Capture(Record{DetectionID: "1", Dim: 0}) // empty vector
	require.NoError(t, c.Close(t.Context()))

	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "store must not be created for an empty-vector capture")
}

func TestCapture_CloseNeverOpenedIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "embeddings.db")
	c := NewCapture(func() (string, int) { return path, 50000 })

	require.NoError(t, c.Close(t.Context()))
	require.NoError(t, c.Close(t.Context())) // idempotent
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestCapture_PruneEnforcesCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "embeddings.db")
	spy := newSpyMetrics()

	// maxRows=2, prune after every write.
	c := NewCapture(func() (string, int) { return path, 2 },
		WithCaptureMetrics(spy), WithPruneInterval(1))
	for i := range 5 {
		c.Capture(testRecord(string(rune('a'+i)), 4))
	}
	require.NoError(t, c.Close(t.Context()))

	store, err := NewStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	from := time.Unix(0, 0).UTC()
	to := time.Unix(2_000_000_000, 0).UTC()
	rows, err := store.Query(t.Context(), "birdnet", from, to)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(rows), 2, "rolling cap must bound stored rows")
	assert.Positive(t, spy.pruned, "prune count metric should fire")
}

// White-box: a full buffer drops rather than blocking.
func TestCapture_DropsWhenBufferFull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "embeddings.db")
	spy := newSpyMetrics()
	store, err := NewStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	// Construct a started Capture with a size-1 buffer and NO running writer,
	// so the channel cannot drain. The first enqueue fills it; the second drops.
	c := NewCapture(func() (string, int) { return path, 50000 }, WithCaptureMetrics(spy))
	c.store = store
	c.ch = make(chan Record, 1)
	c.started = true

	c.Capture(testRecord("1", 4)) // fills buffer
	c.Capture(testRecord("2", 4)) // dropped

	assert.Equal(t, 1, spy.capture["dropped_queue_full"])
	assert.Equal(t, 0, spy.capture["persisted"])
}
