package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
)

// fakeResolver is a minimal datastore.SpeciesNameResolver for tests.
type fakeResolver struct{ m map[string]string }

func (f fakeResolver) Resolve(sci, _ string) string { return f.m[sci] }

func (f fakeResolver) ResolveLocal(sci string) (string, bool) {
	v, ok := f.m[sci]
	return v, ok
}

func TestBuildNameMaps_ResolverLocalizes(t *testing.T) {
	// The resolver overrides the label's common name in both the forward
	// (sciToCommon) and reverse (commonToSci) maps, so insights display and
	// search both reflect the localized name.
	nm := buildNameMaps([]string{"Turdus merula_LabelName"},
		fakeResolver{m: map[string]string{"Turdus merula": "Mustarastas"}})
	assert.Equal(t, "Mustarastas", nm.sciToCommon["Turdus merula"])
	assert.Equal(t, "Turdus merula", nm.commonToSci[apicore.NormalizeForLookup("Mustarastas")])
}

func TestBuildNameMaps_NilResolverKeepsLabel(t *testing.T) {
	nm := buildNameMaps([]string{"Turdus merula_LabelName"}, nil)
	assert.Equal(t, "LabelName", nm.sciToCommon["Turdus merula"])
}

func TestBuildNameMaps_ScientificOnlyLabelSearchable(t *testing.T) {
	// A scientific-only label (no "_", e.g. Perch v2 / bat labels) has no embedded
	// common name; when the resolver provides one, the species must become
	// searchable by it (forward + reverse maps populated).
	nm := buildNameMaps([]string{"Myotis myotis"},
		fakeResolver{m: map[string]string{"Myotis myotis": "Greater Mouse-eared Bat"}})
	assert.Equal(t, "Greater Mouse-eared Bat", nm.sciToCommon["Myotis myotis"])
	assert.Equal(t, "Myotis myotis", nm.commonToSci[apicore.NormalizeForLookup("Greater Mouse-eared Bat")])

	// Without a resolver, a scientific-only label has no common name and is skipped.
	bare := buildNameMaps([]string{"Myotis myotis"}, nil)
	_, ok := bare.sciToCommon["Myotis myotis"]
	assert.False(t, ok)
}
