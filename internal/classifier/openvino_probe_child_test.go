//go:build openvino

package classifier

import (
	"fmt"
	"os"

	"github.com/tphakala/birdnet-go/internal/inference"
)

// init lets this test binary serve as the OpenVINO probe child. A test that
// loads a model on the auto OpenVINO device makes the probe re-execute the test
// binary, which has no `support openvino-probe` command: without this hook the
// child ignores the arguments, reruns the tests and starts probe children of its
// own, and the orphans pile up until the host runs out of memory. It runs from
// init so the child exits before TestMain and before any test starts.
func init() {
	libraryPath, ok := inference.OVProbeChildLibraryPath(os.Args)
	if !ok {
		return
	}
	if err := inference.RunOVProbeChild(libraryPath, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
