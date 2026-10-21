package media

// Readiness check of the clip spectrogram path: the thresholds and sentinels
// live in audio_readiness.go, and the check runs in Go with no binary.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
)

// nativeSettledAge is how long a clip must have gone unmodified for its size to
// count as settled without the stability wait. A writer that is still appending
// keeps moving the modification time, so an older file cannot be mid-write.
//
// This shortcut is also the residual gap of the readiness check: a writer that
// stamps an old modification time on a file it is still filling (some copy tools
// set it last), or that stalls for longer than the stability wait and resumes,
// passes here for formats with no exact length (MP3, M4A, Opus). The generator
// compares the file with the snapshot this check returns, which covers growth
// after readiness for every format, and WAV and FLAC are additionally refused when
// they decode shorter than their header declares. What remains is a stall of an
// in-place MP3, Opus or M4A write that outlasts the whole readiness-to-publish
// window, and a change in the instant between the generator's final stat and the
// rename; both need a writer outside BirdNET-Go, whose own writers publish a
// finished file by rename.
const nativeSettledAge = 10 * fileStabilityCheckDuration

// nativeSettle is the outcome of one readiness pass over a clip.
type nativeSettle struct {
	// info is the stat of the file that decided it is settled: the first stat when
	// the modification time is old, otherwise the re-stat after the stability wait.
	// It is nil when the file is not ready.
	info os.FileInfo
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
//
// On success it returns the state of the file that passed, for the generator to
// compare against before it publishes (spectrogram.WithSourceState).
func validateNativeSpectrogramInput(ctx context.Context, absAudioPath, audioPath, queueKey string) (spectrogram.SourceState, error) {
	start := time.Now()
	retryDelay := readinessRetryDelay
	var lastCause error

	for attempt := 1; attempt <= maxReadinessAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return spectrogram.SourceState{}, err
		}

		settle, err := settleNativeFile(ctx, absAudioPath)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return spectrogram.SourceState{}, ctxErr
			}
			return spectrogram.SourceState{}, notReadyError(err)
		}

		if settle.cause == nil {
			probe, err := probeNativeFile(ctx, absAudioPath, audioPath, queueKey, start)
			if err != nil {
				return spectrogram.SourceState{}, err
			}
			return spectrogram.NewSourceState(settle.info, probe.DecodedBytes), nil
		}

		lastCause = settle.cause
		if attempt < maxReadinessAttempts {
			retryDelay = max(retryDelay, settle.retryAfter)
			if err := waitNative(ctx, retryDelay); err != nil {
				return spectrogram.SourceState{}, err
			}
			retryDelay *= readinessBackoffMultiplier
		}
	}

	getSpectrogramLogger().Info("Audio file not ready for native spectrogram, client should retry",
		logger.String("audio_path", audioPath),
		logger.Error(lastCause),
		logger.Int("attempts", maxReadinessAttempts),
		logger.String("queue_key", queueKey))
	return spectrogram.SourceState{}, notReadyError(lastCause)
}

// probeNativeFile runs the decoder's header probe on a clip whose size has
// settled and returns what it reports. It is called once: the answer is a
// function of the file's bytes.
func probeNativeFile(ctx context.Context, absAudioPath, audioPath, queueKey string, start time.Time) (transcode.ProbeInfo, error) {
	info, err := transcode.ProbeFile(ctx, absAudioPath)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return transcode.ProbeInfo{}, ctxErr
		}
		if errors.Is(err, transcode.ErrUnsupportedSource) || errors.Is(err, transcode.ErrAudioTooLong) {
			return transcode.ProbeInfo{}, err
		}
		return transcode.ProbeInfo{}, notReadyError(err)
	}
	getSpectrogramLogger().Debug("Audio file validated for native spectrogram",
		logger.String("audio_path", audioPath),
		logger.Int("sample_rate", info.SampleRate),
		logger.Int("channels", info.Channels),
		logger.Int64("validation_duration_ms", time.Since(start).Milliseconds()),
		logger.String("queue_key", queueKey))
	return info, nil
}

// notReadyError wraps cause as a retryable not-ready error with the default
// spectrogram retry delay.
func notReadyError(cause error) *AudioNotReadyError {
	return &AudioNotReadyError{
		RetryAfter: spectrogramRetryDelay,
		Err:        fmt.Errorf("%w: %w", errAudioFileNotReady, cause),
	}
}

// settleNativeFile runs one readiness pass: the size must be at least
// minReadyAudioBytes and, for a recently modified file, unchanged over
// fileStabilityCheckDuration. A stat failure is returned as an error;
// any other verdict is in the result.
func settleNativeFile(ctx context.Context, path string) (nativeSettle, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nativeSettle{}, err
	}
	size := info.Size()

	switch {
	case size == 0:
		return nativeSettle{retryAfter: readinessRetryDelay, cause: errAudioFileEmpty}, nil
	case size < minReadyAudioBytes:
		return nativeSettle{
			retryAfter: readinessRetryDelay,
			cause:      fmt.Errorf("%w: size %d bytes < minimum %d bytes", errAudioFileTooSmall, size, minReadyAudioBytes),
		}, nil
	}

	if time.Since(info.ModTime()) > nativeSettledAge {
		return nativeSettle{info: info}, nil
	}
	if err := waitNative(ctx, fileStabilityCheckDuration); err != nil {
		return nativeSettle{}, err
	}
	// A failed re-stat counts as a change: the file is not in a settled state.
	changed := true
	after, statErr := os.Stat(path)
	if statErr == nil {
		changed = after.Size() != size
	}
	if changed {
		return nativeSettle{
			retryAfter: readinessRetryDelay * readinessBackoffMultiplier,
			cause:      fmt.Errorf("%w: file size is still changing", errAudioFileIncomplete),
		}, nil
	}
	return nativeSettle{info: after}, nil
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
