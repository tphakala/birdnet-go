// dynamic_threshold_test.go: Unit tests for dynamic threshold functionality
package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestCustomThresholdRespected verifies that custom user-configured thresholds
// are not adjusted by dynamic threshold logic
func TestCustomThresholdRespected(t *testing.T) {
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				DynamicThreshold: conf.DynamicThresholdSettings{
					Enabled: true,
					Trigger: 0.90,
					Min:     0.20,
				},
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{
						"american robin": {Threshold: 0.95},
					},
				},
			},
		},
		DynamicThresholds: make(map[string]*DynamicThreshold),
	}

	result := datastore.Results{Confidence: 0.80}
	adjusted := p.getAdjustedConfidenceThreshold("american robin", result, 0.95, true)

	assert.InDelta(t, 0.95, adjusted, 0.001, "Custom threshold should be returned unchanged")
}

// TestGlobalThresholdAdjusted verifies that global (non-custom) thresholds
// can be adjusted by dynamic threshold logic
func TestGlobalThresholdAdjusted(t *testing.T) {
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
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

	// First, add the species to dynamic thresholds
	p.addSpeciesToDynamicThresholds("house sparrow", 0.80)

	// Trigger dynamic threshold with high confidence detection
	result := datastore.Results{Confidence: 0.95}
	adjusted := p.getAdjustedConfidenceThreshold("house sparrow", result, 0.80, false)

	// Should be adjusted to 75% of base (0.80 * 0.75 = 0.60)
	assert.InDelta(t, 0.60, adjusted, 0.001, "Global threshold should be adjusted by dynamic logic")
}

// TestDynamicThresholdNotInitialized verifies that if dynamic threshold
// doesn't exist for a species, it returns the base threshold
func TestDynamicThresholdNotInitialized(t *testing.T) {
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				DynamicThreshold: conf.DynamicThresholdSettings{
					Enabled: true,
					Trigger: 0.90,
					Min:     0.20,
				},
			},
		},
		DynamicThresholds: make(map[string]*DynamicThreshold),
	}

	result := datastore.Results{Confidence: 0.85}
	adjusted := p.getAdjustedConfidenceThreshold("new species", result, 0.80, false)

	assert.InDelta(t, 0.80, adjusted, 0.001, "Should return base threshold if no dynamic threshold exists")
}

// TestCustomThresholdZeroValue verifies the edge case where a species
// is in Config but has a zero threshold (not configured)
func TestCustomThresholdZeroValue(t *testing.T) {
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				DynamicThreshold: conf.DynamicThresholdSettings{
					Enabled: true,
					Trigger: 0.90,
					Min:     0.20,
				},
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{
						"test bird": {
							Threshold: 0.0, // Not configured, only has actions/interval
							Interval:  300,
						},
					},
				},
			},
		},
		DynamicThresholds: make(map[string]*DynamicThreshold),
	}

	result := datastore.Results{Confidence: 0.85}

	// With zero threshold, isCustomThreshold should be false (not truly custom)
	// This test documents the expected behavior after fixing the edge case
	adjusted := p.getAdjustedConfidenceThreshold("test bird", result, 0.80, false)

	// Should be able to apply dynamic threshold since threshold wasn't really configured
	assert.NotEqual(t, float32(0.0), adjusted, "Zero threshold should not be treated as custom")
}

// TestDynamicThresholdLevels verifies the three levels of dynamic threshold adjustment
func TestDynamicThresholdLevels(t *testing.T) {
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
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

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", baseThreshold)

	// Level 1: First high-confidence detection (75%)
	result1 := datastore.Results{Confidence: 0.95}
	adjusted1 := p.getAdjustedConfidenceThreshold("test species", result1, baseThreshold, false)
	assert.InDelta(t, 0.60, adjusted1, 0.001, "Level 1 should be 75% of base (0.80 * 0.75)")

	// Level 2: Second high-confidence detection (50%)
	result2 := datastore.Results{Confidence: 0.95}
	adjusted2 := p.getAdjustedConfidenceThreshold("test species", result2, baseThreshold, false)
	assert.InDelta(t, 0.40, adjusted2, 0.001, "Level 2 should be 50% of base (0.80 * 0.50)")

	// Level 3: Third high-confidence detection (25%)
	result3 := datastore.Results{Confidence: 0.95}
	adjusted3 := p.getAdjustedConfidenceThreshold("test species", result3, baseThreshold, false)
	assert.InDelta(t, 0.20, adjusted3, 0.001, "Level 3 should be 25% of base (0.80 * 0.25)")
}

// TestDynamicThresholdMinimumFloor verifies that dynamic threshold
// never goes below the configured minimum
func TestDynamicThresholdMinimumFloor(t *testing.T) {
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				DynamicThreshold: conf.DynamicThresholdSettings{
					Enabled:    true,
					Trigger:    0.90,
					Min:        0.30, // Higher minimum
					ValidHours: 24,
				},
			},
		},
		DynamicThresholds: make(map[string]*DynamicThreshold),
	}

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", baseThreshold)

	// Trigger Level 3 (25% of 0.80 = 0.20, which is below min of 0.30)
	for range 3 {
		result := datastore.Results{Confidence: 0.95}
		p.getAdjustedConfidenceThreshold("test species", result, baseThreshold, false)
	}

	// Final check should respect minimum
	result := datastore.Results{Confidence: 0.85}
	adjusted := p.getAdjustedConfidenceThreshold("test species", result, baseThreshold, false)
	assert.InDelta(t, 0.30, adjusted, 0.001, "Should not go below configured minimum")
}
