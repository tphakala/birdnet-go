package processor

import (
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
		// 5 seconds is long enough to remain in the future even on a
		// heavily-loaded CI runner that delays test dispatch, so the
		// Execute call below reliably hits the deferred-retry code path.
		// The previous 100ms was flaky under load.
		readyAt: time.Now().Add(5 * time.Second),
	}

	err := action.Execute(t.Context(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deferred")
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

	chunk := make([]byte, 3*conf.SampleRate*(conf.BitDepth/8))
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

func TestGetJobQueueRetryConfig_SaveAudioDeferredRead(t *testing.T) {
	t.Parallel()

	cfg := getJobQueueRetryConfig(&SaveAudioAction{
		sourceID: "test-source",
		duration: 10,
		readyAt:  time.Now().Add(time.Second),
	})

	// Lock the deferred-export retry contract to exact values so an
	// accidental change to workers.go's retry policy (MaxRetries,
	// InitialDelay, MaxDelay, or Multiplier) fails this test instead
	// of silently altering production behavior. The previous
	// GreaterOrEqual(MaxRetries, 1) assertion was too permissive to
	// catch a regression.
	assert.True(t, cfg.Enabled)
	assert.Equal(t, 7, cfg.MaxRetries)
	assert.Equal(t, time.Second, cfg.InitialDelay)
	assert.Equal(t, 30*time.Second, cfg.MaxDelay)
	assert.InEpsilon(t, 2.0, cfg.Multiplier, 0.001)
}
