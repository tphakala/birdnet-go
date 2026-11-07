//go:build openvino

package classifier

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/inference"
)

// The OpenVINO device probe re-executes the running binary as
// `support openvino-probe --library-path <path>` (newOVProbeCommand in
// internal/inference/openvino_probe.go, pinned by TestNewOVProbeCommandArgs).
const (
	probeChildParentCommand = "support"
	probeChildSubcommand    = "openvino-probe"
	probeChildLibraryFlag   = "--library-path"
	// probeChildArgCount is the executable, the parent command, the subcommand,
	// the flag and its value.
	probeChildArgCount = 5
	// probeChildHookTestEnv marks the processes TestOpenVINOProbeChildHook_ServesProbe
	// starts, so a child that was not handed the probe invocation exits at once.
	probeChildHookTestEnv = "BIRDNET_TEST_OVPROBE_HOOK"
	// probeChildGuardMessage is what such a child prints before exiting.
	probeChildGuardMessage = "openvino probe hook: not the probe invocation"
	// probeChildGuardExitCode is that child's exit status.
	probeChildGuardExitCode = 2
	// probeChildLoadFailure is the text a probe child that served the probe
	// reports when the library cannot be loaded (the path itself is redacted).
	probeChildLoadFailure = "failed to load libopenvino_c"
)

// probeChildLibraryPath reports whether args is the probe child invocation and
// returns its library path.
func probeChildLibraryPath(args []string) (libraryPath string, ok bool) {
	if len(args) != probeChildArgCount || args[1] != probeChildParentCommand ||
		args[2] != probeChildSubcommand || args[3] != probeChildLibraryFlag {
		return "", false
	}
	return args[4], true
}

// serveOVProbeChildIfRequested lets this test binary serve as the OpenVINO probe
// child. A test that loads a model on the auto OpenVINO device makes the probe
// re-execute the test binary, which has no `support openvino-probe` command:
// without this hook the child ignores the arguments, reruns the tests and starts
// probe children of its own, and the orphans pile up until the host runs out of
// memory. TestMain calls it first, so the child exits before any test starts.
func serveOVProbeChildIfRequested() {
	libraryPath, ok := probeChildLibraryPath(os.Args)
	if !ok {
		return
	}
	if err := inference.RunOVProbeChild(libraryPath, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// exitIfOVProbeHookTestChild ends any process started by
// TestOpenVINOProbeChildHook_ServesProbe (marked with probeChildHookTestEnv) that
// serveOVProbeChildIfRequested did not already end: it prints
// probeChildGuardMessage and exits instead of running the tests. TestMain calls
// it right after serveOVProbeChildIfRequested, as a separate call, so a hook that
// stops recognizing the invocation, or a removed serve call, fails that test at
// once without the child recursing into other probing tests.
func exitIfOVProbeHookTestChild() {
	if os.Getenv(probeChildHookTestEnv) == "" {
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, probeChildGuardMessage)
	os.Exit(probeChildGuardExitCode)
}

// TestProbeChildLibraryPath pins the argv the hook treats as the probe child
// invocation: only the exact five-element form matches, so an ordinary or partly
// matching test invocation still runs the tests.
func TestProbeChildLibraryPath(t *testing.T) {
	t.Parallel()

	const (
		exe         = "/path/to/classifier.test"
		libraryPath = "/opt/openvino/lib/libopenvino_c.so"
	)
	tests := []struct {
		name     string
		args     []string
		wantPath string
		wantOK   bool
	}{
		{name: "probe invocation", args: []string{exe, "support", "openvino-probe", "--library-path", libraryPath}, wantPath: libraryPath, wantOK: true},
		{name: "probe invocation with an empty library path", args: []string{exe, "support", "openvino-probe", "--library-path", ""}, wantPath: "", wantOK: true},
		{name: "no arguments", args: []string{exe}},
		{name: "ordinary test flags", args: []string{exe, "-test.run", "TestX"}},
		{name: "subcommand without the flag", args: []string{exe, "support", "openvino-probe"}},
		{name: "wrong subcommand", args: []string{exe, "support", "other", "--library-path", libraryPath}},
		{name: "wrong parent command", args: []string{exe, "debug", "openvino-probe", "--library-path", libraryPath}},
		{name: "wrong flag", args: []string{exe, "support", "openvino-probe", "--path", libraryPath}},
		{name: "extra argument", args: []string{exe, "support", "openvino-probe", "--library-path", libraryPath, "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotPath, gotOK := probeChildLibraryPath(tt.args)
			assert.Equal(t, tt.wantOK, gotOK)
			assert.Equal(t, tt.wantPath, gotPath)
		})
	}
}

// TestOpenVINOProbeChildHook_ServesProbe runs the real device probe, which
// re-executes this test binary with the production probe arguments, against a
// library path that does not exist. The child must serve the probe (and fail to
// load the library), not run the test suite or hit the guard.
func TestOpenVINOProbeChildHook_ServesProbe(t *testing.T) {
	if os.Getenv(probeChildHookTestEnv) != "" {
		t.Skip("running inside a probe child started by this test")
	}
	t.Setenv(probeChildHookTestEnv, "1")
	missing := filepath.Join(t.TempDir(), "missing-libopenvino_c.so")

	devices, err := inference.OpenVINOProbeDevices(missing)
	require.Error(t, err, "a probe against a missing library must fail")
	assert.Empty(t, devices)
	assert.NotContains(t, err.Error(), probeChildGuardMessage,
		"the probe child did not recognize the probe invocation")
	assert.Contains(t, err.Error(), probeChildLoadFailure,
		"the probe child must report that it could not load the requested library")
}
