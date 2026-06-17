package guideprovider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
)

type fakeEBirdClient struct {
	tree *ebird.TaxonomyTree
	err  error
}

func (f fakeEBirdClient) BuildFamilyTree(_ context.Context, _ string) (*ebird.TaxonomyTree, error) {
	return f.tree, f.err
}

func TestEBirdProvider_NilClientErrors(t *testing.T) {
	t.Parallel()
	_, err := NewEBirdGuideProviderWithMetrics(nil, noopMetrics{})
	require.Error(t, err)
}

func TestEBirdProvider_FetchEnrichment(t *testing.T) {
	t.Parallel()
	client := fakeEBirdClient{tree: &ebird.TaxonomyTree{
		Genus:         "Turdus",
		Family:        "Turdidae",
		SpeciesCommon: "Common Blackbird",
	}}
	p, err := NewEBirdGuideProviderWithMetrics(client, noopMetrics{})
	require.NoError(t, err)
	assert.Equal(t, EBirdProviderName, p.Name())

	g, err := p.Fetch(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Turdus", g.Genus)
	assert.Equal(t, "Turdidae", g.Family)
	assert.Equal(t, "Common Blackbird", g.CommonName)
	assert.Equal(t, EBirdProviderName, g.SourceProvider)
}

func TestEBirdProvider_NotFoundMapsToGuideNotFound(t *testing.T) {
	t.Parallel()
	notFound := errors.Newf("species not found in eBird taxonomy").
		Category(errors.CategoryNotFound).
		Component("ebird").
		Build()
	client := fakeEBirdClient{err: notFound}
	p, err := NewEBirdGuideProviderWithMetrics(client, noopMetrics{})
	require.NoError(t, err)

	_, err = p.Fetch(t.Context(), "Nope", FetchOptions{})
	assert.True(t, errors.Is(err, ErrGuideNotFound))
}
