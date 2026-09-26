package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLiveLocation mutates the published settings snapshot, so it must not run in parallel.
func TestLiveLocation(t *testing.T) {
	prev := GetSettings()
	t.Cleanup(func() { StoreSettings(prev) })

	fallback := &Settings{}
	fallback.BirdNET.Latitude = 60.1699
	fallback.BirdNET.Longitude = 24.9384
	source := LiveLocation(fallback)

	StoreSettings(nil)
	lat, lon := source()
	assert.InDelta(t, 60.1699, lat, 1e-9, "without a published snapshot the fallback is used")
	assert.InDelta(t, 24.9384, lon, 1e-9, "without a published snapshot the fallback is used")

	published := &Settings{}
	published.BirdNET.Latitude = -33.8688
	published.BirdNET.Longitude = 151.2093
	StoreSettings(published)
	lat, lon = source()
	assert.InDelta(t, -33.8688, lat, 1e-9, "the published snapshot wins over the fallback")
	assert.InDelta(t, 151.2093, lon, 1e-9, "the published snapshot wins over the fallback")

	updated := CloneSettings(published)
	updated.BirdNET.Latitude = 40.7128
	updated.BirdNET.Longitude = -74.006
	StoreSettings(updated)
	lat, lon = source()
	assert.InDelta(t, 40.7128, lat, 1e-9, "a later snapshot is picked up on the next call")
	assert.InDelta(t, -74.006, lon, 1e-9, "a later snapshot is picked up on the next call")
}
