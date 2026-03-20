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
		{name: "filtering_disabled_1detection", minDetections: 1, expected: 1},
		{name: "zero_detections", minDetections: 0, expected: 0},
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

	// Invariant: visibility threshold must never exceed minDetections.
	// Otherwise detections can be approved without ever appearing in "currently hearing".
	t.Run("threshold_never_exceeds_minDetections", func(t *testing.T) {
		t.Parallel()
		for minDet := range 100 {
			threshold := CalculateVisibilityThreshold(minDet)
			assert.LessOrEqual(t, threshold, minDet,
				"threshold %d exceeds minDetections %d — detections would bypass currently hearing", threshold, minDet)
		}
	})
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

func TestSnapshotVisiblePending_UsesCreatedAtNotFirstDetected(t *testing.T) {
	t.Parallel()

	// FirstDetected is back-dated (for audio export), CreatedAt is real wall-clock time.
	// SSE output should use CreatedAt.
	backdatedTime := time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)
	realCreationTime := time.Date(2026, 3, 7, 10, 0, 13, 0, time.UTC) // 13s later

	p := &Processor{
		Settings: &conf.Settings{},
		pendingDetections: map[string]PendingDetection{
			"src1:test_species": {
				Detection: Detections{
					Result: detection.Result{
						Species: detection.Species{
							CommonName:     "Test Bird",
							ScientificName: "Testus birdus",
						},
					},
				},
				Source:        "src1",
				FirstDetected: backdatedTime,
				CreatedAt:     realCreationTime,
				Count:         5,
			},
		},
	}

	result := p.SnapshotVisiblePending(4) // threshold = max(2, 4/4) = 2, count=5 passes
	require.Len(t, result, 1)

	// The SSE firstDetected field should use CreatedAt (real time), NOT FirstDetected (back-dated)
	assert.Equal(t, realCreationTime.Unix(), result[0].FirstDetected,
		"SSE firstDetected should use CreatedAt (real wall-clock time), not the back-dated FirstDetected")
	assert.NotEqual(t, backdatedTime.Unix(), result[0].FirstDetected,
		"SSE firstDetected must NOT use the back-dated FirstDetected timestamp")
}

func TestBuildFlushNotification_UsesCreatedAt(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: &conf.Settings{},
	}

	backdatedTime := time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)
	realCreationTime := time.Date(2026, 3, 7, 10, 0, 13, 0, time.UTC)

	item := &PendingDetection{
		Detection: Detections{
			Result: detection.Result{
				Species: detection.Species{
					CommonName:     "Test Bird",
					ScientificName: "Testus birdus",
				},
			},
		},
		Source:        "src1",
		FirstDetected: backdatedTime,
		CreatedAt:     realCreationTime,
	}

	notification := p.buildFlushNotification(item, PendingStatusApproved)
	assert.Equal(t, realCreationTime.Unix(), notification.FirstDetected,
		"Flush notification should use CreatedAt for display timestamp")
}

func TestSnapshotVisiblePending_IncludesHitCount(t *testing.T) {
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
				Source:    "src1",
				CreatedAt: time.Date(2026, 3, 7, 10, 0, 13, 0, time.UTC),
				Count:     7,
			},
		},
	}

	result := p.SnapshotVisiblePending(4) // threshold = 2, count=7 passes
	require.Len(t, result, 1)
	assert.Equal(t, 7, result[0].HitCount, "HitCount should match pending detection Count")
}

func TestBuildFlushNotification_IncludesHitCount(t *testing.T) {
	t.Parallel()

	p := &Processor{Settings: &conf.Settings{}}
	item := &PendingDetection{
		Detection: Detections{
			Result: detection.Result{
				Species: detection.Species{
					CommonName:     "Test Bird",
					ScientificName: "Testus birdus",
				},
			},
		},
		Source:    "src1",
		CreatedAt: time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC),
		Count:     5,
	}

	notif := p.buildFlushNotification(item, PendingStatusApproved)
	assert.Equal(t, 5, notif.HitCount, "Flush notification should include HitCount")
}

func TestPendingSnapshotChanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prev    []SSEPendingDetection
		curr    []SSEPendingDetection
		changed bool
	}{
		{
			name:    "both_empty",
			prev:    []SSEPendingDetection{},
			curr:    []SSEPendingDetection{},
			changed: false,
		},
		{
			name: "identical_snapshots",
			prev: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 3, Status: PendingStatusActive},
			},
			curr: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 3, Status: PendingStatusActive},
			},
			changed: false,
		},
		{
			name: "new_species_added",
			prev: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 3, Status: PendingStatusActive},
			},
			curr: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 3, Status: PendingStatusActive},
				{Species: "Great Tit", SourceID: "src1", HitCount: 2, Status: PendingStatusActive},
			},
			changed: true,
		},
		{
			name: "species_removed",
			prev: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 3, Status: PendingStatusActive},
				{Species: "Great Tit", SourceID: "src1", HitCount: 2, Status: PendingStatusActive},
			},
			curr: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 3, Status: PendingStatusActive},
			},
			changed: true,
		},
		{
			name: "hit_count_increased",
			prev: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 3, Status: PendingStatusActive},
			},
			curr: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 4, Status: PendingStatusActive},
			},
			changed: true,
		},
		{
			name: "status_changed",
			prev: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 3, Status: PendingStatusActive},
			},
			curr: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 3, Status: PendingStatusApproved},
			},
			changed: true,
		},
		{
			name:    "nil_prev_empty_curr",
			prev:    nil,
			curr:    []SSEPendingDetection{},
			changed: false,
		},
		{
			name: "nil_prev_with_curr",
			prev: nil,
			curr: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 2, Status: PendingStatusActive},
			},
			changed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := pendingSnapshotChanged(tt.prev, tt.curr)
			assert.Equal(t, tt.changed, result)
		})
	}
}
