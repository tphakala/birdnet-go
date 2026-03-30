// dynamic_threshold_test.go: Unit tests for dynamic threshold functionality
package processor

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// testModelID is the default model identifier used in threshold tests.
const testModelID = "BirdNET_V2.4"

// testThresholdKey builds the composite key used for direct map assertions in tests.
func testThresholdKey(species string) string {
	return dynamicThresholdKey(testModelID, species)
}

// newTestProcessor creates a Processor with default settings for dynamic threshold testing
func newTestProcessor() *Processor {
	return &Processor{
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{
				Threshold: 0.80, // Default base threshold for testing
			},
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
		pendingResets:     make(map[string]struct{}),
	}
}

// =============================================================================
// Tests for getAdjustedConfidenceThreshold (READ-ONLY function)
// =============================================================================

// TestCustomThresholdRespected verifies that custom user-configured thresholds
// are not adjusted by dynamic threshold logic
func TestCustomThresholdRespected(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.Species = conf.SpeciesSettings{
		Config: map[string]conf.SpeciesConfig{
			"american robin": {Threshold: 0.95},
		},
	}

	adjusted := p.getAdjustedConfidenceThreshold(testModelID, "american robin", 0.95, true)

	assert.InDelta(t, 0.95, adjusted, 0.001, "Custom threshold should be returned unchanged")
}

// TestDynamicThresholdNotInitialized verifies that if dynamic threshold
// doesn't exist for a species, it returns the base threshold
func TestDynamicThresholdNotInitialized(t *testing.T) {
	p := newTestProcessor()

	adjusted := p.getAdjustedConfidenceThreshold(testModelID, "new species", 0.80, false)

	assert.InDelta(t, 0.80, adjusted, 0.001, "Should return base threshold if no dynamic threshold exists")
}

// TestGetAdjustedThresholdReadsCurrentValue verifies that getAdjustedConfidenceThreshold
// returns the current threshold value without modifying it (read-only behavior)
func TestGetAdjustedThresholdReadsCurrentValue(t *testing.T) {
	p := newTestProcessor()

	key := testThresholdKey("test species")
	// Pre-set a threshold at Level 2
	p.DynamicThresholds[key] = &DynamicThreshold{
		Level:          2,
		CurrentValue:   0.40,
		Timer:          time.Now().Add(1 * time.Hour),
		HighConfCount:  2,
		ValidHours:     24,
		ScientificName: "Testus speciesus",
	}

	// Call getAdjustedConfidenceThreshold
	adjusted := p.getAdjustedConfidenceThreshold(testModelID, "test species", 0.80, false)

	// Should return current value without learning (no level change)
	assert.InDelta(t, 0.40, adjusted, 0.001, "Should return current threshold value")
	assert.Equal(t, 2, p.DynamicThresholds[key].Level, "Level should remain unchanged")
	assert.Equal(t, 2, p.DynamicThresholds[key].HighConfCount, "HighConfCount should remain unchanged")
}

// TestGetAdjustedThresholdDoesNotLearn verifies that getAdjustedConfidenceThreshold
// no longer triggers learning from high-confidence detections
func TestGetAdjustedThresholdDoesNotLearn(t *testing.T) {
	p := newTestProcessor()

	// Initialize species at Level 0
	p.addSpeciesToDynamicThresholds(testModelID, "test species", "Testus speciesus", 0.80)

	// Call getAdjustedConfidenceThreshold (no longer takes confidence)
	adjusted := p.getAdjustedConfidenceThreshold(testModelID, "test species", 0.80, false)

	key := testThresholdKey("test species")
	// Should NOT trigger learning - stays at base threshold
	assert.InDelta(t, 0.80, adjusted, 0.001, "Should return base threshold (no learning)")
	assert.Equal(t, 0, p.DynamicThresholds[key].Level, "Level should remain 0")
	assert.Equal(t, 0, p.DynamicThresholds[key].HighConfCount, "HighConfCount should remain 0")
}

// TestGetAdjustedThresholdResetsExpiredThreshold verifies that expired thresholds
// are reset to base when reading
func TestGetAdjustedThresholdResetsExpiredThreshold(t *testing.T) {
	p := newTestProcessor()

	key := testThresholdKey("test species")
	// Set up an expired threshold at Level 2
	p.DynamicThresholds[key] = &DynamicThreshold{
		Level:          2,
		CurrentValue:   0.40,
		Timer:          time.Now().Add(-1 * time.Hour), // Expired
		HighConfCount:  2,
		ValidHours:     24,
		ScientificName: "Testus speciesus",
	}

	// Call getAdjustedConfidenceThreshold
	adjusted := p.getAdjustedConfidenceThreshold(testModelID, "test species", 0.80, false)

	// Should reset to base threshold
	assert.InDelta(t, 0.80, adjusted, 0.001, "Expired threshold should reset to base")
	assert.Equal(t, 0, p.DynamicThresholds[key].Level, "Level should reset to 0")
	assert.Equal(t, 0, p.DynamicThresholds[key].HighConfCount, "HighConfCount should reset to 0")
}

// =============================================================================
// Tests for LearnFromApprovedDetection (LEARNING function)
// =============================================================================

// TestLearnFromApprovedDetectionLevels verifies the three levels of dynamic threshold
// adjustment when approved detections are spaced apart (beyond the learning cooldown)
func TestLearnFromApprovedDetectionLevels(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds(testModelID, "test species", "Testus speciesus", baseThreshold)

	key := testThresholdKey("test species")

	// Level 1: First approved high-confidence detection (75%)
	p.LearnFromApprovedDetection(testModelID, "test species", "Testus speciesus", 0.95)
	assert.Equal(t, 1, p.DynamicThresholds[key].Level, "Level should be 1 after first learning")
	assert.InDelta(t, 0.60, p.DynamicThresholds[key].CurrentValue, 0.001, "Value should be 75% of base")

	// Simulate time passing beyond the learning cooldown
	p.DynamicThresholds[key].LastLearnedAt = time.Now().Add(-15 * time.Second)

	// Level 2: Second approved high-confidence detection (50%)
	p.LearnFromApprovedDetection(testModelID, "test species", "Testus speciesus", 0.95)
	assert.Equal(t, 2, p.DynamicThresholds[key].Level, "Level should be 2 after second learning")
	assert.InDelta(t, 0.40, p.DynamicThresholds[key].CurrentValue, 0.001, "Value should be 50% of base")

	// Simulate more time passing
	p.DynamicThresholds[key].LastLearnedAt = time.Now().Add(-15 * time.Second)

	// Level 3: Third approved high-confidence detection (25%)
	p.LearnFromApprovedDetection(testModelID, "test species", "Testus speciesus", 0.95)
	assert.Equal(t, 3, p.DynamicThresholds[key].Level, "Level should be 3 after third learning")
	assert.InDelta(t, 0.20, p.DynamicThresholds[key].CurrentValue, 0.001, "Value should be 25% of base")
}

// TestLearnFromApprovedDetectionCooldown verifies that rapid approved detections
// within the same detection window don't cause multiple threshold reductions
func TestLearnFromApprovedDetectionCooldown(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds(testModelID, "test species", "Testus speciesus", baseThreshold)

	key := testThresholdKey("test species")

	// First approved detection triggers Level 1
	p.LearnFromApprovedDetection(testModelID, "test species", "Testus speciesus", 0.95)
	assert.Equal(t, 1, p.DynamicThresholds[key].Level, "First approval should trigger Level 1")
	assert.Equal(t, 1, p.DynamicThresholds[key].HighConfCount, "HighConfCount should be 1")

	// Immediate second approval should NOT trigger Level 2 (cooldown not expired)
	p.LearnFromApprovedDetection(testModelID, "test species", "Testus speciesus", 0.95)
	assert.Equal(t, 1, p.DynamicThresholds[key].Level, "Level should stay at 1 during cooldown")
	assert.Equal(t, 1, p.DynamicThresholds[key].HighConfCount, "HighConfCount should stay at 1")

	// Immediate third approval should also NOT trigger Level 3
	p.LearnFromApprovedDetection(testModelID, "test species", "Testus speciesus", 0.95)
	assert.Equal(t, 1, p.DynamicThresholds[key].Level, "Level should still be 1")
	assert.Equal(t, 1, p.DynamicThresholds[key].HighConfCount, "HighConfCount should still be 1")
}

// TestLearnFromApprovedDetectionIgnoresLowConfidence verifies that low-confidence
// approved detections do not trigger learning
func TestLearnFromApprovedDetectionIgnoresLowConfidence(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.DynamicThreshold.Trigger = 0.90

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds(testModelID, "test species", "Testus speciesus", baseThreshold)

	key := testThresholdKey("test species")

	// Low confidence (below trigger) should not learn
	p.LearnFromApprovedDetection(testModelID, "test species", "Testus speciesus", 0.85)

	assert.Equal(t, 0, p.DynamicThresholds[key].Level, "Level should remain 0 for low confidence")
	assert.Equal(t, 0, p.DynamicThresholds[key].HighConfCount, "HighConfCount should remain 0")
	assert.InDelta(t, 0.80, p.DynamicThresholds[key].CurrentValue, 0.001, "Value should remain at base")
}

// TestLearnFromApprovedDetectionIgnoresCustomThreshold verifies that species
// with custom thresholds don't trigger learning
func TestLearnFromApprovedDetectionIgnoresCustomThreshold(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.Species = conf.SpeciesSettings{
		Config: map[string]conf.SpeciesConfig{
			"american robin": {Threshold: 0.95},
		},
	}

	// Initialize threshold
	p.addSpeciesToDynamicThresholds(testModelID, "american robin", "Turdus migratorius", 0.95)

	key := testThresholdKey("american robin")

	// High confidence approval should not learn (has custom threshold)
	p.LearnFromApprovedDetection(testModelID, "american robin", "Turdus migratorius", 0.98)

	assert.Equal(t, 0, p.DynamicThresholds[key].Level, "Level should remain 0 for custom threshold")
}

// TestLearnFromApprovedDetectionMinimumFloor verifies that dynamic threshold
// never goes below the configured minimum
func TestLearnFromApprovedDetectionMinimumFloor(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.DynamicThreshold.Min = 0.30 // Higher minimum

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds(testModelID, "test species", "Testus speciesus", baseThreshold)

	key := testThresholdKey("test species")

	// Trigger Level 3 (25% of 0.80 = 0.20, which is below min of 0.30)
	for i := range 3 {
		if i > 0 {
			// Simulate time passing beyond cooldown for subsequent detections
			p.DynamicThresholds[key].LastLearnedAt = time.Now().Add(-15 * time.Second)
		}
		p.LearnFromApprovedDetection(testModelID, "test species", "Testus speciesus", 0.95)
	}

	// Should respect minimum
	assert.InDelta(t, 0.30, p.DynamicThresholds[key].CurrentValue, 0.001, "Should not go below configured minimum")
}

// TestLearnFromApprovedDetectionInitializesIfMissing verifies that the function
// can initialize a threshold entry if it doesn't exist (defensive programming)
func TestLearnFromApprovedDetectionInitializesIfMissing(t *testing.T) {
	p := newTestProcessor()

	key := testThresholdKey("new species")

	// Don't call addSpeciesToDynamicThresholds - let LearnFromApprovedDetection create it
	p.LearnFromApprovedDetection(testModelID, "new species", "Newus speciesus", 0.95)

	// Should have created the entry and learned
	assert.NotNil(t, p.DynamicThresholds[key], "Should create threshold entry")
	assert.Equal(t, 1, p.DynamicThresholds[key].Level, "Level should be 1")
	assert.Equal(t, "Newus speciesus", p.DynamicThresholds[key].ScientificName, "ScientificName should be set")
}

// TestLearnFromApprovedDetectionExtendsTimer verifies that approved high-confidence
// detections extend the threshold validity timer
func TestLearnFromApprovedDetectionExtendsTimer(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.DynamicThreshold.ValidHours = 12

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds(testModelID, "test species", "Testus speciesus", baseThreshold)

	key := testThresholdKey("test species")

	// Set timer to soon
	oldTimer := time.Now().Add(1 * time.Hour)
	p.DynamicThresholds[key].Timer = oldTimer

	// Approve a high-confidence detection
	p.LearnFromApprovedDetection(testModelID, "test species", "Testus speciesus", 0.95)

	// Timer should be extended to 12 hours from now
	newTimer := p.DynamicThresholds[key].Timer
	assert.True(t, newTimer.After(oldTimer), "Timer should be extended")
	assert.True(t, newTimer.After(time.Now().Add(11*time.Hour)), "Timer should be ~12 hours in future")
}

// TestLearnFromApprovedDetectionWhenDisabled verifies that learning doesn't happen
// when dynamic threshold is disabled
func TestLearnFromApprovedDetectionWhenDisabled(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.DynamicThreshold.Enabled = false

	p.addSpeciesToDynamicThresholds(testModelID, "test species", "Testus speciesus", 0.80)

	key := testThresholdKey("test species")

	// Should not learn when disabled
	p.LearnFromApprovedDetection(testModelID, "test species", "Testus speciesus", 0.95)

	assert.Equal(t, 0, p.DynamicThresholds[key].Level, "Should not learn when disabled")
}

// =============================================================================
// Integration tests for the complete flow
// =============================================================================

// TestDiscardedDetectionDoesNotTriggerLearning verifies the core bug fix:
// discarded detections should NOT trigger threshold learning
func TestDiscardedDetectionDoesNotTriggerLearning(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds(testModelID, "test species", "Testus speciesus", baseThreshold)

	key := testThresholdKey("test species")

	// Step 1: Get threshold (this is called during detection filtering)
	// With the fix, this should NOT trigger learning
	adjusted := p.getAdjustedConfidenceThreshold(testModelID, "test species", baseThreshold, false)

	// Threshold should still be at base level (no learning yet)
	assert.InDelta(t, 0.80, adjusted, 0.001, "Threshold should be at base (no learning during filtering)")
	assert.Equal(t, 0, p.DynamicThresholds[key].Level, "Level should be 0")

	// Step 2: Detection is discarded as false positive
	// No call to LearnFromApprovedDetection

	// Final state: threshold should still be at base level
	assert.Equal(t, 0, p.DynamicThresholds[key].Level, "Level should remain 0 after discard")
	assert.InDelta(t, 0.80, p.DynamicThresholds[key].CurrentValue, 0.001, "Value should remain at base")
}

// TestApprovedDetectionTriggersLearning verifies that approved detections
// correctly trigger threshold learning
func TestApprovedDetectionTriggersLearning(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds(testModelID, "test species", "Testus speciesus", baseThreshold)

	key := testThresholdKey("test species")

	// Step 1: Get threshold (during detection filtering)
	adjusted := p.getAdjustedConfidenceThreshold(testModelID, "test species", baseThreshold, false)
	assert.InDelta(t, 0.80, adjusted, 0.001, "Threshold at base during filtering")

	// Step 2: Detection is approved
	p.LearnFromApprovedDetection(testModelID, "test species", "Testus speciesus", 0.95)

	// Final state: threshold should now be at Level 1
	assert.Equal(t, 1, p.DynamicThresholds[key].Level, "Level should be 1 after approval")
	assert.InDelta(t, 0.60, p.DynamicThresholds[key].CurrentValue, 0.001, "Value should be 75% of base")
}

// =============================================================================
// Tests for cross-model threshold isolation
// =============================================================================

// TestDynamicThresholds_CrossModelIsolation verifies that thresholds from
// different models are independent and do not contaminate each other.
func TestDynamicThresholds_CrossModelIsolation(t *testing.T) {
	p := newTestProcessor()

	// Add threshold for BirdNET
	p.addSpeciesToDynamicThresholds("BirdNET_V2.4", "robin", "Turdus migratorius", 0.6)

	// Add threshold for Perch
	p.addSpeciesToDynamicThresholds("Perch_V2", "robin", "Turdus migratorius", 0.4)

	// Verify they're independent
	birdnetThreshold := p.getAdjustedConfidenceThreshold("BirdNET_V2.4", "robin", 0.6, false)
	perchThreshold := p.getAdjustedConfidenceThreshold("Perch_V2", "robin", 0.4, false)

	assert.InDelta(t, 0.6, float64(birdnetThreshold), 0.01, "BirdNET threshold should be independent")
	assert.InDelta(t, 0.4, float64(perchThreshold), 0.01, "Perch threshold should be independent")

	// Learn from BirdNET model only
	p.LearnFromApprovedDetection("BirdNET_V2.4", "robin", "Turdus migratorius", 0.95)

	birdnetKey := dynamicThresholdKey("BirdNET_V2.4", "robin")
	perchKey := dynamicThresholdKey("Perch_V2", "robin")

	// BirdNET should have leveled up
	assert.Equal(t, 1, p.DynamicThresholds[birdnetKey].Level, "BirdNET level should increase")

	// Perch should be unaffected
	assert.Equal(t, 0, p.DynamicThresholds[perchKey].Level, "Perch level should remain 0")
}

// TestDynamicThresholdKey verifies composite key creation and splitting
func TestDynamicThresholdKey(t *testing.T) {
	t.Run("CreatesKey", func(t *testing.T) {
		key := dynamicThresholdKey("BirdNET_V2.4", "robin")
		assert.Equal(t, "BirdNET_V2.4:robin", key)
	})

	t.Run("DefaultsEmptyModel", func(t *testing.T) {
		key := dynamicThresholdKey("", "robin")
		assert.Equal(t, "BirdNET:robin", key)
	})

	t.Run("SplitsKey", func(t *testing.T) {
		modelID, species := splitDynamicThresholdKey("BirdNET_V2.4:robin")
		assert.Equal(t, "BirdNET_V2.4", modelID)
		assert.Equal(t, "robin", species)
	})

	t.Run("SplitsKeyWithNoSeparator", func(t *testing.T) {
		modelID, species := splitDynamicThresholdKey("robin")
		assert.Equal(t, defaultModelID, modelID)
		assert.Equal(t, "robin", species)
	})

	t.Run("Roundtrip", func(t *testing.T) {
		original := dynamicThresholdKey("Perch_V2", "american robin")
		modelID, species := splitDynamicThresholdKey(original)
		assert.Equal(t, "Perch_V2", modelID)
		assert.Equal(t, "american robin", species)
	})
}

// =============================================================================
// Tests for RecalculateDynamicThresholds
// =============================================================================

// TestRecalculateDynamicThresholds verifies that changing the global base threshold
// causes all existing dynamic threshold CurrentValue entries to be recalculated
// while preserving each species' level/tier.
func TestRecalculateDynamicThresholds(t *testing.T) {
	t.Run("RecalculatesAllLevels", func(t *testing.T) {
		p := newTestProcessor()
		// Old base was 0.80, set up species at different levels
		// Use composite keys for the map
		p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level0")] = &DynamicThreshold{
			Level:          0,
			CurrentValue:   0.80, // 100% of 0.80
			Timer:          time.Now().Add(1 * time.Hour),
			HighConfCount:  0,
			ValidHours:     24,
			ScientificName: "Speciesus zerous",
		}
		p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level1")] = &DynamicThreshold{
			Level:          1,
			CurrentValue:   0.60, // 75% of 0.80
			Timer:          time.Now().Add(1 * time.Hour),
			HighConfCount:  1,
			ValidHours:     24,
			ScientificName: "Speciesus firstus",
		}
		p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level2")] = &DynamicThreshold{
			Level:          2,
			CurrentValue:   0.40, // 50% of 0.80
			Timer:          time.Now().Add(1 * time.Hour),
			HighConfCount:  2,
			ValidHours:     24,
			ScientificName: "Speciesus secondus",
		}
		p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level3")] = &DynamicThreshold{
			Level:          3,
			CurrentValue:   0.20, // 25% of 0.80
			Timer:          time.Now().Add(1 * time.Hour),
			HighConfCount:  3,
			ValidHours:     24,
			ScientificName: "Speciesus thirdus",
		}

		// Change the base threshold from 0.80 to 0.60
		p.Settings.BirdNET.Threshold = 0.60

		p.RecalculateDynamicThresholds()

		// Verify all values were recalculated with the new base
		assert.InDelta(t, 0.60, p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level0")].CurrentValue, 0.001,
			"Level 0: should be 100%% of new base 0.60")
		assert.InDelta(t, 0.45, p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level1")].CurrentValue, 0.001,
			"Level 1: should be 75%% of new base 0.60")
		assert.InDelta(t, 0.30, p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level2")].CurrentValue, 0.001,
			"Level 2: should be 50%% of new base 0.60")
		assert.InDelta(t, 0.20, p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level3")].CurrentValue, 0.001,
			"Level 3: should be clamped to min 0.20 (25%% of 0.60 = 0.15 < min)")

		// Verify levels are preserved
		assert.Equal(t, 0, p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level0")].Level)
		assert.Equal(t, 1, p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level1")].Level)
		assert.Equal(t, 2, p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level2")].Level)
		assert.Equal(t, 3, p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level3")].Level)
	})

	t.Run("RespectsMinimumThreshold", func(t *testing.T) {
		p := newTestProcessor()
		p.Settings.Realtime.DynamicThreshold.Min = 0.30

		p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level3")] = &DynamicThreshold{
			Level:          3,
			CurrentValue:   0.30, // Was clamped to min 0.30 (25% of 0.80 = 0.20 < 0.30)
			Timer:          time.Now().Add(1 * time.Hour),
			HighConfCount:  3,
			ValidHours:     24,
			ScientificName: "Speciesus thirdus",
		}

		// Lower the base threshold
		p.Settings.BirdNET.Threshold = 0.60

		p.RecalculateDynamicThresholds()

		// 25% of 0.60 = 0.15, but min is 0.30
		assert.InDelta(t, 0.30, p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level3")].CurrentValue, 0.001,
			"Should be clamped to configured minimum")
	})

	t.Run("EmptyMapIsNoOp", func(t *testing.T) {
		p := newTestProcessor()
		p.Settings.BirdNET.Threshold = 0.60

		// Should not panic or error with empty map
		p.RecalculateDynamicThresholds()

		assert.Empty(t, p.DynamicThresholds)
	})

	t.Run("NoChangeWhenBaseUnchanged", func(t *testing.T) {
		p := newTestProcessor()
		// Base is already 0.80

		p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level1")] = &DynamicThreshold{
			Level:          1,
			CurrentValue:   0.60, // 75% of 0.80 - already correct
			Timer:          time.Now().Add(1 * time.Hour),
			HighConfCount:  1,
			ValidHours:     24,
			ScientificName: "Speciesus firstus",
		}

		p.RecalculateDynamicThresholds()

		// Value should remain the same
		assert.InDelta(t, 0.60, p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level1")].CurrentValue, 0.001,
			"Should remain unchanged when base is the same")
	})

	t.Run("HigherBaseThreshold", func(t *testing.T) {
		p := newTestProcessor()

		p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level2")] = &DynamicThreshold{
			Level:          2,
			CurrentValue:   0.40, // 50% of 0.80
			Timer:          time.Now().Add(1 * time.Hour),
			HighConfCount:  2,
			ValidHours:     24,
			ScientificName: "Speciesus secondus",
		}

		// Increase the base threshold from 0.80 to 1.00
		p.Settings.BirdNET.Threshold = 1.00

		p.RecalculateDynamicThresholds()

		// 50% of 1.00 = 0.50
		assert.InDelta(t, 0.50, p.DynamicThresholds[dynamicThresholdKey(testModelID, "species_level2")].CurrentValue, 0.001,
			"Level 2: should be 50%% of new base 1.00")
	})
}

// TestLevelMultiplier verifies the level-to-multiplier mapping is correct
func TestLevelMultiplier(t *testing.T) {
	tests := []struct {
		level    int
		expected float64
	}{
		{0, 1.0},
		{1, thresholdLevel1Multiplier},
		{2, thresholdLevel2Multiplier},
		{3, thresholdLevel3Multiplier},
		{4, 1.0},  // Unknown level defaults to 1.0
		{-1, 1.0}, // Negative level defaults to 1.0
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("Level%d", tt.level), func(t *testing.T) {
			t.Parallel()
			assert.InDelta(t, tt.expected, levelMultiplier(tt.level), 0.001)
		})
	}
}
