package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestLookupSpeciesConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		configMap      map[string]conf.SpeciesConfig
		commonName     string
		scientificName string
		wantConfig     conf.SpeciesConfig
		wantFound      bool
	}{
		{
			name: "match by lowercase common name (fast path)",
			configMap: map[string]conf.SpeciesConfig{
				"american robin": {Threshold: 0.8, Interval: 30},
			},
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			wantConfig:     conf.SpeciesConfig{Threshold: 0.8, Interval: 30},
			wantFound:      true,
		},
		{
			name: "match by scientific name (fallback)",
			configMap: map[string]conf.SpeciesConfig{
				"turdus migratorius": {Threshold: 0.75, Interval: 60},
			},
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			wantConfig:     conf.SpeciesConfig{Threshold: 0.75, Interval: 60},
			wantFound:      true,
		},
		{
			name: "scientific name case insensitive",
			configMap: map[string]conf.SpeciesConfig{
				"Turdus Migratorius": {Threshold: 0.7},
			},
			commonName:     "American Robin",
			scientificName: "turdus migratorius",
			wantConfig:     conf.SpeciesConfig{Threshold: 0.7},
			wantFound:      true,
		},
		{
			name: "no match",
			configMap: map[string]conf.SpeciesConfig{
				"house sparrow": {Threshold: 0.9},
			},
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			wantConfig:     conf.SpeciesConfig{},
			wantFound:      false,
		},
		{
			name:           "nil config map",
			configMap:      nil,
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			wantConfig:     conf.SpeciesConfig{},
			wantFound:      false,
		},
		{
			name: "empty config map",
			configMap: map[string]conf.SpeciesConfig{},
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			wantConfig:     conf.SpeciesConfig{},
			wantFound:      false,
		},
		{
			name: "empty common name still checks scientific",
			configMap: map[string]conf.SpeciesConfig{
				"turdus migratorius": {Threshold: 0.65},
			},
			commonName:     "",
			scientificName: "Turdus migratorius",
			wantConfig:     conf.SpeciesConfig{Threshold: 0.65},
			wantFound:      true,
		},
		{
			name: "empty scientific name still checks common",
			configMap: map[string]conf.SpeciesConfig{
				"american robin": {Threshold: 0.85},
			},
			commonName:     "American Robin",
			scientificName: "",
			wantConfig:     conf.SpeciesConfig{Threshold: 0.85},
			wantFound:      true,
		},
		{
			name: "both names empty returns not found",
			configMap: map[string]conf.SpeciesConfig{
				"american robin": {Threshold: 0.8},
			},
			commonName:     "",
			scientificName: "",
			wantConfig:     conf.SpeciesConfig{},
			wantFound:      false,
		},
		{
			name: "common name takes precedence over scientific name",
			configMap: map[string]conf.SpeciesConfig{
				"american robin":     {Threshold: 0.8, Interval: 30},
				"turdus migratorius": {Threshold: 0.75, Interval: 60},
			},
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			wantConfig:     conf.SpeciesConfig{Threshold: 0.8, Interval: 30}, // common name wins (fast path)
			wantFound:      true,
		},
		{
			name: "config with actions",
			configMap: map[string]conf.SpeciesConfig{
				"american robin": {
					Threshold: 0.8,
					Interval:  30,
					Actions: []conf.SpeciesAction{
						{Type: "ExecuteCommand", Command: "/usr/local/bin/notify.sh"},
					},
				},
			},
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			wantConfig: conf.SpeciesConfig{
				Threshold: 0.8,
				Interval:  30,
				Actions: []conf.SpeciesAction{
					{Type: "ExecuteCommand", Command: "/usr/local/bin/notify.sh"},
				},
			},
			wantFound: true,
		},
		{
			name: "unicode species name",
			configMap: map[string]conf.SpeciesConfig{
				"pájaro azul": {Threshold: 0.6},
			},
			commonName:     "Pájaro Azul",
			scientificName: "Sialia sialis",
			wantConfig:     conf.SpeciesConfig{Threshold: 0.6},
			wantFound:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config, found := lookupSpeciesConfig(tt.configMap, tt.commonName, tt.scientificName)

			assert.Equal(t, tt.wantFound, found, "found mismatch")
			assert.Equal(t, tt.wantConfig, config, "config mismatch")
		})
	}
}

func TestLookupSpeciesConfig_MultipleSpecies(t *testing.T) {
	t.Parallel()

	configMap := map[string]conf.SpeciesConfig{
		"american robin":      {Threshold: 0.8},
		"house sparrow":       {Threshold: 0.9},
		"turdus migratorius":  {Threshold: 0.75}, // Scientific name of American Robin
		"passer domesticus":   {Threshold: 0.85}, // Scientific name of House Sparrow
		"european blackbird":  {Threshold: 0.7},
	}

	// Test that we can look up by common name
	config, found := lookupSpeciesConfig(configMap, "House Sparrow", "Passer domesticus")
	assert.True(t, found)
	assert.InDelta(t, 0.9, config.Threshold, 0.0001)

	// Test that we can look up by scientific name when common name not in config
	config, found = lookupSpeciesConfig(configMap, "Unknown Bird", "Turdus migratorius")
	assert.True(t, found)
	assert.InDelta(t, 0.75, config.Threshold, 0.0001)

	// Test that unknown species returns not found
	config, found = lookupSpeciesConfig(configMap, "Blue Jay", "Cyanocitta cristata")
	assert.False(t, found)
	assert.Equal(t, conf.SpeciesConfig{}, config)
}

func TestTrackEventWithNames_TrackingKeyFallback(t *testing.T) {
	t.Parallel()

	// Create tracker with species config keyed by scientific name
	speciesConfigs := map[string]conf.SpeciesConfig{
		"turdus migratorius": {Interval: 120}, // Scientific name
	}
	tracker := NewEventTrackerWithConfig(60*time.Second, speciesConfigs)

	// Test 1: Empty common name falls back to scientific name for tracking key
	// First call should allow the event
	allowed := tracker.TrackEventWithNames("", "Turdus migratorius", LogToFile)
	assert.True(t, allowed, "first event should be allowed")

	// Second immediate call should be blocked (same tracking key)
	allowed = tracker.TrackEventWithNames("", "Turdus migratorius", LogToFile)
	assert.False(t, allowed, "second event should be blocked due to rate limiting")

	// Test 2: Both names empty should allow the event (can't rate-limit)
	allowed = tracker.TrackEventWithNames("", "", LogToFile)
	assert.True(t, allowed, "event with both names empty should be allowed")

	// Call again - should still allow since there's no tracking key
	allowed = tracker.TrackEventWithNames("", "", LogToFile)
	assert.True(t, allowed, "event with both names empty should always be allowed")
}

func TestTrackEventWithNames_ScientificNameConfig(t *testing.T) {
	t.Parallel()

	// Create tracker with species config keyed by scientific name
	// Note: SpeciesConfig.Interval is in seconds, so 1s is the minimum configurable.
	// This test verifies that lookupSpeciesConfig finds the config by scientific name
	// and applies the custom interval for rate-limiting.
	speciesConfigs := map[string]conf.SpeciesConfig{
		"turdus migratorius": {Interval: 1}, // 1 second (minimum configurable)
	}
	tracker := NewEventTrackerWithConfig(60*time.Second, speciesConfigs)

	// Event with common name not in config but scientific name is - verifies lookup works
	allowed := tracker.TrackEventWithNames("American Robin", "Turdus migratorius", DatabaseSave)
	assert.True(t, allowed, "first event should be allowed")

	// Immediate second call should be blocked - verifies custom interval was found
	allowed = tracker.TrackEventWithNames("American Robin", "Turdus migratorius", DatabaseSave)
	assert.False(t, allowed, "second event should be blocked (custom 1s interval found via scientific name)")

	// Wait for interval to expire (1s + 100ms buffer for CI reliability)
	time.Sleep(1100 * time.Millisecond)

	// Should be allowed again after interval expiry
	allowed = tracker.TrackEventWithNames("American Robin", "Turdus migratorius", DatabaseSave)
	assert.True(t, allowed, "event after interval should be allowed")
}
