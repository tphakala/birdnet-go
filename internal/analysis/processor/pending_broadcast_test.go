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

// settingsForBirdMinDetections returns settings that produce the given
// minDetections value for bird models via calculateMinDetectionsFromSettings.
func settingsForBirdMinDetections(t *testing.T, wantMinDet int) *conf.Settings {
	t.Helper()
	type pair struct {
		level   int
		overlap float64
	}
	// Pre-computed (level, overlap) pairs that yield specific minDetections values.
	known := map[int]pair{
		1:  {level: 0, overlap: 0},   // level 0 always returns 1
		2:  {level: 1, overlap: 2.0}, // 6/1.0*0.20 = 1.2, ceil = 2
		4:  {level: 2, overlap: 2.5}, // 6/0.5*0.30 = 3.6, ceil = 4
		5:  {level: 3, overlap: 2.4}, // 6/0.6*0.50 = 5.0, ceil = 5
		12: {level: 4, overlap: 2.7}, // 6/0.3*0.60 = 12.0, ceil = 12
		21: {level: 5, overlap: 2.8}, // 6/0.2*0.70 = 21.0, ceil = 21
	}
	p, ok := known[wantMinDet]
	if !ok {
		t.Fatalf("no known (level, overlap) pair for minDetections=%d", wantMinDet)
	}
	s := &conf.Settings{}
	s.Realtime.FalsePositiveFilter.Level = p.level
	s.BirdNET.Overlap = p.overlap
	return s
}

func TestSnapshotVisiblePending_FiltersByThreshold(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: settingsForBirdMinDetections(t, 12),
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
				BestModelID:   "model1",
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
				BestModelID:   "model1",
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
				BestModelID:   "model1",
				FirstDetected: time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC),
				CreatedAt:     time.Date(2026, 3, 7, 10, 0, 13, 0, time.UTC),
				Count:         3, // Exactly at threshold
			},
		},
	}

	result := p.SnapshotVisiblePending() // minDetections=12 → threshold=3

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
		assert.Equal(t, "model1", pdA.BestModelID)
		assert.NotZero(t, pdA.FirstDetected)
	}

	// Species C should be visible (count=3 >= threshold=3)
	pdC, okC := bySpecies["Species C"]
	assert.True(t, okC, "Species C should be visible")
	if okC {
		assert.Equal(t, PendingStatusActive, pdC.Status)
		assert.Equal(t, "Genus speciesC", pdC.ScientificName)
		assert.Equal(t, "model1", pdC.BestModelID)
		assert.NotZero(t, pdC.FirstDetected)
	}

	// Species B should be hidden (count=1 < threshold=3)
	_, okB := bySpecies["Species B"]
	assert.False(t, okB, "Species B should be hidden (count=1 < threshold=3)")
}

func TestSnapshotVisiblePending_EmptyMap(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings:          settingsForBirdMinDetections(t, 12),
		pendingDetections: make(map[string]PendingDetection),
	}

	result := p.SnapshotVisiblePending()
	assert.Empty(t, result)
}

func TestSnapshotVisiblePending_AllBelowThreshold(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: settingsForBirdMinDetections(t, 12),
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
				BestModelID:   "model1",
				FirstDetected: time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC),
				CreatedAt:     time.Date(2026, 3, 7, 10, 0, 13, 0, time.UTC),
				Count:         2, // Below threshold of 3
			},
		},
	}

	result := p.SnapshotVisiblePending() // threshold=3
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
		BestModelID:   "birdnet-v2.4",
		FirstDetected: time.Date(2026, 3, 7, 8, 50, 0, 0, time.UTC),
		CreatedAt:     time.Date(2026, 3, 7, 8, 50, 13, 0, time.UTC),
	}

	approved := p.buildFlushNotification(item, PendingStatusApproved)
	assert.Equal(t, "käpytikka", approved.Species)
	assert.Equal(t, "Dendrocopos major", approved.ScientificName)
	assert.Equal(t, PendingStatusApproved, approved.Status)
	assert.Equal(t, "birdnet-v2.4", approved.BestModelID)
	assert.Equal(t, item.CreatedAt.Unix(), approved.FirstDetected)

	rejected := p.buildFlushNotification(item, PendingStatusRejected)
	assert.Equal(t, PendingStatusRejected, rejected.Status)
	assert.Equal(t, "birdnet-v2.4", rejected.BestModelID)
}

func TestBuildSSEModelContributions(t *testing.T) {
	t.Parallel()

	t.Run("nil_map", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, buildSSEModelContributions(nil))
	})

	t.Run("empty_map", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, buildSSEModelContributions(map[string]ModelContribution{}))
	})

	t.Run("single_model_returns_nil", func(t *testing.T) {
		t.Parallel()
		result := buildSSEModelContributions(map[string]ModelContribution{
			"BirdNET_V2.4": {HitCount: 3, MaxConfidence: 0.85},
		})
		assert.Nil(t, result)
	})

	t.Run("two_models_sorted_by_id", func(t *testing.T) {
		t.Parallel()
		result := buildSSEModelContributions(map[string]ModelContribution{
			"Perch_V2":     {HitCount: 2, MaxConfidence: 0.90},
			"BirdNET_V2.4": {HitCount: 3, MaxConfidence: 0.85},
		})
		require.Len(t, result, 2)
		assert.Equal(t, "BirdNET_V2.4", result[0].ModelID)
		assert.Equal(t, 3, result[0].HitCount)
		assert.Equal(t, "Perch_V2", result[1].ModelID)
		assert.Equal(t, 2, result[1].HitCount)
	})
}

func TestSnapshotVisiblePending_UsesCreatedAtNotFirstDetected(t *testing.T) {
	t.Parallel()

	// FirstDetected is back-dated (for audio export), CreatedAt is real wall-clock time.
	// SSE output should use CreatedAt.
	backdatedTime := time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)
	realCreationTime := time.Date(2026, 3, 7, 10, 0, 13, 0, time.UTC) // 13s later

	p := &Processor{
		Settings: settingsForBirdMinDetections(t, 4),
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

	result := p.SnapshotVisiblePending() // threshold = max(2, 4/4) = 2, count=5 passes
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
		Settings: settingsForBirdMinDetections(t, 4),
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

	result := p.SnapshotVisiblePending() // threshold = 2, count=7 passes
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
		{
			name: "best_model_id_changed",
			prev: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", BestModelID: "model-a", HitCount: 3, Status: PendingStatusActive},
			},
			curr: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", BestModelID: "model-b", HitCount: 3, Status: PendingStatusActive},
			},
			changed: true,
		},
		{
			name: "last_updated_changed",
			prev: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 3, Status: PendingStatusActive, LastUpdated: 1000},
			},
			curr: []SSEPendingDetection{
				{Species: "Blue Tit", SourceID: "src1", HitCount: 3, Status: PendingStatusActive, LastUpdated: 1006},
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

func TestSnapshotVisiblePending_IncludesLastUpdated(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)
	lastUpdated := time.Date(2026, 3, 7, 10, 0, 18, 0, time.UTC) // 18s after creation

	p := &Processor{
		Settings: settingsForBirdMinDetections(t, 4),
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
				Source:      "src1",
				CreatedAt:   createdAt,
				LastUpdated: lastUpdated,
				Count:       5,
			},
		},
	}

	result := p.SnapshotVisiblePending() // threshold=2, count=5 passes
	require.Len(t, result, 1)
	assert.Equal(t, lastUpdated.Unix(), result[0].LastUpdated,
		"SSE LastUpdated should reflect the most recent inference hit time")
}

func TestSnapshotVisiblePending_IncludesAudioCapturedAt(t *testing.T) {
	t.Parallel()

	captureTime := time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC)
	p := &Processor{
		Settings: settingsForBirdMinDetections(t, 2),
		pendingDetections: map[string]PendingDetection{
			"src1:robin": {
				Detection: Detections{
					Result: detection.Result{
						Species: detection.Species{
							CommonName:     "Robin",
							ScientificName: "Erithacus rubecula",
						},
					},
				},
				CreatedAt:       captureTime.Add(2 * time.Second),
				AudioCapturedAt: captureTime,
				LastUpdated:     captureTime.Add(1 * time.Second),
				Source:          "src1",
				Count:           5,
			},
		},
	}

	snapshot := p.SnapshotVisiblePending()
	require.Len(t, snapshot, 1)
	assert.Equal(t, captureTime.Unix(), snapshot[0].AudioCapturedAt)
	// Verify existing fields unchanged
	assert.Equal(t, captureTime.Add(2*time.Second).Unix(), snapshot[0].FirstDetected)
	assert.Equal(t, captureTime.Add(1*time.Second).Unix(), snapshot[0].LastUpdated)
}

func TestBuildFlushNotification_IncludesLastUpdated(t *testing.T) {
	t.Parallel()

	p := &Processor{Settings: &conf.Settings{}}

	createdAt := time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)
	lastUpdated := time.Date(2026, 3, 7, 10, 0, 24, 0, time.UTC)

	item := &PendingDetection{
		Detection: Detections{
			Result: detection.Result{
				Species: detection.Species{
					CommonName:     "Test Bird",
					ScientificName: "Testus birdus",
				},
			},
		},
		Source:      "src1",
		CreatedAt:   createdAt,
		LastUpdated: lastUpdated,
		Count:       8,
	}

	notif := p.buildFlushNotification(item, PendingStatusApproved)
	assert.Equal(t, lastUpdated.Unix(), notif.LastUpdated,
		"Flush notification LastUpdated should reflect latest hit time")
}
