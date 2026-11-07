//go:build openvino

package classifier

import (
	"fmt"
	"os"

	"github.com/tphakala/birdnet-go/internal/inference"
)

// The OpenVINO device probe re-executes the running binary as
// `support openvino-probe --library-path <path>` (see newOVProbeCommand in
// internal/inference/openvino_probe.go). These mirror that invocation.
const (
	probeChildParentCommand = "support"
	probeChildSubcommand    = "openvino-probe"
	probeChildLibraryFlag   = "--library-path"
	// probeChildArgCount is the binary name plus the three probe arguments and
	// the library path value.
	probeChildArgCount = 5
)

// init lets this test binary serve as the OpenVINO probe child. A test that
// loads a model on the auto OpenVINO device makes the probe re-execute the test
// binary, which has no such subcommand: without this hook the child ignores the
// arguments, reruns the tests and starts probe children of its own, and the
// orphans pile up until the host runs out of memory. It runs from init so the
// child exits before TestMain and before any test starts.
func init() {
	if len(os.Args) != probeChildArgCount || os.Args[1] != probeChildParentCommand ||
		os.Args[2] != probeChildSubcommand || os.Args[3] != probeChildLibraryFlag {
		return
	}
	if err := inference.RunOVProbeChild(os.Args[4], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
