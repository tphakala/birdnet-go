package processor

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference/vad"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// Settings shared by the end-to-end privacy tests.
const (
	e2eSource       = "src"
	e2eExportLength = 15
	e2ePreCapture   = 3
	e2eSpecies      = "great tit"
)

// e2eSettings returns the privacy-on, VAD-on settings of the end-to-end tests.
func e2eSettings() *conf.Settings {
	settings := vadSettings(true, 0.35)
	settings.Realtime.Audio.Export.Length = e2eExportLength
	settings.Realtime.Audio.Export.PreCapture = e2ePreCapture
	return settings
}

// speechInterval is a stretch of speech in the synthetic audio timeline, as
// offsets from the rig's base time.
type speechInterval struct{ from, to time.Duration }

// markerStreamer is a vad.Streamer whose decision depends on the audio it was
// given: it reports speech for a Flush when any sample appended since the
// previous Flush is non-zero (the rig marks speech samples non-zero).
type markerStreamer struct{ heard bool }

func (m *markerStreamer) Append(pcm []byte, _ int) error {
	if slices.ContainsFunc(pcm, func(b byte) bool { return b != 0 }) {
		m.heard = true
	}
	return nil
}

func (m *markerStreamer) Flush(_ vad.SpeechSession) (prob float32, ok bool, framesRun int, err error) {
	prob = 0
	if m.heard {
		prob = 0.9
	}
	m.heard = false
	return prob, true, 1, nil
}

func (m *markerStreamer) Reset() { m.heard = false }

// privacyRig drives real processDetections input through a Processor whose VAD
// gate scores a synthetic audio timeline, then flushes pending detections.
type privacyRig struct {
	t        *testing.T
	p        *Processor
	settings *conf.Settings
	base     time.Time
	model    string
	clip     time.Duration
	rate     int
	speech   []speechInterval
}

// newPrivacyRig builds a rig for the 3 s (BirdNET_V2.4, 48 kHz) or 5 s (Perch_V2,
// 32 kHz) model. base is how far in the past the timeline origin lies; the
// real clock is used by flushPendingDetections.
func newPrivacyRig(t *testing.T, model string, ago time.Duration) *privacyRig {
	t.Helper()
	conf.StoreSettings(nil)
	t.Cleanup(func() { conf.StoreSettings(nil) })

	spec, ok := classifier.GetModelSpec(model)
	require.True(t, ok)
	settings := e2eSettings()
	g := newTestGate(nil, &fakeSession{}, nil)
	g.newStreamer = func() vad.Streamer { return &markerStreamer{} }
	return &privacyRig{
		t: t, settings: settings, base: time.Now().Add(-ago), model: model,
		clip: spec.ClipLength, rate: spec.EffectiveSampleRate(),
		p: &Processor{
			Settings:           settings,
			LastHumanDetection: map[string]HumanDetection{},
			LastDogDetection:   map[string]time.Time{},
			pendingDetections:  map[string]PendingDetection{},
			vadGate:            g,
		},
	}
}

func (r *privacyRig) at(off time.Duration) time.Time { return r.base.Add(off) }

// clipBegin is where the clip of a bird first heard in the chunk ending at end
// begins: the chunk start moved back by PreCapture.
func (r *privacyRig) clipBegin(end time.Duration) time.Time {
	return r.at(end).Add(-r.clip - e2ePreCapture*time.Second)
}

// chunk builds the analysis window ending at end, with speech samples marked
// non-zero wherever the timeline has speech.
func (r *privacyRig) chunk(end time.Duration) classifier.Results {
	n := int(r.clip.Seconds()) * r.rate
	pcm := make([]byte, n*vadBytesPerSample)
	start := end - r.clip
	for _, sp := range r.speech {
		lo := max(0, int((sp.from-start).Seconds()*float64(r.rate)))
		hi := min(n, int((sp.to-start).Seconds()*float64(r.rate)))
		for i := lo; i < hi; i++ {
			pcm[i*vadBytesPerSample] = 1
		}
	}
	item := classifier.Results{
		StartTime: r.clipBegin(end),
		Audio:     classifier.AudioTiming{EndAt: r.at(end)},
		PCMdata:   pcm,
		ModelID:   r.model,
	}
	item.Source = datastore.AudioSource{ID: e2eSource}
	return item
}

// bytesFor is the PCM size of d of audio for the rig's model.
func (r *privacyRig) bytesFor(d time.Duration) int {
	return int(d.Seconds()*float64(r.rate)) * vadBytesPerSample
}

// positioned stamps item as a producer that reports stream positions: the window
// ends at the stream position of audio time end (positions advance with audio,
// whether or not the audio was lost on the way) and its last contiguous bytes
// are the audio since contiguousSince.
func (r *privacyRig) positioned(item *classifier.Results, end, contiguous time.Duration) {
	item.Audio.Generation = 1
	item.Audio.EndPos = int64(r.bytesFor(end))
	item.Audio.ContiguousBytes = min(r.bytesFor(contiguous), len(item.PCMdata))
}

// feed processes one chunk per step with audio ends in [from, to].
func (r *privacyRig) feed(from, to, step time.Duration) {
	r.t.Helper()
	for end := from; end <= to; end += step {
		r.p.processDetections(r.chunk(end))
	}
}

// feedWithGapAt feeds one chunk per step over [from, to], except that the chunk
// ending at gapAt arrives flagged as following lost audio.
func (r *privacyRig) feedWithGapAt(from, gapAt, to, step time.Duration) {
	r.t.Helper()
	r.feed(from, gapAt-step, step)
	gapped := r.chunk(gapAt)
	gapped.Audio.Gap = true
	r.p.processDetections(gapped)
	r.feed(gapAt+step, to, step)
}

// pend registers a bird first heard in the chunk ending at end, as production
// does: FirstDetected is the chunk start moved back by PreCapture, and the
// deadline has passed.
func (r *privacyRig) pend(end time.Duration) {
	r.t.Helper()
	r.p.pendingDetections[pendingDetectionKey(e2eSource, e2eSpecies)] = PendingDetection{
		Confidence:    0.9,
		Source:        e2eSource,
		BestModelID:   r.model,
		FirstDetected: r.clipBegin(end),
		FlushDeadline: time.Now().Add(-time.Second),
		Count:         5,
	}
}

// pendModel is pend for a detection made by model.
func (r *privacyRig) pendModel(end time.Duration, model string) {
	r.t.Helper()
	r.pend(end)
	key := pendingDetectionKey(e2eSource, e2eSpecies)
	item := r.p.pendingDetections[key]
	item.BestModelID = model
	r.p.pendingDetections[key] = item
}

// outcome flushes once and reports whether the detection was approved, still
// pending, or discarded.
func (r *privacyRig) outcome() string {
	r.t.Helper()
	_, flushed := r.p.flushPendingDetections()
	switch {
	case flushed == 1:
		return "approved"
	case len(r.p.pendingDetections) == 1:
		return "pending"
	default:
		return "discarded"
	}
}

func sec(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

type e2eGeometry struct {
	name  string
	model string
	step  time.Duration
}

// e2eGeometries are the primary cases: the 3 s and 5 s models at no overlap and
// at the false positive filter levels' high-overlap steps.
var e2eGeometries = []e2eGeometry{
	{"3s step 3000ms", "BirdNET_V2.4", sec(3)},
	{"3s step 1000ms", "BirdNET_V2.4", sec(1)},
	{"3s step 200ms", "BirdNET_V2.4", sec(0.2)},
	{"5s step 5000ms", "Perch_V2", sec(5)},
	{"5s step 1667ms", "Perch_V2", 1667 * time.Millisecond},
	{"5s step 333ms", "Perch_V2", 333 * time.Millisecond},
}

// TestPrivacy_PreCaptureSpeechDiscards: speech that sits in the clip's pre-capture
// region (before the detecting chunk) is caught at every geometry, and a clip
// without speech nearby is approved.
func TestPrivacy_PreCaptureSpeechDiscards(t *testing.T) {
	for _, geo := range e2eGeometries {
		for _, tc := range []struct {
			name      string
			hasSpeech bool
			want      string
		}{
			// The detecting chunk ends at 40 s; its clip begins PreCapture before the
			// chunk start. The speech case has 0.5 s of speech ending 1.2 s before the
			// detecting chunk begins: inside the pre-capture part of the clip and
			// farther back than the guard, so a comparison against the chunk start
			// instead of the clip begin would miss it. With zero overlap the chunk
			// before the detecting one ends exactly at the chunk start, so for the
			// 5 s model at step 5000 ms, and likewise at step 1667 ms, this case cannot
			// tell the two apart;
			// TestPrivacy_GuardMargins pins the comparison point itself.
			{"speech in the pre-capture region", true, "discarded"},
			{"no speech", false, "approved"},
		} {
			t.Run(geo.name+"/"+tc.name, func(t *testing.T) {
				r := newPrivacyRig(t, geo.model, 45*time.Second)
				chunkStart := 40*time.Second - r.clip
				if tc.hasSpeech {
					r.speech = []speechInterval{{chunkStart - sec(1.7), chunkStart - sec(1.2)}}
				}
				// Chunks up to well past the clip end (detecting end + 9 s for the
				// 3 s model, +7 s for the 5 s model).
				r.feed(r.clip, 40*time.Second+sec(11), geo.step)
				r.pend(40 * time.Second)
				assert.Equal(t, tc.want, r.outcome())
			})
		}
	}
}

// TestPrivacy_OverlapHeadSpeechDiscards: speech in the head of the detecting chunk
// that an earlier, throttled flush scored (the 5 s overlap-head case) still
// discards the detection.
func TestPrivacy_OverlapHeadSpeechDiscards(t *testing.T) {
	for _, geo := range e2eGeometries {
		t.Run(geo.name, func(t *testing.T) {
			r := newPrivacyRig(t, geo.model, 45*time.Second)
			chunkStart := 40*time.Second - r.clip
			r.speech = []speechInterval{{chunkStart + sec(0.2), chunkStart + sec(0.7)}}
			r.feed(r.clip, 40*time.Second+sec(11), geo.step)
			r.pend(40 * time.Second)
			assert.Equal(t, "discarded", r.outcome())
		})
	}
}

// TestPrivacy_PostCaptureSpeechDefersThenDiscards: the first tick after the
// deadline finds the clip tail not yet scored and leaves the detection pending;
// once the tail is scored and holds speech, the next tick discards it.
func TestPrivacy_PostCaptureSpeechDefersThenDiscards(t *testing.T) {
	for _, geo := range e2eGeometries {
		t.Run(geo.name, func(t *testing.T) {
			r := newPrivacyRig(t, geo.model, 45*time.Second)
			clip := clipSpan(r.settings, &PendingDetection{FirstDetected: r.clipBegin(40 * time.Second)})
			// Speech 0.3 s before the clip's end.
			endOff := clip.to.Sub(r.base)
			r.speech = []speechInterval{{endOff - sec(0.8), endOff - sec(0.3)}}

			r.feed(r.clip, endOff-2*time.Second, geo.step)
			r.pend(40 * time.Second)
			require.Equal(t, "pending", r.outcome(), "the clip tail is not scanned yet")

			r.feed(endOff-2*time.Second+geo.step, endOff+3*time.Second, geo.step)
			assert.Equal(t, "discarded", r.outcome())
		})
	}
}

// TestPrivacy_ExtendedCaptureWaitsForClipEnd: an extended capture's clip reaches
// LastUpdated + window, so it stays pending until coverage reaches that end.
func TestPrivacy_ExtendedCaptureWaitsForClipEnd(t *testing.T) {
	r := newPrivacyRig(t, "BirdNET_V2.4", 60*time.Second)
	r.pend(40 * time.Second)
	key := pendingDetectionKey(e2eSource, e2eSpecies)
	item := r.p.pendingDetections[key]
	item.ExtendedCapture = true
	item.LastUpdated = r.at(46 * time.Second)
	r.p.pendingDetections[key] = item
	endOff := clipSpan(r.settings, &item).to.Sub(r.base)
	require.Greater(t, endOff, 50*time.Second, "an extended clip reaches past the normal one")

	r.feed(r.clip, endOff-sec(2), sec(1))
	require.Equal(t, "pending", r.outcome())
	r.feed(endOff-sec(1), endOff+sec(3), sec(1))
	assert.Equal(t, "approved", r.outcome())
}

// TestPrivacy_CoverageGapTimesOut: when a producer gap breaks coverage inside the
// clip, the detection waits, then is approved unscanned after the wait with a
// log line and a metric (the filter fails open).
func TestPrivacy_CoverageGapTimesOut(t *testing.T) {
	r := newPrivacyRig(t, "BirdNET_V2.4", 100*time.Second)
	pfm, err := metrics.NewPrivacyFilterMetrics(prometheus.NewRegistry())
	require.NoError(t, err)
	r.p.Metrics = &observability.Metrics{PrivacyFilter: pfm}
	r.settings.Realtime.Telemetry.Enabled = true

	r.feedWithGapAt(r.clip, sec(40), sec(55), sec(1))
	r.pend(40 * time.Second)

	assert.Equal(t, "approved", r.outcome(), "the clip end is more than the max wait in the past")
	expected := "\n# HELP privacy_filter_unscanned_saves_total Total detections approved for saving whose clip span was not fully scanned for speech before the coverage wait expired\n" +
		"# TYPE privacy_filter_unscanned_saves_total counter\nprivacy_filter_unscanned_saves_total 1\n"
	require.NoError(t, testutil.CollectAndCompare(pfm, strings.NewReader(expected), "privacy_filter_unscanned_saves_total"))
}

// TestPrivacy_CoverageGapDefersWithinWait: the same broken coverage still defers
// while the wait has not expired.
func TestPrivacy_CoverageGapDefersWithinWait(t *testing.T) {
	r := newPrivacyRig(t, "BirdNET_V2.4", 55*time.Second)
	r.feedWithGapAt(r.clip, sec(40), sec(54), sec(1))
	r.pend(40 * time.Second)
	assert.Equal(t, "pending", r.outcome())
}

// TestPrivacy_NotApplicableKeeps: a detection by a model that cannot carry speech
// on a source no detector scored is approved without waiting, a VAD that never
// loaded falls back to the label coverage, and label-only users are gated too.
func TestPrivacy_NotApplicableKeeps(t *testing.T) {
	t.Run("bat detection on a source no detector scored", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
		bat := r.chunk(sec(30)) // a bat window is analyzed too, and earns no coverage
		bat.ModelID = classifier.RegistryIDBat
		r.p.processDetections(bat)
		r.pendModel(40*time.Second, classifier.RegistryIDBat)
		assert.Equal(t, "approved", r.outcome())
	})
	t.Run("unregistered model spec", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
		r.pendModel(40*time.Second, "no-such-model")
		assert.Equal(t, "approved", r.outcome())
	})
	t.Run("VAD enabled but never loaded falls back to label coverage", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
		r.p.vadGate.newSession = sessionFactory(nil, errors.NewStd("no model"), nil)
		r.feed(r.clip, sec(36), sec(1))
		r.pend(40 * time.Second)
		require.Equal(t, "pending", r.outcome(), "label coverage has not reached the clip end")
		r.feed(sec(37), sec(52), sec(1))
		assert.Equal(t, "approved", r.outcome())
	})
	t.Run("label-only users are gated too", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
		r.settings.Realtime.PrivacyFilter.VAD.Enabled = false
		r.feed(r.clip, sec(36), sec(1))
		r.pend(40 * time.Second)
		require.Equal(t, "pending", r.outcome())
		r.feed(sec(37), sec(52), sec(1))
		assert.Equal(t, "approved", r.outcome())
	})
}

// TestPrivacy_GuardMargins pins the guard on both sides of a clip, at its exact
// edge and with literal offsets that bound the constant between 400 and 600 ms.
func TestPrivacy_GuardMargins(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	settings := e2eSettings()
	item := &PendingDetection{Source: e2eSource, FirstDetected: base, Count: 5}
	clip := clipSpan(settings, item)
	now := clip.to.Add(time.Second)

	t.Run("speech hit against the clip begin", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name string
			hit  time.Time
			want bool
		}{
			{"at begin minus guard", base.Add(-privacyClipGuard), true},
			{"one ns earlier", base.Add(-privacyClipGuard - 1), false},
			{"400 ms before", base.Add(-400 * time.Millisecond), true},
			{"600 ms before", base.Add(-600 * time.Millisecond), false},
		} {
			p := &Processor{LastHumanDetection: map[string]HumanDetection{e2eSource: {Time: tc.hit}}}
			got, _ := discardNow(p, item, settings, 1)
			assert.Equal(t, tc.want, got, tc.name)
		}
	})
	t.Run("coverage against the clip span", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name string
			from time.Time
			to   time.Time
			want coverageVerdict
		}{
			{"covers exactly with guards", base.Add(-privacyClipGuard), clip.to.Add(privacyClipGuard), coverageKeep},
			{"end short by 1 ns", base.Add(-privacyClipGuard), clip.to.Add(privacyClipGuard - 1), coverageDefer},
			{"start late by 1 ns", base.Add(-privacyClipGuard + 1), clip.to.Add(privacyClipGuard), coverageDefer},
			{"end 400 ms past the clip", base.Add(-time.Minute), clip.to.Add(400 * time.Millisecond), coverageDefer},
			{"end 600 ms past the clip", base.Add(-time.Minute), clip.to.Add(600 * time.Millisecond), coverageKeep},
		} {
			p := &Processor{privacyCoverage: map[coverageKey][]audioSpan{
				{e2eSource, "m", metrics.TriggerVAD}: {{from: tc.from, to: tc.to}},
			}}
			p.vadGate = newTestGate(nil, &fakeSession{}, nil)
			p.vadGate.snapshot.Store(&vadConfigSnapshot{})
			assert.Equal(t, tc.want, p.checkPrivacy(settings, item, now).verdict, tc.name)
		}
	})
}

// TestClipSpan_MatchesExporter: clipSpan equals the BeginTime and the begin plus
// the capture length the exporter derives from the EndTime that
// normalizeDetectionTimes stores.
func TestClipSpan_MatchesExporter(t *testing.T) {
	conf.StoreSettings(nil)
	t.Cleanup(func() { conf.StoreSettings(nil) })
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		extended bool
		updated  time.Duration
		capBuf   int
		maxDur   time.Duration // MaxDeadline offset from the first detection; zero for none
	}{
		{"normal", false, 0, 0, 0},
		{"extended", true, 40 * time.Second, 0, 0},
		{"extended capped at the buffer", true, 90 * time.Second, 30, 0},
		{"extended capped at MaxDeadline", true, 90 * time.Second, 0, 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &conf.Settings{}
			settings.Realtime.Audio.Export.Length = e2eExportLength
			settings.Realtime.Audio.Export.PreCapture = e2ePreCapture
			if tt.capBuf > 0 {
				settings.Realtime.ExtendedCapture.Enabled = true
				settings.Realtime.ExtendedCapture.CaptureBufferSeconds = tt.capBuf
			}
			p := &Processor{Settings: settings}
			item := &PendingDetection{FirstDetected: base, LastUpdated: base.Add(tt.updated), ExtendedCapture: tt.extended}
			if tt.maxDur > 0 {
				item.MaxDeadline = base.Add(tt.maxDur)
			}

			clip := clipSpan(settings, item)
			p.normalizeDetectionTimes(item, p.currentSettings())
			win, _ := settings.DetectionCaptureWindow(item.Detection.Result.BeginTime, item.Detection.Result.EndTime)

			assert.True(t, clip.from.Equal(item.Detection.Result.BeginTime))
			assert.True(t, clip.to.Equal(item.Detection.Result.BeginTime.Add(time.Duration(win.Length)*time.Second)))
			if tt.capBuf > 0 {
				assert.True(t, win.Capped, "the capped case must actually be capped")
			}
			if tt.maxDur > 0 {
				assert.True(t, item.Detection.Result.EndTime.Equal(item.MaxDeadline), "the saved EndTime is capped at MaxDeadline like the privacy span")
			}
		})
	}
}

// TestPrivacy_CheckPrivacyReadsHitAndCoverageAtomically: a chunk writes its
// speech hit and then publishes the coverage that includes it. A check that sees
// coverage reaching the end of a clip must therefore see the hit of that same
// chunk; reading the hit and the coverage in separate critical sections lets a
// chunk land in between and approve a clip that holds its speech.
func TestPrivacy_CheckPrivacyReadsHitAndCoverageAtomically(t *testing.T) {
	t.Parallel()
	const iterations = 100000
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	at := func(i int64) time.Time { return base.Add(time.Duration(i) * time.Second) }
	settings := e2eSettings()
	settings.Realtime.PrivacyFilter.VAD.Enabled = false // label coverage is the required detector
	key := coverageKey{e2eSource, "m", metrics.TriggerLabel}
	p := &Processor{LastHumanDetection: map[string]HumanDetection{}}

	write := func(i int64) {
		p.recordHumanDetection(e2eSource, at(i), metrics.TriggerLabel)
		p.publishCoverage(coverageUpdate{key, audioSpan{from: base.Add(-time.Hour), to: at(i)}})
	}
	write(0)

	var done atomic.Int64 // chunks fully written (hit and coverage)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := int64(1); i <= iterations; i++ {
			write(i)
			done.Store(i)
		}
	}()
	// The writer ends on its own; waiting for it keeps it from outliving the test
	// when an assertion fails and the loop below returns early.
	t.Cleanup(func() { <-finished })

	for {
		select {
		case <-finished:
			return
		default:
		}
		next := done.Load() + 1
		// A clip that coverage reaches exactly when chunk next is published.
		item := &PendingDetection{Source: e2eSource}
		item.FirstDetected = at(next).Add(-privacyClipGuard - e2eExportLength*time.Second)
		check := p.checkPrivacy(settings, item, at(next))
		if check.verdict == coverageKeep {
			if !assert.True(t, check.hasHit) ||
				!assert.False(t, check.hit.Time.Before(at(next)),
					"coverage through chunk %d was seen with only the hit of an earlier chunk", next) {
				return
			}
		}
	}
}

// TestPrivacy_BatChunksPublishNoLabelCoverage: an ultrasonic bat chunk is no
// evidence the label path heard the speech band, so it earns no coverage and a
// bat-only source never waits on it.
func TestPrivacy_BatChunksPublishNoLabelCoverage(t *testing.T) {
	r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
	bat := r.chunk(sec(30))
	bat.ModelID = classifier.RegistryIDBat
	r.p.processDetections(bat)
	assert.Empty(t, r.p.privacyCoverage, "bat chunks publish no coverage")

	r.pendModel(40*time.Second, classifier.RegistryIDBat)
	assert.Equal(t, "approved", r.outcome())
}

// TestPrivacy_LabelCoverageAfterOverwriteLossClaimsOnlyTheContiguousTail: after the
// producer lost two seconds of audio, the windows that follow still carry the
// pre-loss audio in their overlap head. Label coverage may claim only their
// contiguous tail, so a clip that includes the lost stretch is never reported as
// scanned.
func TestPrivacy_LabelCoverageAfterOverwriteLossClaimsOnlyTheContiguousTail(t *testing.T) {
	for _, vadEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("vad=%v", vadEnabled), func(t *testing.T) {
			lossLeavesClipUnscanned(t, vadEnabled)
		})
	}
}

// TestPrivacy_NewBufferGenerationNeverJoinsEarlierCoverage: a reallocated analysis
// buffer starts a new generation whose positions mean nothing next to the old
// ones, so its coverage does not join the spans published before, however the
// timestamps line up.
func TestPrivacy_NewBufferGenerationNeverJoinsEarlierCoverage(t *testing.T) {
	for _, vadEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("vad=%v", vadEnabled), func(t *testing.T) {
			r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
			r.settings.Realtime.PrivacyFilter.VAD.Enabled = vadEnabled
			ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
			firstGen := newBufferGeneration(t)
			for end := 3000; end <= 20000; end += 200 {
				item := r.chunk(ms(end))
				r.positioned(&item, ms(end), r.clip)
				item.Audio.Generation = firstGen
				r.p.processDetections(item)
			}
			// The buffer was reallocated: positions restart in a new generation.
			secondGen := newBufferGeneration(t)
			require.NotEqual(t, firstGen, secondGen)
			for end := 20200; end <= 36000; end += 200 {
				item := r.chunk(ms(end))
				item.Audio.Gap = end == 20200
				r.positioned(&item, ms(end-20000), min(ms(end-20000), r.clip))
				item.Audio.Generation = secondGen
				r.p.processDetections(item)
			}
			r.pend(24 * time.Second) // clip 18 s to 33 s spans both generations
			assert.Equal(t, "pending", r.outcome())
		})
	}
}

// newBufferGeneration is the generation a freshly built analysis buffer reports on
// its first window.
func newBufferGeneration(t *testing.T) uint64 {
	t.Helper()
	const readSize = 8
	ab, err := buffer.NewAnalysisBuffer(64, 0, readSize, e2eSource, logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC), nil)
	require.NoError(t, err)
	require.NoError(t, ab.Write(make([]byte, readSize)))
	_, info, release, err := ab.ReadWindow()
	require.NoError(t, err)
	release()
	return info.Generation
}

// lossLeavesClipUnscanned feeds a producer that lost 2 s of audio and checks that a
// clip across the loss stays pending.
func lossLeavesClipUnscanned(t *testing.T, vadEnabled bool) {
	t.Helper()
	r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
	r.settings.Realtime.PrivacyFilter.VAD.Enabled = vadEnabled
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }

	for end := 3000; end <= 20000; end += 200 {
		item := r.chunk(ms(end))
		r.positioned(&item, ms(end), r.clip)
		r.p.processDetections(item)
	}
	// Audio between 20.0 s and 22.0 s was overwritten unread. The first window
	// after it is a gap whose only genuine audio is its fresh 0.2 s; each later
	// one gains another step of genuine tail until the whole window is genuine.
	for end := 22200; end <= 36000; end += 200 {
		item := r.chunk(ms(end))
		item.Audio.Gap = end == 22200
		r.positioned(&item, ms(end), min(ms(end-22000), r.clip))
		r.p.processDetections(item)
	}
	r.pend(24 * time.Second) // clip 18 s to 33 s, across the lost stretch

	assert.Equal(t, "pending", r.outcome(), "the lost stretch was never scanned")
}

// TestPrivacy_ZeroOverlapJitterOver150msJoinsOnContiguousPositions: at zero overlap
// consecutive windows abut. When arrival jitter makes their audio-end stamps differ
// by more than the time tolerance but the producer reports contiguous stream
// positions, the spans still join and a fully scanned clip is released.
func TestPrivacy_ZeroOverlapJitterOver150msJoinsOnContiguousPositions(t *testing.T) {
	for _, vadEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("vad=%v", vadEnabled), func(t *testing.T) {
			zeroOverlapJitterJoins(t, vadEnabled)
		})
	}
}

func zeroOverlapJitterJoins(t *testing.T, vadEnabled bool) {
	t.Helper()
	r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
	r.settings.Realtime.PrivacyFilter.VAD.Enabled = vadEnabled

	for k := 1; k <= 14; k++ {
		end := time.Duration(3*k) * time.Second
		item := r.chunk(end)
		if k%2 == 1 { // odd windows are stamped 300 ms late
			item.Audio.EndAt = item.Audio.EndAt.Add(300 * time.Millisecond)
		}
		r.positioned(&item, end, r.clip)
		r.p.processDetections(item)
	}
	r.pend(21 * time.Second) // clip 15 s to 30 s

	assert.Equal(t, "approved", r.outcome(), "contiguous positions join the spans despite the jitter")
}

// TestProcessApprovedDetection_UsesThePassedSettingsSnapshot: the saved clip must
// be built from the snapshot the privacy check used, not from whatever the
// processor's configuration has become since.
func TestProcessApprovedDetection_UsesThePassedSettingsSnapshot(t *testing.T) {
	conf.StoreSettings(nil)
	t.Cleanup(func() { conf.StoreSettings(nil) })
	checked := e2eSettings() // export length 15 s, pre-capture 3 s
	reloaded := e2eSettings()
	reloaded.Realtime.Audio.Export.Length = 60
	p := &Processor{Settings: reloaded}

	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	item := &PendingDetection{FirstDetected: base, Source: e2eSource}
	p.processApprovedDetection(item, e2eSpecies, checked)

	assert.True(t, item.Detection.Result.EndTime.Equal(base.Add(12*time.Second)),
		"EndTime must follow the checked snapshot (15 s - 3 s), not the reloaded one")
}

// TestBuildSaveAudioAction_UsesThePassedSettingsSnapshot: the audio export length
// and settings come from the snapshot the privacy check used, not from the
// processor's current configuration.
func TestBuildSaveAudioAction_UsesThePassedSettingsSnapshot(t *testing.T) {
	conf.StoreSettings(nil)
	t.Cleanup(func() { conf.StoreSettings(nil) })
	checked := e2eSettings() // export length 15 s
	reloaded := e2eSettings()
	reloaded.Realtime.Audio.Export.Length = 60
	// A capture window that ends in the future takes the deferred read path, which
	// records the export length in the action.
	p := &Processor{Settings: reloaded, BufferMgr: buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))}

	base := time.Now()
	det := &Detections{Result: detection.Result{BeginTime: base, EndTime: base.Add(12 * time.Second)}}
	action := p.buildSaveAudioAction(det, &DetectionContext{}, checked)

	assert.Same(t, checked, action.Settings)
	assert.Equal(t, 15, action.duration, "the exported length follows the checked snapshot")
}

// TestPrivacy_NoRecentCoverageDefersThenFailsOpen: when no detector has recently
// scored the source (the filter was just switched on, a VAD toggle, startup) a
// speech-band detection waits instead of being approved unscanned at once, and
// is approved, logged and counted once the wait expires.
func TestPrivacy_NoRecentCoverageDefersThenFailsOpen(t *testing.T) {
	t.Run("defers within the wait", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
		r.pend(40 * time.Second) // clip 34 s to 49 s, wait until 79 s
		assert.Equal(t, "pending", r.outcome())
	})
	t.Run("stale coverage older than the horizon is no coverage", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 170*time.Second)
		r.feed(r.clip, 40*time.Second, sec(1)) // 130 s old at the flush, past the 120 s horizon
		r.pend(135 * time.Second)              // clip 129 s to 144 s, wait until 174 s
		assert.Equal(t, "pending", r.outcome())
	})
	t.Run("approved and counted after the wait", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 100*time.Second)
		pfm, err := metrics.NewPrivacyFilterMetrics(prometheus.NewRegistry())
		require.NoError(t, err)
		r.p.Metrics = &observability.Metrics{PrivacyFilter: pfm}
		r.settings.Realtime.Telemetry.Enabled = true
		r.pend(40 * time.Second)
		assert.Equal(t, "approved", r.outcome())
		expected := "\n# HELP privacy_filter_unscanned_saves_total Total detections approved for saving whose clip span was not fully scanned for speech before the coverage wait expired\n" +
			"# TYPE privacy_filter_unscanned_saves_total counter\nprivacy_filter_unscanned_saves_total 1\n"
		require.NoError(t, testutil.CollectAndCompare(pfm, strings.NewReader(expected), "privacy_filter_unscanned_saves_total"))
	})
}

// TestPrivacy_ZeroAudioEndStampsHitsAtNowAndPublishesNoCoverage: a window whose
// audio end is unknown (a producer that reports no timing) must neither lose its
// speech hits at time zero nor claim coverage for a made-up span. Hits are
// stamped at processing time, the conservative later direction, and nothing is
// published as scanned.
func TestPrivacy_ZeroAudioEndStampsHitsAtNowAndPublishesNoCoverage(t *testing.T) {
	r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
	before := time.Now()

	// VAD path: the window holds speech throughout.
	r.speech = []speechInterval{{0, time.Hour}}
	for end := 3; end <= 6; end++ {
		item := r.chunk(time.Duration(end) * time.Second)
		item.Audio = classifier.AudioTiming{}
		r.p.processDetections(item)
	}
	hit, ok := r.p.LastHumanDetection[e2eSource]
	require.True(t, ok, "speech in a window with unknown timing must still be recorded")
	assert.False(t, hit.Time.Before(before), "the hit is stamped at processing time, not at time zero")
	assert.Empty(t, r.p.privacyCoverage, "a window with unknown timing publishes no coverage")

	// Label path.
	r2 := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
	r2.settings.Realtime.PrivacyFilter.Confidence = 0.05
	label := classifier.Results{}
	label.Source = datastore.AudioSource{ID: e2eSource}
	r2.p.handleHumanDetection(r2.settings, label, datastoreResult("Speech"))
	labelHit, ok := r2.p.LastHumanDetection[e2eSource]
	require.True(t, ok)
	assert.False(t, labelHit.Time.Before(before))
}

func datastoreResult(species string) datastore.Results {
	return datastore.Results{Species: species, Confidence: 0.9}
}

// TestPrivacy_SwitchingTheFilterOffReleasesDeferredDetections: a hot reload that
// turns the privacy filter off must release detections already waiting for
// coverage, even when coverage was published while the filter was on.
func TestPrivacy_SwitchingTheFilterOffReleasesDeferredDetections(t *testing.T) {
	r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
	r.feed(r.clip, 36*time.Second, sec(1))
	r.pend(40 * time.Second) // clip 34 s to 49 s, coverage not there yet
	require.Equal(t, "pending", r.outcome())

	r.settings.Realtime.PrivacyFilter.Enabled = false
	assert.Equal(t, "approved", r.outcome())
}

// TestPrivacy_VADHitsAreWrittenBeforeTheirCoverageIsPublished: processDetections
// writes a chunk's speech hit and only then publishes the coverage that includes
// it, so a flusher that sees the coverage always sees the hit. The writer is the
// real processDetections with speech in every chunk; the reader checks the
// invariant continuously. (The label hits of the same chunk are written by
// processResults, which needs a loaded orchestrator and is not reachable here.)
func TestPrivacy_VADHitsAreWrittenBeforeTheirCoverageIsPublished(t *testing.T) {
	r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
	r.speech = []speechInterval{{0, time.Hour}}
	const chunks = 400

	var done atomic.Int64
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := int64(1); i <= chunks; i++ {
			r.p.processDetections(r.chunk(time.Duration(i) * time.Second))
			done.Store(i)
		}
	}()
	t.Cleanup(func() { <-finished })

	for {
		select {
		case <-finished:
			return
		default:
		}
		next := done.Load() + 1
		end := time.Duration(next) * time.Second
		// A clip that coverage reaches exactly when chunk next is published.
		item := &PendingDetection{Source: e2eSource, BestModelID: r.model, FirstDetected: r.at(end).Add(-privacyClipGuard - e2eExportLength*time.Second)}
		check := r.p.checkPrivacy(r.settings, item, r.at(end))
		if check.verdict == coverageKeep {
			if !assert.True(t, check.hasHit, "coverage through chunk %d was seen with no speech hit at all", next) ||
				!assert.False(t, check.hit.Time.Before(r.at(end)),
					"coverage through chunk %d was seen before its speech hit", next) {
				return
			}
		}
	}
}

// TestPrivacy_ExtendedCaptureFlushesAfterMaxDeadlineWhileTheBirdKeepsSinging: an
// extended capture's EndTime stops at MaxDeadline (MaxDuration is at least the
// window here), however long the species keeps being heard. Once the audio of the capped clip is scanned the detection is
// released, instead of chasing a clip end that moves with every new detection.
func TestPrivacy_ExtendedCaptureFlushesAfterMaxDeadlineWhileTheBirdKeepsSinging(t *testing.T) {
	r := newPrivacyRig(t, "BirdNET_V2.4", 100*time.Second)
	r.settings.Realtime.ExtendedCapture.Enabled = true
	r.settings.Realtime.ExtendedCapture.MaxDuration = 60
	r.pend(26 * time.Second) // clip begins at 20 s
	key := pendingDetectionKey(e2eSource, e2eSpecies)
	item := r.p.pendingDetections[key]
	item.ExtendedCapture = true
	item.MaxDeadline = item.FirstDetected.Add(60 * time.Second)
	item.LastUpdated = item.FirstDetected.Add(100 * time.Second) // still singing well past the bound
	r.p.pendingDetections[key] = item
	require.True(t, detectionEndTime(r.settings, &item).Equal(item.MaxDeadline), "EndTime is capped at MaxDeadline")

	// The saved clip ends PreCapture after EndTime (83 s); the audio up to it plus
	// the guard is scanned and nothing beyond exists yet.
	r.feed(r.clip, 84*time.Second+500*time.Millisecond, sec(1))
	assert.Equal(t, "approved", r.outcome())
}

// TestDetectionEndTime_ExtendedCaptureCap pins the EndTime rule of an extended
// capture: LastUpdated plus the window, capped at MaxDeadline, and never below the
// normal capture span when MaxDuration is shorter than the window.
func TestDetectionEndTime_ExtendedCaptureCap(t *testing.T) {
	t.Parallel()
	settings := e2eSettings() // window 12 s
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		maxDuration time.Duration
		updated     time.Duration
		want        time.Duration
	}{
		{"below the cap follows LastUpdated", 60 * time.Second, 10 * time.Second, 22 * time.Second},
		{"past the cap stops at MaxDeadline", 60 * time.Second, 100 * time.Second, 60 * time.Second},
		{"MaxDuration shorter than the window keeps the normal span", 5 * time.Second, 20 * time.Second, 12 * time.Second},
		{"LastUpdated before the first detection keeps the normal span", 60 * time.Second, -5 * time.Second, 12 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			item := &PendingDetection{
				FirstDetected: base, LastUpdated: base.Add(tt.updated),
				ExtendedCapture: true, MaxDeadline: base.Add(tt.maxDuration),
			}
			assert.True(t, base.Add(tt.want).Equal(detectionEndTime(settings, item)))
		})
	}
}

// otherModelChunk builds a silent window of model ending at end, as a second
// model's monitor on the same source would produce it.
func (r *privacyRig) otherModelChunk(model string, end time.Duration) classifier.Results {
	spec := classifier.ModelRegistry[model].Spec
	rate := spec.EffectiveSampleRate()
	item := classifier.Results{
		StartTime: r.at(end).Add(-spec.ClipLength - e2ePreCapture*time.Second),
		Audio:     classifier.AudioTiming{EndAt: r.at(end)},
		PCMdata:   make([]byte, int(spec.ClipLength.Seconds())*rate*vadBytesPerSample),
		ModelID:   model,
	}
	item.Source = datastore.AudioSource{ID: e2eSource}
	return item
}

// feedOther feeds one chunk of model per second with audio ends in [from, to].
func (r *privacyRig) feedOther(model string, from, to int) {
	r.t.Helper()
	for end := from; end <= to; end++ {
		r.p.processDetections(r.otherModelChunk(model, time.Duration(end)*time.Second))
	}
}

// TestPrivacy_LabelCoverageNeedsEveryCurrentModel: with the VAD off, a clip is
// released only when every label model that is currently scoring the source has
// scanned the part of the clip it could have heard. A model that began mid-clip
// owes only the part from its start, and one that began after the clip owes
// nothing.
func TestPrivacy_LabelCoverageNeedsEveryCurrentModel(t *testing.T) {
	const second = "Perch_V2"
	newRig := func(ago time.Duration) *privacyRig {
		r := newPrivacyRig(t, "BirdNET_V2.4", ago)
		r.settings.Realtime.PrivacyFilter.VAD.Enabled = false
		r.feed(r.clip, 40*time.Second, sec(1)) // the 3 s model has scanned past the clip end
		return r
	}
	t.Run("a model behind the clip tail holds the clip", func(t *testing.T) {
		r := newRig(45 * time.Second)
		r.feedOther(second, 5, 33)
		r.pend(24 * time.Second) // clip 18 s to 33 s
		require.Equal(t, "pending", r.outcome(), "the second model has not covered the clip tail yet")
		r.feedOther(second, 34, 36)
		assert.Equal(t, "approved", r.outcome())
	})
	t.Run("a model that has not reached the clip start holds the clip", func(t *testing.T) {
		r := newRig(45 * time.Second)
		r.feedOther(second, 5, 10)
		r.pend(24 * time.Second) // clip 18 s to 33 s
		assert.Equal(t, "pending", r.outcome())
	})
	t.Run("a model that began mid-clip owes only its part", func(t *testing.T) {
		r := newRig(45 * time.Second)
		r.feedOther(second, 25, 30) // scans 20 s to 30 s of the clip, padded to 17.5 s to 33.5 s
		r.pend(24 * time.Second)
		require.Equal(t, "pending", r.outcome())
		r.feedOther(second, 31, 36) // now 20 s to 36 s, all it could have heard
		assert.Equal(t, "approved", r.outcome())
	})
	t.Run("a model that began after the clip is not waited for", func(t *testing.T) {
		r := newRig(45 * time.Second)
		r.feedOther(second, 45, 48)
		r.pend(24 * time.Second)
		assert.Equal(t, "approved", r.outcome())
	})
	t.Run("a removed model times out, fails open and is counted", func(t *testing.T) {
		r := newRig(100 * time.Second)
		pfm, err := metrics.NewPrivacyFilterMetrics(prometheus.NewRegistry())
		require.NoError(t, err)
		r.p.Metrics = &observability.Metrics{PrivacyFilter: pfm}
		r.settings.Realtime.Telemetry.Enabled = true
		r.feedOther(second, 5, 18) // stops while the 3 s model scans on
		r.pend(24 * time.Second)   // clip 18 s to 33 s, wait until 63 s, flushed at 100 s
		assert.Equal(t, "approved", r.outcome())
		expected := "\n# HELP privacy_filter_unscanned_saves_total Total detections approved for saving whose clip span was not fully scanned for speech before the coverage wait expired\n" +
			"# TYPE privacy_filter_unscanned_saves_total counter\nprivacy_filter_unscanned_saves_total 1\n"
		require.NoError(t, testutil.CollectAndCompare(pfm, strings.NewReader(expected), "privacy_filter_unscanned_saves_total"))
	})
	t.Run("a removed model holds the clip within the wait", func(t *testing.T) {
		r := newRig(45 * time.Second)
		r.feedOther(second, 5, 18)
		r.pend(24 * time.Second)
		assert.Equal(t, "pending", r.outcome())
	})
}

// TestPrivacy_LabelCoverageNeedsOneModelThatScannedTheWholeClip: a model that
// began mid-clip owes only its part, but the clip is released only when some
// current model scanned all of it. A source whose only scanner began inside the
// clip (the filter was just switched on, or coverage restarted after a stall)
// has never scanned the clip head, so the clip defers and then fails open.
func TestPrivacy_LabelCoverageNeedsOneModelThatScannedTheWholeClip(t *testing.T) {
	t.Run("defers while the only model began mid-clip", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
		r.settings.Realtime.PrivacyFilter.VAD.Enabled = false
		r.feed(30*time.Second, 40*time.Second, sec(1)) // scans 27 s on; the clip 18 s to 33 s starts before
		r.pend(24 * time.Second)
		assert.Equal(t, "pending", r.outcome())
	})
	t.Run("defers when every current model began mid-clip", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
		r.settings.Realtime.PrivacyFilter.VAD.Enabled = false
		r.feed(30*time.Second, 40*time.Second, sec(1)) // scans from 27 s
		r.feedOther("Perch_V2", 33, 40)                // scans from 28 s
		r.pend(24 * time.Second)
		assert.Equal(t, "pending", r.outcome())
	})
	t.Run("a model whose coverage is older than the horizon is not the whole-clip model", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 200*time.Second)
		r.settings.Realtime.PrivacyFilter.VAD.Enabled = false
		pfm, err := metrics.NewPrivacyFilterMetrics(prometheus.NewRegistry())
		require.NoError(t, err)
		r.p.Metrics = &observability.Metrics{PrivacyFilter: pfm}
		r.settings.Realtime.Telemetry.Enabled = true
		r.feed(r.clip, 40*time.Second, sec(1)) // covers the clip, but its newest end is 160 s old at the flush
		r.feedOther("Perch_V2", 25, 90)        // current, began mid-clip
		r.pend(24 * time.Second)               // clip 18 s to 33 s, wait until 63 s, flushed at 200 s
		require.Equal(t, "approved", r.outcome())
		expected := "\n# HELP privacy_filter_unscanned_saves_total Total detections approved for saving whose clip span was not fully scanned for speech before the coverage wait expired\n" +
			"# TYPE privacy_filter_unscanned_saves_total counter\nprivacy_filter_unscanned_saves_total 1\n"
		require.NoError(t, testutil.CollectAndCompare(pfm, strings.NewReader(expected), "privacy_filter_unscanned_saves_total"))
	})
	t.Run("fails open and is counted after the wait", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 100*time.Second)
		r.settings.Realtime.PrivacyFilter.VAD.Enabled = false
		pfm, err := metrics.NewPrivacyFilterMetrics(prometheus.NewRegistry())
		require.NoError(t, err)
		r.p.Metrics = &observability.Metrics{PrivacyFilter: pfm}
		r.settings.Realtime.Telemetry.Enabled = true
		r.feed(30*time.Second, 40*time.Second, sec(1))
		r.pend(24 * time.Second) // wait until 63 s, flushed at 100 s
		assert.Equal(t, "approved", r.outcome())
		expected := "\n# HELP privacy_filter_unscanned_saves_total Total detections approved for saving whose clip span was not fully scanned for speech before the coverage wait expired\n" +
			"# TYPE privacy_filter_unscanned_saves_total counter\nprivacy_filter_unscanned_saves_total 1\n"
		require.NoError(t, testutil.CollectAndCompare(pfm, strings.NewReader(expected), "privacy_filter_unscanned_saves_total"))
	})
}
