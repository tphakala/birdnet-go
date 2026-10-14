package media

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	wavpcm "github.com/tphakala/go-wav/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
)

const (
	fixtureToneHz = 1000.0
	fixtureAmp    = 0.3
	// fixtureBatToneHz is an ultrasonic tone for bat fixtures.
	fixtureBatToneHz = 40000.0
)

// toneBytes returns mono 16-bit little-endian PCM of a sine.
func toneBytes(rate int, seconds, freq, amp float64) []byte {
	frames := int(seconds * float64(rate))
	out := make([]byte, frames*2)
	for i := range frames {
		v := int16(math.Round(amp * math.MaxInt16 * math.Sin(2*math.Pi*freq*float64(i)/float64(rate))))
		out[2*i] = byte(v)
		out[2*i+1] = byte(uint16(v) >> 8) //nolint:gosec // G115: PCM bit reinterpretation in a test fixture
	}
	return out
}

// writeToneWAV writes a mono 16-bit WAV tone at path.
func writeToneWAV(t *testing.T, path string, rate int, seconds, freq float64) {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wavpcm.EncodeInterleaved(&buf, wavpcm.Config{SampleRate: rate, BitDepth: 16, Channels: 1}, toneBytes(rate, seconds, freq, fixtureAmp)))
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
}

// writeToneFLAC writes a mono 16-bit FLAC tone at path.
func writeToneFLAC(t *testing.T, path string, rate int, seconds, freq float64) {
	t.Helper()
	require.NoError(t, flac.EncodePCM(t.Context(), &flac.Options{
		PCMData: toneBytes(rate, seconds, freq, fixtureAmp), OutputPath: path, SampleRate: rate, Channels: 1, BitDepth: 16,
	}))
}

// writeLegacyAAC writes a file whose .aac extension is not one of the clip formats,
// so the transcoder rejects it as an unsupported source.
func writeLegacyAAC(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte{0xFF, 0xF1}, 100), 0o600))
}

// writeCorrupt writes bytes that carry a FLAC marker but no valid stream.
func writeCorrupt(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, append([]byte("fLaC"), bytes.Repeat([]byte{0xAB}, 200)...), 0o600))
}

// fixturePath returns the path of name under dir.
func fixturePath(dir, name string) string { return filepath.Join(dir, name) }
