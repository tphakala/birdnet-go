package openvino

import (
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	// propInferenceNumThreads is the CPU-plugin property that sets the thread count.
	propInferenceNumThreads = "INFERENCE_NUM_THREADS"
	// propGPULargeAllocations is the GPU-plugin property that allows allocations
	// above the device's default limit.
	propGPULargeAllocations = "GPU_ENABLE_LARGE_ALLOCATIONS"
	// propValueYes is the value that switches a boolean OpenVINO property on.
	propValueYes = "YES"
)

// gpuLargeAllocationsKey is the property name sent to the GPU plugin. It is a
// variable only so the hardware-gated test can send a key the plugin rejects and
// exercise the retry path; production code never assigns it.
//
//nolint:gochecknoglobals // test seam for the hardware-gated retry test
var gpuLargeAllocationsKey = propGPULargeAllocations

// largeAllocationsUnsupported is the process-wide memo that the bound OpenVINO
// library rejected the large allocations key. It is only ever set, never reset
// outside tests: the library bound by InitOV cannot change within a process.
//
//nolint:gochecknoglobals // process-wide memo: the bound library is shared by every classifier
var largeAllocationsUnsupported atomic.Bool

// GPULargeAllocationsUnsupported reports whether the bound OpenVINO library
// rejected GPU_ENABLE_LARGE_ALLOCATIONS in this process. Once true it stays true,
// and GPU compiles omit the key. It is always false without the openvino tag.
func GPULargeAllocationsUnsupported() bool { return largeAllocationsUnsupported.Load() }

// extraCompileProperty returns the optional device-specific compile property for
// the resolved device name: the thread count for the CPU, the large allocations
// key for a GPU whose model asks for it and whose library has not rejected it.
// It returns empty strings when there is none. A single pair is enough because
// the CPU thread count and the GPU key are mutually exclusive by device; a
// property for both would need a second pair in ovbind_compile.
func extraCompileProperty(device string, opts Options) (key, value string) {
	switch {
	case device == DeviceCPU && opts.Threads > 0:
		return propInferenceNumThreads, strconv.Itoa(opts.Threads)
	case IsGPUDevice(device) && opts.LargeAllocations && !largeAllocationsUnsupported.Load():
		return gpuLargeAllocationsKey, propValueYes
	default:
		return "", ""
	}
}

// largeAllocationsRejected reports whether err means the library does not know
// the large allocations key. It matches the key name, not a message, because the
// wording differs across runtimes. It refuses while the GPU fault latch is set,
// because the retry is another GPU compile.
func largeAllocationsRejected(key string, err error) bool {
	return err != nil && key == gpuLargeAllocationsKey && !gpuFault.blocked() &&
		strings.Contains(err.Error(), key)
}

// compileWithLargeAllocationsRetry compiles with the property picked for device
// and, when the library rejects the large allocations key, records that in the
// process-wide memo and compiles once more without it. compile must classify its
// failure with the GPU fault latch before returning, so a fatal error blocks the
// retry. compile is called at most twice.
func compileWithLargeAllocationsRetry(device string, opts Options, compile func(key, value string) error) error {
	key, value := extraCompileProperty(device, opts)
	err := compile(key, value)
	if !largeAllocationsRejected(key, err) {
		return err
	}
	largeAllocationsUnsupported.Store(true)
	return compile("", "")
}
