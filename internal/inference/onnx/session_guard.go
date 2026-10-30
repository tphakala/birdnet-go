package onnx

import (
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// sessionRunner is the subset of *ort.DynamicAdvancedSession that the
// classifiers use, so tests can supply a fake.
type sessionRunner interface {
	Run(inputs, outputs []ort.Value) error
	Destroy() error
}

// guardedSession serialises Close against in-flight Run calls. Concurrent
// runs share the read lock; close takes the write lock, so it waits for every
// active Run and later calls see a closed session. The zero value is a closed
// session. It must not be copied after first use.
type guardedSession struct {
	mu sync.RWMutex
	s  sessionRunner
}

// isClosed reports whether the session has been closed. It is advisory: the
// authoritative check is inside run.
func (g *guardedSession) isClosed() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.s == nil
}

// run executes the session, or returns ErrSessionClosed after close.
func (g *guardedSession) run(inputs, outputs []ort.Value) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.s == nil {
		return ErrSessionClosed
	}
	return g.s.Run(inputs, outputs)
}

// close destroys the session once. Later calls return nil.
func (g *guardedSession) close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.s == nil {
		return nil
	}
	err := g.s.Destroy()
	g.s = nil
	return err
}
