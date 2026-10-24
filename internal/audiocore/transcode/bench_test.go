package transcode

import (
	"math"
	"math/rand/v2"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
)

const (
	benchNoiseAmp     = 0.05
	benchToneAmp      = 0.3
	benchClipSeconds  = 300.0
	benchLeadSeconds  = 10.0
	benchBatSeconds   = 60.0
	benchBatRate      = 256000
	benchBatExpansion = 20
)

// benchNoisyTone returns mono 16-bit PCM of a tone in white noise.
func benchNoisyTone(rate int, seconds, freq float64) []byte {
	rng := rand.New(rand.NewPCG(1, 2))
	frames := int(seconds * float64(rate))
	out := make([]byte, frames*bytesPerSample)
	for i := range frames {
		v := benchNoiseAmp*rng.NormFloat64() + benchToneAmp*math.Sin(2*math.Pi*freq*float64(i)/float64(rate))
		s := int16(math.Max(math.Min(math.Round(v*math.MaxInt16), math.MaxInt16), math.MinInt16))
		out[bytesPerSample*i] = byte(s)
		out[bytesPerSample*i+1] = byte(uint16(s) >> 8) //nolint:gosec // G115: PCM bit reinterpretation in a benchmark fixture
	}
	return out
}

// BenchmarkExtractClip_MaxWindowHeavyDenoiseNormalizeOpus times the largest clip
// request: a 300 s window of a 48 kHz WAV with heavy denoise and normalization,
// encoded to Opus. The request timeout for it is 10 minutes.
func BenchmarkExtractClip_MaxWindowHeavyDenoiseNormalizeOpus(b *testing.B) {
	path := filepath.Join(b.TempDir(), "long.wav")
	pcm := &pcmBuffer{Data: benchNoisyTone(testRate, benchLeadSeconds+benchClipSeconds, testToneHz), SampleRate: testRate, Channels: 1}
	wav, err := encodeWAV(pcm)
	require.NoError(b, err)
	writeBytes(b, path, wav)
	tempDir := b.TempDir()

	for b.Loop() {
		_, err := ExtractClip(b.Context(), &ClipOptions{
			InputPath: path, Start: benchLeadSeconds, End: benchLeadSeconds + benchClipSeconds,
			Format: audioformat.Opus, Filters: Filters{Denoise: "heavy", Normalize: true}, TempDir: tempDir,
		})
		require.NoError(b, err)
	}
}

// BenchmarkAudibleBats_Minute256kHzFactor20Normalize times a 60 s ultrasonic
// recording at 256 kHz time-expanded 20x with normalization. The request timeout
// for it is 120 s.
func BenchmarkAudibleBats_Minute256kHzFactor20Normalize(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bat.wav")
	pcm := &pcmBuffer{Data: benchNoisyTone(benchBatRate, benchBatSeconds, batToneHz), SampleRate: benchBatRate, Channels: 1}
	wav, err := encodeWAV(pcm)
	require.NoError(b, err)
	writeBytes(b, path, wav)

	for b.Loop() {
		_, err := AudibleBats(b.Context(), path, benchBatExpansion, Filters{Normalize: true})
		require.NoError(b, err)
	}
}
