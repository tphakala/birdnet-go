package analysis

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/api"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// initBirdImageCache sets up image providers and selects a default cache for
// bird thumbnails. It registers wikimedia and avicommons providers into the
// global registry, picks the preferred (or best available) provider, and
// kicks off a background warm-up for species not yet cached.
// Returns nil if no provider could be initialized.
func initBirdImageCache(settings *conf.Settings, ds datastore.Interface, metrics *observability.Metrics) *imageprovider.BirdImageCache {
	log := GetLogger()
	registry := api.ImageProviderRegistry
	if registry == nil {
		log.Error("image provider registry not initialized")
		return nil
	}

	// Register providers (error-tolerant — one failing doesn't block the other).
	registerImageProviders(log, registry, ds, metrics)

	// Wire cross-provider fallback support.
	registry.RangeProviders(func(_ string, cache *imageprovider.BirdImageCache) bool {
		cache.SetRegistry(registry)
		return true
	})

	// Select default provider based on settings.
	defaultCache := selectImageProvider(log, registry, settings.Realtime.Dashboard.Thumbnails.ImageProvider)
	if defaultCache == nil {
		log.Error("no image providers available")
		return nil
	}

	// Warm up cache in background.
	speciesList, err := ds.GetAllDetectedSpecies()
	if err != nil {
		log.Warn("failed to get detected species for cache warm-up", logger.Error(err))
		return defaultCache
	}

	// Filter empty scientific names once.
	names := make([]string, 0, len(speciesList))
	for i := range speciesList {
		if speciesList[i].ScientificName != "" {
			names = append(names, speciesList[i].ScientificName)
		}
	}

	go warmUpImageCache(defaultCache, names)

	return defaultCache
}

// registerImageProviders registers wikimedia and avicommons image providers into the registry.
// Failures are logged but don't prevent other providers from registering.
func registerImageProviders(log logger.Logger, registry *imageprovider.ImageProviderRegistry, ds datastore.Interface, metrics *observability.Metrics) {
	if _, err := registry.GetOrRegister("wikimedia", func() (*imageprovider.BirdImageCache, error) {
		return imageprovider.CreateDefaultCache(metrics, ds)
	}); err != nil {
		log.Error("failed to register wikimedia provider", logger.Error(err))
	}

	if _, err := registry.GetOrRegister("avicommons", func() (*imageprovider.BirdImageCache, error) {
		return imageprovider.CreateAviCommonsCache(api.ImageDataFs, metrics, ds)
	}); err != nil {
		log.Error("failed to register avicommons provider", logger.Error(err))
	}
}

// selectImageProvider picks the default image provider based on the preferred setting.
// Falls back to avicommons, then any available provider.
func selectImageProvider(log logger.Logger, registry *imageprovider.ImageProviderRegistry, preferred string) *imageprovider.BirdImageCache {
	// Try preferred provider.
	if preferred != "" && preferred != "auto" {
		if cache, ok := registry.GetCache(preferred); ok {
			log.Info("selected image provider", logger.String("provider", preferred))
			return cache
		}
		log.Warn("preferred image provider not available", logger.String("preferred", preferred))
	}

	// Default/fallback: avicommons.
	if cache, ok := registry.GetCache("avicommons"); ok {
		log.Info("selected image provider", logger.String("provider", "avicommons"))
		return cache
	}

	// Last resort: any registered provider.
	var fallback *imageprovider.BirdImageCache
	registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
		log.Info("selected fallback image provider", logger.String("provider", name))
		fallback = cache
		return false
	})
	return fallback
}

const (
	// warmUpRetryDelay is how long warm-up waits for the prefetch queue to drain
	// before retrying one species: roughly the time it takes a running prefetch
	// to finish and free its slot.
	warmUpRetryDelay = 2 * time.Second
	// warmUpMaxConsecutiveDrops stops the whole warm-up once this many species in
	// a row are refused. PrefetchAsync answers with a bare bool and declines for
	// several unrelated reasons, only one of which is worth waiting out, so
	// warm-up cannot tell a full queue from a closing cache or from a species
	// whose last resolution just failed. Bounding the run of refusals is what
	// keeps a shutting-down cache from walking the entire species list, and it
	// costs a few seconds at most.
	warmUpMaxConsecutiveDrops = 3
)

// warmUpImageCache pre-fetches images for species not yet in the cache.
//
// It schedules through the cache's own prefetch machinery rather than calling
// the blocking Get on raw goroutines. That older loop had its own 5-slot
// semaphore, so it ignored the cache's concurrency and queue bounds, and its
// fetches ran on context.Background(), untracked by the cache's WaitGroup and
// so still running past Close.
//
// PrefetchAsync declines once its queue is full, so this applies backpressure
// rather than substituting for it: a straight call per species would silently
// drop everything past the queue cap on a large install.
func warmUpImageCache(cache *imageprovider.BirdImageCache, species []string) {
	if len(species) == 0 {
		return
	}

	log := GetLogger()
	log.Info("starting image cache warm-up", logger.Int("species_count", len(species)))

	scheduled, consecutiveDrops := 0, 0
	for _, name := range species {
		if warmUpSchedule(cache, name) {
			scheduled++
			consecutiveDrops = 0
			continue
		}

		consecutiveDrops++
		log.Debug("warm-up could not schedule a species", logger.String("species", name))
		if consecutiveDrops >= warmUpMaxConsecutiveDrops {
			// Deliberately not blamed on the queue: the refusal carries no
			// reason, and a closing cache, a species in backoff and a full queue
			// are indistinguishable from here.
			log.Info("stopping image cache warm-up: prefetches are being declined",
				logger.Int("scheduled", scheduled),
				logger.Int("not_scheduled", len(species)-scheduled))
			return
		}
	}

	log.Info("image cache warm-up scheduled",
		logger.Int("scheduled", scheduled),
		logger.Int("not_scheduled", len(species)-scheduled))
}

// warmUpSchedule registers one species for prefetching, waiting once for the
// queue to drain.
//
// A single retry is deliberate. If the refusal is queue pressure, a slot frees
// as running prefetches complete. If it is anything else no amount of waiting
// helps: a species backed off after a failed resolution holds that marker for
// longer than any retry ladder worth running, and the request path resolves it
// on demand anyway. The previous exponential ladder spent 12.7 seconds per
// species discovering that, and burned it again on every species after Close.
func warmUpSchedule(cache *imageprovider.BirdImageCache, name string) bool {
	if cache.PrefetchAsync(name) {
		return true
	}
	time.Sleep(warmUpRetryDelay)
	return cache.PrefetchAsync(name)
}
