// guide_cache_init.go wires the species guide cache into the analysis pipeline:
// it builds the cache from settings, registers providers, and warms it.
package analysis

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// initGuideCacheIfNeeded builds and starts the species guide cache when the
// feature is enabled, returning nil when disabled or when the datastore cannot
// provide a GORM handle. OpenFauna is always registered as the primary, offline
// provider (taxonomy, common names, links); Wikipedia is registered as the
// secondary description provider only when the user opts in via EnableWikipedia.
func initGuideCacheIfNeeded(settings *conf.Settings, ds datastore.Interface, gpMetrics *metrics.GuideProviderMetrics) *guideprovider.GuideCache {
	cfg := settings.Realtime.Dashboard.SpeciesGuide
	if !cfg.Enabled {
		return nil
	}

	log := GetLogger()

	// The GORM store needs the datastore's *gorm.DB. Both the legacy and v2-only
	// datastores expose it via GormDBProvider; if neither is available (or the
	// handle is nil) the guide cache cannot be built.
	provider, ok := ds.(datastore.GormDBProvider)
	if !ok || provider.GormDB() == nil {
		log.Warn("Species guide enabled but no database handle is available; guide cache disabled")
		return nil
	}

	store, err := guideprovider.NewGORMGuideStoreWithMetrics(provider.GormDB(), gpMetrics)
	if err != nil {
		log.Error("Failed to create species guide store; guide cache disabled", logger.Error(err))
		return nil
	}

	cache := guideprovider.NewGuideCache(store, gpMetrics)
	cache.SetWarmTopN(cfg.WarmTopN)

	// OpenFauna is the primary provider: registered first (so it is the merge base
	// and DB cache key), it supplies offline taxonomy, localized common names, and
	// external links with no credentials or network. It is always present when the
	// guide is enabled.
	cache.RegisterProvider(guideprovider.OpenFaunaProviderName,
		guideprovider.NewOpenFaunaGuideProviderWithMetrics(gpMetrics))

	// Wikipedia is the optional secondary provider, supplying only the online prose
	// description (the one thing OpenFauna cannot). It is registered solely when the
	// user opts in (EnableWikipedia); otherwise the guide stays fully offline. The
	// cache's default merge policy ("all") consults every registered provider, so a
	// registered Wikipedia fills the description gap left by OpenFauna.
	if cfg.EnableWikipedia {
		cache.RegisterProvider(guideprovider.WikipediaProviderName,
			guideprovider.NewWikipediaGuideProviderWithMetrics(gpMetrics))
	}

	cache.Start()
	log.Info("Species guide cache initialized",
		logger.Bool("wikipedia_descriptions", cfg.EnableWikipedia))
	return cache
}

// warmGuideCacheWithTopSpecies warms the guide cache for the top-N detected
// species. It is a no-op when warming is disabled (topN <= 0).
func warmGuideCacheWithTopSpecies(cache *guideprovider.GuideCache, ds datastore.Interface, topN int, log logger.Logger) {
	if cache == nil || topN <= 0 {
		return
	}
	species, err := ds.GetAllDetectedSpecies()
	if err != nil {
		log.Warn("Failed to load species for guide cache warming", logger.Error(err))
		return
	}
	// Preallocate by the smaller of the warm target and the actual species count,
	// so an out-of-range topN can never drive an oversized allocation here.
	names := make([]string, 0, min(topN, len(species)))
	for i := range species {
		if len(names) >= topN {
			break
		}
		if species[i].ScientificName != "" {
			names = append(names, species[i].ScientificName)
		}
	}
	if len(names) > 0 {
		cache.WarmForSpecies(names)
	}
}
