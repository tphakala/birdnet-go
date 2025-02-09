package analysis

import (
	"sync"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// BufferManager handles the lifecycle of analysis buffer monitors
type BufferManager struct {
	monitors sync.Map
	bn       *birdnet.BirdNET
	quitChan chan struct{}
	wg       *sync.WaitGroup
}

// NewBufferManager creates a new buffer manager
func NewBufferManager(bn *birdnet.BirdNET, quitChan chan struct{}, wg *sync.WaitGroup) *BufferManager {
	return &BufferManager{
		bn:       bn,
		quitChan: quitChan,
		wg:       wg,
	}
}

// AddMonitor safely adds a new analysis buffer monitor for a source
func (m *BufferManager) AddMonitor(source string) {
	// Check if monitor already exists
	if _, exists := m.monitors.Load(source); exists {
		return
	}

	// Create a monitor-specific quit channel
	monitorQuit := make(chan struct{})
	m.monitors.Store(source, monitorQuit)

	// Start the monitor
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		myaudio.AnalysisBufferMonitor(m.wg, m.bn, monitorQuit, source)
	}()
}

// RemoveMonitor safely stops and removes a monitor for a source
func (m *BufferManager) RemoveMonitor(source string) {
	// Get the monitor's quit channel
	if quitChan, exists := m.monitors.Load(source); exists {
		// Signal the monitor to stop
		close(quitChan.(chan struct{}))
		// Remove from the map
		m.monitors.Delete(source)
	}
}

// RemoveAllMonitors stops all running monitors
func (m *BufferManager) RemoveAllMonitors() {
	m.monitors.Range(func(key, value interface{}) bool {
		m.RemoveMonitor(key.(string))
		return true
	})
}

// UpdateMonitors ensures monitors are running for all given sources
func (m *BufferManager) UpdateMonitors(sources []string) {
	// Track existing monitors that should be removed
	toRemove := make(map[string]bool)
	m.monitors.Range(func(key, _ interface{}) bool {
		toRemove[key.(string)] = true
		return true
	})

	// Add new monitors and mark existing ones as still needed
	for _, source := range sources {
		if source != "" {
			delete(toRemove, source)
			m.AddMonitor(source)
		}
	}

	// Remove monitors that are no longer needed
	for source := range toRemove {
		m.RemoveMonitor(source)
	}
}
