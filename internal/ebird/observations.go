package ebird

import (
	"context"
	"fmt"
	"slices"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// Observation represents a recent bird observation from eBird.
type Observation struct {
	SpeciesCode    string  `json:"speciesCode"`
	CommonName     string  `json:"comName"`
	ScientificName string  `json:"sciName"`
	LocationName   string  `json:"locName"`
	ObservationDt  string  `json:"obsDt"`
	Latitude       float64 `json:"lat"`
	Longitude      float64 `json:"lng"`
	HowMany        int     `json:"howMany"`
}

// GetRecentObservations returns recent bird observations near the given coordinates.
// Uses eBird API v2: GET /v2/data/obs/geo/recent
// Results are cached for the configured CacheTTL duration.
func (c *Client) GetRecentObservations(ctx context.Context, lat, lng float64, days int) ([]Observation, error) {
	if days <= 0 || days > 30 {
		days = 14
	}

	cacheKey := fmt.Sprintf("obs_recent_%.4f_%.4f_%d", lat, lng, days)
	if cached, found := c.cache.Get(cacheKey); found {
		if obs, ok := cached.([]Observation); ok {
			c.metrics.mu.Lock()
			c.metrics.cacheHits++
			c.metrics.mu.Unlock()
			return slices.Clone(obs), nil
		}
	}

	url := fmt.Sprintf("%s/v2/data/obs/geo/recent?lat=%.4f&lng=%.4f&back=%d&maxResults=200",
		c.config.BaseURL, lat, lng, days)

	reqCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	var observations []Observation
	if err := c.doRequestWithRetry(reqCtx, "GET", url, nil, &observations); err != nil {
		return nil, fmt.Errorf("get recent observations: %w", err)
	}

	c.cache.Set(cacheKey, slices.Clone(observations), c.config.CacheTTL)

	c.metrics.mu.Lock()
	c.metrics.cacheMisses++
	c.metrics.mu.Unlock()

	GetLogger().Debug("fetched recent observations from eBird",
		logger.Float64("lat", lat),
		logger.Float64("lng", lng),
		logger.Int("days", days),
		logger.Int("count", len(observations)))

	return observations, nil
}
