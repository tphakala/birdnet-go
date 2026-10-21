package media

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	nativeReadyRate    = 48000
	nativeReadySeconds = 1.0
	// nativeReadyUndecodableBytes sizes a corrupt clip above the minimum size so
	// the size rule does not decide the outcome.
	nativeReadyUndecodableBytes = 2000
	// nativeReadyAppendInterval is the writer period of the never-stable file.
	nativeReadyAppendInterval = 100 * time.Millisecond
	// nativeReadyOversizeBytes is one byte past the decoder's source file bound
	// (256 MiB, transcode limits.go maxSourceFileBytes).
	nativeReadyOversizeBytes = 256<<20 + 1
	// nativeReadyUnsupportedBytes sizes the .aac fixture above the minimum size.
	nativeReadyUnsupportedBytes = 2048
)

// nativeReadyBackoff is the wait between attempts after a first retry delay of
// base: base, then doubling, over the attempts that follow the first.
func nativeReadyBackoff(base time.Duration) time.Duration {
	var total time.Duration
	delay := base
	for range maxReadinessAttempts - 1 {
		total += delay
		delay *= readinessBackoffMultiplier
	}
	return total
}

// callNative runs validateNativeSpectrogramInput inside the current bubble and
// returns its error and the fake time it took.
func callNative(ctx context.Context, path string) (elapsed time.Duration, err error) {
	start := time.Now()
	_, err = validateNativeSpectrogramInput(ctx, path, filepath.Base(path), "test-key")
	return time.Since(start), err
}

func requireNotReady(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	anr, ok := errors.AsType[*AudioNotReadyError](err)
	require.Truef(t, ok, "want AudioNotReadyError, got %T: %v", err, err)
	require.ErrorIs(t, err, errAudioFileNotReady)
	assert.Equal(t, spectrogramRetryDelay, anr.RetryAfter)
}

// appendBytes grows the file at path by two bytes.
func appendBytes(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write([]byte{0, 0})
	return errors.Join(werr, f.Close())
}

func writeNativeUndecodable(t *testing.T, path string) {
	t.Helper()
	data := make([]byte, nativeReadyUndecodableBytes)
	copy(data, "fLaC")
	for i := 4; i < len(data); i++ {
		data[i] = 0xAB
	}
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func TestValidateNativeSpectrogramInput_ReadyClip(t *testing.T) {
	writers := map[string]func(*testing.T, string, int, float64, float64){
		"wav":  writeToneWAV,
		"flac": writeToneFLAC,
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "clip."+name)
			write(t, path, nativeReadyRate, nativeReadySeconds, fixtureToneHz)
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.GreaterOrEqual(t, info.Size(), minReadyAudioBytes)

			synctest.Test(t, func(t *testing.T) {
				elapsed, err := callNative(t.Context(), path)
				require.NoError(t, err)
				assert.Equal(t, fileStabilityCheckDuration, elapsed)
			})
		})
	}
}

// A clip that has not been modified for longer than nativeSettledAge cannot be
// mid-write, so readiness skips the stability wait. The bubble's fake clock
// makes the elapsed time exact: any wait would show as a non-zero duration.
func TestValidateNativeSpectrogramInput_SettledClipSkipsStabilityWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.wav")
	writeToneWAV(t, path, nativeReadyRate, nativeReadySeconds, fixtureToneHz)

	synctest.Test(t, func(t *testing.T) {
		old := time.Now().Add(-2 * nativeSettledAge)
		require.NoError(t, os.Chtimes(path, old, old))

		elapsed, err := callNative(t.Context(), path)

		require.NoError(t, err)
		assert.Zero(t, elapsed, "an old clip must not wait out the stability check")
	})
}

func TestValidateNativeSpectrogramInput_EmptyFileExhausts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.wav")
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	synctest.Test(t, func(t *testing.T) {
		elapsed, err := callNative(t.Context(), path)
		requireNotReady(t, err)
		require.ErrorIs(t, err, errAudioFileEmpty)
		assert.Equal(t, nativeReadyBackoff(readinessRetryDelay), elapsed)
	})
}

func TestValidateNativeSpectrogramInput_TinyFileExhausts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tiny.wav")
	require.NoError(t, os.WriteFile(path, make([]byte, minReadyAudioBytes-1), 0o600))

	synctest.Test(t, func(t *testing.T) {
		elapsed, err := callNative(t.Context(), path)
		requireNotReady(t, err)
		require.ErrorIs(t, err, errAudioFileTooSmall)
		assert.Equal(t, nativeReadyBackoff(readinessRetryDelay), elapsed)
	})
}

func TestValidateNativeSpectrogramInput_GrowingFileRetriesUntilStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growing.wav")
	writeToneWAV(t, path, nativeReadyRate, nativeReadySeconds, fixtureToneHz)

	synctest.Test(t, func(t *testing.T) {
		var wg sync.WaitGroup
		var appendErr error
		wg.Go(func() {
			time.Sleep(fileStabilityCheckDuration / 2)
			appendErr = appendBytes(path)
		})

		elapsed, err := callNative(t.Context(), path)
		wg.Wait()

		require.NoError(t, appendErr)
		require.NoError(t, err)
		// Attempt 1 waits one window and sees a change; the raised retry delay
		// is ValidationRetryDelay*RetryDelayMultiplier; attempt 2 is stable.
		want := fileStabilityCheckDuration + readinessRetryDelay*readinessBackoffMultiplier + fileStabilityCheckDuration
		assert.Equal(t, want, elapsed)
	})
}

func TestValidateNativeSpectrogramInput_NeverStableExhausts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unstable.wav")
	writeToneWAV(t, path, nativeReadyRate, nativeReadySeconds, fixtureToneHz)

	synctest.Test(t, func(t *testing.T) {
		stop := make(chan struct{})
		var wg sync.WaitGroup
		var appendErr error
		wg.Go(func() {
			for appendErr == nil {
				select {
				case <-stop:
					return
				case <-time.After(nativeReadyAppendInterval):
					appendErr = appendBytes(path)
				}
			}
		})

		elapsed, err := callNative(t.Context(), path)
		close(stop)
		wg.Wait()

		require.NoError(t, appendErr)

		requireNotReady(t, err)
		require.ErrorIs(t, err, errAudioFileIncomplete)
		want := maxReadinessAttempts*fileStabilityCheckDuration +
			nativeReadyBackoff(readinessRetryDelay*readinessBackoffMultiplier)
		assert.Equal(t, want, elapsed)
	})
}

func TestValidateNativeSpectrogramInput_UndecodableIsNotRetried(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.flac")
	writeNativeUndecodable(t, path)

	synctest.Test(t, func(t *testing.T) {
		elapsed, err := callNative(t.Context(), path)
		requireNotReady(t, err)
		require.ErrorIs(t, err, transcode.ErrUndecodableSource)
		assert.Equal(t, fileStabilityCheckDuration, elapsed)
	})
}

func TestValidateNativeSpectrogramInput_UnsupportedIsPermanent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.aac")
	require.NoError(t, os.WriteFile(path, make([]byte, nativeReadyUnsupportedBytes), 0o600))

	synctest.Test(t, func(t *testing.T) {
		_, err := callNative(t.Context(), path)
		require.ErrorIs(t, err, transcode.ErrUnsupportedSource)
		_, isNotReady := errors.AsType[*AudioNotReadyError](err)
		assert.False(t, isNotReady)
		assert.NotErrorIs(t, err, errAudioFileNotReady)
	})
}

func TestValidateNativeSpectrogramInput_CanceledDuringStabilityWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.wav")
	writeToneWAV(t, path, nativeReadyRate, nativeReadySeconds, fixtureToneHz)

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var wg sync.WaitGroup
		wg.Go(func() {
			time.Sleep(fileStabilityCheckDuration / 2)
			cancel()
		})

		_, err := callNative(ctx, path)
		wg.Wait()

		require.ErrorIs(t, err, context.Canceled)
		_, isNotReady := errors.AsType[*AudioNotReadyError](err)
		assert.False(t, isNotReady)
	})
}

func TestValidateNativeSpectrogramInput_CanceledDuringBackoff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tiny.wav")
	require.NoError(t, os.WriteFile(path, make([]byte, minReadyAudioBytes-1), 0o600))

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var wg sync.WaitGroup
		wg.Go(func() {
			time.Sleep(readinessRetryDelay / 2)
			cancel()
		})

		elapsed, err := callNative(ctx, path)
		wg.Wait()

		require.ErrorIs(t, err, context.Canceled)
		_, isNotReady := errors.AsType[*AudioNotReadyError](err)
		assert.False(t, isNotReady)
		assert.Equal(t, readinessRetryDelay/2, elapsed)
	})
}

func TestValidateNativeSpectrogramInput_TooLongIsPermanent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.wav")
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(nativeReadyOversizeBytes))
	require.NoError(t, f.Close())

	synctest.Test(t, func(t *testing.T) {
		_, err := callNative(t.Context(), path)
		require.ErrorIs(t, err, transcode.ErrAudioTooLong)
		_, isNotReady := errors.AsType[*AudioNotReadyError](err)
		assert.False(t, isNotReady)
	})
}

func TestValidateNativeSpectrogramInput_MissingFileIsNotReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, err := callNative(t.Context(), filepath.Join(t.TempDir(), "gone.wav"))
		requireNotReady(t, err)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}

// TestValidateNativeSpectrogramInput_TransitionsAreJudgedOnCurrentBytes pins that
// nothing about an earlier verdict is remembered: each step is a fresh call on
// the same path and is judged on the bytes it finds.
func TestValidateNativeSpectrogramInput_TransitionsAreJudgedOnCurrentBytes(t *testing.T) {
	type step string
	const (
		stepTiny    step = "tiny"
		stepValid   step = "valid"
		stepCorrupt step = "corrupt-same-size-and-mtime"
		stepDeleted step = "deleted"
	)
	sequences := map[string][]step{
		"tiny-first":  {stepTiny, stepValid, stepCorrupt, stepValid, stepDeleted},
		"valid-first": {stepValid, stepCorrupt, stepTiny, stepValid},
	}
	for name, seq := range sequences {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "clip.flac")
			writeToneFLAC(t, path, nativeReadyRate, nativeReadySeconds, fixtureToneHz)
			validBytes, err := os.ReadFile(path)
			require.NoError(t, err)
			info, err := os.Stat(path)
			require.NoError(t, err)
			validMtime := info.ModTime()
			corrupt := make([]byte, len(validBytes))
			copy(corrupt, "fLaC")

			for _, s := range seq {
				switch s {
				case stepTiny:
					require.NoError(t, os.WriteFile(path, make([]byte, minReadyAudioBytes-1), 0o600))
				case stepValid:
					require.NoError(t, os.WriteFile(path, validBytes, 0o600))
				case stepCorrupt:
					require.NoError(t, os.WriteFile(path, corrupt, 0o600))
					require.NoError(t, os.Chtimes(path, validMtime, validMtime))
				case stepDeleted:
					require.NoError(t, os.Remove(path))
				}

				synctest.Test(t, func(t *testing.T) {
					_, err := callNative(t.Context(), path)
					switch s {
					case stepValid:
						require.NoErrorf(t, err, "step %s", s)
					case stepTiny:
						requireNotReady(t, err)
						require.ErrorIs(t, err, errAudioFileTooSmall)
					case stepCorrupt:
						requireNotReady(t, err)
						require.ErrorIs(t, err, transcode.ErrUndecodableSource)
					case stepDeleted:
						requireNotReady(t, err)
						require.ErrorIs(t, err, os.ErrNotExist)
					}
				})
			}
		})
	}
}

// The state readiness returns is the stat of the pass that judged the clip ready,
// plus the probe's decoded-size bound, so the generator can tell whether the file
// is still the one that passed.
func TestValidateNativeSpectrogramInput_ReturnsSettledState(t *testing.T) {
	tests := []struct {
		name string
		old  bool
	}{
		{name: "old modification time skips the stability wait", old: true},
		{name: "recent modification time waits for stability"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "clip.wav")
			writeToneWAV(t, path, nativeReadyRate, nativeReadySeconds, fixtureToneHz)

			synctest.Test(t, func(t *testing.T) {
				if tt.old {
					old := time.Now().Add(-2 * nativeSettledAge)
					require.NoError(t, os.Chtimes(path, old, old))
				}

				state, err := validateNativeSpectrogramInput(t.Context(), path, filepath.Base(path), "test-key")
				require.NoError(t, err)

				now, err := os.Stat(path)
				require.NoError(t, err)
				assert.True(t, state.Matches(now), "the state matches the file as it stood")
				assert.Equal(t, int64(nativeReadyRate*nativeReadySeconds*2), state.DecodedBytes(), "16-bit mono at the probe's declared length")

				require.NoError(t, appendBytes(path))
				grown, err := os.Stat(path)
				require.NoError(t, err)
				assert.False(t, state.Matches(grown), "a grown file no longer matches")
			})
		})
	}
}
