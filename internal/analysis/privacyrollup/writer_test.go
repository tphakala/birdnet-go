package privacyrollup

import (
	"context"
	"slices"

	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// fakeStore records AddDelta and DeleteBefore calls.
type fakeStore struct {
	mu         sync.Mutex
	rows       []entities.PrivacyFilterHourly
	failFirst  map[uint]bool // source ids whose next write fails once
	cutoffs    []int64
	deleteErrs []error // popped per DeleteBefore call
}

func (f *fakeStore) AddDelta(_ context.Context, row *entities.PrivacyFilterHourly) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFirst[row.SourceID] {
		delete(f.failFirst, row.SourceID)
		return errors.NewStd("write failed")
	}
	f.rows = append(f.rows, *row)
	return nil
}

func (f *fakeStore) DeleteBefore(_ context.Context, cutoff int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoffs = append(f.cutoffs, cutoff)
	if len(f.deleteErrs) > 0 {
		err := f.deleteErrs[0]
		f.deleteErrs = f.deleteErrs[1:]
		return 0, err
	}
	return 0, nil
}

func (f *fakeStore) snapshot() []entities.PrivacyFilterHourly {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.rows)
}

// fakeResolver maps (uri, node) to ids, counting calls.
type fakeResolver struct {
	mu    sync.Mutex
	calls map[sourceKey]int
	fail  map[string]bool // uri -> error
}

func (r *fakeResolver) resolve(_ context.Context, uri, node string, _ *string) (uint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[sourceKey]int{}
	}
	r.calls[sourceKey{uri, node}]++
	if r.fail[uri] {
		return 0, errors.NewStd("no source")
	}
	switch uri {
	case "A":
		return 1, nil
	case "B":
		return 2, nil
	}
	return 3, nil
}

func eventFor(uri, node, model string, kind Kind) Event {
	return Event{Kind: kind, At: testHour, SourceURI: uri, NodeName: node, ModelID: model, Trigger: "label", ThresholdMilli: 50, Score: 0.9}
}

func drainAndFlush(t *testing.T, w *Writer) {
	t.Helper()
	w.drainQueued()
	w.flush(t.Context())
}

func TestWriter_FlushResolvesOncePerFlushAndSkipsUnresolved(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	res := &fakeResolver{fail: map[string]bool{"B": true}}
	w := NewWriter(store, res.resolve, nil)

	record := func() {
		w.Record(eventFor("A", "n1", "m1", KindCountedHit))
		w.Record(eventFor("A", "n1", "m2", KindCountedHit))
		w.Record(eventFor("A", "n1", "m3", KindDiscard))
		w.Record(eventFor("A", "n2", "m1", KindCountedHit)) // same URI, other node
		w.Record(eventFor("B", "n1", "m1", KindCountedHit))
		w.Record(eventFor("", "n1", "m1", KindCountedHit))
	}
	record()
	drainAndFlush(t, w)

	assert.Equal(t, 1, res.calls[sourceKey{"A", "n1"}])
	assert.Equal(t, 1, res.calls[sourceKey{"A", "n2"}])
	assert.Equal(t, 1, res.calls[sourceKey{"B", "n1"}])
	assert.NotContains(t, res.calls, sourceKey{"", "n1"})
	assert.Len(t, store.snapshot(), 4, "only source A rows reach the store")
	for _, row := range store.snapshot() {
		assert.Equal(t, uint(1), row.SourceID)
	}
	assert.Equal(t, int64(2), w.unresolved.Load(), "B's row and the empty source's event")

	record()
	drainAndFlush(t, w)
	assert.Equal(t, 2, res.calls[sourceKey{"A", "n1"}], "no cache across flushes")
}

func TestWriter_FailedWriteDropsDeltaNeverReadds(t *testing.T) {
	t.Parallel()
	store := &fakeStore{failFirst: map[uint]bool{1: true}}
	res := &fakeResolver{}
	w := NewWriter(store, res.resolve, nil)

	w.Record(eventFor("A", "n", "m", KindCountedHit))
	drainAndFlush(t, w)
	assert.Empty(t, store.snapshot())
	assert.Equal(t, int64(1), w.failed.Load())

	w.Record(eventFor("A", "n", "m", KindCountedHit))
	drainAndFlush(t, w)
	rows := store.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, int64(1), rows[0].Hits, "only the second flush's delta is sent")
}

func TestWriter_RecordNeverBlocksWhenTheBufferIsFull(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	res := &fakeResolver{}
	w := NewWriter(store, res.resolve, nil)

	const extra = 25
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range privacyRollupEventBuffer + extra {
			w.Record(eventFor("A", "n", "m", KindCountedHit))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked")
	}
	assert.Equal(t, int64(extra), w.dropped.Load())

	drainAndFlush(t, w)
	rows := store.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, int64(privacyRollupEventBuffer), rows[0].Hits)
}

func TestWriter_StopFlushesQueuedEventsAndRecordAfterStopIsSafe(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	res := &fakeResolver{}
	w := NewWriter(store, res.resolve, nil)
	w.Start()

	w.Record(eventFor("A", "n", "m", KindCountedHit))
	w.Record(eventFor("A", "n", "m", KindCountedHit))
	require.NoError(t, w.Stop(t.Context()))

	rows := store.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, int64(2), rows[0].Hits)

	assert.NotPanics(t, func() { w.Record(eventFor("A", "n", "m", KindCountedHit)) })
	var nilWriter *Writer
	assert.NotPanics(t, func() { nilWriter.Record(Event{}) })
	require.NoError(t, w.Stop(t.Context()), "a second Stop is harmless")
}

func TestWriter_PrunesOnFirstTickThenHourly(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	w := NewWriter(store, (&fakeResolver{}).resolve, nil)
	start := time.Date(2026, 3, 1, 10, 0, 30, 500, time.UTC)

	w.tick(t.Context(), start)
	require.Len(t, store.cutoffs, 1)
	assert.Equal(t, start.Add(-privacyRollupRetention).Unix(), store.cutoffs[0], "cutoff is not truncated to the hour")

	w.tick(t.Context(), start.Add(59*time.Minute))
	assert.Len(t, store.cutoffs, 1)

	store.deleteErrs = []error{errors.NewStd("locked")}
	w.tick(t.Context(), start.Add(60*time.Minute))
	require.Len(t, store.cutoffs, 2, "an hour after the last prune")

	w.tick(t.Context(), start.Add(61*time.Minute))
	assert.Len(t, store.cutoffs, 3, "a failed prune is retried on the next tick")

	w.tick(t.Context(), start.Add(62*time.Minute))
	assert.Len(t, store.cutoffs, 3)
}
