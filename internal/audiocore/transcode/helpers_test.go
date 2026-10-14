package transcode

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	aacpcm "github.com/tphakala/go-aac/pcm"
	flacpcm "github.com/tphakala/go-flac/pcm"
	wavpcm "github.com/tphakala/go-wav/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/aac"
	"github.com/tphakala/birdnet-go/internal/audiocore/mp3"
	"github.com/tphakala/birdnet-go/internal/audiocore/opus"
)

const (
	testRate     = 48000
	testToneHz   = 1000.0
	testOffToneH = 3000.0
	testAmp      = 0.5
	testSeconds  = 2.0
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

// writeWAVFile writes 16-bit PCM as a WAV file.
func writeWAVFile(t *testing.T, path string, samples []int16, rate, channels int) {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wavpcm.EncodeInterleaved(&buf, wavpcm.Config{SampleRate: rate, BitDepth: 16, Channels: channels}, int16Bytes(samples)))
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
}

// writeFLACFile writes samples as FLAC at the given bit depth. A depth below 16
// shifts the samples down and a depth above 16 shifts them up, so the signal
// keeps its level; bytes are packed at ceil(depth/8) per sample.
func writeFLACFile(t *testing.T, path string, samples []int16, rate, channels, depth int) {
	t.Helper()
	width := (depth + 7) / 8
	raw := make([]byte, 0, len(samples)*width)
	for _, s := range samples {
		v := int32(s)
		if depth >= 16 {
			v <<= depth - 16
		} else {
			v >>= 16 - depth
		}
		for b := range width {
			raw = append(raw, byte(v>>(8*b)))
		}
	}
	var buf bytes.Buffer
	require.NoError(t, flacpcm.EncodeInterleaved(&buf, flacpcm.Config{SampleRate: rate, BitDepth: depth, Channels: channels}, raw))
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
}

// writeADTSFile writes samples as a raw ADTS AAC stream.
func writeADTSFile(t *testing.T, path string, samples []int16, rate, channels int) {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, aacpcm.EncodeInterleaved(&buf, aacpcm.Config{SampleRate: rate, BitDepth: 16, Channels: channels}, int16Bytes(samples)))
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
}

// writeM4AFile writes samples as AAC-LC in MP4 through the production encoder.
func writeM4AFile(t *testing.T, path string, samples []int16, rate, channels int) {
	t.Helper()
	require.NoError(t, aac.EncodePCM(t.Context(), &aac.Options{
		PCMData: int16Bytes(samples), OutputPath: path, SampleRate: rate, Channels: channels, BitDepth: 16, BitrateKbps: 128,
	}))
}

// writeMP3File writes samples as MP3 through the production encoder.
func writeMP3File(t *testing.T, path string, samples []int16, rate, channels int) {
	t.Helper()
	require.NoError(t, mp3.EncodePCM(t.Context(), &mp3.Options{
		PCMData: int16Bytes(samples), OutputPath: path, SampleRate: rate, Channels: channels, BitDepth: 16, BitrateKbps: 128,
	}))
}

// writeOpusFile writes samples as Ogg Opus through the production encoder.
func writeOpusFile(t *testing.T, path string, samples []int16, rate, channels int) {
	t.Helper()
	require.NoError(t, opus.EncodePCM(t.Context(), &opus.Options{
		PCMData: int16Bytes(samples), OutputPath: path, SampleRate: rate, Channels: channels, BitDepth: 16, BitrateKbps: 96,
	}))
}

// fixture describes one writable source format.
type fixture struct {
	name  string
	ext   string
	write func(t *testing.T, path string, samples []int16, rate, channels int)
	// lossless is true when decoding returns the source samples bit-exactly, apart
	// from the low bits dropped by quantShift.
	lossless bool
	// quantShift is the number of low bits a narrower sample width discards.
	quantShift uint
}

// fixtures lists every source format the decoder supports, with stereo support
// noted by the caller.
func fixtures() []fixture {
	flacAt := func(depth int) func(*testing.T, string, []int16, int, int) {
		return func(t *testing.T, path string, s []int16, rate, ch int) {
			t.Helper()
			writeFLACFile(t, path, s, rate, ch, depth)
		}
	}
	return []fixture{
		{name: "wav", ext: ".wav", write: writeWAVFile, lossless: true},
		{name: "flac16", ext: ".flac", write: flacAt(16), lossless: true},
		{name: "flac24", ext: ".flac", write: flacAt(24), lossless: true},
		{name: "flac32", ext: ".flac", write: flacAt(32), lossless: true},
		{name: "flac8", ext: ".flac", write: flacAt(8), lossless: true, quantShift: 8},
		{name: "flac12", ext: ".flac", write: flacAt(12), lossless: true, quantShift: 4},
		{name: "flac20", ext: ".flac", write: flacAt(20), lossless: true},
		{name: "mp3", ext: ".mp3", write: writeMP3File},
		{name: "m4a", ext: ".m4a", write: writeM4AFile},
		{name: "adts", ext: ".aac", write: writeADTSFile},
		{name: "opus", ext: ".opus", write: writeOpusFile},
	}
}

// writeFixture writes a stereo-or-mono tone in the fixture's format and returns
// its path.
func writeFixture(t *testing.T, fx fixture, rate, channels int, seconds float64) (path string, samples []int16) {
	t.Helper()
	samples = tone(rate, channels, seconds, testToneHz, testAmp)
	path = filepath.Join(t.TempDir(), "src"+fx.ext)
	fx.write(t, path, samples, rate, channels)
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
