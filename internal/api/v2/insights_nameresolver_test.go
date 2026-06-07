package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeResolver is a minimal datastore.SpeciesNameResolver for tests.
type fakeResolver struct{ m map[string]string }

func (f fakeResolver) Resolve(sci, _ string) string { return f.m[sci] }

func TestBuildNameMaps_ResolverLocalizes(t *testing.T) {
	// The resolver overrides the label's common name in both the forward
	// (sciToCommon) and reverse (commonToSci) maps, so insights display and
	// search both reflect the localized name.
	nm := buildNameMaps([]string{"Turdus merula_LabelName"},
		fakeResolver{m: map[string]string{"Turdus merula": "Mustarastas"}})
	assert.Equal(t, "Mustarastas", nm.sciToCommon["Turdus merula"])
	assert.Equal(t, "Turdus merula", nm.commonToSci[normalizeForLookup("Mustarastas")])
}

func TestBuildNameMaps_NilResolverKeepsLabel(t *testing.T) {
	nm := buildNameMaps([]string{"Turdus merula_LabelName"}, nil)
	assert.Equal(t, "LabelName", nm.sciToCommon["Turdus merula"])
}
