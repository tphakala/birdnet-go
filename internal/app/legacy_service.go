package app

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// LegacyFunc is a blocking function that runs until the quit channel is closed.
// This is the signature that RealtimeAnalysis will be adapted to.
type LegacyFunc func(quit <-chan struct{}) error

// LegacyService wraps a blocking function as a Service.
// This is the migration seam: the existing RealtimeAnalysis() runs inside
// this wrapper while subsystems are gradually extracted into proper services.
type LegacyService struct {
	name      string
	fn        LegacyFunc
	started   atomic.Bool
	quit      chan struct{}
	closeQuit sync.Once
	errChan   chan error
	// done is closed when the legacy function has exited and its error has been captured.
	done chan struct{}
	// result stores the error from the legacy function so both ErrChan and Stop can access it.
	result error
}

// NewLegacyService creates a LegacyService that wraps a blocking function.
func NewLegacyService(name string, fn LegacyFunc) *LegacyService {
	return &LegacyService{
		name: name,
		fn:   fn,
	}
}

// Name returns the service name.
func (l *LegacyService) Name() string { return l.name }

// ErrChan returns a channel that receives the error when the legacy function exits.
// App.Wait() selects on this to detect early exit (e.g., startup failure inside
// the blocking function) without requiring a signal.
func (l *LegacyService) ErrChan() <-chan error {
	return l.errChan
}

// Start launches the blocking function in a goroutine.
func (l *LegacyService) Start(_ context.Context) error {
	l.quit = make(chan struct{})
	l.errChan = make(chan error, 1)
	l.done = make(chan struct{})
	l.started.Store(true)
	go func() {
		l.result = l.fn(l.quit)
		l.errChan <- l.result
		close(l.done)
	}()
	return nil
}

// Stop signals the blocking function to exit and waits for completion.
// Returns an error if called before Start().
func (l *LegacyService) Stop(ctx context.Context) error {
	if !l.started.Load() {
		return fmt.Errorf("service %q: Stop called before Start", l.name)
	}
	l.closeQuit.Do(func() { close(l.quit) })
	// Wait for the legacy function to finish. We use l.done instead of l.errChan
	// because ErrChan may have already been consumed by Wait() for early-exit detection.
	select {
	case <-l.done:
		return l.result
	case <-ctx.Done():
		return ctx.Err()
	}
}
