package analysis

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// TestProcessMonitorTick_SkipsWhenModelNotLoaded verifies the monitor-layer guard
// that stops the "model not loaded" error storm (Sentry BIRDNET-GO-2G6 / 1S1). A
// monitor whose model was unloaded (or never registered) must skip the inference
// window with a single warning instead of dispatching to ProcessData, which would
// log "error processing data" on every window during the reconfigure gap.
func TestProcessMonitorTick_SkipsWhenModelNotLoaded(t *testing.T) {
	t.Parallel()

	const (
		sourceID = "src"
		modelID  = "Perch_V2"
		readSize = 480
	)

	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	require.NoError(t, mgr.AllocateAnalysis(sourceID, modelID, readSize*2, 0, readSize))
	ab, err := mgr.AnalysisBuffer(sourceID, modelID)
	require.NoError(t, err)
	require.NoError(t, ab.Write(make([]byte, readSize)))

	var logBuf bytes.Buffer
	bm := &BufferManager{
		bn:        &classifier.Orchestrator{}, // no models loaded, so IsModelLoaded is false
		bufferMgr: mgr,
		logger:    logger.NewSlogLogger(&logBuf, logger.LogLevelDebug, time.UTC),
	}

	cfg := &monitorConfig{sourceID: sourceID, modelID: modelID, readSize: readSize}
	quit := make(chan struct{})
	state := &monitorTickState{}

	keepRunning := bm.processMonitorTick(quit, cfg, readSize, 0, state, 1)
	require.True(t, keepRunning, "the monitor keeps running so a reinstall can resume it")
	assert.Contains(t, logBuf.String(), "model not loaded, skipping inference window")
	assert.NotContains(t, logBuf.String(), "error processing data",
		"a not-loaded model must not reach ProcessData")
	assert.True(t, state.notLoadedWarned, "the warn-once latch is set after the first skip")

	// A second full window on the next tick must not repeat the warning and must
	// still never dispatch to ProcessData.
	require.NoError(t, ab.Write(make([]byte, readSize)))
	logBuf.Reset()
	keepRunning = bm.processMonitorTick(quit, cfg, readSize, 0, state, 2)
	require.True(t, keepRunning)
	assert.NotContains(t, logBuf.String(), "error processing data")
	assert.NotContains(t, logBuf.String(), "model not loaded, skipping inference window",
		"the warning is emitted once per monitor, not on every tick")
}

func TestMonitorConfig_ReadSize(t *testing.T) {
	t.Parallel()

	cfg := monitorConfig{
		sourceID: "mic1",
		modelID:  "birdnet-v2.4",
		spec:     classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		readSize: 288000,
	}
	assert.Equal(t, 288000, cfg.readSize)

	cfg2 := monitorConfig{
		sourceID: "mic1",
		modelID:  "perch-v2",
		spec:     classifier.ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
		readSize: 320000,
	}
	assert.Equal(t, 320000, cfg2.readSize)
}

func TestMonitorKey_Equality(t *testing.T) {
	t.Parallel()

	k1 := monitorKey{sourceID: "mic1", modelID: "birdnet-v2.4"}
	k2 := monitorKey{sourceID: "mic1", modelID: "perch-v2"}
	k3 := monitorKey{sourceID: "mic1", modelID: "birdnet-v2.4"}

	assert.NotEqual(t, k1, k2)
	assert.Equal(t, k1, k3)
}

func TestMonitorKey_DifferentSources(t *testing.T) {
	t.Parallel()

	k1 := monitorKey{sourceID: "mic1", modelID: "birdnet-v2.4"}
	k2 := monitorKey{sourceID: "mic2", modelID: "birdnet-v2.4"}

	assert.NotEqual(t, k1, k2, "different sources with same model should not be equal")
}

func TestMonitorKey_UsableAsMapKey(t *testing.T) {
	t.Parallel()

	m := make(map[monitorKey]int)
	k1 := monitorKey{sourceID: "mic1", modelID: "birdnet-v2.4"}
	k2 := monitorKey{sourceID: "mic1", modelID: "perch-v2"}
	k3 := monitorKey{sourceID: "mic1", modelID: "birdnet-v2.4"} // same as k1

	m[k1] = 1
	m[k2] = 2

	assert.Len(t, m, 2)
	assert.Equal(t, 1, m[k3], "same key should retrieve same value")
}

func TestBuildMonitorConfig(t *testing.T) {
	t.Parallel()

	info := &classifier.ModelInfo{
		ID:   "BirdNET_V2.4",
		Name: classifier.ModelNameBirdNETv24,
		Spec: classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
	}

	cfg := buildMonitorConfig("mic1", info)

	assert.Equal(t, "mic1", cfg.sourceID)
	assert.Equal(t, "BirdNET_V2.4", cfg.modelID)
	assert.Equal(t, 48000, cfg.spec.SampleRate)
	assert.Equal(t, 3*time.Second, cfg.spec.ClipLength)

	// readSize = SampleRate * ClipLengthSec * NumChannels * (BitDepth / 8)
	// = 48000 * 3 * 1 * 2 = 288000
	expectedReadSize := 48000 * 3 * conf.NumChannels * (conf.BitDepth / 8)
	assert.Equal(t, expectedReadSize, cfg.readSize)
}

func TestBuildMonitorConfig_PerchV2(t *testing.T) {
	t.Parallel()

	info := &classifier.ModelInfo{
		ID:   "Perch_V2",
		Name: classifier.ModelNamePerchV2,
		Spec: classifier.ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
	}

	cfg := buildMonitorConfig("stream1", info)

	assert.Equal(t, "stream1", cfg.sourceID)
	assert.Equal(t, "Perch_V2", cfg.modelID)
	assert.Equal(t, 32000, cfg.spec.SampleRate)
	assert.Equal(t, 5*time.Second, cfg.spec.ClipLength)

	// readSize = SampleRate * ClipLengthSec * NumChannels * (BitDepth / 8)
	// = 32000 * 5 * 1 * 2 = 320000
	expectedReadSize := 32000 * 5 * conf.NumChannels * (conf.BitDepth / 8)
	assert.Equal(t, expectedReadSize, cfg.readSize)
}

func TestNewBufferManager_NilParams(t *testing.T) {
	t.Parallel()

	_, err := NewBufferManager(nil, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BirdNET instance cannot be nil")
}

func TestMonitorConfig_OverlapSizeDefault(t *testing.T) {
	t.Parallel()

	// overlapSize defaults to zero when not set
	cfg := monitorConfig{
		sourceID: "mic1",
		modelID:  "birdnet-v2.4",
		spec:     classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		readSize: 288000,
	}
	assert.Equal(t, 0, cfg.overlapSize, "overlapSize should default to zero")
}

// TestDetectionOffset_DerivedFromClipLength validates that the detection offset
// uses the model's clip length to position bird calls correctly in saved clips.
// The offset compensates for the age of audio in the analysis window: the oldest
// sample is approximately ClipLength seconds old when Read() returns. Using
// ClipLength ensures the bird call appears between PreCapture and
// (PreCapture + ClipLength) seconds into the saved clip.
func TestDetectionOffset_DerivedFromClipLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		clipLength    time.Duration
		preCapture    int
		captureLength int
	}{
		{"BirdNET_v2.4_default", 3 * time.Second, 3, 20},
		{"BirdNET_v2.4_no_precapture", 3 * time.Second, 0, 15},
		{"BirdNET_v3.0", 5 * time.Second, 3, 20},
		{"Perch_v2", 5 * time.Second, 5, 30},
		{"BirdNET_v2.4_max_precapture", 3 * time.Second, 5, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Replicate the production offset calculation from
			// analysisBufferMonitor + processMonitorTick.
			detectionOffset := tt.clipLength
			preCapture := time.Duration(tt.preCapture) * time.Second
			beginTimeOffset := preCapture + detectionOffset

			now := time.Now()
			startTime := now.Add(-beginTimeOffset)

			// The bird vocalized somewhere in the analysis window, which
			// spans approximately [now - clipLength, now]. With the offset
			// set to clipLength, the window start maps to PreCapture seconds
			// into the saved clip.
			windowStart := now.Add(-tt.clipLength)
			windowEnd := now

			birdPositionEarliest := windowStart.Sub(startTime)
			birdPositionLatest := windowEnd.Sub(startTime)

			assert.Equal(t, preCapture, birdPositionEarliest,
				"bird at window start should appear at PreCapture in clip")
			assert.Equal(t, preCapture+tt.clipLength, birdPositionLatest,
				"bird at window end should appear at PreCapture+ClipLength in clip")

			// Verify the clip doesn't overflow the capture length.
			captureLength := time.Duration(tt.captureLength) * time.Second
			assert.LessOrEqual(t, birdPositionLatest, captureLength,
				"latest bird position must fit within capture length")
		})
	}
}
