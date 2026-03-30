// pending_key_test.go tests the composite key for pendingDetections map
// to ensure per-source and per-model isolation of pending detections.
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
	modelID := "BirdNET_V2.4"

	// Simulate detection from source A
	keyA := pendingDetectionKey("source_a", species, modelID)
	p.pendingDetections[keyA] = PendingDetection{
		Confidence:    0.85,
		Source:        "source_a",
		ModelID:       modelID,
		FirstDetected: now,
		FlushDeadline: now.Add(10 * time.Second),
		Count:         1,
	}

	// Simulate detection from source B (same species)
	keyB := pendingDetectionKey("source_b", species, modelID)
	p.pendingDetections[keyB] = PendingDetection{
		Confidence:    0.90,
		Source:        "source_b",
		ModelID:       modelID,
		FirstDetected: now.Add(1 * time.Second),
		FlushDeadline: now.Add(11 * time.Second),
		Count:         1,
	}

	// Both must exist independently
	require.Len(t, p.pendingDetections, 2)
	assert.Equal(t, "source_a", p.pendingDetections[keyA].Source)
	assert.Equal(t, "source_b", p.pendingDetections[keyB].Source)
	assert.NotEqual(t, keyA, keyB)
}

func TestPendingDetectionKey_MultiModel(t *testing.T) {
	t.Parallel()
	p := &Processor{
		Settings:          &conf.Settings{},
		pendingDetections: make(map[string]PendingDetection),
	}

	now := time.Now()
	species := testSpeciesEagleOwl
	sourceID := "source_a"

	// Simulate detection from model A
	keyA := pendingDetectionKey(sourceID, species, "BirdNET_V2.4")
	p.pendingDetections[keyA] = PendingDetection{
		Confidence:    0.85,
		Source:        sourceID,
		ModelID:       "BirdNET_V2.4",
		FirstDetected: now,
		FlushDeadline: now.Add(10 * time.Second),
		Count:         1,
	}

	// Simulate detection from model B (same species, same source)
	keyB := pendingDetectionKey(sourceID, species, "Perch_V2")
	p.pendingDetections[keyB] = PendingDetection{
		Confidence:    0.90,
		Source:        sourceID,
		ModelID:       "Perch_V2",
		FirstDetected: now.Add(1 * time.Second),
		FlushDeadline: now.Add(11 * time.Second),
		Count:         1,
	}

	// Both must exist independently — different models must not collide
	require.Len(t, p.pendingDetections, 2)
	assert.Equal(t, "BirdNET_V2.4", p.pendingDetections[keyA].ModelID)
	assert.Equal(t, "Perch_V2", p.pendingDetections[keyB].ModelID)
	assert.NotEqual(t, keyA, keyB)
}

func TestPendingDetectionKey_Format(t *testing.T) {
	t.Parallel()
	key := pendingDetectionKey("rtsp_cam1", testSpeciesEagleOwl, "BirdNET_V2.4")
	assert.Equal(t, "rtsp_cam1:eurasian eagle-owl:BirdNET_V2.4", key)
}

func TestPendingDetectionKey_RTSPUrlWithColons(t *testing.T) {
	t.Parallel()

	// RTSP URLs contain colons — verify keys are unique and species isolation works
	sourceID := "rtsp://user:pass@192.168.1.100:554/stream"
	species := testSpeciesEagleOwl
	modelID := "BirdNET_V2.4"

	key := pendingDetectionKey(sourceID, species, modelID)
	assert.Contains(t, key, species)
	assert.Contains(t, key, sourceID)
	assert.Contains(t, key, modelID)

	// Two different RTSP sources with same species must produce different keys
	key2 := pendingDetectionKey("rtsp://cam2:554/alt", species, modelID)
	assert.NotEqual(t, key, key2)
}
