package processor

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	// labelConfirmMinBaseOverlap is the effective base overlap above which a
	// human label hit needs a second hit before it counts as speech. At more than
	// half the analysis clip length every stretch of audio is scored more than
	// twice, so one stray label score is weak evidence. At or below it (strictly
	// greater than is required) a single label hit counts, as it always has.
	labelConfirmMinBaseOverlap = classifier.AnalysisBaseClipLength / 2

	// labelConfirmWindow is the longest gap between the audio-end stamps of two
	// hits for the second to confirm the first. A held label hit with no second
	// hit inside the window is dropped and never counts.
	labelConfirmWindow = 15 * time.Second

	// privacyFilterOperation is the log operation of a human hit that counts as
	// speech. The events API counts every line with this exact operation as a
	// privacy discard.
	privacyFilterOperation = "privacy_filter"

	// labelConfirmOperation is the log operation of the label confirmation
	// messages. It is deliberately not "privacy_filter": the events API counts
	// every privacy_filter line as a privacy discard (see how
	// privacy_filter_coverage is treated in events.go).
	labelConfirmOperation = "privacy_filter_label_confirm"
)

// heldLabelHit is a human label hit waiting for a second hit.
type heldLabelHit struct {
	at         time.Time
	confidence float32
}

// labelConfirmState is the per-source confirmation state, guarded by
// detectionMutex. It keeps only the latest held hit and the latest counted hit,
// so its size does not grow with the number of hits.
type labelConfirmState struct {
	held        *heldLabelHit
	lastCounted time.Time // audio end of the latest counted hit; zero when none
}

// labelHitDecision is what a hit did to the confirmation state, for logging
// after the lock is released.
type labelHitDecision struct {
	counts    bool          // the hit itself counts as speech
	held      bool          // the hit was held for confirmation
	confirmed *heldLabelHit // a held hit that this hit confirmed and was recorded
	dropped   *heldLabelHit // a held hit whose window expired without a second hit
}

// labelConfirmationActive reports whether a single label hit needs confirmation
// at baseOverlap, the effective base overlap read for the current operation.
func labelConfirmationActive(baseOverlap time.Duration) bool {
	return baseOverlap > labelConfirmMinBaseOverlap
}

// withinLabelConfirmWindow reports whether two audio-end stamps are close
// enough to confirm each other.
func withinLabelConfirmWindow(a, b time.Time) bool {
	return a.Sub(b).Abs() <= labelConfirmWindow
}

// labelConfirmStateLocked returns source's state, creating it lazily. The caller
// holds detectionMutex for writing.
func (p *Processor) labelConfirmStateLocked(source string) *labelConfirmState {
	if p.labelConfirm == nil {
		p.labelConfirm = make(map[string]*labelConfirmState)
	}
	st, ok := p.labelConfirm[source]
	if !ok {
		st = &labelConfirmState{}
		p.labelConfirm[source] = st
	}
	return st
}

// noteCountedLocked remembers at as the latest counted hit of the source.
func (st *labelConfirmState) noteCountedLocked(at time.Time) {
	if at.After(st.lastCounted) {
		st.lastCounted = at
	}
}

// recordLabelHit applies the confirmation rule to one human label hit stamped at
// the audio end `at`. Below the overlap threshold it records the hit at once.
// Above it the hit counts when a held hit of the source is inside the window (any
// model, even the same chunk) or when a hit already counted is, and is otherwise
// held. Hits that count are recorded with their own stamps and the label trigger.
func (p *Processor) recordLabelHit(settings *conf.Settings, source string, at time.Time, confidence float32) labelHitDecision {
	baseOverlap := p.effectiveBaseOverlap(settings)

	p.detectionMutex.Lock()
	defer p.detectionMutex.Unlock()

	if !labelConfirmationActive(baseOverlap) {
		p.recordHumanDetectionLocked(source, at, metrics.TriggerLabel)
		return labelHitDecision{counts: true}
	}

	st := p.labelConfirmStateLocked(source)
	var d labelHitDecision
	if st.held != nil {
		if withinLabelConfirmWindow(st.held.at, at) {
			d.confirmed = st.held
			st.held = nil
			p.recordHumanDetectionLocked(source, d.confirmed.at, metrics.TriggerLabel)
			st.noteCountedLocked(d.confirmed.at)
		} else {
			d.dropped = st.held
			st.held = nil
		}
	}

	if d.confirmed != nil || (!st.lastCounted.IsZero() && withinLabelConfirmWindow(st.lastCounted, at)) {
		p.recordHumanDetectionLocked(source, at, metrics.TriggerLabel)
		st.noteCountedLocked(at)
		d.counts = true
		return d
	}

	st.held = &heldLabelHit{at: at, confidence: confidence}
	d.held = true
	return d
}

// recordVADHit records a VAD speech hit, which always counts on its own, and
// lets it confirm a held label hit of the source. The VAD hit also counts as a
// confirmer for label hits that follow it inside the window.
func (p *Processor) recordVADHit(settings *conf.Settings, source string, at time.Time) labelHitDecision {
	baseOverlap := p.effectiveBaseOverlap(settings)

	p.detectionMutex.Lock()
	defer p.detectionMutex.Unlock()

	p.recordHumanDetectionLocked(source, at, metrics.TriggerVAD)
	d := labelHitDecision{counts: true}
	if !labelConfirmationActive(baseOverlap) {
		return d
	}

	st := p.labelConfirmStateLocked(source)
	if st.held != nil {
		if withinLabelConfirmWindow(st.held.at, at) {
			d.confirmed = st.held
			p.recordHumanDetectionLocked(source, d.confirmed.at, metrics.TriggerLabel)
		} else {
			d.dropped = st.held
		}
		st.held = nil
	}
	st.noteCountedLocked(at)
	return d
}

// logLabelHitDecision logs the outcome of a hit after detectionMutex is
// released. Counted label hits keep the "human detection filtered" line; held and
// ignored hits use the label confirmation operation.
func (p *Processor) logLabelHitDecision(settings *conf.Settings, source string, at time.Time, confidence float32, d *labelHitDecision, logSelf bool) {
	threshold := float32(settings.Realtime.PrivacyFilter.Confidence)
	display := p.getDisplayNameForSource(source)
	logCounted := func(conf float32) {
		GetLogger().Info("human detection filtered",
			logger.Float32("confidence", conf),
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
	case d.held:
		GetLogger().Debug("human label hit held for confirmation",
			logger.Float32("confidence", confidence),
			logger.Time("hit_time", at),
			logger.Duration("window", labelConfirmWindow),
			logger.String("source", display),
			logger.String("operation", labelConfirmOperation))
	case d.counts && logSelf:
		logCounted(confidence)
	}
}
