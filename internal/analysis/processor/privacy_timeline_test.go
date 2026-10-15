package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

func TestItemAudioEnd_Fallbacks(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.PreCapture = 3

	tests := []struct {
		name string
		item classifier.Results
		want time.Time
	}{
		{
			name: "audio end wins",
			item: classifier.Results{AudioEndAt: base, AudioCapturedAt: base.Add(time.Hour), StartTime: base.Add(2 * time.Hour)},
			want: base,
		},
		{
			name: "captured time when no audio end",
			item: classifier.Results{AudioCapturedAt: base, StartTime: base.Add(2 * time.Hour)},
			want: base,
		},
		{
			name: "start time plus pre-capture plus chunk duration",
			item: classifier.Results{ModelID: testVADModel, StartTime: base, PCMdata: make([]byte, testVADChunkBytes)},
			want: base.Add(3*time.Second + 3*time.Second),
		},
		{
			name: "unknown model has no chunk duration",
			item: classifier.Results{ModelID: "no-such-model", StartTime: base, PCMdata: make([]byte, testVADChunkBytes)},
			want: base.Add(3 * time.Second),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, tt.want.Equal(itemAudioEnd(settings, &tt.item)))
		})
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

			label := classifier.Results{AudioEndAt: t2}
			label.Source.ID = "src1"
			p.handleHumanDetection(settings, label, datastore.Results{Species: "Speech", Confidence: 0.9})
			require.Equal(t, metrics.TriggerLabel, p.LastHumanDetection["src1"].Trigger)

			p.runVADGate(settings, makeVADItem(testVADModel, "src1", tt.vadAudioEnd, testVADChunkBytes))

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
		p.runVADGate(settings, item)
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
	span := func(from, to time.Duration) vadSpan { return vadSpan{base.Add(from), base.Add(to)} }

	t.Run("contiguous spans extend the newest", func(t *testing.T) {
		t.Parallel()
		p := &Processor{}
		p.publishCoverage(key, span(0, 10*time.Second))
		p.publishCoverage(key, span(10*time.Second+vadCoverageJoinTolerance, 20*time.Second))
		p.publishCoverage(key, span(-time.Second, 15*time.Second)) // earlier start widens it
		assert.Equal(t, []vadSpan{span(-time.Second, 20*time.Second)}, p.privacyCoverage[key])
	})
	t.Run("a hole starts a new span and zero-length spans are ignored", func(t *testing.T) {
		t.Parallel()
		p := &Processor{}
		p.publishCoverage(key, span(0, 10*time.Second))
		p.publishCoverage(key, span(30*time.Second, 30*time.Second))
		p.publishCoverage(key, span(30*time.Second, 40*time.Second))
		assert.Equal(t, []vadSpan{span(0, 10*time.Second), span(30*time.Second, 40*time.Second)}, p.privacyCoverage[key])
	})
	t.Run("old spans are pruned and the list is capped", func(t *testing.T) {
		t.Parallel()
		p := &Processor{}
		p.publishCoverage(key, span(0, time.Second))
		for i := range privacyCoverageMaxSpans + 4 {
			from := time.Duration(10+i*2) * time.Second
			p.publishCoverage(key, span(from, from+time.Second))
		}
		assert.Len(t, p.privacyCoverage[key], privacyCoverageMaxSpans)
		p.publishCoverage(key, span(privacyCoverageHorizon+time.Hour, privacyCoverageHorizon+time.Hour+time.Second))
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

	p.publishCoverage(coverageKey{"src", "m", metrics.TriggerVAD}, vadSpan{now.Add(-time.Minute), now.Add(-30 * time.Second)})
	assert.Equal(t, metrics.TriggerVAD, p.requiredDetectorLocked(settings, "src", now), "recent VAD coverage")
	assert.Equal(t, metrics.TriggerLabel, p.requiredDetectorLocked(settings, "other", now))
	assert.Equal(t, metrics.TriggerLabel, p.requiredDetectorLocked(settings, "src", now.Add(time.Hour)), "stale VAD coverage")

	disabled := vadSettings(false, 0.35)
	assert.Equal(t, metrics.TriggerLabel, p.requiredDetectorLocked(disabled, "src", now))
}
