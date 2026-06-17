// guide_cache_init.go wires the species guide cache into the analysis pipeline:
// it builds the cache from settings, registers providers, and warms it.
package analysis

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// initGuideCacheIfNeeded builds and starts the species guide cache when the
// feature is enabled, returning nil when disabled or when the datastore cannot
// provide a GORM handle. It registers the Wikipedia provider always and the
// eBird provider only when the configuration opts in and an API key is present.
func initGuideCacheIfNeeded(settings *conf.Settings, ds datastore.Interface, gpMetrics *metrics.GuideProviderMetrics) *guideprovider.GuideCache {
	cfg := settings.Realtime.Dashboard.SpeciesGuide
	if !cfg.Enabled {
		return nil
	}

	log := GetLogger()

	// The GORM store needs the concrete datastore's *gorm.DB. In v2-only mode the
	// legacy DB is absent, so the guide feature is unavailable there.
	provider, ok := ds.(datastore.GormDBProvider)
	if !ok || provider.GormDB() == nil {
		log.Warn("Species guide enabled but no legacy database handle is available; guide cache disabled")
		return nil
	}

	store, err := guideprovider.NewGORMGuideStoreWithMetrics(provider.GormDB(), gpMetrics)
	if err != nil {
		log.Error("Failed to create species guide store; guide cache disabled", logger.Error(err))
		return nil
	}

	cache := guideprovider.NewGuideCache(store, gpMetrics)
	cache.SetFallbackPolicy(cfg.FallbackPolicy)
	cache.SetWarmTopN(cfg.WarmTopN)

	// Wikipedia is always available (no credentials required).
	cache.RegisterProvider(guideprovider.WikipediaProviderName,
		guideprovider.NewWikipediaGuideProviderWithMetrics(gpMetrics))

	// eBird enrichment is registered only when the user opted into it (provider
	// "auto" or "ebird") and an API key is configured. Failure to build it is
	// logged and skipped — it must never fail guide startup.
	if cfg.Provider != conf.SpeciesGuideProviderWikipedia &&
		settings.Realtime.EBird.Enabled && settings.Realtime.EBird.APIKey != "" {
		ebirdClient, eErr := ebird.NewClient(ebird.Config{
			APIKey:   settings.Realtime.EBird.APIKey,
			CacheTTL: time.Duration(settings.Realtime.EBird.CacheTTL) * time.Hour,
		})
		switch {
		case eErr != nil:
			log.Warn("eBird client init failed; skipping eBird guide provider", logger.Error(eErr))
		default:
			provider, pErr := guideprovider.NewEBirdGuideProviderWithMetrics(ebirdClient, gpMetrics)
			if pErr != nil {
				log.Warn("eBird guide provider init failed; skipping", logger.Error(pErr))
				ebirdClient.Close()
			} else {
				cache.RegisterProvider(guideprovider.EBirdProviderName, provider)
			}
		}
	}

	cache.Start()
	log.Info("Species guide cache initialized",
		logger.String("provider", cfg.Provider),
		logger.String("fallback_policy", cfg.FallbackPolicy))
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
	names := make([]string, 0, topN)
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
