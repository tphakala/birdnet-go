package audiocore

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrameRef_NilSafe(t *testing.T) {
	t.Parallel()
	var ref *FrameRef
	assert.NotPanics(t, func() { ref.Retain() })
	assert.NotPanics(t, func() { ref.Release() })
}

func TestFrameRef_ReleaseCalledOnceWhenCountHitsZero(t *testing.T) {
	t.Parallel()
	var released atomic.Int32
	ref := NewFrameRef(func() { released.Add(1) })

	ref.Retain()  // 2
	ref.Retain()  // 3
	ref.Release() // 2
	assert.EqualValues(t, 0, released.Load(), "release should not fire until count hits zero")
	ref.Release() // 1
	ref.Release() // 0 -> fires
	assert.EqualValues(t, 1, released.Load(), "release should fire exactly once")
}

func TestFrameRef_ExtraReleaseDoesNotDoubleFire(t *testing.T) {
	t.Parallel()
	var released atomic.Int32
	ref := NewFrameRef(func() { released.Add(1) })
	ref.Release() // 0 -> fires
	ref.Release() // -1 -> must NOT fire
	assert.EqualValues(t, 1, released.Load(), "release must fire exactly once even with extra decrements")
}

func TestFrameRef_ConcurrentRetainRelease(t *testing.T) {
	t.Parallel()
	var released atomic.Int32
	ref := NewFrameRef(func() { released.Add(1) })

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			ref.Retain()
			ref.Release()
		}()
	}
	wg.Wait()
	ref.Release()
	require.EqualValues(t, 1, released.Load())
}

func TestAudioFrame_RefIsOptional(t *testing.T) {
	t.Parallel()
	// Zero-value AudioFrame with nil Ref must work. Many tests construct
	// frames this way.
	frame := AudioFrame{Data: []byte{1, 2, 3}}
	assert.Nil(t, frame.Ref)
	assert.NotPanics(t, func() { frame.Ref.Release() })
}

func TestNewFrameRef_NilReleasePanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { _ = NewFrameRef(nil) })
}
