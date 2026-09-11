package api

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// fakeResolver is a minimal datastore.SpeciesNameResolver for tests.
type fakeResolver struct{ m map[string]string }

func (f fakeResolver) Resolve(sci, _ string) string { return f.m[sci] }

func (f fakeResolver) ResolveLocal(sci string) (string, bool) {
	v, ok := f.m[sci]
	return v, ok
}

// newNameMapController builds a bare Controller with an initialized species-name
// index, for exercising the facade-owned UpdateCommonNameMap/SetNameResolver and
// name-map accessors without spinning up the full facade. The underlying builder
// itself is golden-tested in internal/speciesindex; these tests cover the facade
// wiring (resolver install plus rebuild feeding the accessors).
func newNameMapController(t *testing.T) *Controller {
	t.Helper()
	e := echo.New()
	c := &Controller{Core: &apicore.Core{Group: e.Group("/api/v2")}}
	c.names = speciesindex.New(nil)
	return c
}

func TestController_UpdateCommonNameMap_ResolverLocalizes(t *testing.T) {
	t.Parallel()

	// The resolver overrides the label's common name in both the forward
	// (sciToCommon) and reverse (commonToSci) maps, so insights display and
	// search both reflect the localized name.
	c := newNameMapController(t)
	c.SetNameResolver(fakeResolver{m: map[string]string{"Turdus merula": "Mustarastas"}})
	c.UpdateCommonNameMap([]string{"Turdus merula_LabelName"})

	assert.Equal(t, "Mustarastas", c.loadCommonNameMap()["Turdus merula"])
	assert.Equal(t, "Turdus merula", c.loadCommonToScientificMap()[apicore.NormalizeForLookup("Mustarastas")])
}

func TestController_UpdateCommonNameMap_NilResolverKeepsLabel(t *testing.T) {
	t.Parallel()

	c := newNameMapController(t)
	c.UpdateCommonNameMap([]string{"Turdus merula_LabelName"})
	assert.Equal(t, "LabelName", c.loadCommonNameMap()["Turdus merula"])
}

func TestResolveCommonName(t *testing.T) {
	t.Parallel()

	m := map[string]string{
		"Turdus merula": "Eurasian Blackbird",
		"Parus major":   "Great Tit",
	}
	assert.Equal(t, "Eurasian Blackbird", apicore.ResolveCommonName(m, "Turdus merula"))
	// A miss falls back to the input verbatim.
	assert.Equal(t, "Unknown species", apicore.ResolveCommonName(m, "Unknown species"))
}

func TestController_UpdateCommonNameMap_ScientificOnlyLabelSearchable(t *testing.T) {
	t.Parallel()

	// A scientific-only label (no "_", e.g. Perch v2 / bat labels) has no embedded
	// common name; when the resolver provides one, the species becomes searchable
	// by it (forward + reverse maps populated).
	c := newNameMapController(t)
	c.SetNameResolver(fakeResolver{m: map[string]string{"Myotis myotis": "Greater Mouse-eared Bat"}})
	c.UpdateCommonNameMap([]string{"Myotis myotis"})
	assert.Equal(t, "Greater Mouse-eared Bat", c.loadCommonNameMap()["Myotis myotis"])
	assert.Equal(t, "Myotis myotis", c.loadCommonToScientificMap()[apicore.NormalizeForLookup("Greater Mouse-eared Bat")])

	// Without a resolver, a scientific-only label has no common name and is absent.
	bare := newNameMapController(t)
	bare.UpdateCommonNameMap([]string{"Myotis myotis"})
	_, ok := bare.loadCommonNameMap()["Myotis myotis"]
	assert.False(t, ok)
}
