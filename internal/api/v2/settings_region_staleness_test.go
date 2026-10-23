package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// regionTestSettings builds a minimal settings snapshot carrying a station
// location, for the coordinate-change and staleness tests.
func regionTestSettings(lat, lon float64, configured bool) *conf.Settings {
	s := &conf.Settings{}
	s.BirdNET.Latitude = lat
	s.BirdNET.Longitude = lon
	s.BirdNET.LocationConfigured = configured
	return s
}

// TestCoordinatesChanged covers the gate that drives both range-filter reload and
// region staleness detection. The LocationConfigured leg matters: setting a
// location for the first time can leave the numeric coordinates unchanged (e.g.
// staying at 0,0), and that must still count as a change.
func TestCoordinatesChanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		old  *conf.Settings
		next *conf.Settings
		want bool
	}{
		{"identical", regionTestSettings(60.17, 24.94, true), regionTestSettings(60.17, 24.94, true), false},
		{"latitude changed", regionTestSettings(60.17, 24.94, true), regionTestSettings(4.61, 24.94, true), true},
		{"longitude changed", regionTestSettings(60.17, 24.94, true), regionTestSettings(60.17, -74.08, true), true},
		{"location configured first time, same coords", regionTestSettings(0, 0, false), regionTestSettings(0, 0, true), true},
		{"both unconfigured, same coords", regionTestSettings(0, 0, false), regionTestSettings(0, 0, false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, coordinatesChanged(tt.old, tt.next))
		})
	}
}

// TestController_notifyRegionStaleness_NilModelManager verifies the Controller
// glue returns early without a ModelManager, so a coordinate-changing save never
// panics on a controller that has none.
func TestController_notifyRegionStaleness_NilModelManager(t *testing.T) {
	old := regionTestSettings(60.17, 24.94, true)  // Helsinki
	next := regionTestSettings(4.61, -74.08, true) // Bogota

	c := &Controller{Core: &apicore.Core{}}
	assert.NotPanics(t, func() { c.notifyRegionStaleness(old, next) })
}
