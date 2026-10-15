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

// audioSpan is a stretch of audio time, [from, to].
type audioSpan struct {
	from, to time.Time
}

// chunkAudio is the per-chunk data the VAD gate and the coverage publisher both
// need, built once per processed chunk.
type chunkAudio struct {
	key        vadStreamKey
	pcm        []byte
	sampleRate int
	// span is the audio the chunk's window covers on the capture timeline, ending
	// at item.Audio.EndAt (set by the producer, backlog corrected).
	span audioSpan
	// gap and freshBytes come from the producer (classifier.AudioTiming).
	gap        bool
	freshBytes int
}

// speechBandSpec reports whether a model's audio can carry speech: the ultrasonic
// bat model is analyzed on raw audio at its own rate and is not.
func speechBandSpec(spec classifier.ModelSpec) bool {
	return spec.RawSampleRate == 0 && spec.EffectiveSampleRate() > 0
}

// newChunkAudio builds the per-chunk data for item. ok is false when the chunk
// cannot carry speech the privacy detectors could hear: no PCM, an unknown model
// or an ultrasonic one.
func newChunkAudio(item *classifier.Results) (chunk chunkAudio, ok bool) {
	if len(item.PCMdata) == 0 {
		return chunkAudio{}, false
	}
	spec, found := classifier.GetModelSpec(item.ModelID)
	if !found || !speechBandSpec(spec) {
		return chunkAudio{}, false
	}
	rate := spec.EffectiveSampleRate()
	end := item.Audio.EndAt
	return chunkAudio{
		key:        vadStreamKey{source: item.Source.ID, model: item.ModelID},
		pcm:        item.PCMdata,
		sampleRate: rate,
		span:       audioSpan{from: end.Add(-pcmChunkDuration(len(item.PCMdata), rate)), to: end},
		gap:        item.Audio.Gap,
		freshBytes: item.Audio.FreshBytes,
	}, true
}

// scoredFrom is where the chunk's genuinely captured audio begins. A window after
// a gap (the first of a buffer, or after lost audio) has a zero-filled or spliced
// head that must not be claimed as scanned, so it claims nothing before its end.
func (c *chunkAudio) scoredFrom() time.Time {
	if c.gap {
		return c.span.to
	}
	return c.span.from
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
// detection: from FirstDetected until the ReadyAt of the capture window that
// conf.Settings.DetectionCaptureWindow derives from the detection's end time.
// buildSaveAudioAction derives its capture length from the same method.
func clipSpan(settings *conf.Settings, item *PendingDetection) audioSpan {
	win, _ := settings.DetectionCaptureWindow(item.FirstDetected, detectionEndTime(settings, item))
	return audioSpan{from: item.FirstDetected, to: win.ReadyAt}
}

// detectionEndTime is the EndTime normalizeDetectionTimes assigns to an approved
// detection: LastUpdated for an extended capture, else FirstDetected, plus the
// normal detection window. clipSpan and normalizeDetectionTimes both use it, so
// the span the privacy gate waits for cannot drift from the saved clip.
func detectionEndTime(settings *conf.Settings, item *PendingDetection) time.Time {
	window := normalDetectionWindow(settings)
	if item.ExtendedCapture {
		return item.LastUpdated.Add(window)
	}
	return item.FirstDetected.Add(window)
}

// coverageUpdate is one span published for a coverage key.
type coverageUpdate struct {
	key  coverageKey
	span audioSpan
}

// publishCoverage records the scanned spans in updates under one detectionMutex
// hold. A span that starts within vadCoverageJoinTolerance of its key's newest
// recorded span's end extends it (keeping the older start); a later one starts a
// new span. Spans older than privacyCoverageHorizon before the newest end are
// dropped.
func (p *Processor) publishCoverage(updates ...coverageUpdate) {
	p.detectionMutex.Lock()
	defer p.detectionMutex.Unlock()
	if p.privacyCoverage == nil {
		p.privacyCoverage = make(map[coverageKey][]audioSpan)
	}
	for _, u := range updates {
		p.mergeCoverageLocked(u.key, u.span)
	}
}

// mergeCoverageLocked is the per-key body of publishCoverage; the caller must
// hold detectionMutex and have created the map.
func (p *Processor) mergeCoverageLocked(key coverageKey, span audioSpan) {
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

// publishPrivacyCoverage publishes the coverage a processed chunk earned, in one
// step. Call it after the chunk's VAD hit and label hits are written, so a
// published span never claims audio whose hits are still outstanding. vad is the
// span runVADGate returned when it ran an inference (vadRan). When the privacy
// filter is on, the label model's own chunk counts as label coverage.
func (p *Processor) publishPrivacyCoverage(settings *conf.Settings, chunk *chunkAudio, vad audioSpan, vadRan bool) {
	if !settings.Realtime.PrivacyFilter.Enabled {
		return
	}
	updates := make([]coverageUpdate, 0, 2)
	if vadRan {
		updates = append(updates, coverageUpdate{coverageKey{chunk.key.source, chunk.key.model, metrics.TriggerVAD}, vad})
	}
	updates = append(updates, coverageUpdate{
		coverageKey{chunk.key.source, chunk.key.model, metrics.TriggerLabel},
		audioSpan{from: chunk.scoredFrom(), to: chunk.span.to},
	})
	p.publishCoverage(updates...)
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

// privacyCheck is one consistent read of the privacy state for a pending
// detection: its saved clip span, the source's latest speech hit and the
// coverage gate's verdict. The hit and the coverage are read in one
// detectionMutex hold; a chunk writes its hit before it publishes the coverage
// that includes it, so any coverage the check sees has its hits visible in it.
type privacyCheck struct {
	clip    audioSpan
	hit     HumanDetection
	hasHit  bool
	verdict coverageVerdict
}

// checkPrivacy takes the privacyCheck for a pending detection at now. With the
// privacy filter off it reads nothing and the verdict is coverageKeep.
//
// The verdict defers until a speech detector has scored the whole saved clip span
// (padded by privacyClipGuard), so speech in the clip's post-capture tail is not
// scored after the decision. It keeps the detection when no detector has recently
// scored the source (a bat-only source, or a VAD that never loaded and no label
// model running), and reports coverageTimedOut once now passes the clip end by
// privacyCoverageMaxWait.
func (p *Processor) checkPrivacy(settings *conf.Settings, item *PendingDetection, now time.Time) privacyCheck {
	clip := clipSpan(settings, item)
	if !settings.Realtime.PrivacyFilter.Enabled {
		return privacyCheck{clip: clip, verdict: coverageKeep}
	}

	p.detectionMutex.RLock()
	defer p.detectionMutex.RUnlock()
	hit, hasHit := p.LastHumanDetection[item.Source]
	return privacyCheck{clip: clip, hit: hit, hasHit: hasHit, verdict: p.coverageVerdictLocked(settings, item.Source, clip, now)}
}

// coverageVerdictLocked is the coverage half of checkPrivacy; the caller must
// hold detectionMutex.
func (p *Processor) coverageVerdictLocked(settings *conf.Settings, source string, clip audioSpan, now time.Time) coverageVerdict {
	detector := p.requiredDetectorLocked(settings, source, now)
	if !p.hasRecentCoverageLocked(source, detector, now) {
		return coverageKeep
	}
	for key, spans := range p.privacyCoverage {
		if key.source != source || key.detector != detector {
			continue
		}
		for _, s := range spans {
			if !s.from.After(clip.from.Add(-privacyClipGuard)) && !s.to.Before(clip.to.Add(privacyClipGuard)) {
				return coverageKeep
			}
		}
	}
	if now.Before(clip.to.Add(privacyCoverageMaxWait)) {
		return coverageDefer
	}
	return coverageTimedOut
}
