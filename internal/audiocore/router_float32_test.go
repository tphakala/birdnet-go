package audiocore

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime/debug"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dspeq "github.com/tphakala/go-audio-dsp/equalizer"
	dspgain "github.com/tphakala/go-audio-dsp/gain"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
)

const (
	// refFrameSamples is the per-frame sample count the router tests feed through
	// the processing stage (30 ms at 48 kHz).
	refFrameSamples = 1440

	// refNoiseAmplitude and refToneAmplitude are about -20 dBFS (0.1 of full
	// scale) so the encoder never saturates and EQ boosts stay in range.
	refNoiseAmplitude = 0.1 * 32768
	refToneAmplitude  = 0.1 * 32768
	refToneHz         = 1000.0

	// refInt16Scale is the PCM16 scale shared by the library encoder and the
	// independent reference encoder in these tests.
	refInt16Scale = 32768.0

	// maxRefLSBDiff and maxRefDiffFraction bound the float32 path against the
	// independent float64 reference: at most 1 LSB, on fewer than 0.2% of
	// samples (the worst measured case differs on 0.087%).
	maxRefLSBDiff      = 1
	maxRefDiffFraction = 0.002

	// poisonByte is written into pooled byte buffers before a test runs, next to
	// NaN in the float buffers, so a read of a stale sample shows up as noise in
	// the output.
	poisonByte = 0xAA
)

// testGain returns the go-audio-dsp gain a route would hold for db, or nil for
// 0 dB, matching AddRoute.
func testGain(tb testing.TB, db float64) *dspgain.Gain {
	tb.Helper()
	if db == 0 {
		return nil
	}
	g, err := dspgain.New(db)
	require.NoError(tb, err)
	return g
}

// testChain builds a filter chain from bands at sampleRate.
func testChain(tb testing.TB, sampleRate int, bands ...dspeq.Band) *dspeq.FilterChain {
	tb.Helper()
	chain := dspeq.NewFilterChain()
	for _, b := range bands {
		f, err := dspeq.NewFilter(b, sampleRate)
		require.NoError(tb, err)
		require.NoError(tb, chain.AddFilter(f))
	}
	return chain
}

// noiseAndTone returns n deterministic int16 samples of -20 dBFS white noise
// plus a -20 dBFS tone at sampleRate.
func noiseAndTone(n, sampleRate int) []int16 {
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: deterministic test signal, not security sensitive
	out := make([]int16, n)
	for i := range out {
		noise := (rng.Float64()*2 - 1) * refNoiseAmplitude
		tone := refToneAmplitude * math.Sin(2*math.Pi*refToneHz*float64(i)/float64(sampleRate))
		out[i] = int16(noise + tone)
	}
	return out
}

// int16sToBytes encodes samples as little-endian PCM16.
func int16sToBytes(samples []int16) []byte {
	out := make([]byte, len(samples)*2)
	for i, s := range samples {
		out[i*2] = byte(s)
		out[i*2+1] = byte(s >> 8) //nolint:gosec // G115: intentional int16 to byte truncation for PCM serialisation
	}
	return out
}

// bytesToInt16s decodes little-endian PCM16.
func bytesToInt16s(data []byte) []int16 {
	out := make([]int16, len(data)/2)
	for i := range out {
		out[i] = int16(uint16(data[i*2]) | uint16(data[i*2+1])<<8) //nolint:gosec // G115: intentional uint16 to int16 reinterpretation for PCM audio
	}
	return out
}

// refEncode is the test's own float64 encoder: scale by 32768, round half to
// even, saturate. It is independent of the library encoder under test.
func refEncode(x float64) int16 {
	v := math.RoundToEven(x * refInt16Scale)
	return int16(max(math.MinInt16, min(math.MaxInt16, v)))
}

// refProcess runs the float64 reference pipeline over samples: float64 decode,
// the chain's float64 cascade (ApplyFloat64), a float64 math.Pow gain, and
// refEncode. chain may be nil and gainDB may be 0.
func refProcess(samples []int16, chain *dspeq.FilterChain, gainDB float64) []int16 {
	buf := make([]float64, len(samples))
	for i, s := range samples {
		buf[i] = float64(s) / refInt16Scale
	}
	if chain != nil {
		chain.ApplyFloat64(buf)
	}
	factor := math.Pow(10, gainDB/20)
	out := make([]int16, len(buf))
	for i, v := range buf {
		out[i] = refEncode(v * factor)
	}
	return out
}

// diffStats compares got with want and returns the largest absolute difference
// in LSB and the number of samples that differ.
func diffStats(got, want []int16) (maxDiff, differing int) {
	for i := range want {
		d := int(got[i]) - int(want[i])
		if d < 0 {
			d = -d
		}
		if d != 0 {
			differing++
		}
		maxDiff = max(maxDiff, d)
	}
	return maxDiff, differing
}

// poisonFloat32Pool fills count pooled buffers of the given length with NaN and
// returns them, pinning the "pooled buffers are fully overwritten before they
// are read" invariant: sync.Pool does not zero, so any sample the router reads
// before writing turns the output into saturated or garbage samples.
func poisonFloat32Pool(mgr *buffer.Manager, size, count int) {
	pool := mgr.Float32PoolFor(size)
	bufs := make([][]float32, count)
	for i := range bufs {
		bufs[i] = pool.Get()
		for j := range bufs[i] {
			bufs[i][j] = float32(math.NaN())
		}
	}
	for _, b := range bufs {
		pool.Put(b)
	}
}

// poisonBytePool does the same for byte pools with a recognisable pattern.
func poisonBytePool(mgr *buffer.Manager, size, count int) {
	pool := mgr.BytePoolFor(size)
	bufs := make([][]byte, count)
	for i := range bufs {
		bufs[i] = pool.Get()
		for j := range bufs[i] {
			bufs[i][j] = poisonByte
		}
	}
	for _, b := range bufs {
		pool.Put(b)
	}
}

// newProcessingTestRouter returns a router with a buffer manager, and a route
// with the given chain and gain wired to a fresh collecting consumer. Rates
// differ when srcRate != dstRate, which adds a resampler like AddRoute does.
func newProcessingTestRouter(t *testing.T, srcRate, dstRate int, chain *dspeq.FilterChain, gainDB float64) (*AudioRouter, *Route, *collectingConsumer, *buffer.Manager) {
	t.Helper()
	mgr := buffer.NewManager(GetLogger())
	r := NewAudioRouter(GetLogger(), mgr)
	t.Cleanup(r.Close)

	consumer := newCollectingConsumer(dstRate)
	route := newAlignTestRoute(t, consumer, alignRunOpts{gainDB: gainDB, srcRate: srcRate, dstRate: dstRate, bitDepth: 16})
	route.filterChain.Store(chain)
	return r, route, consumer, mgr
}

// TestApplyProcessing_MatchesFloat64Reference pins the numerical contract of the
// router's float32 path: against an independent float64 pipeline (float64
// decode, ApplyFloat64 cascade, math.Pow gain, round half to even by 32768,
// saturate) the output is within 1 LSB on all but a tiny fraction of samples.
// The old 32767-scale truncating encoder differed by up to 2 LSB on 46% to 98% of
// samples on these inputs, so this test fails if that encoder comes back.
//
// Pooled float32 and byte buffers are pre-filled with NaN and 0xAA so a read of
// a stale sample before it is written fails the comparison too.
func TestApplyProcessing_MatchesFloat64Reference(t *testing.T) {
	// Not parallel: it pins the GC so sync.Pool keeps the poisoned buffers.
	defer debug.SetGCPercent(debug.SetGCPercent(-1))

	const (
		rate48k  = 48000
		rate256k = 256000
	)
	tests := []struct {
		name    string
		rate    int
		bands   []dspeq.Band
		gainDB  float64
		samples int
	}{
		{"gain_plus_6dB", rate48k, nil, 6, 4 * rate48k},
		{"gain_minus_6dB", rate48k, nil, -6, 4 * rate48k},
		{"gain_plus_3_5dB", rate48k, nil, 3.5, 4 * rate48k},
		{"highpass_100Hz_x2", rate48k, []dspeq.Band{{Type: dspeq.HighPass, Frequency: 100, Q: 0.707, Passes: 2}}, 0, 4 * rate48k},
		{"lowshelf_minus12_then_gain_6", rate48k, []dspeq.Band{{Type: dspeq.LowShelf, Frequency: 200, Q: 0.707, GainDB: -12}}, 6, 4 * rate48k},
		{"three_bands_plus_3dB", rate48k, []dspeq.Band{
			{Type: dspeq.HighPass, Frequency: 80, Q: 0.707, Passes: 1},
			{Type: dspeq.Peaking, Frequency: 3000, WidthHz: 1500, GainDB: 4},
			{Type: dspeq.HighShelf, Frequency: 8000, Q: 0.707, GainDB: -3},
		}, 3, 4 * rate48k},
		{"highpass_20Hz_x4_at_256kHz", rate256k, []dspeq.Band{{Type: dspeq.HighPass, Frequency: 20, Q: 0.707, Passes: 4}}, 0, rate256k},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var chain, refChain *dspeq.FilterChain
			if tt.bands != nil {
				chain = testChain(t, tt.rate, tt.bands...)
				refChain = testChain(t, tt.rate, tt.bands...)
			}

			mgr := buffer.NewManager(GetLogger())
			r := NewAudioRouter(GetLogger(), mgr)
			t.Cleanup(r.Close)
			route := &Route{}
			route.gain.Store(testGain(t, tt.gainDB))

			poisonFloat32Pool(mgr, refFrameSamples, 8)
			poisonBytePool(mgr, refFrameSamples*2, 8)

			input := noiseAndTone(tt.samples, tt.rate)
			want := refProcess(input, refChain, tt.gainDB)

			got := make([]int16, 0, len(input))
			for off := 0; off < len(input); off += refFrameSamples {
				end := min(off+refFrameSamples, len(input))
				frame := AudioFrame{Data: int16sToBytes(input[off:end]), SampleRate: tt.rate, BitDepth: 16, Channels: 1}
				res, err := r.applyProcessing(frame, route, chain, route.gain.Load())
				require.NoError(t, err)
				got = append(got, bytesToInt16s(res.Frame.Data)...)
				res.release()
			}

			require.Len(t, got, len(want))
			maxDiff, differing := diffStats(got, want)
			assert.LessOrEqual(t, maxDiff, maxRefLSBDiff, "output must stay within 1 LSB of the float64 reference")
			assert.Less(t, float64(differing)/float64(len(want)), maxRefDiffFraction,
				"at most 0.2%% of samples may differ from the float64 reference (got %d of %d)", differing, len(want))
		})
	}
}

// TestApplyProcessing_IdentityChainIsLossless pins that an empty (identity)
// filter chain with no gain returns every int16 value unchanged, including
// -32768 and 32767. The old 32767-scale encoder turned 16384 into 16383 and
// -32768 into -32767.
func TestApplyProcessing_IdentityChainIsLossless(t *testing.T) {
	t.Parallel()

	mgr := buffer.NewManager(GetLogger())
	r := NewAudioRouter(GetLogger(), mgr)
	t.Cleanup(r.Close)
	route := &Route{}

	all := make([]int16, 0, 1<<16)
	for v := math.MinInt16; v <= math.MaxInt16; v++ {
		all = append(all, int16(v))
	}
	data := int16sToBytes(all)

	res, err := r.applyProcessing(AudioFrame{Data: data, BitDepth: 16}, route, dspeq.NewFilterChain(), route.gain.Load())
	require.NoError(t, err)
	t.Cleanup(res.release)
	assert.Equal(t, data, res.Frame.Data, "an identity chain must return every int16 value unchanged")
}

// TestApplyProcessing_GainFastPathMatchesFloatPath pins that the gain-only byte
// fast path (Gain.ApplyBytesTo, no float buffer) and the float path (identity
// chain plus the same gain) produce identical bytes over full-scale random
// input, because scaling by 1/32768 and 32768 is exact.
func TestApplyProcessing_GainFastPathMatchesFloatPath(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // G404: deterministic test signal, not security sensitive
	input := make([]int16, refFrameSamples)
	for i := range input {
		input[i] = int16(rng.IntN(1<<16) + math.MinInt16)
	}
	data := int16sToBytes(input)

	for _, db := range []float64{-40, -6, -0.1, 0.1, 3.5, 6, 20, 40} {
		t.Run(fmt.Sprintf("%gdB", db), func(t *testing.T) {
			t.Parallel()
			mgr := buffer.NewManager(GetLogger())
			r := NewAudioRouter(GetLogger(), mgr)
			t.Cleanup(r.Close)

			fast := &Route{}
			fast.gain.Store(testGain(t, db))
			slow := &Route{}
			slow.gain.Store(testGain(t, db))
			frame := AudioFrame{Data: data, BitDepth: 16}

			fastRes, err := r.applyProcessing(frame, fast, nil, fast.gain.Load())
			require.NoError(t, err)
			t.Cleanup(fastRes.release)
			require.Nil(t, fastRes.FloatPool, "the fast path must not take a float buffer")
			require.Empty(t, fastRes.FloatBuf, "the fast path must not take a float buffer")

			slowRes, err := r.applyProcessing(frame, slow, dspeq.NewFilterChain(), slow.gain.Load())
			require.NoError(t, err)
			t.Cleanup(slowRes.release)
			require.NotEmpty(t, slowRes.FloatBuf, "the float path must take a float buffer")

			assert.Equal(t, slowRes.Frame.Data, fastRes.Frame.Data, "gain %g dB: fast path and float path must agree byte for byte", db)
		})
	}
}

// TestRouter_ResampleThenProcessUsesFloatHandoff pins the resample-then-process
// route: the resampler's float32 output goes straight to the EQ and gain, so
// the result is within 1 LSB of a reference that resamples to float32 itself
// and processes in float64, and no resample byte buffer is ever taken.
func TestRouter_ResampleThenProcessUsesFloatHandoff(t *testing.T) {
	// Not parallel: it pins the GC so sync.Pool keeps the poisoned buffers.
	defer debug.SetGCPercent(debug.SetGCPercent(-1))

	const (
		srcRate      = 256000
		dstRate      = 48000
		gainDB       = 6.0
		frameSamples = 2560
	)
	bands := []dspeq.Band{{Type: dspeq.HighPass, Frequency: 100, Q: 0.707, Passes: 2}}
	r, route, consumer, mgr := newProcessingTestRouter(t, srcRate, dstRate, testChain(t, dstRate, bands...), gainDB)
	poisonFloat32Pool(mgr, frameSamples*dstRate/srcRate, 8)
	poisonBytePool(mgr, frameSamples*2*dstRate/srcRate, 8)

	ref, err := resample.NewResampler(srcRate, dstRate)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, ref.Close()) })
	refChain := testChain(t, dstRate, bands...)

	input := noiseAndTone(srcRate, srcRate)
	var want []int16
	for off := 0; off+frameSamples <= len(input); off += frameSamples {
		data := int16sToBytes(input[off : off+frameSamples])
		r.handleRouteFrame(AudioFrame{Data: data, SampleRate: srcRate, BitDepth: 16, Channels: 1}, route)

		floats, rerr := ref.ResampleFloat32Into(data)
		require.NoError(t, rerr)
		buf := make([]float64, len(floats))
		for i, f := range floats {
			buf[i] = float64(f)
		}
		refChain.ApplyFloat64(buf)
		factor := math.Pow(10, gainDB/20)
		for _, v := range buf {
			want = append(want, refEncode(v*factor))
		}
	}

	got := bytesToInt16s(consumer.out)
	require.Len(t, got, len(want), "the route must emit exactly the samples the reference resampler produced")
	require.NotEmpty(t, got)
	maxDiff, differing := diffStats(got, want)
	assert.LessOrEqual(t, maxDiff, maxRefLSBDiff)
	assert.Less(t, float64(differing)/float64(len(want)), maxRefDiffFraction)

	assert.Nil(t, route.resamplePool.Load(), "the float handoff must not take a resample byte buffer")
	assert.NotNil(t, route.float32Pool.Load(), "the float handoff must take a pooled float32 buffer")
	assert.NotNil(t, route.byteOutPool.Load(), "the float handoff must take a pooled output byte buffer")
}

// TestRouter_ZeroSampleResampleOutputSkipsWrite pins that a resampling route
// whose resampler emits no samples for a non-empty frame writes nothing to the
// consumer, on both the unprocessed (byte) branch and the processed (float
// handoff) branch. The size is found by probing a fresh resampler, since which
// inputs produce no output while the filter primes is the resampler's business.
func TestRouter_ZeroSampleResampleOutputSkipsWrite(t *testing.T) {
	t.Parallel()

	const (
		srcRate     = 256000
		dstRate     = 48000
		maxProbeLen = 8
	)
	probeLen := 0
	for n := 1; n <= maxProbeLen && probeLen == 0; n++ {
		rs, err := resample.NewResampler(srcRate, dstRate)
		require.NoError(t, err)
		out, err := rs.ResampleFloat32Into(make([]byte, n*2))
		require.NoError(t, err)
		require.NoError(t, rs.Close())
		if len(out) == 0 {
			probeLen = n
		}
	}
	if probeLen == 0 {
		t.Skipf("the %d to %d Hz resampler emits output for every 1 to %d sample input; no zero-output case to pin", srcRate, dstRate, maxProbeLen)
	}

	for _, tt := range []struct {
		name   string
		gainDB float64
	}{
		{"unprocessed_byte_branch", 0},
		{"processed_float_handoff", 6},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, route, consumer, _ := newProcessingTestRouter(t, srcRate, dstRate, nil, tt.gainDB)

			r.handleRouteFrame(AudioFrame{Data: make([]byte, probeLen*2), SampleRate: srcRate, BitDepth: 16, Channels: 1}, route)

			assert.Zero(t, consumer.writes, "a zero-sample resample result must not reach the consumer")
			assert.Zero(t, route.errors.Load(), "an empty resample result is not an error")
		})
	}
}

// frameCapture is a consumer that keeps the last frame handed to Write, with
// Data copied because the router recycles it after Write returns.
type frameCapture struct {
	mockConsumer
	last   AudioFrame
	writes int
}

func (c *frameCapture) Write(frame AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	c.last = frame
	c.last.Data = bytes.Clone(frame.Data)
	c.writes++
	return nil
}

// TestRouter_ResampledOrProcessedFrameKeepsMetadataAndDropsRef pins what a
// consumer receives on a route that resamples or processes: the producer's Ref
// is not carried (it tracks a different buffer than the delivered Data), the
// sample rate is the consumer's, and the rest of the frame metadata is kept.
func TestRouter_ResampledOrProcessedFrameKeepsMetadataAndDropsRef(t *testing.T) {
	t.Parallel()

	const srcRate = 48000
	bands := []dspeq.Band{{Type: dspeq.HighPass, Frequency: 100, Q: 0.707, Passes: 1}}
	tests := []struct {
		name    string
		dstRate int
		eq      bool
		gainDB  float64
	}{
		{"resample_only", 32000, false, 0},
		{"gain_only", srcRate, false, -6},
		{"eq_only", srcRate, true, 0},
		{"resample_then_process_handoff", 32000, true, -6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mgr := buffer.NewManager(GetLogger())
			r := NewAudioRouter(GetLogger(), mgr)
			t.Cleanup(r.Close)

			consumer := &frameCapture{}
			consumer.id, consumer.sampleRate, consumer.bitDepth, consumer.channels = "capture", tt.dstRate, 16, 1
			route := newAlignTestRoute(t, consumer, alignRunOpts{gainDB: tt.gainDB, srcRate: srcRate, dstRate: tt.dstRate, bitDepth: 16})
			if tt.eq {
				route.filterChain.Store(testChain(t, tt.dstRate, bands...))
			}

			released := 0
			in := AudioFrame{
				SourceID: "meta-src", SourceName: "meta-name",
				Data: alignTestSignal(1200), SampleRate: srcRate,
				BitDepth: 16, Channels: 1,
				Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
				Ref:       NewFrameRef(func() { released++ }),
			}
			r.handleRouteFrame(in, route)

			require.Equal(t, 1, consumer.writes, "the frame must reach the consumer")
			got := consumer.last
			assert.Nil(t, got.Ref, "the producer's Ref tracks the producer's buffer, not the delivered Data")
			assert.Equal(t, 1, released, "the router must release its reference to the producer's frame exactly once")
			assert.Equal(t, tt.dstRate, got.SampleRate)
			assert.Equal(t, in.SourceID, got.SourceID)
			assert.Equal(t, in.SourceName, got.SourceName)
			assert.Equal(t, in.BitDepth, got.BitDepth)
			assert.Equal(t, in.Channels, got.Channels)
			assert.Equal(t, in.Timestamp, got.Timestamp)
		})
	}
}

// TestRouter_NonPCM16FrameSkipsEQAndGain pins that a frame declaring another bit
// depth is never decoded, scaled or float-resampled as PCM16, even on a route
// with gain and EQ: without a resampler it passes through unchanged, and with
// one it takes the byte resample branch (resample buffer pooled, no float
// scratch, no processing output buffer).
func TestRouter_NonPCM16FrameSkipsEQAndGain(t *testing.T) {
	t.Parallel()

	const srcRate = 48000
	bands := []dspeq.Band{{Type: dspeq.HighPass, Frequency: 100, Q: 0.707, Passes: 1}}
	data := alignTestSignal(600)

	t.Run("no_resampler_passes_through", func(t *testing.T) {
		t.Parallel()
		r, route, consumer, _ := newProcessingTestRouter(t, srcRate, srcRate, testChain(t, srcRate, bands...), 6)
		r.handleRouteFrame(AudioFrame{Data: data, SampleRate: srcRate, BitDepth: 8, Channels: 1}, route)

		assert.Equal(t, data, consumer.out, "a non-PCM16 frame must reach the consumer unprocessed")
		assert.Nil(t, route.float32Pool.Load(), "a non-PCM16 frame must not take a float scratch buffer")
		assert.Nil(t, route.byteOutPool.Load(), "a non-PCM16 frame must not take a processing output buffer")
	})

	t.Run("resampler_takes_byte_branch", func(t *testing.T) {
		t.Parallel()
		r, route, consumer, _ := newProcessingTestRouter(t, srcRate, 32000, testChain(t, 32000, bands...), 6)
		r.handleRouteFrame(AudioFrame{Data: data, SampleRate: srcRate, BitDepth: 8, Channels: 1}, route)

		require.Equal(t, 1, consumer.writes, "the resampled frame must reach the consumer")
		assert.NotNil(t, route.resamplePool.Load(), "a non-PCM16 frame must take the byte resample branch")
		assert.Nil(t, route.float32Pool.Load(), "a non-PCM16 frame must not take a float scratch buffer")
		assert.Nil(t, route.byteOutPool.Load(), "a non-PCM16 frame must not take a processing output buffer")
	})
}
