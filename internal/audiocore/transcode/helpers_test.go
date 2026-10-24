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

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/wav"
)

const (
	testRate     = 48000
	testToneHz   = 1000.0
	testOffToneH = 3000.0
	testAmp      = 0.5
	testSeconds  = 2.0

	// lossyBitrateKbps is the bit rate of the lossy fixtures, in kbit/s.
	lossyBitrateKbps = 128
	// opusBitrateKbps is the bit rate of the Opus fixtures, in kbit/s.
	opusBitrateKbps = 96

	// Ogg page layout: a fixed 27-byte header whose last byte counts the lacing
	// values that follow, and whose bytes 6 to 13 hold the granule position.
	oggHeaderBytes        = 27
	oggSegmentCountOffset = 26
	oggGranuleOffset      = 6
	// opusHeaderPages is the number of pages that hold OpusHead and OpusTags
	// rather than audio.
	opusHeaderPages = 2
)

// tone returns interleaved 16-bit samples of a sine of the given amplitude
// (fraction of full scale); every channel carries the same signal.
func tone(rate, channels int, seconds, freq, amp float64) []int16 {
	frames := int(seconds * float64(rate))
	out := make([]int16, frames*channels)
	for i := range frames {
		v := int16(math.Round(amp * math.MaxInt16 * math.Sin(2*math.Pi*freq*float64(i)/float64(rate))))
		for c := range channels {
			out[i*channels+c] = v
		}
	}
	return out
}

// int16Bytes serializes samples as little-endian bytes.
func int16Bytes(samples []int16) []byte {
	b := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s)) //nolint:gosec // G115: PCM bit reinterpretation in a test fixture
	}
	return b
}

// bytesInt16 parses little-endian bytes into samples.
func bytesInt16(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2:])) //nolint:gosec // G115: PCM bit reinterpretation in a test fixture
	}
	return out
}

// channelOf extracts one channel of interleaved samples.
func channelOf(samples []int16, channels, ch int) []float64 {
	out := make([]float64, len(samples)/channels)
	for i := range out {
		out[i] = float64(samples[i*channels+ch])
	}
	return out
}

// rmsFloat64 returns the root mean square of x.
func rmsFloat64(x []float64) float64 {
	var sumSquares float64
	for _, v := range x {
		sumSquares += v * v
	}
	return math.Sqrt(sumSquares / float64(len(x)))
}

// goertzelPower returns the signal power at freq.
func goertzelPower(x []float64, rate int, freq float64) float64 {
	w := 2 * math.Pi * freq / float64(rate)
	coeff := 2 * math.Cos(w)
	var s1, s2 float64
	for _, v := range x {
		s := v + coeff*s1 - s2
		s2, s1 = s1, s
	}
	return (s1*s1 + s2*s2 - coeff*s1*s2) / float64(len(x)*len(x))
}

// dominantIs reports whether freq carries at least 20 dB more power than the
// off-frequency probe.
func dominantIs(x []float64, rate int, freq, offFreq float64) bool {
	return goertzelPower(x, rate, freq) > 100*goertzelPower(x, rate, offFreq)
}

// peakAbs returns the largest absolute sample value.
func peakAbs(samples []int16) int {
	peak := 0
	for _, s := range samples {
		peak = max(peak, int(math.Abs(float64(s))))
	}
	return peak
}

// writeWAVFile writes mono 16-bit PCM as a WAV file through the production writer.
func writeWAVFile(t *testing.T, path string, samples []int16, rate int) {
	t.Helper()
	require.NoError(t, wav.EncodePCM(t.Context(), &wav.Options{PCMData: int16Bytes(samples), OutputPath: path, SampleRate: rate, BitDepth: outputBitDepth}))
}

// writeEncoded writes mono 16-bit samples to path through the native encoder
// clip export uses for format. bitrateKbps is ignored by lossless formats.
func writeEncoded(t *testing.T, format string, bitrateKbps int, path string, samples []int16, rate int) {
	t.Helper()
	encode, ok := fileEncoders[format]
	require.Truef(t, ok, "no file encoder for %s", format)
	require.NoError(t, encode(t.Context(), &fileEncodeOptions{
		data: int16Bytes(samples), outputPath: path, sampleRate: rate, channels: 1, bitrateKbps: bitrateKbps,
	}))
}

// encodedWriter returns a fixture writer for a format written by writeEncoded.
func encodedWriter(format string, bitrateKbps int) func(*testing.T, string, []int16, int) {
	return func(t *testing.T, path string, samples []int16, rate int) {
		t.Helper()
		writeEncoded(t, format, bitrateKbps, path, samples, rate)
	}
}

// fixture describes one writable source format.
type fixture struct {
	name  string
	ext   string
	write func(t *testing.T, path string, samples []int16, rate int)
	// lossless is true when decoding returns the source samples bit-exactly.
	lossless bool
}

// fixtures lists every source format the decoder reads, one per clip format.
func fixtures() []fixture {
	return []fixture{
		{name: "wav", ext: ".wav", write: writeWAVFile, lossless: true},
		{name: "flac", ext: ".flac", write: encodedWriter(audioformat.FLAC, 0), lossless: true},
		{name: "mp3", ext: ".mp3", write: encodedWriter(audioformat.MP3, lossyBitrateKbps)},
		{name: "m4a", ext: ".m4a", write: encodedWriter(audioformat.AAC, lossyBitrateKbps)},
		{name: "opus", ext: ".opus", write: encodedWriter(audioformat.Opus, opusBitrateKbps)},
	}
}

// fixtureNamed returns the fixture called name.
func fixtureNamed(t *testing.T, name string) fixture {
	t.Helper()
	for _, fx := range fixtures() {
		if fx.name == name {
			return fx
		}
	}
	require.FailNowf(t, "unknown fixture", "%s", name)
	return fixture{}
}

// writeFixture writes a mono tone in the fixture's format and returns its path.
func writeFixture(t *testing.T, fx fixture, rate int, seconds float64) (path string, samples []int16) {
	t.Helper()
	samples = tone(rate, 1, seconds, testToneHz, testAmp)
	path = filepath.Join(t.TempDir(), "src"+fx.ext)
	fx.write(t, path, samples, rate)
	return path, samples
}

// burst returns a silent signal holding a tone burst in its middle half, so a
// decoder's alignment can be read from where the first sample above the noise
// floor appears.
func burst(rate, channels int, seconds float64) []int16 {
	frames := int(seconds * float64(rate))
	body := tone(rate, channels, seconds/2, testToneHz, testAmp)
	out := make([]int16, frames*channels)
	copy(out[frames/4*channels:], body)
	return out
}

// firstAbove returns the frame index of the first sample whose magnitude exceeds
// threshold, or -1.
func firstAbove(samples []int16, channels, threshold int) int {
	for i := 0; i < len(samples); i += channels {
		if int(math.Abs(float64(samples[i]))) > threshold {
			return i / channels
		}
	}
	return -1
}

// writeBytes writes data to path.
func writeBytes(tb testing.TB, path string, data []byte) {
	tb.Helper()
	require.NoError(tb, os.WriteFile(path, data, 0o600))
}

// oggPages returns the [start, end) byte range of every complete Ogg page in data.
func oggPages(data []byte) [][2]int {
	var out [][2]int
	for off := 0; off+oggHeaderBytes <= len(data); {
		segments := int(data[off+oggSegmentCountOffset])
		bodyStart := off + oggHeaderBytes + segments
		if bodyStart > len(data) {
			break
		}
		end := bodyStart
		for _, lace := range data[off+oggHeaderBytes : bodyStart] {
			end += int(lace)
		}
		if end > len(data) {
			break
		}
		out = append(out, [2]int{off, end})
		off = end
	}
	return out
}

// opusPageEnds returns, for each audio page of an Ogg Opus file, the number of
// decoded frames (pre-skip dropped) that exist once the page is read. The
// pre-skip is the last granule minus totalFrames, the end-trim of the final page.
func opusPageEnds(t *testing.T, data []byte, totalFrames int64) []int64 {
	t.Helper()
	pages := oggPages(data)
	require.Greater(t, len(pages), opusHeaderPages)
	granules := make([]int64, 0, len(pages)-opusHeaderPages)
	for _, pg := range pages[opusHeaderPages:] {
		granules = append(granules, int64(binary.LittleEndian.Uint64(data[pg[0]+oggGranuleOffset:]))) //nolint:gosec // G115: a granule position is a signed 64-bit value
	}
	preSkip := granules[len(granules)-1] - totalFrames
	for i := range granules {
		granules[i] -= preSkip
	}
	return granules
}

// tailOverrunFrames is how far before the end the overrunning window starts.
const tailOverrunFrames = 100

// readFile returns the bytes of the file at path.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: a test fixture path
	require.NoError(t, err)
	return data
}

// openTestSource opens path and its decoder, closing the file when the test ends.
func openTestSource(t *testing.T, path string) *source {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // G304: a test fixture path
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, f.Close()) })
	src, err := openSource(f, path)
	require.NoError(t, err)
	return src
}

// decodedFrames returns the frame count of the whole-file decode of path.
func decodedFrames(t *testing.T, path string) int {
	t.Helper()
	whole, err := decode(t.Context(), path, nil, 0)
	require.NoError(t, err)
	return whole.Frames()
}

// assertWindowsMatchLinear requires that a seekWindowFrames window at each start
// frame equals the matching slice of the whole-file decode of the mono clip at
// path byte for byte, and is identical when requested again. A window that
// reaches past the end must return exactly the tail.
func assertWindowsMatchLinear(t *testing.T, path string, starts []int64) {
	t.Helper()
	whole, err := decode(t.Context(), path, nil, 0)
	require.NoError(t, err)
	total := int64(whole.Frames())
	rate := float64(whole.SampleRate)

	for _, start := range starts {
		req := &window{Start: float64(start) / rate, Duration: float64(seekWindowFrames) / rate}
		frames := min(seekWindowFrames, total-start)
		want := whole.Data[start*bytesPerSample : (start+frames)*bytesPerSample]

		win, err := decode(t.Context(), path, req, 0)
		require.NoErrorf(t, err, "start %d", start)
		assert.Truef(t, bytes.Equal(want, win.Data), "start %d: the window must equal the matching slice of the whole decode (got %d frames, want %d)", start, win.Frames(), frames)

		again, err := decode(t.Context(), path, req, 0)
		require.NoError(t, err)
		assert.Truef(t, bytes.Equal(win.Data, again.Data), "start %d: a repeated request must return identical bytes", start)
	}

	start := total - tailOverrunFrames
	win, err := decode(t.Context(), path, &window{Start: float64(start) / rate, Duration: 1}, 0)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(whole.Data[start*bytesPerSample:], win.Data), "a window past the end returns exactly the tail")
}
