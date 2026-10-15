package processor

import (
	"math"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/inference/vad"
)

// The second model used by the multi-model tests: a 5 s clip at 32 kHz.
const testVADModelB = "Perch_V2"

var testVADBase = time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

// streamerLog builds one fakeStreamer per stream, in creation order.
type streamerLog struct{ list []*fakeStreamer }

func (l *streamerLog) factory() vad.Streamer {
	f := &fakeStreamer{prob: 0.1, flushOK: true}
	l.list = append(l.list, f)
	return f
}

func newLoggedGate() (*vadGate, *streamerLog) {
	l := &streamerLog{}
	g := newTestGate(nil, &fakeSession{}, nil)
	g.newStreamer = l.factory
	return g, l
}

func sumInts(v []int) int {
	n := 0
	for _, x := range v {
		n += x
	}
	return n
}

// modelStepBytes is the PCM size of step of audio for model.
func modelStepBytes(model string, step time.Duration) int {
	return int(step.Seconds()*float64(classifier.ModelRegistry[model].Spec.EffectiveSampleRate())) * vadBytesPerSample
}

// TestVADGate_InterleavedModelsKeepSeparateStreams: a 3 s model at 48 kHz and a
// 5 s model at 32 kHz on one source each get their own streamer; the model
// switch never resets either, and each stream flushes about once per second.
func TestVADGate_InterleavedModelsKeepSeparateStreams(t *testing.T) {
	t.Parallel()
	type ev struct {
		at    time.Duration
		model string
	}
	events := make([]ev, 0, 64)
	for at := time.Duration(0); at <= 6*time.Second; at += 200 * time.Millisecond {
		events = append(events, ev{at, testVADModel})
	}
	for at := time.Duration(0); at <= 6*time.Second; at += 333 * time.Millisecond {
		events = append(events, ev{at, testVADModelB})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].at < events[j].at })

	g, l := newLoggedGate()
	flushes := map[string]int{}
	for _, e := range events {
		if _, ran := scoreChunk(t, g, modelChunk("s1", e.model, testVADBase.Add(e.at))); ran {
			flushes[e.model]++
		}
	}

	require.Len(t, l.list, 2, "one streamer per (source, model)")
	a, b := l.list[0], l.list[1]
	assert.Zero(t, a.resets+b.resets, "interleaving models must never reset a stream")
	assert.Equal(t, testVADChunkBytes, a.appends[0])
	assert.Equal(t, modelStepBytes(testVADModelB, 5*time.Second), b.appends[0])
	for _, n := range a.appends[1:] {
		assert.Equal(t, modelStepBytes(testVADModel, 200*time.Millisecond), n, "model A appends one 200 ms step")
	}
	for _, n := range b.appends[1:] {
		assert.Equal(t, modelStepBytes(testVADModelB, 333*time.Millisecond), n, "model B appends one 333 ms step")
	}
	assert.Equal(t, 7, flushes[testVADModel], "about one decision per second per stream")
	assert.InDelta(t, 5, flushes[testVADModelB], 1)
}

// TestVADGate_ThrottleMergesChunksAndSpansNewestAudio: however many chunks the
// 1 s throttle merges into a flush, the span ends at the flushing chunk's audio
// end, starts at the first chunk's audio start, and the appended audio is exactly
// the audio in between.
func TestVADGate_ThrottleMergesChunksAndSpansNewestAudio(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		model string
		step  time.Duration
	}{
		{"3s clip step 200ms", testVADModel, 200 * time.Millisecond},
		{"3s clip step 300ms", testVADModel, 300 * time.Millisecond},
		{"3s clip step 600ms", testVADModel, 600 * time.Millisecond},
		{"3s clip step 1000ms", testVADModel, time.Second},
		{"5s clip step 333ms", testVADModelB, 333 * time.Millisecond},
		{"5s clip step 1667ms", testVADModelB, 1667 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g, l := newLoggedGate()
			spec := classifier.ModelRegistry[tt.model].Spec
			firstStart := testVADBase.Add(-spec.ClipLength)
			flushed := 0
			for k := range 40 {
				end := testVADBase.Add(time.Duration(k) * tt.step)
				sc, ran := scoreChunk(t, g, modelChunk("s1", tt.model, end))
				if !ran {
					continue
				}
				flushed++
				assert.True(t, sc.span.to.Equal(end), "span ends at the flushing chunk's audio end")
				assert.True(t, sc.span.from.Equal(firstStart), "span starts at the first chunk's audio start")
				wantBytes := int(math.Round(sc.span.to.Sub(sc.span.from).Seconds()*float64(spec.EffectiveSampleRate()))) * vadBytesPerSample
				assert.Equal(t, wantBytes, sumInts(l.list[0].appends), "appended audio equals the audio between from and to")
			}
			assert.Greater(t, flushed, 2)
		})
	}
}

// TestVADGate_GapFlagRestartsCoverage: a producer gap claims none of the window it
// arrives with, for a stream's first window and mid-stream (which also resets the
// LSTM state).
func TestVADGate_GapFlagRestartsCoverage(t *testing.T) {
	t.Parallel()
	t.Run("first window", func(t *testing.T) {
		t.Parallel()
		g, _ := newLoggedGate()
		ch := modelChunk("s1", testVADModel, testVADBase)
		ch.gap = true
		sc, ran := scoreChunk(t, g, ch)
		require.True(t, ran)
		assert.True(t, sc.span.from.Equal(testVADBase), "the zero-filled head is not claimed")
	})
	t.Run("mid-stream", func(t *testing.T) {
		t.Parallel()
		g, l := newLoggedGate()
		scoreChunk(t, g, modelChunk("s1", testVADModel, testVADBase))

		gapEnd := testVADBase.Add(time.Second)
		ch := modelChunk("s1", testVADModel, gapEnd)
		ch.gap = true
		sc, ran := scoreChunk(t, g, ch)
		assert.True(t, ran, "a reset stream flushes immediately")
		assert.Equal(t, 1, l.list[0].resets)
		assert.True(t, sc.span.from.Equal(gapEnd), "a gap claims none of the spliced window")

		next := testVADBase.Add(2 * time.Second)
		sc, ran = scoreChunk(t, g, modelChunk("s1", testVADModel, next))
		assert.True(t, ran)
		assert.True(t, sc.span.from.Equal(gapEnd), "later chunks extend the span from the gap")
		assert.True(t, sc.span.to.Equal(next))
	})
}

// TestVADGate_TimeHoleAndCoverage: a hole between two chunks beyond the join
// tolerance breaks the covered span without resetting the LSTM state; one within
// the tolerance is timing jitter and keeps the span.
func TestVADGate_TimeHoleAndCoverage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		hole      time.Duration
		keepsSpan bool
	}{
		{"hole beyond tolerance breaks coverage", 300 * time.Millisecond, false},
		{"hole within tolerance keeps coverage", 100 * time.Millisecond, true},
		{"hole exactly at tolerance keeps coverage", vadCoverageJoinTolerance, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g, l := newLoggedGate()
			first, _ := scoreChunk(t, g, modelChunk("s1", testVADModel, testVADBase))

			end := testVADBase.Add(3*time.Second + tt.hole)
			sc, ran := scoreChunk(t, g, modelChunk("s1", testVADModel, end))
			require.True(t, ran)
			assert.Zero(t, l.list[0].resets, "a hole inside the reset gap keeps the LSTM state")
			if tt.keepsSpan {
				assert.True(t, sc.span.from.Equal(first.span.from))
			} else {
				assert.True(t, sc.span.from.Equal(end.Add(-3*time.Second)), "the unscored hole breaks coverage")
			}
		})
	}
}

// TestVADGate_ModelRemovedAndReaddedTransitions: one model stopping, returning or
// the gate closing never disturbs another model's stream.
func TestVADGate_ModelRemovedAndReaddedTransitions(t *testing.T) {
	t.Parallel()
	const modelA, modelB = "A", "B"
	feed := func(g *vadGate, model string, at time.Duration) (audioSpan, bool) {
		sc, ran := scoreChunk(t, g, chunkWith("s1", model, make([]byte, testVADChunkBytes), testVADRate, testVADBase.Add(at)))
		return sc.span, ran
	}
	warm := func() (*vadGate, *streamerLog) {
		g, l := newLoggedGate()
		for s := range 4 {
			at := time.Duration(s) * time.Second
			feed(g, modelA, at)
			feed(g, modelB, at)
		}
		return g, l
	}
	baseStart := testVADBase.Add(-3 * time.Second)

	t.Run("other model continues unaffected while one stops", func(t *testing.T) {
		t.Parallel()
		g, l := warm()
		var span audioSpan
		for s := 4; s < 15; s++ {
			sp, ran := feed(g, modelA, time.Duration(s)*time.Second)
			require.True(t, ran)
			span = sp
		}
		assert.Zero(t, l.list[0].resets)
		assert.True(t, span.from.Equal(baseStart), "A's span stays contiguous")
	})
	t.Run("model back after 10 s starts a new span", func(t *testing.T) {
		t.Parallel()
		g, l := warm()
		at := 13 * time.Second
		span, ran := feed(g, modelB, at)
		require.True(t, ran)
		assert.Equal(t, 1, l.list[1].resets)
		assert.True(t, span.from.Equal(testVADBase.Add(at-3*time.Second)))
	})
	t.Run("model back after 500 ms stays contiguous", func(t *testing.T) {
		t.Parallel()
		g, l := warm()
		// The chunk 500 ms after B's last one is inside the throttle and only
		// buffers; the next one, a second after B's last flush, decides.
		_, ran := feed(g, modelB, 3500*time.Millisecond)
		assert.False(t, ran, "inside the throttle")
		span, ran := feed(g, modelB, 4*time.Second)
		require.True(t, ran)
		assert.Zero(t, l.list[1].resets)
		assert.True(t, span.from.Equal(baseStart), "the span continues through the short absence")
	})
	t.Run("close rebuilds both streams with new spans", func(t *testing.T) {
		t.Parallel()
		g, l := warm()
		g.close()
		at := 5 * time.Second
		spanA, ranA := feed(g, modelA, at)
		spanB, ranB := feed(g, modelB, at)
		require.True(t, ranA)
		require.True(t, ranB)
		assert.Len(t, l.list, 4)
		want := testVADBase.Add(at - 3*time.Second)
		assert.True(t, spanA.from.Equal(want))
		assert.True(t, spanB.from.Equal(want))
	})
}

// TestVADGate_PreCaptureHotReloadDoesNotSkipAudio: raising PreCapture mid-stream
// moves every later StartTime back, but the gate times on audio end, so no chunk
// is skipped and the appended audio equals the elapsed audio.
func TestVADGate_PreCaptureHotReloadDoesNotSkipAudio(t *testing.T) {
	t.Parallel()
	g, l := newLoggedGate()
	p := &Processor{LastHumanDetection: map[string]HumanDetection{}, vadGate: g}
	settings := vadSettings(true, 0.35)

	const (
		chunks     = 20
		reloadAt   = 10
		step       = 200 * time.Millisecond
		oldPre     = 3
		newPre     = 5
		clipSecond = 3
	)
	for k := range chunks {
		pre := oldPre
		if k >= reloadAt {
			pre = newPre
		}
		settings.Realtime.Audio.Export.PreCapture = pre
		end := testVADBase.Add(time.Duration(k) * step)
		item := makeVADItem(testVADModel, "s1", end, testVADChunkBytes)
		item.StartTime = end.Add(-time.Duration(pre+clipSecond) * time.Second)
		runGate(p, settings, item)
	}

	require.Len(t, l.list, 1)
	st := l.list[0]
	assert.Zero(t, st.resets)
	assert.Equal(t, testVADChunkBytes+(chunks-1)*modelStepBytes(testVADModel, step), sumInts(st.appends),
		"every chunk's fresh audio must reach the streamer")
	assert.Len(t, st.appends, chunks, "no chunk may be skipped as a duplicate")
}

// TestVADGate_JitteredAudioEndsStillScoreEveryFreshByte: when timing jitter makes
// a window's audio-end delta shorter than the audio it really adds, the gate
// still appends all of the producer's fresh bytes, so speech in the part a
// timestamp-derived trim would drop is scored and recorded.
func TestVADGate_JitteredAudioEndsStillScoreEveryFreshByte(t *testing.T) {
	t.Parallel()
	g := newTestGate(nil, &fakeSession{}, nil)
	g.newStreamer = func() vad.Streamer { return &markerStreamer{} }
	p := &Processor{LastHumanDetection: map[string]HumanDetection{}, vadGate: g}
	settings := vadSettings(true, 0.35)

	const (
		chunkSeconds = 3
		oneSecond    = testVADRate * vadBytesPerSample
	)
	// True audio ends are 10, 11, 12 and 13 s; the second window is stamped 0.2 s
	// early. Speech is in the first 0.2 s of the second window's fresh second.
	trueEnds := []time.Duration{10 * time.Second, 11 * time.Second, 12 * time.Second, 13 * time.Second}
	stamped := []time.Duration{10 * time.Second, 10800 * time.Millisecond, 12 * time.Second, 13 * time.Second}
	speechFrom, speechTo := 10*time.Second, 10200*time.Millisecond

	for i, end := range trueEnds {
		pcm := make([]byte, chunkSeconds*oneSecond)
		start := end - chunkSeconds*time.Second
		for s := range len(pcm) / vadBytesPerSample {
			at := start + time.Duration(s)*time.Second/testVADRate
			if at >= speechFrom && at < speechTo {
				pcm[s*vadBytesPerSample] = 1
			}
		}
		item := makeVADItem(testVADModel, "src1", testVADBase.Add(stamped[i]), 0)
		item.PCMdata = pcm
		item.Audio.FreshBytes = oneSecond
		if i == 0 {
			item.Audio.FreshBytes = len(pcm)
		}
		runGate(p, settings, item)
	}

	hit, ok := p.LastHumanDetection["src1"]
	require.True(t, ok, "speech in the slice a timestamp trim drops must still be scored")
	assert.False(t, hit.Time.Before(testVADBase.Add(10200*time.Millisecond)))
}

// positioned gives ch producer stream positions: it ends at endPos in generation
// gen and its last contiguous bytes are genuine audio.
func positioned(ch *chunkAudio, gen uint64, endPos int64, contiguous int) *chunkAudio {
	ch.generation, ch.endPos, ch.contiguousBytes = gen, endPos, contiguous
	return ch
}

// TestVADGate_PositionHoleRestartsCoverageWithoutResettingTheStream: audio the
// producer never delivered between two windows (a hole in the stream positions)
// restarts the covered span at the new window's contiguous tail, but the LSTM
// state survives because the window is not flagged as a gap.
func TestVADGate_PositionHoleRestartsCoverageWithoutResettingTheStream(t *testing.T) {
	t.Parallel()
	g, l := newLoggedGate()
	full := testVADChunkBytes
	oneSecond := int64(testVADRate * vadBytesPerSample)

	first := positioned(modelChunk("s1", testVADModel, testVADBase), 1, 10*oneSecond, full)
	scFirst, ran := scoreChunk(t, g, first)
	require.True(t, ran)

	second := positioned(modelChunk("s1", testVADModel, testVADBase.Add(time.Second)), 1, 11*oneSecond, full)
	scSecond, ran := scoreChunk(t, g, second)
	require.True(t, ran)
	assert.True(t, scSecond.span.from.Equal(scFirst.span.from), "adjacent windows keep one span")
	assert.Equal(t, int(oneSecond), l.list[0].appends[1], "only the audio added since the last window is appended")

	// Ten seconds of stream never arrived, but the window is not flagged.
	third := positioned(modelChunk("s1", testVADModel, testVADBase.Add(2*time.Second)), 1, 21*oneSecond, full)
	scThird, ran := scoreChunk(t, g, third)
	require.True(t, ran)
	assert.True(t, scThird.span.from.Equal(testVADBase.Add(2*time.Second-3*time.Second)), "coverage restarts at the window's tail")
	assert.Equal(t, full, l.list[0].appends[2], "a hole larger than the window appends the whole window")
	assert.Zero(t, l.list[0].resets, "the stream is not reset without a gap flag")
}

// TestVADGate_FreshBytesFollowStreamPositionsNotTimestamps: with stream positions
// the audio appended is exactly what the stream advanced, however the timestamps
// of the two windows differ.
func TestVADGate_FreshBytesFollowStreamPositionsNotTimestamps(t *testing.T) {
	t.Parallel()
	g, l := newLoggedGate()
	full := testVADChunkBytes
	oneSecond := int64(testVADRate * vadBytesPerSample)

	scoreChunk(t, g, positioned(modelChunk("s1", testVADModel, testVADBase), 1, 10*oneSecond, full))
	// Stamped only 100 ms later, but the stream advanced a whole second.
	scoreChunk(t, g, positioned(modelChunk("s1", testVADModel, testVADBase.Add(100*time.Millisecond)), 1, 11*oneSecond, full))
	assert.Equal(t, int(oneSecond), l.list[0].appends[1])

	// A new generation (a reallocated buffer) is a hole whatever the positions say.
	g2, l2 := newLoggedGate()
	scoreChunk(t, g2, positioned(modelChunk("s1", testVADModel, testVADBase), 1, 10*oneSecond, full))
	sc, ran := scoreChunk(t, g2, positioned(modelChunk("s1", testVADModel, testVADBase.Add(time.Second)), 2, 11*oneSecond, full))
	require.True(t, ran)
	assert.True(t, sc.span.from.Equal(testVADBase.Add(time.Second-3*time.Second)), "a new generation restarts coverage")
	assert.Len(t, l2.list[0].appends, 2)
}
