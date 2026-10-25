package processor

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/privacyrollup"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	// reasonPrivacyFilter is the discard reason shouldDiscardDetection returns for
	// a detection the privacy filter dropped.
	reasonPrivacyFilter = "privacy filter"

	// defaultRollupNodeName is the node name of an event when the node is unnamed.
	// It must match the fallback of the detection save path
	// (v2only/datastore.go, "if nodeName == \"default\""), so the rollup resolves
	// the same audio_sources row as the detections of the source.
	defaultRollupNodeName = "default"
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

// RegisteredSource looks id up in the source registry, connection string first
// and then ID. It returns false when no registry is set or id is unknown.
func (p *Processor) RegisteredSource(id string) (*audiocore.AudioSource, bool) {
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
		node = defaultRollupNodeName
	}
	for i := range events {
		ev := events[i].ev
		ev.NodeName = node
		if src, ok := p.RegisteredSource(events[i].source); ok {
			ev.SourceURI = src.SafeString
			ev.DisplayName = src.DisplayName
		}
		p.privacyRollup.Record(ev)
	}
}

// labelHitEvents builds the rollup events of one label hit decision. The dropped,
// confirmed and own hit are independent, as in logLabelHitDecision: a held hit
// is reported under its own stamp, model and threshold, and a VAD hit (own is
// nil) has its counted event built by recordVADHitEvents.
func labelHitEvents(source string, own *heldLabelHit, d *labelHitDecision) []sourcedEvent {
	var events []sourcedEvent
	add := func(kind privacyrollup.Kind, h *heldLabelHit) {
		events = append(events, sourcedEvent{source: source, ev: privacyrollup.Event{
			Kind: kind, At: h.at, ModelID: h.modelID, Trigger: metrics.TriggerLabel,
			ThresholdMilli: h.thresholdMilli, Score: float64(h.confidence),
		}})
	}
	if d.dropped != nil {
		add(privacyrollup.KindIgnoredHeld, d.dropped)
	}
	if d.confirmed != nil {
		add(privacyrollup.KindConfirmedHeld, d.confirmed)
	}
	switch {
	case own == nil:
	case d.held:
		add(privacyrollup.KindHeldHit, own)
	default:
		add(privacyrollup.KindCountedHit, own)
	}
	return events
}

// recordLabelHitEvents reports a label hit decision. Call it after
// recordLabelHit has returned.
func (p *Processor) recordLabelHitEvents(settings *conf.Settings, source string, own *heldLabelHit, d *labelHitDecision) {
	if p.privacyRollup == nil {
		return
	}
	p.recordPrivacyEvents(settings, labelHitEvents(source, own, d)...)
}

// recordVADHitEvents reports a VAD hit, which always counts, and the held label
// hit it confirmed or dropped. Call it after recordVADHit has returned.
func (p *Processor) recordVADHitEvents(settings *conf.Settings, source string, at time.Time, probability float64, thresholdMilli int, d *labelHitDecision) {
	if p.privacyRollup == nil {
		return
	}
	events := labelHitEvents(source, nil, d)
	events = append(events, sourcedEvent{source: source, ev: privacyrollup.Event{
		Kind: privacyrollup.KindCountedHit, At: at, ModelID: vadHitModelID, Trigger: metrics.TriggerVAD,
		ThresholdMilli: thresholdMilli, Score: probability,
	}})
	p.recordPrivacyEvents(settings, events...)
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
