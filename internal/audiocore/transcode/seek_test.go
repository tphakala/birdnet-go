package transcode

import (
	"bytes"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// seekClipSeconds is the length of the clips the seek tests window into. For
	// Opus it spans three audio pages.
	seekClipSeconds = 12.0
	// seekMidFileSeconds is a window start past the first Opus audio page and its
	// pre-roll, inside every format's clip.
	seekMidFileSeconds = 7.0
	// seekWindowFrames is the length of the windows the seek tests read.
	seekWindowFrames = 3000
	// opusPreRollFrames is how far go-opus's SeekToSample decodes before its
	// target (80 ms at 48 kHz). The window starts below use it to sit around the
	// positions where a seek would land on a page.
	opusPreRollFrames = 3840
	// opusMinAudioPages is the number of audio pages the Opus window test needs to
	// have starts past the first page.
	opusMinAudioPages = 3
)

// Every clip format with a seek positions a window with it: one seek call for a
// start after frame 0, none for a start at frame 0. Opus has no seek and is
// covered by TestDecode_OpusWindowsMatchTheLinearDecode.
func TestDecodeSource_WindowsAfterFrameZeroSeek(t *testing.T) {
	t.Parallel()

	for _, fx := range fixtures() {
		if fx.ext == ".opus" {
			continue
		}
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			path, _ := writeFixture(t, fx, testRate, seekClipSeconds)
			whole, err := decode(t.Context(), path, nil, 0)
			require.NoError(t, err)
			size := int64(len(readFile(t, path)))

			// counted wraps the source's seek with a call counter that also checks
			// the seek reports the frame it was asked for.
			counted := func(src *source) *int {
				require.NotNil(t, src.seek, "the format must have a seek")
				calls := 0
				seek := src.seek
				src.seek = func(frame int64) (int64, error) {
					calls++
					landed, err := seek(frame)
					assert.Equal(t, frame, landed)
					return landed, err
				}
				return &calls
			}

			const start = seekMidFileSeconds * testRate
			src := openTestSource(t, path)
			calls := counted(src)
			req := &window{Start: seekMidFileSeconds, Duration: float64(seekWindowFrames) / testRate}
			win, err := decodeSource(t.Context(), src, req, size, len(whole.Data))
			require.NoError(t, err)
			assert.Equal(t, 1, *calls, "a start after frame 0 seeks exactly once")
			want := whole.Data[start*bytesPerSample : (start+seekWindowFrames)*bytesPerSample]
			if fx.name == "mp3" {
				assert.GreaterOrEqual(t, correlation(bytesInt16(want), bytesInt16(win.Data)), minWindowCorrelation)
			} else {
				assert.True(t, bytes.Equal(want, win.Data), "the window must equal the matching slice of the whole decode")
			}

			src = openTestSource(t, path)
			calls = counted(src)
			req = &window{Start: 0, Duration: float64(seekWindowFrames) / testRate}
			_, err = decodeSource(t.Context(), src, req, size, len(whole.Data))
			require.NoError(t, err)
			assert.Zero(t, *calls, "a start at frame 0 never seeks")
		})
	}
}

// Opus windows are positioned by discarding, because go-opus's SeekToSample is
// not bit-exact on CELT, so they equal the matching slice of the linear decode
// byte for byte, also around the pre-roll and page boundaries a seek would land on.
func TestDecode_OpusWindowsMatchTheLinearDecode(t *testing.T) {
	t.Parallel()

	path, _ := writeFixture(t, fixtureNamed(t, "opus"), testRate, seekClipSeconds)
	assert.Nil(t, openTestSource(t, path).seek, "Opus windows are positioned by discarding")
	total := int64(decodedFrames(t, path))

	pageEnds := opusPageEnds(t, readFile(t, path), total)
	require.GreaterOrEqual(t, len(pageEnds), opusMinAudioPages)
	b1, b2 := pageEnds[0], pageEnds[1]

	// b1+3840 is the first start whose seek would land past the first audio page.
	starts := []int64{
		1, opusPreRollFrames - 1, opusPreRollFrames, opusPreRollFrames + 1,
		b1 + opusPreRollFrames - 1, b1 + opusPreRollFrames, b1 + opusPreRollFrames + 1,
		b2 + opusPreRollFrames, total / 2, total - 1,
	}
	assertWindowsMatchLinear(t, path, starts)
}

// A stream go-opus cannot index keeps an unknown length.
func TestOpenOpus_TruncatedStreamKeepsLengthUnknown(t *testing.T) {
	t.Parallel()

	path, _ := writeFixture(t, fixtureNamed(t, "opus"), testRate, seekClipSeconds)
	data := readFile(t, path)
	firstAudio := oggPages(data)[opusHeaderPages]
	cut := filepath.Join(t.TempDir(), "cut.opus")
	writeBytes(t, cut, data[:firstAudio[0]+(firstAudio[1]-firstAudio[0])/2])

	src := openTestSource(t, cut)
	assert.Equal(t, int64(unknownFrames), src.totalFrames)
}

// An intact Opus stream reports its length from the stream.
func TestOpenOpus_TotalFramesComeFromLength(t *testing.T) {
	t.Parallel()

	path, _ := writeFixture(t, fixtureNamed(t, "opus"), testRate, seekClipSeconds)
	whole, err := decode(t.Context(), path, nil, 0)
	require.NoError(t, err)

	src := openTestSource(t, path)
	assert.Equal(t, int64(whole.Frames()), src.totalFrames)
}

// Every format clip export can write has a decoder, and the table holds no other.
func TestSourceOpeners_CoverEveryClipFormat(t *testing.T) {
	t.Parallel()

	formats := append(slices.Collect(maps.Keys(fileEncoders)), audioformat.WAV)
	for _, format := range formats {
		assert.Contains(t, sourceOpeners, "."+convert.GetFileExtension(format), "format %s", format)
	}
	assert.Len(t, sourceOpeners, len(formats))
}

// rampSource is a source of unknown length without a seek, holding frames
// mono frames whose 16-bit value is the frame index.
func rampSource(frames int) *source {
	data := make([]int16, frames)
	for i := range data {
		data[i] = int16(i) //nolint:gosec // G115: the test keeps frames below the int16 range
	}
	return &source{
		reader:      bytes.NewReader(int16Bytes(data)),
		sampleRate:  testRate,
		channels:    1,
		totalFrames: unknownFrames,
		expansion:   lossyExpansion,
	}
}

// A source without a seek and of unknown length is positioned by discarding, and
// a start past its end is an empty range.
func TestDecodeSource_UnknownLengthWindowIsPositionedByDiscarding(t *testing.T) {
	t.Parallel()

	const (
		rampFrames  = 2000
		startFrame  = 700
		windowCount = 300
	)
	req := &window{Start: float64(startFrame) / testRate, Duration: float64(windowCount) / testRate}
	win, err := decodeSource(t.Context(), rampSource(rampFrames), req, 0, maxDecodedPCMBytes)
	require.NoError(t, err)
	want := make([]int16, windowCount)
	for i := range want {
		want[i] = int16(startFrame + i) //nolint:gosec // G115: below the int16 range
	}
	assert.Equal(t, want, bytesInt16(win.Data))

	past := &window{Start: float64(rampFrames+1) / testRate, Duration: 1}
	_, err = decodeSource(t.Context(), rampSource(rampFrames), past, 0, maxDecodedPCMBytes)
	require.ErrorIs(t, err, ErrEmptyRange)
}

// A failing seek is an undecodable source with its cause kept, and a seek that
// lands short of the target is an empty range.
func TestPositionSource_SeekFailureAndShortLanding(t *testing.T) {
	t.Parallel()

	const target = 1000
	t.Run("failure", func(t *testing.T) {
		t.Parallel()
		cause := io.ErrUnexpectedEOF
		src := rampSource(2000)
		src.seek = func(int64) (int64, error) { return 0, cause }
		err := positionSource(t.Context(), src, target, bytesPerSample, true)
		require.ErrorIs(t, err, ErrUndecodableSource)
		require.ErrorIs(t, err, cause)
		assert.False(t, errors.Is(err, ErrEmptyRange))
	})
	t.Run("short landing", func(t *testing.T) {
		t.Parallel()
		src := rampSource(2000)
		src.seek = func(frame int64) (int64, error) { return frame - 1, nil }
		err := positionSource(t.Context(), src, target, bytesPerSample, true)
		require.ErrorIs(t, err, ErrEmptyRange)
	})
}
