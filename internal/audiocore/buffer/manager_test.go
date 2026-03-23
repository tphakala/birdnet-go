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
)

// TestBufferManager_AllocateAnalysis verifies that an analysis buffer can be
// allocated and retrieved by sourceID.
func TestBufferManager_AllocateAnalysis(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	err := m.AllocateAnalysis("source-1", managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.NoError(t, err)

	ab, err := m.AnalysisBuffer("source-1")
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

	err := m.AllocateAnalysis("source-3", managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.NoError(t, err)

	err = m.AllocateCapture("source-3", managerTestDurationSeconds, managerTestSampleRate, managerTestBytesPerSample)
	require.NoError(t, err)

	m.DeallocateSource("source-3")

	ab, err := m.AnalysisBuffer("source-3")
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
// capture buffer for the same sourceID returns an error.
func TestBufferManager_DoubleAllocate(t *testing.T) {
	t.Parallel()

	m := buffer.NewManager(newTestLogger())

	// First allocations should succeed.
	err := m.AllocateAnalysis("source-4", managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
	require.NoError(t, err)

	err = m.AllocateCapture("source-4", managerTestDurationSeconds, managerTestSampleRate, managerTestBytesPerSample)
	require.NoError(t, err)

	// Second allocations for the same sourceID must return an error.
	err = m.AllocateAnalysis("source-4", managerTestCapacity, managerTestOverlapSize, managerTestReadSize)
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
	assert.NotNil(t, m.Float32Pool())
}
