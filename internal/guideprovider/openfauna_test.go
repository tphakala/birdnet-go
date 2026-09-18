package guideprovider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// fakeOpenFauna is a synthetic openFaunaLookup so the provider can be tested without
// depending on specific rows in the embedded dataset.
type fakeOpenFauna struct {
	taxonomy    openfauna.Taxonomy
	hasTaxonomy bool
	commonName  string
	hasCommon   bool
}

func (f *fakeOpenFauna) Taxonomy(string) (openfauna.Taxonomy, bool) {
	return f.taxonomy, f.hasTaxonomy
}

func (f *fakeOpenFauna) CommonName(string, string) (string, bool) {
	return f.commonName, f.hasCommon
}

func TestOpenFaunaProvider_Name(t *testing.T) {
	t.Parallel()
	p := NewOpenFaunaGuideProviderWithMetrics(noopMetrics{})
	assert.Equal(t, OpenFaunaProviderName, p.Name())
}

func TestOpenFaunaProvider_FetchEnrichment(t *testing.T) {
	t.Parallel()
	p := &OpenFaunaGuideProvider{lookup: &fakeOpenFauna{
		taxonomy:    openfauna.Taxonomy{Family: "Turdidae", Order: "Passeriformes"},
		hasTaxonomy: true,
		commonName:  "Mustarastas",
		hasCommon:   true,
	}}

	g, err := p.Fetch(t.Context(), "Turdus merula", FetchOptions{Locale: "fi"})
	require.NoError(t, err)
	assert.Equal(t, "Turdus", g.Genus)
	assert.Equal(t, "Turdidae", g.Family)
	assert.Equal(t, "Mustarastas", g.CommonName)
	assert.Equal(t, OpenFaunaProviderName, g.SourceProvider)
	// Enrichment-only: it must never carry prose (that is Wikipedia's job).
	assert.Empty(t, g.Description)
}

func TestOpenFaunaProvider_NotFoundMapsToGuideNotFound(t *testing.T) {
	t.Parallel()
	p := &OpenFaunaGuideProvider{lookup: &fakeOpenFauna{}} // neither metadata nor common name

	_, err := p.Fetch(t.Context(), "Nonexistent species", FetchOptions{})
	assert.True(t, errors.Is(err, ErrGuideNotFound))
}

func TestOpenFaunaProvider_TaxonomyOnlyStillResolves(t *testing.T) {
	t.Parallel()
	// Family present but no localized common name: still a usable enrichment, not a
	// not-found (so it does not suppress a valid species).
	p := &OpenFaunaGuideProvider{lookup: &fakeOpenFauna{
		taxonomy:    openfauna.Taxonomy{Family: "Corvidae"},
		hasTaxonomy: true,
	}}

	g, err := p.Fetch(t.Context(), "Corvus corax", FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Corvus", g.Genus)
	assert.Equal(t, "Corvidae", g.Family)
	assert.Empty(t, g.CommonName)
}

// TestOpenFaunaProvider_CommonNameOnlyStillResolves is the mirror of the case above,
// and pins that an EMPTY Family is the intended representation of "this species has
// no taxonomy row" rather than a defect.
//
// openfauna.Taxonomy is a struct value, so reading Family off the zero value is safe and
// yields "". That is deliberate and load-bearing: SpeciesGuide.Family has no
// "unknown" sentinel, and mergeGuides fills an empty primary field from the
// secondary, so returning "" is exactly what lets Wikipedia supply the family in the
// merged guide. Returning ErrGuideNotFound here instead would discard a perfectly
// good localized common name for every species the dataset translates but does not
// carry taxonomy for.
func TestOpenFaunaProvider_CommonNameOnlyStillResolves(t *testing.T) {
	t.Parallel()
	p := &OpenFaunaGuideProvider{lookup: &fakeOpenFauna{
		commonName: "Mustarastas",
		hasCommon:  true,
	}}

	g, err := p.Fetch(t.Context(), "Turdus merula", FetchOptions{Locale: "fi"})
	require.NoError(t, err, "a translated species must resolve even with no taxonomy row")
	assert.Equal(t, "Mustarastas", g.CommonName)
	assert.Equal(t, "Turdus", g.Genus, "genus is derived from the binomial, not from metadata")
	assert.Empty(t, g.Family, "an absent family is represented as empty, so a merge can fill it")

	// The whole point of the empty field: a secondary provider fills it.
	merged := mergeGuides(g, &SpeciesGuide{Family: "Turdidae"})
	assert.Equal(t, "Turdidae", merged.Family)
	assert.Equal(t, "Mustarastas", merged.CommonName, "the primary's own data still wins")
}
