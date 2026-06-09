package batch

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// genWAV writes a mono 48 kHz sine WAV of the given duration using ffmpeg.
func genWAV(t *testing.T, dir, name string, seconds float64) string {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(dir, name)
	cmd := exec.Command(ffmpeg, "-f", "lavfi", "-i",
		"sine=frequency=1000:duration="+strconv.FormatFloat(seconds, 'f', -1, 64),
		"-ar", "48000", "-ac", "1", "-y", out)
	require.NoError(t, cmd.Run(), "ffmpeg fixture generation failed")
	return out
}

func TestPCM16ToFloat32(t *testing.T) {
	t.Parallel()
	// -32768 must map exactly to -1.0 so batch scaling matches the live
	// analysis path (divisor 32768, not 32767).
	assert.InDelta(t, -1.0, float64(pcm16ToFloat32(-32768)), 0)
	assert.InDelta(t, float64(32767)/32768, float64(pcm16ToFloat32(32767)), 0)
	assert.InDelta(t, 0.0, float64(pcm16ToFloat32(0)), 0)
}

func TestDecodeWindows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := genWAV(t, dir, "tone.wav", 7.5) // 2 full 3s windows + 1.5s tail (padded, >=25%)

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}

	const rate = 48000
	windowSamples := rate * 3
	var offsets []time.Duration
	var lengths []int
	err = decodeWindows(t.Context(), ffmpeg, path, rate, windowSamples,
		func(window []float32, offset time.Duration) error {
			offsets = append(offsets, offset)
			lengths = append(lengths, len(window))
			return nil
		})
	require.NoError(t, err)
	require.Len(t, offsets, 3)
	assert.Equal(t, []time.Duration{0, 3 * time.Second, 6 * time.Second}, offsets)
	for _, n := range lengths {
		assert.Equal(t, windowSamples, n, "every window must be exactly windowSamples long")
	}
}

func TestDecodeWindowsShortTailDropped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := genWAV(t, dir, "short.wav", 3.5) // 1 full window + 0.5s tail (<25%, dropped)

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	const rate = 48000
	count := 0
	err = decodeWindows(t.Context(), ffmpeg, path, rate, rate*3,
		func(window []float32, offset time.Duration) error { count++; return nil })
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestDecodeWindowsMissingFile(t *testing.T) {
	t.Parallel()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	err = decodeWindows(t.Context(), ffmpeg, "/nonexistent/clip.wav", 48000, 48000*3,
		func([]float32, time.Duration) error { return nil })
	require.Error(t, err)
}

func TestDecodeWindowsCallbackErrorAborts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := genWAV(t, dir, "abort.wav", 9.0)
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	sentinel := errors.NewStd("stop now")
	count := 0
	err = decodeWindows(t.Context(), ffmpeg, path, 48000, 48000*3,
		func([]float32, time.Duration) error { count++; return sentinel })
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, 1, count, "decode must stop after the callback errors")
}
