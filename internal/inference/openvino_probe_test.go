package inference

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

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
// TestOVProbeHelperProcess in the given mode, and returns a call counter. The
// counter is guarded because concurrent callers may launch children.
func fakeOVProbeChild(t *testing.T, mode string) *ovProbeCallCounter {
	t.Helper()
	calls := &ovProbeCallCounter{}
	origNew := ovProbeNewCommand
	origExe := ovProbeExecutable
	ovProbeExecutable = os.Executable
	ovProbeNewCommand = func(ctx context.Context, exe, _ string) *exec.Cmd {
		calls.inc()
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

// ovProbeCallCounter counts fake child launches across goroutines.
type ovProbeCallCounter struct {
	mu sync.Mutex
	n  int
}

func (c *ovProbeCallCounter) inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
}

func (c *ovProbeCallCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// resetOVProbeForTest clears the per-process probe cache between tests.
func resetOVProbeForTest() {
	ovProbe.mu.Lock()
	defer ovProbe.mu.Unlock()
	ovProbe.results = map[string]ovProbeResult{}
	ovProbe.inFlight = map[string]chan struct{}{}
	ovProbe.lastPath = ""
}

// shortenOVProbeTimeout lowers the probe timeout for the test's lifetime.
func shortenOVProbeTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := ovProbeTimeout
	ovProbeTimeout = d
	t.Cleanup(func() { ovProbeTimeout = orig })
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
			devices, sawOK, err := parseOVProbeOutput(strings.NewReader(tt.input))
			require.NoError(t, err)
			assert.Equal(t, tt.wantDevices, devices)
			assert.Equal(t, tt.wantOK, sawOK)
		})
	}
}

func TestParseOVProbeOutputSurfacesReadErrors(t *testing.T) {
	t.Parallel()
	// A single line longer than the scanner's maximum must be reported, not
	// silently mistaken for a probe that never printed its completion marker.
	huge := strings.Repeat("x", ovProbeMaxLineBytes+1) + "\nOVPROBE_OK\n"
	_, sawOK, err := parseOVProbeOutput(strings.NewReader(huge))
	require.Error(t, err)
	assert.False(t, sawOK)
}

func TestRunOVProbeSuccess(t *testing.T) {
	fakeOVProbeChild(t, "ok")
	devices, cacheable, err := runOVProbe("libopenvino_c.so")
	require.NoError(t, err)
	assert.False(t, cacheable)
	assert.Equal(t, []string{"CPU", "GPU"}, devices)
}

func TestRunOVProbeChildCrashIsCacheable(t *testing.T) {
	fakeOVProbeChild(t, "crash")
	_, cacheable, err := runOVProbe("libopenvino_c.so")
	require.Error(t, err)
	assert.True(t, cacheable, "a child verdict must be remembered")
	assert.Contains(t, err.Error(), "child failed")
	assert.Contains(t, err.Error(), "invalid next size")
}

func TestRunOVProbeMissingCompletionMarkerIsCacheable(t *testing.T) {
	fakeOVProbeChild(t, "no-marker")
	_, cacheable, err := runOVProbe("libopenvino_c.so")
	require.Error(t, err)
	assert.True(t, cacheable)
	assert.Contains(t, err.Error(), "completion marker")
}

func TestRunOVProbeTimeoutIsTransient(t *testing.T) {
	fakeOVProbeChild(t, "hang")
	shortenOVProbeTimeout(t, 500*time.Millisecond)
	_, cacheable, err := runOVProbe("libopenvino_c.so")
	require.Error(t, err)
	assert.False(t, cacheable, "a timeout is not a child verdict and must not be cached")
	assert.Contains(t, err.Error(), "timed out")
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
		assert.Equal(t, 0, calls.count())
		return
	}
	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, calls.count(), "second call must be served from the cache")

	// A cached result also answers OpenVINOHasDevice without new children.
	assert.True(t, OpenVINOHasDevice("GPU"))
	assert.False(t, OpenVINOHasDevice("NPU"))
	assert.Equal(t, 1, calls.count())
}

func TestOpenVINOProbeDevicesCachesChildFailure(t *testing.T) {
	calls := fakeOVProbeChild(t, "crash")

	_, err1 := OpenVINOProbeDevices("libopenvino_c.so")
	_, err2 := OpenVINOProbeDevices("libopenvino_c.so")

	require.Error(t, err1)
	require.Error(t, err2)
	if !ov.Supported {
		assert.Equal(t, 0, calls.count())
		return
	}
	assert.Equal(t, 1, calls.count(), "a crashing probe child must not be re-executed")
	// A cached failure gates OpenVINOHasDevice closed.
	assert.False(t, OpenVINOHasDevice("GPU"))
}

func TestOpenVINOProbeDevicesDoesNotCacheTimeout(t *testing.T) {
	calls := fakeOVProbeChild(t, "hang")
	shortenOVProbeTimeout(t, 300*time.Millisecond)

	_, err1 := OpenVINOProbeDevices("libopenvino_c.so")
	_, err2 := OpenVINOProbeDevices("libopenvino_c.so")

	require.Error(t, err1)
	require.Error(t, err2)
	if !ov.Supported {
		assert.Equal(t, 0, calls.count())
		return
	}
	assert.Equal(t, 2, calls.count(), "a transient timeout must be retried on the next call")
	// Nothing cached, so readers see the pre-probe state rather than a verdict.
	_, status, _ := cachedOVProbeDevices()
	assert.Equal(t, ovProbeNotRun, status)
}

func TestOpenVINOProbeDevicesCachesPerLibraryPath(t *testing.T) {
	calls := fakeOVProbeChild(t, "ok")

	_, errA := OpenVINOProbeDevices("libopenvino_c.so")
	_, errB := OpenVINOProbeDevices("/opt/custom/libopenvino_c.so")
	_, errA2 := OpenVINOProbeDevices("libopenvino_c.so")

	if !ov.Supported {
		require.Error(t, errA)
		require.Error(t, errB)
		require.Error(t, errA2)
		return
	}
	require.NoError(t, errA)
	require.NoError(t, errB)
	require.NoError(t, errA2)
	assert.Equal(t, 2, calls.count(), "A -> B -> A must probe each path once")
}

func TestOpenVINOHasDeviceDoesNotBlockOnInFlightProbe(t *testing.T) {
	if !ov.Supported {
		t.Skip("probe short-circuits without the openvino build tag")
	}
	calls := fakeOVProbeChild(t, "hang")
	shortenOVProbeTimeout(t, 2*time.Second)

	// If an assertion below fails before <-done, the probe goroutine (and its
	// child) would outlive the test; make that a reported leak, not a silent one.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = OpenVINOProbeDevices("libopenvino_c.so")
	}()
	// Wait for the goroutine to register its in-flight probe.
	require.Eventually(t, func() bool {
		_, status, _ := cachedOVProbeDevices()
		return status == ovProbeInFlight
	}, time.Second, 10*time.Millisecond)

	// A status reader must answer immediately, and answer false, while the
	// probe child is still running.
	begin := time.Now()
	assert.False(t, OpenVINOHasDevice("GPU"))
	assert.Less(t, time.Since(begin), 500*time.Millisecond, "reader blocked behind the probe child")

	<-done
	assert.Equal(t, 1, calls.count())
}
