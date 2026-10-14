package transcode

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	aacpcm "github.com/tphakala/go-aac/pcm"
	m4a "github.com/tphakala/go-m4a"
	aacm4a "github.com/tphakala/go-m4a/aacm4a"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// m4aUnitFrames is the number of sample frames one AAC access unit decodes to.
	m4aUnitFrames = 1024
	// m4aWindowFrames is the length of the windows in the seek table, shorter than
	// the file so the window sits well inside it.
	m4aWindowFrames = 3000
	// m4aMidFileFrame is a start well after the pre-roll of any early unit.
	m4aMidFileFrame = 40000
	// noisyM4AWindowSeconds is the length of the noisy-window clip.
	noisyM4AWindowSeconds = 3.0
	// pnsMinCorrelation and pnsMaxLevelDiffDB are go-m4a's documented bounds for a
	// seek into a stream that uses perceptual noise substitution
	// (aacm4a/decoder.go, Decoder type comment).
	pnsMinCorrelation  = 0.96
	pnsMaxLevelDiffDB  = 0.1
	noisyM4ANoiseAmp   = 0.2
	noisyM4AToneAmp    = 0.2
	noisyM4ASeed       = 11
	corruptedUnitIndex = 20
	// corruptLeadUnits is how many units before the corrupted one the fallback
	// window starts.
	corruptLeadUnits = 2
	// hugeEncoderDelay is the largest media_time a version 0 edit list can carry.
	hugeEncoderDelay = math.MaxInt32
)

// m4aEncoderDelay reads the container's EncoderDelay for path.
func m4aEncoderDelay(t *testing.T, path string) (delay int64, frameCount int) {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // G304: a test fixture path
	require.NoError(t, err)
	defer func() { assert.NoError(t, f.Close()) }()
	rd, err := m4a.NewReader(f)
	require.NoError(t, err)
	return rd.Info().EncoderDelay, rd.Info().FrameCount
}

func TestDecode_M4AWindowsMatchTheLinearDecode(t *testing.T) {
	t.Parallel()

	for _, channels := range []int{1, 2} {
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			t.Parallel()
			path, _ := writeFixture(t, fixture{ext: ".m4a", write: writeM4AFile}, testRate, channels, testSeconds)
			delay, _ := m4aEncoderDelay(t, path)

			whole, err := decode(t.Context(), path, nil, 0)
			require.NoError(t, err)
			total := int64(whole.Frames())
			frameBytes := channels * bytesPerSample

			// Positions in the decoder's output (priming included), converted to
			// window starts. They cover a start inside the first access units,
			// exact unit boundaries, the first start whose pre-roll is not clamped,
			// one frame past it, mid-file, and the last frame.
			positions := []int64{
				delay + 1,
				m4aUnitFrames,
				2 * m4aUnitFrames,
				3 * m4aUnitFrames,
				3*m4aUnitFrames + 1,
				delay + m4aMidFileFrame,
			}
			var starts []int64
			for _, pos := range positions {
				if start := pos - delay; start >= 1 {
					starts = append(starts, start)
				}
			}
			starts = append(starts, total-1)

			for _, start := range starts {
				req := &window{Start: float64(start) / testRate, Duration: float64(m4aWindowFrames) / testRate}
				want := whole.Data[start*int64(frameBytes) : min(start+m4aWindowFrames, total)*int64(frameBytes)]

				// decode falls back to the forward decoder when a seek fails, and that
				// output is the expected slice, so the seek is checked on its own.
				requireSeekServes(t, path, start)

				win, err := decode(t.Context(), path, req, 0)
				require.NoErrorf(t, err, "start %d", start)
				assert.Truef(t, bytes.Equal(want, win.Data), "start %d: the window must equal the matching slice of the whole decode (got %d frames, want %d)", start, win.Frames(), len(want)/frameBytes)

				again, err := decode(t.Context(), path, req, 0)
				require.NoError(t, err)
				assert.Truef(t, bytes.Equal(win.Data, again.Data), "start %d: a repeated request must return identical bytes", start)
			}

			// A window reaching past the end is shortened to the audio that exists.
			start := total - 100
			win, err := decode(t.Context(), path, &window{Start: float64(start) / testRate, Duration: 1}, 0)
			require.NoError(t, err)
			assert.True(t, bytes.Equal(whole.Data[start*int64(frameBytes):], win.Data))
		})
	}
}

// requireSeekServes opens path and positions it at start, and requires that the
// seekable decoder, not the forward one, holds the position.
func requireSeekServes(t *testing.T, path string, start int64) {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // G304: a test fixture path
	require.NoError(t, err)
	defer func() { assert.NoError(t, f.Close()) }()
	src, err := openSource(f, path)
	require.NoError(t, err)
	require.NoErrorf(t, positionSource(t.Context(), src, start, src.channels*bytesPerSample, true), "start %d", start)
	reader, ok := src.reader.(*m4aReader)
	require.True(t, ok)
	require.NotNilf(t, reader.seeked, "start %d must be served by the seekable decoder", start)
}

func TestOpenM4A_WindowAfterFrameZeroSeeks(t *testing.T) {
	t.Parallel()

	path, _ := writeFixture(t, fixture{ext: ".m4a", write: writeM4AFile}, testRate, 1, testSeconds)
	frameBytes := bytesPerSample

	open := func(t *testing.T) *source {
		t.Helper()
		f, err := os.Open(path) //nolint:gosec // G304: a test fixture path
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, f.Close()) })
		src, err := openSource(f, path)
		require.NoError(t, err)
		return src
	}

	t.Run("a start after frame zero seeks", func(t *testing.T) {
		t.Parallel()
		src := open(t)
		require.NoError(t, positionSource(t.Context(), src, m4aMidFileFrame, frameBytes, true))
		reader, ok := src.reader.(*m4aReader)
		require.True(t, ok, "an M4A source reads through the m4aReader")
		assert.NotNil(t, reader.seeked, "the window must be served by the seekable decoder")
	})

	t.Run("start zero stays on the forward decoder", func(t *testing.T) {
		t.Parallel()
		src := open(t)
		require.NoError(t, positionSource(t.Context(), src, 0, frameBytes, true))
		reader, ok := src.reader.(*m4aReader)
		require.True(t, ok)
		assert.Nil(t, reader.seeked, "no seek happens at frame zero")
	})
}

// auRanges returns the byte range of every access unit of an m4a file, which
// the encoder writes contiguously in mdat.
func auRanges(t *testing.T, data []byte) [][2]int {
	t.Helper()
	rd, err := m4a.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	var out [][2]int
	off := -1
	for {
		au, err := rd.ReadFrame()
		if errors.Is(err, io.EOF) {
			return out
		}
		require.NoError(t, err)
		if off < 0 {
			off = bytes.Index(data, au)
			require.GreaterOrEqual(t, off, 0)
		}
		require.True(t, bytes.Equal(data[off:off+len(au)], au), "access unit %d is not contiguous in mdat", len(out))
		out = append(out, [2]int{off, off + len(au)})
		off += len(au)
	}
}

// A seekable decoder rejects an access unit with no audio element that the
// forward decoder skips. A window must still be served, from the forward decoder.
func TestDecode_M4AFallsBackWhenAUnitHasNoAudio(t *testing.T) {
	t.Parallel()

	src := tone(testRate, 1, testSeconds, testToneHz, testAmp)
	clean := filepath.Join(t.TempDir(), "clean.m4a")
	writeM4AFile(t, clean, src, testRate, 1)
	data, err := os.ReadFile(clean) //nolint:gosec // G304: a test fixture path
	require.NoError(t, err)

	ranges := auRanges(t, data)
	require.Greater(t, len(ranges), corruptedUnitIndex+corruptLeadUnits+3)
	bad := bytes.Clone(data)
	for i := ranges[corruptedUnitIndex][0]; i < ranges[corruptedUnitIndex][1]; i++ {
		bad[i] = 0xFF // the first element id is ID_END: an access unit with no audio
	}
	path := filepath.Join(t.TempDir(), "bad.m4a")
	require.NoError(t, os.WriteFile(path, bad, 0o600))

	// Precondition: the seekable decoder does fail on this file, so the test
	// proves the fallback rather than a window that never touches the bad unit.
	dec, _, err := aacm4a.NewSeekableDecoder(bytes.NewReader(bad))
	require.NoError(t, err)
	require.NoError(t, dec.SeekSample(corruptedUnitIndex-1))
	_, err = io.ReadAll(dec)
	require.ErrorIs(t, err, aacpcm.ErrCorruptStream)

	whole, err := decode(t.Context(), path, nil, 0)
	require.NoError(t, err, "the forward decoder serves the whole file")

	delay, _ := m4aEncoderDelay(t, path)
	start := int64((corruptedUnitIndex-corruptLeadUnits)*m4aUnitFrames) - delay
	req := &window{Start: float64(start) / testRate, Duration: float64(4*m4aUnitFrames) / testRate}
	win, err := decode(t.Context(), path, req, 0)
	require.NoError(t, err, "a seek failure must not fail a request the forward path serves")
	assert.True(t, bytes.Equal(whole.Data[start*bytesPerSample:(start+4*m4aUnitFrames)*bytesPerSample], win.Data),
		"the fallback window equals the whole-file decode's slice")
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

	src := tone(testRate, 1, testSeconds, testToneHz, testAmp)
	clean := filepath.Join(t.TempDir(), "clean.m4a")
	writeM4AFile(t, clean, src, testRate, 1)
	data, err := os.ReadFile(clean) //nolint:gosec // G304: a test fixture path
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "huge-delay.m4a")
	require.NoError(t, os.WriteFile(path, patchEncoderDelay(t, data, hugeEncoderDelay), 0o600))
	delay, frameCount := m4aEncoderDelay(t, path)
	require.Equal(t, int64(hugeEncoderDelay), delay, "the patch must reach Info.EncoderDelay")

	f, err := os.Open(path) //nolint:gosec // G304: a test fixture path
	require.NoError(t, err)
	defer func() { assert.NoError(t, f.Close()) }()
	opened, err := openSource(f, path)
	require.NoError(t, err)
	assert.LessOrEqual(t, opened.skipFrames, int64(frameCount)*m4aUnitFrames, "the priming skip is bounded by the decoded length")

	_, err = decode(t.Context(), path, &window{Start: 1, Duration: 1}, 0)
	require.Error(t, err)
	assert.Truef(t, errors.Is(err, ErrEmptyRange) || errors.Is(err, ErrUndecodableSource), "got %v", err)
}

// A noisy clip is coded with perceptual noise substitution, which a seek cannot
// reconstruct, so the window matches the linear decode only within go-m4a's
// documented bounds.
func TestDecode_M4ANoisyWindowWithinPNSTolerance(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(noisyM4ASeed, noisyM4ASeed))
	frames := int(noisyM4AWindowSeconds * testRate)
	src := make([]int16, frames)
	for i := range src {
		v := noisyM4ANoiseAmp*rng.NormFloat64() + noisyM4AToneAmp*math.Sin(2*math.Pi*testToneHz*float64(i)/testRate)
		src[i] = int16(math.Max(math.Min(math.Round(v*math.MaxInt16), math.MaxInt16), math.MinInt16))
	}
	path := filepath.Join(t.TempDir(), "noisy.m4a")
	writeM4AFile(t, path, src, testRate, 1)

	whole, err := decode(t.Context(), path, nil, 0)
	require.NoError(t, err)

	const start = m4aMidFileFrame
	requireSeekServes(t, path, start)
	win, err := decode(t.Context(), path, &window{Start: float64(start) / testRate, Duration: float64(m4aWindowFrames) / testRate}, 0)
	require.NoError(t, err)
	require.Equal(t, m4aWindowFrames, win.Frames(), "the window length is exact")

	want := bytesInt16(whole.Data[start*bytesPerSample : (start+m4aWindowFrames)*bytesPerSample])
	got := bytesInt16(win.Data)
	assert.GreaterOrEqual(t, correlation(want, got), pnsMinCorrelation)
	assert.InDelta(t, 0, 20*math.Log10(rms(got)/rms(want)), pnsMaxLevelDiffDB)
}

// rms returns the root mean square of samples.
func rms(samples []int16) float64 {
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// rewindProbe is a source whose seek always fails with seekErr (or lands short of
// the target when seekErr is nil), counting calls to rewind.
func rewindProbe(seekErr error, rewinds *int) *source {
	const probeFrames = 20000
	return &source{
		reader:      bytes.NewReader(make([]byte, probeFrames*bytesPerSample)),
		sampleRate:  testRate,
		channels:    1,
		totalFrames: probeFrames,
		seek:        func(int64) (int64, error) { return 0, seekErr },
		rewind:      func() bool { *rewinds++; return true },
		expansion:   lossyExpansion,
	}
}

// Only a seek failure the forward path could still serve is retried; a cancelled
// context and an empty range are reported as they are.
func TestDecodeSource_SeekFallbackIsLimitedToRetryableFailures(t *testing.T) {
	t.Parallel()

	const startSeconds, durSeconds = 0.1, 0.1
	req := &window{Start: startSeconds, Duration: durSeconds}
	maxBytes := int(2 * testRate * bytesPerSample)

	tests := []struct {
		name        string
		seekErr     error
		wantRewinds int
		wantErr     error
	}{
		{name: "a decode failure is retried once", seekErr: aacpcm.ErrCorruptStream, wantRewinds: 1},
		{name: "a cancelled context is not retried", seekErr: context.Canceled, wantErr: context.Canceled},
		{name: "an expired context is not retried", seekErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
		{name: "a start past the end is not retried", seekErr: nil, wantErr: ErrEmptyRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rewinds := 0
			pcm, err := decodeSource(t.Context(), rewindProbe(tt.seekErr, &rewinds), req, 0, maxBytes)
			assert.Equal(t, tt.wantRewinds, rewinds)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, int(durSeconds*testRate), pcm.Frames())
		})
	}
}

// The forward decoder may only be reused while it is still at the start of the file.
func TestM4AReader_RewindRequiresAnUnreadForwardDecoder(t *testing.T) {
	t.Parallel()

	assert.False(t, (&m4aReader{}).rewind(), "without a seek there is nothing to abandon")
	assert.False(t, (&m4aReader{seekTried: true, fwdUsed: true}).rewind(), "a read forward decoder is no longer at the start")

	r := &m4aReader{seekTried: true, seeked: &aacm4a.Decoder{}}
	assert.True(t, r.rewind())
	assert.Nil(t, r.seeked, "a rewind drops the seekable decoder so reads return to the forward decoder")
	assert.False(t, r.seekTried, "a rewind clears the seek attempt")
	assert.False(t, r.rewind(), "a second rewind has nothing to abandon")
}
