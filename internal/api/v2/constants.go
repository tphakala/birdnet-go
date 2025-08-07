package api

import "time"

// Cache duration constants for HTTP responses
const (
	// ImageCacheDuration is the cache duration for species images (30 days)
	// These are external images from wikimedia/flickr that are stable
	ImageCacheDuration = 30 * 24 * time.Hour

	// NotFoundCacheDuration is the cache duration for 404 responses (24 hours)
	// This prevents repeated lookups for missing resources
	NotFoundCacheDuration = 24 * time.Hour

	// SpectrogramCacheDuration is the cache duration for spectrograms (30 days)
	// Once generated, spectrograms don't change
	SpectrogramCacheDuration = 30 * 24 * time.Hour
)

// Cache duration in seconds for HTTP headers
const (
	// ImageCacheSeconds is the cache duration for species images in seconds
	ImageCacheSeconds = 2592000 // 30 days

	// NotFoundCacheSeconds is the cache duration for 404 responses in seconds
	NotFoundCacheSeconds = 86400 // 24 hours

	// SpectrogramCacheSeconds is the cache duration for spectrograms in seconds
	SpectrogramCacheSeconds = 2592000 // 30 days
)