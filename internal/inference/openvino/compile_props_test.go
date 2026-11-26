package openvino

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Test inputs for the compile property tests.
const (
	testThreadCount     = 4
	testThreadCountText = "4"
	// gpuKeyRejectedText mirrors the error text lastErr builds when the GPU plugin
	// does not know the large allocations key.
	gpuKeyRejectedText = "openvino: ov_core_compile_model failed: status=-1: Check 'it != m_options_map.end()' failed at runtime/plugin_config.hpp:214: Option not found: GPU_ENABLE_LARGE_ALLOCATIONS"
	gpuOtherFailText   = "openvino: ov_core_compile_model failed: status=-1: unsupported operation Foo"
	cpuThreadsRejected = "openvino: ov_core_compile_model failed: status=-1: Unsupported property INFERENCE_NUM_THREADS by CPU plugin."
)

// resetLargeAllocationsStateForTest clears the memo and the latch.
func resetLargeAllocationsStateForTest() {
	largeAllocationsUnsupported.Store(false)
	resetGPUFaultForTest()
}

type propCall struct{ key, value string }

// TestExtraCompileProperty pins which device-specific property each device class
// receives. Not parallel: it reads the process-wide memo.
func TestExtraCompileProperty(t *testing.T) {
	resetLargeAllocationsStateForTest()
	t.Cleanup(resetLargeAllocationsStateForTest)

	tests := []struct {
		name      string
		device    string
		opts      Options
		wantKey   string
		wantValue string
	}{
		{"cpu threads", DeviceCPU, Options{Threads: testThreadCount, LargeAllocations: true}, propInferenceNumThreads, testThreadCountText},
		{"cpu without threads never gets the GPU key", DeviceCPU, Options{LargeAllocations: true}, "", ""},
		{"indexed gpu gets the key and no threads", "GPU.1", Options{Threads: testThreadCount, LargeAllocations: true}, propGPULargeAllocations, propValueYes},
		{"gpu without the flag gets nothing", DeviceGPU, Options{}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, value := extraCompileProperty(tt.device, tt.opts)
			assert.Equal(t, tt.wantKey, key)
			assert.Equal(t, tt.wantValue, value)
		})
	}
}

// TestCompileWithLargeAllocationsRetry_Sequences runs scripted builds through the
// retry helper. Not parallel: it touches the process-wide memo and latch.
func TestCompileWithLargeAllocationsRetry_Sequences(t *testing.T) {
	gpuKeyCall := propCall{propGPULargeAllocations, propValueYes}
	noKeyCall := propCall{}

	// run executes one build with scripted results and returns the recorded calls.
	run := func(device string, opts Options, results ...error) ([]propCall, error) {
		var calls []propCall
		err := compileWithLargeAllocationsRetry(device, opts, func(key, value string) error {
			calls = append(calls, propCall{key, value})
			require.LessOrEqual(t, len(calls), len(results), "more compile calls than scripted")
			return results[len(calls)-1]
		})
		return calls, err
	}
	reset := func(t *testing.T) {
		t.Helper()
		resetLargeAllocationsStateForTest()
		t.Cleanup(resetLargeAllocationsStateForTest)
	}
	gpuOpts := Options{LargeAllocations: true}

	t.Run("library rejects the key and later builds omit it", func(t *testing.T) {
		reset(t)
		calls, err := run(DeviceGPU, gpuOpts, errors.NewStd(gpuKeyRejectedText), nil)
		require.NoError(t, err)
		assert.Equal(t, []propCall{gpuKeyCall, noKeyCall}, calls)
		assert.True(t, GPULargeAllocationsUnsupported())

		calls, err = run(DeviceGPU, gpuOpts, nil)
		require.NoError(t, err)
		assert.Equal(t, []propCall{noKeyCall}, calls)

		calls, err = run(DeviceCPU, Options{Threads: testThreadCount, LargeAllocations: true}, nil)
		require.NoError(t, err)
		assert.Equal(t, []propCall{{propInferenceNumThreads, testThreadCountText}}, calls)
	})

	t.Run("unrelated failure neither retries nor sets the memo", func(t *testing.T) {
		reset(t)
		failure := errors.NewStd(gpuOtherFailText)
		calls, err := run(DeviceGPU, gpuOpts, failure)
		require.ErrorIs(t, err, failure)
		assert.Equal(t, []propCall{gpuKeyCall}, calls)
		assert.False(t, GPULargeAllocationsUnsupported())

		calls, _ = run(DeviceGPU, gpuOpts, nil)
		assert.Equal(t, []propCall{gpuKeyCall}, calls)
	})

	t.Run("latch trip blocks the retry", func(t *testing.T) {
		reset(t)
		tripped := gpuFault.setupFailed(true, errors.NewStd(gpuKeyRejectedText+" CL_OUT_OF_RESOURCES"))
		calls, err := run(DeviceGPU, gpuOpts, tripped)
		require.ErrorIs(t, err, ErrGPUDeviceFault)
		assert.Len(t, calls, 1)
		assert.False(t, GPULargeAllocationsUnsupported())
	})

	t.Run("failed retry returns the retry error", func(t *testing.T) {
		reset(t)
		second := errors.NewStd(gpuOtherFailText)
		calls, err := run(DeviceGPU, gpuOpts, errors.NewStd(gpuKeyRejectedText), second)
		require.Error(t, err)
		assert.Same(t, second, err)
		assert.Equal(t, []propCall{gpuKeyCall, noKeyCall}, calls)
		assert.True(t, GPULargeAllocationsUnsupported())
	})

	t.Run("rejected thread count on the CPU is not retried", func(t *testing.T) {
		reset(t)
		calls, err := run(DeviceCPU, Options{Threads: testThreadCount}, errors.NewStd(cpuThreadsRejected))
		require.Error(t, err)
		assert.Len(t, calls, 1)
		assert.False(t, GPULargeAllocationsUnsupported())
	})
}
