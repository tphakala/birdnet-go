package media

// Native counterpart of validateSpectrogramInputs. It keeps ffprobe's readiness
// thresholds by reusing package ffmpeg's constants and sentinels, so both
// renderer gates answer the same file state the same way, and it runs no binary.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// nativeSettledAge is how long a clip must have gone unmodified for its size to
// count as settled without the stability wait. A writer that is still appending
// keeps moving the modification time, so an older file cannot be mid-write.
const nativeSettledAge = 10 * ffmpeg.FileStabilityCheckDuration

// nativeSettle is the outcome of one readiness pass over a clip.
type nativeSettle struct {
	// cause says why the file is not ready; nil means it is ready.
	cause error
	// retryAfter is how long to wait before the next pass when not ready.
	retryAfter time.Duration
}

// validateNativeSpectrogramInput checks that the clip at absAudioPath is ready
// for the native renderer: large enough, with a stable size (a clip unmodified
// for nativeSettledAge is taken as stable without the wait), and openable by
// the decoder. A file that is not ready after the bounded retries yields an
// AudioNotReadyError. A source the decoder permanently rejects (unsupported
// format or bounds, too long) and context errors are returned as they are.
func validateNativeSpectrogramInput(ctx context.Context, absAudioPath, audioPath, queueKey string) error {
	start := time.Now()
	retryDelay := ffmpeg.ValidationRetryDelay
	var lastCause error

	for attempt := 1; attempt <= ffmpeg.MaxValidationAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		settle, err := settleNativeFile(ctx, absAudioPath)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return notReadyError(err)
		}

		if settle.cause == nil {
			return probeNativeFile(ctx, absAudioPath, audioPath, queueKey, start)
		}

		lastCause = settle.cause
		if attempt < ffmpeg.MaxValidationAttempts {
			retryDelay = max(retryDelay, settle.retryAfter)
			if err := waitNative(ctx, retryDelay); err != nil {
				return err
			}
			retryDelay *= ffmpeg.RetryDelayMultiplier
		}
	}

	getSpectrogramLogger().Info("Audio file not ready for native spectrogram, client should retry",
		logger.String("audio_path", audioPath),
		logger.Error(lastCause),
		logger.Int("attempts", ffmpeg.MaxValidationAttempts),
		logger.String("queue_key", queueKey))
	return notReadyError(lastCause)
}

// probeNativeFile runs the decoder's header probe on a clip whose size has
// settled. It is called once: the answer is a function of the file's bytes.
func probeNativeFile(ctx context.Context, absAudioPath, audioPath, queueKey string, start time.Time) error {
	info, err := transcode.ProbeFile(ctx, absAudioPath)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(err, transcode.ErrUnsupportedSource) || errors.Is(err, transcode.ErrAudioTooLong) {
			return err
		}
		return notReadyError(err)
	}
	getSpectrogramLogger().Debug("Audio file validated for native spectrogram",
		logger.String("audio_path", audioPath),
		logger.Int("sample_rate", info.SampleRate),
		logger.Int("channels", info.Channels),
		logger.Int64("validation_duration_ms", time.Since(start).Milliseconds()),
		logger.String("queue_key", queueKey))
	return nil
}

// notReadyError wraps cause as a retryable not-ready error with the default
// spectrogram retry delay.
func notReadyError(cause error) *AudioNotReadyError {
	return &AudioNotReadyError{
		RetryAfter: spectrogramRetryDelay,
		Err:        fmt.Errorf("%w: %w", ffmpeg.ErrAudioFileNotReady, cause),
	}
}

// settleNativeFile runs one readiness pass: the size must be at least
// ffmpeg.MinValidAudioSize and, for a recently modified file, unchanged over
// ffmpeg.FileStabilityCheckDuration. A stat failure is returned as an error;
// any other verdict is in the result.
func settleNativeFile(ctx context.Context, path string) (nativeSettle, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nativeSettle{}, err
	}
	size := info.Size()

	switch {
	case size == 0:
		return nativeSettle{retryAfter: ffmpeg.ValidationRetryDelay, cause: ffmpeg.ErrAudioFileEmpty}, nil
	case size < ffmpeg.MinValidAudioSize:
		return nativeSettle{
			retryAfter: ffmpeg.ValidationRetryDelay,
			cause:      fmt.Errorf("%w: size %d bytes < minimum %d bytes", ffmpeg.ErrAudioFileTooSmall, size, ffmpeg.MinValidAudioSize),
		}, nil
	}

	if time.Since(info.ModTime()) > nativeSettledAge {
		return nativeSettle{}, nil
	}
	if err := waitNative(ctx, ffmpeg.FileStabilityCheckDuration); err != nil {
		return nativeSettle{}, err
	}
	// A failed re-stat counts as a change: the file is not in a settled state.
	changed := true
	if after, statErr := os.Stat(path); statErr == nil {
		changed = after.Size() != size
	}
	if changed {
		return nativeSettle{
			retryAfter: ffmpeg.ValidationRetryDelay * ffmpeg.RetryDelayMultiplier,
			cause:      fmt.Errorf("%w: file size is still changing", ffmpeg.ErrAudioFileIncomplete),
		}, nil
	}
	return nativeSettle{}, nil
}

// waitNative sleeps for d, returning ctx.Err() when ctx ends first.
func waitNative(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
