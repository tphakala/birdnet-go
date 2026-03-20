package processor

import (
	"slices"

	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Pending broadcast constants.
const (
	// pendingBroadcastBufferSize is the channel buffer for pending detection broadcasts.
	pendingBroadcastBufferSize = 10
)

// PendingDetectionStatus represents the lifecycle state of a pending detection.
type PendingDetectionStatus string

const (
	PendingStatusActive   PendingDetectionStatus = "active"
	PendingStatusApproved PendingDetectionStatus = "approved"
	PendingStatusRejected PendingDetectionStatus = "rejected"
)

// SSEPendingDetection is the lightweight DTO sent over SSE for pending detections.
// It contains only the fields needed for the "currently hearing" dashboard card.
type SSEPendingDetection struct {
	Species        string                 `json:"species"`        // Common name
	ScientificName string                 `json:"scientificName"` // Scientific name
	Thumbnail      string                 `json:"thumbnail"`      // Bird image URL
	Status         PendingDetectionStatus `json:"status"`         // "active", "approved", "rejected"
	FirstDetected  int64                  `json:"firstDetected"`  // Unix timestamp (seconds)
	Source         string                 `json:"source"`         // Source display name
	SourceID       string                 `json:"sourceID"`       // Raw source ID for client-side filtering
	HitCount       int                    `json:"hitCount"`       // Number of inference hits accumulated
}

// sortPendingSnapshot sorts a pending detection snapshot by FirstDetected
// (oldest first), with species name and source ID as tie-breakers for determinism.
// This ordering is required by pendingSnapshotChanged which does index-based comparison.
func sortPendingSnapshot(s []SSEPendingDetection) {
	slices.SortFunc(s, func(a, b SSEPendingDetection) int {
		if a.FirstDetected != b.FirstDetected {
			if a.FirstDetected < b.FirstDetected {
				return -1
			}
			return 1
		}
		if a.Species != b.Species {
			if a.Species < b.Species {
				return -1
			}
			return 1
		}
		if a.SourceID < b.SourceID {
			return -1
		}
		if a.SourceID > b.SourceID {
			return 1
		}
		return 0
	})
}

// CalculateVisibilityThreshold computes the minimum hit count for a pending
// detection to be visible in the "currently hearing" card.
// It returns 25% of minDetections, floored at 2, but never exceeds minDetections.
// Without the cap, detections could be approved (flushed) before ever becoming
// visible — e.g. when minDetections=1 (level 0, no filtering) the old floor of 2
// meant single-hit detections bypassed "currently hearing" entirely.
func CalculateVisibilityThreshold(minDetections int) int {
	threshold := minDetections / 4
	threshold = max(2, threshold)
	// Never exceed minDetections: approved detections must always be visible first.
	return min(threshold, minDetections)
}

// SnapshotVisiblePending returns all pending detections that have accumulated
// enough hits to pass the visibility threshold. Results have status "active".
// The caller must NOT hold pendingMutex.
func (p *Processor) SnapshotVisiblePending(minDetections int) []SSEPendingDetection {
	threshold := CalculateVisibilityThreshold(minDetections)

	p.pendingMutex.RLock()
	result := make([]SSEPendingDetection, 0, len(p.pendingDetections))
	for key := range p.pendingDetections {
		item := p.pendingDetections[key]
		if item.Count < threshold {
			continue
		}
		result = append(result, SSEPendingDetection{
			Species:        item.Detection.Result.Species.CommonName,
			ScientificName: item.Detection.Result.Species.ScientificName,
			Thumbnail:      p.getThumbnailURL(item.Detection.Result.Species.ScientificName),
			Status:         PendingStatusActive,
			FirstDetected:  item.CreatedAt.Unix(),
			Source:         p.getDisplayNameForSource(item.Source),
			SourceID:       item.Source,
			HitCount:       item.Count,
		})
	}
	p.pendingMutex.RUnlock()

	sortPendingSnapshot(result)

	return result
}

// getThumbnailURL returns the thumbnail URL for a species from the bird image cache.
// Returns empty string if the cache is unavailable or the species has no image.
func (p *Processor) getThumbnailURL(scientificName string) string {
	if p.BirdImageCache == nil {
		return ""
	}
	img, err := p.BirdImageCache.Get(scientificName)
	if err != nil || img.IsNegativeEntry() || img.URL == "" {
		return ""
	}
	return imageprovider.ProxyImageURL(scientificName)
}

// broadcastPendingSnapshot broadcasts a pending detection snapshot via the
// PendingBroadcaster callback only when the snapshot differs from the last
// broadcast (new species, removed species, or updated hit counts).
// If no broadcaster is set, this is a no-op.
func (p *Processor) broadcastPendingSnapshot(snapshot []SSEPendingDetection) {
	p.pendingBroadcasterMu.RLock()
	broadcaster := p.PendingBroadcaster
	p.pendingBroadcasterMu.RUnlock()

	if broadcaster == nil {
		return
	}

	// Skip broadcast if snapshot is identical to the last one sent.
	// This prevents spamming SSE clients with repeated messages when
	// no new predictions arrived for any visible pending species.
	p.lastBroadcastSnapshotMu.Lock()
	if !pendingSnapshotChanged(p.lastBroadcastSnapshot, snapshot) {
		p.lastBroadcastSnapshotMu.Unlock()
		return
	}
	// Store a copy so subsequent comparisons are independent.
	p.lastBroadcastSnapshot = make([]SSEPendingDetection, len(snapshot))
	copy(p.lastBroadcastSnapshot, snapshot)
	p.lastBroadcastSnapshotMu.Unlock()

	broadcaster(snapshot)
}

// buildFlushNotification creates an SSEPendingDetection with terminal status
// for a detection that has been flushed (approved or rejected).
func (p *Processor) buildFlushNotification(item *PendingDetection, status PendingDetectionStatus) SSEPendingDetection {
	return SSEPendingDetection{
		Species:        item.Detection.Result.Species.CommonName,
		ScientificName: item.Detection.Result.Species.ScientificName,
		Thumbnail:      p.getThumbnailURL(item.Detection.Result.Species.ScientificName),
		Status:         status,
		FirstDetected:  item.CreatedAt.Unix(),
		Source:         p.getDisplayNameForSource(item.Source),
		SourceID:       item.Source,
		HitCount:       item.Count,
	}
}

// pendingSnapshotChanged reports whether two sorted pending snapshots differ
// in species composition, hit counts, or status.
func pendingSnapshotChanged(prev, curr []SSEPendingDetection) bool {
	if len(prev) != len(curr) {
		return true
	}
	for i := range prev {
		if prev[i].Species != curr[i].Species ||
			prev[i].SourceID != curr[i].SourceID ||
			prev[i].HitCount != curr[i].HitCount ||
			prev[i].Status != curr[i].Status {
			return true
		}
	}
	return false
}

// logPendingBroadcast logs pending broadcast activity at debug level.
func logPendingBroadcast(activeCount, terminalCount int) {
	if activeCount > 0 || terminalCount > 0 {
		GetLogger().Debug("Broadcasting pending detections",
			logger.Int("active", activeCount),
			logger.Int("terminal", terminalCount),
			logger.String("operation", "pending_broadcast"))
	}
}
