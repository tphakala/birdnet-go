package processor

import (
	"math"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/inference/vad"
)

// Geometry of the second model used by the multi-model tests: a 5 s clip at 32 kHz.
const (
	testVADModelB       = "Perch_V2"
	testVADRateB        = 32000
	testVADChunkBytesB  = 5 * testVADRateB * vadBytesPerSample
	testVADChunkSeconds = 3
)

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

// chunkOf builds a gap-free chunk of model at rate whose audio ends at end and
// whose length is clipSeconds long.
func chunkOf(source, model string, clipSeconds, rate int, end time.Time) *vadChunk {
	return &vadChunk{
		key:        vadStreamKey{source: source, model: model},
		pcm:        make([]byte, clipSeconds*rate*vadBytesPerSample),
		sampleRate: rate,
		audioEnd:   end,
	}
}

func sumInts(v []int) int {
	n := 0
	for _, x := range v {
		n += x
	}
	return n
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
		events = append(events, ev{at, "A"})
	}
	for at := time.Duration(0); at <= 6*time.Second; at += 333 * time.Millisecond {
		events = append(events, ev{at, "B"})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].at < events[j].at })

	g, l := newLoggedGate()
	flushes := map[string]int{}
	for _, e := range events {
		var ch *vadChunk
		if e.model == "A" {
			ch = chunkOf("s1", testVADModel, testVADChunkSeconds, testVADRate, testVADBase.Add(e.at))
		} else {
			ch = chunkOf("s1", testVADModelB, 5, testVADRateB, testVADBase.Add(e.at))
		}
		_, ran, _, _, err := g.score(&vad.Config{}, "k", ch)
		require.NoError(t, err)
		if ran {
			flushes[e.model]++
		}
	}

	require.Len(t, l.list, 2, "one streamer per (source, model)")
	a, b := l.list[0], l.list[1]
	assert.Zero(t, a.resets+b.resets, "interleaving models must never reset a stream")
	assert.Equal(t, testVADChunkBytes, a.appends[0])
	assert.Equal(t, testVADChunkBytesB, b.appends[0])
	for _, n := range a.appends[1:] {
		assert.Equal(t, 200*testVADRate*vadBytesPerSample/1000, n, "model A appends one 200 ms step")
	}
	for _, n := range b.appends[1:] {
		assert.Equal(t, 333*testVADRateB*vadBytesPerSample/1000, n, "model B appends one 333 ms step")
	}
	assert.Equal(t, 7, flushes["A"], "about one decision per second per stream")
	assert.InDelta(t, 5, flushes["B"], 1)
}

// TestVADGate_ThrottleMergesChunksAndSpansNewestAudio: however many chunks the
// 1 s throttle merges into a flush, the span ends at the flushing chunk's audio
// end, starts at the first chunk's audio start, and the appended audio is exactly
// the audio in between.
func TestVADGate_ThrottleMergesChunksAndSpansNewestAudio(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		step        time.Duration
		clipSeconds int
		rate        int
	}{
		{"3s clip step 200ms", 200 * time.Millisecond, 3, testVADRate},
		{"3s clip step 300ms", 300 * time.Millisecond, 3, testVADRate},
		{"3s clip step 600ms", 600 * time.Millisecond, 3, testVADRate},
		{"3s clip step 1000ms", time.Second, 3, testVADRate},
		{"5s clip step 333ms", 333 * time.Millisecond, 5, testVADRateB},
		{"5s clip step 1667ms", 1667 * time.Millisecond, 5, testVADRateB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g, l := newLoggedGate()
			clipDur := time.Duration(tt.clipSeconds) * time.Second
			firstStart := testVADBase.Add(-clipDur)
			flushed := 0
			for k := range 40 {
				end := testVADBase.Add(time.Duration(k) * tt.step)
				_, ran, _, span, err := g.score(&vad.Config{}, "k", chunkOf("s1", testVADModel, tt.clipSeconds, tt.rate, end))
				require.NoError(t, err)
				if !ran {
					continue
				}
				flushed++
				assert.True(t, span.to.Equal(end), "span ends at the flushing chunk's audio end")
				assert.True(t, span.from.Equal(firstStart), "span starts at the first chunk's audio start")
				wantBytes := int(math.Round(span.to.Sub(span.from).Seconds()*float64(tt.rate))) * vadBytesPerSample
				assert.Equal(t, wantBytes, sumInts(l.list[0].appends), "appended audio equals the audio between from and to")
			}
			assert.Greater(t, flushed, 2)
		})
	}
}

// TestVADGate_GapFlagResetsAndRestartsCoverage: a producer gap resets the stream
// and claims none of the spliced window as covered.
func TestVADGate_GapFlagResetsAndRestartsCoverage(t *testing.T) {
	t.Parallel()
	g, l := newLoggedGate()
	_, _, _, _, err := g.score(&vad.Config{}, "k", chunkOf("s1", testVADModel, 3, testVADRate, testVADBase))
	require.NoError(t, err)

	gapEnd := testVADBase.Add(time.Second)
	ch := chunkOf("s1", testVADModel, 3, testVADRate, gapEnd)
	ch.gap = true
	_, ran, _, span, err := g.score(&vad.Config{}, "k", ch)
	require.NoError(t, err)
	assert.True(t, ran, "a reset stream flushes immediately")
	assert.Equal(t, 1, l.list[0].resets)
	assert.True(t, span.from.Equal(gapEnd), "a gap claims none of the spliced window")

	next := testVADBase.Add(2 * time.Second)
	_, ran, _, span, err = g.score(&vad.Config{}, "k", chunkOf("s1", testVADModel, 3, testVADRate, next))
	require.NoError(t, err)
	assert.True(t, ran)
	assert.True(t, span.from.Equal(gapEnd), "later chunks extend the span from the gap")
	assert.True(t, span.to.Equal(next))
}

// TestVADGate_FirstWindowGapClaimsNothingOfTheZeroFilledHead: the first window of
// a buffer (gap) creates the stream with coverage starting at its audio end.
func TestVADGate_FirstWindowGapClaimsNothingOfTheZeroFilledHead(t *testing.T) {
	t.Parallel()
	g, _ := newLoggedGate()
	ch := chunkOf("s1", testVADModel, 3, testVADRate, testVADBase)
	ch.gap = true
	_, ran, _, span, err := g.score(&vad.Config{}, "k", ch)
	require.NoError(t, err)
	require.True(t, ran)
	assert.True(t, span.from.Equal(testVADBase))
}

func TestVADGate_HoleBeyondToleranceBreaksCoverage(t *testing.T) {
	t.Parallel()
	g, l := newLoggedGate()
	_, _, _, first, err := g.score(&vad.Config{}, "k", chunkOf("s1", testVADModel, 3, testVADRate, testVADBase))
	require.NoError(t, err)

	end := testVADBase.Add(3*time.Second + 300*time.Millisecond)
	_, ran, _, span, err := g.score(&vad.Config{}, "k", chunkOf("s1", testVADModel, 3, testVADRate, end))
	require.NoError(t, err)
	require.True(t, ran)
	assert.Zero(t, l.list[0].resets, "a hole inside the reset gap keeps the LSTM state")
	assert.True(t, span.from.Equal(end.Add(-3*time.Second)), "the unscored hole breaks coverage")
	assert.True(t, span.from.After(first.from))
}

func TestVADGate_HoleWithinToleranceKeepsCoverage(t *testing.T) {
	t.Parallel()
	g, l := newLoggedGate()
	_, _, _, first, err := g.score(&vad.Config{}, "k", chunkOf("s1", testVADModel, 3, testVADRate, testVADBase))
	require.NoError(t, err)

	end := testVADBase.Add(3*time.Second + 100*time.Millisecond)
	_, ran, _, span, err := g.score(&vad.Config{}, "k", chunkOf("s1", testVADModel, 3, testVADRate, end))
	require.NoError(t, err)
	require.True(t, ran)
	assert.Zero(t, l.list[0].resets)
	assert.True(t, span.from.Equal(first.from), "timing jitter inside the tolerance keeps the span")
}

// TestVADGate_ModelRemovedAndReaddedTransitions: one model stopping, returning or
// the gate closing never disturbs another model's stream.
func TestVADGate_ModelRemovedAndReaddedTransitions(t *testing.T) {
	t.Parallel()
	feed := func(t *testing.T, g *vadGate, model string, at time.Duration) (vadSpan, bool) {
		t.Helper()
		_, ran, _, span, err := g.score(&vad.Config{}, "k", chunkOf("s1", model, 3, testVADRate, testVADBase.Add(at)))
		require.NoError(t, err)
		return span, ran
	}
	warm := func(t *testing.T) (*vadGate, *streamerLog) {
		t.Helper()
		g, l := newLoggedGate()
		for s := range 4 {
			at := time.Duration(s) * time.Second
			feed(t, g, "A", at)
			feed(t, g, "B", at)
		}
		return g, l
	}

	t.Run("other model continues unaffected while one stops", func(t *testing.T) {
		t.Parallel()
		g, l := warm(t)
		var span vadSpan
		for s := 4; s < 15; s++ {
			sp, ran := feed(t, g, "A", time.Duration(s)*time.Second)
			require.True(t, ran)
			span = sp
		}
		assert.Zero(t, l.list[0].resets)
		assert.True(t, span.from.Equal(testVADBase.Add(-3*time.Second)), "A's span stays contiguous")
	})
	t.Run("model back after 10 s starts a new span", func(t *testing.T) {
		t.Parallel()
		g, l := warm(t)
		at := 13 * time.Second
		span, ran := feed(t, g, "B", at)
		require.True(t, ran)
		assert.Equal(t, 1, l.list[1].resets)
		assert.True(t, span.from.Equal(testVADBase.Add(at-3*time.Second)))
	})
	t.Run("model back after 500 ms stays contiguous", func(t *testing.T) {
		t.Parallel()
		g, l := warm(t)
		span, ran := feed(t, g, "B", 3500*time.Millisecond)
		_ = ran
		assert.Zero(t, l.list[1].resets)
		if ran {
			assert.True(t, span.from.Equal(testVADBase.Add(-3*time.Second)))
		}
	})
	t.Run("close rebuilds both streams with new spans", func(t *testing.T) {
		t.Parallel()
		g, l := warm(t)
		g.close()
		at := 5 * time.Second
		spanA, ranA := feed(t, g, "A", at)
		spanB, ranB := feed(t, g, "B", at)
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
		stepMs     = 200
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
		end := testVADBase.Add(time.Duration(k*stepMs) * time.Millisecond)
		item := makeVADItem(testVADModel, "s1", end, testVADChunkBytes)
		item.StartTime = end.Add(-time.Duration(pre+clipSecond) * time.Second)
		p.runVADGate(settings, item)
	}

	require.Len(t, l.list, 1)
	st := l.list[0]
	assert.Zero(t, st.resets)
	step := stepMs * testVADRate * vadBytesPerSample / 1000
	assert.Equal(t, testVADChunkBytes+(chunks-1)*step, sumInts(st.appends),
		"every chunk's fresh audio must reach the streamer")
	assert.Len(t, st.appends, chunks, "no chunk may be skipped as a duplicate")
}
