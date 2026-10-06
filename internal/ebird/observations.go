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

// observationKey identifies one cached observations lookup. The coordinates
// are the %.4f strings the request URL uses, so keys round exactly like the
// request does.
type observationKey struct {
	lat, lng string
	days     int
}

// GetRecentObservations returns recent bird observations near the given coordinates.
// Uses eBird API v2: GET /v2/data/obs/geo/recent
// Results are cached for the configured CacheTTL duration. The returned slice
// is a copy the caller may modify.
func (c *Client) GetRecentObservations(ctx context.Context, lat, lng float64, days int) ([]Observation, error) {
	if days <= 0 || days > 30 {
		days = 14
	}

	key := observationKey{
		lat:  fmt.Sprintf("%.4f", lat),
		lng:  fmt.Sprintf("%.4f", lng),
		days: days,
	}
	obs, err := c.observations.GetOrLoad(ctx, key, func(loadCtx context.Context) ([]Observation, error) {
		url := fmt.Sprintf("%s/v2/data/obs/geo/recent?lat=%s&lng=%s&back=%d&maxResults=200",
			c.config.BaseURL, key.lat, key.lng, days)

		reqCtx, cancel := context.WithTimeout(loadCtx, c.config.Timeout)
		defer cancel()

		var observations []Observation
		if err := c.doRequestWithRetry(reqCtx, "GET", url, nil, &observations); err != nil {
			return nil, fmt.Errorf("get recent observations: %w", err)
		}

		GetLogger().Debug("fetched recent observations from eBird",
			logger.Float64("lat", lat),
			logger.Float64("lng", lng),
			logger.Int("days", days),
			logger.Int("count", len(observations)))

		return observations, nil
	})
	if err != nil {
		return nil, err
	}
	return slices.Clone(obs), nil
}
