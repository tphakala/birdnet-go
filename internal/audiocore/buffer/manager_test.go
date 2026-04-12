package buffer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
)

const (
	managerTestCapacity    = 64 * 1024
	managerTestOverlapSize = 512
	managerTestReadSize    = 1024

	managerTestDurationSeconds = 60
	managerTestSampleRate      = 48000
	managerTestBytesPerSample  = 2

	// managerTestModelID is the default model identifier used in single-model
	// tests where a concrete model name is not relevant.
	managerTestModelID = "birdnet-v2.4"
)

// TestBufferManager_AllocateAnalysis verifies that an analysis buffer can be
// allocated and retrieved by (sourceID, modelID).
func TestBufferManager_AllocateAnalysis(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	err := m.AllocateAnalysis("source-1", managerTestModelID, managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.NoError(t, err)

	ab, err := m.AnalysisBuffer("source-1", managerTestModelID)
	require.NoError(t, err)
	assert.NotNil(t, ab)
}

// TestBufferManager_AllocateCapture verifies that a capture buffer can be
// allocated and retrieved by sourceID.
func TestBufferManager_AllocateCapture(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	err := m.AllocateCapture("source-2", managerTestDurationSeconds, managerTestSampleRate, managerTestBytesPerSample)
	require.NoError(t, err)

	cb, err := m.CaptureBuffer("source-2")
	require.NoError(t, err)
	assert.NotNil(t, cb)
}

// TestBufferManager_DeallocateSource verifies that both analysis and capture
// buffers for a source are removed atomically after DeallocateSource.
func TestBufferManager_DeallocateSource(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	err := m.AllocateAnalysis("source-3", managerTestModelID, managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.NoError(t, err)

	err = m.AllocateCapture("source-3", managerTestDurationSeconds, managerTestSampleRate, managerTestBytesPerSample)
	require.NoError(t, err)

	m.DeallocateSource("source-3")

	ab, err := m.AnalysisBuffer("source-3", managerTestModelID)
	require.ErrorIs(t, err, audiocore.ErrBufferNotFound)
	assert.Nil(t, ab)

	cb, err := m.CaptureBuffer("source-3")
	require.ErrorIs(t, err, audiocore.ErrBufferNotFound)
	assert.Nil(t, cb)
}

// TestBufferManager_DeallocateNonExistent verifies that calling DeallocateSource
// for an unknown sourceID does not panic.
func TestBufferManager_DeallocateNonExistent(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	assert.NotPanics(t, func() {
		m.DeallocateSource("does-not-exist")
	})
}

// TestBufferManager_DoubleAllocate verifies that allocating a second analysis or
// capture buffer for the same (sourceID, modelID) returns an error.
func TestBufferManager_DoubleAllocate(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	// First allocations should succeed.
	err := m.AllocateAnalysis("source-4", managerTestModelID, managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.NoError(t, err)

	err = m.AllocateCapture("source-4", managerTestDurationSeconds, managerTestSampleRate, managerTestBytesPerSample)
	require.NoError(t, err)

	// Second allocations for the same (sourceID, modelID) must return an error.
	err = m.AllocateAnalysis("source-4", managerTestModelID, managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.Error(t, err)

	err = m.AllocateCapture("source-4", managerTestDurationSeconds, managerTestSampleRate, managerTestBytesPerSample)
	require.Error(t, err)
}

// TestBufferManager_PoolAccessors verifies that the shared pools are non-nil
// after construction.
func TestBufferManager_PoolAccessors(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	assert.NotNil(t, m.BytePool())
	assert.NotNil(t, m.Float32Pool(2048))
}

// ---------------------------------------------------------------------------
// Multi-model composite-key tests
// ---------------------------------------------------------------------------

// TestManager_AllocateAnalysis_MultiModel allocates two models for the same
// source and verifies that distinct buffers are returned via AnalysisBuffer.
func TestManager_AllocateAnalysis_MultiModel(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	const source = "mic1"

	err := m.AllocateAnalysis(source, "birdnet-v2.4", managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.NoError(t, err)

	err = m.AllocateAnalysis(source, "perch-v2", managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.NoError(t, err)

	ab1, err := m.AnalysisBuffer(source, "birdnet-v2.4")
	require.NoError(t, err)
	require.NotNil(t, ab1)

	ab2, err := m.AnalysisBuffer(source, "perch-v2")
	require.NoError(t, err)
	require.NotNil(t, ab2)

	// The two buffers must be distinct instances.
	assert.NotSame(t, ab1, ab2, "buffers for different models must be distinct instances")
}

// TestManager_AllocateAnalysis_DuplicateModelError verifies that allocating the
// same (source, model) pair twice returns an error.
func TestManager_AllocateAnalysis_DuplicateModelError(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	const source = "mic1"
	const model = "birdnet-v2.4"

	err := m.AllocateAnalysis(source, model, managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.NoError(t, err)

	err = m.AllocateAnalysis(source, model, managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.Error(t, err)
	assert.Contains(t, err.Error(), source)
	assert.Contains(t, err.Error(), model)
}

// TestManager_DeallocateSource_RemovesAllModels allocates two models and a
// capture buffer for a source, deallocates the source, then verifies all
// buffers are gone.
func TestManager_DeallocateSource_RemovesAllModels(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	const source = "mic1"

	require.NoError(t, m.AllocateAnalysis(source, "birdnet-v2.4", managerTestCapacity, managerTestOverlapSize, managerTestReadSize))
	require.NoError(t, m.AllocateAnalysis(source, "perch-v2", managerTestCapacity, managerTestOverlapSize, managerTestReadSize))
	require.NoError(t, m.AllocateCapture(source, managerTestDurationSeconds, managerTestSampleRate, managerTestBytesPerSample))

	m.DeallocateSource(source)

	_, err := m.AnalysisBuffer(source, "birdnet-v2.4")
	require.ErrorIs(t, err, audiocore.ErrBufferNotFound)

	_, err = m.AnalysisBuffer(source, "perch-v2")
	require.ErrorIs(t, err, audiocore.ErrBufferNotFound)

	_, err = m.CaptureBuffer(source)
	require.ErrorIs(t, err, audiocore.ErrBufferNotFound)

	// AnalysisBuffers should return an empty map.
	assert.Empty(t, m.AnalysisBuffers(source))
}

// TestManager_AnalysisBuffers_ReturnsAllForSource allocates two models for
// "mic1" and one model for "mic2", then verifies that AnalysisBuffers("mic1")
// returns a map with exactly 2 entries and correct model keys.
func TestManager_AnalysisBuffers_ReturnsAllForSource(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	require.NoError(t, m.AllocateAnalysis("mic1", "birdnet-v2.4", managerTestCapacity, managerTestOverlapSize, managerTestReadSize))
	require.NoError(t, m.AllocateAnalysis("mic1", "perch-v2", managerTestCapacity, managerTestOverlapSize, managerTestReadSize))
	require.NoError(t, m.AllocateAnalysis("mic2", "birdnet-v2.4", managerTestCapacity, managerTestOverlapSize, managerTestReadSize))

	mic1Buffers := m.AnalysisBuffers("mic1")
	assert.Len(t, mic1Buffers, 2, "mic1 should have 2 analysis buffers")
	assert.Contains(t, mic1Buffers, "birdnet-v2.4")
	assert.Contains(t, mic1Buffers, "perch-v2")

	mic2Buffers := m.AnalysisBuffers("mic2")
	assert.Len(t, mic2Buffers, 1, "mic2 should have 1 analysis buffer")
	assert.Contains(t, mic2Buffers, "birdnet-v2.4")

	// Non-existent source should return empty map.
	assert.Empty(t, m.AnalysisBuffers("mic3"))
}

// ---------------------------------------------------------------------------
// HasAnalysis tests
// ---------------------------------------------------------------------------

// TestManager_HasAnalysis verifies that HasAnalysis correctly reports whether
// an analysis buffer has been allocated for a given (sourceID, modelID) pair.
func TestManager_HasAnalysis(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	// Before allocation — should return false.
	assert.False(t, m.HasAnalysis("source-1", "birdnet"))

	// Allocate and check — should return true.
	err := m.AllocateAnalysis("source-1", "birdnet", managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.NoError(t, err)
	assert.True(t, m.HasAnalysis("source-1", "birdnet"))

	// Different model — should return false.
	assert.False(t, m.HasAnalysis("source-1", "perch_v2"))

	// Different source — should return false.
	assert.False(t, m.HasAnalysis("source-2", "birdnet"))
}

// ---------------------------------------------------------------------------
// Per-size Float32Pool tests
// ---------------------------------------------------------------------------

// TestManager_Float32Pool_LazySizes verifies that Float32Pool returns distinct
// pools for different sizes and the same pool for the same size.
func TestManager_Float32Pool_LazySizes(t *testing.T) {
	t.Parallel()
	m := buffer.NewManager(newTestLogger())

	// Request two different sizes.
	pool1 := m.Float32Pool(144384)
	require.NotNil(t, pool1)

	pool2 := m.Float32Pool(160000)
	require.NotNil(t, pool2)

	assert.NotSame(t, pool1, pool2, "different sizes must have distinct pools")

	// Same size returns same pool.
	pool1Again := m.Float32Pool(144384)
	assert.Same(t, pool1, pool1Again, "same size must return same pool")
}
