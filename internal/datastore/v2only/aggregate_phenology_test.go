package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

func TestBuildSpeciesPhenology_Empty(t *testing.T) {
	t.Parallel()

	// Nil input returns a non-nil empty slice (the wire layer must never marshal null).
	got := buildSpeciesPhenology(nil, accumTestZone)
	require.NotNil(t, got)
	assert.Empty(t, got)
}

func TestBuildSpeciesPhenology_FormatsAndSortsByArrival(t *testing.T) {
	t.Parallel()

	// Rows arrive in count-DESC order from the query (highest volume first). The helper formats the
	// MIN/MAX timestamps to local dates and re-sorts by arrival (first-seen) so the Gantt reads
	// top-to-bottom in arrival order regardless of volume.
	rows := []repository.SpeciesPhenology{
		{
			ScientificName: "Zosterops",
			FirstDetected:  localUnix(accumTestZone, 2026, 6, 5, 6, 0),
			LastDetected:   localUnix(accumTestZone, 2026, 6, 10, 7, 0),
			Count:          50, // highest volume, but latest arrival
		},
		{
			ScientificName: "Apus apus",
			FirstDetected:  localUnix(accumTestZone, 2026, 6, 1, 5, 0),
			LastDetected:   localUnix(accumTestZone, 2026, 6, 3, 6, 0),
			Count:          30, // earlier arrival, lower volume
		},
	}

	got := buildSpeciesPhenology(rows, accumTestZone)
	require.Len(t, got, 2)

	assert.Equal(t, "Apus apus", got[0].ScientificName, "earliest arrival sorts first")
	assert.Equal(t, "2026-06-01", got[0].FirstSeen)
	assert.Equal(t, "2026-06-03", got[0].LastSeen)
	assert.Equal(t, 30, got[0].Count)

	assert.Equal(t, "Zosterops", got[1].ScientificName)
	assert.Equal(t, "2026-06-05", got[1].FirstSeen)
	assert.Equal(t, "2026-06-10", got[1].LastSeen)
	assert.Equal(t, 50, got[1].Count)
}

func TestBuildSpeciesPhenology_TimezoneDateAssignment(t *testing.T) {
	t.Parallel()

	// Fixtures whose LOCAL date differs from their UTC date, so the test fails if formatting ignores
	// loc. In UTC+2, 00:30 local June 2 is 22:30 UTC June 1, and 00:30 local June 4 is 22:30 UTC
	// June 3. Formatting in loc must yield the local dates (June 2 / June 4), not the UTC ones.
	rows := []repository.SpeciesPhenology{
		{
			ScientificName: "Early riser",
			FirstDetected:  localUnix(accumTestZone, 2026, 6, 2, 0, 30),
			LastDetected:   localUnix(accumTestZone, 2026, 6, 4, 0, 30),
			Count:          5,
		},
	}

	got := buildSpeciesPhenology(rows, accumTestZone)
	require.Len(t, got, 1)
	assert.Equal(t, "2026-06-02", got[0].FirstSeen, "00:30 local must land on its own local date, not the UTC date")
	assert.Equal(t, "2026-06-04", got[0].LastSeen, "00:30 local must land on its own local date, not the UTC date")
}

func TestBuildSpeciesPhenology_SingleDaySpecies(t *testing.T) {
	t.Parallel()

	// A species detected only once has first == last on the same calendar day.
	ts := localUnix(accumTestZone, 2026, 6, 7, 9, 0)
	rows := []repository.SpeciesPhenology{
		{ScientificName: "One-off", FirstDetected: ts, LastDetected: ts, Count: 1},
	}

	got := buildSpeciesPhenology(rows, accumTestZone)
	require.Len(t, got, 1)
	assert.Equal(t, "2026-06-07", got[0].FirstSeen)
	assert.Equal(t, "2026-06-07", got[0].LastSeen)
	assert.Equal(t, 1, got[0].Count)
}

func TestBuildSpeciesPhenology_TieBreak(t *testing.T) {
	t.Parallel()

	// Same first-seen: tie-break by last-seen ascending, then scientific name ascending. Deterministic
	// ordering keeps the Gantt stable across requests.
	first := localUnix(accumTestZone, 2026, 6, 2, 6, 0)
	rows := []repository.SpeciesPhenology{
		{ScientificName: "B late", FirstDetected: first, LastDetected: localUnix(accumTestZone, 2026, 6, 9, 6, 0), Count: 10},
		{ScientificName: "C early", FirstDetected: first, LastDetected: localUnix(accumTestZone, 2026, 6, 4, 6, 0), Count: 10},
		{ScientificName: "A same", FirstDetected: first, LastDetected: localUnix(accumTestZone, 2026, 6, 4, 6, 0), Count: 10},
	}

	got := buildSpeciesPhenology(rows, accumTestZone)
	require.Len(t, got, 3)
	// Earliest last-seen first; on an equal last-seen, scientific name ascending ("A same" < "C early").
	assert.Equal(t, "A same", got[0].ScientificName)
	assert.Equal(t, "C early", got[1].ScientificName)
	assert.Equal(t, "B late", got[2].ScientificName)
}

func TestBuildSpeciesPhenology_NilLocDefaultsUTC(t *testing.T) {
	t.Parallel()

	rows := []repository.SpeciesPhenology{
		{
			ScientificName: "Turdus merula",
			FirstDetected:  time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC).Unix(),
			LastDetected:   time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC).Unix(),
			Count:          3,
		},
	}

	got := buildSpeciesPhenology(rows, nil)
	require.Len(t, got, 1)
	assert.Equal(t, "2026-06-01", got[0].FirstSeen)
	assert.Equal(t, "2026-06-02", got[0].LastSeen)
}
