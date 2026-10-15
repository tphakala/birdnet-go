package processor

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference/vad"
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
	settings := vadSettings(true, 0.35)
	settings.Realtime.Audio.Export.Length = e2eExportLength
	settings.Realtime.Audio.Export.PreCapture = e2ePreCapture
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
		StartTime:  r.at(end).Add(-r.clip - e2ePreCapture*time.Second),
		AudioEndAt: r.at(end),
		PCMdata:    pcm,
		ModelID:    r.model,
	}
	item.Source = datastore.AudioSource{ID: e2eSource}
	return item
}

// feed processes one chunk per step with audio ends in [from, to].
func (r *privacyRig) feed(from, to, step time.Duration) {
	r.t.Helper()
	for end := from; end <= to; end += step {
		r.p.processDetections(r.chunk(end))
	}
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
		FirstDetected: r.at(end).Add(-r.clip - e2ePreCapture*time.Second),
		FlushDeadline: time.Now().Add(-time.Second),
		Count:         5,
	}
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
			name   string
			speech []speechInterval
			want   string
		}{
			// The detecting chunk ends at 40 s; its clip begins PreCapture before the
			// chunk start. The speech case has 0.5 s of speech ending 0.2 s before the
			// detecting chunk begins, i.e. inside the pre-capture part of the clip.
			{"speech in the pre-capture region", nil, "discarded"},
			{"no speech", []speechInterval{}, "approved"},
		} {
			t.Run(geo.name+"/"+tc.name, func(t *testing.T) {
				r := newPrivacyRig(t, geo.model, 45*time.Second)
				chunkStart := 40*time.Second - r.clip
				if tc.want == "discarded" {
					r.speech = []speechInterval{{chunkStart - sec(0.7), chunkStart - sec(0.2)}}
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
			_, clipEnd := clipSpan(r.settings, &PendingDetection{FirstDetected: r.at(40 * time.Second).Add(-r.clip - e2ePreCapture*time.Second)})
			// Speech 0.3 s before the clip's end.
			endOff := clipEnd.Sub(r.base)
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
	_, clipEnd := clipSpan(r.settings, &item)
	endOff := clipEnd.Sub(r.base)
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

	r.feed(r.clip, sec(38), sec(1))
	gapped := r.chunk(sec(40))
	gapped.AudioGap = true
	r.p.processDetections(gapped)
	r.feed(sec(41), sec(55), sec(1))
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
	r.feed(r.clip, sec(38), sec(1))
	gapped := r.chunk(sec(40))
	gapped.AudioGap = true
	r.p.processDetections(gapped)
	r.feed(sec(41), sec(54), sec(1))
	r.pend(40 * time.Second)
	assert.Equal(t, "pending", r.outcome())
}

// TestPrivacy_NotApplicableKeeps: a source no detector scored is approved without
// waiting, and a VAD that never loaded falls back to the label coverage.
func TestPrivacy_NotApplicableKeeps(t *testing.T) {
	t.Run("bat-only source has no coverage", func(t *testing.T) {
		r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
		r.pend(40 * time.Second)
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

// TestPrivacy_GuardBoundary pins the guard margins exactly.
func TestPrivacy_GuardBoundary(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	settings := vadSettings(true, 0.35)
	settings.Realtime.Audio.Export.Length = e2eExportLength
	settings.Realtime.Audio.Export.PreCapture = e2ePreCapture
	item := &PendingDetection{Source: e2eSource, FirstDetected: base, Count: 5}
	_, end := clipSpan(settings, item)

	t.Run("discard check", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name string
			hit  time.Time
			want bool
		}{
			{"hit at begin minus guard", base.Add(-privacyClipGuard), true},
			{"hit one ns earlier", base.Add(-privacyClipGuard - 1), false},
		} {
			p := &Processor{LastHumanDetection: map[string]HumanDetection{e2eSource: {Time: tc.hit}}}
			got, _ := p.shouldDiscardDetection(item, settings, 1)
			assert.Equal(t, tc.want, got, tc.name)
		}
	})
	t.Run("coverage check", func(t *testing.T) {
		t.Parallel()
		now := end.Add(time.Second)
		for _, tc := range []struct {
			name string
			from time.Time
			to   time.Time
			want coverageVerdict
		}{
			{"covers exactly with guards", base.Add(-privacyClipGuard), end.Add(privacyClipGuard), coverageKeep},
			{"end short by 1 ns defers", base.Add(-privacyClipGuard), end.Add(privacyClipGuard - 1), coverageDefer},
			{"start late by 1 ns defers", base.Add(-privacyClipGuard + 1), end.Add(privacyClipGuard), coverageDefer},
		} {
			p := &Processor{privacyCoverage: map[coverageKey][]vadSpan{
				{e2eSource, "m", metrics.TriggerVAD}: {{from: tc.from, to: tc.to}},
			}}
			p.vadGate = newTestGate(nil, &fakeSession{}, nil)
			p.vadGate.snapshot.Store(&vadConfigSnapshot{})
			assert.Equal(t, tc.want, p.privacyCoverageVerdict(settings, item, now), tc.name)
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
	}{
		{"normal", false, 0, 0},
		{"extended", true, 40 * time.Second, 0},
		{"extended capped at the buffer", true, 90 * time.Second, 30},
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

			begin, end := clipSpan(settings, item)
			p.normalizeDetectionTimes(item)
			win, _ := settings.DetectionCaptureWindow(item.Detection.Result.BeginTime, item.Detection.Result.EndTime)

			assert.True(t, begin.Equal(item.Detection.Result.BeginTime))
			assert.True(t, end.Equal(item.Detection.Result.BeginTime.Add(time.Duration(win.Length)*time.Second)))
			if tt.capBuf > 0 {
				assert.True(t, win.Capped, "the capped case must actually be capped")
			}
		})
	}
}

// TestPrivacy_HitWrittenAfterCoverageReadIsStillSeen: a chunk can write its speech
// hit and publish the coverage that completes a pending clip between the
// flusher's coverage read and its hit read. The flusher must read the hit after
// the coverage, so the late hit still discards the detection.
func TestPrivacy_HitWrittenAfterCoverageReadIsStillSeen(t *testing.T) {
	r := newPrivacyRig(t, "BirdNET_V2.4", 45*time.Second)
	r.feed(r.clip, sec(52), sec(1))
	r.pend(40 * time.Second)
	hitAt := r.at(40 * time.Second)
	r.p.afterCoverageRead = func() {
		r.p.recordHumanDetection(e2eSource, hitAt, metrics.TriggerVAD)
	}
	assert.Equal(t, "discarded", r.outcome())
}
