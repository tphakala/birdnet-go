package batch

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

type fakeSearcher struct {
	notes []datastore.Note
}

func (f *fakeSearcher) SearchNotesAdvanced(filters *datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) {
	start := filters.Offset
	if start >= len(f.notes) {
		return nil, int64(len(f.notes)), nil
	}
	end := min(start+filters.Limit, len(f.notes))
	return f.notes[start:end], int64(len(f.notes)), nil
}

func TestBackfillItems(t *testing.T) {
	t.Parallel()
	clipDir := t.TempDir()
	touch(t, filepath.Join(clipDir, "clips", "n1.wav"))
	touch(t, filepath.Join(clipDir, "clips", "n3.wav"))

	begin := time.Date(2026, 6, 1, 5, 0, 0, 0, time.UTC)
	notes := []datastore.Note{
		{ID: 1, ScientificName: "Turdus merula", ClipName: "clips/n1.wav", BeginTime: begin},
		{ID: 2, ScientificName: "Erithacus rubecula", ClipName: ""},                // no clip recorded
		{ID: 3, ScientificName: "Parus major", ClipName: "clips/n3.wav", BeginTime: begin},
		{ID: 4, ScientificName: "Sitta europaea", ClipName: "clips/gone.wav"}, // purged from disk
	}

	items, err := BackfillItems(&fakeSearcher{notes: notes}, clipDir, BackfillFilter{})
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, "1", items[0].DetectionID)
	assert.Equal(t, "Turdus merula", items[0].Species)
	assert.Equal(t, filepath.Join(clipDir, "clips", "n1.wav"), items[0].Path)
	assert.Equal(t, begin, items[0].CapturedAt)
	assert.Equal(t, "3", items[1].DetectionID)
}

func TestBackfillItemsLimit(t *testing.T) {
	t.Parallel()
	clipDir := t.TempDir()
	notes := make([]datastore.Note, 10)
	for i := range notes {
		name := "c" + strconv.Itoa(i) + ".wav"
		touch(t, filepath.Join(clipDir, name))
		notes[i] = datastore.Note{ID: uint(i + 1), ClipName: name}
	}
	items, err := BackfillItems(&fakeSearcher{notes: notes}, clipDir, BackfillFilter{Limit: 3})
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestBackfillItemsSpeciesAndSinceForwarded(t *testing.T) {
	t.Parallel()
	clipDir := t.TempDir()
	rec := &recordingSearcher{}
	since := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	_, err := BackfillItems(rec, clipDir, BackfillFilter{Species: "Turdus merula", Since: since})
	require.NoError(t, err)
	require.NotNil(t, rec.got)
	assert.Equal(t, []string{"Turdus merula"}, rec.got.Species)
	require.NotNil(t, rec.got.DateRange)
	// DateRange.Start must be the Since value; End is a far-future sentinel so the
	// date >= start AND date <= end query in applyDateRangeFilter admits all future records.
	assert.Equal(t, since, rec.got.DateRange.Start)
	assert.True(t, rec.got.DateRange.End.After(time.Now().AddDate(100, 0, 0)),
		"DateRange.End should be a far-future sentinel (>100 years from now)")
	assert.Equal(t, "date_desc", rec.got.SortBy)
}

type recordingSearcher struct{ got *datastore.AdvancedSearchFilters }

func (r *recordingSearcher) SearchNotesAdvanced(f *datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) {
	r.got = f
	return nil, 0, nil
}
