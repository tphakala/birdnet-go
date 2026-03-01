// pending_key_test.go tests the composite key for pendingDetections map
// to ensure per-source isolation of pending detections.
package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestPendingDetectionKey_MultiSource(t *testing.T) {
	p := &Processor{
		Settings:          &conf.Settings{},
		pendingDetections: make(map[string]PendingDetection),
	}

	now := time.Now()
	species := "eurasian eagle-owl"

	// Simulate detection from source A
	keyA := pendingDetectionKey("source_a", species)
	p.pendingDetections[keyA] = PendingDetection{
		Confidence:    0.85,
		Source:        "source_a",
		FirstDetected: now,
		FlushDeadline: now.Add(10 * time.Second),
		Count:         1,
	}

	// Simulate detection from source B (same species)
	keyB := pendingDetectionKey("source_b", species)
	p.pendingDetections[keyB] = PendingDetection{
		Confidence:    0.90,
		Source:        "source_b",
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

func TestPendingDetectionKey_Format(t *testing.T) {
	t.Parallel()
	key := pendingDetectionKey("rtsp_cam1", "eurasian eagle-owl")
	assert.Equal(t, "rtsp_cam1:eurasian eagle-owl", key)
}

func TestPendingDetectionKey_RTSPUrlWithColons(t *testing.T) {
	t.Parallel()

	// RTSP URLs contain colons — verify keys are unique and species isolation works
	sourceID := "rtsp://user:pass@192.168.1.100:554/stream"
	species := "eurasian eagle-owl"

	key := pendingDetectionKey(sourceID, species)
	assert.Contains(t, key, species)
	assert.Contains(t, key, sourceID)

	// Two different RTSP sources with same species must produce different keys
	key2 := pendingDetectionKey("rtsp://cam2:554/alt", species)
	assert.NotEqual(t, key, key2)
}
