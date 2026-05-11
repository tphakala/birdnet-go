// pending_key_test.go tests the composite key for pendingDetections map
// to ensure per-source isolation and cross-model merging of pending detections.
package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

const testSpeciesEagleOwl = "eurasian eagle-owl"

func TestPendingDetectionKey_MultiSource(t *testing.T) {
	t.Parallel()
	p := &Processor{
		Settings:          &conf.Settings{},
		pendingDetections: make(map[string]PendingDetection),
	}

	now := time.Now()
	species := testSpeciesEagleOwl

	// Simulate detection from source A
	keyA := pendingDetectionKey("source_a", species)
	p.pendingDetections[keyA] = PendingDetection{
		Confidence:    0.85,
		Source:        "source_a",
		BestModelID:   "BirdNET_V2.4",
		FirstDetected: now,
		FlushDeadline: now.Add(10 * time.Second),
		Count:         1,
		ModelContributions: map[string]ModelContribution{
			"BirdNET_V2.4": {HitCount: 1, MaxConfidence: 0.85, LastHitAt: now},
		},
	}

	// Simulate detection from source B (same species)
	keyB := pendingDetectionKey("source_b", species)
	p.pendingDetections[keyB] = PendingDetection{
		Confidence:    0.90,
		Source:        "source_b",
		BestModelID:   "BirdNET_V2.4",
		FirstDetected: now.Add(1 * time.Second),
		FlushDeadline: now.Add(11 * time.Second),
		Count:         1,
		ModelContributions: map[string]ModelContribution{
			"BirdNET_V2.4": {HitCount: 1, MaxConfidence: 0.90, LastHitAt: now},
		},
	}

	// Both must exist independently
	require.Len(t, p.pendingDetections, 2)
	assert.Equal(t, "source_a", p.pendingDetections[keyA].Source)
	assert.Equal(t, "source_b", p.pendingDetections[keyB].Source)
	assert.NotEqual(t, keyA, keyB)
}

func TestPendingDetectionKey_MultiModel_MergedEntry(t *testing.T) {
	t.Parallel()
	p := &Processor{
		Settings:          &conf.Settings{},
		pendingDetections: make(map[string]PendingDetection),
	}

	now := time.Now()
	species := testSpeciesEagleOwl
	sourceID := "source_a"

	// With cross-model consensus, same source+species from different models
	// merge into a single entry
	key := pendingDetectionKey(sourceID, species)
	p.pendingDetections[key] = PendingDetection{
		Confidence:  0.90,
		Source:      sourceID,
		BestModelID: "Perch_V2",
		Count:       2,
		ModelContributions: map[string]ModelContribution{
			"BirdNET_V2.4": {HitCount: 1, MaxConfidence: 0.85, LastHitAt: now},
			"Perch_V2":     {HitCount: 1, MaxConfidence: 0.90, LastHitAt: now},
		},
	}

	require.Len(t, p.pendingDetections, 1)
	entry := p.pendingDetections[key]
	assert.Equal(t, "Perch_V2", entry.BestModelID)
	assert.Equal(t, 2, entry.Count)
	assert.Len(t, entry.ModelContributions, 2)
	assert.InDelta(t, 0.85, entry.ModelContributions["BirdNET_V2.4"].MaxConfidence, 1e-9)
	assert.InDelta(t, 0.90, entry.ModelContributions["Perch_V2"].MaxConfidence, 1e-9)
}

func TestPendingDetectionKey_Format(t *testing.T) {
	t.Parallel()
	key := pendingDetectionKey("rtsp_cam1", testSpeciesEagleOwl)
	assert.Equal(t, "rtsp_cam1:eurasian eagle-owl", key)
}

func TestPendingDetectionKey_RTSPUrlWithColons(t *testing.T) {
	t.Parallel()

	sourceID := "rtsp://user:pass@192.168.1.100:554/stream"
	species := testSpeciesEagleOwl

	key := pendingDetectionKey(sourceID, species)
	assert.Contains(t, key, species)
	assert.Contains(t, key, sourceID)

	// Two different RTSP sources with same species must produce different keys
	key2 := pendingDetectionKey("rtsp://cam2:554/alt", species)
	assert.NotEqual(t, key, key2)
}
