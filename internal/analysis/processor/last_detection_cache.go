// last_detection_cache.go - Per-model in-memory cache of the most recent detection.
package processor

import (
	"github.com/tphakala/birdnet-go/internal/detection"
)

// LastDetection holds a snapshot of the most recently observed detection for
// a single AI model. It is intentionally lightweight: only the fields needed
// by the inference-status page are captured.
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

// lastDetectionCache and lastDetectionMu are added to the Processor struct in
// processor.go. They are zero-value safe: the map is lazily initialised here.

// updateLastDetection records res as the most recent detection for modelID.
// It always overwrites any previous entry (most-recent wins, not highest-confidence).
// Empty modelID is silently skipped. The write lock is held only for the map
// update so the detection hot path is minimally impacted.
// res is passed by pointer to avoid copying the large detection.Result value.
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
	if p.lastDetectionCache == nil {
		p.lastDetectionCache = make(map[string]LastDetection)
	}
	p.lastDetectionCache[modelID] = entry
	p.lastDetectionMu.Unlock()
}

// GetLastDetection returns the most recent detection for modelID and whether
// an entry exists. Callers must treat the returned value as a snapshot copy;
// the cache may be updated concurrently.
func (p *Processor) GetLastDetection(modelID string) (LastDetection, bool) {
	p.lastDetectionMu.RLock()
	v, ok := p.lastDetectionCache[modelID]
	p.lastDetectionMu.RUnlock()
	return v, ok
}

