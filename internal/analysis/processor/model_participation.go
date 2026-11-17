package processor

import (
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// activeModelIDs returns the registry IDs of the models analysing the source at
// this moment: a snapshot taken when a detection is approved. An analysis buffer
// for (source, model) stands for "configured on this source and loaded" (the
// buffers are allocated and released together with the model topology, with a
// short lag on teardown), and IsModelActive excludes a model paused by its
// schedule. Orchestrator.IsModelLoaded is deliberately not called: LoadModel holds
// the orchestrator lock for a whole model load, and this runs under pendingMutex.
// It returns nil when the buffer manager or the orchestrator is not wired.
func (p *Processor) activeModelIDs(sourceID string) []string {
	if p.BufferMgr == nil || p.Bn == nil {
		return nil
	}
	return activeModels(p.BufferMgr.AnalysisBuffers(sourceID), p.Bn.IsModelActive)
}

// activeModels returns the keys of buffers (registry model IDs) for which
// isActive reports true. Only the keys are read.
func activeModels(buffers map[string]*buffer.AnalysisBuffer, isActive func(modelID string) bool) []string {
	ids := make([]string, 0, len(buffers))
	for modelID := range buffers {
		if isActive(modelID) {
			ids = append(ids, modelID)
		}
	}
	return ids
}

// resultModelContributions builds the Result entries for a detection: one per
// hit model with its statistics, plus a zero-hit entry (HitCount 0, no statistics)
// for each active model that recorded no hit. A hit entry is never replaced by a
// zero-hit entry.
func resultModelContributions(hits map[string]ModelContribution, activeIDs []string) map[string]detection.ResultModelContrib {
	out := make(map[string]detection.ResultModelContrib, len(hits)+len(activeIDs))
	for modelID, contrib := range hits {
		out[modelID] = detection.ResultModelContrib{
			Model:         classifier.DetectionModelInfoForID(modelID),
			HitCount:      contrib.HitCount,
			MaxConfidence: contrib.MaxConfidence,
			FirstHitAt:    contrib.FirstHitAt,
			LastHitAt:     contrib.LastHitAt,
			MinConfidence: contrib.MinConfidence,
			ConfidenceSum: contrib.ConfidenceSum,
		}
	}
	for _, modelID := range activeIDs {
		if _, hit := out[modelID]; hit {
			continue
		}
		out[modelID] = detection.ResultModelContrib{Model: classifier.DetectionModelInfoForID(modelID)}
	}
	return out
}
