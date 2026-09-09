package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
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

// TestController_notifyRegionStaleness_Guards exercises the Controller glue's
// early-return guards (nil ModelManager, no installed regional models) so a
// coordinate-changing save never panics when the gallery is empty. The firing
// path (a stale installed regional variant) is covered by the pure detector and
// notification tests in internal/classifier.
func TestController_notifyRegionStaleness_Guards(t *testing.T) {
	old := regionTestSettings(60.17, 24.94, true)  // Helsinki
	next := regionTestSettings(4.61, -74.08, true) // Bogota

	// Nil ModelManager: no-op, no panic.
	c := &Controller{Core: &apicore.Core{}}
	assert.NotPanics(t, func() { c.notifyRegionStaleness(old, next) })

	// ModelManager set but nothing installed: InstalledRegionalModels is empty, so
	// the detector is never invoked and no notification is emitted.
	c.ModelManager = classifier.NewModelManager(t.TempDir(), nil, nil)
	assert.NotPanics(t, func() { c.notifyRegionStaleness(old, next) })
}
