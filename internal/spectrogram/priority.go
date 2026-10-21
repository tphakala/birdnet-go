package spectrogram

import (
	"fmt"
	"runtime"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// nativeRenderNice is the nice value native renders run at on Linux, the value
// the earlier external renderer ran at through `nice -n 19`.
const nativeRenderNice = 19

// runAtLowPriority runs fn on a goroutine locked to its own OS thread, with that
// thread lowered to background priority where the platform supports it, and
// returns fn's error. A panic in fn is returned as an error.
//
// The goroutine never calls runtime.UnlockOSThread, so the runtime destroys the
// thread when the goroutine returns instead of handing it back to the pool at
// the lowered priority. The runtime also creates new threads from a template
// thread while the creating thread is locked, so no other goroutine inherits the
// priority. The caller blocks until fn is done, so nothing outlives the call.
func runAtLowPriority(fn func() error) error {
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if err := lowerThreadPriority(); err != nil {
			// The render still runs, at normal priority.
			GetLogger().Debug("Could not lower the render thread priority", logger.Error(err))
		}
		result <- callRecovering(fn)
	}()
	return <-result
}

// callRecovering calls fn and converts a panic into an error.
func callRecovering(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("native render panicked: %v", r)
		}
	}()
	return fn()
}
