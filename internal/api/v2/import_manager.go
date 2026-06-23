// Package api provides the HTTP API for BirdNET-Go.
package api

import (
	"context"
	"sync"

	"github.com/tphakala/birdnet-go/internal/imports"
)

// importManager ensures only one import runs at a time and retains the most
// recent job so SSE, cancel, and status endpoints keep working after completion.
type importManager struct {
	mu      sync.Mutex
	current *importJob // nil until first start; may be done
}

// newImportManager creates a new importManager.
func newImportManager() *importManager { return &importManager{} }

// start reserves the single import slot.
// Returns false if an import is already in progress.
func (m *importManager) start(job *importJob) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && !m.current.isDone() {
		return false
	}
	m.current = job
	return true
}

// get returns the job whose id matches id, or nil if no such job is current.
func (m *importManager) get(id string) *importJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && m.current.id == id {
		return m.current
	}
	return nil
}

// active returns the current job (may be done or nil) for status reporting.
func (m *importManager) active() *importJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// importJob tracks a single in-flight or completed import.
type importJob struct {
	id     string
	cancel context.CancelFunc

	mu      sync.Mutex
	stats   imports.ImportStats
	seq     uint64 // monotonic counter; used as the SSE event id
	done    bool
	runErr  error
	changed chan struct{} // closed and replaced on every update (last-value broadcast)
}

// newImportJob creates a new importJob with the given id and cancel function.
func newImportJob(id string, cancel context.CancelFunc) *importJob {
	return &importJob{id: id, cancel: cancel, changed: make(chan struct{})}
}

// Report implements imports.ProgressReporter. Called from the engine goroutine.
func (j *importJob) Report(s imports.ImportStats) {
	j.mu.Lock()
	j.stats = s
	j.seq++
	j.wakeLocked()
	j.mu.Unlock()
}

// finish records the final state and error of a completed or cancelled import.
func (j *importJob) finish(s imports.ImportStats, err error) {
	j.mu.Lock()
	j.stats = s
	j.runErr = err
	j.done = true
	j.seq++
	j.wakeLocked()
	j.mu.Unlock()
}

// wakeLocked closes the current changed channel and replaces it with a new one.
// Must be called with j.mu held.
func (j *importJob) wakeLocked() {
	close(j.changed)
	j.changed = make(chan struct{})
}

// isDone reports whether the job has reached a terminal state.
func (j *importJob) isDone() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done
}

// snapshot returns a consistent view of the current state and the wait channel
// for the next change. The caller waits on changed, then calls snapshot again.
func (j *importJob) snapshot() (stats imports.ImportStats, seq uint64, done bool, changed <-chan struct{}, runErr error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.stats, j.seq, j.done, j.changed, j.runErr
}
