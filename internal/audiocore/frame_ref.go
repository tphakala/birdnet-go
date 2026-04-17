// Package audiocore, frame_ref.go.
// Reference-counted release primitive for pooled AudioFrame.Data buffers.
package audiocore

import "sync/atomic"

// FrameRef coordinates release of a pooled byte slice across multiple
// concurrent holders. The producer creates a FrameRef with remaining=1
// representing its own reference; each consumer that retains the frame
// increments, and every holder calls Release exactly once. The release
// closure fires when the counter transitions to zero.
//
// A nil *FrameRef is a valid no-op: Retain and Release are safe on nil.
// This lets legacy call sites (tests, non-pooled producers) omit the ref.
//
// Release is called at most once. An extra Release after the counter has
// reached zero does not re-fire the closure (counter goes negative).
type FrameRef struct {
	remaining atomic.Int32
	release   func()
}

// NewFrameRef returns a FrameRef with a single outstanding reference held
// by the caller. release is invoked exactly once, when the counter reaches
// zero. release must not be nil.
func NewFrameRef(release func()) *FrameRef {
	if release == nil {
		panic("audiocore: NewFrameRef requires non-nil release")
	}
	fr := &FrameRef{release: release}
	fr.remaining.Store(1)
	return fr
}

// Retain adds one outstanding reference. Safe on nil receiver.
func (f *FrameRef) Retain() {
	if f == nil {
		return
	}
	f.remaining.Add(1)
}

// Release removes one outstanding reference and fires the release closure
// when the counter transitions from 1 to 0. Safe on nil receiver.
func (f *FrameRef) Release() {
	if f == nil {
		return
	}
	if f.remaining.Add(-1) == 0 {
		f.release()
	}
}
