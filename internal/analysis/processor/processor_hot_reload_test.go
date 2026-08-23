package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestCurrentSettings_FallsBackToInjected(t *testing.T) {
	conf.StoreSettings(nil)
	t.Cleanup(func() { conf.StoreSettings(nil) })

	injected := &conf.Settings{
		BirdNET: conf.BirdNETConfig{Threshold: 0.42},
	}
	p := &Processor{Settings: injected}

	got := p.currentSettings()
	assert.Same(t, injected, got, "should fall back to injected settings when no global settings published")
	assert.InDelta(t, 0.42, got.BirdNET.Threshold, 0.001)
}

func TestCurrentSettings_ReturnsGlobalWhenPublished(t *testing.T) {
	injected := &conf.Settings{
		BirdNET: conf.BirdNETConfig{Threshold: 0.42},
	}
	global := &conf.Settings{
		BirdNET: conf.BirdNETConfig{Threshold: 0.99},
	}

	conf.StoreSettings(global)
	t.Cleanup(func() { conf.StoreSettings(nil) })

	p := &Processor{Settings: injected}

	got := p.currentSettings()
	assert.Same(t, global, got, "should return global settings when published")
	assert.InDelta(t, 0.99, got.BirdNET.Threshold, 0.001)
}

// TestDynamicThreshold_AppliesLiveBaseWithoutRecalc verifies that a base-threshold
// change takes effect immediately at the next detection with no recalculation step.
// The applied value is derived at read time from the caller's live per-model base and
// the shared level, so the recalc machinery that previously rewrote stored absolute
// values is no longer needed.
func TestDynamicThreshold_AppliesLiveBaseWithoutRecalc(t *testing.T) {
	// Start with base threshold 0.80
	initial := &conf.Settings{
		BirdNET: conf.BirdNETConfig{Threshold: 0.80},
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{Length: 15, PreCapture: 3},
			},
			DynamicThreshold: conf.DynamicThresholdSettings{
				Enabled: true, Trigger: 0.90, Min: 0.10, ValidHours: 24,
			},
		},
	}
	p := &Processor{
		Settings:          initial,
		DynamicThresholds: make(map[string]*DynamicThreshold),
		pendingResets:     make(map[string]struct{}),
	}

	// Add a species at level 1. BaseThreshold is display metadata; the applied value
	// comes from the caller's live base, not this stored field.
	p.DynamicThresholds["species_a"] = &DynamicThreshold{
		Level:          1,
		BaseThreshold:  0.80,
		Timer:          time.Now().Add(1 * time.Hour),
		ScientificName: "Speciesus testus",
	}

	// Before: with the caller's base at 0.80, level 1 applies 75% -> 0.60.
	assert.InDelta(t, 0.60, p.getAdjustedConfidenceThreshold("species_a", 0.80, false), 0.01,
		"expected 75% of base 0.80 = 0.60")

	// Simulate the UI lowering the threshold to 0.40 via global settings.
	updated := &conf.Settings{
		BirdNET:  conf.BirdNETConfig{Threshold: 0.40},
		Realtime: initial.Realtime,
	}
	conf.StoreSettings(updated)
	t.Cleanup(func() { conf.StoreSettings(nil) })

	// After: the next read with the new base (0.40) applies 75% -> 0.30 immediately,
	// with no recalculation call.
	assert.InDelta(t, 0.30, p.getAdjustedConfidenceThreshold("species_a", 0.40, false), 0.01,
		"expected 75% of new base 0.40 = 0.30 with no recalc step")
}

func TestCalculateMinDetections_ReadsGlobalSettings(t *testing.T) {
	conf.StoreSettings(nil)
	t.Cleanup(func() { conf.StoreSettings(nil) })

	// Start with FP filter level 3 (strict)
	initial := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			FalsePositiveFilter: conf.FalsePositiveFilterSettings{Level: 3},
		},
		BirdNET: conf.BirdNETConfig{Overlap: 2.0},
	}
	p := &Processor{Settings: initial}

	minDetStrict := p.calculateMinDetections()

	// Change to level 0 (disabled) via global settings
	updated := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			FalsePositiveFilter: conf.FalsePositiveFilterSettings{Level: 0},
		},
		BirdNET: conf.BirdNETConfig{Overlap: 2.0},
	}
	conf.StoreSettings(updated)
	t.Cleanup(func() { conf.StoreSettings(nil) })

	minDetDisabled := p.calculateMinDetections()

	assert.Greater(t, minDetStrict, 1, "strict filter should require multiple detections")
	assert.Equal(t, 1, minDetDisabled, "disabled filter should require exactly 1 detection")
}
