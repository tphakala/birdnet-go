//go:build openvino

package classifier

import (
	"fmt"
	"os"

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

// init lets this test binary serve as the OpenVINO probe child. A test that
// loads a model on the auto OpenVINO device makes the probe re-execute the test
// binary, which has no `support openvino-probe` command: without this hook the
// child ignores the arguments, reruns the tests and starts probe children of its
// own, and the orphans pile up until the host runs out of memory. It runs from
// init so the child exits before TestMain and before any test starts.
func init() {
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
