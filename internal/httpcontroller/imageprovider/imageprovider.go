package imageprovider

import (
	"slices"
	"sync"
)

type ImageProvider interface {
	fetch(scientificName string) (BirdImage, error)
}

type BirdImage struct {
	Url         string
	LicenseName string
	LicenseUrl  string
	AuthorName  string
	AuthorUrl   string
}

type BirdImageCache struct {
	dataMap              sync.Map
	dataMutexMap         sync.Map
	birdImageProvider    ImageProvider
	nonBirdImageProvider ImageProvider
}

type emptyImageProvider struct {
}

func (l *emptyImageProvider) fetch(scientificName string) (BirdImage, error) {
	return BirdImage{}, nil
}

func initCache(e ImageProvider) *BirdImageCache {
	return &BirdImageCache{
		birdImageProvider:    e,
		nonBirdImageProvider: &emptyImageProvider{}, // TODO: Use a real image provider for non-birds
	}
}

func CreateDefaultCache() (*BirdImageCache, error) {
	provider, err := NewWikiMediaProvider()
	if err != nil {
		return nil, err
	}
	return initCache(provider), nil
}

func (c *BirdImageCache) Get(scientificName string) (info BirdImage, err error) {
	// Check if the bird image is already in the cache
	birdImage, ok := c.dataMap.Load(scientificName)
	if ok {
		return birdImage.(BirdImage), nil
	}

	// Use a per-item mutex to ensure only one query is performed per item
	mu, _ := c.dataMutexMap.LoadOrStore(scientificName, &sync.Mutex{})
	mutex := mu.(*sync.Mutex)

	mutex.Lock()
	defer mutex.Unlock()

	// Check again if bird image is cached after acquiring the lock
	birdImage, ok = c.dataMap.Load(scientificName)
	if ok {
		return birdImage.(BirdImage), nil
	}

	// Fetch the bird image from the image provider
	fetchedBirdImage, err := c.fetch(scientificName)
	if err != nil {
		// TODO for now store a empty result in the cache to avoid future queries that would fail.
		// In the future, look at the error and decide if it was caused by networking and is recoverable.
		// And if it was, do not store the empty result in the cache.
		c.dataMap.Store(scientificName, BirdImage{})
		return BirdImage{}, err
	}

	// Store the fetched image information in the cache
	c.dataMap.Store(scientificName, fetchedBirdImage)

	return fetchedBirdImage, nil
}

func (c *BirdImageCache) fetch(scientificName string) (info BirdImage, err error) {
	var imageProviderToUse ImageProvider

	// Determine the image provider based on the scientific name
	if slices.Contains([]string{
		"Dog",
		"Engine",
		"Environmental",
		"Fireworks",
		"Gun",
		"Human non-vocal",
		"Human vocal",
		"Human whistle",
		"Noise",
		"Power tools",
		"Siren",
	}, scientificName) {
		imageProviderToUse = c.nonBirdImageProvider
	} else {
		imageProviderToUse = c.birdImageProvider
	}

	// Fetch the image from the image provider
	return imageProviderToUse.fetch(scientificName)
}
