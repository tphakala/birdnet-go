package analysis

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
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
	assert.True(t, state.skippedSinceProcess, "an unanalyzed window makes the next one report a gap")

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

// scriptedModelState is a classifierBackend fake that drives processMonitorTick
// through the not-loaded -> loaded resume transition a real Orchestrator only
// reaches with fully loaded models (its models map and bat scheduler are
// package-private to internal/classifier). The embedded interface is nil: only
// IsModelLoaded and IsModelActive are exercised by this test; the remaining
// classifierBackend methods would panic if called, which they never are because
// the model is kept inactive so ProcessData is not reached.
type scriptedModelState struct {
	classifierBackend
	loaded     []bool // IsModelLoaded return per call; the final entry repeats
	loadedCall int
	active     bool // IsModelActive return
}

func (s *scriptedModelState) IsModelLoaded(string) bool {
	if len(s.loaded) == 0 {
		return false
	}
	i := s.loadedCall
	if i >= len(s.loaded) {
		i = len(s.loaded) - 1
	}
	s.loadedCall++
	return s.loaded[i]
}

func (s *scriptedModelState) IsModelActive(string) bool { return s.active }

// TestProcessMonitorTick_ResumesWhenModelReloads covers the resume/recovery path:
// once a monitor has warned that its model is not loaded, a later tick that finds
// the model loaded again must log the resume line exactly once and clear the
// warn-once latch. IsModelActive is false so the tick returns before ProcessData,
// exercising the resume block without driving the full inference pipeline.
func TestProcessMonitorTick_ResumesWhenModelReloads(t *testing.T) {
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

	var logBuf bytes.Buffer
	// loaded=false on tick 1 (model unloaded), true afterwards. active=false so the
	// resume block is reached but ProcessData is never dispatched.
	state := &scriptedModelState{loaded: []bool{false, true}, active: false}
	bm := &BufferManager{
		bn:        state,
		bufferMgr: mgr,
		logger:    logger.NewSlogLogger(&logBuf, logger.LogLevelDebug, time.UTC),
	}

	cfg := &monitorConfig{sourceID: sourceID, modelID: modelID, readSize: readSize}
	quit := make(chan struct{})
	tickState := &monitorTickState{}

	// Tick 1: model not loaded -> warn once, latch set, no dispatch.
	require.NoError(t, ab.Write(make([]byte, readSize)))
	require.True(t, bm.processMonitorTick(quit, cfg, readSize, 0, tickState, 1))
	assert.Contains(t, logBuf.String(), "model not loaded, skipping inference window")
	assert.True(t, tickState.notLoadedWarned, "the warn-once latch is set after the first skip")

	// Tick 2: model came back -> resume log fires once and the latch resets.
	logBuf.Reset()
	require.NoError(t, ab.Write(make([]byte, readSize)))
	require.True(t, bm.processMonitorTick(quit, cfg, readSize, 0, tickState, 2))
	assert.Contains(t, logBuf.String(), "model loaded again, resuming inference",
		"the resume log fires when the model reloads")
	assert.False(t, tickState.notLoadedWarned, "the warn-once latch resets on resume")
	assert.NotContains(t, logBuf.String(), "buffer monitor dispatching to ProcessData",
		"an inactive model must not dispatch to ProcessData")

	// Tick 3: still loaded and inactive, latch already clear -> no repeated resume log.
	logBuf.Reset()
	require.NoError(t, ab.Write(make([]byte, readSize)))
	require.True(t, bm.processMonitorTick(quit, cfg, readSize, 0, tickState, 3))
	assert.NotContains(t, logBuf.String(), "model loaded again, resuming inference",
		"the resume log fires once, not on every subsequent tick")
}

// TestProcessMonitorTick_ToleratesBufferAllocationRace verifies the startup grace
// that keeps a monitor alive while its analysis buffer is still being allocated. The
// stream-reset callback starts a monitor the moment StartStream fires, before
// registerConsumersForSources allocates the source's analysis buffers, so a monitor
// must poll through a bounded grace instead of dying on the first missing-buffer tick
// (which would leave a legitimate target with no monitor: a silent analysis stop).
func TestProcessMonitorTick_ToleratesBufferAllocationRace(t *testing.T) {
	t.Parallel()

	const (
		sourceID = "src"
		modelID  = "BirdNET_V2.4"
		readSize = 480
	)

	// A buffer manager with NO analysis buffer allocated yet for (sourceID, modelID).
	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	bm := &BufferManager{
		bn:        &classifier.Orchestrator{},
		bufferMgr: mgr,
		logger:    logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC),
	}
	cfg := &monitorConfig{sourceID: sourceID, modelID: modelID, readSize: readSize}
	quit := make(chan struct{})
	state := &monitorTickState{}

	// While the buffer is missing and has never been read, the monitor keeps running
	// for the whole grace window instead of stopping.
	for tick := int64(1); tick <= bufferAllocGraceTicks; tick++ {
		require.True(t, bm.processMonitorTick(quit, cfg, readSize, 0, state, tick),
			"monitor must keep polling while its buffer is still being allocated (tick %d)", tick)
	}
	require.False(t, state.hasReadBuffer, "no buffer has been read yet")

	// The buffer now appears (registerConsumersForSources finished) and receives a
	// full analysis window: the monitor reads it and continues, and the not-found
	// counter resets. IsModelLoaded is false on the empty orchestrator, so the tick
	// stops before inference; the point is only that the monitor survived the race.
	require.NoError(t, mgr.AllocateAnalysis(sourceID, modelID, readSize*2, 0, readSize))
	ab, err := mgr.AnalysisBuffer(sourceID, modelID)
	require.NoError(t, err)
	require.NoError(t, ab.Write(make([]byte, readSize)))
	require.True(t, bm.processMonitorTick(quit, cfg, readSize, 0, state, bufferAllocGraceTicks+1),
		"monitor must survive once its buffer is allocated")
	assert.True(t, state.hasReadBuffer, "buffer has now been read")
	assert.Zero(t, state.notFoundTicks, "the not-found counter resets once the buffer reads")
}

// TestProcessMonitorTick_StopsWhenBufferNeverAllocated verifies the other side of the
// grace: a monitor started for a model that is not a target for the source (its buffer
// is never allocated) stops once the grace elapses rather than polling forever.
func TestProcessMonitorTick_StopsWhenBufferNeverAllocated(t *testing.T) {
	t.Parallel()

	const (
		sourceID = "src"
		modelID  = "Perch_V2"
		readSize = 480
	)

	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	var logBuf bytes.Buffer
	bm := &BufferManager{
		bn:        &classifier.Orchestrator{},
		bufferMgr: mgr,
		logger:    logger.NewSlogLogger(&logBuf, logger.LogLevelDebug, time.UTC),
	}
	cfg := &monitorConfig{sourceID: sourceID, modelID: modelID, readSize: readSize}
	quit := make(chan struct{})
	state := &monitorTickState{}

	// Every tick within the grace keeps the monitor running.
	for tick := int64(1); tick <= bufferAllocGraceTicks; tick++ {
		require.True(t, bm.processMonitorTick(quit, cfg, readSize, 0, state, tick))
	}
	// The tick past the grace stops the monitor and warns once.
	require.False(t, bm.processMonitorTick(quit, cfg, readSize, 0, state, bufferAllocGraceTicks+1),
		"monitor must stop once the allocation grace elapses without a buffer")
	assert.Contains(t, logBuf.String(), "analysis buffer not found for monitor after allocation grace, stopping")
}

// TestProcessMonitorTick_ToleratesReconfigureBufferReallocation guards the fix for the
// kept-source reconfigure race: ReconfigureSource deallocates the analysis buffer and
// registerConsumersForSources reallocates it a few steps later. A monitor that had
// already read the old buffer must poll through the grace across that gap and resume on
// the reallocated buffer, not stop and get silently dropped.
func TestProcessMonitorTick_ToleratesReconfigureBufferReallocation(t *testing.T) {
	t.Parallel()

	const (
		sourceID = "src"
		modelID  = "BirdNET_V2.4"
		readSize = 480
	)

	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	require.NoError(t, mgr.AllocateAnalysis(sourceID, modelID, readSize*2, 0, readSize))
	ab, err := mgr.AnalysisBuffer(sourceID, modelID)
	require.NoError(t, err)
	require.NoError(t, ab.Write(make([]byte, readSize)))

	bm := &BufferManager{
		bn:        &classifier.Orchestrator{},
		bufferMgr: mgr,
		logger:    logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC),
	}
	cfg := &monitorConfig{sourceID: sourceID, modelID: modelID, readSize: readSize}
	quit := make(chan struct{})
	state := &monitorTickState{}

	// Tick 1: the buffer exists and is read once (hasReadBuffer flips to true).
	require.True(t, bm.processMonitorTick(quit, cfg, readSize, 0, state, 1))
	require.True(t, state.hasReadBuffer, "the buffer was read once")

	// ReconfigureSource deallocates the analysis buffer.
	mgr.DeallocateSource(sourceID)

	// The monitor keeps polling across the whole reallocation window instead of
	// stopping, even though it had already read the buffer.
	for tick := int64(2); tick <= bufferAllocGraceTicks+1; tick++ {
		require.True(t, bm.processMonitorTick(quit, cfg, readSize, 0, state, tick),
			"a monitor whose buffer a reconfigure deallocated must keep polling (tick %d)", tick)
	}

	// registerConsumersForSources reallocates the buffer; the monitor resumes and the
	// grace counter resets.
	require.NoError(t, mgr.AllocateAnalysis(sourceID, modelID, readSize*2, 0, readSize))
	ab2, err := mgr.AnalysisBuffer(sourceID, modelID)
	require.NoError(t, err)
	require.NoError(t, ab2.Write(make([]byte, readSize)))
	require.True(t, bm.processMonitorTick(quit, cfg, readSize, 0, state, bufferAllocGraceTicks+2),
		"the monitor must resume once the buffer is reallocated")
	assert.Zero(t, state.notFoundTicks, "the grace counter resets once the buffer is found again")
}

// TestProcessMonitorTick_StopsWhenRemovedBufferNeverReturns verifies the safety net: if
// a monitor's buffer is removed after a read and never reallocated (and its quit channel
// is never closed either), the monitor stops once the grace elapses rather than polling
// forever. In production a genuine removal closes the quit channel and analysisBufferMonitor's
// outer select exits first; this covers the buffer-map path directly.
func TestProcessMonitorTick_StopsWhenRemovedBufferNeverReturns(t *testing.T) {
	t.Parallel()

	const (
		sourceID = "src"
		modelID  = "BirdNET_V2.4"
		readSize = 480
	)

	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	require.NoError(t, mgr.AllocateAnalysis(sourceID, modelID, readSize*2, 0, readSize))
	ab, err := mgr.AnalysisBuffer(sourceID, modelID)
	require.NoError(t, err)
	require.NoError(t, ab.Write(make([]byte, readSize)))

	var logBuf bytes.Buffer
	bm := &BufferManager{
		bn:        &classifier.Orchestrator{},
		bufferMgr: mgr,
		logger:    logger.NewSlogLogger(&logBuf, logger.LogLevelDebug, time.UTC),
	}
	cfg := &monitorConfig{sourceID: sourceID, modelID: modelID, readSize: readSize}
	quit := make(chan struct{})
	state := &monitorTickState{}

	// Tick 1: read once, then the buffer is deallocated and never comes back.
	require.True(t, bm.processMonitorTick(quit, cfg, readSize, 0, state, 1))
	require.True(t, state.hasReadBuffer)
	mgr.DeallocateSource(sourceID)

	// Every tick within the grace keeps the monitor polling.
	for tick := int64(2); tick <= bufferAllocGraceTicks+1; tick++ {
		require.True(t, bm.processMonitorTick(quit, cfg, readSize, 0, state, tick))
	}
	// The tick past the grace stops the monitor, logging the removed-not-reallocated case.
	logBuf.Reset()
	require.False(t, bm.processMonitorTick(quit, cfg, readSize, 0, state, bufferAllocGraceTicks+2),
		"a removed buffer that never returns must stop the monitor once the grace elapses")
	assert.Contains(t, logBuf.String(), "analysis buffer removed and not reallocated within grace, stopping monitor")
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

// timingTestBuffer allocates an analysis buffer for the window timing tests.
func timingTestBuffer(t *testing.T, capacity, readSize int) (*buffer.Manager, *buffer.AnalysisBuffer) {
	t.Helper()
	const (
		sourceID = "timing-src"
		modelID  = "timing-model"
	)
	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	require.NoError(t, mgr.AllocateAnalysis(sourceID, modelID, capacity, 0, readSize))
	ab, err := mgr.AnalysisBuffer(sourceID, modelID)
	require.NoError(t, err)
	return mgr, ab
}

func readWindow(t *testing.T, ab *buffer.AnalysisBuffer) {
	t.Helper()
	w, release, err := ab.Read()
	require.NoError(t, err)
	require.NotNil(t, w)
	release()
}

// TestMonitorTickState_WindowTimingSubtractsBacklog pins AudioEndAt: the time of
// the last write minus the duration of the audio still unread in the ring.
func TestMonitorTickState_WindowTimingSubtractsBacklog(t *testing.T) {
	t.Parallel()
	const (
		readSize       = 1000
		bytesPerSecond = 2000
	)
	_, ab := timingTestBuffer(t, readSize*10, readSize)
	for range 3 {
		require.NoError(t, ab.Write(make([]byte, readSize)))
	}
	readWindow(t, ab)
	_, lastWriteAt := ab.Backlog()

	state := &monitorTickState{}
	audioEnd, _ := state.windowTiming(ab, bytesPerSecond)
	// Two of three chunks (2000 bytes = 1 s) are still unread.
	assert.Equal(t, lastWriteAt.Add(-time.Second), audioEnd)
}

// TestMonitorTickState_WindowTimingGapFlag pins when AudioGap is raised: the
// first window, an inactive-model skip, overwrite loss and buffer reallocation.
func TestMonitorTickState_WindowTimingGapFlag(t *testing.T) {
	t.Parallel()
	const (
		readSize       = 1000
		bytesPerSecond = 2000
	)
	mgr, ab := timingTestBuffer(t, readSize*2, readSize)
	state := &monitorTickState{}

	next := func(buf *buffer.AnalysisBuffer) bool {
		t.Helper()
		require.NoError(t, buf.Write(make([]byte, readSize)))
		readWindow(t, buf)
		_, gap := state.windowTiming(buf, bytesPerSecond)
		return gap
	}

	assert.True(t, next(ab), "first window of a buffer")
	assert.False(t, next(ab), "contiguous window")

	require.NoError(t, ab.Write(make([]byte, readSize)))
	readWindow(t, ab)
	_, _ = state.windowTiming(ab, bytesPerSecond)
	state.markWindowSkipped()
	assert.True(t, next(ab), "a window read but not analyzed precedes this one")
	assert.False(t, next(ab), "the skip is cleared once a window is dispatched")

	// Overwrite loss: fill the 2-chunk ring beyond capacity before the next read.
	for range 3 {
		require.NoError(t, ab.Write(make([]byte, readSize)))
	}
	readWindow(t, ab)
	_, gap := state.windowTiming(ab, bytesPerSecond)
	assert.True(t, gap, "overwritten audio before this window")
	assert.False(t, next(ab))

	mgr.DeallocateAnalysis("timing-src", "timing-model")
	require.NoError(t, mgr.AllocateAnalysis("timing-src", "timing-model", readSize*2, 0, readSize))
	ab2, err := mgr.AnalysisBuffer("timing-src", "timing-model")
	require.NoError(t, err)
	require.NotSame(t, ab, ab2, "reallocation must give a new buffer")
	assert.True(t, next(ab2), "reallocated buffer")
}

// dispatchBackend is a classifierBackend fake for driving processMonitorTick all
// the way through ProcessData: the model is loaded and active, and inference
// returns no results (or predictErr).
type dispatchBackend struct {
	classifierBackend
	predictErr error
}

func (dispatchBackend) IsModelLoaded(string) bool { return true }
func (dispatchBackend) IsModelActive(string) bool { return true }

func (b dispatchBackend) PredictModelTimed(context.Context, string, [][]float32) ([]datastore.Results, classifier.PredictTiming, error) {
	return nil, classifier.PredictTiming{}, b.predictErr
}

func (dispatchBackend) EffectiveBaseOverlap() time.Duration { return 0 }

func (dispatchBackend) ModelSpecFor(id string) (classifier.ModelSpec, bool) {
	info, ok := classifier.ModelRegistry[id]
	return info.Spec, ok
}

// nextQueuedResult waits for the queued result of source on the shared results queue.
func nextQueuedResult(t *testing.T, source string) classifier.Results {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case r := <-classifier.ResultsQueue:
			if r.Source.ID == source {
				return r
			}
		case <-deadline:
			require.FailNow(t, "no queued result for source "+source)
		}
	}
}

// TestProcessMonitorTick_ForwardsAudioTimingToResults pins the whole producer
// chain: the backlog-corrected audio end and the gap flag computed for a window
// reach the queued classifier.Results, and a window skipped because inference
// failed makes the next one report a gap.
func TestProcessMonitorTick_ForwardsAudioTimingToResults(t *testing.T) {
	const (
		sourceID = "timing-forward-src"
		modelID  = "BirdNET_V2.4"
	)
	spec := classifier.ModelRegistry[modelID].Spec
	window := spec.ClipSizeBytes()
	bytesPerSecond := spec.EffectiveSampleRate() * conf.BytesPerSample * conf.NumChannels

	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	require.NoError(t, mgr.AllocateAnalysis(sourceID, modelID, window*4, 0, window))
	ab, err := mgr.AnalysisBuffer(sourceID, modelID)
	require.NoError(t, err)

	backend := &dispatchBackend{}
	bm := &BufferManager{bn: backend, bufferMgr: mgr, logger: logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)}
	cfg := &monitorConfig{sourceID: sourceID, modelID: modelID, spec: spec, readSize: window}
	state := &monitorTickState{}
	quit := make(chan struct{})

	// Two windows are buffered, so one full window of audio is still unread when
	// the first is read.
	require.NoError(t, ab.Write(make([]byte, window)))
	require.NoError(t, ab.Write(make([]byte, window)))
	_, lastWriteAt := ab.Backlog()

	require.True(t, bm.processMonitorTick(quit, cfg, window, 0, state, 1))
	first := nextQueuedResult(t, sourceID)
	assert.True(t, first.AudioGap, "the first window of a buffer is a gap")
	wantEnd := lastWriteAt.Add(-time.Duration(window) * time.Second / time.Duration(bytesPerSecond))
	assert.True(t, first.AudioEndAt.Equal(wantEnd), "audio end is the last write minus the unread backlog: got %v want %v", first.AudioEndAt, wantEnd)

	require.True(t, bm.processMonitorTick(quit, cfg, window, 0, state, 2))
	second := nextQueuedResult(t, sourceID)
	assert.False(t, second.AudioGap, "a contiguous window is not a gap")
	assert.True(t, second.AudioEndAt.Equal(lastWriteAt))

	// An inference failure leaves the window unanalyzed, so the next one is a gap.
	backend.predictErr = errors.NewStd("predict failed")
	require.NoError(t, ab.Write(make([]byte, window)))
	require.True(t, bm.processMonitorTick(quit, cfg, window, 0, state, 3))
	assert.True(t, state.skippedSinceProcess, "a failed ProcessData marks the window skipped")
	backend.predictErr = nil
	require.NoError(t, ab.Write(make([]byte, window)))
	require.True(t, bm.processMonitorTick(quit, cfg, window, 0, state, 4))
	assert.True(t, nextQueuedResult(t, sourceID).AudioGap, "the window after a skipped one is a gap")
}
