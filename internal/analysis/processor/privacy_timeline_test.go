package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// TestNewChunkAudio: only a chunk the privacy detectors can hear gets chunk data,
// and a gap claims nothing of its own window.
func TestNewChunkAudio(t *testing.T) {
	t.Parallel()
	end := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	item := func(model string, pcmLen int, gap bool) *classifier.Results {
		it := makeVADItem(model, "src", end, pcmLen)
		it.Audio.Gap = gap
		it.Audio.FreshBytes = 7
		return it
	}

	chunk, ok := newChunkAudio(item(testVADModel, testVADChunkBytes, false))
	require.True(t, ok)
	assert.Equal(t, vadStreamKey{source: "src", model: testVADModel}, chunk.key)
	assert.Equal(t, testVADRate, chunk.sampleRate)
	assert.True(t, chunk.span.to.Equal(end))
	assert.True(t, chunk.span.from.Equal(end.Add(-3*time.Second)))
	assert.True(t, chunk.scoredFrom().Equal(chunk.span.from))
	assert.Equal(t, 7, chunk.freshBytes)

	gapped, ok := newChunkAudio(item(testVADModel, testVADChunkBytes, true))
	require.True(t, ok)
	assert.True(t, gapped.scoredFrom().Equal(end), "a gap window claims nothing before its end")

	for name, it := range map[string]*classifier.Results{
		"ultrasonic bat model": item(classifier.RegistryIDBat, testVADChunkBytes, false),
		"empty PCM":            item(testVADModel, 0, false),
		"unknown model":        item("no-such-model", testVADChunkBytes, false),
	} {
		_, ok := newChunkAudio(it)
		assert.False(t, ok, name)
	}
}

// TestRecordHumanDetection_NeverMovesBackward pins the monotonic hit time: a
// later write with an earlier stamp (the VAD after a label hit, or a 5 s
// model's chunk after a 3 s model's) must not hide the later hit.
func TestRecordHumanDetection_NeverMovesBackward(t *testing.T) {
	t.Parallel()
	t2 := time.Date(2026, 6, 11, 8, 0, 10, 0, time.UTC)
	tests := []struct {
		name        string
		vadAudioEnd time.Time
		wantTime    time.Time
		wantTrigger string
	}{
		{"earlier VAD stamp keeps the label hit", t2.Add(-5 * time.Second), t2, metrics.TriggerLabel},
		{"equal stamp keeps the existing trigger", t2, t2, metrics.TriggerLabel},
		{"later VAD stamp overwrites", t2.Add(time.Second), t2.Add(time.Second), metrics.TriggerVAD},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Processor{
				LastHumanDetection: map[string]HumanDetection{},
				LastDogDetection:   map[string]time.Time{},
				vadGate:            newTestGate(&fakeStreamer{prob: 0.9, flushOK: true}, &fakeSession{}, nil),
			}
			settings := vadSettings(true, 0.35)
			settings.Realtime.PrivacyFilter.Confidence = 0.05

			label := classifier.Results{Audio: classifier.AudioTiming{EndAt: t2}}
			label.Source.ID = "src1"
			p.handleHumanDetection(settings, label, datastore.Results{Species: "Speech", Confidence: 0.9})
			require.Equal(t, metrics.TriggerLabel, p.LastHumanDetection["src1"].Trigger)

			runGate(p, settings, makeVADItem(testVADModel, "src1", tt.vadAudioEnd, testVADChunkBytes))

			got := p.LastHumanDetection["src1"]
			assert.True(t, tt.wantTime.Equal(got.Time), "got %v want %v", got.Time, tt.wantTime)
			assert.Equal(t, tt.wantTrigger, got.Trigger)
		})
	}
}

// TestRunVADGate_HitStampedWithFlushAudioEnd: several chunks merged into one
// throttled flush stamp the hit with the flushing chunk's audio end, never a
// back-dated StartTime.
func TestRunVADGate_HitStampedWithFlushAudioEnd(t *testing.T) {
	t.Parallel()
	g := newTestGate(&fakeStreamer{prob: 0.9, flushOK: true}, &fakeSession{}, nil)
	p := &Processor{LastHumanDetection: map[string]HumanDetection{}, vadGate: g}
	settings := vadSettings(true, 0.35)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	run := func(offset time.Duration) {
		item := makeVADItem(testVADModel, "src1", base.Add(offset), testVADChunkBytes)
		item.StartTime = base.Add(-time.Hour) // must never be used as the stamp
		runGate(p, settings, item)
	}

	run(0)
	require.True(t, p.LastHumanDetection["src1"].Time.Equal(base), "the first chunk flushes at once")
	// Four 200 ms chunks only buffer: the stamp stays at the last flush.
	for k := 1; k <= 4; k++ {
		run(time.Duration(k) * 200 * time.Millisecond)
	}
	assert.True(t, p.LastHumanDetection["src1"].Time.Equal(base))
	// The fifth chunk is 1 s after the first flush: all five merge into one flush
	// that is stamped with this chunk's audio end.
	run(time.Second)
	assert.True(t, p.LastHumanDetection["src1"].Time.Equal(base.Add(time.Second)))
}

// TestPublishCoverage pins span merging and pruning.
func TestPublishCoverage(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	key := coverageKey{"src", "m", metrics.TriggerVAD}
	span := func(from, to time.Duration) audioSpan { return audioSpan{from: base.Add(from), to: base.Add(to)} }

	t.Run("contiguous spans extend the newest", func(t *testing.T) {
		t.Parallel()
		p := &Processor{}
		p.publishCoverage(coverageUpdate{key, span(0, 10*time.Second)})
		p.publishCoverage(coverageUpdate{key, span(10*time.Second+vadCoverageJoinTolerance, 20*time.Second)})
		p.publishCoverage(coverageUpdate{key, span(-time.Second, 15*time.Second)}) // earlier start widens it
		assert.Equal(t, []audioSpan{span(-time.Second, 20*time.Second)}, p.privacyCoverage[key])
	})
	t.Run("a hole starts a new span and zero-length spans are ignored", func(t *testing.T) {
		t.Parallel()
		p := &Processor{}
		p.publishCoverage(coverageUpdate{key, span(0, 10*time.Second)})
		p.publishCoverage(coverageUpdate{key, span(30*time.Second, 30*time.Second)})
		p.publishCoverage(coverageUpdate{key, span(30*time.Second, 40*time.Second)})
		assert.Equal(t, []audioSpan{span(0, 10*time.Second), span(30*time.Second, 40*time.Second)}, p.privacyCoverage[key])
	})
	t.Run("several updates are all recorded", func(t *testing.T) {
		t.Parallel()
		p := &Processor{}
		other := coverageKey{"src", "m", metrics.TriggerLabel}
		p.publishCoverage(coverageUpdate{key, span(0, time.Second)}, coverageUpdate{other, span(0, 2*time.Second)})
		assert.Equal(t, []audioSpan{span(0, time.Second)}, p.privacyCoverage[key])
		assert.Equal(t, []audioSpan{span(0, 2*time.Second)}, p.privacyCoverage[other])
	})
	t.Run("old spans are pruned and the list is capped", func(t *testing.T) {
		t.Parallel()
		p := &Processor{}
		p.publishCoverage(coverageUpdate{key, span(0, time.Second)})
		for i := range privacyCoverageMaxSpans + 4 {
			from := time.Duration(10+i*2) * time.Second
			p.publishCoverage(coverageUpdate{key, span(from, from+time.Second)})
		}
		assert.Len(t, p.privacyCoverage[key], privacyCoverageMaxSpans)
		p.publishCoverage(coverageUpdate{key, span(privacyCoverageHorizon+time.Hour, privacyCoverageHorizon+time.Hour+time.Second)})
		assert.Len(t, p.privacyCoverage[key], 1, "everything older than the horizon is dropped")
	})
}

// TestRequiredDetector picks the VAD only when it is enabled and either loaded
// or recently active on the source.
func TestRequiredDetector(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	settings := vadSettings(true, 0.35)
	p := &Processor{vadGate: newTestGate(nil, &fakeSession{}, nil)}

	assert.Equal(t, metrics.TriggerLabel, p.requiredDetectorLocked(settings, "src", now), "VAD not loaded and never seen")

	p.publishCoverage(coverageUpdate{coverageKey{"src", "m", metrics.TriggerVAD}, audioSpan{from: now.Add(-time.Minute), to: now.Add(-30 * time.Second)}})
	assert.Equal(t, metrics.TriggerVAD, p.requiredDetectorLocked(settings, "src", now), "recent VAD coverage")
	assert.Equal(t, metrics.TriggerLabel, p.requiredDetectorLocked(settings, "other", now))
	assert.Equal(t, metrics.TriggerLabel, p.requiredDetectorLocked(settings, "src", now.Add(time.Hour)), "stale VAD coverage")

	disabled := vadSettings(false, 0.35)
	assert.Equal(t, metrics.TriggerLabel, p.requiredDetectorLocked(disabled, "src", now))
}

// TestMergeCoverage_JoinsOnStreamPositionsWhenReported: with producer stream
// positions a span joins the previous one only in the same generation and when it
// starts at or before the stored end position, whatever the timestamps say; spans
// without positions fall back to the time tolerance.
func TestMergeCoverage_JoinsOnStreamPositionsWhenReported(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	key := coverageKey{"src", "m", metrics.TriggerLabel}
	span := func(from, to time.Duration, startPos, endPos int64, gen uint64) audioSpan {
		return audioSpan{from: base.Add(from), to: base.Add(to), startPos: startPos, endPos: endPos, gen: gen}
	}
	tests := []struct {
		name      string
		next      audioSpan
		wantSpans int
	}{
		{"adjacent positions join despite a timestamp hole", span(10*time.Second, 13*time.Second, 1000, 1300, 1), 1},
		{"a position hole splits despite adjacent timestamps", span(3*time.Second, 6*time.Second, 1500, 1800, 1), 2},
		{"a new generation never joins", span(3*time.Second, 6*time.Second, 1000, 1300, 2), 2},
		{"no positions use the time tolerance", span(3*time.Second+vadCoverageJoinTolerance, 6*time.Second, 0, 0, 0), 1},
		{"no positions split beyond the time tolerance", span(4*time.Second, 6*time.Second, 0, 0, 0), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Processor{}
			p.publishCoverage(coverageUpdate{key, span(0, 3*time.Second, 1000, 1000, 1)})
			if tt.next.gen == 0 {
				p.privacyCoverage[key][0].gen = 0 // the stored span has no positions either
			}
			p.publishCoverage(coverageUpdate{key, tt.next})
			assert.Len(t, p.privacyCoverage[key], tt.wantSpans)
		})
	}
}
