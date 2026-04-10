package processor

import (
	"context"
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
		readyAt:   time.Now().Add(100 * time.Millisecond),
	}

	err := action.Execute(context.Background(), nil)
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

	require.NoError(t, action.Execute(context.Background(), nil))

	outputPath := filepath.Join(tmpDir, "deferred.wav")
	info, err := os.Stat(outputPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))
}

func TestGetJobQueueRetryConfig_SaveAudioDeferredRead(t *testing.T) {
	t.Parallel()

	cfg := getJobQueueRetryConfig(&SaveAudioAction{
		sourceID: "test-source",
		duration: 10,
		readyAt:  time.Now().Add(time.Second),
	})

	assert.True(t, cfg.Enabled)
	assert.GreaterOrEqual(t, cfg.MaxRetries, 1)
}
