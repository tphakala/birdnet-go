package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Helsinki coordinates for suncalc tests.
const (
	helsinkiLatitude  = 60.1699
	helsinkiLongitude = 24.9384
)

// referenceDate returns March 1, 2026 at noon in UTC for test consistency.
func referenceDate() time.Time {
	return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
}

// newTestSunCalc creates a SunCalc instance for Helsinki.
func newTestSunCalc() *suncalc.SunCalc {
	return suncalc.NewSunCalc(helsinkiLatitude, helsinkiLongitude)
}

func TestIsDaylightFilterSpecies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		enabled        bool
		allSpecies     bool
		speciesMap     map[string]bool
		scientificName string
		expected       bool
	}{
		{
			name:           "disabled returns false",
			enabled:        false,
			scientificName: "Strix aluco",
			expected:       false,
		},
		{
			name:           "all species mode returns true",
			enabled:        true,
			allSpecies:     true,
			scientificName: "Strix aluco",
			expected:       true,
		},
		{
			name:           "matching species returns true",
			enabled:        true,
			allSpecies:     false,
			speciesMap:     map[string]bool{"strix aluco": true},
			scientificName: "Strix aluco",
			expected:       true,
		},
		{
			name:           "non-matching species returns false",
			enabled:        true,
			allSpecies:     false,
			speciesMap:     map[string]bool{"strix aluco": true},
			scientificName: "Parus major",
			expected:       false,
		},
		{
			name:           "case-insensitive matching",
			enabled:        true,
			allSpecies:     false,
			speciesMap:     map[string]bool{"bubo bubo": true},
			scientificName: "BUBO BUBO",
			expected:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Processor{
				Settings: &conf.Settings{
					Realtime: conf.RealtimeSettings{
						DaylightFilter: conf.DaylightFilterSettings{Enabled: tt.enabled},
					},
				},
				daylightFilterAll:     tt.allSpecies,
				daylightFilterSpecies: tt.speciesMap,
			}
			assert.Equal(t, tt.expected, p.isDaylightFilterSpecies(tt.scientificName))
		})
	}
}

func TestIsDaylight(t *testing.T) {
	t.Parallel()

	sc := newTestSunCalc()
	refDate := referenceDate()

	// Get actual sun event times so tests are timezone-independent.
	sunTimes, err := sc.GetSunEventTimes(refDate)
	require.NoError(t, err, "failed to get sun event times for reference date")

	// Offset 0 for these tests.
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				DaylightFilter: conf.DaylightFilterSettings{
					Enabled: true,
					Offset:  0,
				},
			},
		},
		sunCalc: sc,
	}

	tests := []struct {
		name     string
		time     time.Time
		expected bool
	}{
		{
			name:     "midday is daylight",
			time:     sunTimes.CivilDawn.Add(3 * time.Hour),
			expected: true,
		},
		{
			name:     "well before civil dawn is not daylight",
			time:     sunTimes.CivilDawn.Add(-2 * time.Hour),
			expected: false,
		},
		{
			name:     "well after civil dusk is not daylight",
			time:     sunTimes.CivilDusk.Add(2 * time.Hour),
			expected: false,
		},
		{
			name:     "exactly at civil dawn is daylight (inclusive start)",
			time:     sunTimes.CivilDawn,
			expected: true,
		},
		{
			name:     "exactly at civil dusk is not daylight (exclusive end)",
			time:     sunTimes.CivilDusk,
			expected: false,
		},
		{
			name:     "one second before civil dawn is not daylight",
			time:     sunTimes.CivilDawn.Add(-1 * time.Second),
			expected: false,
		},
		{
			name:     "one second before civil dusk is daylight",
			time:     sunTimes.CivilDusk.Add(-1 * time.Second),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := p.isDaylight(tt.time)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsDaylightWithPositiveOffset(t *testing.T) {
	t.Parallel()

	sc := newTestSunCalc()
	refDate := referenceDate()

	sunTimes, err := sc.GetSunEventTimes(refDate)
	require.NoError(t, err)

	// Positive offset = shrink window (more lenient, less daylight).
	// daylightStart = CivilDawn + 2h, daylightEnd = CivilDusk - 2h
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				DaylightFilter: conf.DaylightFilterSettings{
					Enabled: true,
					Offset:  2,
				},
			},
		},
		sunCalc: sc,
	}

	// Just after original civil dawn but before adjusted start → not daylight
	t.Run("after dawn but within offset is not daylight", func(t *testing.T) {
		t.Parallel()
		testTime := sunTimes.CivilDawn.Add(1 * time.Hour)
		result, err := p.isDaylight(testTime)
		require.NoError(t, err)
		assert.False(t, result, "time within positive offset window should not be daylight")
	})

	// Midday should still be daylight
	t.Run("midday still daylight", func(t *testing.T) {
		t.Parallel()
		testTime := sunTimes.CivilDawn.Add(5 * time.Hour)
		result, err := p.isDaylight(testTime)
		require.NoError(t, err)
		assert.True(t, result, "midday should still be daylight with positive offset")
	})
}

func TestIsDaylightWithNegativeOffset(t *testing.T) {
	t.Parallel()

	sc := newTestSunCalc()
	refDate := referenceDate()

	sunTimes, err := sc.GetSunEventTimes(refDate)
	require.NoError(t, err)

	// Negative offset = expand window (stricter, more daylight).
	// daylightStart = CivilDawn - 1h, daylightEnd = CivilDusk + 1h
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				DaylightFilter: conf.DaylightFilterSettings{
					Enabled: true,
					Offset:  -1,
				},
			},
		},
		sunCalc: sc,
	}

	// Before civil dawn but within expanded window → daylight
	t.Run("before dawn within expanded window is daylight", func(t *testing.T) {
		t.Parallel()
		testTime := sunTimes.CivilDawn.Add(-30 * time.Minute)
		result, err := p.isDaylight(testTime)
		require.NoError(t, err)
		assert.True(t, result, "time within negative offset expansion should be daylight")
	})

	// After civil dusk within expanded window → daylight
	t.Run("after dusk within expanded window is daylight", func(t *testing.T) {
		t.Parallel()
		testTime := sunTimes.CivilDusk.Add(30 * time.Minute)
		result, err := p.isDaylight(testTime)
		require.NoError(t, err)
		assert.True(t, result, "time within negative offset expansion should be daylight")
	})
}

func TestIsDaylightInvertedWindow(t *testing.T) {
	t.Parallel()

	sc := newTestSunCalc()
	refDate := referenceDate()

	sunTimes, err := sc.GetSunEventTimes(refDate)
	require.NoError(t, err)

	// Use an extreme positive offset that inverts the window.
	// For Helsinki in March, civil dawn to civil dusk is roughly 11 hours.
	// An offset of 12 hours would push start past end.
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				DaylightFilter: conf.DaylightFilterSettings{
					Enabled: true,
					Offset:  12,
				},
			},
		},
		sunCalc: sc,
	}

	// Midday should return false because the window is inverted.
	testTime := sunTimes.CivilDawn.Add(5 * time.Hour)
	result, err := p.isDaylight(testTime)
	require.NoError(t, err)
	assert.False(t, result, "inverted window should always return false")
}

func TestCheckDaylightFilter(t *testing.T) {
	t.Parallel()

	sc := newTestSunCalc()
	refDate := referenceDate()

	sunTimes, err := sc.GetSunEventTimes(refDate)
	require.NoError(t, err)

	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				DaylightFilter: conf.DaylightFilterSettings{
					Enabled: true,
					Offset:  0,
				},
			},
		},
		sunCalc:               sc,
		daylightFilterAll:     false,
		daylightFilterSpecies: map[string]bool{"strix aluco": true},
	}

	tests := []struct {
		name           string
		scientificName string
		detectionTime  time.Time
		wantDiscard    bool
	}{
		{
			name:           "owl during day is discarded",
			scientificName: "Strix aluco",
			detectionTime:  sunTimes.CivilDawn.Add(3 * time.Hour),
			wantDiscard:    true,
		},
		{
			name:           "owl at night is kept",
			scientificName: "Strix aluco",
			detectionTime:  sunTimes.CivilDawn.Add(-2 * time.Hour),
			wantDiscard:    false,
		},
		{
			name:           "non-owl during day is kept",
			scientificName: "Parus major",
			detectionTime:  sunTimes.CivilDawn.Add(3 * time.Hour),
			wantDiscard:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := p.checkDaylightFilter(tt.scientificName, tt.detectionTime)
			assert.Equal(t, tt.wantDiscard, result)
		})
	}
}

func TestCheckDaylightFilterDisabled(t *testing.T) {
	t.Parallel()

	sc := newTestSunCalc()
	refDate := referenceDate()

	sunTimes, err := sc.GetSunEventTimes(refDate)
	require.NoError(t, err)

	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				DaylightFilter: conf.DaylightFilterSettings{
					Enabled: false,
				},
			},
		},
		sunCalc:               sc,
		daylightFilterAll:     false,
		daylightFilterSpecies: map[string]bool{"strix aluco": true},
	}

	// Even a matching species during daylight should not be discarded when disabled.
	result := p.checkDaylightFilter("Strix aluco", sunTimes.CivilDawn.Add(3*time.Hour))
	assert.False(t, result, "disabled filter should never discard")
}

func TestInitDaylightFilterWithTaxonomy(t *testing.T) {
	t.Parallel()

	db, err := birdnet.LoadTaxonomyDatabase()
	require.NoError(t, err, "failed to load taxonomy database")

	// "Strigiformes" is the order containing all owls.
	isAll, resolved := resolveSpeciesFilter([]string{"Strigiformes"}, nil, db, "daylight_filter")
	assert.False(t, isAll, "Strigiformes should not resolve to all species")
	assert.Greater(t, len(resolved), 100,
		"Strigiformes should resolve to >100 species, got %d", len(resolved))

	// Check specific owl species are included.
	assert.True(t, resolved["strix aluco"],
		"Strigiformes should include Strix aluco (Tawny Owl)")
	assert.True(t, resolved["bubo bubo"],
		"Strigiformes should include Bubo bubo (Eurasian Eagle-Owl)")
	assert.True(t, resolved["athene noctua"],
		"Strigiformes should include Athene noctua (Little Owl)")
}

func TestInitDaylightFilterUnconfiguredLocation(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{
				Latitude:  0,
				Longitude: 0,
			},
			Realtime: conf.RealtimeSettings{
				DaylightFilter: conf.DaylightFilterSettings{
					Enabled: true,
					Species: []string{"Strix aluco"},
				},
			},
		},
		sunCalc: newTestSunCalc(),
		// Pre-populate to verify they get cleared
		daylightFilterAll:     true,
		daylightFilterSpecies: map[string]bool{"strix aluco": true},
	}

	p.initDaylightFilter()

	p.daylightFilterMu.RLock()
	defer p.daylightFilterMu.RUnlock()
	assert.Nil(t, p.daylightFilterSpecies,
		"species should be cleared with unconfigured location")
	assert.False(t, p.daylightFilterAll,
		"all-species flag should be false with unconfigured location")
}

func TestInitDaylightFilterEmptySpeciesList(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{
				Latitude:  60.1699,
				Longitude: 24.9384,
			},
			Realtime: conf.RealtimeSettings{
				DaylightFilter: conf.DaylightFilterSettings{
					Enabled: true,
					Species: []string{}, // empty list
				},
			},
		},
		sunCalc: newTestSunCalc(),
		// Pre-populate to verify they get cleared
		daylightFilterAll:     true,
		daylightFilterSpecies: map[string]bool{"strix aluco": true},
	}

	p.initDaylightFilter()

	p.daylightFilterMu.RLock()
	defer p.daylightFilterMu.RUnlock()
	assert.False(t, p.daylightFilterAll,
		"empty species list should NOT enable filter-all for an exclusionary filter")
	assert.Nil(t, p.daylightFilterSpecies,
		"species should be nil with empty species list")
}

func TestInitDaylightFilterDisabled(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				DaylightFilter: conf.DaylightFilterSettings{
					Enabled: false,
				},
			},
		},
		// Pre-populate to verify they get cleared.
		daylightFilterSpecies: map[string]bool{"strix aluco": true},
		daylightFilterAll:     true,
	}

	p.initDaylightFilter()

	p.daylightFilterMu.RLock()
	defer p.daylightFilterMu.RUnlock()
	assert.Nil(t, p.daylightFilterSpecies,
		"species should be cleared when filter is disabled")
	assert.False(t, p.daylightFilterAll,
		"all-species flag should be cleared when filter is disabled")
}
