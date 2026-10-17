package transcode

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// int16Scale converts an int16 sample to the +-1.0 float range Stream delivers.
	int16Scale = 32768.0

	// alignmentToleranceSamples is the allowed distance, in samples, between an
	// impulse and its ideal position after a resample.
	alignmentToleranceSamples = 1

	// wavFmtChunkBytes is the size of the PCM fmt chunk body.
	wavFmtChunkBytes = 16
	// wavFormatPCM is the WAV format tag for integer PCM.
	wavFormatPCM = 1
)

// collectStream runs Stream and returns every delivered sample.
func collectStream(t *testing.T, m *MonoSource) []float32 {
	t.Helper()
	var all []float32
	require.NoError(t, m.Stream(t.Context(), func(b []float32) error {
		all = append(all, b...)
		return nil
	}))
	return all
}

// writeWAVChannels writes interleaved 16-bit PCM with a hand-built header, so
// tests can make stereo files the mono production writer cannot.
func writeWAVChannels(t *testing.T, path string, samples []int16, rate, channels int) {
	t.Helper()
	data := int16Bytes(samples)
	hdr := make([]byte, wavHeaderBytes, wavHeaderBytes+len(data))
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], uint32(wavHeaderBytes-8+len(data))) //nolint:gosec // G115: small test sizes
	copy(hdr[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(hdr[16:], wavFmtChunkBytes)
	binary.LittleEndian.PutUint16(hdr[20:], wavFormatPCM)
	binary.LittleEndian.PutUint16(hdr[22:], uint16(channels))                     //nolint:gosec // G115: small test value
	binary.LittleEndian.PutUint32(hdr[24:], uint32(rate))                         //nolint:gosec // G115: small test value
	binary.LittleEndian.PutUint32(hdr[28:], uint32(rate*channels*bytesPerSample)) //nolint:gosec // G115: small test value
	binary.LittleEndian.PutUint16(hdr[32:], uint16(channels*bytesPerSample))      //nolint:gosec // G115: small test value
	binary.LittleEndian.PutUint16(hdr[34:], outputBitDepth)                       //nolint:gosec // G115: small test value
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], uint32(len(data))) //nolint:gosec // G115: small test sizes
	require.NoError(t, os.WriteFile(path, append(hdr, data...), 0o600))
}

// pcmBytes16 serializes samples for MonoFromPCM16.
func pcmBytes16(samples []int16) []byte { return int16Bytes(samples) }

// peakIndex returns the index of the largest magnitude sample.
func peakIndex(x []float32) int {
	best, idx := float32(-1), -1
	for i, v := range x {
		if a := float32(math.Abs(float64(v))); a > best {
			best, idx = a, i
		}
	}
	return idx
}

// impulseClip returns a silent mono clip with a full-scale impulse at frame at.
func impulseClip(frames, at int) []int16 {
	s := make([]int16, frames)
	s[at] = math.MaxInt16
	return s
}

func TestDecodeMono_MonoEqualRatePassesSamplesThrough(t *testing.T) {
	samples := tone(testRate, 1, 0.5, testToneHz, testAmp)
	path := filepath.Join(t.TempDir(), "mono.wav")
	writeWAVFile(t, path, samples, testRate)

	m, err := DecodeMono(t.Context(), path, 0)
	require.NoError(t, err)
	got := collectStream(t, m)

	require.Len(t, got, len(samples))
	for i, s := range samples {
		require.InDelta(t, float64(s)/int16Scale, float64(got[i]), 1e-7, "sample %d", i)
	}
	assert.Equal(t, testRate, m.SampleRate())
	assert.Equal(t, int64(len(samples)), m.SpanSamples())
}

func TestDecodeMono_StereoAveragesChannels(t *testing.T) {
	const frames = 1000
	tests := []struct {
		name string
		l, r int16
		want float64
	}{
		{name: "identical channels give the channel", l: 8000, r: 8000, want: 8000},
		{name: "opposite channels cancel", l: 8000, r: -8000, want: 0},
		{name: "one silent channel halves", l: 8000, r: 0, want: 4000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			samples := make([]int16, 0, 2*frames)
			for range frames {
				samples = append(samples, tt.l, tt.r)
			}
			path := filepath.Join(t.TempDir(), "stereo.wav")
			writeWAVChannels(t, path, samples, testRate, 2)

			m, err := DecodeMono(t.Context(), path, 0)
			require.NoError(t, err)
			got := collectStream(t, m)

			require.Len(t, got, frames)
			assert.Equal(t, int64(frames), m.SpanSamples())
			for _, v := range got {
				require.InDelta(t, tt.want/int16Scale, float64(v), 1e-6)
			}
		})
	}
}

func TestDecodeMono_RejectsThreeChannels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "three.wav")
	writeWAVChannels(t, path, make([]int16, 3*100), testRate, 3)

	_, err := DecodeMono(t.Context(), path, 0)

	require.ErrorIs(t, err, ErrUnsupportedSource)
}

func TestDecodeMono_EmptyFileIsUndecodable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.wav")
	writeWAVChannels(t, path, nil, testRate, 1)

	_, err := DecodeMono(t.Context(), path, 0)

	require.ErrorIs(t, err, ErrUndecodableSource)
}

func TestExportPathsStillRejectStereo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stereo.wav")
	writeWAVChannels(t, path, make([]int16, 2*testRate), testRate, 2)

	_, err := ExtractClip(t.Context(), &ClipOptions{InputPath: path, Start: 0, End: 0.5, Format: audioformat.WAV, TempDir: t.TempDir()})
	require.ErrorIs(t, err, ErrUnsupportedSource, "ExtractClip")

	_, err = ProcessToWAV(t.Context(), path, Filters{})
	require.ErrorIs(t, err, ErrUnsupportedSource, "ProcessToWAV")
}

func TestDecodeMono_AllClipFormats(t *testing.T) {
	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			path, _ := writeFixture(t, fx, testRate, testSeconds)

			m, err := DecodeMono(t.Context(), path, 0)
			require.NoError(t, err)
			got := collectStream(t, m)

			require.NotEmpty(t, got)
			x := make([]float64, len(got))
			for i, v := range got {
				x[i] = float64(v)
			}
			assert.True(t, dominantIs(x, m.SampleRate(), testToneHz, testOffToneH), "1 kHz must dominate")
		})
	}
}

func TestMonoSource_ResampleLengthAndTail(t *testing.T) {
	const (
		seconds   = 1
		burstSecs = 0.05
		tailSecs  = 0.06
		dstRate   = 24000
	)
	samples := make([]int16, seconds*testRate)
	body := tone(testRate, 1, burstSecs, testToneHz, testAmp)
	copy(samples[len(samples)-len(body):], body)

	m, err := MonoFromPCM16(pcmBytes16(samples), testRate, dstRate)
	require.NoError(t, err)
	got := collectStream(t, m)

	assert.InDelta(t, float64(m.SpanSamples()), float64(len(got)), resampleSlackFrames)
	require.Equal(t, int64(seconds*dstRate), m.SpanSamples())
	tail := got[len(got)-int(tailSecs*dstRate):]
	var peak float32
	for _, v := range tail {
		peak = max(peak, float32(math.Abs(float64(v))))
	}
	assert.Greater(t, peak, float32(0.1), "a burst in the last 50 ms must appear in the final 60 ms")
}

func TestMonoSource_ResampleAlignment(t *testing.T) {
	tests := []struct {
		name     string
		from, to int
		frames   int
	}{
		{name: "bird 48 to 24 kHz", from: 48000, to: 24000, frames: 96000},
		{name: "bat 384 to 256 kHz", from: 384000, to: 256000, frames: 384000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := MonoFromPCM16(pcmBytes16(impulseClip(tt.frames, tt.frames/2)), tt.from, tt.to)
			require.NoError(t, err)
			got := collectStream(t, m)

			ideal := int(m.SpanSamples()) / 2
			assert.InDelta(t, ideal, peakIndex(got), alignmentToleranceSamples)
		})
	}
}

func TestMonoSource_StereoChunkBoundary(t *testing.T) {
	const frames = chunkFrames + 3
	path := filepath.Join(t.TempDir(), "stereo.wav")
	samples := make([]int16, 0, 2*frames)
	for range frames {
		samples = append(samples, 6000, 2000)
	}
	writeWAVChannels(t, path, samples, testRate, 2)

	m, err := DecodeMono(t.Context(), path, 0)
	require.NoError(t, err)
	got := collectStream(t, m)

	require.Len(t, got, frames)
	assert.InDelta(t, 4000.0/int16Scale, float64(got[frames-1]), 1e-6)
}

func TestMonoSource_StreamTwiceIsIdentical(t *testing.T) {
	samples := tone(testRate, 1, 0.5, testToneHz, testAmp)
	m, err := MonoFromPCM16(pcmBytes16(samples), testRate, 24000)
	require.NoError(t, err)

	first := collectStream(t, m)
	second := collectStream(t, m)
	assert.Equal(t, first, second)
}

func TestMonoFromPCM16_Bounds(t *testing.T) {
	t.Run("odd length drops one byte", func(t *testing.T) {
		data := append(pcmBytes16([]int16{100, 200, 300}), 0x7f)
		m, err := MonoFromPCM16(data, testRate, 0)
		require.NoError(t, err)
		assert.Len(t, collectStream(t, m), 3)
	})
	t.Run("empty is an empty range", func(t *testing.T) {
		_, err := MonoFromPCM16(nil, testRate, 0)
		require.ErrorIs(t, err, ErrEmptyRange)
		_, err = MonoFromPCM16([]byte{1}, testRate, 0)
		require.ErrorIs(t, err, ErrEmptyRange)
	})
	t.Run("rates outside the bounds are unsupported", func(t *testing.T) {
		data := pcmBytes16([]int16{1, 2})
		_, err := MonoFromPCM16(data, 4000, 0)
		require.ErrorIs(t, err, ErrUnsupportedSource)
		_, err = MonoFromPCM16(data, 2_000_000, 0)
		require.ErrorIs(t, err, ErrUnsupportedSource)
		_, err = MonoFromPCM16(data, testRate, 2_000_000)
		require.ErrorIs(t, err, ErrUnsupportedSource)
	})
	t.Run("non-positive target keeps the source rate", func(t *testing.T) {
		m, err := MonoFromPCM16(pcmBytes16([]int16{1, 2}), testRate, -1)
		require.NoError(t, err)
		assert.Equal(t, testRate, m.SampleRate())
	})
	t.Run("a one-frame clip spans one sample", func(t *testing.T) {
		m, err := MonoFromPCM16(pcmBytes16([]int16{1}), 48000, 24000)
		require.NoError(t, err)
		assert.Equal(t, int64(1), m.SpanSamples())
	})
}

func TestMonoSource_CanceledContext(t *testing.T) {
	m, err := MonoFromPCM16(pcmBytes16(make([]int16, 1000)), testRate, 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = m.Stream(ctx, func([]float32) error { return nil })

	require.ErrorIs(t, err, context.Canceled)
}

func TestMonoSource_CallbackErrorStops(t *testing.T) {
	m, err := MonoFromPCM16(pcmBytes16(make([]int16, 3*chunkFrames)), testRate, 0)
	require.NoError(t, err)
	errStop := errors.NewStd("stop")
	calls := 0

	err = m.Stream(t.Context(), func([]float32) error {
		calls++
		return errStop
	})

	require.ErrorIs(t, err, errStop)
	assert.Equal(t, 1, calls)
}

func BenchmarkMonoSourceStreamResample(b *testing.B) {
	samples := tone(48000, 1, 15, testToneHz, testAmp)
	m, err := MonoFromPCM16(int16Bytes(samples), 48000, 24000)
	require.NoError(b, err)
	b.ReportAllocs()
	for b.Loop() {
		require.NoError(b, m.Stream(b.Context(), func([]float32) error { return nil }))
	}
}

// A stereo clip whose channels carry the same signal must resample to what the
// mono clip does, so the resample path keeps the right frame stride and span.
func TestDecodeMono_StereoResampleMatchesMono(t *testing.T) {
	const (
		srcRate = 48000
		dstRate = 24000
		seconds = 1.5
		eps     = 1e-6
	)
	dir := t.TempDir()
	monoPath := filepath.Join(dir, "mono.wav")
	stereoPath := filepath.Join(dir, "stereo.wav")
	writeWAVChannels(t, monoPath, tone(srcRate, 1, seconds, testToneHz, testAmp), srcRate, 1)
	writeWAVChannels(t, stereoPath, tone(srcRate, 2, seconds, testToneHz, testAmp), srcRate, 2)

	mono, err := DecodeMono(t.Context(), monoPath, dstRate)
	require.NoError(t, err)
	stereo, err := DecodeMono(t.Context(), stereoPath, dstRate)
	require.NoError(t, err)

	assert.Equal(t, mono.SpanSamples(), stereo.SpanSamples())
	want, got := collectStream(t, mono), collectStream(t, stereo)
	require.Len(t, got, len(want))
	for i := range want {
		require.InDelta(t, want[i], got[i], eps, "sample %d", i)
	}
}

// Cancelling while the last resampled chunk is delivered must still return the
// context error, not flush the resampler tail and report success.
func TestMonoSource_CanceledOnLastChunkWhileResampling(t *testing.T) {
	m, err := MonoFromPCM16(pcmBytes16(make([]int16, chunkFrames/2)), 48000, 24000)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	err = m.Stream(ctx, func([]float32) error {
		cancel()
		return nil
	})

	require.ErrorIs(t, err, context.Canceled)
}
