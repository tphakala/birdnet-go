// internal/api/v2/clip_decode.go
//
// FFmpeg-based decoder for saved clip files (wav/opus/flac/mp3/...) into
// mono float32 PCM at a model's target sample rate. Used by the
// /detections/:id/reanalyze endpoint to feed a saved clip back through the
// classifier pipeline against an alternate model.
package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// maxReanalyzeBytes caps the PCM byte buffer returned by ffmpeg to keep
	// memory bounded under hostile input. At 48 kHz mono 16-bit this is
	// ~62 seconds of audio — comfortably longer than the longest detection
	// clip a default-configured BirdNET-Go install will ever save.
	maxReanalyzeBytes = 6 * 1024 * 1024
)

// decodeClipMonoPCM16 invokes ffmpeg to decode the clip at clipPath into raw
// signed 16-bit little-endian PCM samples, downmixed to mono and resampled to
// targetSampleRate. The first return value is the float32-normalized sample
// stream ready to feed Orchestrator.PredictModel.
//
// maxDurationSec caps the input duration handed to ffmpeg (-t flag), which
// hard-bounds both decode time and output size for hostile or oversized
// inputs. The function additionally enforces maxReanalyzeBytes on the
// captured PCM buffer as a defense-in-depth check.
func decodeClipMonoPCM16(
	ctx context.Context,
	ffmpegPath, clipPath string,
	targetSampleRate, maxDurationSec int,
) ([]float32, error) {
	if ffmpegPath == "" {
		return nil, errors.Newf("ffmpeg path not configured").
			Component("api/v2/reanalyze").
			Category(errors.CategoryConfiguration).
			Build()
	}
	if targetSampleRate <= 0 {
		return nil, errors.Newf("invalid target sample rate: %d", targetSampleRate).
			Component("api/v2/reanalyze").
			Category(errors.CategoryValidation).
			Context("target_sample_rate", targetSampleRate).
			Build()
	}
	if maxDurationSec <= 0 {
		return nil, errors.Newf("invalid max duration: %d seconds", maxDurationSec).
			Component("api/v2/reanalyze").
			Category(errors.CategoryValidation).
			Context("max_duration_sec", maxDurationSec).
			Build()
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-i", clipPath,
		"-ac", "1",
		"-ar", fmt.Sprintf("%d", targetSampleRate),
		"-f", "s16le",
		"-t", fmt.Sprintf("%d", maxDurationSec),
		"pipe:1",
	}

	//nolint:gosec // G204: ffmpegPath is from admin-controlled Settings.Realtime.Audio.FfmpegPath
	// (validated at startup by ValidateToolPath), and clipPath is resolved from the
	// authenticated detection record via the SecureFS-validated repository path —
	// not user-supplied freeform input.
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.New(err).
			Component("api/v2/reanalyze").
			Category(errors.CategoryAudio).
			Context("operation", "ffmpeg_stdout_pipe").
			Build()
	}

	if err := cmd.Start(); err != nil {
		return nil, errors.New(err).
			Component("api/v2/reanalyze").
			Category(errors.CategoryAudio).
			Context("operation", "ffmpeg_start").
			Context("ffmpeg_path", ffmpegPath).
			Build()
	}

	// Cap the captured PCM buffer regardless of -t honoring; LimitReader
	// triggers a non-EOF error from ffmpeg's stdout writer if exceeded, which
	// surfaces as the context cancellation below. Add a one-byte tail so we
	// can detect "exceeded" vs "exactly hit cap".
	pcm, readErr := io.ReadAll(io.LimitReader(stdout, maxReanalyzeBytes+1))
	waitErr := cmd.Wait()

	if waitErr != nil {
		return nil, errors.New(waitErr).
			Component("api/v2/reanalyze").
			Category(errors.CategoryAudio).
			Context("operation", "ffmpeg_decode").
			Context("ffmpeg_stderr", stderrBuf.String()).
			Build()
	}
	if readErr != nil {
		return nil, errors.New(readErr).
			Component("api/v2/reanalyze").
			Category(errors.CategoryAudio).
			Context("operation", "ffmpeg_read_stdout").
			Build()
	}
	if len(pcm) > maxReanalyzeBytes {
		return nil, errors.Newf("clip decode exceeded byte cap").
			Component("api/v2/reanalyze").
			Category(errors.CategoryValidation).
			Context("byte_cap", maxReanalyzeBytes).
			Build()
	}
	if len(pcm) == 0 {
		return nil, errors.Newf("ffmpeg produced no PCM output").
			Component("api/v2/reanalyze").
			Category(errors.CategoryAudio).
			Context("ffmpeg_stderr", stderrBuf.String()).
			Build()
	}

	channels, err := convert.ConvertToFloat32(pcm, 16)
	if err != nil {
		return nil, errors.New(err).
			Component("api/v2/reanalyze").
			Category(errors.CategoryAudio).
			Context("operation", "pcm_to_float32").
			Build()
	}
	if len(channels) == 0 {
		return nil, errors.Newf("conversion returned zero channels").
			Component("api/v2/reanalyze").
			Category(errors.CategoryAudio).
			Build()
	}
	return channels[0], nil
}
