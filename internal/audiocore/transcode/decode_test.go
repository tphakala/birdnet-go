package transcode

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	aacpcm "github.com/tphakala/go-aac/pcm"
	flac "github.com/tphakala/go-flac"
	m4a "github.com/tphakala/go-m4a"
	mp3 "github.com/tphakala/go-mp3"
	mp3pcm "github.com/tphakala/go-mp3/pcm"
	wav "github.com/tphakala/go-wav"
	wavpcm "github.com/tphakala/go-wav/pcm"

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
	// lossyBitrate is the bit rate, in bit/s, of the tagless MP3 fixture.
	lossyBitrate = 128_000
	// mp3FrameSamples is the number of samples in one MPEG-1 Layer III frame.
	mp3FrameSamples = 1152
	// burstThreshold marks the first loud sample of a burst, above any codec
	// pre-echo.
	burstThreshold = 4000
)

func TestDecode_RoundTripPerFormat(t *testing.T) {
	t.Parallel()

	for _, fx := range fixtures() {
		for _, channels := range []int{1, 2} {
			t.Run(fx.name+"_"+map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
				t.Parallel()
				path, src := writeFixture(t, fx, testRate, channels, testSeconds)

				pcm, err := decode(t.Context(), path, nil, 0)
				require.NoError(t, err)
				assert.Equal(t, testRate, pcm.SampleRate)
				assert.Equal(t, channels, pcm.Channels)

				got := bytesInt16(pcm.Data)
				if fx.lossless {
					require.Len(t, got, len(src), "lossless formats keep every frame")
					for i, s := range src {
						require.Equal(t, int16(int32(s)>>fx.quantShift<<fx.quantShift), got[i], "sample %d", i)
					}
					return
				}
				assert.InDelta(t, len(src)/channels, pcm.Frames(), lossyFrameSlack)
				if fx.name == "m4a" || fx.name == "opus" || fx.name == "mp3" {
					assert.Equal(t, len(src)/channels, pcm.Frames(), "edit-list, pre-skip and gapless-tag trimming make the length exact")
				}
				for ch := range channels {
					x := channelOf(got, channels, ch)
					assert.Truef(t, dominantIs(x, testRate, testToneHz, testOffToneH), "channel %d should be dominated by %v Hz", ch, testToneHz)
				}
			})
		}
	}
}

// MP3 clips written before the gapless tag existed carry no tag, so their
// priming is kept as it always was.
func TestDecode_TaglessMP3KeepsItsPriming(t *testing.T) {
	t.Parallel()

	src := tone(testRate, 1, testSeconds, testToneHz, testAmp)
	var buf bytes.Buffer
	require.NoError(t, mp3pcm.EncodeInterleaved(&buf, mp3pcm.Config{
		SampleRate: testRate, Channels: 1, Bitrate: lossyBitrate, OmitGaplessTag: true,
	}, int16Bytes(src)))
	path := filepath.Join(t.TempDir(), "tagless.mp3")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))

	pcm, err := decode(t.Context(), path, nil, 0)
	require.NoError(t, err)
	assert.Greater(t, pcm.Frames(), len(src), "without a tag the encoder priming stays in the decode")
	assert.InDelta(t, len(src)+mp3.TotalDelay, pcm.Frames(), mp3FrameSamples)
}

func TestDecode_M4ATrimsPrimingAndPadding(t *testing.T) {
	t.Parallel()

	src := burst(testRate, 1, testSeconds)
	path := filepath.Join(t.TempDir(), "burst.m4a")
	writeM4AFile(t, path, src, testRate, 1)

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
			path, _ := writeFixture(t, fx, testRate, 1, testSeconds)

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

func TestDecode_SniffWinsOverExtension(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := tone(testRate, 1, 0.5, testToneHz, testAmp)

	flacPath := filepath.Join(dir, "real.flac")
	writeFLACFile(t, flacPath, src, testRate, 1, 16)
	flacBytes, err := os.ReadFile(flacPath)
	require.NoError(t, err)
	misnamed := filepath.Join(dir, "actually-flac.wav")
	require.NoError(t, os.WriteFile(misnamed, flacBytes, 0o600))

	pcm, err := decode(t.Context(), misnamed, nil, 0)
	require.NoError(t, err)
	assert.Equal(t, len(src), pcm.Frames())

	// Inconclusive leading bytes fall back to the extension: a WAV with leading
	// junk is not identifiable, an extensionless file is unknown.
	junk := filepath.Join(dir, "noext")
	require.NoError(t, os.WriteFile(junk, []byte("not audio at all, just text"), 0o600))
	_, err = decode(t.Context(), junk, nil, 0)
	require.ErrorIs(t, err, ErrUnsupportedSource)
}

func TestDecode_UnknownContainerIsUnsupported(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "data.bin")
	require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte{0x01, 0x02, 0x03}, 100), 0o600))
	_, err := decode(t.Context(), path, nil, 0)
	require.ErrorIs(t, err, ErrUnsupportedSource)
}

func TestDecode_OggThatIsNotOpusIsUnsupported(t *testing.T) {
	t.Parallel()

	// A minimal Ogg page whose payload is a Vorbis identification header.
	page := append([]byte("OggS\x00\x02"), make([]byte, 20)...)
	page = append(page, 0x01)
	page = append(page, []byte("\x01vorbis")...)
	path := filepath.Join(t.TempDir(), "vorbis.ogg")
	require.NoError(t, os.WriteFile(path, page, 0o600))

	_, err := decode(t.Context(), path, nil, 0)
	require.ErrorIs(t, err, ErrUnsupportedSource)
}

func TestDecodeAtLeast_RejectsALowRateFromTheHeader(t *testing.T) {
	t.Parallel()

	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			path, _ := writeFixture(t, fx, testRate, 1, 0.5)
			_, err := decodeAtLeast(t.Context(), path, nil, 0, testRate+1)
			require.ErrorIs(t, err, ErrBelowMinimumRate)
			pcm, err := decodeAtLeast(t.Context(), path, nil, 0, testRate)
			require.NoError(t, err)
			assert.Equal(t, testRate, pcm.SampleRate)
		})
	}
}

// heAACFile writes an AAC-LC m4a whose AudioSpecificConfig is patched to declare
// explicit SBR signalling (audio object type 5).
func heAACFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "he.m4a")
	writeM4AFile(t, path, tone(testRate, 1, 0.5, testToneHz, testAmp), testRate, 1)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	// AAC-LC, 48 kHz, mono: 00010 0011 0001 000 -> 0x11 0x88. Object type 5
	// (SBR) with the same rate and channel bits: 00101 0011 0001 000 -> 0x29 0x88.
	lc := []byte{0x11, 0x88}
	// DecoderSpecificInfo: tag 0x05, length 2, then the two ASC bytes.
	idx := bytes.Index(data, append([]byte{0x05, 0x02}, lc...))
	require.NotEqual(t, -1, idx, "the esds DecoderSpecificInfo must be findable in the fixture")
	copy(data[idx+2:], []byte{0x29, 0x88})
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestDecode_HEAACIsUnsupported(t *testing.T) {
	t.Parallel()

	path := heAACFile(t)
	_, err := decode(t.Context(), path, nil, 0)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrHEAACSource)
	require.ErrorIs(t, err, ErrUnsupportedSource)

	_, err = decodeAtLeast(t.Context(), path, nil, 0, testRate+1)
	require.ErrorIs(t, err, ErrHEAACSource, "the header check classifies HE-AAC the same way")
}

func TestClassifyDecodeError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		wantAny      []error
		wantNot      []error
		wantPassthru bool
	}{
		{name: "SBR is HE-AAC", err: aacpcm.ErrUnsupportedSBR, wantAny: []error{ErrHEAACSource, ErrUnsupportedSource}},
		{name: "PS is HE-AAC", err: aacpcm.ErrUnsupportedPS, wantAny: []error{ErrHEAACSource, ErrUnsupportedSource}},
		{name: "aac unsupported", err: aacpcm.ErrUnsupported, wantAny: []error{ErrUnsupportedSource}, wantNot: []error{ErrHEAACSource}},
		{name: "m4a unsupported", err: m4a.ErrUnsupported, wantAny: []error{ErrUnsupportedSource}},
		{name: "m4a box too large", err: m4a.ErrBoxTooLarge, wantAny: []error{ErrUnsupportedSource}},
		{name: "wav unsupported", err: wav.ErrUnsupported, wantAny: []error{ErrUnsupportedSource}},
		{name: "wav not riff", err: wav.ErrNotRIFF, wantAny: []error{ErrUnsupportedSource}},
		{name: "flac unsupported", err: flac.ErrUnsupported, wantAny: []error{ErrUnsupportedSource}},
		{name: "mp3 unsupported", err: mp3.ErrUnsupported, wantAny: []error{ErrUnsupportedSource}},
		{name: "unexpected eof is undecodable", err: io.ErrUnexpectedEOF, wantAny: []error{ErrUndecodableSource}, wantNot: []error{ErrUnsupportedSource}},
		{name: "m4a corrupt is undecodable", err: m4a.ErrCorrupt, wantAny: []error{ErrUndecodableSource}},
		{name: "aac corrupt is undecodable", err: aacpcm.ErrCorruptStream, wantAny: []error{ErrUndecodableSource}},
		{name: "canceled passes through", err: context.Canceled, wantPassthru: true},
		{name: "deadline passes through", err: context.DeadlineExceeded, wantPassthru: true},
		{name: "sentinel passes through", err: ErrAudioTooLong, wantPassthru: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyDecodeError("test", tt.err)
			require.Error(t, got)
			if tt.wantPassthru {
				assert.Equal(t, tt.err, got)
				return
			}
			for _, want := range tt.wantAny {
				require.ErrorIs(t, got, want)
			}
			for _, not := range tt.wantNot {
				assert.False(t, errors.Is(got, not), "must not match %v", not)
			}
			require.ErrorIs(t, got, tt.err, "the decoder error stays in the chain")
		})
	}
}

func TestDecode_CorruptAndTruncatedInputs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := filepath.Join(dir, "good.flac")
	writeFLACFile(t, good, tone(testRate, 1, 1, testToneHz, testAmp), testRate, 1, 16)
	goodBytes, err := os.ReadFile(good)
	require.NoError(t, err)

	m4aGood := filepath.Join(dir, "good.m4a")
	writeM4AFile(t, m4aGood, tone(testRate, 1, 1, testToneHz, testAmp), testRate, 1)
	m4aBytes, err := os.ReadFile(m4aGood)
	require.NoError(t, err)

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
		{name: "adts junk", file: "j.aac", data: bytes.Repeat([]byte{0x00, 0x11}, 500)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), tt.file)
			require.NoError(t, os.WriteFile(path, tt.data, 0o600))

			assert.NotPanics(t, func() {
				_, err := decode(t.Context(), path, nil, 0)
				require.Error(t, err)
				assert.Truef(t, errors.Is(err, ErrUndecodableSource) || errors.Is(err, ErrUnsupportedSource),
					"want a typed source error, got %v", err)
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
			{name: "three channels", rate: testRate, ch: 3},
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
		writeWAVFile(t, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate, 1)

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

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	pcm, err := decode(t.Context(), path, nil, 0)
	runtime.ReadMemStats(&after)

	if err == nil {
		assert.LessOrEqual(t, pcm.Frames(), 50)
	} else {
		require.ErrorIs(t, err, ErrUndecodableSource)
	}
	const allocBudget = 16 << 20
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(allocBudget), "a tiny file must not allocate for its claimed length")
}

func TestDecode_WindowPastEnd(t *testing.T) {
	t.Parallel()

	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			path, _ := writeFixture(t, fx, testRate, 1, 1)
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
			path, src := writeFixture(t, fx, testRate, 1, testSeconds)
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
	writeWAVFile(t, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate, 1)

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

	// An Ogg Opus source declares no length, so there is nothing to clamp the
	// window to and the requested length alone is held against the cap.
	opusPath := filepath.Join(t.TempDir(), "long.opus")
	writeOpusFile(t, opusPath, tone(testRate, 1, 2, testToneHz, testAmp), testRate, 1)
	_, err = decode(t.Context(), opusPath, &window{Start: 0, Duration: 1}, capBytes)
	require.ErrorIs(t, err, ErrAudioTooLong, "a window on a source of unknown length is checked against the cap")
}

func TestDecode_WindowPastEndOfHighRateSourceIsClamped(t *testing.T) {
	t.Parallel()

	const (
		rate     = 192000
		capBytes = rate * 2 * 2 // two seconds of mono 16-bit audio
	)
	path := filepath.Join(t.TempDir(), "short.wav")
	writeWAVFile(t, path, tone(rate, 1, 0.5, testToneHz, testAmp), rate, 1)

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
		writeOpusFile(t, path, tone(testRate, 1, 1, testToneHz, testAmp), testRate, 1)
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

// A header can declare a frame count whose byte size overflows int64 (an 8-bit
// RF64 WAV declaring 2^62 frames is 2^63 bytes once widened to 16-bit). The
// declared count only sizes buffers; it must never panic or drive allocation.
func TestDecode_HugeDeclaredLength8BitWAV(t *testing.T) {
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

// Bytes that identify no container fall back to the extension, so an MP3 with
// leading padding still decodes.
func TestDecode_ExtensionDecidesWhenSniffingIsInconclusive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := filepath.Join(dir, "good.mp3")
	writeMP3File(t, good, tone(testRate, 1, 1, testToneHz, testAmp), testRate, 1)
	data, err := os.ReadFile(good)
	require.NoError(t, err)

	padded := filepath.Join(dir, "padded.mp3")
	require.NoError(t, os.WriteFile(padded, append(make([]byte, 4), data...), 0o600))
	pcm, err := decode(t.Context(), padded, nil, 0)
	require.NoError(t, err)
	assert.Equal(t, testRate, pcm.SampleRate)
	assert.InDelta(t, testRate, pcm.Frames(), lossyFrameSlack)

	unnamed := filepath.Join(dir, "padded")
	require.NoError(t, os.WriteFile(unnamed, append(make([]byte, 4), data...), 0o600))
	_, err = decode(t.Context(), unnamed, nil, 0)
	require.ErrorIs(t, err, ErrUnsupportedSource, "without a usable extension the container stays unknown")
}

func TestDecode_AcceptsTheSourceRateBounds(t *testing.T) {
	t.Parallel()

	for _, rate := range []int{minSourceSampleRate, maxSourceSampleRate} {
		path := filepath.Join(t.TempDir(), "edge.wav")
		writeWAVFile(t, path, tone(rate, 1, 0.1, testToneHz, testAmp), rate, 1)
		pcm, err := decode(t.Context(), path, nil, 0)
		require.NoError(t, err, "rate %d", rate)
		assert.Equal(t, rate, pcm.SampleRate)
	}
}
