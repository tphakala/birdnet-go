// last_detection_cache.go - Per-model in-memory ring of recent detections.
package processor

import (
	"github.com/tphakala/birdnet-go/internal/detection"
)

// lastDetectionRingSize is the number of most-recent above-threshold detections
// retained per model for the inference-status "Last heard" view.
const lastDetectionRingSize = 10

// LastDetection holds a snapshot of a single above-threshold detection for one
// AI model. It is intentionally lightweight: only the fields needed by the
// inference-status page are captured.
type LastDetection struct {
	// Species is the common name of the detected species.
	Species string
	// ScientificName is the scientific name of the detected species.
	ScientificName string
	// Confidence is the model's output confidence in the range [0, 1].
	Confidence float64
	// AtUnix is the Unix timestamp (seconds) of when the detection occurred.
	AtUnix int64
}

// detectionRing is a fixed-capacity circular buffer of the most recent
// above-threshold detections for a single model. head is the index where the
// next write lands; count is the number of valid entries (capped at the ring
// size). It is not goroutine-safe on its own; the Processor's lastDetectionMu
// guards every access.
type detectionRing struct {
	items [lastDetectionRingSize]LastDetection
	head  int
	count int
}

// lastDetectionCache and lastDetectionMu are added to the Processor struct in
// processor.go. They are zero-value safe: the map and per-model rings are
// lazily initialised in updateLastDetection.

// updateLastDetection records res as the most recent detection for modelID,
// appending it to that model's ring (oldest entry evicted once full). Only
// above-threshold detections reach this path (it is called post-threshold),
// so every retained entry is an above-threshold detection. Empty modelID is
// silently skipped. The write lock is held only for the ring update so the
// detection hot path is minimally impacted. res is passed by pointer to avoid
// copying the large detection.Result value.
func (p *Processor) updateLastDetection(modelID string, res *detection.Result) {
	if modelID == "" {
		return
	}
	entry := LastDetection{
		Species:        res.Species.CommonName,
		ScientificName: res.Species.ScientificName,
		Confidence:     res.Confidence,
		AtUnix:         res.Timestamp.Unix(),
	}
	p.lastDetectionMu.Lock()
	defer p.lastDetectionMu.Unlock()
	if p.lastDetectionCache == nil {
		p.lastDetectionCache = make(map[string]*detectionRing)
	}
	ring := p.lastDetectionCache[modelID]
	if ring == nil {
		ring = &detectionRing{}
		p.lastDetectionCache[modelID] = ring
	}
	ring.items[ring.head] = entry
	ring.head = (ring.head + 1) % lastDetectionRingSize
	if ring.count < lastDetectionRingSize {
		ring.count++
	}
}

// GetLastDetection returns the most recent detection for modelID and whether an
// entry exists. Callers must treat the returned value as a snapshot copy; the
// cache may be updated concurrently.
func (p *Processor) GetLastDetection(modelID string) (LastDetection, bool) {
	p.lastDetectionMu.RLock()
	defer p.lastDetectionMu.RUnlock()
	ring := p.lastDetectionCache[modelID]
	if ring == nil || ring.count == 0 {
		return LastDetection{}, false
	}
	// The most recent write is one slot behind head (head points at the next
	// write position). Wrap to the end of the array when head is at index 0.
	newest := (ring.head - 1 + lastDetectionRingSize) % lastDetectionRingSize
	return ring.items[newest], true
}

// GetRecentDetections returns a newest-first copy of the recent above-threshold
// detections for modelID, up to lastDetectionRingSize entries. The returned
// slice is freshly allocated under the read lock with values copied out, so it
// never shares backing storage with the ring; the caller may retain or mutate
// it freely while the hot path keeps writing. Returns nil when no entry exists.
func (p *Processor) GetRecentDetections(modelID string) []LastDetection {
	p.lastDetectionMu.RLock()
	defer p.lastDetectionMu.RUnlock()
	ring := p.lastDetectionCache[modelID]
	if ring == nil || ring.count == 0 {
		return nil
	}
	out := make([]LastDetection, ring.count)
	// Walk backwards from the most recent write so the result is newest-first.
	idx := (ring.head - 1 + lastDetectionRingSize) % lastDetectionRingSize
	for i := range ring.count {
		out[i] = ring.items[idx]
		idx = (idx - 1 + lastDetectionRingSize) % lastDetectionRingSize
	}
	return out
}
