package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference/vad"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// Chunk geometry used across these tests: a 3 s analysis chunk at 48 kHz.
const (
	testVADRate       = 48000
	testVADChunkBytes = 3 * testVADRate * vadBytesPerSample // 288000
	testVADHalfSecond = testVADRate * vadBytesPerSample / 2 // 48000 = 0.5 s
)

// fakeSession is a vad.SpeechSession test double. The fake streamer never calls
// Run, so it only needs to satisfy the interface and record Close.
type fakeSession struct {
	closed   bool
	strategy string
}

func (f *fakeSession) Run(_, _, _ []float32) (probs, hOut, cOut []float32, err error) {
	return nil, nil, nil, nil
}

func (f *fakeSession) Close() error { f.closed = true; return nil }
func (f *fakeSession) Strategy() string {
	if f.strategy != "" {
		return f.strategy
	}
	return vad.StrategySequence
}

// fakeStreamer is a vad.Streamer test double with scripted output.
type fakeStreamer struct {
	appends  []int // byte length of every Append
	rates    []int // sample rate of every Append
	flushes  int
	resets   int
	prob     float32
	flushOK  bool
	flushErr error
}

func (f *fakeStreamer) Append(pcm []byte, rate int) error {
	f.appends = append(f.appends, len(pcm))
	f.rates = append(f.rates, rate)
	return nil
}

func (f *fakeStreamer) Flush(_ vad.SpeechSession) (prob float32, ok bool, framesRun int, err error) {
	f.flushes++
	return f.prob, f.flushOK, 1, f.flushErr
}

func (f *fakeStreamer) Reset() { f.resets++ }

// sessionFactory returns a newSession func that always yields sess (and counts builds).
func sessionFactory(sess vad.SpeechSession, buildErr error, builds *int) func(vad.Config) (vad.SpeechSession, error) {
	return func(vad.Config) (vad.SpeechSession, error) {
		if builds != nil {
			*builds++
		}
		if buildErr != nil {
			return nil, buildErr
		}
		return sess, nil
	}
}

// newTestGate wires a gate to one shared fake session and a streamer factory that
// yields st for every source (override g.newStreamer for multi-source tests).
func newTestGate(st vad.Streamer, sess vad.SpeechSession, builds *int) *vadGate {
	return &vadGate{
		newSession:  sessionFactory(sess, nil, builds),
		newStreamer: func() vad.Streamer { return st },
		streams:     map[string]*sourceStream{},
	}
}

func TestResolveVADModel(t *testing.T) {
	t.Parallel()

	// Explicit path override wins.
	cfg, key := resolveVADModel(&conf.VADSettings{ModelPath: "/custom/silero.onnx"}, "/lib.so")
	assert.Equal(t, "path:/custom/silero.onnx", key)
	assert.Equal(t, "/custom/silero.onnx", cfg.ModelPath)
	assert.Empty(t, cfg.ModelData)
	assert.Equal(t, "/lib.so", cfg.LibraryPath)

	// No path: fall back to the embedded model (present in the default build).
	cfg, key = resolveVADModel(&conf.VADSettings{}, "")
	if vad.HasEmbeddedModel() {
		assert.Equal(t, "embedded", key)
		assert.NotEmpty(t, cfg.ModelData, "embedded model bytes must be supplied")
		assert.Empty(t, cfg.ModelPath)
	} else {
		assert.Empty(t, key, "no model available without an embedded model or a path")
	}
}

func TestVADGate_LazyLoadAndReuse(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.9, flushOK: true}
	builds := 0
	g := newTestGate(st, &fakeSession{}, &builds)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	// First chunk builds the session, appends the whole chunk, and flushes.
	prob, ran, _, err := g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s1", base, testVADRate)
	require.NoError(t, err)
	assert.True(t, ran)
	assert.InDelta(t, 0.9, prob, 1e-6)
	assert.Equal(t, 1, builds)
	assert.Equal(t, []int{testVADChunkBytes}, st.appends)
	assert.Equal(t, 1, st.flushes)

	// A later chunk (past the throttle) reuses the same session.
	_, ran, _, err = g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s1", base.Add(2*time.Second), testVADRate)
	require.NoError(t, err)
	assert.True(t, ran)
	assert.Equal(t, 1, builds, "the shared session must not be rebuilt")
	assert.Equal(t, 2, st.flushes)
}

// TestVADGate_FreshTailSlicing pins the overlap decoupling: each overlapping
// chunk contributes only its non-overlapped tail.
func TestVADGate_FreshTailSlicing(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.1, flushOK: true}
	g := newTestGate(st, &fakeSession{}, nil)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	pcm := make([]byte, testVADChunkBytes)

	// t0: whole 3 s chunk is fresh.
	_, _, _, err := g.score(&vad.Config{}, "k", pcm, "s1", base, testVADRate)
	require.NoError(t, err)
	// t0+0.5s: 0.5 s of fresh audio (the tail).
	_, _, _, err = g.score(&vad.Config{}, "k", pcm, "s1", base.Add(500*time.Millisecond), testVADRate)
	require.NoError(t, err)
	// t0+1.0s: another 0.5 s tail (delta from the previous chunk's start).
	_, _, _, err = g.score(&vad.Config{}, "k", pcm, "s1", base.Add(1*time.Second), testVADRate)
	require.NoError(t, err)

	assert.Equal(t, []int{testVADChunkBytes, testVADHalfSecond, testVADHalfSecond}, st.appends,
		"only the non-overlapped tail of each chunk may be appended")
	assert.Zero(t, st.resets, "overlapping chunks must not reset the streamer")
}

// TestVADGate_ContiguousChunksDoNotReset guards the default-overlap case (step ==
// chunk length, so delta == chunkDur): the whole chunk is fresh but the LSTM
// state and aggregation window MUST be preserved, or speech straddling a chunk
// boundary would be split with no context.
func TestVADGate_ContiguousChunksDoNotReset(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.1, flushOK: true}
	g := newTestGate(st, &fakeSession{}, nil)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	pcm := make([]byte, testVADChunkBytes) // 3 s

	_, _, _, err := g.score(&vad.Config{}, "k", pcm, "s1", base, testVADRate)
	require.NoError(t, err)
	// Exactly one chunk later: perfectly contiguous, no overlap and no gap.
	_, _, _, err = g.score(&vad.Config{}, "k", pcm, "s1", base.Add(3*time.Second), testVADRate)
	require.NoError(t, err)

	assert.Equal(t, []int{testVADChunkBytes, testVADChunkBytes}, st.appends,
		"a contiguous chunk is wholly fresh")
	assert.Zero(t, st.resets, "a contiguous chunk must NOT reset the streamer state")
}

// TestVADGate_GapResetsStreamer: a gap well beyond one chunk (past the jitter
// tolerance) is a discontinuity that resets the streamer and treats the whole
// chunk as fresh.
func TestVADGate_GapResetsStreamer(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.1, flushOK: true}
	g := newTestGate(st, &fakeSession{}, nil)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	pcm := make([]byte, testVADChunkBytes)

	_, _, _, err := g.score(&vad.Config{}, "k", pcm, "s1", base, testVADRate)
	require.NoError(t, err)
	// 6 s later (>> 3 s chunk + 1 s tolerance): a genuine coverage gap.
	_, _, _, err = g.score(&vad.Config{}, "k", pcm, "s1", base.Add(6*time.Second), testVADRate)
	require.NoError(t, err)

	assert.Equal(t, 1, st.resets, "a gap well beyond one chunk must reset the streamer")
	assert.Equal(t, []int{testVADChunkBytes, testVADChunkBytes}, st.appends)
}

// TestVADGate_JitterWithinToleranceDoesNotReset guards against wall-clock jitter
// pushing a contiguous chunk's start-time delta just past chunkDur: a delta
// within the vadResetGap tolerance must be treated as a whole fresh chunk with
// the LSTM state preserved, not a discontinuity (Agent 6 finding).
func TestVADGate_JitterWithinToleranceDoesNotReset(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.1, flushOK: true}
	g := newTestGate(st, &fakeSession{}, nil)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	pcm := make([]byte, testVADChunkBytes) // 3 s

	_, _, _, err := g.score(&vad.Config{}, "k", pcm, "s1", base, testVADRate)
	require.NoError(t, err)
	// 3.3 s later: a contiguous overlap=0 boundary jittered 0.3 s past chunkDur,
	// still inside the 1 s tolerance.
	_, _, _, err = g.score(&vad.Config{}, "k", pcm, "s1", base.Add(3300*time.Millisecond), testVADRate)
	require.NoError(t, err)

	assert.Zero(t, st.resets, "jitter within tolerance must not reset the streamer")
	assert.Equal(t, []int{testVADChunkBytes, testVADChunkBytes}, st.appends,
		"an over-chunk delta clamps to the whole chunk, no dropped sample")
}

// TestVADGate_SampleRateChangeResets: a source changing sample rate is a
// discontinuity (carried state belongs to the old rate).
func TestVADGate_SampleRateChangeResets(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.1, flushOK: true}
	g := newTestGate(st, &fakeSession{}, nil)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, _, _, err := g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s1", base, testVADRate)
	require.NoError(t, err)
	_, _, _, err = g.score(&vad.Config{}, "k", make([]byte, 16000*vadBytesPerSample), "s1", base.Add(500*time.Millisecond), 16000)
	require.NoError(t, err)

	assert.Equal(t, 1, st.resets, "a sample-rate change must reset the streamer")
	assert.Equal(t, []int{16000}, st.rates[1:], "the new rate must be used after the reset")
}

// TestVADGate_FlushCadence pins the throttle's role: every accepted chunk is
// buffered, but a decision (Flush) happens at most once per vadSourceThrottle
// per source.
func TestVADGate_FlushCadence(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.9, flushOK: true}
	g := newTestGate(st, &fakeSession{}, nil)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	pcm := make([]byte, testVADChunkBytes)

	_, ran, _, err := g.score(&vad.Config{}, "k", pcm, "s1", base, testVADRate)
	require.NoError(t, err)
	assert.True(t, ran, "first chunk must produce a decision")

	// 0.5 s later: buffered but not flushed (within the cadence).
	_, ran, _, err = g.score(&vad.Config{}, "k", pcm, "s1", base.Add(500*time.Millisecond), testVADRate)
	require.NoError(t, err)
	assert.False(t, ran, "within the flush cadence: buffer only")
	assert.Equal(t, 1, st.flushes)
	assert.Len(t, st.appends, 2, "the fresh tail must still be appended")

	// 1.0 s after the last flush: flush again.
	_, ran, _, err = g.score(&vad.Config{}, "k", pcm, "s1", base.Add(1*time.Second), testVADRate)
	require.NoError(t, err)
	assert.True(t, ran)
	assert.Equal(t, 2, st.flushes)

	// A different source has its own streamer and cadence, flushing immediately.
	st2 := &fakeStreamer{prob: 0.9, flushOK: true}
	g.newStreamer = func() vad.Streamer { return st2 }
	_, ran, _, err = g.score(&vad.Config{}, "k", pcm, "s2", base.Add(1*time.Second), testVADRate)
	require.NoError(t, err)
	assert.True(t, ran, "a new source flushes immediately")
	assert.Equal(t, 1, st2.flushes)
}

// TestVADGate_FlushNotOKIsNotADecision: when Flush has no complete hop yet, no
// decision is reported and the cadence timestamp does not advance.
func TestVADGate_FlushNotOKIsNotADecision(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0, flushOK: false}
	g := newTestGate(st, &fakeSession{}, nil)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, ran, _, err := g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s1", base, testVADRate)
	require.NoError(t, err)
	assert.False(t, ran)

	// The next chunk may flush immediately (the cadence clock did not advance).
	st.flushOK = true
	st.prob = 0.9
	_, ran, _, err = g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s1", base.Add(500*time.Millisecond), testVADRate)
	require.NoError(t, err)
	assert.True(t, ran, "a not-ok flush must not consume the cadence slot")
}

// TestVADGate_OutOfOrderChunkIsSkipped: an out-of-order (earlier) chunk carries
// no new audio (it was already appended), so it is skipped entirely.
func TestVADGate_OutOfOrderChunkIsSkipped(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.9, flushOK: true}
	g := newTestGate(st, &fakeSession{}, nil)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	pcm := make([]byte, testVADChunkBytes)

	_, ran, _, err := g.score(&vad.Config{}, "k", pcm, "s1", base.Add(2*time.Second), testVADRate)
	require.NoError(t, err)
	assert.True(t, ran)
	require.Len(t, st.appends, 1)

	// An earlier chunk: delta <= 0, skipped without append or flush.
	_, ran, _, err = g.score(&vad.Config{}, "k", pcm, "s1", base.Add(500*time.Millisecond), testVADRate)
	require.NoError(t, err)
	assert.False(t, ran, "out-of-order chunk must be skipped")
	assert.Len(t, st.appends, 1, "no fresh audio must be appended for an out-of-order chunk")
	assert.Equal(t, 1, st.flushes)
}

func TestVADGate_ModelChangeRebuilds(t *testing.T) {
	t.Parallel()
	sess1 := &fakeSession{}
	sess2 := &fakeSession{}
	st := &fakeStreamer{prob: 0.9, flushOK: true}
	builds := 0
	g := &vadGate{
		newSession: func(vad.Config) (vad.SpeechSession, error) {
			builds++
			if builds == 1 {
				return sess1, nil
			}
			return sess2, nil
		},
		newStreamer: func() vad.Streamer { return st },
		streams:     map[string]*sourceStream{},
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, _, _, err := g.score(&vad.Config{}, "a", make([]byte, testVADChunkBytes), "s1", base, testVADRate)
	require.NoError(t, err)
	_, _, _, err = g.score(&vad.Config{}, "b", make([]byte, testVADChunkBytes), "s1", base.Add(2*time.Second), testVADRate)
	require.NoError(t, err)

	assert.Equal(t, 2, builds, "changing the model source must rebuild the session")
	assert.True(t, sess1.closed, "the old session must be closed on reload")
}

func TestVADGate_EmptyPathUnloads(t *testing.T) {
	t.Parallel()
	sess := &fakeSession{}
	st := &fakeStreamer{prob: 0.9, flushOK: true}
	g := newTestGate(st, sess, nil)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, ran, _, err := g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s1", base, testVADRate)
	require.NoError(t, err)
	assert.True(t, ran)

	// Empty key (uninstalled): session dropped, streamers cleared, no scoring.
	_, ran, _, err = g.score(&vad.Config{}, "", make([]byte, testVADChunkBytes), "s1", base.Add(2*time.Second), testVADRate)
	require.NoError(t, err)
	assert.False(t, ran)
	assert.True(t, sess.closed, "the session must be closed when the model source is cleared")
	assert.Empty(t, g.streams)
}

func TestVADGate_LoadFailureBacksOff(t *testing.T) {
	t.Parallel()
	builds := 0
	g := &vadGate{
		newSession:  sessionFactory(nil, errors.NewStd("load failed"), &builds),
		newStreamer: func() vad.Streamer { return &fakeStreamer{} },
		streams:     map[string]*sourceStream{},
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, ran, _, err := g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s1", base, testVADRate)
	require.Error(t, err)
	assert.False(t, ran)
	assert.Equal(t, 1, builds)

	// Immediately after a failure, a different source does not re-attempt (backoff).
	_, ran, _, err = g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s2", base.Add(2*time.Second), testVADRate)
	require.NoError(t, err)
	assert.False(t, ran)
	assert.Equal(t, 1, builds, "load must not be retried within the backoff window")
}

// TestVADGate_FlushErrorBacksOff proves a Flush failure drops the session and
// every streamer and enters the failure backoff instead of re-running inference
// on every chunk.
func TestVADGate_FlushErrorBacksOff(t *testing.T) {
	t.Parallel()
	sess := &fakeSession{}
	infErr := errors.Newf("inference boom").Component("test").Category(errors.CategoryModelLoad).Build()
	st := &fakeStreamer{flushErr: infErr}
	builds := 0
	g := newTestGate(st, sess, &builds)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, ran, _, err := g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s1", base, testVADRate)
	require.Error(t, err)
	assert.False(t, ran)
	assert.True(t, sess.closed, "an ONNX inference failure must drop the shared session")
	assert.Empty(t, g.streams)

	// A later chunk within the backoff must NOT rebuild or flush.
	_, ran, _, err = g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s2", base.Add(2*time.Second), testVADRate)
	require.NoError(t, err)
	assert.False(t, ran, "flush failure must back off, not retry every chunk")
	assert.Equal(t, 1, builds, "no rebuild within the backoff window")
}

// TestVADGate_DataErrorDropsStreamerNotSession proves a per-source data or
// resampling error (a non-inference category) drops only that source's streamer
// and keeps the shared session serving every other source, rather than tearing
// the whole gate down and entering a global backoff.
func TestVADGate_DataErrorDropsStreamerNotSession(t *testing.T) {
	t.Parallel()
	sess := &fakeSession{}
	dataErr := errors.Newf("bad pcm").Component("test").Category(errors.CategoryValidation).Build()
	st := &fakeStreamer{flushErr: dataErr}
	builds := 0
	g := newTestGate(st, sess, &builds)
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, ran, _, err := g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s1", base, testVADRate)
	require.Error(t, err)
	assert.False(t, ran)
	assert.False(t, sess.closed, "a data error must NOT tear down the shared session")
	assert.Equal(t, 1, st.resets, "the offending streamer is reset")
	_, held := g.streams["s1"]
	assert.False(t, held, "the offending source's streamer is dropped")

	// The session stays healthy: another source scores immediately, no backoff.
	st2 := &fakeStreamer{prob: 0.9, flushOK: true}
	g.newStreamer = func() vad.Streamer { return st2 }
	_, ran, _, err = g.score(&vad.Config{}, "k", make([]byte, testVADChunkBytes), "s2", base.Add(2*time.Second), testVADRate)
	require.NoError(t, err)
	assert.True(t, ran, "the shared session stays healthy for other sources")
	assert.Equal(t, 1, builds, "the session is not rebuilt (no teardown occurred)")
}

// TestVADGate_ConfigChangeClearsBackoff proves that correcting the model source
// after a failed load retries immediately instead of waiting out the failed
// source's backoff window.
func TestVADGate_ConfigChangeClearsBackoff(t *testing.T) {
	t.Parallel()
	good := &fakeSession{}
	builds := 0
	g := &vadGate{
		newSession: func(vad.Config) (vad.SpeechSession, error) {
			builds++
			if builds == 1 {
				return nil, errors.NewStd("bad model path")
			}
			return good, nil
		},
		newStreamer: func() vad.Streamer { return &fakeStreamer{prob: 0.9, flushOK: true} },
		streams:     map[string]*sourceStream{},
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, ran, _, err := g.score(&vad.Config{}, "path:/bad.onnx", make([]byte, testVADChunkBytes), "s1", base, testVADRate)
	require.Error(t, err)
	assert.False(t, ran)

	_, ran, _, err = g.score(&vad.Config{}, "path:/good.onnx", make([]byte, testVADChunkBytes), "s1", base.Add(2*time.Second), testVADRate)
	require.NoError(t, err)
	assert.True(t, ran, "a corrected model source must be retried immediately")
	assert.Equal(t, 2, builds)
}

// makeVADItem builds a minimal results item for the given source and chunk.
func makeVADItem(modelID, source string, start time.Time, pcmLen int) *classifier.Results {
	return &classifier.Results{
		ModelID:   modelID,
		PCMdata:   make([]byte, pcmLen),
		Source:    datastore.AudioSource{ID: source},
		StartTime: start,
	}
}

func vadSettings(enabled bool, threshold float64) *conf.Settings {
	s := &conf.Settings{}
	s.Realtime.PrivacyFilter.Enabled = true
	s.Realtime.PrivacyFilter.VAD.Enabled = enabled
	s.Realtime.PrivacyFilter.VAD.Threshold = threshold
	s.Realtime.PrivacyFilter.VAD.ModelPath = "/fake/silero.onnx"
	return s
}

func TestRunVADGate_SpeechWritesLastHumanDetection(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.9, flushOK: true} // above threshold
	p := &Processor{
		LastHumanDetection: map[string]HumanDetection{},
		vadGate:            newTestGate(st, &fakeSession{}, nil),
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	item := makeVADItem("BirdNET_V2.4", "src1", base, testVADChunkBytes)

	p.runVADGate(vadSettings(true, 0.35), item)

	got, ok := p.LastHumanDetection["src1"]
	require.True(t, ok, "speech hit must record LastHumanDetection")
	assert.Equal(t, base, got.Time)
	assert.Equal(t, metrics.TriggerVAD, got.Trigger, "VAD path must tag the trigger")
}

func TestRunVADGate_BelowThresholdDoesNotWrite(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.1, flushOK: true} // below threshold
	p := &Processor{
		LastHumanDetection: map[string]HumanDetection{},
		vadGate:            newTestGate(st, &fakeSession{}, nil),
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	p.runVADGate(vadSettings(true, 0.35), makeVADItem("BirdNET_V2.4", "src1", base, testVADChunkBytes))

	_, ok := p.LastHumanDetection["src1"]
	assert.False(t, ok, "below-threshold speech must not record a human detection")
	// Prove the "no write" verdict came from the threshold comparison, not an
	// upstream early-return: a flush must actually have happened.
	assert.Equal(t, 1, st.flushes, "the chunk must actually have been scored")
}

func TestRunVADGate_AtThresholdWrites(t *testing.T) {
	t.Parallel()
	// 0.5 is exactly representable in both float32 and float64, so the boundary
	// is unambiguous and this genuinely pins the >= (not >) comparison.
	st := &fakeStreamer{prob: 0.5, flushOK: true}
	p := &Processor{
		LastHumanDetection: map[string]HumanDetection{},
		vadGate:            newTestGate(st, &fakeSession{}, nil),
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	p.runVADGate(vadSettings(true, 0.5), makeVADItem("BirdNET_V2.4", "src1", base, testVADChunkBytes))

	got, ok := p.LastHumanDetection["src1"]
	require.True(t, ok, "prob == threshold must record a human detection (>= comparison)")
	assert.Equal(t, base, got.Time)
}

func TestRunVADGate_DisabledIsInert(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.99, flushOK: true}
	builds := 0
	p := &Processor{
		LastHumanDetection: map[string]HumanDetection{},
		vadGate:            newTestGate(st, &fakeSession{}, &builds),
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	p.runVADGate(vadSettings(false, 0.35), makeVADItem("BirdNET_V2.4", "src1", base, testVADChunkBytes))

	assert.Empty(t, p.LastHumanDetection, "disabled VAD must not write")
	assert.Equal(t, 0, builds, "disabled VAD must not build a session")
}

func TestRunVADGate_SkipsBatAndEmptyPCM(t *testing.T) {
	t.Parallel()
	st := &fakeStreamer{prob: 0.99, flushOK: true}
	p := &Processor{
		LastHumanDetection: map[string]HumanDetection{},
		vadGate:            newTestGate(st, &fakeSession{}, nil),
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	// Ultrasonic bat chunk: skipped.
	p.runVADGate(vadSettings(true, 0.35), makeVADItem(classifier.RegistryIDBat, "src1", base, testVADChunkBytes))
	// Empty PCM: skipped.
	p.runVADGate(vadSettings(true, 0.35), makeVADItem("BirdNET_V2.4", "src2", base, 0))

	assert.Empty(t, p.LastHumanDetection)
	assert.Empty(t, st.appends, "bat and empty-PCM chunks must not be buffered")
	assert.Zero(t, st.flushes, "bat and empty-PCM chunks must not be scored")
}

func TestNewVADGate(t *testing.T) {
	t.Parallel()
	g := newVADGate()
	require.NotNil(t, g)
	assert.NotNil(t, g.streams, "streams must be initialised to avoid a nil-map write panic")
	assert.NotNil(t, g.newSession, "newSession must default to vad.NewSession")
	assert.NotNil(t, g.newStreamer, "newStreamer must default to a vad.NewStreamer factory")
}

func TestClampVADThreshold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "in range unchanged", in: 0.35, want: 0.35},
		{name: "zero clamps up to min", in: 0, want: vadMinThreshold},
		{name: "negative clamps up to min", in: -1, want: vadMinThreshold},
		{name: "above one clamps to max", in: 2.0, want: vadMaxThreshold},
		{name: "exactly max stays", in: 1.0, want: 1.0},
		{name: "exactly min stays", in: vadMinThreshold, want: vadMinThreshold},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.InDelta(t, tt.want, clampVADThreshold(tt.in), 1e-9)
		})
	}
}

// TestFreshTailBytes pins the byte math for the non-overlapped tail slice.
func TestFreshTailBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		delta  time.Duration
		rate   int
		pcmLen int
		want   int
	}{
		{name: "half second at 48k", delta: 500 * time.Millisecond, rate: 48000, pcmLen: testVADChunkBytes, want: testVADHalfSecond},
		{name: "full chunk when delta equals chunk", delta: 3 * time.Second, rate: 48000, pcmLen: testVADChunkBytes, want: testVADChunkBytes},
		{name: "clamped to pcm length", delta: 10 * time.Second, rate: 48000, pcmLen: testVADChunkBytes, want: testVADChunkBytes},
		{name: "zero delta is zero", delta: 0, rate: 48000, pcmLen: testVADChunkBytes, want: 0},
		{name: "negative delta clamps to zero", delta: -time.Second, rate: 48000, pcmLen: testVADChunkBytes, want: 0},
		{name: "rounds to nearest sample not truncated", delta: 46439909 * time.Nanosecond, rate: 44100, pcmLen: 4096, want: 4096},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, freshTailBytes(tt.delta, tt.rate, tt.pcmLen))
		})
	}
}
