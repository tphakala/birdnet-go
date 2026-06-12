package api

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeSpeciesScientific(t *testing.T) {
	t.Parallel()

	t.Run("nil and empty return nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, sanitizeSpeciesScientific(nil))
		assert.Nil(t, sanitizeSpeciesScientific([]string{}))
	})

	t.Run("trims, drops empties, de-duplicates, preserves order", func(t *testing.T) {
		t.Parallel()
		got := sanitizeSpeciesScientific([]string{
			"  Barbastella barbastellus ", "", "   ", "Myotis daubentonii",
			"Barbastella barbastellus", // duplicate after trim
		})
		assert.Equal(t, []string{"Barbastella barbastellus", "Myotis daubentonii"}, got)
	})

	t.Run("caps the list to the maximum", func(t *testing.T) {
		t.Parallel()
		in := make([]string, maxSearchSpeciesScientific+50)
		for i := range in {
			in[i] = "Species " + strconv.Itoa(i)
		}
		got := sanitizeSpeciesScientific(in)
		assert.Len(t, got, maxSearchSpeciesScientific)
	})
}

func TestBuildSearchFilters_ThreadsSpeciesScientific(t *testing.T) {
	t.Parallel()

	c := &Controller{}
	req := &SearchRequest{
		Species:           "Corvus",
		SpeciesScientific: []string{"Barbastella barbastellus", "Myotis daubentonii"},
	}
	filters := c.buildSearchFilters(req, context.Background())

	assert.Equal(t, "Corvus", filters.Species)
	require.Equal(t, []string{"Barbastella barbastellus", "Myotis daubentonii"}, filters.SpeciesScientific)
}
