package v2only

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// fakeResolver is a minimal datastore.SpeciesNameResolver for tests.
type fakeResolver struct{ m map[string]string }

func (f fakeResolver) Resolve(sci, _ string) string { return f.m[sci] }

func (f fakeResolver) ResolveLocal(sci string) (string, bool) {
	v, ok := f.m[sci]
	return v, ok
}

func TestResolveCommonName_ResolverOverridesLabelMap(t *testing.T) {
	ds := &Datastore{
		log: logger.NewConsoleLogger("v2only_test", logger.LogLevelError),
	}

	// Without a resolver: the label map wins. Install a shared index built from
	// labels only, mirroring the fallback seed New() installs.
	labelOnly := speciesindex.New(nil)
	labelOnly.Rebuild([]string{"Turdus merula_LabelName"}, "")
	ds.SetSpeciesIndex(labelOnly)
	assert.Equal(t, "LabelName", ds.resolveCommonName("Turdus merula"))

	// Swap in a shared index whose resolver overrides the label-derived name, the
	// way the orchestrator-owned service does once injected.
	withResolver := speciesindex.New(fakeResolver{m: map[string]string{"Turdus merula": "Localized"}})
	withResolver.Rebuild([]string{"Turdus merula_LabelName"}, "")
	ds.SetSpeciesIndex(withResolver)
	assert.Equal(t, "Localized", ds.resolveCommonName("Turdus merula"))

	// Resolver miss on a species not in labels: falls back to the scientific name.
	assert.Equal(t, "Myotis myotis", ds.resolveCommonName("Myotis myotis"))
}

func TestResolveCommonName_RealOpenFaunaOverrides(t *testing.T) {
	// End-to-end: a real OpenFauna resolver over a one-species working set must
	// override a conflicting label map. Assert behavior (non-empty, != "WRONG"),
	// not the exact dataset string, so a dataset refresh does not break the test.
	r := openfauna.NewResolver()
	require.NoError(t, r.Rebuild([]string{"Turdus merula"}, "en"))

	ds := &Datastore{
		log: logger.NewConsoleLogger("v2only_test", logger.LogLevelError),
	}
	svc := speciesindex.New(r)
	svc.Rebuild([]string{"Turdus merula_WRONG"}, "")
	ds.SetSpeciesIndex(svc)

	got := ds.resolveCommonName("Turdus merula")
	assert.NotEqual(t, "WRONG", got, "OpenFauna must override the label-derived name")
	assert.NotEmpty(t, got)
}

// Note: the name-map construction behaviors previously exercised here through
// buildNameMaps directly (scientific-only labels searchable via a resolver, and
// resolver-localized reverse maps) now live in internal/speciesindex, where the
// shared builder is owned and golden-tested.
