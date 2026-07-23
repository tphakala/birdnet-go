package audiocore

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
)

// collectingConsumer accumulates every byte handed to Write so a test can
// compare the whole processed stream rather than individual frames. Write
// copies the payload because the router recycles frame buffers as soon as
// Write returns, and counts calls so a test can tell "the router skipped this
// frame" apart from "the router wrote an empty frame".
type collectingConsumer struct {
	mockConsumer
	out    []byte
	writes int
}

func newCollectingConsumer(sampleRate int) *collectingConsumer {
	return &collectingConsumer{
		mockConsumer: mockConsumer{
			id:         "align-consumer",
			sampleRate: sampleRate,
			bitDepth:   16,
			channels:   1,
		},
	}
}

func (c *collectingConsumer) Write(frame AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	c.out = append(c.out, frame.Data...)
	c.writes++
	return nil
}

// alignTestSignal builds sampleCount mono PCM16 samples with strong sample-to-sample
// variation. A trivial signal (silence, a constant, or a slow ramp) would hide the
// defect this file guards: byte-swapping silence yields silence, so the stream can
// desynchronise without changing a single output byte. The alternating sign and the
// odd stride keep both bytes of every sample distinct from its neighbours, so a
// one-byte shift is guaranteed to change the output.
func alignTestSignal(sampleCount int) []byte {
	data := make([]byte, sampleCount*2)
	for i := range sampleCount {
		v := int16((i*2731)%40000 - 20000) //nolint:gosec // G115: intentional wrap for a synthetic test signal
		if i%2 == 1 {
			v = -v
		}
		data[i*2] = byte(v)        //nolint:gosec // G115: intentional int16 to byte truncation for PCM serialisation
		data[i*2+1] = byte(v >> 8) //nolint:gosec // G115: intentional int16 to byte truncation for PCM serialisation
	}
	return data
}

// splitAt cuts src into consecutive chunks of the given sizes. The sizes must
// sum to len(src).
func splitAt(t *testing.T, src []byte, sizes []int) [][]byte {
	t.Helper()
	chunks := make([][]byte, 0, len(sizes))
	off := 0
	for _, n := range sizes {
		require.LessOrEqual(t, off+n, len(src), "chunk sizes overrun the source signal")
		chunks = append(chunks, src[off:off+n])
		off += n
	}
	require.Equal(t, len(src), off, "chunk sizes must consume the whole source signal")
	return chunks
}

// alignRunOpts configures one run through a hand-built route.
type alignRunOpts struct {
	gainLinear float64
	srcRate    int
	dstRate    int
	bitDepth   int
}

// alignRunResult is everything a test needs to assert on after a run.
type alignRunResult struct {
	out          []byte
	writes       int
	routeErrors  int64
	realignments int64
}

// runChunksThroughRoute feeds chunks through handleRouteFrame on a freshly built
// route. Calling handleRouteFrame directly (as the benchmarks do) keeps the test
// deterministic: it exercises the same align/resample/process/write path as the
// drainer goroutine without depending on channel scheduling.
//
// The realignments count is returned so every test can assert that alignment
// actually engaged. Without that assertion a future edit to the chunk sizes that
// accidentally made every chunk even would leave the comparisons green and
// vacuous, comparing two identical control runs.
func runChunksThroughRoute(t *testing.T, chunks [][]byte, opts alignRunOpts) alignRunResult {
	t.Helper()

	r := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(r.Close)

	consumer := newCollectingConsumer(opts.dstRate)
	route := newAlignTestRoute(t, r, consumer, opts)

	for _, chunk := range chunks {
		r.handleRouteFrame(AudioFrame{
			SourceID:   "align-src",
			SourceName: "align",
			Data:       chunk,
			SampleRate: opts.srcRate,
			BitDepth:   opts.bitDepth,
			Channels:   1,
		}, route)
	}

	return alignRunResult{
		out:          consumer.out,
		writes:       consumer.writes,
		routeErrors:  route.errors.Load(),
		realignments: route.realignments.Load(),
	}
}

// newAlignTestRoute builds a Route wired to consumer, adding a resampler only
// when the rates differ (mirroring what AddRoute does).
func newAlignTestRoute(t *testing.T, r *AudioRouter, consumer AudioConsumer, opts alignRunOpts) *Route {
	t.Helper()
	_ = r
	route := &Route{
		SourceID:         "align-src",
		Consumer:         consumer,
		sourceSampleRate: opts.srcRate,
		gainLinear:       opts.gainLinear,
		inbox:            make(chan AudioFrame, 1),
		done:             make(chan struct{}),
		stopped:          make(chan struct{}),
	}
	if opts.srcRate != opts.dstRate {
		rs, err := resample.NewResampler(opts.srcRate, opts.dstRate)
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, rs.Close()) })
		route.resampler = rs
	}
	return route
}

// TestRouter_OddLengthFramesDoNotDesyncGainPath is the regression test for the
// router discarding the odd trailing byte of a PCM16 frame on the EQ/gain path.
// Dropping half a sample is not a truncation, it is a desynchronisation: every
// following sample is then read from the wrong byte offset and decodes as noise.
//
// The assertion is exact byte equality against a control run that carries the
// same total byte stream in even-length chunks. Because the odd chunk sizes sum
// to an even total, nothing is left in the carry at the end and the two runs must
// produce identical output.
func TestRouter_OddLengthFramesDoNotDesyncGainPath(t *testing.T) {
	t.Parallel()

	const (
		sampleCount = 2000
		// -6 dB, chosen so no sample clamps and the transform stays lossless
		// enough for an exact comparison between the two chunkings.
		gainLinear = 0.5
		rate       = 48000
	)
	signal := alignTestSignal(sampleCount)
	opts := alignRunOpts{gainLinear: gainLinear, srcRate: rate, dstRate: rate, bitDepth: 16}

	control := runChunksThroughRoute(t, splitAt(t, signal, []int{1000, 1000, 1000, 1000}), opts)
	require.Zero(t, control.routeErrors, "even-length control run must not produce route errors")
	require.Zero(t, control.realignments, "even-length control run must not realign anything")
	require.Len(t, control.out, len(signal), "control run must emit every input sample")

	got := runChunksThroughRoute(t, splitAt(t, signal, []int{999, 1001, 997, 1003}), opts)
	assert.Zero(t, got.routeErrors, "odd-length frames must not produce route errors")
	assert.Positive(t, got.realignments, "the odd chunking must actually exercise the carry")
	assert.Equal(t, control.out, got.out,
		"odd-length frames must yield the same processed sample stream as even-length frames; "+
			"a mismatch means the trailing byte was dropped and the stream desynchronised")
}

// TestRouter_OddLengthFramesSurviveResampling covers the second consumer of
// whole-sample framing on this path. resample.ResampleTo rejects an odd byte
// count outright, so before the carry existed an odd frame on a resampling route
// was dropped entirely and reported as a route error (and a telemetry event) for
// every frame.
func TestRouter_OddLengthFramesSurviveResampling(t *testing.T) {
	t.Parallel()

	const (
		sampleCount = 2400
		srcRate     = 48000
		dstRate     = 32000
	)
	signal := alignTestSignal(sampleCount)
	opts := alignRunOpts{gainLinear: 1.0, srcRate: srcRate, dstRate: dstRate, bitDepth: 16}

	control := runChunksThroughRoute(t, splitAt(t, signal, []int{1200, 1200, 1200, 1200}), opts)
	require.Zero(t, control.routeErrors, "even-length control run must not produce route errors")
	require.NotEmpty(t, control.out, "control run must emit resampled audio")

	got := runChunksThroughRoute(t, splitAt(t, signal, []int{1199, 1201, 1197, 1203}), opts)
	assert.Zero(t, got.routeErrors, "odd-length frames must not be rejected by the resampler")
	assert.Positive(t, got.realignments, "the odd chunking must actually exercise the carry")
	// Exact equality relies on go-audio-resampler being chunk-boundary
	// invariant: a fixed-length-kernel convolution produces identical output for
	// identical input regardless of how the input was split. If a resampler
	// upgrade ever breaks this test with both runs individually plausible,
	// suspect that property before suspecting the alignment code.
	assert.Equal(t, control.out, got.out,
		"odd-length frames must yield the same resampled stream as even-length frames")
}

// TestRouter_PassThroughPCM16RealignsOddFrames pins the alignment gate covering
// pass-through routes, which is the default configuration (no resampler, no EQ,
// 0 dB gain). The analysis consumers all read a pass-through route, so aligning
// only the routes that resample or process would leave them enforcing the
// whole-sample invariant nowhere.
func TestRouter_PassThroughPCM16RealignsOddFrames(t *testing.T) {
	t.Parallel()

	const rate = 48000
	signal := alignTestSignal(2000)
	opts := alignRunOpts{gainLinear: 1.0, srcRate: rate, dstRate: rate, bitDepth: 16}

	control := runChunksThroughRoute(t, splitAt(t, signal, []int{1000, 1000, 1000, 1000}), opts)
	require.Zero(t, control.realignments, "even-length control run must not realign anything")
	require.Equal(t, signal, control.out, "a pass-through route must emit the stream unchanged")

	got := runChunksThroughRoute(t, splitAt(t, signal, []int{999, 1001, 997, 1003}), opts)
	assert.Zero(t, got.routeErrors, "odd-length frames must not produce route errors")
	assert.Positive(t, got.realignments, "a pass-through PCM16 route must realign odd frames")
	assert.Equal(t, signal, got.out,
		"a pass-through route must reassemble the same sample stream from odd-length frames")
}

// TestRouter_NonPCM16PassThroughIsUntouched pins the other half of the gate. A
// 2-byte carry applied to a stream that is not 16-bit PCM would regroup its frame
// boundaries and permanently withhold a trailing byte at stream end, so frames
// declaring another bit depth must pass through byte for byte.
func TestRouter_NonPCM16PassThroughIsUntouched(t *testing.T) {
	t.Parallel()

	const rate = 48000
	sizes := []int{999, 1001, 997, 1003}
	signal := alignTestSignal(2000)
	chunks := splitAt(t, signal, sizes)

	r := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(r.Close)
	consumer := newCollectingConsumer(rate)
	route := newAlignTestRoute(t, r, consumer, alignRunOpts{gainLinear: 1.0, srcRate: rate, dstRate: rate})

	for _, chunk := range chunks {
		r.handleRouteFrame(AudioFrame{
			SourceID: "align-src", SourceName: "align",
			Data: chunk, SampleRate: rate,
			BitDepth: 8, Channels: 1,
		}, route)
	}

	assert.Equal(t, len(chunks), consumer.writes, "every non-PCM16 frame must reach the consumer")
	assert.Zero(t, route.realignments.Load(), "a non-PCM16 route must not realign")
	assert.Equal(t, signal, consumer.out, "a non-PCM16 route must pass every byte through unchanged")
}

// TestRouter_AllCarryFrameSkipsWrite covers the frame that disappears entirely
// into the carry. A frame shorter than one sample leaves nothing to hand
// downstream, and consumers are not documented to accept a zero-length frame, so
// the router must skip the write rather than deliver an empty one.
func TestRouter_AllCarryFrameSkipsWrite(t *testing.T) {
	t.Parallel()

	const rate = 48000
	signal := alignTestSignal(2000)
	// The leading 1-byte chunk is consumed entirely into the carry, so only
	// three of the four chunks produce a write. The sizes still sum to an even
	// total, so nothing is stranded at the end.
	chunks := splitAt(t, signal, []int{1, 999, 1001, 1999})
	opts := alignRunOpts{gainLinear: 1.0, srcRate: rate, dstRate: rate, bitDepth: 16}

	got := runChunksThroughRoute(t, chunks, opts)

	assert.Equal(t, len(chunks)-1, got.writes,
		"the sub-sample frame must be withheld, not written as an empty frame")
	assert.Positive(t, got.realignments, "the sub-sample frame must engage the carry")
	assert.Equal(t, signal, got.out, "no byte may be lost when a whole frame is carried")
}

// TestRouter_StrandedFinalByteIsWithheld covers the one byte that legitimately
// does not come out: a stream ending mid-sample has nothing left to pair its
// last byte with, so it stays in the carry rather than being emitted alone.
func TestRouter_StrandedFinalByteIsWithheld(t *testing.T) {
	t.Parallel()

	const rate = 48000
	signal := alignTestSignal(2000)
	withStray := append(bytes.Clone(signal), 0x7f)
	opts := alignRunOpts{gainLinear: 1.0, srcRate: rate, dstRate: rate, bitDepth: 16}

	got := runChunksThroughRoute(t, splitAt(t, withStray, []int{999, 1001, 997, 1004}), opts)

	assert.Positive(t, got.realignments, "the odd chunking must exercise the carry")
	assert.Equal(t, signal, got.out, "the unpaired trailing byte must stay in the carry")
}

// TestRouter_ResetAlignmentDropsStalePartialSample covers a stream discontinuity.
// A Route outlives an FFmpeg process restart, so a half-sample held when the old
// stream died would be prepended to the first frame of the new one, shifting
// every sample that follows it for the life of the route.
func TestRouter_ResetAlignmentDropsStalePartialSample(t *testing.T) {
	t.Parallel()

	const rate = 48000
	first := alignTestSignal(500)   // 1000 bytes
	second := alignTestSignal(1000) // 2000 bytes, a fresh whole stream

	r := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(r.Close)
	consumer := newCollectingConsumer(rate)
	route := newAlignTestRoute(t, r, consumer, alignRunOpts{gainLinear: 1.0, srcRate: rate, dstRate: rate})

	writeFrame := func(data []byte) {
		r.handleRouteFrame(AudioFrame{
			SourceID: "align-src", SourceName: "align",
			Data: data, SampleRate: rate,
			BitDepth: 16, Channels: 1,
		}, route)
	}

	// End the first stream mid-sample: 999 of 1000 bytes go out, one is held.
	writeFrame(first[:999])
	require.Equal(t, 1, route.carryLen, "the truncated first stream must leave a byte in the carry")

	// Register the route so ResetAlignment can find it by source ID, then
	// deregister it so the router's own teardown does not wait on a drainer
	// goroutine this test never started.
	r.mu.Lock()
	r.routes["align-src"] = []*Route{route}
	r.mu.Unlock()
	r.ResetAlignment("align-src")
	r.mu.Lock()
	delete(r.routes, "align-src")
	r.mu.Unlock()

	require.True(t, route.carryResetPending.Load(), "ResetAlignment must arm the pending flag")

	// The new stream must arrive unshifted.
	writeFrame(second)

	assert.Zero(t, route.carryLen, "the reset must clear the carry before the next frame")
	assert.Equal(t, append(bytes.Clone(first[:998]), second...), consumer.out,
		"the stale half-sample must be dropped, not prepended to the new stream")
}

// FuzzRouterAlignPCM16 asserts the framer's total invariants over arbitrary
// splits, which the hand-picked chunk lists above cannot cover: every frame
// handed downstream holds whole samples, and the concatenated output is exactly
// the input truncated to a whole number of samples, with nothing reordered.
func FuzzRouterAlignPCM16(f *testing.F) {
	f.Add(alignTestSignal(200), []byte{99, 1, 200, 3})
	f.Add(alignTestSignal(64), []byte{1, 1, 1, 1})
	f.Add([]byte{1, 2, 3}, []byte{1})

	f.Fuzz(func(t *testing.T, payload, sizes []byte) {
		const rate = 48000
		r := NewAudioRouter(GetLogger(), nil)
		defer r.Close()

		consumer := newCollectingConsumer(rate)
		route := &Route{
			SourceID: "fuzz-src", Consumer: consumer,
			sourceSampleRate: rate, gainLinear: 1.0,
			inbox: make(chan AudioFrame, 1),
			done:  make(chan struct{}), stopped: make(chan struct{}),
		}

		off := 0
		for i := 0; off < len(payload); i++ {
			n := len(payload) - off
			if len(sizes) > 0 {
				if step := int(sizes[i%len(sizes)]); step > 0 && step < n {
					n = step
				}
			}
			r.handleRouteFrame(AudioFrame{
				SourceID: "fuzz-src", SourceName: "fuzz",
				Data: payload[off : off+n], SampleRate: rate,
				BitDepth: 16, Channels: 1,
			}, route)
			off += n
		}

		require.Zero(t, len(consumer.out)%2, "every emitted byte count must be a whole number of samples")
		want := payload[:len(payload)&^1]
		// bytes.Equal rather than require.Equal: an all-truncated payload leaves
		// the output nil and want an empty slice, which are equal here but not
		// to reflect.DeepEqual.
		require.Truef(t, bytes.Equal(want, consumer.out),
			"the reassembled stream must equal the input truncated to whole samples (want %d bytes, got %d)",
			len(want), len(consumer.out))
	})
}
