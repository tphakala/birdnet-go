package transcode

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
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

// WithRate must build the source DecodeMono builds for the same target rate,
// stereo included, and leave the receiver at the rate it was decoded at.
func TestMonoSource_WithRateMatchesDecodeRate(t *testing.T) {
	const (
		srcRate = 48000
		dstRate = 24000
		seconds = 1.5
	)
	path := filepath.Join(t.TempDir(), "stereo.wav")
	writeWAVChannels(t, path, tone(srcRate, 2, seconds, testToneHz, testAmp), srcRate, 2)

	native, err := DecodeMono(t.Context(), path, 0)
	require.NoError(t, err)
	want, err := DecodeMono(t.Context(), path, dstRate)
	require.NoError(t, err)

	got, err := native.WithRate(dstRate)
	require.NoError(t, err)

	assert.Equal(t, want.SampleRate(), got.SampleRate())
	assert.Equal(t, want.SpanSamples(), got.SpanSamples())
	assert.Equal(t, collectStream(t, want), collectStream(t, got))
	assert.Equal(t, srcRate, native.SampleRate(), "the receiver keeps its rate")

	_, err = native.WithRate(minSourceSampleRate - 1)
	require.ErrorIs(t, err, ErrUnsupportedSource)
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

const (
	// streamTestSeconds gives 158,400 frames at testRate: three chunks of
	// chunkFrames with a short last one, and not a multiple of any codec frame size.
	streamTestSeconds = 3.3

	// heapTestSeconds is 32 chunks at testRate, 4 MiB decoded.
	heapTestSeconds = 44.0
	// heapTestChunk is the chunk at which the heap is read, well into the stream.
	heapTestChunk = 24

	// xingSearchBytes bounds the search for the Xing or Info tag at the start of an
	// MP3, and xingFramesOffset is where the frame count sits after the tag name
	// and the flags word.
	xingSearchBytes  = 400
	xingFramesOffset = 8
)

// The file-backed source must deliver what the buffered decode of the same file
// delivers, for every format BirdNET-Go writes and at the rate conversions the
// native render uses. A difference would change cached spectrogram PNGs.
func TestOpenMono_StreamsSameSamplesAsDecodeMono(t *testing.T) {
	t.Parallel()

	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			path, _ := writeFixture(t, fx, testRate, streamTestSeconds)
			for _, target := range []int{0, 24000, 32000} {
				t.Run(fmt.Sprintf("to %d", target), func(t *testing.T) {
					t.Parallel()
					want, err := DecodeMono(t.Context(), path, target)
					require.NoError(t, err)
					got, err := OpenMono(t.Context(), path, 0)
					require.NoError(t, err)
					require.True(t, got.Streamed(), "a clip of several chunks streams")
					if target > 0 {
						got, err = got.WithRate(target)
						require.NoError(t, err)
					}

					assert.Equal(t, want.SampleRate(), got.SampleRate())
					assert.Equal(t, want.SpanSamples(), got.SpanSamples())
					first := collectStream(t, got)
					assert.Equal(t, collectStream(t, want), first)
					assert.Equal(t, first, collectStream(t, got), "a second Stream delivers the same samples")
				})
			}
		})
	}
}

// A WAV or FLAC cut short must fail from Stream, before Render builds a picture.
func TestOpenMono_TruncatedSourceFailsWhenStreamed(t *testing.T) {
	t.Parallel()

	const frames = 3 * chunkFrames
	wav := wavBytes(t, testRate, 1, frames)
	type cutFile struct {
		name string
		ext  string
		data []byte
	}
	flacCuts := truncatedFLACCuts(t, streamTestSeconds)
	tests := make([]cutFile, 0, 1+len(flacCuts))
	tests = append(tests, cutFile{name: "WAV cut to half its data", ext: ".wav", data: wav[:wavHeaderBytes+frames/2*bytesPerSample]})
	for _, cut := range flacCuts {
		tests = append(tests, cutFile{name: "FLAC cut " + cut.name, ext: ".flac", data: cut.data})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "cut"+tt.ext)
			writeBytes(t, path, tt.data)

			m, err := OpenMono(t.Context(), path, 0)
			require.NoError(t, err)
			require.True(t, m.Streamed())

			err = m.Stream(t.Context(), func([]float32) error { return nil })
			require.ErrorIs(t, err, ErrTruncatedSource)
			require.ErrorIs(t, err, ErrUndecodableSource)
		})
	}
}

// A header that declares no usable length, or one outside the streaming bounds,
// keeps the buffered decode, so those clips behave as they did.
func TestOpenMono_FallsBackToBufferedDecode(t *testing.T) {
	t.Parallel()

	const frames = 2 * chunkFrames
	wavWithSize := func(size uint32) []byte {
		data := wavBytes(t, testRate, 1, frames)
		binary.LittleEndian.PutUint32(data[wavDataSizeOffset:], size)
		return data
	}
	unknownSize := wavBytes(t, testRate, 1, frames)
	for _, off := range []int{wavRIFFSizeOffset, wavDataSizeOffset} {
		copy(unknownSize[off:off+wavSizeFieldBytes], make([]byte, wavSizeFieldBytes))
	}
	flacPath := filepath.Join(t.TempDir(), "src.flac")
	writeEncoded(t, audioformat.FLAC, 0, flacPath, tone(testRate, 1, streamTestSeconds, testToneHz, testAmp), testRate)
	flacNoTotal := readFile(t, flacPath)
	// STREAMINFO starts after the 4-byte marker and the 4-byte block header. The
	// 36-bit total is the low nibble of the 14th byte and the four bytes after it.
	const flacTotalHighByte = 8 + 13
	flacNoTotal[flacTotalHighByte] &= 0xF0
	clear(flacNoTotal[flacTotalHighByte+1 : flacTotalHighByte+5])

	tests := []struct {
		name     string
		ext      string
		data     []byte
		maxBytes int
	}{
		{name: "WAV with a zero data size", ext: ".wav", data: unknownSize, maxBytes: math.MaxInt},
		{name: "WAV with a data size of 0xFFFFFFFF", ext: ".wav", data: wavWithSize(math.MaxUint32), maxBytes: math.MaxInt},
		{name: "FLAC with no total", ext: ".flac", data: flacNoTotal, maxBytes: math.MaxInt},
		{name: "declared length above the ceiling", ext: ".wav", data: wavBytes(t, testRate, 1, frames), maxBytes: (frames - 1) * bytesPerSample},
		{name: "shorter than one chunk", ext: ".wav", data: wavBytes(t, testRate, 1, chunkFrames-1), maxBytes: math.MaxInt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "clip"+tt.ext)
			writeBytes(t, path, tt.data)

			m, err := openMono(t.Context(), path, 0, tt.maxBytes)
			require.NoError(t, err)
			assert.False(t, m.Streamed())

			want, err := DecodeMono(t.Context(), path, 0)
			require.NoError(t, err)
			assert.Equal(t, want.SpanSamples(), m.SpanSamples())
			assert.Equal(t, collectStream(t, want), collectStream(t, m))
		})
	}
}

// Cancelling inside a chunk must stop the file read, not let it run on.
func TestMonoSource_FileStreamStopsOnCancel(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "long.wav")
	writeBytes(t, path, wavBytes(t, testRate, 1, 3*chunkFrames+100))
	m, err := OpenMono(t.Context(), path, 0)
	require.NoError(t, err)
	require.True(t, m.Streamed())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0

	err = m.Stream(ctx, func([]float32) error {
		calls++
		cancel()
		return nil
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls)
}

// heapAlloc returns the live heap after a collection.
func heapAlloc() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// A streamed source must not hold the clip: the live heap part way through the
// stream, against the heap before the source was opened, stays well below the
// decoded size. Not parallel, since it reads the process-wide heap.
func TestOpenMono_StreamHeapDoesNotGrowWithClipLength(t *testing.T) {
	const decodedBytes = heapTestSeconds * testRate * bytesPerSample
	for _, name := range []string{"wav", "m4a"} {
		t.Run(name, func(t *testing.T) {
			path, _ := writeFixture(t, fixtureNamed(t, name), testRate, heapTestSeconds)
			before := heapAlloc()

			m, err := OpenMono(t.Context(), path, 0)
			require.NoError(t, err)
			require.True(t, m.Streamed())
			var during uint64
			chunks := 0
			require.NoError(t, m.Stream(t.Context(), func([]float32) error {
				chunks++
				if chunks == heapTestChunk {
					during = heapAlloc()
				}
				return nil
			}))

			require.GreaterOrEqual(t, chunks, heapTestChunk)
			assert.Less(t, int64(during)-int64(before), int64(decodedBytes/2), "heap before %d, during the stream %d", before, during)
		})
	}
}

// mp3WithDoubledFrameCount returns an MP3 whose Xing or Info tag declares twice
// the frames the file holds.
func mp3WithDoubledFrameCount(t *testing.T, data []byte) []byte {
	t.Helper()
	head := data[:min(len(data), xingSearchBytes)]
	idx := bytes.Index(head, []byte("Xing"))
	if idx < 0 {
		idx = bytes.Index(head, []byte("Info"))
	}
	require.GreaterOrEqual(t, idx, 0, "the production encoder writes a length tag")
	const flagsOffset = 4
	require.NotZero(t, binary.BigEndian.Uint32(data[idx+flagsOffset:])&1, "the tag carries a frame count")
	out := bytes.Clone(data)
	frames := binary.BigEndian.Uint32(out[idx+xingFramesOffset:])
	binary.BigEndian.PutUint32(out[idx+xingFramesOffset:], 2*frames)
	return out
}

// A non-exact source whose header declares more than it delivers (a foreign MP3
// with a wrong length tag) must report it, so the caller can render it buffered.
func TestOpenMono_StreamReportsASourceShorterThanDeclared(t *testing.T) {
	t.Parallel()

	good, _ := writeFixture(t, fixtureNamed(t, "mp3"), testRate, streamTestSeconds)
	path := filepath.Join(t.TempDir(), "doubled.mp3")
	writeBytes(t, path, mp3WithDoubledFrameCount(t, readFile(t, good)))

	m, err := OpenMono(t.Context(), path, 0)
	require.NoError(t, err)
	require.True(t, m.Streamed())

	err = m.Stream(t.Context(), func([]float32) error { return nil })
	require.ErrorIs(t, err, ErrShorterThanDeclared)
	require.NotErrorIs(t, err, ErrUndecodableSource, "this is a signal to decode buffered, not a source fault")
	_, enhanced := errors.AsType[*errors.EnhancedError](err)
	assert.False(t, enhanced, "the signal is built without the errors builder, so it is not reported")

	buffered, err := DecodeMono(t.Context(), path, 0)
	require.NoError(t, err, "the buffered decode still renders what the file holds")
	assert.Less(t, buffered.SpanSamples(), m.SpanSamples())
}

// A file replaced between OpenMono and Stream must not stream with the shape the
// header declared at open: that would draw a span the audio does not have.
func TestMonoSource_StreamRejectsAFileChangedSinceOpen(t *testing.T) {
	t.Parallel()

	const frames = 2 * chunkFrames
	tests := []struct {
		name        string
		replacement []byte
	}{
		{name: "different sample rate", replacement: wavBytes(t, 44100, 1, frames)},
		{name: "longer file", replacement: wavBytes(t, testRate, 1, frames+chunkFrames)},
		{name: "shorter file", replacement: wavBytes(t, testRate, 1, chunkFrames)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "clip.wav")
			writeBytes(t, path, wavBytes(t, testRate, 1, frames))
			m, err := OpenMono(t.Context(), path, 0)
			require.NoError(t, err)
			require.True(t, m.Streamed())
			writeBytes(t, path, tt.replacement)

			err = m.Stream(t.Context(), func([]float32) error { return nil })

			require.ErrorIs(t, err, ErrUndecodableSource)
			require.NotErrorIs(t, err, ErrTruncatedSource, "the changed header is reported before any frame is read")
		})
	}
}

// A WAV whose header declares audio but whose body is empty has no audio to
// draw: that is undecodable, not truncated, as the buffered decode reports it.
func TestOpenMono_HeaderOnlyWAVHoldsNoAudio(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.wav")
	writeBytes(t, path, wavBytes(t, testRate, 1, 2*chunkFrames)[:wavHeaderBytes])
	m, err := OpenMono(t.Context(), path, 0)
	require.NoError(t, err)
	require.True(t, m.Streamed())

	err = m.Stream(t.Context(), func([]float32) error { return nil })

	require.ErrorIs(t, err, ErrUndecodableSource)
	require.NotErrorIs(t, err, ErrTruncatedSource)
	_, bufferedErr := DecodeMono(t.Context(), path, 0)
	require.ErrorIs(t, bufferedErr, ErrUndecodableSource)
	require.NotErrorIs(t, bufferedErr, ErrTruncatedSource, "the streamed classification matches the buffered one")
}
