package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
)

func TestCalculateVisibilityThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		minDetections int
		expected      int
	}{
		{name: "default_level3_12detections", minDetections: 12, expected: 3},
		{name: "high_filtering_21detections", minDetections: 21, expected: 5},
		{name: "low_filtering_5detections", minDetections: 5, expected: 2},
		{name: "very_low_4detections", minDetections: 4, expected: 2},
		{name: "minimal_2detections", minDetections: 2, expected: 2},
		{name: "filtering_disabled_1detection", minDetections: 1, expected: 2},
		{name: "zero_detections", minDetections: 0, expected: 2},
		{name: "medium_filtering_6detections", minDetections: 6, expected: 2},
		{name: "level4_overlap0_2detections", minDetections: 2, expected: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := CalculateVisibilityThreshold(tt.minDetections)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSnapshotVisiblePending_FiltersByThreshold(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: &conf.Settings{},
		pendingDetections: map[string]PendingDetection{
			"src1:species_a": {
				Detection: Detections{
					Result: detection.Result{
						Species: detection.Species{
							CommonName:     "Species A",
							ScientificName: "Genus speciesA",
						},
					},
				},
				Source:        "src1",
				FirstDetected: time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC),
				CreatedAt:     time.Date(2026, 3, 7, 10, 0, 13, 0, time.UTC),
				Count:         5, // Above threshold of 3 (minDetections=12)
			},
			"src1:species_b": {
				Detection: Detections{
					Result: detection.Result{
						Species: detection.Species{
							CommonName:     "Species B",
							ScientificName: "Genus speciesB",
						},
					},
				},
				Source:        "src1",
				FirstDetected: time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC),
				CreatedAt:     time.Date(2026, 3, 7, 10, 0, 13, 0, time.UTC),
				Count:         1, // Below threshold of 3
			},
			"src1:species_c": {
				Detection: Detections{
					Result: detection.Result{
						Species: detection.Species{
							CommonName:     "Species C",
							ScientificName: "Genus speciesC",
						},
					},
				},
				Source:        "src1",
				FirstDetected: time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC),
				CreatedAt:     time.Date(2026, 3, 7, 10, 0, 13, 0, time.UTC),
				Count:         3, // Exactly at threshold
			},
		},
	}

	result := p.SnapshotVisiblePending(12) // minDetections=12 → threshold=3

	// Should include Species A (count=5) and Species C (count=3), but not Species B (count=1)
	require.Len(t, result, 2)

	bySpecies := make(map[string]SSEPendingDetection)
	for _, pd := range result {
		bySpecies[pd.Species] = pd
	}

	// Species A should be visible (count=5 >= threshold=3)
	pdA, okA := bySpecies["Species A"]
	assert.True(t, okA, "Species A should be visible")
	if okA {
		assert.Equal(t, PendingStatusActive, pdA.Status)
		assert.Equal(t, "Genus speciesA", pdA.ScientificName)
		assert.NotZero(t, pdA.FirstDetected)
	}

	// Species C should be visible (count=3 >= threshold=3)
	pdC, okC := bySpecies["Species C"]
	assert.True(t, okC, "Species C should be visible")
	if okC {
		assert.Equal(t, PendingStatusActive, pdC.Status)
		assert.Equal(t, "Genus speciesC", pdC.ScientificName)
		assert.NotZero(t, pdC.FirstDetected)
	}

	// Species B should be hidden (count=1 < threshold=3)
	_, okB := bySpecies["Species B"]
	assert.False(t, okB, "Species B should be hidden (count=1 < threshold=3)")
}

func TestSnapshotVisiblePending_EmptyMap(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings:          &conf.Settings{},
		pendingDetections: make(map[string]PendingDetection),
	}

	result := p.SnapshotVisiblePending(12)
	assert.Empty(t, result)
}

func TestSnapshotVisiblePending_AllBelowThreshold(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: &conf.Settings{},
		pendingDetections: map[string]PendingDetection{
			"src1:species_a": {
				Detection: Detections{
					Result: detection.Result{
						Species: detection.Species{
							CommonName:     "Species A",
							ScientificName: "Genus speciesA",
						},
					},
				},
				Source:        "src1",
				FirstDetected: time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC),
				CreatedAt:     time.Date(2026, 3, 7, 10, 0, 13, 0, time.UTC),
				Count:         2, // Below threshold of 3
			},
		},
	}

	result := p.SnapshotVisiblePending(12) // threshold=3
	assert.Empty(t, result)
}

func TestBuildFlushNotification(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: &conf.Settings{},
	}

	item := &PendingDetection{
		Detection: Detections{
			Result: detection.Result{
				Species: detection.Species{
					CommonName:     "käpytikka",
					ScientificName: "Dendrocopos major",
				},
			},
		},
		Source:        "src1",
		FirstDetected: time.Date(2026, 3, 7, 8, 50, 0, 0, time.UTC),
		CreatedAt:     time.Date(2026, 3, 7, 8, 50, 13, 0, time.UTC),
	}

	approved := p.buildFlushNotification(item, PendingStatusApproved)
	assert.Equal(t, "käpytikka", approved.Species)
	assert.Equal(t, "Dendrocopos major", approved.ScientificName)
	assert.Equal(t, PendingStatusApproved, approved.Status)
	assert.Equal(t, item.CreatedAt.Unix(), approved.FirstDetected)

	rejected := p.buildFlushNotification(item, PendingStatusRejected)
	assert.Equal(t, PendingStatusRejected, rejected.Status)
}
