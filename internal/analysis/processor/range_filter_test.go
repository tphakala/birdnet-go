package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// newRangeFilterProcessor creates a minimal Processor for range filter tests.
func newRangeFilterProcessor() *Processor {
	return &Processor{
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{
				Threshold: 0.1,
			},
		},
		DynamicThresholds: make(map[string]*DynamicThreshold),
		pendingResets:     make(map[string]struct{}),
	}
}

// rangeFilterSettings builds a conf.Settings snapshot with the given species list
// and location configuration.
func rangeFilterSettings(species []string, locationConfigured bool) *conf.Settings {
	return &conf.Settings{
		BirdNET: conf.BirdNETConfig{
			Threshold:          0.1,
			LocationConfigured: locationConfigured,
			RangeFilter: conf.RangeFilterSettings{
				Species: species,
			},
		},
	}
}

// TestRangeFilterPerchRespected verifies that Perch detections are filtered by the
// range filter when a location is configured — the bug where Perch bypassed the
// range filter entirely.
func TestRangeFilterPerchRespected(t *testing.T) {
	t.Parallel()

	// Simulate a range filter list built from BirdNET labels.
	// BirdNET label format: "Scientific Name_Common Name"
	inRangeSpecies := []string{
		"Turdus merula_Eurasian Blackbird",
		"Parus major_Great Tit",
	}

	// Perch uses scientific names only.
	perchModelID := classifier.RegistryIDPerchV2

	tests := []struct {
		name               string
		speciesLabel       string // Perch scientific name
		locationConfigured bool
		wantFiltered       bool
	}{
		{
			name:               "out-of-range Perch species filtered when location configured",
			speciesLabel:       "Turdus migratorius", // American Robin — not in range list
			locationConfigured: true,
			wantFiltered:       true,
		},
		{
			name:               "in-range Perch species passes when location configured",
			speciesLabel:       "Turdus merula", // prefix of "Turdus merula_Eurasian Blackbird"
			locationConfigured: true,
			wantFiltered:       false,
		},
		{
			name:               "Perch-exclusive species not filtered when location not configured",
			speciesLabel:       "Turdus migratorius", // not in BirdNET label set subset
			locationConfigured: false,
			wantFiltered:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newRangeFilterProcessor()
			settings := rangeFilterSettings(inRangeSpecies, tt.locationConfigured)
			result := datastore.Results{Species: tt.speciesLabel, Confidence: 0.9}

			// Use non-human species names to avoid privacy filter.
			filtered, _ := p.shouldFilterDetection(settings, result, "Some Bird", tt.speciesLabel, "some bird", 0.1, "test", perchModelID)

			assert.Equal(t, tt.wantFiltered, filtered, "species=%q locationConfigured=%v", tt.speciesLabel, tt.locationConfigured)
		})
	}
}

// TestRangeFilterBirdNETUnchanged verifies that BirdNET filtering behaviour is
// unchanged after the fix.
func TestRangeFilterBirdNETUnchanged(t *testing.T) {
	t.Parallel()

	inRangeSpecies := []string{
		"Turdus merula_Eurasian Blackbird",
	}

	birdnetModelID := detection.DefaultModelName + "_V2.4"

	tests := []struct {
		name               string
		speciesLabel       string
		locationConfigured bool
		wantFiltered       bool
	}{
		{
			name:               "out-of-range BirdNET species filtered when location configured",
			speciesLabel:       "Turdus migratorius_American Robin",
			locationConfigured: true,
			wantFiltered:       true,
		},
		{
			name:               "in-range BirdNET species passes when location configured",
			speciesLabel:       "Turdus merula_Eurasian Blackbird",
			locationConfigured: true,
			wantFiltered:       false,
		},
		{
			name:               "out-of-range BirdNET species filtered even when location not configured",
			speciesLabel:       "Turdus migratorius_American Robin",
			locationConfigured: false,
			wantFiltered:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newRangeFilterProcessor()
			settings := rangeFilterSettings(inRangeSpecies, tt.locationConfigured)
			result := datastore.Results{Species: tt.speciesLabel, Confidence: 0.9}

			filtered, _ := p.shouldFilterDetection(settings, result, "Some Bird", "Turdus sp.", "some bird", 0.1, "test", birdnetModelID)

			assert.Equal(t, tt.wantFiltered, filtered, "species=%q locationConfigured=%v", tt.speciesLabel, tt.locationConfigured)
		})
	}
}

// TestRangeFilterBatExempt verifies that Bat detections are never filtered by the
// bird range filter, since bat species are not in the bird range filter species list.
func TestRangeFilterBatExempt(t *testing.T) {
	t.Parallel()

	// Range filter list with no bat species (as in real use).
	inRangeSpecies := []string{
		"Turdus merula_Eurasian Blackbird",
	}

	batModelID := classifier.RegistryIDBat

	tests := []struct {
		name               string
		locationConfigured bool
	}{
		{name: "bat not filtered when location configured", locationConfigured: true},
		{name: "bat not filtered when location not configured", locationConfigured: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newRangeFilterProcessor()
			settings := rangeFilterSettings(inRangeSpecies, tt.locationConfigured)
			// Bat species label not in the bird range filter list.
			result := datastore.Results{Species: "Pipistrellus pipistrellus", Confidence: 0.9}

			filtered, _ := p.shouldFilterDetection(settings, result, "Common Pipistrelle", "Pipistrellus pipistrellus", "common pipistrelle", 0.1, "test", batModelID)

			assert.False(t, filtered, "bat detection should never be filtered by bird range filter")
		})
	}
}
