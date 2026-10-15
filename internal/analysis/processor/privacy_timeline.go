package processor

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	// privacyClipGuard pads a saved clip's span on both sides in the privacy
	// checks. A speech hit within the guard before the clip begin counts as inside
	// the clip, and the clip counts as scanned only when coverage reaches the guard
	// beyond its end. It absorbs timestamp skew between the capture buffer and the
	// analysis buffer, the sub-hop (32 ms) audio a flush leaves unscored, and the
	// (minConsec-1) hops (64 ms) before a run of speech frames. It must stay below
	// 1 s so a hit in the chunk one second before the clip begins is still kept.
	privacyClipGuard = 500 * time.Millisecond
	// privacyCoverageMaxWait is how long past a clip's end a detection waits for a
	// speech detector to scan the whole clip before it is approved unscanned.
	privacyCoverageMaxWait = 30 * time.Second
	// privacyCoverageHorizon bounds how stale a source's coverage may be before the
	// coverage gate treats the source as not covered by any detector at all (a
	// bat-only source, or a detector that never ran) and does not wait. It equals
	// the default capture buffer; an extended capture can reach further back, and
	// then a clip with a hole in its coverage waits for the fail-open timeout.
	privacyCoverageHorizon = time.Duration(conf.DefaultCaptureBufferSeconds) * time.Second
	// privacyCoverageMaxSpans caps the scanned spans kept per (source, model,
	// detector); a new span starts only after a hole, so few are ever live.
	privacyCoverageMaxSpans = 16
)

// coverageKey identifies a published coverage list: the source and model whose
// chunks were scored, and which detector (metrics.TriggerVAD or
// metrics.TriggerLabel) scored them.
type coverageKey struct {
	source   string
	model    string
	detector string
}

// coverageVerdict is the privacy coverage gate's decision for a pending detection.
type coverageVerdict int

const (
	// coverageKeep means the detection may proceed: its clip span is scanned, or
	// no detector applies to its source.
	coverageKeep coverageVerdict = iota
	// coverageDefer means a speech detector has not yet scored the whole clip
	// span; the detection stays pending.
	coverageDefer
	// coverageTimedOut means the clip span was still not fully scanned when the
	// wait expired; the detection is approved unscanned (the filter fails open).
	coverageTimedOut
)

// itemAudioEnd returns the capture time of the last sample of the chunk in item:
// item.AudioEndAt when the producer set it, else item.AudioCapturedAt, else
// StartTime advanced by PreCapture and the chunk's PCM duration (StartTime is
// back-dated by PreCapture plus the clip length at read time, so this
// reconstructs the read time; only tests and hypothetical producers that set
// none of the audio times reach it).
func itemAudioEnd(settings *conf.Settings, item *classifier.Results) time.Time {
	if !item.AudioEndAt.IsZero() {
		return item.AudioEndAt
	}
	if !item.AudioCapturedAt.IsZero() {
		return item.AudioCapturedAt
	}
	end := item.StartTime.Add(time.Duration(settings.Realtime.Audio.Export.PreCapture) * time.Second)
	if spec, ok := classifier.GetModelSpec(item.ModelID); ok {
		end = end.Add(pcmChunkDuration(len(item.PCMdata), spec.EffectiveSampleRate()))
	}
	return end
}

// recordHumanDetection records a speech hit on source at the capture time of the
// end of the audio that produced it. The hit time never moves backward: the
// discard check reads only the latest value, so a later write with an earlier
// stamp (the label path after the VAD within one chunk, or a 5 s model's chunk
// after a 3 s model's) would hide an earlier-reaching hit. An equal time keeps
// the existing entry and its trigger.
func (p *Processor) recordHumanDetection(source string, at time.Time, trigger string) {
	p.detectionMutex.Lock()
	defer p.detectionMutex.Unlock()
	if existing, ok := p.LastHumanDetection[source]; ok && !at.After(existing.Time) {
		return
	}
	p.LastHumanDetection[source] = HumanDetection{Time: at, Trigger: trigger}
}

// clipSpan returns the exact audio span the exporter saves for a pending
// detection: from FirstDetected for the capture length that
// conf.Settings.DetectionCaptureWindow derives from the detection's end time.
// normalizeDetectionTimes and buildSaveAudioAction use the same inputs, so the
// span cannot drift from the saved clip.
func clipSpan(settings *conf.Settings, item *PendingDetection) (begin, end time.Time) {
	begin = item.FirstDetected
	win, _ := settings.DetectionCaptureWindow(begin, detectionEndTime(settings, item))
	return begin, begin.Add(time.Duration(win.Length) * time.Second)
}

// detectionEndTime is the EndTime normalizeDetectionTimes assigns to an approved
// detection: LastUpdated for an extended capture, else FirstDetected, plus the
// normal detection window (export length minus pre-capture, never negative).
func detectionEndTime(settings *conf.Settings, item *PendingDetection) time.Time {
	captureLength := time.Duration(settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength := time.Duration(settings.Realtime.Audio.Export.PreCapture) * time.Second
	window := max(time.Duration(0), captureLength-preCaptureLength)
	if item.ExtendedCapture {
		return item.LastUpdated.Add(window)
	}
	return item.FirstDetected.Add(window)
}

// publishCoverage records that detector scored the audio span for key. A span
// that starts within vadCoverageJoinTolerance of the newest recorded span's end
// extends it (keeping the older start); a later one starts a new span. Spans
// older than privacyCoverageHorizon before the newest end are dropped. It takes
// detectionMutex.
func (p *Processor) publishCoverage(key coverageKey, span vadSpan) {
	p.detectionMutex.Lock()
	defer p.detectionMutex.Unlock()
	if p.privacyCoverage == nil {
		p.privacyCoverage = make(map[coverageKey][]vadSpan)
	}
	spans := p.privacyCoverage[key]
	if n := len(spans); n > 0 && span.from.Compare(spans[n-1].to.Add(vadCoverageJoinTolerance)) <= 0 {
		if span.to.After(spans[n-1].to) {
			spans[n-1].to = span.to
		}
		if span.from.Before(spans[n-1].from) {
			spans[n-1].from = span.from
		}
	} else if span.to.After(span.from) {
		spans = append(spans, span)
	}
	if len(spans) == 0 {
		return
	}
	newest := spans[len(spans)-1].to
	kept := spans[:0]
	for _, s := range spans {
		if newest.Sub(s.to) <= privacyCoverageHorizon {
			kept = append(kept, s)
		}
	}
	if len(kept) > privacyCoverageMaxSpans {
		kept = kept[len(kept)-privacyCoverageMaxSpans:]
	}
	p.privacyCoverage[key] = kept
}

// publishPrivacyCoverage publishes the coverage a processed chunk earned. Call it
// after the chunk's VAD hit and label hits are written, so a published span never
// claims audio whose hits are still outstanding. vadSpan is the span runVADGate
// returned when it ran an inference. When the privacy filter is on, the label
// model's own chunk counts as label coverage (a bat chunk does not: the label
// path cannot hear speech in ultrasonic audio).
func (p *Processor) publishPrivacyCoverage(settings *conf.Settings, item *classifier.Results, vad vadSpan, vadRan bool) {
	if !settings.Realtime.PrivacyFilter.Enabled {
		return
	}
	if vadRan {
		p.publishCoverage(coverageKey{item.Source.ID, item.ModelID, metrics.TriggerVAD}, vad)
	}
	if item.ModelID == classifier.RegistryIDBat || len(item.PCMdata) == 0 {
		return
	}
	spec, ok := classifier.GetModelSpec(item.ModelID)
	if !ok || spec.EffectiveSampleRate() <= 0 {
		return
	}
	end := itemAudioEnd(settings, item)
	from := end.Add(-pcmChunkDuration(len(item.PCMdata), spec.EffectiveSampleRate()))
	if item.AudioGap {
		// The first window of a buffer has a zero-filled overlap head and a window
		// after lost audio is spliced; neither head is real audio.
		from = end
	}
	p.publishCoverage(coverageKey{item.Source.ID, item.ModelID, metrics.TriggerLabel}, vadSpan{from: from, to: end})
}

// requiredDetectorLocked returns the detector whose coverage the gate waits for
// on source: the VAD when it is enabled and either holds a session or recently
// scored the source, else the label path. The caller must hold detectionMutex.
func (p *Processor) requiredDetectorLocked(settings *conf.Settings, source string, now time.Time) string {
	if !settings.Realtime.PrivacyFilter.VAD.Enabled {
		return metrics.TriggerLabel
	}
	if p.vadGate != nil && p.vadGate.loaded() {
		return metrics.TriggerVAD
	}
	if p.hasRecentCoverageLocked(source, metrics.TriggerVAD, now) {
		return metrics.TriggerVAD
	}
	return metrics.TriggerLabel
}

// hasRecentCoverageLocked reports whether any model's detector coverage for
// source ends within privacyCoverageHorizon of now. The caller must hold
// detectionMutex.
func (p *Processor) hasRecentCoverageLocked(source, detector string, now time.Time) bool {
	for key, spans := range p.privacyCoverage {
		if key.source != source || key.detector != detector || len(spans) == 0 {
			continue
		}
		if now.Sub(spans[len(spans)-1].to) <= privacyCoverageHorizon {
			return true
		}
	}
	return false
}

// privacyCoverageVerdict decides whether a pending detection whose deadline has
// passed may proceed. It defers until a speech detector has scored the whole
// saved clip span (padded by privacyClipGuard), so speech in the clip's
// post-capture tail is not scored after the decision. It keeps the detection
// when the privacy filter is off or no detector has recently scored the source
// (a bat-only source, or a VAD that never loaded and no label model running),
// and reports coverageTimedOut once now passes the clip end by
// privacyCoverageMaxWait.
func (p *Processor) privacyCoverageVerdict(settings *conf.Settings, item *PendingDetection, now time.Time) coverageVerdict {
	if !settings.Realtime.PrivacyFilter.Enabled {
		return coverageKeep
	}
	begin, end := clipSpan(settings, item)

	p.detectionMutex.RLock()
	defer p.detectionMutex.RUnlock()
	detector := p.requiredDetectorLocked(settings, item.Source, now)
	if !p.hasRecentCoverageLocked(item.Source, detector, now) {
		return coverageKeep
	}
	for key, spans := range p.privacyCoverage {
		if key.source != item.Source || key.detector != detector {
			continue
		}
		for _, s := range spans {
			if !s.from.After(begin.Add(-privacyClipGuard)) && !s.to.Before(end.Add(privacyClipGuard)) {
				return coverageKeep
			}
		}
	}
	if now.Before(end.Add(privacyCoverageMaxWait)) {
		return coverageDefer
	}
	return coverageTimedOut
}
