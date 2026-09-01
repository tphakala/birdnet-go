package inference

// This file implements an out-of-process probe for OpenVINO device
// availability. Enumerating devices walks the vendor driver stack (NEO, IGC,
// Level Zero) inside ov_core_get_available_devices, and a fault anywhere in
// that stack aborts the process before Go can recover: glibc heap-corruption
// detection raises SIGABRT during cgo execution, which took down the whole
// analyzer in a systemd crash-loop (issue #4236). Running the first
// enumeration in a short-lived child process turns that worst case into an
// ordinary "OpenVINO unavailable" fallback to ONNX Runtime, matching the
// backend's stated philosophy that any OpenVINO failure degrades to ORT.
//
// The child is this same executable invoked as `support openvino-probe`,
// which loads the core, enumerates devices, and reports them on stdout using
// marker-prefixed lines (config loading may log to stdout, so bare lines are
// not trusted). The probe result is cached per (process, library path):
// device topology does not change under a running service, and a cached
// failure prevents a crashing driver stack from being re-executed on every
// hot reload.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
)

const (
	// ovProbeDeviceMarker prefixes one probe stdout line per available device
	// (e.g. "OVPROBE_DEVICE=GPU"). A marker prefix is required because the
	// child shares the normal CLI bootstrap, whose logging can interleave
	// arbitrary lines on stdout.
	ovProbeDeviceMarker = "OVPROBE_DEVICE="
	// ovProbeOKMarker is the final probe stdout line, printed only after the
	// enumeration completed and every device line was written. Requiring it
	// means truncated output from a child that died mid-write is never
	// mistaken for a successful empty result.
	ovProbeOKMarker = "OVPROBE_OK"
	// ovProbeSubcommand is the hidden CLI path the probe child runs.
	ovProbeSubcommand = "openvino-probe"
	// ovProbeParentCommand is the CLI command group hosting the probe.
	ovProbeParentCommand = "support"
	// ovProbeLibraryPathFlag names the probe child's library path flag.
	ovProbeLibraryPathFlag = "--library-path"
)

// ovProbeTimeout bounds one probe child run. First-ever enumeration includes
// vendor driver initialization (cold NEO caches take seconds, not minutes);
// a child still running after this long is treated as failed and killed by
// the context. Variable rather than constant so tests can shorten it.
var ovProbeTimeout = 60 * time.Second

// ovProbeState caches the single per-process probe outcome.
type ovProbeState struct {
	mu          sync.Mutex
	done        bool
	libraryPath string
	devices     []string
	err         error
}

var ovProbe ovProbeState

// Test seams: how the probe locates and launches its child process.
var (
	ovProbeExecutable = os.Executable
	ovProbeNewCommand = newOVProbeCommand
)

// newOVProbeCommand builds the probe child invocation: this executable run as
// `support openvino-probe --library-path <path>`.
func newOVProbeCommand(ctx context.Context, exe, libraryPath string) *exec.Cmd {
	return exec.CommandContext(ctx, exe,
		ovProbeParentCommand, ovProbeSubcommand, ovProbeLibraryPathFlag, libraryPath)
}

// OpenVINOProbeDevices reports the OpenVINO device names visible on this host
// (e.g. "CPU", "GPU"), determined by running the enumeration in a short-lived
// child process so a driver-stack crash cannot abort the caller. The result
// (success or failure) is cached for the life of the process per library
// path; only a changed libraryPath re-runs the probe. Callers treat any error
// as "no OpenVINO devices" and fall back to ONNX Runtime.
func OpenVINOProbeDevices(libraryPath string) ([]string, error) {
	if !ov.Supported {
		return nil, ov.ErrOpenVINOUnavailable
	}

	ovProbe.mu.Lock()
	defer ovProbe.mu.Unlock()
	if ovProbe.done && ovProbe.libraryPath == libraryPath {
		return slices.Clone(ovProbe.devices), ovProbe.err
	}

	devices, err := runOVProbe(libraryPath)
	ovProbe.done = true
	ovProbe.libraryPath = libraryPath
	ovProbe.devices = devices
	ovProbe.err = err
	return slices.Clone(devices), err
}

// cachedOVProbeDevices returns the cached probe result, reporting ok=false
// when no probe has completed in this process.
func cachedOVProbeDevices() (devices []string, probed bool, err error) {
	ovProbe.mu.Lock()
	defer ovProbe.mu.Unlock()
	if !ovProbe.done {
		return nil, false, nil
	}
	return slices.Clone(ovProbe.devices), true, ovProbe.err
}

// runOVProbe launches the probe child and parses its marker output.
func runOVProbe(libraryPath string) ([]string, error) {
	exe, err := ovProbeExecutable()
	if err != nil {
		return nil, errors.Newf("openvino probe: cannot resolve own executable: %v", err).
			Category(errors.CategorySystem).Build()
	}

	ctx, cancel := context.WithTimeout(context.Background(), ovProbeTimeout)
	defer cancel()

	cmd := ovProbeNewCommand(ctx, exe, libraryPath)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Newf("openvino probe: stdout pipe: %v", err).
			Category(errors.CategorySystem).Build()
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Newf("openvino probe: start: %v", err).
			Category(errors.CategorySystem).Build()
	}

	devices, sawOK := parseOVProbeOutput(stdout)

	if err := cmd.Wait(); err != nil {
		// A signaled child (the crash this probe exists for) surfaces here as
		// e.g. "signal: aborted"; include trailing stderr for the journal.
		return nil, errors.Newf("openvino probe: child failed: %v (stderr: %s)",
			err, lastOVProbeStderr(stderr.String())).
			Category(errors.CategorySystem).Build()
	}
	if !sawOK {
		return nil, errors.Newf("openvino probe: child exited without completion marker").
			Category(errors.CategorySystem).Build()
	}
	return devices, nil
}

// parseOVProbeOutput scans probe stdout for marker lines, returning the device
// names and whether the completion marker was seen. Non-marker lines (logging
// from the CLI bootstrap) are ignored.
func parseOVProbeOutput(r io.Reader) (devices []string, sawOK bool) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == ovProbeOKMarker:
			sawOK = true
		case strings.HasPrefix(line, ovProbeDeviceMarker):
			if name := strings.TrimPrefix(line, ovProbeDeviceMarker); name != "" {
				devices = append(devices, name)
			}
		}
	}
	return devices, sawOK
}

// ovProbeStderrExcerpt bounds each half of the child stderr excerpt echoed
// into a probe error.
const ovProbeStderrExcerpt = 300

// lastOVProbeStderr trims child stderr to a head+tail excerpt so a probe
// error stays journal-sized. The head carries the cause (glibc prints
// "free(): invalid next size" and Go prints "SIGABRT: abort" before the
// goroutine and register dump), the tail the final state.
func lastOVProbeStderr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 2*ovProbeStderrExcerpt {
		return s
	}
	return s[:ovProbeStderrExcerpt] + " ... " + s[len(s)-ovProbeStderrExcerpt:]
}

// RunOVProbeChild is the child-side body of `support openvino-probe`: it
// loads the OpenVINO core in THIS process, enumerates devices, and writes the
// marker lines the parent parses to w. It is the only intended in-process
// caller of the enumeration on the planning path; everything else consults
// the cached probe result.
func RunOVProbeChild(libraryPath string, w io.Writer) error {
	if err := InitOpenVINO(libraryPath); err != nil {
		return err
	}
	devices, err := ov.AvailableDevices()
	if err != nil {
		return err
	}
	for _, d := range devices {
		if _, err := fmt.Fprintf(w, "%s%s\n", ovProbeDeviceMarker, d); err != nil {
			return errors.Newf("openvino probe: write device line: %v", err).
				Category(errors.CategorySystem).Build()
		}
	}
	if _, err := fmt.Fprintln(w, ovProbeOKMarker); err != nil {
		return errors.Newf("openvino probe: write completion marker: %v", err).
			Category(errors.CategorySystem).Build()
	}
	return nil
}
