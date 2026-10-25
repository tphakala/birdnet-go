package processor

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/privacyrollup"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	// labelConfirmMinBaseOverlap is the effective base overlap above which a human
	// label hit needs a second hit before it counts as speech (at or below it, a
	// single hit counts). Above half the clip length every stretch of audio is
	// scored more than twice, so one stray label score is weak evidence.
	labelConfirmMinBaseOverlap = classifier.AnalysisBaseClipLength / 2

	// labelConfirmWindow is the longest gap between the audio-end stamps of two
	// hits for the second to confirm the first. A held hit with no second hit
	// inside the window is dropped and never counts.
	labelConfirmWindow = 15 * time.Second

	// privacyFilterOperation is the log operation of a human hit that counts as
	// speech. The events API counts every line with this exact operation as a
	// privacy discard.
	privacyFilterOperation = "privacy_filter"

	// privacyFilterDiscardOperation is the log operation of the debug summary of a
	// detection the privacy filter dropped. It differs from privacyFilterOperation
	// so the summary is not counted as a second discard.
	privacyFilterDiscardOperation = "privacy_filter_discard"

	// labelConfirmOperation is the log operation of the confirmation messages. It
	// must differ from privacyFilterOperation so held and ignored hits are not
	// counted as discards.
	labelConfirmOperation = "privacy_filter_label_confirm"
)

// labelHitDecision is what a hit did to the confirmation state, for logging
// after the lock is released. A label hit counts unless held is set.
type labelHitDecision struct {
	held      bool      // the label hit was held for confirmation
	confirmed *humanHit // a held hit that this hit confirmed and was recorded
	dropped   *humanHit // a held hit that will never count: its window expired without a second hit, or a hit arrived with label confirmation off

	// confirmationOff marks dropped as resolved by a hit that arrived with label
	// confirmation off.
	confirmationOff bool
}

// withinLabelConfirmWindow reports whether two audio-end stamps are close
// enough to confirm each other.
func withinLabelConfirmWindow(a, b time.Time) bool {
	return a.Sub(b).Abs() <= labelConfirmWindow
}

// popHeldLocked removes and returns the held label hit of source, if any. It is
// the only place a held hit leaves the map. The caller holds detectionMutex for
// writing.
func (p *Processor) popHeldLocked(source string) (humanHit, bool) {
	held, ok := p.heldLabelHits[source]
	if ok {
		delete(p.heldLabelHits, source)
	}
	return held, ok
}

// dropHeldLocked removes the held label hit of source, if any, and returns it as
// dropped. It resolves a held hit when a hit arrives with label confirmation
// off: the hit was held under a rule that no longer applies, so it is never
// confirmed. The caller holds detectionMutex for writing.
func (p *Processor) dropHeldLocked(source string) labelHitDecision {
	held, ok := p.popHeldLocked(source)
	if !ok {
		return labelHitDecision{}
	}
	return labelHitDecision{dropped: &held, confirmationOff: true}
}

// takeHeldLocked removes the held label hit of source, if any, and resolves it
// against a counted hit stamped at: inside the window it is confirmed and
// recorded with its own stamp, model and confidence, otherwise dropped. The
// caller holds detectionMutex for writing.
func (p *Processor) takeHeldLocked(source string, at time.Time) labelHitDecision {
	held, ok := p.popHeldLocked(source)
	if !ok {
		return labelHitDecision{}
	}
	if !withinLabelConfirmWindow(held.at, at) {
		return labelHitDecision{dropped: &held}
	}
	p.recordHumanHitLocked(source, held)
	return labelHitDecision{confirmed: &held}
}

// recordLabelHit applies the confirmation rule to one human label hit, built
// with labelHumanHit. At or below labelConfirmMinBaseOverlap it is recorded at
// once and drops a held hit of the source, which can no longer be confirmed.
// Above it, the hit counts when it confirms a held hit of the source (any
// model, even the same chunk) or when a counted hit of the source, label or VAD,
// is inside the window; otherwise it is held. LastHumanDetection carries the
// latest counted stamp, so a hit counted while confirmation was off also
// confirms after a hot reload to a higher overlap. Counted hits are recorded
// with their own stamps, models and confidences, so the verdict can be traced to
// every model behind it.
func (p *Processor) recordLabelHit(settings *conf.Settings, source string, hit humanHit) labelHitDecision {
	baseOverlap := p.effectiveBaseOverlap(settings)

	p.detectionMutex.Lock()
	defer p.detectionMutex.Unlock()

	if baseOverlap <= labelConfirmMinBaseOverlap {
		d := p.dropHeldLocked(source)
		p.recordHumanHitLocked(source, hit)
		return d
	}

	d := p.takeHeldLocked(source, hit.at)
	if last, ok := p.LastHumanDetection[source]; d.confirmed != nil || (ok && withinLabelConfirmWindow(last.Time, hit.at)) {
		p.recordHumanHitLocked(source, hit)
		return d
	}

	if p.heldLabelHits == nil {
		p.heldLabelHits = make(map[string]humanHit)
	}
	p.heldLabelHits[source] = hit
	d.held = true
	return d
}

// recordVADHit records a VAD speech hit, built with vadHumanHit. It always
// counts on its own. Above labelConfirmMinBaseOverlap it confirms a held label
// hit of the source; at or below it, it drops the held hit instead.
func (p *Processor) recordVADHit(settings *conf.Settings, source string, hit humanHit) labelHitDecision {
	baseOverlap := p.effectiveBaseOverlap(settings)

	p.detectionMutex.Lock()
	defer p.detectionMutex.Unlock()

	p.recordHumanHitLocked(source, hit)
	if baseOverlap <= labelConfirmMinBaseOverlap {
		return p.dropHeldLocked(source)
	}
	return p.takeHeldLocked(source, hit.at)
}

// reportHitDecision logs the outcome of hit and hands its rollup events to the
// recorder, after detectionMutex is released. hit is the label or VAD hit being
// processed and d is what it did to the confirmation state; the hit is recorded
// unless d.held is set. The lines are logged regardless of the recorder; the
// rollup events are built only when one is set.
func (p *Processor) reportHitDecision(settings *conf.Settings, source string, hit *humanHit, d *labelHitDecision) {
	p.logHitDecision(settings, source, hit, d)
	if p.privacyRollup != nil {
		p.recordPrivacyEvents(settings, hitEvents(source, hit, d)...)
	}
}

// hitEvents builds the rollup events of one hit decision: the dropped, the
// confirmed and the own hit, in that order. Each is reported under its own
// stamp, model and threshold.
func hitEvents(source string, hit *humanHit, d *labelHitDecision) []sourcedEvent {
	var events []sourcedEvent
	if d.dropped != nil {
		events = append(events, hitEvent(source, privacyrollup.KindIgnoredHeld, d.dropped))
	}
	if d.confirmed != nil {
		events = append(events, hitEvent(source, privacyrollup.KindConfirmedHeld, d.confirmed))
	}
	if d.held {
		events = append(events, hitEvent(source, privacyrollup.KindHeldHit, hit))
	} else {
		events = append(events, hitEvent(source, privacyrollup.KindCountedHit, hit))
	}
	return events
}

// logHitDecision logs the outcome of hit. Counted label hits keep the "human
// detection filtered" line; held and ignored hits use labelConfirmOperation; a
// VAD hit logs its own line elsewhere. Every line names the model that scored
// the hit, and a hit that a later hit confirmed also names the confirming
// detector, so the aggregated verdict can be traced back to the models behind
// it.
func (p *Processor) logHitDecision(settings *conf.Settings, source string, hit *humanHit, d *labelHitDecision) {
	vad := hit.trigger == metrics.TriggerVAD
	if vad && d.confirmed == nil && d.dropped == nil {
		return
	}

	threshold := float32(settings.Realtime.PrivacyFilter.Confidence)
	display := p.getDisplayNameForSource(source)
	logCounted := func(h *humanHit, confirmedBy string) {
		fields := []logger.Field{
			logger.Float32("confidence", float32(h.score)),
			logger.Float32("threshold", threshold),
			logger.String("model_id", h.modelID),
			logger.Time("hit_time", h.at),
			logger.String("source", display),
			logger.String("operation", privacyFilterOperation),
		}
		if confirmedBy != "" {
			fields = append(fields, logger.String("confirmed_by", confirmedBy))
		}
		GetLogger().Info("human detection filtered", fields...)
	}
	if d.dropped != nil {
		msg := "privacy filter: human label hit ignored, no second hit within the confirmation window"
		fields := []logger.Field{
			logger.Float32("confidence", float32(d.dropped.score)),
			logger.Float32("threshold", threshold),
			logger.String("model_id", d.dropped.modelID),
			logger.Time("hit_time", d.dropped.at),
			logger.String("source", display),
			logger.String("operation", labelConfirmOperation),
		}
		if d.confirmationOff {
			msg = "privacy filter: human label hit ignored, label confirmation no longer applies"
		} else {
			fields = append(fields, logger.Duration("window", labelConfirmWindow))
		}
		GetLogger().Info(msg, fields...)
	}
	if d.confirmed != nil {
		// A held hit confirmed by hit, whether a label hit or a VAD hit (whose
		// model is vadHitModelID).
		logCounted(d.confirmed, hit.modelID)
	}
	if vad {
		return
	}
	if d.held {
		GetLogger().Debug("human label hit held for confirmation",
			logger.Float32("confidence", float32(hit.score)),
			logger.String("model_id", hit.modelID),
			logger.Time("hit_time", hit.at),
			logger.Duration("window", labelConfirmWindow),
			logger.String("source", display),
			logger.String("operation", labelConfirmOperation))
		return
	}
	logCounted(hit, "")
}
