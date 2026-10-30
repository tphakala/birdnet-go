package onnx

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

// errFakeDestroy is the error fakeSession.Destroy returns when destroyErr is set.
var errFakeDestroy = errors.NewStd("fake destroy failure")

// fakeSession is a sessionRunner that records how Run and Destroy interact.
//
// destroyed and sawDestroyed are plain fields on purpose: Run reads destroyed
// after it is released, so a Destroy that is not ordered against Run is a data
// race the race detector reports. Do not turn them into atomics.
type fakeSession struct {
	entered   chan struct{} // closed on the first Run
	release   chan struct{} // when non-nil, Run blocks until it is closed
	enterOnce sync.Once

	// allIn is closed once wantInside Runs are inside Run at the same time;
	// every such Run blocks until then. It is nil unless a test sets it.
	allIn      chan struct{}
	abort      chan struct{} // closed by the test cleanup so a failed test leaks no Run
	wantInside int32
	inside     atomic.Int32

	destroyErr   error
	destroyed    bool
	sawDestroyed bool
	destroys     atomic.Int32
}

func (f *fakeSession) Run(_, _ []ort.Value) error {
	if f.entered != nil {
		f.enterOnce.Do(func() { close(f.entered) })
	}
	if f.allIn != nil {
		if f.inside.Add(1) == f.wantInside {
			close(f.allIn)
		}
		select {
		case <-f.allIn:
		case <-f.abort:
		}
		// Several Runs share the fake here, so do not write sawDestroyed.
		return nil
	}
	if f.release != nil {
		<-f.release
	}
	f.sawDestroyed = f.destroyed
	return nil
}

func (f *fakeSession) Destroy() error {
	f.destroyed = true
	f.destroys.Add(1)
	return f.destroyErr
}

// goErr runs fn in a goroutine and returns a channel that receives its result.
func goErr(fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	return ch
}

// awaitErr returns the next value from ch, or fails the test after the default
// timeout so a deadlocked guard fails instead of hanging the run.
func awaitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(testutil.DefaultTestTimeout):
		require.FailNow(t, "timed out waiting for the guarded session call to return")
		return nil
	}
}
