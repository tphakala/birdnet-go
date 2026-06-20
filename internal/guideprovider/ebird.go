package guideprovider

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// ebirdTaxonomyClient is the subset of the eBird client used for taxonomy
// enrichment. Declaring it as an interface keeps the provider testable.
type ebirdTaxonomyClient interface {
	BuildFamilyTree(ctx context.Context, scientificName string) (*ebird.TaxonomyTree, error)
}

// EBirdGuideProvider enriches guides with eBird taxonomy (genus/family and the
// localized common name). It carries no prose description of its own; in the
// fallback="all" merge it fills the taxonomy gaps left by Wikipedia.
type EBirdGuideProvider struct {
	client ebirdTaxonomyClient
}

// NewEBirdGuideProviderWithMetrics constructs an eBird provider. It returns an
// error when the client is unusable so the caller can log and skip registration
// without failing startup. The metrics sink is recorded by the cache around
// Fetch, so it is accepted for signature compatibility but not retained.
func NewEBirdGuideProviderWithMetrics(client ebirdTaxonomyClient, _ GuideCacheMetrics) (*EBirdGuideProvider, error) {
	if client == nil {
		return nil, errors.Newf("eBird client is nil").
			Component("guideprovider").
			Category(errors.CategoryConfiguration).
			Build()
	}
	return &EBirdGuideProvider{client: client}, nil
}

// Name returns the provider's registration name.
func (p *EBirdGuideProvider) Name() string { return EBirdProviderName }

// Close releases resources held by the underlying eBird client (notably its
// rate-limiter ticker). It satisfies the optional closer contract the cache
// invokes on Close, so a hot-reload that rebuilds the cache does not leak the
// previous client. Safe to call when the client does not expose Close.
func (p *EBirdGuideProvider) Close() error {
	if closer, ok := p.client.(interface{ Close() }); ok {
		closer.Close()
	}
	return nil
}

// Fetch returns taxonomy enrichment for a species.
func (p *EBirdGuideProvider) Fetch(ctx context.Context, scientificName string, _ FetchOptions) (*SpeciesGuide, error) {
	tree, err := p.client.BuildFamilyTree(ctx, scientificName)
	if err != nil {
		switch {
		case errors.IsNotFound(err):
			return nil, ErrGuideNotFound
		case errors.IsTransientNetworkError(err):
			return nil, NewTransientError(err)
		default:
			return nil, err
		}
	}
	if tree == nil {
		return nil, ErrGuideNotFound
	}
	return &SpeciesGuide{
		ScientificName: scientificName,
		CommonName:     tree.SpeciesCommon,
		Genus:          tree.Genus,
		Family:         tree.Family,
		SourceProvider: EBirdProviderName,
	}, nil
}
