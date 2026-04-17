// Tests for SaveAudioAction deferred-read behavior used by Extended Capture.
// These tests cover the path introduced when buildSaveAudioAction can't read
// the capture segment immediately because the tail of the clip has not been
// written to the ring buffer yet.

package processor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	audioBuffer "github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestSaveAudioActionExecute_DefersUntilCaptureReady(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	settings := conf.NewTestSettings().
		WithAudioExport(tmpDir, "wav", "192k").
		Build()

	mgr := audioBuffer.NewManager(GetLogger())

	action := &SaveAudioAction{
		Settings:  settings,
		ClipName:  "deferred.wav",
		bufferMgr: mgr,
		sourceID:  "test-source",
		beginTime: time.Now(),
		duration:  2,
		// Use a generous margin so a CI runner that delays test dispatch
		// still sees readyAt as "in the future" when Execute runs. A
		// shorter value (e.g. 100 ms) was observed to be flaky under load.
		readyAt: time.Now().Add(5 * time.Second),
	}

	err := action.Execute(t.Context(), nil)
	require.Error(t, err)
	require.ErrorIs(t, err, errAudioExportDeferred)
}

func TestSaveAudioActionExecute_ReadsDeferredCaptureWhenReady(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	settings := conf.NewTestSettings().
		WithAudioExport(tmpDir, "wav", "192k").
		Build()

	mgr := audioBuffer.NewManager(GetLogger())
	sourceID := "test-source"
	require.NoError(t, mgr.AllocateCapture(sourceID, 10, conf.SampleRate, conf.BitDepth/8))

	cb, err := mgr.CaptureBuffer(sourceID)
	require.NoError(t, err)

	// Write three seconds of deterministic PCM so ReadSegment has data to
	// return for the two-second window requested below.
	const writeSeconds = 3
	chunk := make([]byte, writeSeconds*conf.SampleRate*(conf.BitDepth/8))
	for i := range chunk {
		chunk[i] = byte(i % 251)
	}
	require.NoError(t, cb.Write(chunk))

	action := &SaveAudioAction{
		Settings:  settings,
		ClipName:  "deferred.wav",
		bufferMgr: mgr,
		sourceID:  sourceID,
		beginTime: cb.StartTime(),
		duration:  2,
		readyAt:   time.Now().Add(-time.Second),
	}

	require.NoError(t, action.Execute(t.Context(), nil))

	outputPath := filepath.Join(tmpDir, "deferred.wav")
	info, err := os.Stat(outputPath)
	require.NoError(t, err)
	assert.Positive(t, info.Size())
}

func TestSaveAudioActionExecute_PropagatesBufferReadError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	settings := conf.NewTestSettings().
		WithAudioExport(tmpDir, "wav", "192k").
		Build()

	mgr := audioBuffer.NewManager(GetLogger())
	// Deliberately do not allocate a capture buffer for this sourceID so
	// CaptureBuffer() returns an error. Execute should surface that error
	// instead of silently dropping the clip.
	action := &SaveAudioAction{
		Settings:  settings,
		ClipName:  "deferred.wav",
		bufferMgr: mgr,
		sourceID:  "missing-source",
		beginTime: time.Now().Add(-10 * time.Second),
		duration:  2,
		readyAt:   time.Now().Add(-time.Second),
	}

	err := action.Execute(t.Context(), nil)
	require.Error(t, err)
	assert.NotErrorIs(t, err, errAudioExportDeferred)
}

func TestGetJobQueueRetryConfig_SaveAudioDeferred(t *testing.T) {
	t.Parallel()

	mgr := audioBuffer.NewManager(GetLogger())

	cfg := getJobQueueRetryConfig(&SaveAudioAction{
		bufferMgr: mgr,
		sourceID:  "test-source",
		duration:  10,
		readyAt:   time.Now().Add(time.Second),
	})

	// Pin the backoff parameters so an accidental change to workers.go
	// fails this test instead of silently altering production behavior.
	assert.True(t, cfg.Enabled)
	assert.Equal(t, saveAudioDeferredInitialDelay, cfg.InitialDelay)
	assert.Equal(t, saveAudioDeferredMaxDelay, cfg.MaxDelay)
	assert.InEpsilon(t, saveAudioDeferredMultiplier, cfg.Multiplier, 0.001)
	// Short clips (10s + 30s margin = 40s) fit within the exponential
	// phase (~61s), so MaxRetries equals the exponential-phase count.
	assert.Equal(t, saveAudioDeferredExpPhaseRetries, cfg.MaxRetries)
}

func TestSaveAudioDeferredMaxRetries_ScalesWithDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		duration int
		minTotal time.Duration
	}{
		// duration + saveAudioDeferredMarginSeconds must be covered by the
		// summed backoff schedule; a few representative points across the
		// supported range (1s..MaxExtendedCaptureDuration=1200s).
		{"short", 10, 40 * time.Second},
		{"medium", 60, 90 * time.Second},
		{"long", 300, 330 * time.Second},
		{"maximum", 1200, 1230 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			retries := saveAudioDeferredMaxRetries(tt.duration)
			// Simulate the backoff sum to verify the schedule covers the budget.
			total := time.Duration(0)
			delay := saveAudioDeferredInitialDelay
			for range retries {
				total += delay
				delay = time.Duration(float64(delay) * saveAudioDeferredMultiplier)
				if delay > saveAudioDeferredMaxDelay {
					delay = saveAudioDeferredMaxDelay
				}
			}
			assert.GreaterOrEqual(t, total, tt.minTotal,
				"retry schedule must cover capture duration + margin for %s", tt.name)
		})
	}
}

func TestGetJobQueueRetryConfig_SaveAudioEagerHasNoRetry(t *testing.T) {
	t.Parallel()

	// Eager (non-deferred) SaveAudioAction has pcmData already populated and
	// must not participate in the retry loop; retrying an encode failure
	// does not help because the PCM window has already been copied out.
	cfg := getJobQueueRetryConfig(&SaveAudioAction{
		pcmData: []byte{0},
	})

	assert.False(t, cfg.Enabled)
}

// Guard against drift: errAudioExportDeferred is a plain sentinel today but
// callers may wrap it (e.g. with fmt.Errorf %w) as the deferred-read path
// evolves. Assert the wrapped form still satisfies errors.Is so
// DefersUntilCaptureReady's ErrorIs check cannot be defeated by a future
// wrap/unwrap change.
func TestErrAudioExportDeferred_IsSentinel(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("deferred execute: %w", errAudioExportDeferred)
	assert.ErrorIs(t, wrapped, errAudioExportDeferred)
}
