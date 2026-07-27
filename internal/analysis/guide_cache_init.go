// guide_cache_init.go wires the species guide cache into the analysis pipeline:
// it builds the cache from settings, registers providers, and warms it.
package analysis

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// warmRankingQueryTimeout bounds the species-ranking query that selects which
// species to warm. The query asks for the all-time summary of every species the
// station has ever detected (the datastore exposes no top-N variant), so on a
// long-running station it is the one unbounded piece of guide startup; warming is
// opportunistic, so timing out and skipping it is strictly better than delaying
// startup or blocking shutdown.
const warmRankingQueryTimeout = 30 * time.Second

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
	// Warm/pre-fetch in the dashboard locale so warming targets the same cache key the
	// UI requests; otherwise non-English instances warm "en" and pay a wasted fetch per
	// new detection while the user's locale stays cold.
	cache.SetWarmLocale(settings.Realtime.Dashboard.Locale)

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

// startGuideCacheWarm kicks off warmGuideCacheWithTopSpecies on a background
// goroutine and returns immediately. Callers on a latency-sensitive path must use
// this rather than calling the warm directly.
//
// The ranking query is the one unbounded piece of guide setup: it asks the datastore
// for the all-time summary of every species ever detected (a GROUP BY over the whole
// detection history — the datastore exposes no top-N variant), so on a long-running
// station it can run for seconds and is capped only by warmRankingQueryTimeout. Run
// inline it would delay the HTTP listener coming up by up to that timeout at startup,
// and would stall the single control-monitor goroutine — and every reconfigure signal
// queued behind it — whenever any species-guide setting is saved.
//
// Warming is purely opportunistic (a cold entry is simply fetched on demand), so
// detaching it costs nothing: nothing downstream waits on the result. The goroutine is
// deliberately untracked — it holds no resources beyond the query, honors ctx, and
// self-limits via warmRankingQueryTimeout, while cache.WarmForSpecies is already
// wait-group-tracked and no-ops on a closed cache. Pass a cancellable ctx where one
// exists so shutdown aborts the query rather than waiting it out.
func startGuideCacheWarm(ctx context.Context, cache *guideprovider.GuideCache, ds datastore.Interface, topN int, log logger.Logger) {
	// Check the no-op conditions up front so a disabled warm costs no goroutine at all.
	if cache == nil || topN <= 0 {
		return
	}
	go warmGuideCacheWithTopSpecies(ctx, cache, ds, topN, log)
}

// warmGuideCacheWithTopSpecies warms the guide cache for the top-N most-detected
// species, so the entries most likely to be viewed are pre-loaded. It is a no-op
// when warming is disabled (topN <= 0).
//
// This blocks on the ranking query; production callers go through startGuideCacheWarm.
// The query is bounded by warmRankingQueryTimeout and honors ctx, so it cannot outlive
// a shutdown that begins while it is still running.
func warmGuideCacheWithTopSpecies(ctx context.Context, cache *guideprovider.GuideCache, ds datastore.Interface, topN int, log logger.Logger) {
	if cache == nil || topN <= 0 {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, warmRankingQueryTimeout)
	defer cancel()
	names := topDetectedSpeciesNames(queryCtx, ds, topN, log)
	if len(names) > 0 {
		cache.WarmForSpecies(names)
	}
}

// topDetectedSpeciesNames returns up to topN scientific names ranked by all-time
// detection count (most-detected first, ties broken by most-recently-seen then name).
// If the ranked summary query is unavailable it falls back to the alphabetical
// GetAllDetectedSpecies list, so warming still runs (just unranked) rather than being
// skipped entirely.
func topDetectedSpeciesNames(ctx context.Context, ds datastore.Interface, topN int, log logger.Logger) []string {
	summary, err := ds.GetSpeciesSummaryData(ctx, "", "")
	if err != nil {
		log.Warn("Failed to load ranked species for guide cache warming; falling back to unranked list",
			logger.Error(err))
		return firstDetectedSpeciesNames(ds, topN, log)
	}
	if len(summary) == 0 {
		return nil
	}
	// Rank by detection count desc, then most-recent-seen desc, then name for a stable
	// order among ties (map/query order alone is not "top-N").
	slices.SortFunc(summary, func(a, b datastore.SpeciesSummaryData) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		if c := b.LastSeen.Compare(a.LastSeen); c != 0 {
			return c
		}
		return strings.Compare(a.ScientificName, b.ScientificName)
	})
	return takeSpeciesNames(topN, len(summary), func(i int) string { return summary[i].ScientificName })
}

// firstDetectedSpeciesNames is the unranked fallback: the alphabetical
// GetAllDetectedSpecies list truncated to topN.
func firstDetectedSpeciesNames(ds datastore.Interface, topN int, log logger.Logger) []string {
	species, err := ds.GetAllDetectedSpecies()
	if err != nil {
		log.Warn("Failed to load species for guide cache warming", logger.Error(err))
		return nil
	}
	return takeSpeciesNames(topN, len(species), func(i int) string { return species[i].ScientificName })
}

// takeSpeciesNames collects up to topN non-empty scientific names from an indexed
// source, preallocating by the smaller of topN and the source length so an
// out-of-range topN cannot drive an oversized allocation.
func takeSpeciesNames(topN, count int, nameAt func(i int) string) []string {
	names := make([]string, 0, min(topN, count))
	for i := range count {
		if len(names) >= topN {
			break
		}
		if name := nameAt(i); name != "" {
			names = append(names, name)
		}
	}
	return names
}
