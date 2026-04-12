package buffer

import (
	"fmt"
	"sync"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// defaultBytePoolSize is the size used when creating the shared BytePool.
// 4096 bytes covers a typical audio frame.
const defaultBytePoolSize = 4096

// bufferKey is the composite key used to look up analysis buffers. Each
// (sourceID, modelID) pair addresses a distinct AnalysisBuffer, allowing
// multiple models to process the same audio source concurrently.
type bufferKey struct {
	sourceID string
	modelID  string
}

// Manager coordinates the lifecycle of AnalysisBuffer and CaptureBuffer
// instances. Analysis buffers are keyed by (sourceID, modelID) to support
// multi-model analysis. Capture buffers are keyed by sourceID alone since
// capture is model-independent.
//
// All exported methods are safe for concurrent use.
type Manager struct {
	analysisBuffers map[bufferKey]*AnalysisBuffer
	captureBuffers  map[string]*CaptureBuffer
	bytePool        *BytePool
	float32Pools    map[int]*Float32Pool
	float32PoolMu   sync.Mutex // protects float32Pools; separate from mu to avoid contention
	mu              sync.RWMutex
	logger          logger.Logger
}

// NewManager creates a Manager with a shared BytePool pre-allocated at the
// default size and an empty Float32Pool map that lazily creates pools on first
// request for each size. The provided logger is forwarded to each
// AnalysisBuffer created by AllocateAnalysis.
func NewManager(log logger.Logger) *Manager {
	// Errors from the pool constructors are only possible when size <= 0, which
	// cannot happen with compile-time positive constants.
	bytePool, _ := NewBytePool(defaultBytePoolSize)

	return &Manager{
		analysisBuffers: make(map[bufferKey]*AnalysisBuffer),
		captureBuffers:  make(map[string]*CaptureBuffer),
		bytePool:        bytePool,
		float32Pools:    make(map[int]*Float32Pool),
		logger:          log,
	}
}

// AllocateAnalysis creates an AnalysisBuffer for the given (sourceID, modelID)
// pair and stores it in the Manager. Returns an error if a buffer for that
// composite key already exists or if NewAnalysisBuffer fails.
func (m *Manager) AllocateAnalysis(sourceID, modelID string, capacity, overlapSize, readSize int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := bufferKey{sourceID: sourceID, modelID: modelID}
	if _, exists := m.analysisBuffers[key]; exists {
		return fmt.Errorf("analysis buffer already allocated for source %q model %q", sourceID, modelID)
	}

	ab, err := NewAnalysisBuffer(capacity, overlapSize, readSize, sourceID, m.logger)
	if err != nil {
		return err
	}

	m.analysisBuffers[key] = ab
	return nil
}

// HasAnalysis reports whether an analysis buffer has been allocated for
// the given source and model pair. Safe for concurrent use.
func (m *Manager) HasAnalysis(sourceID, modelID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.analysisBuffers[bufferKey{sourceID: sourceID, modelID: modelID}]
	return exists
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

// DeallocateSource removes all AnalysisBuffers (across all models) and the
// CaptureBuffer for the given sourceID in a single atomic operation. It is
// safe to call for sourceIDs that were never allocated; the method is a no-op
// in that case.
func (m *Manager) DeallocateSource(sourceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key := range m.analysisBuffers {
		if key.sourceID == sourceID {
			delete(m.analysisBuffers, key)
		}
	}
	delete(m.captureBuffers, sourceID)
}

// AnalysisBuffer returns the AnalysisBuffer allocated for the given
// (sourceID, modelID) pair. Returns audiocore.ErrBufferNotFound if no buffer
// has been allocated for that composite key.
func (m *Manager) AnalysisBuffer(sourceID, modelID string) (*AnalysisBuffer, error) {
	m.mu.RLock()
	ab, ok := m.analysisBuffers[bufferKey{sourceID: sourceID, modelID: modelID}]
	m.mu.RUnlock()

	if !ok {
		return nil, audiocore.ErrBufferNotFound
	}
	return ab, nil
}

// AnalysisBuffers returns all AnalysisBuffers allocated for the given sourceID,
// keyed by modelID. Returns an empty (non-nil) map if no buffers exist for the
// source.
func (m *Manager) AnalysisBuffers(sourceID string) map[string]*AnalysisBuffer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*AnalysisBuffer)
	for key, ab := range m.analysisBuffers {
		if key.sourceID == sourceID {
			result[key.modelID] = ab
		}
	}
	return result
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

// Float32Pool returns a pool for the given buffer size, creating one lazily if
// needed. Thread-safe via a dedicated mutex separate from the Manager's main
// RWMutex.
func (m *Manager) Float32Pool(size int) *Float32Pool {
	if size <= 0 {
		return nil
	}
	m.float32PoolMu.Lock()
	defer m.float32PoolMu.Unlock()
	if pool, ok := m.float32Pools[size]; ok {
		return pool
	}
	pool, err := NewFloat32Pool(size)
	if err != nil {
		m.logger.Error("failed to create Float32Pool",
			logger.Int("size", size), logger.Error(err))
		return nil
	}
	m.float32Pools[size] = pool
	return pool
}
