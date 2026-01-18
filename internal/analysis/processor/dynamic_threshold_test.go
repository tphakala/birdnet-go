// dynamic_threshold_test.go: Unit tests for dynamic threshold functionality
package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

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

	adjusted := p.getAdjustedConfidenceThreshold("american robin", 0.95, true)

	assert.InDelta(t, 0.95, adjusted, 0.001, "Custom threshold should be returned unchanged")
}

// TestDynamicThresholdNotInitialized verifies that if dynamic threshold
// doesn't exist for a species, it returns the base threshold
func TestDynamicThresholdNotInitialized(t *testing.T) {
	p := newTestProcessor()

	adjusted := p.getAdjustedConfidenceThreshold("new species", 0.80, false)

	assert.InDelta(t, 0.80, adjusted, 0.001, "Should return base threshold if no dynamic threshold exists")
}

// TestGetAdjustedThresholdReadsCurrentValue verifies that getAdjustedConfidenceThreshold
// returns the current threshold value without modifying it (read-only behavior)
func TestGetAdjustedThresholdReadsCurrentValue(t *testing.T) {
	p := newTestProcessor()

	// Pre-set a threshold at Level 2
	p.DynamicThresholds["test species"] = &DynamicThreshold{
		Level:          2,
		CurrentValue:   0.40,
		Timer:          time.Now().Add(1 * time.Hour),
		HighConfCount:  2,
		ValidHours:     24,
		ScientificName: "Testus speciesus",
	}

	// Call getAdjustedConfidenceThreshold
	adjusted := p.getAdjustedConfidenceThreshold("test species", 0.80, false)

	// Should return current value without learning (no level change)
	assert.InDelta(t, 0.40, adjusted, 0.001, "Should return current threshold value")
	assert.Equal(t, 2, p.DynamicThresholds["test species"].Level, "Level should remain unchanged")
	assert.Equal(t, 2, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should remain unchanged")
}

// TestGetAdjustedThresholdDoesNotLearn verifies that getAdjustedConfidenceThreshold
// no longer triggers learning from high-confidence detections
func TestGetAdjustedThresholdDoesNotLearn(t *testing.T) {
	p := newTestProcessor()

	// Initialize species at Level 0
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", 0.80)

	// Call getAdjustedConfidenceThreshold (no longer takes confidence)
	adjusted := p.getAdjustedConfidenceThreshold("test species", 0.80, false)

	// Should NOT trigger learning - stays at base threshold
	assert.InDelta(t, 0.80, adjusted, 0.001, "Should return base threshold (no learning)")
	assert.Equal(t, 0, p.DynamicThresholds["test species"].Level, "Level should remain 0")
	assert.Equal(t, 0, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should remain 0")
}

// TestGetAdjustedThresholdResetsExpiredThreshold verifies that expired thresholds
// are reset to base when reading
func TestGetAdjustedThresholdResetsExpiredThreshold(t *testing.T) {
	p := newTestProcessor()

	// Set up an expired threshold at Level 2
	p.DynamicThresholds["test species"] = &DynamicThreshold{
		Level:          2,
		CurrentValue:   0.40,
		Timer:          time.Now().Add(-1 * time.Hour), // Expired
		HighConfCount:  2,
		ValidHours:     24,
		ScientificName: "Testus speciesus",
	}

	// Call getAdjustedConfidenceThreshold
	adjusted := p.getAdjustedConfidenceThreshold("test species", 0.80, false)

	// Should reset to base threshold
	assert.InDelta(t, 0.80, adjusted, 0.001, "Expired threshold should reset to base")
	assert.Equal(t, 0, p.DynamicThresholds["test species"].Level, "Level should reset to 0")
	assert.Equal(t, 0, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should reset to 0")
}

// =============================================================================
// Tests for LearnFromApprovedDetection (LEARNING function)
// =============================================================================

// TestLearnFromApprovedDetectionLevels verifies the three levels of dynamic threshold
// adjustment when approved detections are spaced apart (beyond the learning cooldown)
func TestLearnFromApprovedDetectionLevels(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// Level 1: First approved high-confidence detection (75%)
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95)
	assert.Equal(t, 1, p.DynamicThresholds["test species"].Level, "Level should be 1 after first learning")
	assert.InDelta(t, 0.60, p.DynamicThresholds["test species"].CurrentValue, 0.001, "Value should be 75% of base")

	// Simulate time passing beyond the learning cooldown
	p.DynamicThresholds["test species"].LastLearnedAt = time.Now().Add(-15 * time.Second)

	// Level 2: Second approved high-confidence detection (50%)
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95)
	assert.Equal(t, 2, p.DynamicThresholds["test species"].Level, "Level should be 2 after second learning")
	assert.InDelta(t, 0.40, p.DynamicThresholds["test species"].CurrentValue, 0.001, "Value should be 50% of base")

	// Simulate more time passing
	p.DynamicThresholds["test species"].LastLearnedAt = time.Now().Add(-15 * time.Second)

	// Level 3: Third approved high-confidence detection (25%)
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95)
	assert.Equal(t, 3, p.DynamicThresholds["test species"].Level, "Level should be 3 after third learning")
	assert.InDelta(t, 0.20, p.DynamicThresholds["test species"].CurrentValue, 0.001, "Value should be 25% of base")
}

// TestLearnFromApprovedDetectionCooldown verifies that rapid approved detections
// within the same detection window don't cause multiple threshold reductions
func TestLearnFromApprovedDetectionCooldown(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// First approved detection triggers Level 1
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95)
	assert.Equal(t, 1, p.DynamicThresholds["test species"].Level, "First approval should trigger Level 1")
	assert.Equal(t, 1, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should be 1")

	// Immediate second approval should NOT trigger Level 2 (cooldown not expired)
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95)
	assert.Equal(t, 1, p.DynamicThresholds["test species"].Level, "Level should stay at 1 during cooldown")
	assert.Equal(t, 1, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should stay at 1")

	// Immediate third approval should also NOT trigger Level 3
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95)
	assert.Equal(t, 1, p.DynamicThresholds["test species"].Level, "Level should still be 1")
	assert.Equal(t, 1, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should still be 1")
}

// TestLearnFromApprovedDetectionIgnoresLowConfidence verifies that low-confidence
// approved detections do not trigger learning
func TestLearnFromApprovedDetectionIgnoresLowConfidence(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.DynamicThreshold.Trigger = 0.90

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// Low confidence (below trigger) should not learn
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.85)

	assert.Equal(t, 0, p.DynamicThresholds["test species"].Level, "Level should remain 0 for low confidence")
	assert.Equal(t, 0, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should remain 0")
	assert.InDelta(t, 0.80, p.DynamicThresholds["test species"].CurrentValue, 0.001, "Value should remain at base")
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
	p.addSpeciesToDynamicThresholds("american robin", "Turdus migratorius", 0.95)

	// High confidence approval should not learn (has custom threshold)
	p.LearnFromApprovedDetection("american robin", "Turdus migratorius", 0.98)

	assert.Equal(t, 0, p.DynamicThresholds["american robin"].Level, "Level should remain 0 for custom threshold")
}

// TestLearnFromApprovedDetectionMinimumFloor verifies that dynamic threshold
// never goes below the configured minimum
func TestLearnFromApprovedDetectionMinimumFloor(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.DynamicThreshold.Min = 0.30 // Higher minimum

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// Trigger Level 3 (25% of 0.80 = 0.20, which is below min of 0.30)
	for i := range 3 {
		if i > 0 {
			// Simulate time passing beyond cooldown for subsequent detections
			p.DynamicThresholds["test species"].LastLearnedAt = time.Now().Add(-15 * time.Second)
		}
		p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95)
	}

	// Should respect minimum
	assert.InDelta(t, 0.30, p.DynamicThresholds["test species"].CurrentValue, 0.001, "Should not go below configured minimum")
}

// TestLearnFromApprovedDetectionInitializesIfMissing verifies that the function
// can initialize a threshold entry if it doesn't exist (defensive programming)
func TestLearnFromApprovedDetectionInitializesIfMissing(t *testing.T) {
	p := newTestProcessor()

	// Don't call addSpeciesToDynamicThresholds - let LearnFromApprovedDetection create it
	p.LearnFromApprovedDetection("new species", "Newus speciesus", 0.95)

	// Should have created the entry and learned
	assert.NotNil(t, p.DynamicThresholds["new species"], "Should create threshold entry")
	assert.Equal(t, 1, p.DynamicThresholds["new species"].Level, "Level should be 1")
	assert.Equal(t, "Newus speciesus", p.DynamicThresholds["new species"].ScientificName, "ScientificName should be set")
}

// TestLearnFromApprovedDetectionExtendsTimer verifies that approved high-confidence
// detections extend the threshold validity timer
func TestLearnFromApprovedDetectionExtendsTimer(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.DynamicThreshold.ValidHours = 12

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// Set timer to soon
	oldTimer := time.Now().Add(1 * time.Hour)
	p.DynamicThresholds["test species"].Timer = oldTimer

	// Approve a high-confidence detection
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95)

	// Timer should be extended to 12 hours from now
	newTimer := p.DynamicThresholds["test species"].Timer
	assert.True(t, newTimer.After(oldTimer), "Timer should be extended")
	assert.True(t, newTimer.After(time.Now().Add(11*time.Hour)), "Timer should be ~12 hours in future")
}

// TestLearnFromApprovedDetectionWhenDisabled verifies that learning doesn't happen
// when dynamic threshold is disabled
func TestLearnFromApprovedDetectionWhenDisabled(t *testing.T) {
	p := newTestProcessor()
	p.Settings.Realtime.DynamicThreshold.Enabled = false

	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", 0.80)

	// Should not learn when disabled
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95)

	assert.Equal(t, 0, p.DynamicThresholds["test species"].Level, "Should not learn when disabled")
}

// =============================================================================
// Integration tests for the complete flow
// =============================================================================

// TestDiscardedDetectionDoesNotTriggerLearning verifies the core bug fix:
// discarded detections should NOT trigger threshold learning
func TestDiscardedDetectionDoesNotTriggerLearning(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// Step 1: Get threshold (this is called during detection filtering)
	// With the fix, this should NOT trigger learning
	adjusted := p.getAdjustedConfidenceThreshold("test species", baseThreshold, false)

	// Threshold should still be at base level (no learning yet)
	assert.InDelta(t, 0.80, adjusted, 0.001, "Threshold should be at base (no learning during filtering)")
	assert.Equal(t, 0, p.DynamicThresholds["test species"].Level, "Level should be 0")

	// Step 2: Detection is discarded as false positive
	// No call to LearnFromApprovedDetection

	// Final state: threshold should still be at base level
	assert.Equal(t, 0, p.DynamicThresholds["test species"].Level, "Level should remain 0 after discard")
	assert.InDelta(t, 0.80, p.DynamicThresholds["test species"].CurrentValue, 0.001, "Value should remain at base")
}

// TestApprovedDetectionTriggersLearning verifies that approved detections
// correctly trigger threshold learning
func TestApprovedDetectionTriggersLearning(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// Step 1: Get threshold (during detection filtering)
	adjusted := p.getAdjustedConfidenceThreshold("test species", baseThreshold, false)
	assert.InDelta(t, 0.80, adjusted, 0.001, "Threshold at base during filtering")

	// Step 2: Detection is approved
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95)

	// Final state: threshold should now be at Level 1
	assert.Equal(t, 1, p.DynamicThresholds["test species"].Level, "Level should be 1 after approval")
	assert.InDelta(t, 0.60, p.DynamicThresholds["test species"].CurrentValue, 0.001, "Value should be 75% of base")
}
