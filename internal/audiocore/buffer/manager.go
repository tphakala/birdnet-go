package buffer

import (
	"fmt"
	"sync"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// defaultPoolSize is the size used when creating the shared BytePool and
// Float32Pool. 4096 bytes / 2048 float32 samples covers a typical audio frame.
const (
	defaultBytePoolSize    = 4096
	defaultFloat32PoolSize = 2048
)

// Manager coordinates the lifecycle of AnalysisBuffer and CaptureBuffer
// instances keyed by source ID. It owns one shared BytePool and Float32Pool
// that consumers of the package may use for buffer reuse.
//
// All exported methods are safe for concurrent use.
type Manager struct {
	analysisBuffers map[string]*AnalysisBuffer
	captureBuffers  map[string]*CaptureBuffer
	bytePool        *BytePool
	float32Pool     *Float32Pool
	mu              sync.RWMutex
	logger          logger.Logger
}

// NewManager creates a Manager with shared BytePool and Float32Pool
// pre-allocated at default sizes. The provided logger is forwarded to each
// AnalysisBuffer created by AllocateAnalysis.
func NewManager(log logger.Logger) *Manager {
	// Errors from the pool constructors are only possible when size <= 0, which
	// cannot happen with compile-time positive constants.
	bytePool, _ := NewBytePool(defaultBytePoolSize)
	float32Pool, _ := NewFloat32Pool(defaultFloat32PoolSize)

	return &Manager{
		analysisBuffers: make(map[string]*AnalysisBuffer),
		captureBuffers:  make(map[string]*CaptureBuffer),
		bytePool:        bytePool,
		float32Pool:     float32Pool,
		logger:          log,
	}
}

// AllocateAnalysis creates an AnalysisBuffer for the given sourceID and stores
// it in the Manager. Returns an error if a buffer for sourceID already exists
// or if NewAnalysisBuffer fails.
func (m *Manager) AllocateAnalysis(sourceID string, capacity, overlapSize, readSize int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.analysisBuffers[sourceID]; exists {
		return fmt.Errorf("analysis buffer already allocated for source %q", sourceID)
	}

	ab, err := NewAnalysisBuffer(capacity, overlapSize, readSize, sourceID, m.logger)
	if err != nil {
		return err
	}

	m.analysisBuffers[sourceID] = ab
	return nil
}

// AllocateCapture creates a CaptureBuffer for the given sourceID and stores it
// in the Manager. Returns an error if a buffer for sourceID already exists or
// if NewCaptureBuffer fails.
func (m *Manager) AllocateCapture(sourceID string, durationSeconds, sampleRate, bytesPerSample int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.captureBuffers[sourceID]; exists {
		return fmt.Errorf("capture buffer already allocated for source %q", sourceID)
	}

	cb, err := NewCaptureBuffer(durationSeconds, sampleRate, bytesPerSample, sourceID)
	if err != nil {
		return err
	}

	m.captureBuffers[sourceID] = cb
	return nil
}

// DeallocateSource removes both the AnalysisBuffer and the CaptureBuffer for
// the given sourceID in a single atomic operation. It is safe to call for
// sourceIDs that were never allocated; the method is a no-op in that case.
func (m *Manager) DeallocateSource(sourceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.analysisBuffers, sourceID)
	delete(m.captureBuffers, sourceID)
}

// AnalysisBuffer returns the AnalysisBuffer allocated for the given sourceID.
// Returns audiocore.ErrBufferNotFound if no buffer has been allocated for
// sourceID.
func (m *Manager) AnalysisBuffer(sourceID string) (*AnalysisBuffer, error) {
	m.mu.RLock()
	ab, ok := m.analysisBuffers[sourceID]
	m.mu.RUnlock()

	if !ok {
		return nil, audiocore.ErrBufferNotFound
	}
	return ab, nil
}

// CaptureBuffer returns the CaptureBuffer allocated for the given sourceID.
// Returns audiocore.ErrBufferNotFound if no buffer has been allocated for
// sourceID.
func (m *Manager) CaptureBuffer(sourceID string) (*CaptureBuffer, error) {
	m.mu.RLock()
	cb, ok := m.captureBuffers[sourceID]
	m.mu.RUnlock()

	if !ok {
		return nil, audiocore.ErrBufferNotFound
	}
	return cb, nil
}

// BytePool returns the shared BytePool owned by this Manager.
func (m *Manager) BytePool() *BytePool {
	return m.bytePool
}

// Float32Pool returns the shared Float32Pool owned by this Manager.
func (m *Manager) Float32Pool() *Float32Pool {
	return m.float32Pool
}
