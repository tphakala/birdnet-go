package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// TestReconfigureSunCalc covers the hot-reload path for the station location.
// The datastore builds its SunCalc once at startup (see InitializeFreshInstall
// and internal/analysis.initializeV2OnlyMode), so without this a location edited
// in the UI would leave time-of-day classification answering from the old
// observer until the process restarted.
func TestReconfigureSunCalc(t *testing.T) {
	t.Parallel()

	// Helsinki, then Sydney: a different hemisphere and a different IANA zone,
	// so the move changes both the sun events and the zone reporting them.
	const (
		helsinkiLatitude  = 60.1699
		helsinkiLongitude = 24.9384
		sydneyLatitude    = -33.8688
		sydneyLongitude   = 151.2093
	)

	date := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)

	ds := &Datastore{suncalc: suncalc.NewSunCalc(helsinkiLatitude, helsinkiLongitude)}

	before := ds.calculateTimeOfDay(date, helsinkiLatitude, helsinkiLongitude)
	require.NotEqual(t, datastore.TimeOfDayAny, before,
		"test setup must produce a real classification before the move")

	ds.ReconfigureSunCalc(sydneyLatitude, sydneyLongitude)

	// Midsummer midday in Helsinki is the middle of the polar-ish long day; the
	// same instant in Sydney is the middle of a winter night.
	after := ds.calculateTimeOfDay(date, sydneyLatitude, sydneyLongitude)
	assert.NotEqual(t, before, after,
		"classification must follow the new station location, not the old observer")
}

// TestReconfigureSunCalcWithoutSunCalc guards the nil case: a datastore built
// without a SunCalc (no location configured) must ignore the signal rather than
// panic.
func TestReconfigureSunCalcWithoutSunCalc(t *testing.T) {
	t.Parallel()

	ds := &Datastore{}
	assert.NotPanics(t, func() {
		ds.ReconfigureSunCalc(1, 2)
	})
}
