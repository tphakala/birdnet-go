package v2only

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// countsSum sums a species' raw hour-of-day counts.
func countsSum(c *[24]int) int {
	sum := 0
	for _, v := range c {
		sum += v
	}
	return sum
}

func TestBuildAcousticSuccession_SumsAndOrders(t *testing.T) {
	t.Parallel()

	top := []repository.SpeciesCount{
		{LabelID: 1, ScientificName: "Turdus merula", Count: 100},
		{LabelID: 2, ScientificName: "Erithacus rubecula", Count: 50},
		{LabelID: 3, ScientificName: "Strix aluco", Count: 10},
	}
	hourlyByLabel := map[uint][24]int{
		1: hours([2]int{0, 2}, [2]int{12, 6}, [2]int{18, 2}), // total 10
		2: hours([2]int{6, 4}, [2]int{7, 1}),                 // total 5
		3: hours([2]int{23, 1}),                              // total 1
	}

	got := buildAcousticSuccession(top, hourlyByLabel)
	require.Len(t, got, 3)

	// Order preserved (descending volume from GetTopSpecies).
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
	assert.Equal(t, "Erithacus rubecula", got[1].ScientificName)
	assert.Equal(t, "Strix aluco", got[2].ScientificName)

	// Total is the sum of the species' raw FP-excluded hourly counts.
	assert.Equal(t, 10, got[0].Total)
	assert.Equal(t, 5, got[1].Total)
	assert.Equal(t, 1, got[2].Total)

	// Counts are RAW (not normalized): they equal the per-hour detection counts and Counts sums to
	// Total. This is the key difference from buildSpeciesHourlyDistribution (which normalizes to 1.0).
	for i := range got {
		assert.Equal(t, got[i].Total, countsSum(&got[i].Counts), "species %s counts must sum to Total", got[i].ScientificName)
	}
	assert.Equal(t, 2, got[0].Counts[0])
	assert.Equal(t, 6, got[0].Counts[12])
	assert.Equal(t, 2, got[0].Counts[18])
	assert.Equal(t, 4, got[1].Counts[6])
	assert.Equal(t, 1, got[2].Counts[23])
}

func TestBuildAcousticSuccession_MergesLabelsSharingName(t *testing.T) {
	t.Parallel()

	// The same species detected under two model label IDs must collapse to one band whose counts
	// are the summed counts across both labels (systemic multi-model behavior).
	top := []repository.SpeciesCount{
		{LabelID: 1, ScientificName: "Turdus merula", Count: 60},
		{LabelID: 2, ScientificName: "Turdus merula", Count: 40},
		{LabelID: 3, ScientificName: "Erithacus rubecula", Count: 30},
	}
	hourlyByLabel := map[uint][24]int{
		1: hours([2]int{6, 3}),  // Turdus merula via model A
		2: hours([2]int{6, 1}),  // Turdus merula via model B (same hour -> sums)
		3: hours([2]int{12, 2}), // Erithacus rubecula
	}

	got := buildAcousticSuccession(top, hourlyByLabel)
	require.Len(t, got, 2) // two distinct species, not three label rows

	assert.Equal(t, "Turdus merula", got[0].ScientificName)
	assert.Equal(t, 4, got[0].Total)     // 3 + 1 merged
	assert.Equal(t, 4, got[0].Counts[6]) // both labels land in hour 6
	assert.Equal(t, "Erithacus rubecula", got[1].ScientificName)
	assert.Equal(t, 2, got[1].Total)
	assert.Equal(t, 2, got[1].Counts[12])
}

func TestBuildAcousticSuccession_DropsZeroTotalSpecies(t *testing.T) {
	t.Parallel()

	// A species ranked into the top-N by raw volume (GetTopSpecies does not exclude false positives)
	// but with no FP-excluded hourly detections must be dropped rather than stacked as an empty band.
	top := []repository.SpeciesCount{
		{LabelID: 1, ScientificName: "Turdus merula", Count: 5},
		{LabelID: 2, ScientificName: "Pica pica", Count: 3}, // all false positives -> absent from hourly
	}
	hourlyByLabel := map[uint][24]int{
		1: hours([2]int{8, 5}),
	}

	got := buildAcousticSuccession(top, hourlyByLabel)
	require.Len(t, got, 1)
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
}

func TestBuildAcousticSuccession_Empty(t *testing.T) {
	t.Parallel()

	assert.Empty(t, buildAcousticSuccession(nil, nil))

	// Non-empty top but no hourly data -> every species drops out, never nil.
	got := buildAcousticSuccession(
		[]repository.SpeciesCount{{LabelID: 1, ScientificName: "Turdus merula", Count: 5}},
		map[uint][24]int{},
	)
	assert.Empty(t, got)
	assert.NotNil(t, got)
}
