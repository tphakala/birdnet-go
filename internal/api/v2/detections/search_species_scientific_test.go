package detections

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
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

func TestResolveCommonNameSubstrings(t *testing.T) {
	t.Parallel()

	c := &Handler{
		Core: &apicore.Core{},
		loadFoldedCommonNameMap: func() map[string]string {
			return map[string]string{
				"Tyto furcata": apicore.NormalizeForLookup("American Barn Owl"),
				"Tyto alba":    apicore.NormalizeForLookup("Barn Owl"),
				"Strix aluco":  apicore.NormalizeForLookup("Lehtopöllö"),
			}
		},
	}

	t.Run("finds exact and containing common names", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"Tyto alba", "Tyto furcata"}, c.resolveCommonNameSubstrings("BARN OWL"))
	})

	t.Run("normalizes Unicode composition", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"Strix aluco"}, c.resolveCommonNameSubstrings("pöllö"))
	})

	t.Run("empty and unknown queries return nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, c.resolveCommonNameSubstrings("   "))
		assert.Nil(t, c.resolveCommonNameSubstrings("unknown"))
	})
}

func TestMergeSpeciesScientific(t *testing.T) {
	t.Parallel()

	t.Run("server matches precede client matches", func(t *testing.T) {
		t.Parallel()
		got := mergeSpeciesScientific(
			[]string{"Tyto alba", "Tyto furcata"},
			[]string{"Strix aluco"},
		)
		assert.Equal(t, []string{"Tyto alba", "Tyto furcata", "Strix aluco"}, got)
	})

	t.Run("de-duplicates across sources", func(t *testing.T) {
		t.Parallel()
		got := mergeSpeciesScientific(
			[]string{"Tyto alba", "Tyto furcata"},
			[]string{"Tyto furcata", "Strix aluco"},
		)
		assert.Equal(t, []string{"Tyto alba", "Tyto furcata", "Strix aluco"}, got)
	})

	t.Run("caps the combined list with server matches retained", func(t *testing.T) {
		t.Parallel()
		clientMatches := make([]string, maxSearchSpeciesScientific)
		for i := range clientMatches {
			clientMatches[i] = "Species " + strconv.Itoa(i)
		}

		got := mergeSpeciesScientific([]string{"Tyto alba", "Tyto furcata"}, clientMatches)
		assert.Len(t, got, maxSearchSpeciesScientific)
		assert.Equal(t, []string{"Tyto alba", "Tyto furcata"}, got[:2])
		assert.Contains(t, got, "Species 0")
		assert.NotContains(t, got, "Species "+strconv.Itoa(maxSearchSpeciesScientific-1))
	})
}

func TestBuildSearchFilters_ThreadsSpeciesScientific(t *testing.T) {
	t.Parallel()

	c := &Handler{Core: &apicore.Core{}}
	req := &SearchRequest{
		Species:           "Corvus",
		SpeciesScientific: []string{"Barbastella barbastellus", "Myotis daubentonii"},
	}
	filters := c.buildSearchFilters(req, context.Background())

	assert.Equal(t, "Corvus", filters.Species)
	require.Equal(t, []string{"Barbastella barbastellus", "Myotis daubentonii"}, filters.SpeciesScientific)
}
