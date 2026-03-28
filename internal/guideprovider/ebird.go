package guideprovider

import (
	"context"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// EBirdGuideProvider enriches species guide data using the eBird API.
// It wraps the existing ebird.Client and provides taxonomy-based enrichment.
type EBirdGuideProvider struct {
	client *ebird.Client
}

// NewEBirdGuideProvider creates a new EBirdGuideProvider wrapping the given client.
// Returns ErrProviderNotConfigured if the client is nil.
func NewEBirdGuideProvider(client *ebird.Client) (*EBirdGuideProvider, error) {
	if client == nil {
		return nil, ErrProviderNotConfigured
	}
	return &EBirdGuideProvider{client: client}, nil
}

// Fetch retrieves species guide information from the eBird taxonomy API.
// This provider only supplies taxonomy metadata (common name, extinction status).
// It does not provide descriptions.
func (p *EBirdGuideProvider) Fetch(ctx context.Context, scientificName string) (SpeciesGuide, error) {
	log := GetLogger()

	// Get taxonomy data
	taxonomy, err := p.client.GetTaxonomy(ctx, "en")
	if err != nil {
		log.Debug("eBird taxonomy lookup failed",
			logger.String("species", scientificName),
			logger.Any("error", err))
		return SpeciesGuide{}, errors.Newf("eBird taxonomy lookup: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}

	// Search for the species in the taxonomy
	for _, entry := range taxonomy {
		if entry.ScientificName == scientificName {
			guide := SpeciesGuide{
				ScientificName: scientificName,
				CommonName:     entry.CommonName,
				SourceProvider: EBirdProviderName,
				Partial:        true, // eBird provides no descriptions
			}

			// Set conservation status for extinct species
			if entry.Extinct {
				guide.ConservationStatus = fmt.Sprintf("Extinct (%d)", entry.ExtinctYear)
			}

			return guide, nil
		}
	}

	return SpeciesGuide{}, ErrGuideNotFound
}
