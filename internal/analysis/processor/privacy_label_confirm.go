package processor

import (
	"time"

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

	// labelConfirmOperation is the log operation of the confirmation messages. It
	// must differ from privacyFilterOperation so held and ignored hits are not
	// counted as discards.
	labelConfirmOperation = "privacy_filter_label_confirm"
)

// heldLabelHit is a human label hit: the audio end it was stamped with and its
// confidence. While it waits for a second hit it is kept in Processor.heldLabelHits.
type heldLabelHit struct {
	at         time.Time
	confidence float32
}

// labelHitDecision is what a hit did to the confirmation state, for logging
// after the lock is released. A label hit counts unless held is set.
type labelHitDecision struct {
	held      bool          // the label hit was held for confirmation
	confirmed *heldLabelHit // a held hit that this hit confirmed and was recorded
	dropped   *heldLabelHit // a held hit whose window expired without a second hit
}

// withinLabelConfirmWindow reports whether two audio-end stamps are close
// enough to confirm each other.
func withinLabelConfirmWindow(a, b time.Time) bool {
	return a.Sub(b).Abs() <= labelConfirmWindow
}

// takeHeldLocked removes the held label hit of source, if any, and resolves it
// against a counted hit stamped at: inside the window it is confirmed and
// recorded with its own stamp, otherwise dropped. The caller holds detectionMutex
// for writing.
func (p *Processor) takeHeldLocked(source string, at time.Time) labelHitDecision {
	held, ok := p.heldLabelHits[source]
	if !ok {
		return labelHitDecision{}
	}
	delete(p.heldLabelHits, source)
	if !withinLabelConfirmWindow(held.at, at) {
		return labelHitDecision{dropped: &held}
	}
	p.recordHumanDetectionLocked(source, held.at, metrics.TriggerLabel)
	return labelHitDecision{confirmed: &held}
}

// recordLabelHit applies the confirmation rule to one human label hit. At or
// below labelConfirmMinBaseOverlap it is recorded at once. Above it, the hit
// counts when it confirms a held hit of the source (any model, even the same
// chunk) or when a counted hit of the source, label or VAD, is inside the window;
// otherwise it is held. LastHumanDetection carries the latest counted stamp, so
// a hit counted while confirmation was off also confirms after a hot reload to a
// higher overlap. Counted hits are recorded with their own stamps.
func (p *Processor) recordLabelHit(settings *conf.Settings, source string, hit heldLabelHit) labelHitDecision {
	baseOverlap := p.effectiveBaseOverlap(settings)

	p.detectionMutex.Lock()
	defer p.detectionMutex.Unlock()

	if baseOverlap <= labelConfirmMinBaseOverlap {
		p.recordHumanDetectionLocked(source, hit.at, metrics.TriggerLabel)
		return labelHitDecision{}
	}

	d := p.takeHeldLocked(source, hit.at)
	if last, ok := p.LastHumanDetection[source]; d.confirmed != nil || (ok && withinLabelConfirmWindow(last.Time, hit.at)) {
		p.recordHumanDetectionLocked(source, hit.at, metrics.TriggerLabel)
		return d
	}

	if p.heldLabelHits == nil {
		p.heldLabelHits = make(map[string]heldLabelHit)
	}
	p.heldLabelHits[source] = hit
	d.held = true
	return d
}

// recordVADHit records a VAD speech hit, which always counts on its own, and
// lets it confirm a held label hit of the source.
func (p *Processor) recordVADHit(settings *conf.Settings, source string, at time.Time) labelHitDecision {
	baseOverlap := p.effectiveBaseOverlap(settings)

	p.detectionMutex.Lock()
	defer p.detectionMutex.Unlock()

	p.recordHumanDetectionLocked(source, at, metrics.TriggerVAD)
	if baseOverlap <= labelConfirmMinBaseOverlap {
		return labelHitDecision{}
	}
	return p.takeHeldLocked(source, at)
}

// logLabelHitDecision logs the outcome of a hit after detectionMutex is
// released. own is the label hit being processed, recorded unless d.held is set,
// or nil for a VAD hit (which logs its own line elsewhere). Counted label hits keep the "human detection
// filtered" line; held and ignored hits use labelConfirmOperation.
func (p *Processor) logLabelHitDecision(settings *conf.Settings, source string, own *heldLabelHit, d *labelHitDecision) {
	if own == nil && d.confirmed == nil && d.dropped == nil {
		return
	}
	threshold := float32(settings.Realtime.PrivacyFilter.Confidence)
	display := p.getDisplayNameForSource(source)
	logCounted := func(confidence float32) {
		GetLogger().Info("human detection filtered",
			logger.Float32("confidence", confidence),
			logger.Float32("threshold", threshold),
			logger.String("source", display),
			logger.String("operation", privacyFilterOperation))
	}
	if d.dropped != nil {
		GetLogger().Info("privacy filter: human label hit ignored, no second hit within the confirmation window",
			logger.Float32("confidence", d.dropped.confidence),
			logger.Float32("threshold", threshold),
			logger.Time("hit_time", d.dropped.at),
			logger.Duration("window", labelConfirmWindow),
			logger.String("source", display),
			logger.String("operation", labelConfirmOperation))
	}
	if d.confirmed != nil {
		logCounted(d.confirmed.confidence)
	}
	switch {
	case own == nil: // a VAD hit logs its own line elsewhere
	case d.held:
		GetLogger().Debug("human label hit held for confirmation",
			logger.Float32("confidence", own.confidence),
			logger.Time("hit_time", own.at),
			logger.Duration("window", labelConfirmWindow),
			logger.String("source", display),
			logger.String("operation", labelConfirmOperation))
	default:
		logCounted(own.confidence)
	}
}
