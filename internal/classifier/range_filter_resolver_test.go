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
// empty until Rebuild runs and only ever holds the working set. So a removed
// trigger leaves the index empty and this test fails.
func TestBuildRangeFilter_TriggersNameResolverRebuild(t *testing.T) {
	const workingSetSci = "Turdus merula" // scores in the geomodel -> enters the working set
	const outOfSetSci = "Parus major"     // real OpenFauna species, kept OUT of the working set
	const localeEN = "en"

	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.Locale = localeEN
	settings.BirdNET.Labels = []string{
		workingSetSci + "_Common Blackbird",
		outOfSetSci + "_Great Tit",
	}
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	// Only Turdus merula scores in the geomodel, so the range-filter working set
	// (includedSpecies) is exactly {Turdus merula}. Parus major is deliberately
	// absent from the scores so it never enters the resolver's sparse index.
	rf := &fakeUniversalRangeFilter{
		geoLabels: []string{workingSetSci + "_Common Blackbird", outOfSetSci + "_Great Tit"},
		scores:    []SpeciesScore{{Score: 0.9, Label: workingSetSci + "_Common Blackbird"}},
		rawScores: []float32{0.9},
	}

	o := buildTestOrchestrator(t, settings, rf)
	o.openfauna = openfauna.NewResolver()

	// Precondition: the fast-path index is empty before BuildRangeFilter runs.
	_, ok := o.openfauna.ResolveLocal(workingSetSci)
	require.False(t, ok, "fast-path index should be empty before BuildRangeFilter")

	require.NoError(t, BuildRangeFilter(o))

	// The trigger rebuilt the sparse index for the working set, so the in-memory
	// fast path now resolves the working-set species without a dataset scan.
	name, ok := o.openfauna.ResolveLocal(workingSetSci)
	assert.True(t, ok,
		"BuildRangeFilter must rebuild the OpenFauna name resolver for the working set (is the RebuildNameResolver trigger missing?)")
	assert.NotEmpty(t, name)
	assert.NotEqual(t, workingSetSci, name,
		"resolved value should be the localized common name, not the scientific name echoed back")

	// Negative control: a real dataset species outside the working set stays off the
	// fast-path index, proving the rebuilt index is working-set-scoped (sparse) and
	// not the whole dataset. Its on-demand Resolve would still find it.
	_, ok = o.openfauna.ResolveLocal(outOfSetSci)
	assert.False(t, ok, "out-of-working-set species must not be in the fast-path index")
}
