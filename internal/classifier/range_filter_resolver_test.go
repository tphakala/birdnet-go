package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// TestBuildRangeFilter_TriggersNameResolverRebuild proves that BuildRangeFilter
// fires the o.RebuildNameResolver(includedSpecies) call at the end of
// range_filter.go. That seam is otherwise only verified by inspection: deleting
// the call would still pass every other test because the daily action and startup
// paths rebuild the resolver elsewhere and on-demand Resolve masks a stale index.
//
// The assertion targets the in-memory fast-path index via ResolveLocal, which is
// empty until Rebuild runs. Since Phase 2a the working set is the union of every
// loaded model's labels plus the inclusion list (a strict superset of the old
// "inclusion list, else primary labels" seed), so a removed trigger leaves the
// index empty and this test fails.
func TestBuildRangeFilter_TriggersNameResolverRebuild(t *testing.T) {
	const scoredLabelSci = "Turdus merula"  // scores in the geomodel AND is a label
	const unscoredLabelSci = "Parus major"  // a label, but never scores
	const offWorkingSetSci = "Corvus corax" // real OpenFauna species, in neither labels nor scores
	const localeEN = "en"

	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.Locale = localeEN
	settings.BirdNET.Labels = []string{
		scoredLabelSci + "_Common Blackbird",
		unscoredLabelSci + "_Great Tit",
	}
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	// Only Turdus merula scores in the geomodel, so the range-filter inclusion list
	// is exactly {Turdus merula}. Parus major never scores, but it is a loaded label,
	// so it enters the working set via the label union (Phase 2a).
	rf := &fakeUniversalRangeFilter{
		geoLabels: []string{scoredLabelSci + "_Common Blackbird", unscoredLabelSci + "_Great Tit"},
		scores:    []SpeciesScore{{Score: 0.9, Label: scoredLabelSci + "_Common Blackbird"}},
		rawScores: []float32{0.9},
	}

	o := buildTestOrchestrator(t, settings, rf)
	o.openfauna = openfauna.NewResolver()

	// Precondition: the fast-path index is empty before BuildRangeFilter runs.
	_, ok := o.openfauna.ResolveLocal(scoredLabelSci)
	require.False(t, ok, "fast-path index should be empty before BuildRangeFilter")

	require.NoError(t, BuildRangeFilter(o))

	// The trigger rebuilt the index for the working set, so the in-memory fast path
	// now resolves the scored label without a dataset scan.
	name, ok := o.openfauna.ResolveLocal(scoredLabelSci)
	assert.True(t, ok,
		"BuildRangeFilter must rebuild the OpenFauna name resolver for the working set (is the RebuildNameResolver trigger missing?)")
	assert.NotEmpty(t, name)
	assert.NotEqual(t, scoredLabelSci, name,
		"resolved value should be the localized common name, not the scientific name echoed back")

	// A loaded label that never scored is still pre-indexed: the working set is now
	// the union of loaded labels plus the inclusion list, so secondary/primary
	// species no longer fall to the on-demand Lookup path (Phase 2a).
	_, ok = o.openfauna.ResolveLocal(unscoredLabelSci)
	assert.True(t, ok, "a loaded label must be in the fast-path index even when it does not score")

	// Negative control: a real dataset species that is neither a label nor scored
	// stays off the fast-path index, proving the rebuilt index is still working-set
	// scoped and not the whole dataset.
	_, ok = o.openfauna.ResolveLocal(offWorkingSetSci)
	assert.False(t, ok, "a species outside the label union and inclusion list must not be in the fast-path index")

	// The control is only meaningful if the species genuinely exists in the dataset
	// (so its absence from the fast path proves working-set scoping, rather than the
	// species simply not existing). Its on-demand Resolve must still find it.
	assert.NotEmpty(t, o.openfauna.Resolve(offWorkingSetSci, localeEN),
		"the negative-control species must be a real dataset species resolvable on demand")
}
