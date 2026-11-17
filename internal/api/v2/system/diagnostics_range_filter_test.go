package system

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/health/checks"
)

// TestBuildRangeFilterStatusProvider pins the provider's wiring guards: an unreachable
// analysis engine is reported as unavailable (the check skips) instead of a fake healthy.
func TestBuildRangeFilterStatusProvider(t *testing.T) {
	_, h := setupSystemTestEnvironment(t)
	assert.False(t, h.buildRangeFilterStatusProvider()().Available, "no processor")

	h.Processor = &processor.Processor{}
	assert.False(t, h.buildRangeFilterStatusProvider()().Available, "no orchestrator")

	h.Processor = &processor.Processor{Bn: &classifier.Orchestrator{Settings: &conf.Settings{}}}
	got := h.buildRangeFilterStatusProvider()()
	require.True(t, got.Available)
	assert.False(t, got.ParticipantsLoaded)
}

// TestRangeFilterStatusInfoFrom checks the classifier-to-health mapping: uncovered models
// are listed by display name, and every classifier issue maps to its health counterpart.
func TestRangeFilterStatusInfoFrom(t *testing.T) {
	st := classifier.RangeFilterStatusResponse{
		Classifiers: []classifier.ClassifierCoverage{
			{ID: classifier.RegistryIDBirdNETV24, Name: "BirdNET v2.4", CoveredByBackend: true},
			{ID: classifier.RegistryIDPerchV2, Name: "Google Perch v2", CoveredByBackend: false},
		},
		ParticipantsLoaded: true,
		LocationConfigured: true,
		Active:             true,
		Backend:            "mdata_v2",
		ReloadFailed:       true,
		GeomodelIssue:      classifier.GeomodelIssueNotInstalled,
	}
	got := rangeFilterStatusInfoFrom(&st)
	assert.True(t, got.Available)
	assert.Equal(t, []string{"Google Perch v2"}, got.UncoveredModels)
	assert.Equal(t, "mdata_v2", got.Backend)
	assert.True(t, got.ReloadFailed)
	assert.True(t, got.Active)
	assert.False(t, got.GeomodelActive)

	st.Geomodel = &classifier.GeomodelStatus{}
	assert.True(t, rangeFilterStatusInfoFrom(&st).GeomodelActive)

	issues := map[classifier.GeomodelIssue]checks.GeomodelIssue{
		classifier.GeomodelIssueNone:         "",
		classifier.GeomodelIssueNotInstalled: checks.GeomodelIssueNotInstalled,
		classifier.GeomodelIssueNotSelected:  checks.GeomodelIssueNotSelected,
		classifier.GeomodelIssueLoadFailed:   checks.GeomodelIssueLoadFailed,
	}
	for in, want := range issues {
		assert.Equal(t, want, healthGeomodelIssue(in), "issue %q", in)
	}
}
