package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSettings_RangeFilterConfig pins the range-filter accessor: it returns the
// field's own address, a write through it is visible on the field, and a nil receiver
// returns nil.
func TestSettings_RangeFilterConfig(t *testing.T) {
	t.Parallel()

	s := &Settings{}
	rf := s.RangeFilterConfig()
	require.NotNil(t, rf)
	assert.Same(t, &s.BirdNET.RangeFilter, rf, "the accessor must return &BirdNET.RangeFilter")

	rf.Model = "v3"
	rf.PassUnmappedSpecies = true
	assert.Equal(t, "v3", s.BirdNET.RangeFilter.Model, "a write through the accessor must be visible on the field")
	assert.True(t, s.BirdNET.RangeFilter.PassUnmappedSpecies)

	var nilSettings *Settings
	assert.Nil(t, nilSettings.RangeFilterConfig(), "a nil receiver must return nil")
}

// TestSettings_Location pins the location accessor: it returns the configured
// coordinates and flag, and a nil receiver returns the zero triple.
func TestSettings_Location(t *testing.T) {
	t.Parallel()

	s := &Settings{}
	s.BirdNET.Latitude = 60.17
	s.BirdNET.Longitude = 24.94
	s.BirdNET.LocationConfigured = true

	lat, lon, configured := s.Location()
	assert.InDelta(t, 60.17, lat, 1e-9, "latitude must round-trip")
	assert.InDelta(t, 24.94, lon, 1e-9, "longitude must round-trip")
	assert.True(t, configured, "LocationConfigured must round-trip")

	unset := &Settings{}
	lat, lon, configured = unset.Location()
	assert.Zero(t, lat)
	assert.Zero(t, lon)
	assert.False(t, configured, "an unconfigured location must report false")

	var nilSettings *Settings
	lat, lon, configured = nilSettings.Location()
	assert.Zero(t, lat)
	assert.Zero(t, lon)
	assert.False(t, configured, "a nil receiver must return (0, 0, false)")
}

// TestSettings_RangeFilterConfig_CloneIndependence verifies the accessor honours the
// clone-mutate-publish discipline: the clone's accessor points into the clone, so a
// write through it never reaches the original.
func TestSettings_RangeFilterConfig_CloneIndependence(t *testing.T) {
	t.Parallel()

	s := &Settings{}
	s.BirdNET.RangeFilter.Model = "v3"

	clone := CloneSettings(s)
	clone.RangeFilterConfig().Model = "legacy"

	assert.Equal(t, "v3", s.RangeFilterConfig().Model, "mutating the clone must not touch the original")
	assert.Equal(t, "legacy", clone.RangeFilterConfig().Model, "the clone must carry its own value")
}
