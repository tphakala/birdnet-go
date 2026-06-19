// last_detection_cache.go - Per-model in-memory feed of recently heard species.
package processor

import (
	"math"
	"time"
)

// lastDetectionCap is the number of most-recent detections retained per model for
// the inference-status "Last heard" feed. The frontend shows them as two columns
// of ten.
const lastDetectionCap = 20

// detectionThrottleTarget is the spacing the Last-heard feed aims for between
// repeat entries of the same species. The actual per-model interval snaps this to
// a whole number of the model's analysis segments (clip length), so feed entries
// line up with segment boundaries (e.g. 9s for 3s BirdNET segments, 10s for 5s
// Perch segments). Throttling keeps a continuously singing bird from flooding the
// feed while still showing its detection cadence over time.
const detectionThrottleTarget = 9 * time.Second

// LastDetection holds a snapshot of one above-threshold prediction for one AI
// model. The feed records every prediction above the base confidence threshold
// (including non-avian, human, and out-of-range species), not only the ones that
// pass every filter and get saved, so it shows what the model is actually firing
// on. It is intentionally lightweight: only the fields the inference-status "Last
// heard" feed needs are captured.
type LastDetection struct {
	// Species is the common name of the detected species.
	Species string
	// ScientificName is the scientific name of the detected species.
	ScientificName string
	// Confidence is the model's output confidence in the range [0, 1].
	Confidence float64
	// AtUnix is the Unix timestamp (seconds) of when the detection occurred.
	AtUnix int64
	// InRange reports whether the species passes the range filter. It is true when
	// the species is in range, or when the range filter is not active (e.g. no
	// location configured). When the range filter is active it is false for
	// out-of-range birds and for non-avian and human classes; those are shown for
	// diagnostics but are not saved as real detections.
	InRange bool
}

// recentDetectionList is a small, most-recent-first feed of a single model's
// recent detections, capped at lastDetectionCap entries. A species is recorded at
// most once per throttle interval so a continuously singing bird does not flood
// the feed (see observe). It is not goroutine-safe on its own; the Processor's
// lastDetectionMu guards every access.
type recentDetectionList struct {
	// items is ordered most-recent-first; its length never exceeds lastDetectionCap.
	items []LastDetection
}

// detectionThrottle returns the minimum spacing between recorded detections of
// the same species for a model whose analysis segment (clip) length is clip. It
// is clip scaled to the whole multiple closest to detectionThrottleTarget, with a
// floor of one segment; an unknown clip length (<= 0) falls back to the target.
func detectionThrottle(clip time.Duration) time.Duration {
	if clip <= 0 {
		return detectionThrottleTarget
	}
	n := max(int64(math.Round(float64(detectionThrottleTarget)/float64(clip))), 1)
	return time.Duration(n) * clip
}

// speciesKey returns the stable identity used to rate-limit repeated detections
// of the same species. ScientificName is preferred because it is
// locale-independent; CommonName is the fallback when the scientific name is
// missing. It is empty for an unidentified detection.
func speciesKey(scientific, common string) string {
	if scientific != "" {
		return scientific
	}
	return common
}

// observe prepends one detection to the front of the feed, unless the same
// species was already recorded within throttleSec seconds (in which case it is
// dropped, keeping a continuously singing bird from flooding the feed). The
// oldest entry is evicted once the cap is reached. The scan and shift are
// O(lastDetectionCap), far cheaper than the surrounding lock acquisition.
func (l *recentDetectionList) observe(common, scientific string, confidence float64, atUnix int64, inRange bool, throttleSec int64) {
	key := speciesKey(scientific, common)
	// Throttle: scan newest-first for this species' most recent feed entry. If it
	// is within throttleSec, drop this detection; otherwise record it.
	for i := range l.items {
		if speciesKey(l.items[i].ScientificName, l.items[i].Species) != key {
			continue
		}
		// Compare the absolute gap so a backward clock jump (NTP correction or
		// out-of-order processing, where atUnix can be earlier than the stored
		// entry) does not silently drop detections until the clock catches up.
		gap := atUnix - l.items[i].AtUnix
		if gap < 0 {
			gap = -gap
		}
		if gap < throttleSec {
			return
		}
		break
	}

	entry := LastDetection{
		Species:        common,
		ScientificName: scientific,
		Confidence:     confidence,
		AtUnix:         atUnix,
		InRange:        inRange,
	}
	// Prepend the new entry, shifting existing entries down one slot and dropping
	// the oldest once the cap is reached. Grow the slice by one slot only while
	// under the cap; at the cap the last (oldest) entry is overwritten by the
	// shift. copy handles the overlapping move correctly.
	if len(l.items) < lastDetectionCap {
		l.items = append(l.items, LastDetection{})
	}
	copy(l.items[1:], l.items[:len(l.items)-1])
	l.items[0] = entry
}

// lastDetectionCache and lastDetectionMu are added to the Processor struct in
// processor.go. They are zero-value safe: the map and per-model feeds are lazily
// initialised in updateLastDetection.

// updateLastDetection records one above-threshold prediction for modelID in the
// "Last heard" feed, subject to the per-species throttle (see
// recentDetectionList.observe). The caller records every prediction above the base
// confidence threshold (including non-avian, human, and out-of-range species), so
// inRange marks whether the species passes the range filter. Empty modelID is
// silently skipped. throttle is the minimum spacing between recorded detections of
// the same species (derived from the model's clip length by the caller). The write
// lock is held only for the feed update so the detection hot path is minimally
// impacted.
func (p *Processor) updateLastDetection(modelID, commonName, scientificName string, confidence float64, at time.Time, inRange bool, throttle time.Duration) {
	if modelID == "" {
		return
	}
	throttleSec := int64(throttle / time.Second)
	p.lastDetectionMu.Lock()
	defer p.lastDetectionMu.Unlock()
	if p.lastDetectionCache == nil {
		p.lastDetectionCache = make(map[string]*recentDetectionList)
	}
	list := p.lastDetectionCache[modelID]
	if list == nil {
		list = &recentDetectionList{}
		p.lastDetectionCache[modelID] = list
	}
	list.observe(commonName, scientificName, confidence, at.Unix(), inRange, throttleSec)
}

// GetLastDetection returns the most recent detection for modelID and whether an
// entry exists. Callers must treat the returned value as a snapshot copy; the
// cache may be updated concurrently.
func (p *Processor) GetLastDetection(modelID string) (LastDetection, bool) {
	p.lastDetectionMu.RLock()
	defer p.lastDetectionMu.RUnlock()
	list := p.lastDetectionCache[modelID]
	if list == nil || len(list.items) == 0 {
		return LastDetection{}, false
	}
	return list.items[0], true
}

// GetRecentDetections returns a newest-first copy of the recent detections for
// modelID, up to lastDetectionCap entries. The returned slice is freshly
// allocated under the read lock with values copied out, so it never shares
// backing storage with the cache; the caller may retain or mutate it freely while
// the hot path keeps writing. Returns nil when no entry exists.
func (p *Processor) GetRecentDetections(modelID string) []LastDetection {
	p.lastDetectionMu.RLock()
	defer p.lastDetectionMu.RUnlock()
	list := p.lastDetectionCache[modelID]
	if list == nil || len(list.items) == 0 {
		return nil
	}
	out := make([]LastDetection, len(list.items))
	copy(out, list.items)
	return out
}
