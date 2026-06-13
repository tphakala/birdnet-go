package flac

import (
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goflac "github.com/tphakala/go-flac/pcm"
)

const (
	testSampleRate = 48000
	testChannels   = 1
	testBitDepth   = 16
)

// makeTestPCM builds deterministic interleaved int16 LE mono PCM with n samples,
// mixing two tones so the encoder exercises real predictors rather than silence.
func makeTestPCM(n int) []byte {
	samples := make([]int16, n)
	for i := range samples {
		v := 8000.0*math.Sin(2*math.Pi*440*float64(i)/testSampleRate) +
			3000.0*math.Sin(2*math.Pi*1200*float64(i)/testSampleRate)
		samples[i] = int16(v)
	}
	return pcm16(samples...)
}

// decodeFLAC reads a FLAC file back to interleaved little-endian PCM bytes.
func decodeFLAC(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	dec, err := goflac.NewDecoder(f)
	require.NoError(t, err)
	out, err := io.ReadAll(dec)
	require.NoError(t, err)
	return out
}

func baseOpts(path string, pcm []byte) *Options {
	return &Options{
		PCMData:    pcm,
		OutputPath: path,
		SampleRate: testSampleRate,
		Channels:   testChannels,
		BitDepth:   testBitDepth,
	}
}

func TestEncodePCM_RoundTripLossless(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(10000)
	path := filepath.Join(t.TempDir(), "clip.flac")
	require.NoError(t, EncodePCM(context.Background(), baseOpts(path, pcm)))
	assert.Equal(t, pcm, decodeFLAC(t, path), "decoded PCM must equal input (lossless)")
}

func TestEncodePCM_GainRoundTrip(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(10000)
	opts := baseOpts(filepath.Join(t.TempDir(), "clip.flac"), pcm)
	opts.GainDB = 6.0 // roughly 2x
	require.NoError(t, EncodePCM(context.Background(), opts))

	want := make([]byte, len(pcm))
	applyGainInt16(want, pcm, math.Pow(10, opts.GainDB/20))
	assert.Equal(t, want, decodeFLAC(t, opts.OutputPath),
		"decoded PCM must equal the gained input (gain applied, stored losslessly)")
}

func TestEncodePCM_EmptyPCMReturnsError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "clip.flac")
	err := EncodePCM(context.Background(), baseOpts(path, nil))
	require.Error(t, err)
	assert.NoFileExists(t, path)
}

func TestEncodePCM_UnsupportedBitDepthReturnsError(t *testing.T) {
	t.Parallel()
	opts := baseOpts(filepath.Join(t.TempDir(), "clip.flac"), makeTestPCM(100))
	opts.BitDepth = 24
	err := EncodePCM(context.Background(), opts)
	require.Error(t, err)
	assert.NoFileExists(t, opts.OutputPath)
}

func TestEncodePCM_CancelledContext(t *testing.T) {
	t.Parallel()
	opts := baseOpts(filepath.Join(t.TempDir(), "clip.flac"), makeTestPCM(10000))
	opts.GainDB = 3.0 // exercise the gain loop, which checks ctx
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := EncodePCM(ctx, opts)
	require.ErrorIs(t, err, context.Canceled)
	assert.NoFileExists(t, opts.OutputPath)
	assert.NoFileExists(t, opts.OutputPath+".temp")
}

func TestEncodePCM_NoTempFileLeftOnSuccess(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "clip.flac")
	require.NoError(t, EncodePCM(context.Background(), baseOpts(path, makeTestPCM(5000))))
	assert.FileExists(t, path)
	assert.NoFileExists(t, path+".temp")
}

func TestEncodePCM_EmitsSeekTable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "clip.flac")
	require.NoError(t, EncodePCM(context.Background(), baseOpts(path, makeTestPCM(60000))))
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.True(t, hasSeekTable(data), "native FLAC should emit a SEEKTABLE for player parity with FFmpeg")
}

// hasSeekTable walks the FLAC metadata blocks and reports whether a SEEKTABLE
// (block type 3) is present.
func hasSeekTable(data []byte) bool {
	const seekTableBlockType = 3
	if len(data) < 4 || string(data[:4]) != "fLaC" {
		return false
	}
	for off := 4; off+4 <= len(data); {
		header := data[off]
		last := header&0x80 != 0
		btype := header & 0x7f
		length := int(data[off+1])<<16 | int(data[off+2])<<8 | int(data[off+3])
		if btype == seekTableBlockType {
			return true
		}
		off += 4 + length
		if last {
			break
		}
	}
	return false
}

func TestSupportedBitDepth(t *testing.T) {
	t.Parallel()
	assert.True(t, SupportedBitDepth(16))
	assert.False(t, SupportedBitDepth(24))
	assert.False(t, SupportedBitDepth(0))
}
