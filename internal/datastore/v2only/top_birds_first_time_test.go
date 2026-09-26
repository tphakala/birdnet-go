package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// TestV2OnlyDatastore_GetTopBirdsData_FirstTime pins the per-species summary note to carry the
// day's earliest detection as FirstTime alongside the latest one as Time. The daily species
// summary reported "first heard" from Time, so first and last heard were always the same clock
// time, the latest one.
func TestV2OnlyDatastore_GetTopBirdsData_FirstTime(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	t.Cleanup(cleanup)
	ds.timezone = time.UTC
	ctx := t.Context()

	label, err := ds.label.GetOrCreate(ctx, "Megascops asio", ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
	require.NoError(t, err)
	for _, hm := range [][2]int{{7, 30}, {5, 0}, {6, 15}} {
		require.NoError(t, ds.detection.Save(ctx, &entities.Detection{
			ModelID:    ds.defaultModelID,
			LabelID:    label.ID,
			DetectedAt: time.Date(2026, 3, 1, hm[0], hm[1], 0, 0, time.UTC).Unix(),
			Confidence: 0.9,
		}))
	}

	notes, err := ds.GetTopBirdsData(ctx, "2026-03-01", 0, 10)
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(t, "07:30:00", notes[0].Time, "Time stays the latest detection")
	assert.Equal(t, "05:00:00", notes[0].FirstTime, "FirstTime is the earliest detection")
}
