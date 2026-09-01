package inference

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
)

// ovProbeHelperEnv marks a test-binary re-invocation as the probe child
// helper; its value selects the helper's behavior.
const ovProbeHelperEnv = "GO_OVPROBE_HELPER_MODE"

// TestOVProbeHelperProcess is not a real test: it is the body of the fake
// probe child. The probe tests re-invoke the test binary with
// ovProbeHelperEnv set, and this "test" then plays the requested child role.
func TestOVProbeHelperProcess(t *testing.T) {
	mode := os.Getenv(ovProbeHelperEnv)
	if mode == "" {
		t.Skip("not a helper invocation")
	}
	switch mode {
	case "ok":
		fmt.Println("INFO some interleaved log line")
		fmt.Println("OVPROBE_DEVICE=CPU")
		fmt.Println("OVPROBE_DEVICE=GPU")
		fmt.Println("OVPROBE_OK")
		os.Exit(0)
	case "crash":
		// Stands in for the driver-stack abort: marker lines already written,
		// then the child dies without the completion marker.
		fmt.Println("OVPROBE_DEVICE=CPU")
		fmt.Fprintln(os.Stderr, "free(): invalid next size (fast)")
		os.Exit(2)
	case "no-marker":
		// Exits cleanly but never prints the completion marker (e.g. killed
		// stdout, or an unexpected code path).
		fmt.Println("something unrelated")
		os.Exit(0)
	case "hang":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		os.Exit(3)
	}
}

// fakeOVProbeChild points the probe's command seam at this test binary running
// TestOVProbeHelperProcess in the given mode, and returns a call counter.
func fakeOVProbeChild(t *testing.T, mode string) *int {
	t.Helper()
	calls := new(int)
	origNew := ovProbeNewCommand
	origExe := ovProbeExecutable
	ovProbeExecutable = os.Executable
	ovProbeNewCommand = func(ctx context.Context, exe, _ string) *exec.Cmd {
		*calls++
		cmd := exec.CommandContext(ctx, exe, "-test.run=TestOVProbeHelperProcess", "-test.v=false")
		cmd.Env = append(os.Environ(), ovProbeHelperEnv+"="+mode)
		return cmd
	}
	t.Cleanup(func() {
		ovProbeNewCommand = origNew
		ovProbeExecutable = origExe
		resetOVProbeForTest()
	})
	resetOVProbeForTest()
	return calls
}

// resetOVProbeForTest clears the per-process probe cache between tests.
func resetOVProbeForTest() {
	ovProbe.mu.Lock()
	defer ovProbe.mu.Unlock()
	ovProbe.done = false
	ovProbe.libraryPath = ""
	ovProbe.devices = nil
	ovProbe.err = nil
}

func TestParseOVProbeOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		input       string
		wantDevices []string
		wantOK      bool
	}{
		{
			name:        "devices with interleaved logging",
			input:       "INFO starting up\nOVPROBE_DEVICE=CPU\nnoise\nOVPROBE_DEVICE=GPU\nOVPROBE_OK\n",
			wantDevices: []string{"CPU", "GPU"},
			wantOK:      true,
		},
		{
			name:        "no devices but completed",
			input:       "OVPROBE_OK\n",
			wantDevices: nil,
			wantOK:      true,
		},
		{
			name:        "truncated output without completion marker",
			input:       "OVPROBE_DEVICE=CPU\n",
			wantDevices: []string{"CPU"},
			wantOK:      false,
		},
		{
			name:        "empty device name ignored",
			input:       "OVPROBE_DEVICE=\nOVPROBE_OK\n",
			wantDevices: nil,
			wantOK:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			devices, sawOK := parseOVProbeOutput(strings.NewReader(tt.input))
			assert.Equal(t, tt.wantDevices, devices)
			assert.Equal(t, tt.wantOK, sawOK)
		})
	}
}

func TestRunOVProbeSuccess(t *testing.T) {
	fakeOVProbeChild(t, "ok")
	devices, err := runOVProbe("libopenvino_c.so")
	require.NoError(t, err)
	assert.Equal(t, []string{"CPU", "GPU"}, devices)
}

func TestRunOVProbeChildCrash(t *testing.T) {
	fakeOVProbeChild(t, "crash")
	_, err := runOVProbe("libopenvino_c.so")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "child failed")
	assert.Contains(t, err.Error(), "invalid next size")
}

func TestRunOVProbeMissingCompletionMarker(t *testing.T) {
	fakeOVProbeChild(t, "no-marker")
	_, err := runOVProbe("libopenvino_c.so")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "completion marker")
}

func TestRunOVProbeTimeout(t *testing.T) {
	fakeOVProbeChild(t, "hang")
	origTimeout := ovProbeTimeout
	ovProbeTimeout = 500 * time.Millisecond
	t.Cleanup(func() { ovProbeTimeout = origTimeout })
	_, err := runOVProbe("libopenvino_c.so")
	require.Error(t, err)
}

func TestOpenVINOProbeDevicesCachesResult(t *testing.T) {
	calls := fakeOVProbeChild(t, "ok")

	first, err1 := OpenVINOProbeDevices("libopenvino_c.so")
	second, err2 := OpenVINOProbeDevices("libopenvino_c.so")

	if !ov.Supported {
		// Without the openvino build tag the probe short-circuits before any
		// child runs; the seam proves that.
		require.Error(t, err1)
		require.Error(t, err2)
		assert.Equal(t, 0, *calls)
		return
	}
	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, *calls, "second call must be served from the cache")

	// A cached result also answers OpenVINOHasDevice without new children.
	assert.True(t, OpenVINOHasDevice("GPU"))
	assert.False(t, OpenVINOHasDevice("NPU"))
	assert.Equal(t, 1, *calls)
}

func TestOpenVINOProbeDevicesCachesFailure(t *testing.T) {
	calls := fakeOVProbeChild(t, "crash")

	_, err1 := OpenVINOProbeDevices("libopenvino_c.so")
	_, err2 := OpenVINOProbeDevices("libopenvino_c.so")

	require.Error(t, err1)
	require.Error(t, err2)
	if !ov.Supported {
		assert.Equal(t, 0, *calls)
		return
	}
	assert.Equal(t, 1, *calls, "a crashing probe child must not be re-executed")
	// A cached failure gates OpenVINOHasDevice closed.
	assert.False(t, OpenVINOHasDevice("GPU"))
}

func TestOpenVINOProbeDevicesReprobesOnLibraryPathChange(t *testing.T) {
	calls := fakeOVProbeChild(t, "ok")

	_, err1 := OpenVINOProbeDevices("libopenvino_c.so")
	_, err2 := OpenVINOProbeDevices("/opt/custom/libopenvino_c.so")

	if !ov.Supported {
		require.Error(t, err1)
		require.Error(t, err2)
		return
	}
	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.Equal(t, 2, *calls, "a changed library path must re-run the probe")
}
