package privacyrollup

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// Discard is one pending detection the privacy filter dropped, with the
// detectors whose hits caused it. The source identity is captured when the
// detection is discarded; the writer resolves it to an audio_sources id at flush
// time.
type Discard struct {
	// At is the wall-clock time of the flush that discarded the detection.
	At time.Time
	// SourceURI, NodeName and DisplayName identify the audio source as the
	// detection save path does. An empty SourceURI marks an unresolvable source,
	// which is stored with a NULL source.
	SourceURI, NodeName, DisplayName string
	// ScientificName and CommonName name the discarded species.
	ScientificName, CommonName string
	// Confidence is the confidence of the discarded detection.
	Confidence float64
	// ModelID is the registry id of the model that made the detection.
	ModelID string
	// ClipBegin and ClipEnd are the span the exporter would have saved.
	ClipBegin, ClipEnd time.Time
	// LabelThresholdMilli and VADThresholdMilli are the thresholds in effect at
	// the discard, in thousandths; VADEnabled is whether the voice activity gate
	// was on.
	LabelThresholdMilli, VADThresholdMilli int
	VADEnabled                             bool
	// HitCountsLowerBound is true when the hit counts of Hits are lower bounds.
	HitCountsLowerBound bool
	// Hits lists the counted hits that reached the clip, per detector.
	Hits []DiscardHit
}

// DiscardHit totals the counted hits one detector contributed to a Discard.
type DiscardHit struct {
	// Detector is the classifier model id or the voice activity gate's id.
	Detector string
	// Trigger is "label" or "vad".
	Trigger string
	// Count is the number of counted hits.
	Count int
	// MinScore and MaxScore are the lowest and highest score among them.
	MinScore, MaxScore float64
}

// discardRow converts d to its stored form; sourceID is nil for a NULL source.
func discardRow(d *Discard, sourceID *uint) *entities.PrivacyFilterDiscard {
	row := &entities.PrivacyFilterDiscard{
		DiscardedAt:         d.At.UnixMilli(),
		SourceID:            sourceID,
		ScientificName:      d.ScientificName,
		CommonName:          d.CommonName,
		Confidence:          d.Confidence,
		ModelID:             d.ModelID,
		ClipBegin:           d.ClipBegin.UnixMilli(),
		ClipEnd:             d.ClipEnd.UnixMilli(),
		LabelThresholdMilli: d.LabelThresholdMilli,
		VADEnabled:          d.VADEnabled,
		VADThresholdMilli:   d.VADThresholdMilli,
		HitCountsLowerBound: d.HitCountsLowerBound,
		Hits:                make([]entities.PrivacyFilterDiscardHit, len(d.Hits)),
	}
	for i := range d.Hits {
		h := &d.Hits[i]
		row.Hits[i] = entities.PrivacyFilterDiscardHit{
			Detector: h.Detector, TriggerType: h.Trigger, HitCount: h.Count, MinScore: h.MinScore, MaxScore: h.MaxScore,
		}
	}
	return row
}
