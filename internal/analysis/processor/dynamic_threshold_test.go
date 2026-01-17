// dynamic_threshold_test.go: Unit tests for dynamic threshold functionality
package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// newTestProcessor creates a Processor with default settings for dynamic threshold testing
func newTestProcessor() *Processor {
	return &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{
						Length:     15, // 15 second detection window
						PreCapture: 3,
					},
				},
				DynamicThreshold: conf.DynamicThresholdSettings{
					Enabled:    true,
					Trigger:    0.90,
					Min:        0.20,
					ValidHours: 24,
				},
			},
		},
		DynamicThresholds: make(map[string]*DynamicThreshold),
	}
}

// TestCustomThresholdRespected verifies that custom user-configured thresholds
// are not adjusted by dynamic threshold logic
func TestCustomThresholdRespected(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.Species = conf.SpeciesSettings{
		Config: map[string]conf.SpeciesConfig{
			"american robin": {Threshold: 0.95},
		},
	}

	result := datastore.Results{Confidence: 0.80}
	adjusted := p.getAdjustedConfidenceThreshold("american robin", result, 0.95, true)

	assert.InDelta(t, 0.95, adjusted, 0.001, "Custom threshold should be returned unchanged")
}

// TestGlobalThresholdAdjusted verifies that global (non-custom) thresholds
// can be adjusted by dynamic threshold logic
func TestGlobalThresholdAdjusted(t *testing.T) {
	p := newTestProcessor()

	// First, add the species to dynamic thresholds
	p.addSpeciesToDynamicThresholds("house sparrow", "Passer domesticus", 0.80)

	// Trigger dynamic threshold with high confidence detection
	// First detection should trigger learning since LastLearnedAt is zero
	result := datastore.Results{Confidence: 0.95}
	adjusted := p.getAdjustedConfidenceThreshold("house sparrow", result, 0.80, false)

	// Should be adjusted to 75% of base (0.80 * 0.75 = 0.60)
	assert.InDelta(t, 0.60, adjusted, 0.001, "Global threshold should be adjusted by dynamic logic")
}

// TestDynamicThresholdNotInitialized verifies that if dynamic threshold
// doesn't exist for a species, it returns the base threshold
func TestDynamicThresholdNotInitialized(t *testing.T) {
	p := newTestProcessor()

	result := datastore.Results{Confidence: 0.85}
	adjusted := p.getAdjustedConfidenceThreshold("new species", result, 0.80, false)

	assert.InDelta(t, 0.80, adjusted, 0.001, "Should return base threshold if no dynamic threshold exists")
}

// TestCustomThresholdZeroValue verifies the edge case where a species
// is in Config but has a zero threshold (not configured)
func TestCustomThresholdZeroValue(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.Species = conf.SpeciesSettings{
		Config: map[string]conf.SpeciesConfig{
			"test bird": {
				Threshold: 0.0, // Not configured, only has actions/interval
				Interval:  300,
			},
		},
	}

	// Initialize and trigger dynamic adjustment to verify it works for zero-threshold species
	p.addSpeciesToDynamicThresholds("test bird", "Testus birdus", 0.80)
	highConfResult := datastore.Results{Confidence: 0.95}
	adjusted := p.getAdjustedConfidenceThreshold("test bird", highConfResult, 0.80, false)

	// With zero threshold, isCustomThreshold should be false (not truly custom)
	// This test documents the expected behavior after fixing the edge case:
	// Species with threshold=0 should allow dynamic adjustment (not be treated as custom)
	assert.InDelta(t, 0.60, adjusted, 0.001, "Zero threshold should allow dynamic adjustment (0.80 * 0.75)")
}

// TestDynamicThresholdLevels verifies the three levels of dynamic threshold adjustment
// when detections are spaced apart (beyond the learning cooldown)
func TestDynamicThresholdLevels(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// Level 1: First high-confidence detection (75%)
	// First detection triggers learning since LastLearnedAt is zero
	result1 := datastore.Results{Confidence: 0.95}
	adjusted1 := p.getAdjustedConfidenceThreshold("test species", result1, baseThreshold, false)
	assert.InDelta(t, 0.60, adjusted1, 0.001, "Level 1 should be 75% of base (0.80 * 0.75)")

	// Simulate time passing beyond the learning cooldown (detection window is 12s = 15-3)
	// Set LastLearnedAt to 15 seconds ago to allow next learning
	p.DynamicThresholds["test species"].LastLearnedAt = time.Now().Add(-15 * time.Second)

	// Level 2: Second high-confidence detection (50%)
	result2 := datastore.Results{Confidence: 0.95}
	adjusted2 := p.getAdjustedConfidenceThreshold("test species", result2, baseThreshold, false)
	assert.InDelta(t, 0.40, adjusted2, 0.001, "Level 2 should be 50% of base (0.80 * 0.50)")

	// Simulate more time passing
	p.DynamicThresholds["test species"].LastLearnedAt = time.Now().Add(-15 * time.Second)

	// Level 3: Third high-confidence detection (25%)
	result3 := datastore.Results{Confidence: 0.95}
	adjusted3 := p.getAdjustedConfidenceThreshold("test species", result3, baseThreshold, false)
	assert.InDelta(t, 0.20, adjusted3, 0.001, "Level 3 should be 25% of base (0.80 * 0.25)")
}

// TestDynamicThresholdCooldownPreventsRapidLearning verifies that rapid detections
// within the same detection window don't cause multiple threshold reductions
func TestDynamicThresholdCooldownPreventsRapidLearning(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// First high-confidence detection triggers Level 1
	result := datastore.Results{Confidence: 0.95}
	adjusted1 := p.getAdjustedConfidenceThreshold("test species", result, baseThreshold, false)
	assert.InDelta(t, 0.60, adjusted1, 0.001, "First detection should trigger Level 1 (0.80 * 0.75)")

	// Immediate second detection should NOT trigger Level 2 (cooldown not expired)
	adjusted2 := p.getAdjustedConfidenceThreshold("test species", result, baseThreshold, false)
	assert.InDelta(t, 0.60, adjusted2, 0.001, "Second detection within cooldown should stay at Level 1")

	// Immediate third detection should also NOT trigger Level 3
	adjusted3 := p.getAdjustedConfidenceThreshold("test species", result, baseThreshold, false)
	assert.InDelta(t, 0.60, adjusted3, 0.001, "Third detection within cooldown should stay at Level 1")

	// Verify HighConfCount is still 1
	assert.Equal(t, 1, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should be 1 despite multiple detections")
}

// TestDynamicThresholdExpiryWithHighConfidenceDetection verifies that when a threshold
// expires and a high-confidence detection arrives simultaneously, the threshold
// is reset first and then learns from a clean state (level 0 -> level 1)
func TestDynamicThresholdExpiryWithHighConfidenceDetection(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// Manually set threshold to level 2 (simulating prior learning)
	dt := p.DynamicThresholds["test species"]
	dt.Level = 2
	dt.HighConfCount = 2
	dt.CurrentValue = float64(baseThreshold * thresholdLevel2Multiplier)
	// Set timer to expired (in the past)
	dt.Timer = time.Now().Add(-1 * time.Hour)
	dt.LastLearnedAt = time.Now().Add(-2 * time.Hour)

	// High-confidence detection arrives after expiry
	result := datastore.Results{Confidence: 0.95}
	adjusted := p.getAdjustedConfidenceThreshold("test species", result, baseThreshold, false)

	// Should reset first (level 0), then learn to level 1 (75% of base)
	assert.InDelta(t, 0.60, adjusted, 0.001, "After expiry+high-conf, should reset and learn to Level 1 (0.80 * 0.75)")
	assert.Equal(t, 1, dt.Level, "Level should be 1 after reset and first learning")
	assert.Equal(t, 1, dt.HighConfCount, "HighConfCount should be 1 after reset and first learning")
}

// TestDynamicThresholdMinimumFloor verifies that dynamic threshold
// never goes below the configured minimum
func TestDynamicThresholdMinimumFloor(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.DynamicThreshold.Min = 0.30 // Higher minimum

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// Trigger Level 3 (25% of 0.80 = 0.20, which is below min of 0.30)
	// Need to simulate time passing between each detection
	for i := range 3 {
		if i > 0 {
			// Simulate time passing beyond cooldown for subsequent detections
			p.DynamicThresholds["test species"].LastLearnedAt = time.Now().Add(-15 * time.Second)
		}
		result := datastore.Results{Confidence: 0.95}
		p.getAdjustedConfidenceThreshold("test species", result, baseThreshold, false)
	}

	// Final check should respect minimum
	result := datastore.Results{Confidence: 0.85}
	adjusted := p.getAdjustedConfidenceThreshold("test species", result, baseThreshold, false)
	assert.InDelta(t, 0.30, adjusted, 0.001, "Should not go below configured minimum")
}
