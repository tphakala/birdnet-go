package datastore

import (
	"slices"
	"sync"
	"time"
)

// DetectionRateCache provides time-based caching for detection rate queries.
// Uses double-checked locking to avoid thundering herd on cache expiry.
type DetectionRateCache struct {
	mu        sync.RWMutex
	hourly    []HourlyCount
	hourlyExp time.Time
	daily     []DailyCount
	dailyDays int
	dailyExp  time.Time
	ttl       time.Duration
}

// NewDetectionRateCache creates a cache with the specified TTL.
func NewDetectionRateCache(ttl time.Duration) *DetectionRateCache {
	return &DetectionRateCache{ttl: ttl}
}

// GetHourly returns cached 24h hourly counts, refreshing via fetchFn if expired.
func (c *DetectionRateCache) GetHourly(fetchFn func() ([]HourlyCount, error)) ([]HourlyCount, error) {
	c.mu.RLock()
	if time.Now().Before(c.hourlyExp) && c.hourly != nil {
		data := slices.Clone(c.hourly)
		c.mu.RUnlock()
		return data, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	// Double-check after acquiring write lock
	if time.Now().Before(c.hourlyExp) && c.hourly != nil {
		return slices.Clone(c.hourly), nil
	}

	data, err := fetchFn()
	if err != nil {
		return nil, err
	}
	c.hourly = data
	c.hourlyExp = time.Now().Add(c.ttl)
	return slices.Clone(data), nil
}

// GetDaily returns cached daily counts, refreshing via fetchFn if expired or days changed.
func (c *DetectionRateCache) GetDaily(days int, fetchFn func(int) ([]DailyCount, error)) ([]DailyCount, error) {
	c.mu.RLock()
	if time.Now().Before(c.dailyExp) && c.daily != nil && c.dailyDays == days {
		data := slices.Clone(c.daily)
		c.mu.RUnlock()
		return data, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.dailyExp) && c.daily != nil && c.dailyDays == days {
		return slices.Clone(c.daily), nil
	}

	data, err := fetchFn(days)
	if err != nil {
		return nil, err
	}
	c.daily = data
	c.dailyDays = days
	c.dailyExp = time.Now().Add(c.ttl)
	return slices.Clone(data), nil
}
