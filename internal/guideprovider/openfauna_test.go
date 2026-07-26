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
	meta       openfauna.Meta
	hasMeta    bool
	commonName string
	hasCommon  bool
}

func (f *fakeOpenFauna) Meta(string) (openfauna.Meta, bool) { return f.meta, f.hasMeta }

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
		meta:       openfauna.Meta{Family: "Turdidae", Order: "Passeriformes"},
		hasMeta:    true,
		commonName: "Mustarastas",
		hasCommon:  true,
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

func TestOpenFaunaProvider_MetaOnlyStillResolves(t *testing.T) {
	t.Parallel()
	// Family present but no localized common name: still a usable enrichment, not a
	// not-found (so it does not suppress a valid species).
	p := &OpenFaunaGuideProvider{lookup: &fakeOpenFauna{
		meta:    openfauna.Meta{Family: "Corvidae"},
		hasMeta: true,
	}}

	g, err := p.Fetch(t.Context(), "Corvus corax", FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Corvus", g.Genus)
	assert.Equal(t, "Corvidae", g.Family)
	assert.Empty(t, g.CommonName)
}
