package vad

import (
	"encoding/binary"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubSession is a SpeechSession test double. It records every Run's inputs and
// returns queued per-hop probabilities plus marker h/c states, so the streamer's
// buffer bookkeeping is fully testable without ONNX Runtime.
type stubSession struct {
	frames [][]float32 // copy of the frames argument, per run
	hIns   [][]float32 // copy of hIn, per run
	cIns   [][]float32 // copy of cIn, per run
	// probQueue holds one per-hop probability slice per expected run; a missing
	// or short entry yields zeros for the remaining hops.
	probQueue [][]float32
	runIdx    int
	err       error
	closed    bool
}

func (s *stubSession) Run(frames, hIn, cIn []float32) (probs, hOut, cOut []float32, err error) {
	if s.err != nil {
		return nil, nil, nil, s.err
	}
	s.frames = append(s.frames, slices.Clone(frames))
	s.hIns = append(s.hIns, slices.Clone(hIn))
	s.cIns = append(s.cIns, slices.Clone(cIn))

	n := len(frames) / modelInputSamples
	probs = make([]float32, n)
	if s.runIdx < len(s.probQueue) {
		copy(probs, s.probQueue[s.runIdx])
	}
	s.runIdx++
	// Marker states: run k (1-based) returns h filled with k and c with -k, so
	// state threading across Flushes is observable at the next run's hIn/cIn.
	hOut = make([]float32, stateWidth)
	cOut = make([]float32, stateWidth)
	for i := range hOut {
		hOut[i] = float32(s.runIdx)
		cOut[i] = -float32(s.runIdx)
	}
	return probs, hOut, cOut, nil
}

func (s *stubSession) Close() error     { s.closed = true; return nil }
func (s *stubSession) Strategy() string { return StrategySequence }

func newTestStreamer(t *testing.T) *streamer {
	t.Helper()
	return &streamer{minConsec: defaultMinConsecutiveFrames}
}

// pcm16Ramp returns n distinguishable PCM16 samples (values 1..n) as bytes,
// alongside the float32 samples to16k's 16 kHz passthrough will produce.
func pcm16Ramp(t *testing.T, n int) (pcm []byte, want []float32) {
	t.Helper()
	pcm = make([]byte, n*bytesPerSample)
	for i := range n {
		binary.LittleEndian.PutUint16(pcm[i*bytesPerSample:], uint16(i+1)) //nolint:gosec // G115: small positive test values
	}
	want = make([]float32, n)
	pcm16ToFloat32(pcm, want)
	return pcm, want
}

// repeatProbs returns n copies of v (a canned per-hop probability slice).
func repeatProbs(v float32, n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestStreamer_FlushEmitsCompleteHopsAndRetainsRemainder(t *testing.T) {
	t.Parallel()
	stub := &stubSession{probQueue: [][]float32{repeatProbs(0.9, 1), repeatProbs(0.9, 1)}}
	s := newTestStreamer(t)

	// 700 samples: one complete 512-hop, 188-sample remainder retained.
	pcm, want := pcm16Ramp(t, 700)
	require.NoError(t, s.Append(pcm, sampleRate16k))
	_, ok, framesRun, err := s.Flush(stub)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, framesRun)
	require.Len(t, stub.frames, 1)
	require.Len(t, stub.frames[0], modelInputSamples)
	// Row 0: zero context (fresh stream), window = first 512 samples.
	for i := range contextSamples {
		require.Zero(t, stub.frames[0][i], "fresh stream context must be zero")
	}
	assert.Equal(t, want[:windowSamples], stub.frames[0][contextSamples:])
	assert.Len(t, s.pending, 700-windowSamples, "sub-hop remainder must be retained")

	// 324 more samples completes the second hop (188 + 324 = 512): the window
	// must be the ORIGINAL samples 512..700 followed by the new 324, and the
	// context must be samples 448..512 (the tail of the last inferred hop).
	pcm2 := make([]byte, 324*bytesPerSample)
	for i := range 324 {
		binary.LittleEndian.PutUint16(pcm2[i*bytesPerSample:], uint16(1000+i)) //nolint:gosec // G115: small positive test values
	}
	want2 := make([]float32, 324)
	pcm16ToFloat32(pcm2, want2)

	require.NoError(t, s.Append(pcm2, sampleRate16k))
	_, ok, framesRun, err = s.Flush(stub)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, framesRun)
	require.Len(t, stub.frames, 2)
	assert.Equal(t, want[windowSamples-contextSamples:windowSamples], stub.frames[1][:contextSamples],
		"hop context must carry across Flushes")
	assert.Equal(t, want[windowSamples:700], stub.frames[1][contextSamples:contextSamples+188])
	assert.Equal(t, want2, stub.frames[1][contextSamples+188:])
	assert.Empty(t, s.pending)
}

func TestStreamer_NoCompleteHopIsNotOK(t *testing.T) {
	t.Parallel()
	stub := &stubSession{}
	s := newTestStreamer(t)

	pcm, _ := pcm16Ramp(t, 100) // < 512 samples
	require.NoError(t, s.Append(pcm, sampleRate16k))
	prob, ok, framesRun, err := s.Flush(stub)
	require.NoError(t, err)
	assert.False(t, ok, "no complete hop must report ok=false")
	assert.Zero(t, prob)
	assert.Zero(t, framesRun)
	assert.Empty(t, stub.frames, "no inference must run without a complete hop")
	assert.Len(t, s.pending, 100, "buffered samples must be retained")
}

func TestStreamer_EmptyFlushIsNotOK(t *testing.T) {
	t.Parallel()
	s := newTestStreamer(t)
	_, ok, framesRun, err := s.Flush(&stubSession{})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, framesRun)
}

func TestStreamer_StateThreadsAcrossFlushes(t *testing.T) {
	t.Parallel()
	stub := &stubSession{}
	s := newTestStreamer(t)

	pcm, _ := pcm16Ramp(t, windowSamples)
	require.NoError(t, s.Append(pcm, sampleRate16k))
	_, _, _, err := s.Flush(stub)
	require.NoError(t, err)
	require.NoError(t, s.Append(pcm, sampleRate16k))
	_, _, _, err = s.Flush(stub)
	require.NoError(t, err)

	require.Len(t, stub.hIns, 2)
	assert.Equal(t, repeatProbs(0, stateWidth), stub.hIns[0], "first run must start from zero state")
	assert.Equal(t, repeatProbs(0, stateWidth), stub.cIns[0])
	assert.Equal(t, repeatProbs(1, stateWidth), stub.hIns[1], "second run must receive run 1's hOut")
	assert.Equal(t, repeatProbs(-1, stateWidth), stub.cIns[1], "second run must receive run 1's cOut")
}

// TestStreamer_RollingWindowSemantics proves the ring reproduces the old
// full-chunk aggregation window: speech inferred in an earlier Flush keeps the
// aggregate high until it ages out of the last aggWindowFrames hops, exactly as
// it stayed high while inside the old 3 s chunk.
func TestStreamer_RollingWindowSemantics(t *testing.T) {
	t.Parallel()
	const hopsPerFlush = 40
	stub := &stubSession{probQueue: [][]float32{
		repeatProbs(0.9, hopsPerFlush), // flush 1: speech
		repeatProbs(0.0, hopsPerFlush), // flushes 2..4: silence
		repeatProbs(0.0, hopsPerFlush),
		repeatProbs(0.0, hopsPerFlush),
	}}
	s := newTestStreamer(t)
	pcm, _ := pcm16Ramp(t, hopsPerFlush*windowSamples)

	flush := func() float32 {
		t.Helper()
		require.NoError(t, s.Append(pcm, sampleRate16k))
		prob, ok, framesRun, err := s.Flush(stub)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, hopsPerFlush, framesRun)
		return prob
	}

	assert.InDelta(t, 0.9, flush(), 1e-6, "speech flush must score high")
	assert.InDelta(t, 0.9, flush(), 1e-6, "speech still inside the rolling window must keep the aggregate high")
	assert.Len(t, s.ring, 2*hopsPerFlush)

	// Third flush: ring reaches 120 hops and trims to aggWindowFrames (94), which
	// still holds 14 of the 0.9 hops (>= minConsec), so the aggregate stays high.
	assert.InDelta(t, 0.9, flush(), 1e-6)
	assert.Len(t, s.ring, aggWindowFrames, "ring must trim to the aggregation window")

	// Fourth flush: every 0.9 hop has aged out of the last 94; silence reigns.
	assert.InDelta(t, 0.0, flush(), 1e-6, "speech older than the window must age out")
}

func TestStreamer_ResetClearsAllCarriedState(t *testing.T) {
	t.Parallel()
	stub := &stubSession{probQueue: [][]float32{repeatProbs(0.9, 1)}}
	s := newTestStreamer(t)

	pcm, _ := pcm16Ramp(t, windowSamples+100)
	require.NoError(t, s.Append(pcm, sampleRate16k))
	_, _, _, err := s.Flush(stub)
	require.NoError(t, err)

	s.Reset()
	assert.Empty(t, s.ring)
	assert.Empty(t, s.pending)
	assert.Empty(t, s.raw)
	assert.Zero(t, s.rate)
	assert.Equal(t, repeatProbs(0, stateWidth), s.h[:], "h must re-zero on Reset")
	assert.Equal(t, repeatProbs(0, stateWidth), s.c[:], "c must re-zero on Reset")
	assert.Equal(t, repeatProbs(0, contextSamples), s.ctx[:], "hop context must re-zero on Reset")

	// The next run after Reset must start from zero state and zero context.
	require.NoError(t, s.Append(pcm, sampleRate16k))
	_, _, _, err = s.Flush(stub)
	require.NoError(t, err)
	require.Len(t, stub.hIns, 2)
	assert.Equal(t, repeatProbs(0, stateWidth), stub.hIns[1])
	for i := range contextSamples {
		assert.Zero(t, stub.frames[1][i], "post-Reset context must be zero")
	}
}

func TestStreamer_SampleRateChangeErrorsAndResets(t *testing.T) {
	t.Parallel()
	s := newTestStreamer(t)
	pcm, _ := pcm16Ramp(t, 100)

	require.NoError(t, s.Append(pcm, sampleRate16k))
	err := s.Append(pcm, 48000)
	require.Error(t, err, "a sample-rate change must be rejected")
	assert.Empty(t, s.raw, "self-reset must drop buffered audio")
	assert.Zero(t, s.rate)

	// After the self-reset, the new rate is accepted.
	require.NoError(t, s.Append(pcm, 48000))
	assert.Equal(t, 48000, s.rate)
}

func TestStreamer_AppendValidation(t *testing.T) {
	t.Parallel()
	s := newTestStreamer(t)
	require.Error(t, s.Append([]byte{0x01}, sampleRate16k), "odd byte length must be rejected")
	require.Error(t, s.Append([]byte{0, 0}, 0), "non-positive sample rate must be rejected")
	require.NoError(t, s.Append(nil, sampleRate16k), "empty append is a no-op")
}

func TestStreamer_FlushErrorPropagates(t *testing.T) {
	t.Parallel()
	stub := &stubSession{err: ErrSessionClosed}
	s := newTestStreamer(t)
	pcm, _ := pcm16Ramp(t, windowSamples)
	require.NoError(t, s.Append(pcm, sampleRate16k))
	_, _, _, err := s.Flush(stub)
	require.Error(t, err)
}

func TestNewStreamer_DefaultsMinConsec(t *testing.T) {
	t.Parallel()
	s, ok := NewStreamer(0).(*streamer)
	require.True(t, ok)
	assert.Equal(t, defaultMinConsecutiveFrames, s.minConsec)

	s2, ok := NewStreamer(5).(*streamer)
	require.True(t, ok)
	assert.Equal(t, 5, s2.minConsec)
}
