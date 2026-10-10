package rangeapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/dto"
)

func TestGenerateSpeciesCSVPreservesOverrideScore(t *testing.T) {
	t.Parallel()

	handler := &Handler{}
	csvData, err := handler.generateSpeciesCSV([]dto.RangeFilterSpecies{
		{
			ScientificName: "Amazona viridigenalis",
			CommonName:     "Red-crowned Amazon",
			Score:          new(1.0),
			RangeScore:     new(0.95),
		},
	}, Location{}, 0.03)
	require.NoError(t, err)

	assert.Contains(t, string(csvData), "Amazona viridigenalis,Red-crowned Amazon,1.0000")
	assert.NotContains(t, string(csvData), "0.9500")
}
