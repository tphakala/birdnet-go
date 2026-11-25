package openvino

import (
	"strings"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// ErrGPUDeviceFault is returned for every call that would touch the OpenVINO GPU
// after the process-wide GPU fault latch tripped. Its presence implies the latch
// is set and GPUFault reports a cause. The latch is never cleared; only a
// restart of the process clears it.
var ErrGPUDeviceFault = errors.NewStd("openvino: GPU device failed")

const (
	// gpuFailureStreakLimit is the number of consecutive GPU inference failures
	// (any text) that trips the latch. A successful GPU inference resets the count.
	gpuFailureStreakLimit = 3

	// gpuBlockedHint is the short operator guidance appended to every fast-path
	// error. It is what the inference failing notice shows, so it names both ways
	// out.
	gpuBlockedHint = "GPU inference is stopped until BirdNET-Go restarts; restart it or set the OpenVINO device to CPU"
)

// gpuFatalMarkers are OpenCL error names that mean the GPU context is no longer
// usable. The Intel GPU plugin reports an unrecoverable compute runtime fault as
// "[GPU] ... CL_OUT_OF_RESOURCES" and formats other OpenCL failures as
// ", error code: <n> <CL name>". Matching is a case-sensitive substring match on
// the error text. CL_OUT_OF_HOST_MEMORY and CL_MEM_OBJECT_ALLOCATION_FAILURE are
// deliberately absent: they are allocation failures that need not poison the
// context, and the failure streak covers persistent ones.
var gpuFatalMarkers = []string{
	"CL_OUT_OF_RESOURCES",
	"CL_DEVICE_NOT_AVAILABLE",
	"CL_INVALID_CONTEXT",
	"CL_INVALID_COMMAND_QUEUE",
}

// gpuGuard is the process-wide OpenVINO GPU fault latch. The GPU plugin and its
// OpenCL context are shared by every GPU classifier, so one fault poisons them
// all and any further call into the context may hang. All methods are safe for
// concurrent use.
type gpuGuard struct {
	tripped atomic.Bool
	cause   atomic.Pointer[error]
	streak  atomic.Int32
}

// gpuFault is the single process-wide latch.
//
//nolint:gochecknoglobals // process-wide latch: the GPU context is shared by every classifier
var gpuFault gpuGuard

// blocked reports whether the latch is set.
func (g *gpuGuard) blocked() bool { return g.tripped.Load() }

// trip records cause as the first cause, sets the latch and returns the sentinel
// wrapped around cause. The cause is stored before the latch so a reader that
// sees the latch always finds a cause.
func (g *gpuGuard) trip(cause error) error {
	g.cause.CompareAndSwap(nil, &cause)
	g.tripped.Store(true)
	return errors.Newf("%w: %w", ErrGPUDeviceFault, cause).Build()
}

// hasFatalMarker reports whether err's text names a fatal OpenCL error.
func hasFatalMarker(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range gpuFatalMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// inferResult classifies the outcome of one native inference step. A non-GPU
// target returns err unchanged and never trips. A nil err resets the failure
// streak. A fatal marker trips at once; any other failure counts toward the
// streak and trips at gpuFailureStreakLimit. It returns err, or the tripped
// error when the latch is set by this call.
func (g *gpuGuard) inferResult(gpu bool, err error) error {
	if !gpu {
		return err
	}
	if err == nil {
		g.streak.Store(0)
		return nil
	}
	if hasFatalMarker(err) {
		return g.trip(err)
	}
	if g.streak.Add(1) >= gpuFailureStreakLimit {
		return g.trip(err)
	}
	return err
}

// setupFailed classifies a compile, request-create or input-binding failure on
// a GPU target. It trips on a fatal marker only: an unsupported op is a normal
// initialization failure that falls back to ONNX Runtime, so setup failures never
// count toward the streak.
func (g *gpuGuard) setupFailed(gpu bool, err error) error {
	if gpu && hasFatalMarker(err) {
		return g.trip(err)
	}
	return err
}

// blockedErr is the error for a call refused because the latch is set.
func (g *gpuGuard) blockedErr() error {
	return errors.Newf("%w: %s", ErrGPUDeviceFault, gpuBlockedHint).Build()
}

// fault is the guard's view for GPUFault.
func (g *gpuGuard) fault() (faulted bool, cause error) {
	if !g.tripped.Load() {
		return false, nil
	}
	if p := g.cause.Load(); p != nil {
		return true, *p
	}
	return true, ErrGPUDeviceFault
}

// GPUFault reports whether the GPU fault latch is set and, if so, the error that
// tripped it. It never reports (true, nil). The latch is monotonic for the
// process.
func GPUFault() (faulted bool, cause error) { return gpuFault.fault() }
