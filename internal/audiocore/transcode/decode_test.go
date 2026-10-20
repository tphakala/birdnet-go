package transcode

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	m4a "github.com/tphakala/go-m4a"
	aacm4a "github.com/tphakala/go-m4a/aacm4a"
	wavpcm "github.com/tphakala/go-wav/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// lossyFrameSlack is how far a lossy decode's frame count may stray from the
	// source length (codec delay and block padding).
	lossyFrameSlack = 2304
	// minWindowCorrelation is the least normalized correlation between a
	// window decode and the matching slice of a whole decode for MP3, which
	// seeks and re-primes the bit reservoir.
	minWindowCorrelation = 0.99
	// onsetTolerance is how many frames the first loud sample of a trimmed M4A
	// decode may sit from the source's.
	onsetTolerance = 40
	// burstThreshold marks the first loud sample of a burst, above any codec
	// pre-echo.
	burstThreshold = 4000
)

func TestDecode_RoundTripPerFormat(t *testing.T) {
	t.Parallel()

	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			path, src := writeFixture(t, fx, testRate, testSeconds)

			pcm, err := decode(t.Context(), path, nil, 0)
			require.NoError(t, err)
			assert.Equal(t, testRate, pcm.SampleRate)
			assert.Equal(t, 1, pcm.Channels)

			got := bytesInt16(pcm.Data)
			if fx.lossless {
				require.Len(t, got, len(src), "lossless formats keep every frame")
				for i, s := range src {
					require.Equal(t, s, got[i], "sample %d", i)
				}
				return
			}
			assert.Len(t, got, len(src), "edit-list, pre-skip and gapless-tag trimming make the length exact")
			assert.Truef(t, dominantIs(channelOf(got, 1, 0), testRate, testToneHz, testOffToneH), "the decode should be dominated by %v Hz", testToneHz)
		})
	}
}

func TestDecode_M4ATrimsPrimingAndPadding(t *testing.T) {
	t.Parallel()

	src := burst(testRate, 1, testSeconds)
	path := filepath.Join(t.TempDir(), "burst.m4a")
	writeEncoded(t, audioformat.AAC, lossyBitrateKbps, path, src, testRate)

	pcm, err := decode(t.Context(), path, nil, 0)
	require.NoError(t, err)
	assert.Equal(t, len(src), pcm.Frames(), "edit list trimming restores the source length")

	want := firstAbove(src, 1, burstThreshold)
	got := firstAbove(bytesInt16(pcm.Data), 1, burstThreshold)
	require.NotEqual(t, -1, got)
	assert.InDelta(t, want, got, onsetTolerance, "priming samples are skipped so the burst lines up")
}

func TestDecode_WindowMatchesWholeDecodeSlice(t *testing.T) {
	t.Parallel()

	const startSec, durSec = 0.5, 1.0
	startFrame, frames := int(startSec*testRate), int(durSec*testRate)

	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			path, _ := writeFixture(t, fx, testRate, testSeconds)

			whole, err := decode(t.Context(), path, nil, 0)
			require.NoError(t, err)
			win, err := decode(t.Context(), path, &window{Start: startSec, Duration: durSec}, 0)
			require.NoError(t, err)

			require.Equal(t, frames, win.Frames())
			want := whole.Data[startFrame*2 : (startFrame+frames)*2]
			if fx.name == "mp3" {
				assert.GreaterOrEqual(t, correlation(bytesInt16(want), bytesInt16(win.Data)), minWindowCorrelation)
				return
			}
			assert.True(t, bytes.Equal(want, win.Data), "window must equal the matching slice of the whole decode")
		})
	}
}

// correlation returns the normalized correlation of two equal-length signals.
func correlation(a, b []int16) float64 {
	var ab, aa, bb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		ab += x * y
		aa += x * x
		bb += y * y
	}
	return ab / math.Sqrt(aa*bb)
}

// The extension alone picks the decoder, and only the clip export formats are
// known.
func TestDecode_UnknownContainerIsUnsupported(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"data.bin", "x.aac", "x.ogg", "x.mp4", "noext"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), name)
			require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte{0x01, 0x02, 0x03}, 100), 0o600))
			_, err := decode(t.Context(), path, nil, 0)
			require.ErrorIs(t, err, ErrUnsupportedSource)
		})
	}
}

func TestDecodeAtLeast_RejectsALowRateFromTheHeader(t *testing.T) {
	t.Parallel()

	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			path, _ := writeFixture(t, fx, testRate, 0.5)
			_, err := decodeAtLeast(t.Context(), path, nil, 0, testRate+1)
			require.ErrorIs(t, err, ErrBelowMinimumRate)
			pcm, err := decodeAtLeast(t.Context(), path, nil, 0, testRate)
			require.NoError(t, err)
			assert.Equal(t, testRate, pcm.SampleRate)
		})
	}
}

// A library error is a corrupt or truncated file, never an unsupported one, and
// the cause stays in the chain. A failed read of the file itself is a server
// fault: no sentinel, the file I/O category.
func TestDecodeError_Classification(t *testing.T) {
	t.Parallel()

	for name, cause := range map[string]error{
		"unexpected eof":   io.ErrUnexpectedEOF,
		"library sentinel": m4a.ErrUnsupported,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := decodeError("test", cause)
			require.ErrorIs(t, got, ErrUndecodableSource)
			assert.False(t, errors.Is(got, ErrUnsupportedSource), "a library error is not an unsupported source")
			require.ErrorIs(t, got, cause, "the decoder error stays in the chain")
		})
	}

	t.Run("file read failure", func(t *testing.T) {
		t.Parallel()
		cause := &fs.PathError{Op: "read", Path: "clip.wav", Err: io.ErrClosedPipe}
		got := decodeError("test", fmt.Errorf("decoder: %w", cause))
		assert.False(t, errors.Is(got, ErrUndecodableSource), "a disk fault is not a damaged clip")
		require.ErrorIs(t, got, cause)
		enhanced, ok := errors.AsType[*errors.EnhancedError](got)
		require.True(t, ok)
		assert.Equal(t, errors.CategoryFileIO, enhanced.Category)
	})
}

// Opening a clip through a closed file reports the read failure of the file, not
// a damaged clip.
func TestOpenSource_ClosedFileIsAFileIOError(t *testing.T) {
	t.Parallel()

	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			path, _ := writeFixture(t, fx, testRate, 0.5)
			f, err := os.Open(path) //nolint:gosec // G304: a test fixture path
			require.NoError(t, err)
			require.NoError(t, f.Close())

			_, err = openSource(f, path)
			require.Error(t, err)
			assert.False(t, errors.Is(err, ErrUndecodableSource))
			enhanced, ok := errors.AsType[*errors.EnhancedError](err)
			require.True(t, ok)
			assert.Equal(t, errors.CategoryFileIO, enhanced.Category)
		})
	}
}

// failingReads is an io.ReadSeeker that seeks the file and whose Read fails
// with an *fs.PathError once failing is set.
type failingReads struct {
	*os.File
	failing bool
}

func (r *failingReads) Read(p []byte) (int, error) {
	if r.failing {
		return 0, &fs.PathError{Op: "read", Path: r.Name(), Err: io.ErrClosedPipe}
	}
	return r.File.Read(p)
}

// A file read that fails while decoding M4A frames, after the open succeeded,
// reports the failure of the file, not a damaged clip: go-m4a keeps the
// *fs.PathError of a failed frame read in the chain. The decoder is built as
// openM4A builds it, over a reader whose Read starts failing after the open.
func TestDecodeError_M4AFrameReadFailureIsAFileIOError(t *testing.T) {
	t.Parallel()

	path, _ := writeFixture(t, fixtureNamed(t, "m4a"), testRate, 0.5)
	f, err := os.Open(path) //nolint:gosec // G304: a test fixture path
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, f.Close()) })
	r := &failingReads{File: f}
	dec, info, err := aacm4a.NewSeekableDecoder(r, m4a.WithMaxBoxBuffer(maxM4ABoxBytes))
	require.NoError(t, err)
	r.failing = true

	_, readErr := dec.Read(make([]byte, chunkBytes(info.Channels*bytesPerSample)))
	require.Error(t, readErr)
	_, isPathErr := errors.AsType[*fs.PathError](readErr)
	require.True(t, isPathErr, "the frame read failure keeps its *fs.PathError: %v", readErr)

	got := decodeError("test", readErr)
	assert.False(t, errors.Is(got, ErrUndecodableSource), "a disk fault is not a damaged clip")
	enhanced, ok := errors.AsType[*errors.EnhancedError](got)
	require.True(t, ok)
	assert.Equal(t, errors.CategoryFileIO, enhanced.Category)
}

func TestDecode_CorruptAndTruncatedInputs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := filepath.Join(dir, "good.flac")
	writeEncoded(t, audioformat.FLAC, 0, good, tone(testRate, 1, 1, testToneHz, testAmp), testRate)
	goodBytes := readFile(t, good)

	m4aGood := filepath.Join(dir, "good.m4a")
	writeEncoded(t, audioformat.AAC, lossyBitrateKbps, m4aGood, tone(testRate, 1, 1, testToneHz, testAmp), testRate)
	m4aBytes := readFile(t, m4aGood)

	tests := []struct {
		name string
		file string
		data []byte
	}{
		{name: "zero bytes wav", file: "zero.wav", data: nil},
		{name: "zero bytes flac", file: "zero.flac", data: nil},
		{name: "riff header only", file: "hdr.wav", data: []byte("RIFF\x24\x00\x00\x00WAVE")},
		{name: "junk after riff", file: "junk.wav", data: append([]byte("RIFF\x24\x00\x00\x00WAVE"), bytes.Repeat([]byte{0xAB}, 200)...)},
		{name: "flac marker only", file: "m.flac", data: []byte("fLaC")},
		{name: "truncated flac frame", file: "t.flac", data: goodBytes[:len(goodBytes)*2/3]},
		{name: "truncated flac header", file: "th.flac", data: goodBytes[:20]},
		{name: "ogg capture only", file: "o.opus", data: []byte("OggS")},
		{name: "truncated m4a", file: "t.m4a", data: m4aBytes[:len(m4aBytes)/3]},
		{name: "ftyp only", file: "f.m4a", data: []byte("\x00\x00\x00\x08ftyp")},
		{name: "mp3 junk", file: "j.mp3", data: bytes.Repeat([]byte{0x00, 0x11}, 500)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), tt.file)
			require.NoError(t, os.WriteFile(path, tt.data, 0o600))

			assert.NotPanics(t, func() {
				_, err := decode(t.Context(), path, nil, 0)
				require.Error(t, err)
				require.ErrorIs(t, err, ErrUndecodableSource)
			})
		})
	}
}

// wavBytes returns a mono 16-bit WAV of n zero frames at rate.
func wavBytes(t *testing.T, rate, channels, frames int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wavpcm.EncodeInterleaved(&buf, wavpcm.Config{SampleRate: rate, BitDepth: 16, Channels: channels}, make([]byte, frames*channels*2)))
	return buf.Bytes()
}

func TestDecode_BoundsRejectBeforeAllocating(t *testing.T) {
	t.Parallel()

	t.Run("sample rate and channel bounds", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name     string
			rate, ch int
		}{
			{name: "rate 2147483647", rate: math.MaxInt32, ch: 1},
			{name: "rate above the maximum", rate: maxSourceSampleRate + 1, ch: 1},
			{name: "rate 4000", rate: 4000, ch: 1},
			{name: "two channels", rate: testRate, ch: 2},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				path := filepath.Join(t.TempDir(), "x.wav")
				require.NoError(t, os.WriteFile(path, wavBytes(t, tt.rate, tt.ch, 100), 0o600))
				_, err := decode(t.Context(), path, nil, 0)
				require.ErrorIs(t, err, ErrUnsupportedSource)
			})
		}
	})

	t.Run("source file over the size limit", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "huge.wav")
		f, err := os.Create(path) //nolint:gosec // G304: test path
		require.NoError(t, err)
		require.NoError(t, f.Truncate(maxSourceFileBytes+1))
		require.NoError(t, f.Close())
		_, err = decode(t.Context(), path, nil, 0)
		require.ErrorIs(t, err, ErrAudioTooLong)
	})

	t.Run("decoded size over the caller's cap", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "x.wav")
		writeWAVFile(t, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate)

		_, err := decode(t.Context(), path, nil, testRate) // 0.5 s worth of bytes
		require.ErrorIs(t, err, ErrAudioTooLong, "whole-file decode")
		_, err = decode(t.Context(), path, &window{Start: 0, Duration: 1}, testRate)
		require.ErrorIs(t, err, ErrAudioTooLong, "window decode")
	})
}

// This test reads the process-wide allocation counter, so it must not run in
// parallel with the tests that allocate buffers.
func TestDecode_HeaderClaimingHugeLengthDoesNotDriveAllocation(t *testing.T) {
	data := wavBytes(t, testRate, 1, 50)
	// Patch the RIFF size and the data chunk size (offset 40 in the 44-byte
	// canonical header) to claim 127 MiB.
	const claimed = 127 << 20
	binary.LittleEndian.PutUint32(data[4:], claimed+36)
	binary.LittleEndian.PutUint32(data[40:], claimed)
	path := filepath.Join(t.TempDir(), "lie.wav")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	var pcm *pcmBuffer
	var err error
	allocated := allocatedBy(func() { pcm, err = decode(t.Context(), path, nil, 0) })

	if err == nil {
		assert.LessOrEqual(t, pcm.Frames(), 50)
	} else {
		require.ErrorIs(t, err, ErrUndecodableSource)
	}
	const allocBudget = 16 << 20
	assert.Less(t, allocated, uint64(allocBudget), "a tiny file must not allocate for its claimed length")
}

func TestDecode_WindowPastEnd(t *testing.T) {
	t.Parallel()

	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			path, _ := writeFixture(t, fx, testRate, 1)
			_, err := decode(t.Context(), path, &window{Start: 30, Duration: 1}, 0)
			require.ErrorIs(t, err, ErrEmptyRange)
		})
	}
}

func TestDecode_WindowPartlyPastEndIsShortened(t *testing.T) {
	t.Parallel()

	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			path, src := writeFixture(t, fx, testRate, testSeconds)
			pcm, err := decode(t.Context(), path, &window{Start: 1.5, Duration: 10}, 0)
			require.NoError(t, err)
			want := len(src) - int(1.5*testRate)
			assert.InDelta(t, want, pcm.Frames(), lossyFrameSlack)
			assert.Less(t, pcm.Frames(), 10*testRate)
		})
	}
}

func TestDecode_RejectsHugeOrNonFiniteWindow(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.wav")
	writeWAVFile(t, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate)

	invalid := []window{
		{Start: math.NaN(), Duration: 1},
		{Start: math.Inf(1), Duration: 1},
		{Start: -1, Duration: 1},
		{Start: 0, Duration: math.NaN()},
		{Start: 0, Duration: math.Inf(1)},
		{Start: 0, Duration: 0},
		{Start: 0, Duration: -2},
	}
	for _, w := range invalid {
		_, err := decode(t.Context(), path, &w, 0)
		require.ErrorIsf(t, err, ErrInvalidRequest, "window %+v", w)
	}

	_, err := decode(t.Context(), path, &window{Start: 1e18, Duration: 1}, 0)
	require.ErrorIs(t, err, ErrEmptyRange, "an astronomically late start is simply past the end")

	// A window longer than the cap is rejected when the audio it covers is too.
	const capBytes = testRate // half a second of mono 16-bit audio
	_, err = decode(t.Context(), path, &window{Start: 0, Duration: 1}, capBytes)
	require.ErrorIs(t, err, ErrAudioTooLong)

	// A source of unknown length has nothing to clamp the window to, so the
	// requested length alone is held against the cap.
	unknown := &source{
		totalFrames: unknownFrames,
		reader:      bytes.NewReader(make([]byte, 2*testRate*bytesPerSample)),
		sampleRate:  testRate,
		channels:    1,
		expansion:   lossyExpansion,
	}
	_, err = decodeSource(t.Context(), unknown, &window{Start: 0, Duration: 1}, 0, capBytes)
	require.ErrorIs(t, err, ErrAudioTooLong, "a window on a source of unknown length is checked against the cap")
}

// An Opus source knows its length from the stream, so a window reaching past the
// end of a short clip is clamped to the audio that exists before the cap check.
func TestDecode_OpusWindowPastEndOfShortSourceIsClamped(t *testing.T) {
	t.Parallel()

	const (
		seconds  = 0.4
		capBytes = testRate * bytesPerSample / 2 // half a second of mono 16-bit audio
	)
	path := filepath.Join(t.TempDir(), "short.opus")
	writeEncoded(t, audioformat.Opus, opusBitrateKbps, path, tone(testRate, 1, seconds, testToneHz, testAmp), testRate)
	whole, err := decode(t.Context(), path, nil, 0)
	require.NoError(t, err)

	pcm, err := decode(t.Context(), path, &window{Start: 0, Duration: 10}, capBytes)
	require.NoError(t, err, "a window longer than the cap must be clamped to the audio that exists first")
	assert.Equal(t, whole.Frames(), pcm.Frames())
}

func TestDecode_WindowPastEndOfHighRateSourceIsClamped(t *testing.T) {
	t.Parallel()

	const (
		rate     = 192000
		capBytes = rate * 2 * 2 // two seconds of mono 16-bit audio
	)
	path := filepath.Join(t.TempDir(), "short.wav")
	writeWAVFile(t, path, tone(rate, 1, 0.5, testToneHz, testAmp), rate)

	// Ten seconds asked for, half a second exists: the clamped length fits the cap.
	pcm, err := decode(t.Context(), path, &window{Start: 0, Duration: 10}, capBytes)
	require.NoError(t, err, "a window longer than the cap must be clamped to the audio that exists first")
	assert.Equal(t, rate/2, pcm.Frames())
}

// zeroReader returns data forever.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { return len(p), nil }

// stuckReader makes no progress.
type stuckReader struct{}

func (stuckReader) Read([]byte) (int, error) { return 0, nil }

func TestDecode_CancelledContext(t *testing.T) {
	t.Parallel()

	t.Run("decode returns the context error", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "x.opus")
		writeEncoded(t, audioformat.Opus, opusBitrateKbps, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := decode(ctx, path, &window{Start: 0.5, Duration: 0.2}, 0)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("the discard loop stops on cancel", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := discardFrames(ctx, zeroReader{}, 1<<40, 2)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("the read loop stops on cancel", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := readLimited(ctx, zeroReader{}, 1<<30, 0, 2)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("a reader that makes no progress is undecodable", func(t *testing.T) {
		t.Parallel()
		_, err := readLimited(t.Context(), stuckReader{}, 1<<20, 0, 2)
		require.ErrorIs(t, err, ErrUndecodableSource)
		_, err = discardFrames(t.Context(), stuckReader{}, 100, 2)
		require.ErrorIs(t, err, ErrUndecodableSource)
	})
}

func TestReadLimited_TrimsPartialFrameAndCapsCapacity(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte{1}, 4*1000+3)
	out, err := readLimited(t.Context(), bytes.NewReader(data), len(data)+1, 0, 4)
	require.NoError(t, err)
	assert.Len(t, out, 4000, "a trailing partial frame is dropped")

	capped, err := readLimited(t.Context(), bytes.NewReader(data), 1000, 10, 4)
	require.NoError(t, err)
	assert.Len(t, capped, 1000)
	assert.LessOrEqual(t, cap(capped), 1000, "capacity never exceeds the limit")
}

// A corrupt length (a FLAC total, an MP3 tag or an Opus final granule) can
// declare a frame count whose byte size overflows int64. The synthetic source
// here declares 2^62 frames. The declared count only sizes buffers; it must never
// panic or drive allocation.
func TestDecodeSource_HugeDeclaredLengthDoesNotOverflow(t *testing.T) {
	t.Parallel()

	const declaredFrames = 1 << 62
	const actualFrames = 100
	src := &source{
		reader:      bytes.NewReader(make([]byte, actualFrames*bytesPerSample)),
		sampleRate:  testRate,
		channels:    1,
		totalFrames: declaredFrames,
		expansion:   losslessExpansion,
	}

	var pcm *pcmBuffer
	var err error
	require.NotPanics(t, func() {
		pcm, err = decodeSource(t.Context(), src, nil, actualFrames, maxDecodedPCMBytes)
	})
	require.NoError(t, err)
	assert.Equal(t, actualFrames, pcm.Frames(), "the bytes that exist are returned")

	const fileSize = 1 << 20
	plausible := fileSize*lossyExpansion + initialCapSlackBytes
	assert.Equal(t, plausible, initialCapacity(&source{totalFrames: declaredFrames, expansion: lossyExpansion}, fileSize, maxDecodedPCMBytes, bytesPerSample),
		"a declared count above the cap leaves the file-size estimate to size the buffer")
}

func TestDecode_AcceptsTheSourceRateBounds(t *testing.T) {
	t.Parallel()

	for _, rate := range []int{minSourceSampleRate, maxSourceSampleRate} {
		path := filepath.Join(t.TempDir(), "edge.wav")
		writeWAVFile(t, path, tone(rate, 1, 0.1, testToneHz, testAmp), rate)
		pcm, err := decode(t.Context(), path, nil, 0)
		require.NoError(t, err, "rate %d", rate)
		assert.Equal(t, rate, pcm.SampleRate)
	}
}

// Real decoders return short reads (go-m4a returns one access unit per Read), so
// fill must keep reading until the buffer holds whole frames, and drop a partial
// frame only at the end of the source.
func TestFrameReader_FillsWholeFramesAcrossShortReads(t *testing.T) {
	t.Parallel()

	const (
		frameBytes  = 4
		wholeFrames = 50
		tailBytes   = 3
		bufFrames   = 8
	)
	input := make([]byte, wholeFrames*frameBytes+tailBytes)
	for i := range input {
		input[i] = byte(i)
	}
	want := input[:wholeFrames*frameBytes]
	tests := []struct {
		name string
		wrap func(io.Reader) io.Reader
	}{
		{name: "one byte per read", wrap: iotest.OneByteReader},
		{name: "half the request per read", wrap: iotest.HalfReader},
		{name: "data returned together with EOF", wrap: iotest.DataErrReader},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fr := &frameReader{r: tt.wrap(bytes.NewReader(input)), frameBytes: frameBytes, op: "test_fill"}
			buf := make([]byte, bufFrames*frameBytes)
			var got []byte
			for calls := 0; ; calls++ {
				require.Less(t, calls, wholeFrames, "fill must reach the end of the source")
				n, eof, err := fr.fill(t.Context(), buf)
				require.NoError(t, err)
				got = append(got, buf[:n]...)
				if eof {
					break
				}
				require.Equal(t, len(buf), n, "before the end every call fills the buffer")
			}
			assert.Equal(t, want, got, "the partial tail frame is dropped, nothing else")
		})
	}
}
