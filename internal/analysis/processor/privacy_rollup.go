package processor

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/privacyrollup"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	// reasonPrivacyFilter is the discard reason shouldDiscardDetection returns for
	// a detection the privacy filter dropped.
	reasonPrivacyFilter = "privacy filter"
)

// sourcedEvent is a rollup event whose source is still the registry id of the
// audio source; privacySource fills the identity after the processor's mutexes
// are released.
type sourcedEvent struct {
	source string
	ev     privacyrollup.Event
}

// SetPrivacyRollup sets the recorder of privacy filter activity. Call it before
// Start and never reset it; r must not be a typed nil.
func (p *Processor) SetPrivacyRollup(r privacyrollup.Recorder) {
	p.privacyRollup = r
}

// PrivacyRollup returns the recorder set by SetPrivacyRollup, or nil.
func (p *Processor) PrivacyRollup() privacyrollup.Recorder {
	return p.privacyRollup
}

// registeredSource looks id up in the source registry, connection string first
// and then ID. It returns false when no registry is set or id is unknown.
func (p *Processor) registeredSource(id string) (*audiocore.AudioSource, bool) {
	registry := p.Registry()
	if registry == nil {
		return nil, false
	}
	if src, ok := registry.GetByConnection(id); ok {
		return src, true
	}
	return registry.Get(id)
}

// recordPrivacyEvents completes the source identity of events and hands them to
// the recorder. It must run with pendingMutex and detectionMutex released. A
// processor without a recorder does nothing.
func (p *Processor) recordPrivacyEvents(settings *conf.Settings, events ...sourcedEvent) {
	if p.privacyRollup == nil {
		return
	}
	node := settings.Main.Name
	if node == "" {
		node = entities.DefaultSourceNodeName
	}
	for i := range events {
		ev := events[i].ev
		ev.NodeName = node
		if src, ok := p.registeredSource(events[i].source); ok {
			ev.SourceURI = src.SafeString
			ev.DisplayName = src.DisplayName
		}
		p.privacyRollup.Record(ev)
	}
}

// privacyDiscard builds the discard record of a detection the privacy filter
// dropped. It is pure: the source identity comes from the detection itself, as the
// save path reads it, so no lock or registry is needed under pendingMutex.
func privacyDiscard(settings *conf.Settings, item *PendingDetection, priv *privacyCheck, now time.Time) privacyrollup.Discard {
	pf := &settings.Realtime.PrivacyFilter
	res := &item.Detection.Result
	node := res.SourceNode
	if node == "" {
		node = entities.DefaultSourceNodeName
	}
	hits := make([]privacyrollup.DiscardHit, len(priv.contributions))
	for i := range priv.contributions {
		c := &priv.contributions[i]
		hits[i] = privacyrollup.DiscardHit{Detector: c.modelID, Trigger: c.trigger, Count: c.count, MinScore: c.minScore, MaxScore: c.maxScore}
	}
	return privacyrollup.Discard{
		At:                  now,
		SourceURI:           res.AudioSource.SafeString,
		NodeName:            node,
		DisplayName:         res.AudioSource.DisplayName,
		ScientificName:      res.Species.ScientificName,
		CommonName:          res.Species.CommonName,
		Confidence:          res.Confidence,
		ModelID:             item.BestModelID,
		ClipBegin:           priv.clip.from,
		ClipEnd:             priv.clip.to,
		LabelThresholdMilli: privacyrollup.ThresholdMilli(float64(pf.Confidence)),
		VADThresholdMilli:   privacyrollup.ThresholdMilli(clampVADThreshold(pf.VAD.Threshold)),
		VADEnabled:          pf.VAD.Enabled,
		HitCountsLowerBound: priv.hitCountsLowerBound,
		Hits:                hits,
	}
}

// recordPrivacyDiscards hands discard records to the recorder. It must run with
// pendingMutex and detectionMutex released. A processor without a recorder does
// nothing.
func (p *Processor) recordPrivacyDiscards(records []privacyrollup.Discard) {
	if p.privacyRollup == nil {
		return
	}
	for i := range records {
		p.privacyRollup.RecordDiscard(records[i])
	}
}

// hitEvent is the rollup event of kind for hit, keyed on the hit's own stamp,
// detector, threshold and score.
func hitEvent(source string, kind privacyrollup.Kind, h *humanHit) sourcedEvent {
	return sourcedEvent{source: source, ev: privacyrollup.Event{
		Kind: kind, At: h.at, ModelID: h.modelID, Trigger: h.trigger,
		ThresholdMilli: h.thresholdMilli, Score: h.score,
	}}
}

// discardEvent is the event of a detection dropped by the privacy filter,
// attributed to the hit the filter's rule compared (the source's latest hit).
func discardEvent(source string, hit *HumanDetection) sourcedEvent {
	trigger := hit.Trigger
	if trigger == "" {
		// Both writers set a trigger; never key a row on an empty one.
		trigger = metrics.TriggerLabel
	}
	return sourcedEvent{source: source, ev: privacyrollup.Event{
		Kind: privacyrollup.KindDiscard, At: hit.Time, ModelID: hit.ModelID, Trigger: trigger,
		ThresholdMilli: hit.ThresholdMilli, Score: hit.Score,
	}}
}

// unscannedSaveEvent is the event of a detection approved after the coverage wait
// expired, keyed on the detector the verdict was computed for and the clip start.
func unscannedSaveEvent(source string, priv *privacyCheck) sourcedEvent {
	return sourcedEvent{source: source, ev: privacyrollup.Event{
		Kind: privacyrollup.KindUnscannedSave, At: priv.clip.from, ModelID: privacyrollup.NoModelID,
		Trigger: priv.detector, ThresholdMilli: privacyrollup.NoThresholdMilli,
	}}
}
