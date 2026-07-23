package audiocore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
)

// collectingConsumer accumulates every byte handed to Write so a test can
// compare the whole processed stream rather than individual frames. Write
// copies the payload because the router recycles frame buffers as soon as
// Write returns.
type collectingConsumer struct {
	id         string
	sampleRate int
	out        []byte
	writes     int
}

func (c *collectingConsumer) ID() string      { return c.id }
func (c *collectingConsumer) SampleRate() int { return c.sampleRate }
func (c *collectingConsumer) BitDepth() int   { return 16 }
func (c *collectingConsumer) Channels() int   { return 1 }
func (c *collectingConsumer) Close() error    { return nil }

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
		v := int16((i*2731)%40000 - 20000)
		if i%2 == 1 {
			v = -v
		}
		data[i*2] = byte(v)        //nolint:gosec // G115: intentional int16→byte truncation for PCM serialisation
		data[i*2+1] = byte(v >> 8) //nolint:gosec // G115: intentional int16→byte truncation for PCM serialisation
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

// runChunksThroughRoute feeds chunks through handleRouteFrame on a freshly built
// route and returns the consumer's concatenated output plus the route's error count.
// Calling handleRouteFrame directly (as the benchmarks do) keeps the test
// deterministic: it exercises the same resample/process/write path as the drainer
// goroutine without depending on channel scheduling.
func runChunksThroughRoute(t *testing.T, chunks [][]byte, gainLinear float64, srcRate, dstRate int) (out []byte, routeErrors int64) {
	t.Helper()

	r := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(r.Close)

	consumer := &collectingConsumer{id: "align-consumer", sampleRate: dstRate}
	route := &Route{
		SourceID:         "align-src",
		Consumer:         consumer,
		sourceSampleRate: srcRate,
		gainLinear:       gainLinear,
		inbox:            make(chan AudioFrame, 1),
		done:             make(chan struct{}),
		stopped:          make(chan struct{}),
	}
	if srcRate != dstRate {
		rs, err := resample.NewResampler(srcRate, dstRate)
		require.NoError(t, err)
		t.Cleanup(func() { _ = rs.Close() })
		route.resampler = rs
	}

	for _, chunk := range chunks {
		r.handleRouteFrame(AudioFrame{
			SourceID:   "align-src",
			SourceName: "align",
			Data:       chunk,
			SampleRate: srcRate,
			BitDepth:   16,
			Channels:   1,
		}, route)
	}

	return consumer.out, route.errors.Load()
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

	evenSizes := []int{1000, 1000, 1000, 1000}
	oddSizes := []int{999, 1001, 997, 1003}

	control, controlErrs := runChunksThroughRoute(t, splitAt(t, signal, evenSizes), gainLinear, rate, rate)
	require.Zero(t, controlErrs, "even-length control run must not produce route errors")
	require.Len(t, control, len(signal), "control run must emit every input sample")

	got, gotErrs := runChunksThroughRoute(t, splitAt(t, signal, oddSizes), gainLinear, rate, rate)
	assert.Zero(t, gotErrs, "odd-length frames must not produce route errors")
	assert.Equal(t, control, got,
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

	evenSizes := []int{1200, 1200, 1200, 1200}
	oddSizes := []int{1199, 1201, 1197, 1203}

	control, controlErrs := runChunksThroughRoute(t, splitAt(t, signal, evenSizes), 1.0, srcRate, dstRate)
	require.Zero(t, controlErrs, "even-length control run must not produce route errors")
	require.NotEmpty(t, control, "control run must emit resampled audio")

	got, gotErrs := runChunksThroughRoute(t, splitAt(t, signal, oddSizes), 1.0, srcRate, dstRate)
	assert.Zero(t, gotErrs, "odd-length frames must not be rejected by the resampler")
	assert.Equal(t, control, got,
		"odd-length frames must yield the same resampled stream as even-length frames")
}
