package readfile_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/audiocore/readfile"
)

// makePCM16 generates a slice of raw 16-bit little-endian PCM bytes containing a
// simple sine-like ramp pattern.  numSamples specifies the number of mono samples.
func makePCM16(t *testing.T, numSamples int) []byte {
	t.Helper()

	buf := make([]byte, numSamples*2)
	for i := range numSamples {
		// Simple linear ramp from 0 to 32767.
		val := int16((i * 32767) / numSamples) //nolint:gosec // G115: value is always within int16 range
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(val))
	}
	return buf
}

// createTestWAV writes a small WAV file using convert.SavePCMDataToWAV and
// returns its path.  The file is placed in t.TempDir() and cleaned up
// automatically.
func createTestWAV(t *testing.T, sampleRate, numSamples int) string {
	t.Helper()

	pcmData := makePCM16(t, numSamples)
	path := filepath.Join(t.TempDir(), "test.wav")
	require.NoError(t, convert.SavePCMDataToWAV(path, pcmData, sampleRate, 16))
	return path
}

// TestGetAudioInfo_WAV verifies that GetAudioInfo reads correct metadata from
// a WAV file created with SavePCMDataToWAV.
func TestGetAudioInfo_WAV(t *testing.T) {
	t.Parallel()

	const (
		sampleRate = 48000
		numSamples = sampleRate * 3 // 3 seconds
	)

	wavPath := createTestWAV(t, sampleRate, numSamples)

	info, err := readfile.GetAudioInfo(wavPath)
	require.NoError(t, err)

	assert.Equal(t, sampleRate, info.SampleRate, "sample rate mismatch")
	assert.Equal(t, 1, info.NumChannels, "expected mono")
	assert.Equal(t, 16, info.BitDepth, "expected 16-bit")
	// TotalSamples is derived from file size; allow a small tolerance for the WAV header.
	assert.Positive(t, info.TotalSamples, "total samples should be positive")
}

// TestGetAudioInfo_FLAC verifies that GetAudioInfo reads correct metadata from
// a FLAC file if one is available in the testdata directory.  The test is
// skipped when no FLAC test fixtures are present.
func TestGetAudioInfo_FLAC(t *testing.T) {
	t.Parallel()

	testdataDir := "testdata"
	entries, err := os.ReadDir(testdataDir)
	if err != nil || len(entries) == 0 {
		t.Skip("no testdata directory or no files found – skipping FLAC test")
	}

	var flacPath string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == readfile.ExtFLAC {
			flacPath = filepath.Join(testdataDir, e.Name())
			break
		}
	}

	if flacPath == "" {
		t.Skip("no .flac file found in testdata – skipping FLAC test")
	}

	info, err := readfile.GetAudioInfo(flacPath)
	require.NoError(t, err)

	assert.Positive(t, info.SampleRate, "sample rate must be positive")
	assert.Positive(t, info.TotalSamples, "total samples must be positive")
	assert.Positive(t, info.NumChannels, "num channels must be positive")
	assert.Positive(t, info.BitDepth, "bit depth must be positive")
}

// TestGetAudioInfo_UnknownFormat verifies that GetAudioInfo returns an error
// for a file with an unrecognised extension.
func TestGetAudioInfo_UnknownFormat(t *testing.T) {
	t.Parallel()

	// Write a small dummy file with an unknown extension.
	path := filepath.Join(t.TempDir(), "audio.xyz")
	require.NoError(t, os.WriteFile(path, []byte("not audio data"), 0o600))

	_, err := readfile.GetAudioInfo(path)
	assert.Error(t, err, "expected error for unknown format")
}

// TestGetAudioInfo_EmptyPath verifies that an empty path returns an error.
func TestGetAudioInfo_EmptyPath(t *testing.T) {
	t.Parallel()

	_, err := readfile.GetAudioInfo("")
	assert.Error(t, err)
}

// TestGetAudioInfo_NoExtension verifies that a file without an extension returns
// an error.
func TestGetAudioInfo_NoExtension(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "audiofile")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0o600))

	_, err := readfile.GetAudioInfo(path)
	assert.Error(t, err)
}

// TestGetTotalChunks verifies the chunk count calculation for common inputs.
func TestGetTotalChunks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		sampleRate   int
		totalSamples int
		overlap      float64
		wantPositive bool
	}{
		{
			name:         "standard 3s no overlap",
			sampleRate:   48000,
			totalSamples: 48000 * 9, // 9 seconds
			overlap:      0,
			wantPositive: true,
		},
		{
			name:         "overlap 1.5s",
			sampleRate:   48000,
			totalSamples: 48000 * 9,
			overlap:      1.5,
			wantPositive: true,
		},
		{
			name:         "zero step returns 0",
			sampleRate:   48000,
			totalSamples: 48000 * 9,
			overlap:      3, // step = 0
			wantPositive: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			chunks := readfile.GetTotalChunks(tt.sampleRate, tt.totalSamples, tt.overlap)
			if tt.wantPositive {
				assert.Positive(t, chunks, "expected positive chunk count")
			} else {
				assert.Equal(t, 0, chunks, "expected zero chunk count")
			}
		})
	}
}
