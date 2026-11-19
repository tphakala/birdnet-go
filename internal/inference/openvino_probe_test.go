package inference

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
	"github.com/tphakala/birdnet-go/internal/testutil"
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
	case "multi-gpu":
		// A host with an iGPU and a discrete card: OpenVINO lists only the
		// indexed GPU names and no bare "GPU".
		fmt.Println("OVPROBE_DEVICE=CPU")
		fmt.Println("OVPROBE_DEVICE=GPU.0")
		fmt.Println("OVPROBE_DEVICE=GPU.1")
		fmt.Println("OVPROBE_OK")
		os.Exit(0)
	case "multi-gpu-info":
		// The same host as "multi-gpu", from a child that also reports the
		// DEVICE_TYPE and FULL_DEVICE_NAME of every device.
		fmt.Println("OVPROBE_DEVICE=CPU")
		fmt.Println("OVPROBE_DEVICE=GPU.0")
		fmt.Println("OVPROBE_DEVICE=GPU.1")
		fmt.Println("OVPROBE_DEVICE_INFO=CPU|integrated|Test CPU")
		fmt.Println("OVPROBE_DEVICE_INFO=GPU.0|integrated|Test iGPU (iGPU)")
		fmt.Println("OVPROBE_DEVICE_INFO=GPU.1|discrete|Test Arc A750 (dGPU)")
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

// TestNewOVProbeCommandArgs pins the probe child invocation. The openvino-tagged
// classifier test binary serves this exact argv itself (serveOVProbeChildIfRequested
// in internal/classifier/openvino_probe_child_test.go), so a change here must be
// made there as well; TestOpenVINOProbeChildHook_ServesProbe in that package fails
// when the two drift apart.
func TestNewOVProbeCommandArgs(t *testing.T) {
	t.Parallel()

	const (
		exe         = "/path/to/binary"
		libraryPath = "/opt/openvino/lib/libopenvino_c.so"
	)
	cmd := newOVProbeCommand(t.Context(), exe, libraryPath)
	assert.Equal(t, []string{exe, "support", "openvino-probe", "--library-path", libraryPath}, cmd.Args)
}

func TestParseOVProbeOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		input       string
		wantDevices []string
		wantInfo    []OpenVINODevice
		wantOK      bool
	}{
		{
			name:        "devices with interleaved logging",
			input:       "INFO starting up\nOVPROBE_DEVICE=CPU\nnoise\nOVPROBE_DEVICE=GPU\nOVPROBE_OK\n",
			wantDevices: []string{"CPU", "GPU"},
			wantInfo:    []OpenVINODevice{{Name: "CPU"}, {Name: "GPU"}},
			wantOK:      true,
		},
		{
			name:        "no devices but completed",
			input:       "OVPROBE_OK\n",
			wantDevices: nil,
			wantInfo:    nil,
			wantOK:      true,
		},
		{
			name:        "truncated output without completion marker",
			input:       "OVPROBE_DEVICE=CPU\n",
			wantDevices: []string{"CPU"},
			wantInfo:    []OpenVINODevice{{Name: "CPU"}},
			wantOK:      false,
		},
		{
			name:        "empty device name ignored",
			input:       "OVPROBE_DEVICE=\nOVPROBE_OK\n",
			wantDevices: nil,
			wantInfo:    nil,
			wantOK:      true,
		},
		{
			name:        "info lines attach to the device of the same name",
			input:       "OVPROBE_DEVICE=CPU\nOVPROBE_DEVICE=GPU.1\nOVPROBE_DEVICE_INFO=GPU.1|discrete|Arc\nOVPROBE_OK\n",
			wantDevices: []string{"CPU", "GPU.1"},
			wantInfo:    []OpenVINODevice{{Name: "CPU"}, {Name: "GPU.1", Type: "discrete", FullName: "Arc"}},
			wantOK:      true,
		},
		{
			name:        "info line for an unlisted device is ignored",
			input:       "OVPROBE_DEVICE=CPU\nOVPROBE_DEVICE_INFO=GPU.7|discrete|Ghost\nOVPROBE_OK\n",
			wantDevices: []string{"CPU"},
			wantInfo:    []OpenVINODevice{{Name: "CPU"}},
			wantOK:      true,
		},
		{
			name:        "malformed info lines are ignored",
			input:       "OVPROBE_DEVICE=GPU\nOVPROBE_DEVICE_INFO=GPU|discrete\nOVPROBE_DEVICE_INFO=\nOVPROBE_OK\n",
			wantDevices: []string{"GPU"},
			wantInfo:    []OpenVINODevice{{Name: "GPU"}},
			wantOK:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			devices, info, sawOK, err := parseOVProbeOutput(strings.NewReader(tt.input))
			require.NoError(t, err)
			assert.Equal(t, tt.wantDevices, devices)
			assert.Equal(t, tt.wantInfo, info)
			assert.Equal(t, tt.wantOK, sawOK)
		})
	}
}

// TestOVProbeOutputRoundTrip pins that what the child writes is what the parent
// reads back: a full name with spaces, parentheses and the field separator, and
// values carrying CR or LF (which would otherwise split the line).
func TestOVProbeOutputRoundTrip(t *testing.T) {
	t.Parallel()
	devices := []OpenVINODevice{
		{Name: "CPU", Type: "integrated", FullName: "12th Gen Intel(R) Core(TM) i7-1260P"},
		{Name: "GPU.0", Type: "integrated", FullName: "Intel(R) UHD Graphics 730 (iGPU)"},
		{Name: "GPU.1", Type: "discrete", FullName: "Vendor | Model (dGPU)"},
		{Name: "GPU.2", Type: "dis\ncrete\r", FullName: "two\r\nlines"},
		{Name: "NPU"},
	}

	var buf strings.Builder
	require.NoError(t, writeOVProbeOutput(&buf, devices))
	names, info, sawOK, err := parseOVProbeOutput(strings.NewReader(buf.String()))
	require.NoError(t, err)

	assert.True(t, sawOK)
	assert.Equal(t, []string{"CPU", "GPU.0", "GPU.1", "GPU.2", "NPU"}, names)
	assert.Equal(t, []OpenVINODevice{
		devices[0], devices[1], devices[2],
		{Name: "GPU.2", Type: "dis crete", FullName: "two lines"},
		{Name: "NPU"},
	}, info)
}

func TestParseOVProbeOutputSurfacesReadErrors(t *testing.T) {
	t.Parallel()
	// A single line longer than the scanner's maximum must be reported, not
	// silently mistaken for a probe that never printed its completion marker.
	huge := strings.Repeat("x", ovProbeMaxLineBytes+1) + "\nOVPROBE_OK\n"
	_, _, sawOK, err := parseOVProbeOutput(strings.NewReader(huge))
	require.Error(t, err)
	assert.False(t, sawOK)
}

func TestRunOVProbeSuccess(t *testing.T) {
	fakeOVProbeChild(t, "ok")
	devices, _, cacheable, err := runOVProbe("libopenvino_c.so")
	require.NoError(t, err)
	assert.False(t, cacheable)
	assert.Equal(t, []string{"CPU", "GPU"}, devices)
}

func TestRunOVProbeChildCrashIsCacheable(t *testing.T) {
	fakeOVProbeChild(t, "crash")
	_, _, cacheable, err := runOVProbe("libopenvino_c.so")
	require.Error(t, err)
	assert.True(t, cacheable, "a child verdict must be remembered")
	assert.Contains(t, err.Error(), "child failed")
	assert.Contains(t, err.Error(), "invalid next size")
}

func TestRunOVProbeMissingCompletionMarkerIsCacheable(t *testing.T) {
	fakeOVProbeChild(t, "no-marker")
	_, _, cacheable, err := runOVProbe("libopenvino_c.so")
	require.Error(t, err)
	assert.True(t, cacheable)
	assert.Contains(t, err.Error(), "completion marker")
}

func TestRunOVProbeTimeoutIsTransient(t *testing.T) {
	fakeOVProbeChild(t, "hang")
	shortenOVProbeTimeout(t, 500*time.Millisecond)
	_, _, cacheable, err := runOVProbe("libopenvino_c.so")
	require.Error(t, err)
	assert.False(t, cacheable, "a timeout is not a child verdict and must not be cached")
	assert.Contains(t, err.Error(), "timed out")
}

// TestOpenVINOHasDeviceMatchesGPUByClass pins that a multi-GPU host, which lists
// only GPU.0 and GPU.1, still reports a GPU: the capability gate must not look for
// the literal "GPU".
func TestOpenVINOHasDeviceMatchesGPUByClass(t *testing.T) {
	fakeOVProbeChild(t, "multi-gpu")
	if !ov.Supported {
		t.Skip("probe needs the openvino build tag")
	}

	_, err := OpenVINOProbeDevices("libopenvino_c.so")
	require.NoError(t, err)

	assert.True(t, OpenVINOHasDevice(OVDeviceGPU), "indexed GPUs satisfy a GPU request")
	assert.True(t, OpenVINOHasDevice("GPU.1"), "a concrete name still matches exactly")
	assert.False(t, OpenVINOHasDevice("GPU.2"))
}

// TestOpenVINOGPUs pins the status reader's contract: before any probe it
// answers nil and launches no child; after a probe it lists the GPU-class
// devices with the name and type the child reported, and no CPU entry.
func TestOpenVINOGPUs(t *testing.T) {
	calls := fakeOVProbeChild(t, "multi-gpu-info")
	if !ov.Supported {
		t.Skip("probe needs the openvino build tag")
	}

	assert.Nil(t, OpenVINOGPUs(), "no probe has run yet")
	assert.Equal(t, 0, calls.count(), "the status reader must never launch a child")

	all, err := OpenVINOProbeDeviceInfo("libopenvino_c.so")
	require.NoError(t, err)
	assert.Len(t, all, 3, "the full probe result keeps the CPU entry")

	assert.Equal(t, []OpenVINODevice{
		{Name: "GPU.0", Type: "integrated", FullName: "Test iGPU (iGPU)"},
		{Name: "GPU.1", Type: "discrete", FullName: "Test Arc A750 (dGPU)"},
	}, OpenVINOGPUs())
	assert.Equal(t, 1, calls.count())
}

// TestOpenVINOGPUsFromOldChild pins that a child without info lines (a binary
// replaced under a running service) still yields the GPUs, with the name and
// type left empty.
func TestOpenVINOGPUsFromOldChild(t *testing.T) {
	fakeOVProbeChild(t, "multi-gpu")
	if !ov.Supported {
		t.Skip("probe needs the openvino build tag")
	}

	_, err := OpenVINOProbeDevices("libopenvino_c.so")
	require.NoError(t, err)

	assert.Equal(t, []OpenVINODevice{{Name: "GPU.0"}, {Name: "GPU.1"}}, OpenVINOGPUs())
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
	assert.Equal(t, OVProbeStatusNotRun, OpenVINORuntimeSnapshot().ProbeStatus)
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
	assert.Equal(t, OVProbeStatusCached, OpenVINORuntimeSnapshot().ProbeStatus)
}

func TestOpenVINOHasDeviceDoesNotBlockOnInFlightProbe(t *testing.T) {
	if !ov.Supported {
		t.Skip("probe short-circuits without the openvino build tag")
	}
	// If an assertion below fails before <-done, the probe goroutine (and its
	// child) would outlive the test; make that a reported leak, not a silent one.
	testutil.VerifyNoLeaks(t)

	calls := fakeOVProbeChild(t, "hang")
	shortenOVProbeTimeout(t, 2*time.Second)

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
	assert.Equal(t, OVProbeStatusInFlight, OpenVINORuntimeSnapshot().ProbeStatus)

	// A status reader must answer immediately, and answer false, while the
	// probe child is still running.
	begin := time.Now()
	assert.False(t, OpenVINOHasDevice("GPU"))
	assert.Less(t, time.Since(begin), 500*time.Millisecond, "reader blocked behind the probe child")

	<-done
	assert.Equal(t, 1, calls.count())
}

// markOVCoreLoadedForTest makes the package believe InitOpenVINO loaded the core
// from libraryPath (loaded=true) or that no core is loaded (loaded=false),
// restoring the previous state at cleanup. No real library is loaded.
func markOVCoreLoadedForTest(t *testing.T, loaded bool, libraryPath string) {
	t.Helper()
	ovInitMu.Lock()
	prevInit, prevPath := ovInitialized, ovInitPath
	ovInitialized, ovInitPath = loaded, libraryPath
	ovInitMu.Unlock()
	t.Cleanup(func() {
		ovInitMu.Lock()
		ovInitialized, ovInitPath = prevInit, prevPath
		ovInitMu.Unlock()
	})
}

// TestOpenVINOHasDeviceNeverProbesOrEnumeratesBeforeProbe pins that a status
// reader on a host whose core is loaded but never probed (the explicit-CPU plan
// path) answers false without launching a probe child: it must neither block on
// a child nor fall back to the in-process driver walk (issue #4236).
func TestOpenVINOHasDeviceNeverProbesOrEnumeratesBeforeProbe(t *testing.T) {
	calls := fakeOVProbeChild(t, "ok")
	markOVCoreLoadedForTest(t, true, "libopenvino_c.so")

	assert.False(t, OpenVINOHasDevice("GPU"))
	assert.False(t, OpenVINOHasDevice("CPU"))
	assert.Equal(t, 0, calls.count(), "a status reader must not launch the probe child")
}

// ovInProcessEnumerationAllowed names the only function in this package allowed
// to call ov.AvailableDevices: the probe child, which enumerates in a separate
// process precisely so a driver fault cannot abort BirdNET-Go (issue #4236).
const ovInProcessEnumerationAllowed = "RunOVProbeChild"

// ovInProcessDeviceQueries names the OpenVINO calls that walk the vendor driver
// stack for a device and so must run only in the probe child: the device
// enumeration and the per-device property reads.
var ovInProcessDeviceQueries = []string{"AvailableDevices", "DeviceProperty"}

// TestOnlyProbeChildEnumeratesInProcess is the structural guard for #4236: a
// behavioural test cannot tell an in-process ov.AvailableDevices call apart
// from "no device" without a real OpenVINO core, so this parses the package's
// non-test sources and fails if any function other than the probe child calls
// it.
func TestOnlyProbeChildEnumeratesInProcess(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	var callers []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if ok && slices.Contains(ovInProcessDeviceQueries, sel.Sel.Name) {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "ov" {
						callers = append(callers, name+":"+fn.Name.Name+":"+sel.Sel.Name)
					}
				}
				return true
			})
		}
	}
	require.NotEmpty(t, callers, "the probe child must still enumerate (guards against a vacuous scan)")
	for _, query := range ovInProcessDeviceQueries {
		assert.Truef(t, slices.ContainsFunc(callers, func(c string) bool { return strings.HasSuffix(c, ":"+query) }),
			"the probe child must still call ov.%s (guards against a vacuous scan)", query)
	}
	for _, c := range callers {
		assert.Containsf(t, c, ":"+ovInProcessEnumerationAllowed+":",
			"%s queries OpenVINO devices in-process; only the probe child may (issue #4236)", c)
	}
}

// TestEnsureOpenVINOProbeRunsChildOnce pins that EnsureOpenVINOProbe probes the
// library the core was loaded from, out of process and once, after which status
// readers answer from the probe.
func TestEnsureOpenVINOProbeRunsChildOnce(t *testing.T) {
	calls := fakeOVProbeChild(t, "ok")
	markOVCoreLoadedForTest(t, true, "/opt/custom/libopenvino_c.so")

	EnsureOpenVINOProbe()
	EnsureOpenVINOProbe()

	if !ov.Supported {
		assert.Equal(t, 0, calls.count(), "without the openvino build tag there is nothing to probe")
		return
	}
	assert.Equal(t, 1, calls.count(), "the verdict is cached, so a second call launches no child")
	ovProbe.mu.Lock()
	_, probedLoadedPath := ovProbe.results["/opt/custom/libopenvino_c.so"]
	ovProbe.mu.Unlock()
	assert.True(t, probedLoadedPath, "the probe must target the library the core was loaded from")
	assert.True(t, OpenVINOHasDevice("GPU"), "readers answer from the probe once it has run")
}

// TestEnsureOpenVINOProbeNoopWithoutCore pins that a host with no OpenVINO core
// loaded never launches a probe child.
func TestEnsureOpenVINOProbeNoopWithoutCore(t *testing.T) {
	calls := fakeOVProbeChild(t, "ok")
	markOVCoreLoadedForTest(t, false, "")

	EnsureOpenVINOProbe()

	assert.Equal(t, 0, calls.count())
}

// TestInitOpenVINORecordsLoadedLibraryPath pins that the path later handed to
// the probe child is the library the loader really bound, not the configured
// string: with nothing configured the loader may have discovered one, and a
// child that received "" would rediscover (or load another) library.
func TestInitOpenVINORecordsLoadedLibraryPath(t *testing.T) {
	const discovered = "/discovered/openvino/libopenvino_c.so"
	for name, tt := range map[string]struct {
		configured string
		loaded     string
		want       string
	}{
		"discovered path replaces empty config": {configured: "", loaded: discovered, want: discovered},
		"configured path is kept":               {configured: "/pinned/lib.so", loaded: "/pinned/lib.so", want: "/pinned/lib.so"},
		"default name load is named":            {configured: "", loaded: "", want: ov.DefaultLibraryName},
	} {
		t.Run(name, func(t *testing.T) {
			markOVCoreLoadedForTest(t, false, "")
			t.Cleanup(func() { ovInitializedSnap.Store(false) })
			origInit, origLoaded := ovInit, ovLoadedLibrary
			t.Cleanup(func() { ovInit, ovLoadedLibrary = origInit, origLoaded })
			ovInit = func(string) error { return nil }
			ovLoadedLibrary = func() (string, bool) { return tt.loaded, true }

			require.NoError(t, InitOpenVINO(tt.configured))

			assert.Equal(t, tt.want, OpenVINOLoadedLibraryPath())
		})
	}
}
