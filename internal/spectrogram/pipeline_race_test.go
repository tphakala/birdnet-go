package spectrogram

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// TestFFmpegSoxPipelineFailurePathNoRace exercises the FFmpeg-failure branch of
// generateWithFFmpegSoxPipeline. The branch reads the soxOutput buffer to build
// error context. Because soxCmd.Stderr is a *bytes.Buffer, os/exec runs a
// background goroutine that copies Sox stderr into that buffer until
// soxCmd.Wait() joins it. Reading soxOutput before Wait() returns is a data
// race on the bytes.Buffer.
//
// The test drives FFmpeg to fail (nonexistent input) while Sox is started and
// emitting stderr, then runs the path many times. Run with -race to detect the
// race: with the bug present the race detector flags the concurrent read/write
// on the buffer; with the fix in place it stays clean.
func TestFFmpegSoxPipelineFailurePathNoRace(t *testing.T) {
	soxPath := requireSoxAvailable(t)
	ffmpegPath := requireFFmpegAvailable(t)

	env := setupTestEnv(t)
	env.Settings.Realtime.Audio.SoxPath = soxPath
	env.Settings.Realtime.Audio.FfmpegPath = ffmpegPath

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	// A nonexistent input makes FFmpeg exit non-zero, taking the FFmpeg-failure
	// branch. Sox is started reading from FFmpeg's stdout pipe and writes a
	// failure message to stderr when the pipe closes, so the os/exec copier
	// goroutine is actively writing the soxOutput buffer around the time the
	// failure branch reads it.
	missingAudio := filepath.Join(env.TempDir, "does-not-exist.wav")
	outputPath := filepath.Join(env.TempDir, "out.png")

	const (
		iterations          = 200
		width               = 800
		raw                 = false
		preValidatedSeconds = 1.0
	)
	for range iterations {
		// An error is expected every time (FFmpeg fails). The point of the test
		// is that the -race detector must not flag a data race on soxOutput.
		err := gen.generateWithFFmpegSoxPipeline(
			env.Ctx,
			env.Settings,
			missingAudio,
			outputPath,
			width,
			raw,
			preValidatedSeconds,
			BirdProfile(),
		)
		require.Error(t, err, "FFmpeg should fail for a nonexistent input file")
	}
}
