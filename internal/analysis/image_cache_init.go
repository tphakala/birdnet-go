package analysis

import (
	"sync"

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

// warmUpImageCache pre-fetches images for species not yet in the cache.
// Runs in a background goroutine with bounded concurrency.
func warmUpImageCache(cache *imageprovider.BirdImageCache, species []string) {
	if len(species) == 0 {
		return
	}

	log := GetLogger()
	log.Info("starting image cache warm-up", logger.Int("species_count", len(species)))

	const maxConcurrent = 5
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for _, name := range species {
		wg.Add(1)
		go func(sciName string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if _, err := cache.Get(sciName); err != nil {
				log.Debug("warm-up fetch failed",
					logger.String("species", sciName),
					logger.Error(err))
			}
		}(name)
	}

	wg.Wait()
	log.Info("image cache warm-up complete", logger.Int("species_count", len(species)))
}
