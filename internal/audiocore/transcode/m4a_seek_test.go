package transcode

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	aaccodec "github.com/tphakala/go-aac"
	m4a "github.com/tphakala/go-m4a"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// m4aMidFileFrame is a start well after the pre-roll of any early unit.
	m4aMidFileFrame = 40000
	// noisyM4AWindowSeconds is the length of the noisy-window clip.
	noisyM4AWindowSeconds = 3.0
	// pnsMinCorrelation and pnsMaxLevelDiffDB are go-m4a's documented bounds for a
	// seek into a stream that uses perceptual noise substitution
	// (aacm4a/decoder.go, Decoder type comment).
	pnsMinCorrelation = 0.96
	pnsMaxLevelDiffDB = 0.1
	noisyM4ANoiseAmp  = 0.2
	noisyM4AToneAmp   = 0.2
	noisyM4ASeed      = 11
	// hugeEncoderDelay is the largest media_time a version 0 edit list can carry.
	hugeEncoderDelay = math.MaxInt32
)

// m4aInfoOf reads the container info of the m4a file at path.
func m4aInfoOf(t *testing.T, path string) m4a.Info {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // G304: a test fixture path
	require.NoError(t, err)
	defer func() { assert.NoError(t, f.Close()) }()
	rd, err := m4a.NewReader(f)
	require.NoError(t, err)
	return rd.Info()
}

// cleanM4ABytes writes a mono tone as m4a and returns the file's bytes.
func cleanM4ABytes(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clean.m4a")
	writeEncoded(t, audioformat.AAC, lossyBitrateKbps, path, tone(testRate, 1, testSeconds, testToneHz, testAmp), testRate)
	return readFile(t, path)
}

func TestDecode_M4AWindowsMatchTheLinearDecode(t *testing.T) {
	t.Parallel()

	path, _ := writeFixture(t, fixtureNamed(t, "m4a"), testRate, testSeconds)
	delay := m4aInfoOf(t, path).EncoderDelay
	total := int64(decodedFrames(t, path))

	// Positions in the decoder's output (priming included), converted to window
	// starts. They cover a start inside the first access units, exact unit
	// boundaries (with two units of pre-roll, 2*FrameSize is the first whose
	// pre-roll is not clamped to unit 0), one frame past a boundary, mid-file, and
	// the last frame.
	positions := []int64{
		delay + 1,
		aaccodec.FrameSize,
		2 * aaccodec.FrameSize,
		3 * aaccodec.FrameSize,
		3*aaccodec.FrameSize + 1,
		delay + m4aMidFileFrame,
	}
	var starts []int64
	for _, pos := range positions {
		if start := pos - delay; start >= 1 {
			starts = append(starts, start)
		}
	}
	starts = append(starts, total-1)
	assertWindowsMatchLinear(t, path, starts)
}

// The AAC writer also runs at 44.1 kHz, where a frame index does not map to a
// whole number of nanoseconds, so the seek's time rounding must still land on
// the frame the forward path discards to.
func TestDecode_M4AWindowsMatchTheLinearDecodeAt44k(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cd.m4a")
	writeEncoded(t, audioformat.AAC, lossyBitrateKbps, path, tone(referenceCDRate, 1, testSeconds, testToneHz, testAmp), referenceCDRate)
	total := int64(decodedFrames(t, path))
	assertWindowsMatchLinear(t, path, []int64{1, aaccodec.FrameSize + 1, m4aMidFileFrame + 1, total - 1})
}

// A seek to the end of the track reports a frame below the target, which
// positionSource turns into an empty range, rather than an error.
func TestOpenM4A_SeekPastTheEndLandsShort(t *testing.T) {
	t.Parallel()

	path, _ := writeFixture(t, fixtureNamed(t, "m4a"), testRate, testSeconds)
	src := openTestSource(t, path)
	require.Positive(t, src.totalFrames)
	landed, err := src.seek(src.totalFrames + 1)
	require.NoError(t, err)
	assert.Less(t, landed, src.totalFrames+1)
}

// patchEncoderDelay rewrites the media_time of the first version 0 elst entry.
func patchEncoderDelay(t *testing.T, data []byte, mediaTime uint32) []byte {
	t.Helper()
	const (
		elstTypeLen   = 4
		versionFlags  = 4
		entryCount    = 4
		segmentLength = 4
	)
	idx := bytes.Index(data, []byte("elst"))
	require.GreaterOrEqual(t, idx, 0, "the fixture carries an edit list")
	require.Zero(t, data[idx+elstTypeLen], "only version 0 edit lists are patched")
	out := bytes.Clone(data)
	binary.BigEndian.PutUint32(out[idx+elstTypeLen+versionFlags+entryCount+segmentLength:], mediaTime)
	return out
}

func TestOpenM4A_ClampsAHugeEncoderDelay(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "huge-delay.m4a")
	writeBytes(t, path, patchEncoderDelay(t, cleanM4ABytes(t), hugeEncoderDelay))
	info := m4aInfoOf(t, path)
	require.Equal(t, int64(hugeEncoderDelay), info.EncoderDelay, "the patch must reach Info.EncoderDelay")

	opened := openTestSource(t, path)
	assert.LessOrEqual(t, opened.skipFrames, int64(info.FrameCount)*aaccodec.FrameSize, "the priming skip is bounded by the decoded length")

	_, err := decode(t.Context(), path, &window{Start: 1, Duration: 1}, 0)
	require.Error(t, err)
	assert.Truef(t, errors.Is(err, ErrEmptyRange) || errors.Is(err, ErrUndecodableSource), "got %v", err)
}

// A noisy clip is coded with perceptual noise substitution, which a seek cannot
// reconstruct, so the window matches the linear decode only within go-m4a's
// documented bounds.
func TestDecode_M4ANoisyWindowWithinPNSTolerance(t *testing.T) {
	t.Parallel()

	src := noisePlusTone(noisyM4ASeed, testRate, int(noisyM4AWindowSeconds*testRate), 0, noisyM4ANoiseAmp, noisyM4AToneAmp)
	path := filepath.Join(t.TempDir(), "noisy.m4a")
	writeEncoded(t, audioformat.AAC, lossyBitrateKbps, path, src, testRate)

	whole, err := decode(t.Context(), path, nil, 0)
	require.NoError(t, err)

	const start = m4aMidFileFrame
	win, err := decode(t.Context(), path, &window{Start: float64(start) / testRate, Duration: float64(seekWindowFrames) / testRate}, 0)
	require.NoError(t, err)
	require.Equal(t, seekWindowFrames, win.Frames(), "the window length is exact")

	want := bytesInt16(whole.Data[start*bytesPerSample : (start+seekWindowFrames)*bytesPerSample])
	got := bytesInt16(win.Data)
	assert.GreaterOrEqual(t, correlation(want, got), pnsMinCorrelation, "correlation with the linear decode")
	assert.InDelta(t, 0, 20*math.Log10(rmsFloat64(channelOf(got, 1, 0))/rmsFloat64(channelOf(want, 1, 0))), pnsMaxLevelDiffDB, "level against the linear decode")
}
