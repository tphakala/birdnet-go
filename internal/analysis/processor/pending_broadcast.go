package processor

import (
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
}

// CalculateVisibilityThreshold computes the minimum hit count for a pending
// detection to be visible in the "currently hearing" card.
// It returns 25% of minDetections, floored at 2.
func CalculateVisibilityThreshold(minDetections int) int {
	threshold := minDetections / 4
	return max(2, threshold)
}

// SnapshotVisiblePending returns all pending detections that have accumulated
// enough hits to pass the visibility threshold. Results have status "active".
// The caller must NOT hold pendingMutex.
func (p *Processor) SnapshotVisiblePending(minDetections int) []SSEPendingDetection {
	threshold := CalculateVisibilityThreshold(minDetections)

	p.pendingMutex.RLock()
	// Pre-count visible items for capacity hint.
	count := 0
	for key := range p.pendingDetections {
		if p.pendingDetections[key].Count >= threshold {
			count++
		}
	}

	result := make([]SSEPendingDetection, 0, count)
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
			FirstDetected:  item.FirstDetected.Unix(),
			Source:         p.getDisplayNameForSource(item.Source),
		})
	}
	p.pendingMutex.RUnlock()

	return result
}

// getThumbnailURL returns the thumbnail URL for a species from the bird image cache.
// Returns empty string if the cache is unavailable or the species has no image.
func (p *Processor) getThumbnailURL(scientificName string) string {
	if p.BirdImageCache == nil {
		return ""
	}
	img, err := p.BirdImageCache.Get(scientificName)
	if err != nil {
		return ""
	}
	return img.URL
}

// broadcastPendingSnapshot broadcasts a pending detection snapshot via the
// PendingBroadcaster callback. If no broadcaster is set, this is a no-op.
func (p *Processor) broadcastPendingSnapshot(snapshot []SSEPendingDetection) {
	p.pendingBroadcasterMu.RLock()
	broadcaster := p.PendingBroadcaster
	p.pendingBroadcasterMu.RUnlock()

	if broadcaster == nil {
		return
	}

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
		FirstDetected:  item.FirstDetected.Unix(),
		Source:         p.getDisplayNameForSource(item.Source),
	}
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
