// dynamic_threshold_test.go: Unit tests for dynamic threshold functionality.
//
// Dynamic thresholds are tracked PER SPECIES (not per model): the learned state is
// keyed by the lowercase common name, any model's approved high-confidence detection
// advances the shared level, and the applied threshold is computed live from the
// caller's model-specific base and the shared level.
package processor

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestGetAdjustedThresholdDerivesValue verifies that getAdjustedConfidenceThreshold
// derives the applied value from the caller's base and the stored level, without
// mutating the learned state (read-only behavior).
func TestGetAdjustedThresholdDerivesValue(t *testing.T) {
	p := newTestProcessor()

	// Pre-set a threshold at Level 2
	p.DynamicThresholds["test species"] = &DynamicThreshold{
		Level:          2,
		BaseThreshold:  0.80,
		Timer:          time.Now().Add(1 * time.Hour),
		HighConfCount:  2,
		ValidHours:     24,
		ScientificName: "Testus speciesus",
	}

	// Level 2 multiplier is 0.50, so base 0.80 -> 0.40.
	adjusted := p.getAdjustedConfidenceThreshold("test species", 0.80, false)

	assert.InDelta(t, 0.40, adjusted, 0.001, "Should derive current threshold value from base and level")
	assert.Equal(t, 2, p.DynamicThresholds["test species"].Level, "Level should remain unchanged")
	assert.Equal(t, 2, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should remain unchanged")
}

// TestGetAdjustedThresholdDoesNotLearn verifies that getAdjustedConfidenceThreshold
// no longer triggers learning from high-confidence detections
func TestGetAdjustedThresholdDoesNotLearn(t *testing.T) {
	p := newTestProcessor()

	// Initialize species at Level 0
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", 0.80)

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
		BaseThreshold:  0.80,
		Timer:          time.Now().Add(-1 * time.Hour), // Expired
		HighConfCount:  2,
		ValidHours:     24,
		ScientificName: "Testus speciesus",
	}

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

	// Level 1: First approved high-confidence detection (75% of base)
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95, baseThreshold)
	assert.Equal(t, 1, p.DynamicThresholds["test species"].Level, "Level should be 1 after first learning")
	assert.InDelta(t, 0.60, p.getAdjustedConfidenceThreshold("test species", baseThreshold, false), 0.001,
		"Applied value should be 75% of base")

	// Simulate time passing beyond the learning cooldown
	p.DynamicThresholds["test species"].LastLearnedAt = time.Now().Add(-15 * time.Second)

	// Level 2: Second approved high-confidence detection (50% of base)
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95, baseThreshold)
	assert.Equal(t, 2, p.DynamicThresholds["test species"].Level, "Level should be 2 after second learning")
	assert.InDelta(t, 0.40, p.getAdjustedConfidenceThreshold("test species", baseThreshold, false), 0.001,
		"Applied value should be 50% of base")

	// Simulate more time passing
	p.DynamicThresholds["test species"].LastLearnedAt = time.Now().Add(-15 * time.Second)

	// Level 3: Third approved high-confidence detection (25% of base)
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95, baseThreshold)
	assert.Equal(t, 3, p.DynamicThresholds["test species"].Level, "Level should be 3 after third learning")
	assert.InDelta(t, 0.20, p.getAdjustedConfidenceThreshold("test species", baseThreshold, false), 0.001,
		"Applied value should be 25% of base")
}

// TestLearnFromApprovedDetectionCooldown verifies that rapid approved detections
// within the same detection window don't cause multiple threshold reductions. The
// cooldown is model-independent, so it is also what collapses the several contributing
// models of one approval into a single level increment (each call after the first
// falls inside the cooldown); TestDynamicThresholds_SharedAcrossModels covers the
// per-species sharing itself.
func TestLearnFromApprovedDetectionCooldown(t *testing.T) {
	p := newTestProcessor()

	baseThreshold := float32(0.80)
	p.addSpeciesToDynamicThresholds("test species", "Testus speciesus", baseThreshold)

	// First approved detection triggers Level 1
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95, baseThreshold)
	assert.Equal(t, 1, p.DynamicThresholds["test species"].Level, "First approval should trigger Level 1")
	assert.Equal(t, 1, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should be 1")

	// Immediate second approval should NOT trigger Level 2 (cooldown not expired)
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95, baseThreshold)
	assert.Equal(t, 1, p.DynamicThresholds["test species"].Level, "Level should stay at 1 during cooldown")
	assert.Equal(t, 1, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should stay at 1")

	// Immediate third approval should also NOT trigger Level 3
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95, baseThreshold)
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
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.85, baseThreshold)

	assert.Equal(t, 0, p.DynamicThresholds["test species"].Level, "Level should remain 0 for low confidence")
	assert.Equal(t, 0, p.DynamicThresholds["test species"].HighConfCount, "HighConfCount should remain 0")
	assert.InDelta(t, 0.80, p.getAdjustedConfidenceThreshold("test species", baseThreshold, false), 0.001,
		"Applied value should remain at base")
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
	p.LearnFromApprovedDetection("american robin", "Turdus migratorius", 0.98, 0.95)

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
		p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95, baseThreshold)
	}

	// Should respect minimum
	assert.InDelta(t, 0.30, p.getAdjustedConfidenceThreshold("test species", baseThreshold, false), 0.001,
		"Applied value should not go below configured minimum")
}

// TestLearnFromApprovedDetectionInitializesIfMissing verifies that the function
// can initialize a threshold entry if it doesn't exist (defensive programming)
func TestLearnFromApprovedDetectionInitializesIfMissing(t *testing.T) {
	p := newTestProcessor()

	// Don't call addSpeciesToDynamicThresholds - let LearnFromApprovedDetection create it
	p.LearnFromApprovedDetection("new species", "Newus speciesus", 0.95, 0.80)

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
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95, baseThreshold)

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
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95, 0.80)

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

	// Step 2: Detection is discarded as false positive; no call to LearnFromApprovedDetection.

	// Final state: threshold should still be at base level
	assert.Equal(t, 0, p.DynamicThresholds["test species"].Level, "Level should remain 0 after discard")
	assert.InDelta(t, 0.80, p.getAdjustedConfidenceThreshold("test species", baseThreshold, false), 0.001,
		"Applied value should remain at base")
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
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.95, baseThreshold)

	// Final state: threshold should now be at Level 1
	assert.Equal(t, 1, p.DynamicThresholds["test species"].Level, "Level should be 1 after approval")
	assert.InDelta(t, 0.60, p.getAdjustedConfidenceThreshold("test species", baseThreshold, false), 0.001,
		"Applied value should be 75% of base")
}

// =============================================================================
// Tests for per-species (cross-model) sharing
// =============================================================================

// TestDynamicThresholds_SharedAcrossModels verifies the intended behavior after the
// per-model to per-species change: a species is tracked once, any model's approved
// high-confidence detection advances the shared level, and each model applies that
// shared level against its own base threshold.
func TestDynamicThresholds_SharedAcrossModels(t *testing.T) {
	p := newTestProcessor()

	const species = "robin"
	const birdnetBase = float32(0.80)
	const perchBase = float32(0.40)

	// A single detection approved by the BirdNET model.
	p.LearnFromApprovedDetection(species, "Turdus migratorius", 0.95, birdnetBase)

	// Exactly one entry exists for the species (no per-model duplication).
	assert.Len(t, p.DynamicThresholds, 1, "there should be a single per-species entry")
	dt := p.DynamicThresholds[species]
	if !assert.NotNil(t, dt, "shared entry should exist") {
		return
	}
	assert.Equal(t, 1, dt.Level, "one approval advances the shared level to 1")

	// The shared level (1 -> 0.75 multiplier) applies against each model's own base.
	assert.InDelta(t, 0.60, p.getAdjustedConfidenceThreshold(species, birdnetBase, false), 0.001,
		"BirdNET reads 75% of its base 0.80")
	assert.InDelta(t, 0.30, p.getAdjustedConfidenceThreshold(species, perchBase, false), 0.001,
		"Perch reads 75% of its base 0.40 from the same shared level")
}

// TestLearnFromApprovedDetectionRecordsBase verifies that learning records the
// model-specific base passed by the caller as display metadata.
func TestLearnFromApprovedDetectionRecordsBase(t *testing.T) {
	p := newTestProcessor()

	p.LearnFromApprovedDetection("eurasian wren", "Troglodytes troglodytes", 0.95, 0.40)

	dt := p.DynamicThresholds["eurasian wren"]
	if assert.NotNil(t, dt, "entry should be created") {
		assert.Equal(t, 1, dt.Level, "first learning should reach level 1")
		assert.InDelta(t, 0.40, dt.BaseThreshold, 0.001, "recorded base should be the caller's model base")
	}
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

// TestEffectiveDynamicThreshold verifies the derived-value helper: base times the
// level multiplier, clamped to the minimum.
func TestEffectiveDynamicThreshold(t *testing.T) {
	tests := []struct {
		name     string
		base     float64
		level    int
		min      float64
		expected float64
	}{
		{"level 0 returns base", 0.80, 0, 0.20, 0.80},
		{"level 1 is 75% of base", 0.80, 1, 0.20, 0.60},
		{"level 2 is 50% of base", 0.80, 2, 0.20, 0.40},
		{"level 3 is 25% of base", 0.80, 3, 0.20, 0.20},
		{"clamped to min", 0.80, 3, 0.30, 0.30},
		{"per-model base", 0.40, 1, 0.20, 0.30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.InDelta(t, tt.expected, effectiveDynamicThreshold(tt.base, tt.level, tt.min), 0.001)
		})
	}
}

// =============================================================================
// Regression coverage for #4194 (dynamic-threshold timer renewal)
// =============================================================================
//
// The #4194 bug lived in updateDynamicThreshold, which renewed the expiry timer
// from the PENDING detection path on any detection above the model base, letting
// sub-trigger noise (or a lower-base model's normal output on the shared
// per-species timer) pin a species at its lowered gate indefinitely. That method
// was removed; LearnFromApprovedDetection is now the only runtime path that
// renews the timer (creation-time init and DB hydration also assign it), and it
// is Trigger-gated.
//
// The test below guards that surviving renewal path: a sub-trigger detection must
// neither renew the timer nor count as a learning event on an already-latched
// species. It does not drive the removed pending path (processDetections) end to
// end, so it does not by itself reproduce the original bug; the above-Trigger
// renewal direction is covered by TestLearnFromApprovedDetectionExtendsTimer.

// TestLearnFromApprovedDetectionSubTriggerDoesNotRenewLatchedTimer verifies the
// sole runtime renewal path stays Trigger-gated: a species latched at its maximum
// reduction must not have its expiry timer renewed, nor its learning count
// advanced, by a sub-trigger detection, so the lowered gate decays once
// above-Trigger activity stops.
func TestLearnFromApprovedDetectionSubTriggerDoesNotRenewLatchedTimer(t *testing.T) {
	p := newTestProcessor()

	// Species latched at Level 3 with a timer about to expire.
	before := time.Now()
	soonToExpire := before.Add(1 * time.Hour)
	p.DynamicThresholds["test species"] = &DynamicThreshold{
		Level:          3,
		BaseThreshold:  0.80,
		Timer:          soonToExpire,
		HighConfCount:  3,
		ValidHours:     24,
		LastLearnedAt:  before.Add(-1 * time.Hour),
		ScientificName: "Testus speciesus",
	}

	// 0.85 is above base (0.80) but below Trigger (0.90): a sub-trigger detection.
	// It must not renew the timer, so the latched state is allowed to expire.
	p.LearnFromApprovedDetection("test species", "Testus speciesus", 0.85, 0.80)

	dt := p.DynamicThresholds["test species"]
	require.NotNil(t, dt, "threshold entry must still exist after a sub-trigger detection")
	// Assert the timer is unchanged (exactly soonToExpire), not merely "still soon":
	// the intended behavior is no renewal at all, so any partial or unexpected timer
	// mutation must fail. Exact equality also keeps the assertion time-independent.
	assert.Equal(t, soonToExpire, dt.Timer,
		"Sub-trigger detection must not renew the timer; it must stay exactly at soonToExpire")
	// HighConfCount is the discriminating field: Level is already saturated at 3,
	// so a spurious learn would surface as the count advancing to 4.
	assert.Equal(t, 3, dt.HighConfCount, "Sub-trigger detection must not count as a learning event")
}
