package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/cadence"
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

// TestCalculateMinDetections_LevelChangeWithFixedPlan pins that a level change
// alters the count at the next read while the published cadence plan stays the
// same, and that the count follows the plan's effective overlap rather than the
// configured one.
func TestCalculateMinDetections_LevelChangeWithFixedPlan(t *testing.T) {
	conf.StoreSettings(nil)
	t.Cleanup(func() { conf.StoreSettings(nil) })

	orch := &classifier.Orchestrator{}
	orch.SetCadencePlan(&cadence.Plan{
		ConfiguredBaseOverlap: 2800 * time.Millisecond,
		EffectiveBaseOverlap:  1800 * time.Millisecond,
	})
	mk := func(level int) *conf.Settings {
		return &conf.Settings{
			Realtime: conf.RealtimeSettings{
				FalsePositiveFilter: conf.FalsePositiveFilterSettings{Level: level},
			},
			BirdNET: conf.BirdNETConfig{Overlap: 2.8},
		}
	}
	p := &Processor{Settings: mk(5), Bn: orch}

	assert.Equal(t, 4, p.calculateMinDetections(), "level 5 at effective 1.8s: step 1.2s, 70% of 5 windows")

	conf.StoreSettings(mk(2))
	assert.Equal(t, 2, p.calculateMinDetections(), "level 2 at effective 1.8s: 30% of 5 windows, plan unchanged")
	assert.Equal(t, 1800*time.Millisecond, orch.CadencePlan().EffectiveBaseOverlap)
}

// TestFlushPendingDetections_UsesEffectiveBaseOverlap pins that the flush, which
// decides whether a pending detection is kept, counts confirmations at the
// published plan's effective overlap. Level 5 needs 4 hits at the effective
// 1.8 s (a 1.2 s step) but 21 at the configured 2.8 s, so a detection with 4
// hits is flushed only when the flush follows the plan.
func TestFlushPendingDetections_UsesEffectiveBaseOverlap(t *testing.T) {
	conf.StoreSettings(nil)
	t.Cleanup(func() { conf.StoreSettings(nil) })

	orch := &classifier.Orchestrator{}
	orch.SetCadencePlan(&cadence.Plan{
		ConfiguredBaseOverlap: 2800 * time.Millisecond,
		EffectiveBaseOverlap:  1800 * time.Millisecond,
		Status:                cadence.StatusCapped,
	})
	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			FalsePositiveFilter: conf.FalsePositiveFilterSettings{Level: 5},
		},
		BirdNET: conf.BirdNETConfig{Overlap: 2.8},
	}
	p := &Processor{Settings: settings, Bn: orch, pendingDetections: make(map[string]PendingDetection)}

	now := time.Now()
	p.pendingDetections[pendingDetectionKey("src", "great tit")] = PendingDetection{
		Confidence:    0.9,
		Source:        "src",
		FirstDetected: now.Add(-10 * time.Second),
		FlushDeadline: now.Add(-time.Second),
		Count:         4,
	}

	pending, flushed := p.flushPendingDetections()
	assert.Equal(t, 1, pending)
	assert.Equal(t, 1, flushed, "4 hits meet the level 5 count at the effective overlap")
	assert.Empty(t, p.pendingDetections)
}
