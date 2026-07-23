package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRangeFilterSpeciesJSONScoreCompatibility(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(RangeFilterSpecies{
		Score:               new(1.0),
		RangeScore:          new(0.95),
		IsManuallyIncluded:  true,
		IsSyntheticOverride: true,
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	assert.InDelta(t, 1.0, got["score"], 1e-9)
	assert.InDelta(t, 0.95, got["rangeScore"], 1e-9)
	assert.Equal(t, true, got["isManuallyIncluded"])
	assert.NotContains(t, got, "isSyntheticOverride")

	data, err = json.Marshal(RangeFilterSpecies{Score: new(0.73)})
	require.NoError(t, err)
	got = nil
	require.NoError(t, json.Unmarshal(data, &got))
	assert.NotContains(t, got, "rangeScore")
	assert.NotContains(t, got, "isManuallyIncluded")
}
