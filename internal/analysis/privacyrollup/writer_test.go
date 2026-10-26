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

// fakeDiscardStore records Save and DeleteBefore calls of the discard store.
type fakeDiscardStore struct {
	mu         sync.Mutex
	saved      []entities.PrivacyFilterDiscard
	saveCalls  int
	failSaves  int // the next failSaves Save calls fail
	cutoffs    []int64
	deleteErrs []error // popped per DeleteBefore call
}

func (f *fakeDiscardStore) Save(_ context.Context, d *entities.PrivacyFilterDiscard) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveCalls++
	if f.failSaves > 0 {
		f.failSaves--
		return errors.NewStd("save failed")
	}
	f.saved = append(f.saved, *d)
	return nil
}

func (f *fakeDiscardStore) DeleteBefore(_ context.Context, cutoff int64) (int64, error) {
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

func (f *fakeDiscardStore) snapshot() []entities.PrivacyFilterDiscard {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.saved)
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
	w := NewWriter(store, &fakeDiscardStore{}, res.resolve, nil)

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
	w := NewWriter(store, &fakeDiscardStore{}, res.resolve, nil)

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
	w := NewWriter(store, &fakeDiscardStore{}, res.resolve, nil)

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
	discards := &fakeDiscardStore{}
	res := &fakeResolver{}
	w := NewWriter(store, discards, res.resolve, nil)
	w.Start()

	w.Record(eventFor("A", "n", "m", KindCountedHit))
	w.Record(eventFor("A", "n", "m", KindCountedHit))
	w.RecordDiscard(discardFor("A"))
	require.NoError(t, w.Stop(t.Context()))
	assert.Len(t, discards.snapshot(), 1, "Stop saves queued discards")

	rows := store.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, int64(2), rows[0].Hits)

	assert.NotPanics(t, func() { w.Record(eventFor("A", "n", "m", KindCountedHit)) })
	var nilWriter *Writer
	assert.NotPanics(t, func() { nilWriter.Record(Event{}) })
	assert.NotPanics(t, func() { nilWriter.RecordDiscard(Discard{}) })
	assert.NotPanics(t, func() { w.RecordDiscard(discardFor("A")) })
	require.NoError(t, w.Stop(t.Context()), "a second Stop is harmless")
}

func TestWriter_PrunesOnFirstTickThenHourly(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	discards := &fakeDiscardStore{}
	w := NewWriter(store, discards, (&fakeResolver{}).resolve, nil)
	start := time.Date(2026, 3, 1, 10, 0, 30, 500, time.UTC)

	w.tick(t.Context(), start)
	require.Len(t, store.cutoffs, 1)
	assert.Equal(t, start.Add(-privacyRollupRetention).Unix(), store.cutoffs[0], "cutoff is not truncated to the hour")
	require.Len(t, discards.cutoffs, 1)
	assert.Equal(t, start.Add(-privacyDiscardRetention).UnixMilli(), discards.cutoffs[0], "discard cutoff is in Unix ms")

	w.tick(t.Context(), start.Add(59*time.Minute))
	assert.Len(t, store.cutoffs, 1)
	assert.Len(t, discards.cutoffs, 1)

	store.deleteErrs = []error{errors.NewStd("locked")}
	w.tick(t.Context(), start.Add(60*time.Minute))
	require.Len(t, store.cutoffs, 2, "an hour after the last prune")

	w.tick(t.Context(), start.Add(61*time.Minute))
	assert.Len(t, store.cutoffs, 3, "a failed prune is retried on the next tick")

	w.tick(t.Context(), start.Add(62*time.Minute))
	assert.Len(t, store.cutoffs, 3)

	// Only the discard prune fails: both are retried on the next tick.
	discards.deleteErrs = []error{errors.NewStd("locked")}
	w.tick(t.Context(), start.Add(122*time.Minute))
	require.Len(t, store.cutoffs, 4)
	w.tick(t.Context(), start.Add(123*time.Minute))
	assert.Len(t, store.cutoffs, 5, "a failed discard prune keeps lastPrune, so the rollup prune repeats too")
	assert.Len(t, discards.cutoffs, 5)
	w.tick(t.Context(), start.Add(124*time.Minute))
	assert.Len(t, store.cutoffs, 5)
}

func discardFor(uri string) Discard {
	return Discard{
		At: testHour, SourceURI: uri, NodeName: "n", DisplayName: "Mic", ScientificName: "Parus major", CommonName: "Great Tit",
		Confidence: 0.87, ModelID: "m1", ClipBegin: testHour.Add(-3 * time.Second), ClipEnd: testHour.Add(6 * time.Second),
		LabelThresholdMilli: 50, VADEnabled: true, VADThresholdMilli: 350, HitCountsLowerBound: true,
		Hits: []DiscardHit{
			{Detector: "m1", Trigger: "label", Count: 2, MinScore: 0.25, MaxScore: 0.75},
			{Detector: "silero_vad", Trigger: "vad", Count: 1, MinScore: 0.5, MaxScore: 0.5},
		},
	}
}

func TestWriter_DiscardRowsShareTheSourceCacheAndKeepUnresolvedAsNull(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	discards := &fakeDiscardStore{}
	res := &fakeResolver{fail: map[string]bool{"B": true}}
	w := NewWriter(store, discards, res.resolve, nil)

	w.Record(eventFor("A", "n", "m", KindCountedHit))
	w.RecordDiscard(discardFor("A"))
	w.RecordDiscard(discardFor("B"))
	w.RecordDiscard(discardFor(""))
	drainAndFlush(t, w)

	assert.Equal(t, 1, res.calls[sourceKey{"A", "n"}], "the rollup and the discard share one resolve")
	assert.NotContains(t, res.calls, sourceKey{"", "n"})
	rows := discards.snapshot()
	require.Len(t, rows, 3)
	require.NotNil(t, rows[0].SourceID)
	assert.Equal(t, uint(1), *rows[0].SourceID)
	assert.Nil(t, rows[1].SourceID, "a source that cannot be resolved stores NULL")
	assert.Nil(t, rows[2].SourceID, "an empty source stores NULL")
	assert.Equal(t, int64(0), w.unresolved.Load(), "only the rollup side counts unresolved sources")
	assert.Equal(t, int64(0), w.discardsFailed.Load())

	first := rows[0]
	d := discardFor("A")
	assert.Equal(t, d.At.UnixMilli(), first.DiscardedAt)
	assert.Equal(t, d.ClipBegin.UnixMilli(), first.ClipBegin)
	assert.Equal(t, d.ClipEnd.UnixMilli(), first.ClipEnd)
	assert.Equal(t, "Parus major", first.ScientificName)
	assert.Equal(t, "Great Tit", first.CommonName)
	assert.InDelta(t, 0.87, first.Confidence, 1e-9)
	assert.Equal(t, "m1", first.ModelID)
	assert.Equal(t, 50, first.LabelThresholdMilli)
	assert.True(t, first.VADEnabled)
	assert.Equal(t, 350, first.VADThresholdMilli)
	assert.True(t, first.HitCountsLowerBound)
	require.Len(t, first.Hits, 2)
	assert.Equal(t, entities.PrivacyFilterDiscardHit{Detector: "m1", TriggerType: "label", HitCount: 2, MinScore: 0.25, MaxScore: 0.75}, first.Hits[0])
	assert.Equal(t, entities.PrivacyFilterDiscardHit{Detector: "silero_vad", TriggerType: "vad", HitCount: 1, MinScore: 0.5, MaxScore: 0.5}, first.Hits[1])
}

func TestWriter_RecordDiscardNeverBlocksWhenTheBufferIsFull(t *testing.T) {
	t.Parallel()
	discards := &fakeDiscardStore{}
	w := NewWriter(&fakeStore{}, discards, (&fakeResolver{}).resolve, nil)

	const extra = 7
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range privacyDiscardBuffer + extra {
			w.RecordDiscard(discardFor("A"))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RecordDiscard blocked")
	}
	assert.Equal(t, int64(extra), w.discardsDropped.Load())

	drainAndFlush(t, w)
	assert.Len(t, discards.snapshot(), privacyDiscardBuffer)
}

func TestWriter_FailedDiscardWriteSkipsTheRestAndNeverRetries(t *testing.T) {
	t.Parallel()
	discards := &fakeDiscardStore{failSaves: 1}
	res := &fakeResolver{}
	w := NewWriter(&fakeStore{}, discards, res.resolve, nil)

	w.RecordDiscard(discardFor("A"))
	w.RecordDiscard(discardFor("B"))
	w.RecordDiscard(discardFor("C"))
	drainAndFlush(t, w)

	assert.Empty(t, discards.snapshot())
	assert.Equal(t, int64(3), w.discardsFailed.Load())
	assert.Equal(t, 1, discards.saveCalls, "no write after the first failure and no retry")
	assert.Len(t, res.calls, 1, "the rest of the batch is not resolved either")

	w.RecordDiscard(discardFor("A"))
	drainAndFlush(t, w)
	assert.Len(t, discards.snapshot(), 1, "only the newly queued record is saved")
}
