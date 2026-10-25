package processor

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/privacyrollup"
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
	// coverage gate treats the source as not covered by any detector at all (the
	// filter just switched on, a VAD toggle, a stall, or a bat-only source). A
	// speech-band detection then waits for the fail-open timeout; a bat detection
	// keeps. A clip whose head predates every retained label span waits too, even
	// when the one model that scored the source is current. It equals
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
	// it comes from a model whose audio cannot carry speech and no detector has
	// recently scored its source.
	coverageKeep coverageVerdict = iota
	// coverageDefer means a speech detector has not yet scored the whole clip
	// span; the detection stays pending.
	coverageDefer
	// coverageTimedOut means the clip span was still not fully scanned when the
	// wait expired; the detection is approved unscanned (the filter fails open).
	coverageTimedOut
)

// audioSpan is a stretch of audio time, [from, to]. A coverage span published
// from a producer that reports stream positions also carries the stream
// position range it covers (startPos to endPos) and the stream generation, so
// spans join on positions instead of timestamps; gen zero means no positions.
type audioSpan struct {
	from, to         time.Time
	startPos, endPos int64
	gen              uint64
}

// chunkAudio is the per-chunk data the VAD gate and the coverage publisher both
// need, built once per processed chunk.
type chunkAudio struct {
	key        vadStreamKey
	pcm        []byte
	sampleRate int
	// span is the audio the chunk's window covers on the capture timeline, ending
	// at item.Audio.EndAt (set by the producer, backlog corrected), or at the
	// processing time when the producer gave none (see timingUnknown).
	span audioSpan
	// timingUnknown reports that the producer gave no audio end, so span was made
	// up from the processing time: hits are stamped with it (the conservative,
	// later direction) but it claims no coverage.
	timingUnknown bool
	// gap, freshBytes, contiguousBytes, endPos and generation come from the
	// producer (classifier.AudioTiming); generation zero means it reports no
	// stream positions.
	gap             bool
	freshBytes      int
	contiguousBytes int
	endPos          int64
	generation      uint64
}

// speechBandModel reports whether the registered model modelID analyzes audio that
// can carry speech. An unregistered model, like the bat model, does not.
func speechBandModel(modelID string) bool {
	spec, ok := classifier.GetModelSpec(modelID)
	return ok && speechBandSpec(spec)
}

// speechBandSpec reports whether a model's audio can carry speech: the ultrasonic
// bat model is analyzed on raw audio at its own rate and is not.
func speechBandSpec(spec classifier.ModelSpec) bool {
	return spec.RawSampleRate == 0 && spec.EffectiveSampleRate() > 0
}

// newChunkAudio builds the per-chunk data for item, including the producer's
// contiguous tail and stream positions when it reports them, or marked as having
// unknown timing when it gives no audio end. ok is false when the chunk
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
	end, known := audioEndOrNow(item)
	return chunkAudio{
		timingUnknown:   !known,
		key:             vadStreamKey{source: item.Source.ID, model: item.ModelID},
		pcm:             item.PCMdata,
		sampleRate:      rate,
		span:            audioSpan{from: end.Add(-pcmChunkDuration(len(item.PCMdata), rate)), to: end},
		gap:             item.Audio.Gap,
		freshBytes:      item.Audio.FreshBytes,
		contiguousBytes: item.Audio.ContiguousBytes,
		endPos:          item.Audio.EndPos,
		generation:      item.Audio.Generation,
	}, true
}

// hasPositions reports that the producer gave stream positions for the chunk.
func (c *chunkAudio) hasPositions() bool { return c.generation != 0 }

// tailBytes is how many bytes at the end of the chunk are genuine contiguous
// audio: the producer's count, clamped to the chunk, or the whole chunk when the
// producer does not report one.
func (c *chunkAudio) tailBytes() int {
	if c.contiguousBytes <= 0 || c.contiguousBytes > len(c.pcm) {
		return len(c.pcm)
	}
	return c.contiguousBytes
}

// tailStartPos is the stream position where the chunk's contiguous tail starts.
func (c *chunkAudio) tailStartPos() int64 { return c.endPos - int64(c.tailBytes()) }

// audioEndOrNow is the capture time of the last sample of item's window, or the
// current time when the producer gave none (known is false then). A later stamp is
// the conservative direction for a speech hit; coverage must not use it.
func audioEndOrNow(item *classifier.Results) (end time.Time, known bool) {
	if item.Audio.EndAt.IsZero() {
		return time.Now(), false
	}
	return item.Audio.EndAt, true
}

// scoredFrom is where the chunk's genuinely captured audio begins. With stream
// positions it is the start of the contiguous tail, so a zero-filled head or the
// pre-loss audio in an overlap head is never claimed as scanned. Without
// positions, a window after a gap claims nothing before its end.
func (c *chunkAudio) scoredFrom() time.Time {
	if c.hasPositions() {
		return c.span.to.Add(-pcmChunkDuration(c.tailBytes(), c.sampleRate))
	}
	if c.gap {
		return c.span.to
	}
	return c.span.from
}

// coverage is the span this chunk's own scan earned: its contiguous tail, with
// the stream positions when the producer reports them.
func (c *chunkAudio) coverage() audioSpan {
	span := audioSpan{from: c.scoredFrom(), to: c.span.to}
	if c.hasPositions() {
		span.startPos, span.endPos, span.gen = c.tailStartPos(), c.endPos, c.generation
	}
	return span
}

// recordHumanDetection records a speech hit on source at the capture time of the
// end of the audio that produced it. The hit time never moves backward: the
// discard check reads only the latest value, so a later write with an earlier
// stamp (the label path after the VAD within one chunk, or a 5 s model's chunk
// after a 3 s model's) would hide an earlier-reaching hit. An equal time keeps
// the existing entry and its trigger. The hit is recorded without a model or
// score; callers that know them use recordHumanHitLocked.
func (p *Processor) recordHumanDetection(source string, at time.Time, trigger string) {
	p.detectionMutex.Lock()
	defer p.detectionMutex.Unlock()
	p.recordHumanHitLocked(source, humanHit{at: at, trigger: trigger, thresholdMilli: privacyrollup.NoThresholdMilli})
}

// recordHumanHitLocked is recordHumanDetection for a caller that already holds
// detectionMutex for writing, and that knows the model that scored the hit and
// its score. The hit joins the per-source attribution list (every counted hit,
// whichever model produced it, so the discard log can name all contributors)
// and moves LastHumanDetection forward under the rule of recordHumanDetection.
func (p *Processor) recordHumanHitLocked(source string, hit humanHit) {
	p.appendHumanHitLocked(source, hit)
	if existing, ok := p.LastHumanDetection[source]; ok && !hit.at.After(existing.Time) {
		return
	}
	p.LastHumanDetection[source] = HumanDetection{Time: hit.at, Trigger: hit.trigger, ModelID: hit.modelID, Score: hit.score, ThresholdMilli: hit.thresholdMilli}
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
// detection: FirstDetected plus the normal detection window, or for an extended
// capture LastUpdated plus the window, with LastUpdated capped so the end never
// passes MaxDeadline (MaxDuration after FirstDetected) however long the species
// keeps being heard, and the end never below the normal span when MaxDuration is
// shorter than the window. The saved clip ends PreCapture after this end (see
// conf.Settings.DetectionCaptureWindow), so without the cap the privacy coverage
// gate would chase a clip end that moves with every new detection. clipSpan and
// normalizeDetectionTimes both use it, so the span the privacy gate waits for
// cannot drift from the saved clip.
func detectionEndTime(settings *conf.Settings, item *PendingDetection) time.Time {
	window := normalDetectionWindow(settings)
	end := item.FirstDetected.Add(window)
	if !item.ExtendedCapture {
		return end
	}
	last := item.LastUpdated
	if !item.MaxDeadline.IsZero() {
		last = earlierTime(last, item.MaxDeadline.Add(-window))
	}
	return latestTime(end, last.Add(window))
}

// latestTime returns the later of a and b.
func latestTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// earlierTime returns the earlier of a and b.
func earlierTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// coverageUpdate is one span published for a coverage key.
type coverageUpdate struct {
	key  coverageKey
	span audioSpan
}

// publishCoverage records the scanned spans in updates under one detectionMutex
// hold. A span that continues its key's newest recorded span extends it (keeping
// the older start); a later one starts a new span. Spans with stream positions
// continue when they are in the same generation and start at or before the stored
// end position (see mergeCoverageLocked); spans without positions continue when
// they start within vadCoverageJoinTolerance of the stored end time. Spans older
// than privacyCoverageHorizon before the newest end are dropped.
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
// hold detectionMutex and have created the map. When both spans carry stream
// positions they join only if they are in the same generation and the new span
// starts at or before the stored end position: a publish that was skipped, or
// audio lost on the way, leaves a position hole whatever the timestamps say.
// Otherwise a span joins when it starts within vadCoverageJoinTolerance of the
// stored end time.
func (p *Processor) mergeCoverageLocked(key coverageKey, span audioSpan) {
	spans := p.privacyCoverage[key]
	if n := len(spans); n > 0 && spansJoin(&spans[n-1], &span) {
		last := &spans[n-1]
		if span.to.After(last.to) {
			last.to = span.to
		}
		if span.from.Before(last.from) {
			last.from = span.from
		}
		if span.gen != 0 {
			last.endPos = max(last.endPos, span.endPos)
			last.startPos = min(last.startPos, span.startPos)
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

// spansJoin reports whether next continues last without an unscanned hole.
func spansJoin(last, next *audioSpan) bool {
	if last.gen != 0 && next.gen != 0 {
		return last.gen == next.gen && next.startPos <= last.endPos
	}
	return next.from.Compare(last.to.Add(vadCoverageJoinTolerance)) <= 0
}

// publishPrivacyCoverage publishes the coverage a processed chunk earned, in one
// step. Call it after the chunk's VAD hit and label hits are written, so a
// published span never claims audio whose hits are still outstanding; a chunk with
// unknown timing publishes nothing. Label
// coverage claims the chunk's contiguous tail; spans join on stream positions
// when the producer reports them. vad is the
// span runVADGate returned when it ran an inference (vadRan). When the privacy
// filter is on, the label model's own chunk counts as label coverage.
func (p *Processor) publishPrivacyCoverage(settings *conf.Settings, chunk *chunkAudio, vad audioSpan, vadRan bool) {
	if !settings.Realtime.PrivacyFilter.Enabled || chunk.timingUnknown {
		return
	}
	updates := make([]coverageUpdate, 0, 2)
	if vadRan {
		updates = append(updates, coverageUpdate{coverageKey{chunk.key.source, chunk.key.model, metrics.TriggerVAD}, vad})
	}
	updates = append(updates, coverageUpdate{
		coverageKey{chunk.key.source, chunk.key.model, metrics.TriggerLabel},
		chunk.coverage(),
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
	// detector is the detector the coverage verdict was computed for
	// (metrics.TriggerLabel or metrics.TriggerVAD).
	detector string
	// contributions lists who produced the counted hits that reach the saved clip
	// (see hitContributionsLocked). Filled only with privacyfilter.debug on and a
	// latest hit that reaches the clip, for the discard log.
	contributions []hitContribution
}

// checkPrivacy takes the privacyCheck for a pending detection at now. With the
// privacy filter off it reads nothing and the verdict is coverageKeep.
//
// The verdict defers until a speech detector has scored the whole saved clip span
// (padded by privacyClipGuard), so speech in the clip's post-capture tail is not
// scored after the decision. The VAD path is satisfied by any one model's stream.
// The label path needs one current model that scanned the whole padded clip and
// every current model to have scanned the part of it that model could have heard
// (see labelCoverageCoversLocked), since each model's windows are scored
// separately. A speech-band detection on a source no detector has recently scored
// waits too, and reports coverageTimedOut once now passes the clip
// end by privacyCoverageMaxWait. A detection by a model that cannot carry speech
// (the bat model) or an unregistered one keeps at once only when no detector has
// recently scored its source; otherwise it waits for coverage like any other.
// With privacyfilter.debug on and a latest hit that reaches the clip, the check
// also lists who produced the counted hits that reach it, for the discard log.
func (p *Processor) checkPrivacy(settings *conf.Settings, item *PendingDetection, now time.Time) privacyCheck {
	clip := clipSpan(settings, item)
	if !settings.Realtime.PrivacyFilter.Enabled {
		return privacyCheck{clip: clip, verdict: coverageKeep}
	}

	p.detectionMutex.RLock()
	defer p.detectionMutex.RUnlock()
	hit, hasHit := p.LastHumanDetection[item.Source]
	detector := p.requiredDetectorLocked(settings, item.Source, now)
	check := privacyCheck{clip: clip, hit: hit, hasHit: hasHit, detector: detector,
		verdict: p.coverageVerdictLocked(settings, detector, item.Source, item.BestModelID, clip, now)}
	// Only a hit that reaches the clip leads to a discard, so only then is the
	// contributor list of any use.
	reachFrom := clip.from.Add(-privacyClipGuard)
	if hasHit && settings.Realtime.PrivacyFilter.Debug && !hit.Time.Before(reachFrom) {
		check.contributions = p.hitContributionsLocked(item.Source, reachFrom)
	}
	return check
}

// coverageVerdictLocked is the coverage half of checkPrivacy; detector is the
// required detector of the source (requiredDetectorLocked) and model the model
// of the pending detection. With the VAD as the required detector, one stream
// covering the padded clip keeps it; with the label detector one current model
// must have covered all of it and every current model its own share
// (labelCoverageCoversLocked). Otherwise the verdict defers
// until the clip end plus privacyCoverageMaxWait, then reports a timeout. The
// caller must hold detectionMutex.
func (p *Processor) coverageVerdictLocked(settings *conf.Settings, detector, source, model string, clip audioSpan, now time.Time) coverageVerdict {
	if !p.hasRecentCoverageLocked(source, detector, now) && !speechBandModel(model) {
		// No detector has recently scored the source, and the detection comes from
		// a model whose audio cannot carry speech (the ultrasonic bat model) or
		// that is not registered (consistent with newChunkAudio): nothing to wait
		// for. A speech-band detection instead falls through and waits, so a
		// source that was just switched on, a VAD toggle or startup fails open
		// through the timeout, which logs and counts it.
		return coverageKeep
	}
	padded := audioSpan{from: clip.from.Add(-privacyClipGuard), to: clip.to.Add(privacyClipGuard)}
	var covered bool
	if detector == metrics.TriggerLabel {
		covered = p.labelCoverageCoversLocked(source, padded, now)
	} else {
		covered = p.anyStreamCoversLocked(source, detector, padded)
	}
	if covered {
		return coverageKeep
	}
	if now.Before(clip.to.Add(privacyCoverageMaxWait)) {
		return coverageDefer
	}
	return coverageTimedOut
}

// anyStreamCoversLocked reports whether one published span of any model's stream
// for source and detector contains padded. The caller must hold detectionMutex.
func (p *Processor) anyStreamCoversLocked(source, detector string, padded audioSpan) bool {
	for key, spans := range p.privacyCoverage {
		if key.source != source || key.detector != detector {
			continue
		}
		for _, s := range spans {
			if spanContains(s, padded) {
				return true
			}
		}
	}
	return false
}

// labelCoverageCoversLocked reports whether the label models currently scoring
// source have scanned padded. A model is current when its newest span ends within
// privacyCoverageHorizon of now. Two things must hold. At least one current model
// has a single span containing all of padded: a clip head that no model ever
// scanned (the filter was just switched on, coverage restarted after a stall or
// lost spans) is never counted as scanned because the only scanner began later.
// And every current model covers [max(padded.from, start of its earliest retained
// span), padded.to] with one span: a model that began mid-clip owes only the part
// from its start, one that began after the clip end owes nothing, and one that has
// not yet reached the clip leaves it deferred. The caller must hold
// detectionMutex.
func (p *Processor) labelCoverageCoversLocked(source string, padded audioSpan, now time.Time) bool {
	owed, whole := false, false
	for key, spans := range p.privacyCoverage {
		if key.source != source || key.detector != metrics.TriggerLabel || len(spans) == 0 {
			continue
		}
		if now.Sub(spans[len(spans)-1].to) > privacyCoverageHorizon {
			continue
		}
		need := audioSpan{from: padded.from, to: padded.to}
		if runStart := spans[0].from; runStart.After(need.from) {
			need.from = runStart
		}
		if !need.to.After(need.from) {
			continue
		}
		owed = true
		covered := false
		for _, s := range spans {
			if spanContains(s, padded) {
				whole = true
			}
			if spanContains(s, need) {
				covered = true
			}
		}
		if !covered {
			return false
		}
	}
	return owed && whole
}

// spanContains reports whether outer covers all of inner by time.
func spanContains(outer, inner audioSpan) bool {
	return !outer.from.After(inner.from) && !outer.to.Before(inner.to)
}

const (
	// humanHitMaxPreCapture is the longest pre-capture the export settings allow:
	// half the longest capture length of 60 s (see the audio export validation).
	humanHitMaxPreCapture = 30 * time.Second

	// humanHitRetention is how long counted hits stay in the attribution list,
	// measured back from the hit being appended. It covers the longest saved clip:
	// the extended capture limit and the longest pre-capture, with the coverage
	// wait and the clip guard around them, so a hit at the start of such a clip is
	// still listed when its detection is decided.
	humanHitRetention = time.Duration(conf.MaxExtendedCaptureDuration)*time.Second +
		humanHitMaxPreCapture + privacyCoverageMaxWait + privacyClipGuard

	// humanHitMaxPerSource bounds the attribution list of one source (about 0.6 MB),
	// so a long stretch of continuous speech cannot grow it without limit. Once it
	// is reached the oldest entries by arrival order go first, and contributor
	// counts are a lower bound.
	humanHitMaxPerSource = 8192

	// vadHitModelID names the VAD gate in hit attribution, next to classifier
	// model IDs.
	vadHitModelID = "silero_vad"
)

// humanHit is the one carrier of a speech hit: the audio end it was stamped
// with, what produced it (trigger and model), its score and the threshold it was
// scored against in thousandths. It serves the attribution list, held label hits
// and the reporting of hit decisions. thresholdMilli is kept so a later
// confirmation or drop reports the threshold of the hit and not the one in
// effect when it resolves.
type humanHit struct {
	at             time.Time
	trigger        string
	modelID        string // model that scored the hit, empty when unknown
	score          float64
	thresholdMilli int
}

// labelHumanHit is a human label hit scored by modelID with confidence, against
// the privacy filter confidence threshold thresholdMilli (in thousandths).
func labelHumanHit(at time.Time, modelID string, confidence float32, thresholdMilli int) humanHit {
	return humanHit{at: at, trigger: metrics.TriggerLabel, modelID: modelID, score: float64(confidence), thresholdMilli: thresholdMilli}
}

// vadHumanHit is a VAD speech hit with its speech probability, attributed to
// vadHitModelID, against the VAD threshold thresholdMilli (in thousandths).
func vadHumanHit(at time.Time, probability float64, thresholdMilli int) humanHit {
	return humanHit{at: at, trigger: metrics.TriggerVAD, modelID: vadHitModelID, score: probability, thresholdMilli: thresholdMilli}
}

// appendHumanHitLocked adds hit to the attribution list of source and drops
// entries older than humanHitRetention before it or beyond humanHitMaxPerSource.
// The caller holds detectionMutex for writing.
func (p *Processor) appendHumanHitLocked(source string, hit humanHit) {
	if p.humanHits == nil {
		p.humanHits = make(map[string][]humanHit)
	}
	cutoff := hit.at.Add(-humanHitRetention)
	kept := slices.DeleteFunc(p.humanHits[source], func(h humanHit) bool { return h.at.Before(cutoff) })
	kept = append(kept, hit)
	if over := len(kept) - humanHitMaxPerSource; over > 0 {
		kept = slices.Delete(kept, 0, over)
	}
	p.humanHits[source] = kept
}

// hitContribution totals the counted hits one detector (a classifier model with
// the label trigger, or the VAD gate) contributed to a window.
type hitContribution struct {
	trigger  string
	modelID  string
	count    int
	maxScore float64
}

// hitContributionsLocked groups the counted hits of source stamped at or after
// since by detector, most hits first. The caller holds detectionMutex.
func (p *Processor) hitContributionsLocked(source string, since time.Time) []hitContribution {
	var out []hitContribution
	for _, h := range p.humanHits[source] {
		if h.at.Before(since) {
			continue
		}
		found := false
		for i := range out {
			if out[i].trigger == h.trigger && out[i].modelID == h.modelID {
				out[i].count++
				out[i].maxScore = max(out[i].maxScore, h.score)
				found = true
				break
			}
		}
		if !found {
			out = append(out, hitContribution{trigger: h.trigger, modelID: h.modelID, count: 1, maxScore: h.score})
		}
	}
	slices.SortStableFunc(out, func(a, b hitContribution) int { return cmp.Compare(b.count, a.count) })
	return out
}

// formatHitContributions renders contributions for a log field, for example
// "Perch_V2/label x3 max 0.188; silero_vad/vad x2 max 0.810".
func formatHitContributions(cs []hitContribution) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		model := c.modelID
		if model == "" {
			model = "unknown"
		}
		parts = append(parts, fmt.Sprintf("%s/%s x%d max %.3f", model, c.trigger, c.count, c.maxScore))
	}
	return strings.Join(parts, "; ")
}
