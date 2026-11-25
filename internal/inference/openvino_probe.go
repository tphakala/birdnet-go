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
	// ovProbeDeviceInfoMarker prefixes one line per device carrying its
	// properties as "<name>|<type>|<full name>" (e.g.
	// "OVPROBE_DEVICE_INFO=GPU.1|discrete|Intel(R) Arc(TM) A750 Graphics (dGPU)").
	// It is a separate prefix, not an extension of the device line, so a parent
	// built before it existed ignores the lines and a parent built after it
	// treats their absence as "type and name unknown".
	ovProbeDeviceInfoMarker = "OVPROBE_DEVICE_INFO="
	// ovProbeInfoFieldSeparator splits the fields of a device info line. The
	// full name is the last field and is split off with a field limit, so a
	// separator inside it survives.
	ovProbeInfoFieldSeparator = "|"
	// ovProbeInfoFieldCount is the number of fields in a device info line.
	ovProbeInfoFieldCount = 3
	// ovPropertyDeviceType is the OpenVINO property naming the kind of device
	// ("integrated" or "discrete" for the Intel GPU plugin). The C headers define
	// no constant for it.
	ovPropertyDeviceType = "DEVICE_TYPE"
	// ovPropertyFullDeviceName is the OpenVINO property holding the adapter name.
	ovPropertyFullDeviceName = "FULL_DEVICE_NAME"
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

// OpenVINODevice describes one OpenVINO device the probe enumerated. Type and
// FullName are best effort: they stay empty when the child could not read the
// property, or came from a build that does not report it.
type OpenVINODevice struct {
	// Name is the enumerated device name ("CPU", "GPU", "GPU.1").
	Name string
	// Type is the DEVICE_TYPE property, "integrated" or "discrete" for GPUs.
	Type string
	// FullName is the FULL_DEVICE_NAME property, the adapter's marketing name.
	FullName string
}

// ovProbeResult is one cached probe verdict. info runs parallel to devices
// (same length and order); the names stay in their own list for the callers
// that need only them.
type ovProbeResult struct {
	devices []string
	info    []OpenVINODevice
	err     error
}

// ovProbeState caches probe outcomes per library path and tracks probes in
// flight. The mutex guards only the maps; it is never held while a child
// runs, so status/API readers of the cache do not stall behind a slow or
// hanging driver stack.
type ovProbeState struct {
	mu sync.Mutex
	// results holds the cached verdict per library path. Keyed by path so
	// switching openvinopath A -> B -> A does not re-run a probe (and a known
	// crashing driver stack) for a path already decided.
	results map[string]ovProbeResult
	// inFlight holds a channel per library path whose probe child is running;
	// it is closed when the child finishes, so concurrent callers for the same
	// path wait for that result instead of launching a second child.
	inFlight map[string]chan struct{}
	// lastPath is the library path most recently requested through
	// OpenVINOProbeDevices; it is the path cache readers without a path of
	// their own (OpenVINOHasDevice) are answered for.
	lastPath string
}

var ovProbe = ovProbeState{
	results:  map[string]ovProbeResult{},
	inFlight: map[string]chan struct{}{},
}

// ovProbeCacheStatus describes what the probe cache can say right now.
type ovProbeCacheStatus int

const (
	// ovProbeNotRun means no probe has been attempted (or a transient failure
	// left nothing cached); callers must still not enumerate in-process.
	ovProbeNotRun ovProbeCacheStatus = iota
	// ovProbeInFlight means a probe child is currently running; callers should
	// not touch the in-process driver stack until it reports.
	ovProbeInFlight
	// ovProbeCached means a definitive result (success or child failure) is
	// available.
	ovProbeCached
)

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
// child process so a driver-stack crash cannot abort the caller. Callers treat
// any error as "no OpenVINO devices" and fall back to ONNX Runtime.
//
// Caching: a successful result, and any failure produced by the child itself
// (signaled, non-zero exit, no completion marker), is cached for the life of
// the process per library path, so a crashing driver stack is not re-executed
// on every hot reload; only a changed libraryPath re-probes. Failures that
// never reached a child verdict (executable lookup, pipe or start errors, the
// probe timeout) are transient and are NOT cached, so the next planning pass
// retries. Concurrent callers for the same path share one child.
func OpenVINOProbeDevices(libraryPath string) ([]string, error) {
	res, err := probeOpenVINO(libraryPath)
	return res.devices, err
}

// OpenVINOProbeDeviceInfo is OpenVINOProbeDevices with the properties the child
// reported for each device. It shares the cache and the in-flight child with
// OpenVINOProbeDevices, so calling both launches one child.
func OpenVINOProbeDeviceInfo(libraryPath string) ([]OpenVINODevice, error) {
	res, err := probeOpenVINO(libraryPath)
	return res.info, err
}

// probeOpenVINO returns the probe result for libraryPath, from the cache, from
// a child already running for it, or from a new child. The returned slices are
// the caller's own copies.
func probeOpenVINO(libraryPath string) (ovProbeResult, error) {
	if !ov.Supported {
		return ovProbeResult{}, ov.ErrOpenVINOUnavailable
	}

	for {
		ovProbe.mu.Lock()
		ovProbe.lastPath = libraryPath
		if res, ok := ovProbe.results[libraryPath]; ok {
			ovProbe.mu.Unlock()
			return res.clone(), res.err
		}
		if wait, running := ovProbe.inFlight[libraryPath]; running {
			ovProbe.mu.Unlock()
			<-wait
			continue // re-read the cache the finished probe populated (or retry)
		}
		inFlight := make(chan struct{})
		ovProbe.inFlight[libraryPath] = inFlight
		ovProbe.mu.Unlock()

		return probeAndPublish(libraryPath, inFlight)
	}
}

// clone returns a copy of the result that shares no slice with the cache.
func (r ovProbeResult) clone() ovProbeResult {
	r.devices = slices.Clone(r.devices)
	r.info = slices.Clone(r.info)
	return r
}

// probeAndPublish runs the probe child for libraryPath, records a cacheable
// verdict, and always releases the in-flight registration. The release is
// deferred so that a panic inside the exec plumbing cannot leave the path
// marked in flight forever, which would block every later caller on <-wait.
func probeAndPublish(libraryPath string, inFlight chan struct{}) (res ovProbeResult, err error) {
	defer func() {
		ovProbe.mu.Lock()
		delete(ovProbe.inFlight, libraryPath)
		close(inFlight)
		ovProbe.mu.Unlock()
	}()

	devices, info, cacheable, err := runOVProbe(libraryPath)
	res = ovProbeResult{devices: devices, info: info, err: err}

	ovProbe.mu.Lock()
	if err == nil || cacheable {
		ovProbe.results[libraryPath] = res
	}
	ovProbe.mu.Unlock()
	return res.clone(), err
}

// cachedOVProbeDevices returns the cached probe result for the most recently
// requested library path, together with the cache status. It never blocks on
// a running probe.
func cachedOVProbeDevices() (devices []string, status ovProbeCacheStatus, err error) {
	res, status := cachedOVProbe()
	return res.devices, status, res.err
}

// cachedOVProbe is cachedOVProbeDevices with the device properties as well.
// The result is empty unless the status is ovProbeCached.
func cachedOVProbe() (res ovProbeResult, status ovProbeCacheStatus) {
	ovProbe.mu.Lock()
	defer ovProbe.mu.Unlock()
	if res, ok := ovProbe.results[ovProbe.lastPath]; ok {
		return res.clone(), ovProbeCached
	}
	if _, running := ovProbe.inFlight[ovProbe.lastPath]; running {
		return ovProbeResult{}, ovProbeInFlight
	}
	return ovProbeResult{}, ovProbeNotRun
}

// OpenVINOGPUs lists the GPU-class devices (see OpenVINOIsGPUDevice) of the
// cached probe result for the most recently probed library, with the name and
// type the probe child reported. It has the same contract as OpenVINOHasDevice:
// it never launches a child and never blocks, and it answers nil when the probe
// has not completed, is still running, or failed.
func OpenVINOGPUs() []OpenVINODevice {
	res, status := cachedOVProbe()
	if status != ovProbeCached || res.err != nil {
		return nil
	}
	return FilterOpenVINOGPUs(res.info)
}

// FilterOpenVINOGPUs returns the GPU-class entries (see OpenVINOIsGPUDevice) of
// devices, in order; nil when there are none.
func FilterOpenVINOGPUs(devices []OpenVINODevice) []OpenVINODevice {
	var gpus []OpenVINODevice
	for _, d := range devices {
		if OpenVINOIsGPUDevice(d.Name) {
			gpus = append(gpus, d)
		}
	}
	return gpus
}

// runOVProbe launches the probe child and parses its marker output. cacheable
// reports whether a returned error is a child verdict worth remembering (see
// OpenVINOProbeDevices) as opposed to a transient launch or timeout failure.
func runOVProbe(libraryPath string) (devices []string, info []OpenVINODevice, cacheable bool, err error) {
	exe, err := ovProbeExecutable()
	if err != nil {
		return nil, nil, false, errors.Newf("openvino probe: cannot resolve own executable: %v", err).
			Category(errors.CategorySystem).Build()
	}

	ctx, cancel := context.WithTimeout(context.Background(), ovProbeTimeout)
	defer cancel()

	cmd := ovProbeNewCommand(ctx, exe, libraryPath)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, false, errors.Newf("openvino probe: stdout pipe: %v", err).
			Category(errors.CategorySystem).Build()
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, false, errors.Newf("openvino probe: start: %v", err).
			Category(errors.CategorySystem).Build()
	}

	devices, info, sawOK, scanErr := parseOVProbeOutput(stdout)
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		// The context killed the child: a timeout, not a verdict. Do not cache,
		// so a transiently slow driver init gets another chance next time.
		return nil, nil, false, errors.Newf("openvino probe: child timed out after %s", ovProbeTimeout).
			Category(errors.CategorySystem).Build()
	}
	if waitErr != nil {
		// A signaled child (the crash this probe exists for) surfaces here as
		// e.g. "signal: aborted"; include a stderr excerpt for the journal.
		return nil, nil, true, errors.Newf("openvino probe: child failed: %v (stderr: %s)",
			waitErr, lastOVProbeStderr(stderr.String())).
			Category(errors.CategorySystem).Build()
	}
	if scanErr != nil {
		return nil, nil, false, errors.Newf("openvino probe: reading child output: %v", scanErr).
			Category(errors.CategorySystem).Build()
	}
	if !sawOK {
		return nil, nil, true, errors.Newf("openvino probe: child exited without completion marker").
			Category(errors.CategorySystem).Build()
	}
	return devices, info, false, nil
}

// ovProbeMaxLineBytes bounds a single stdout line from the child. Marker lines
// are tiny; the headroom is for bootstrap log lines sharing the stream.
const ovProbeMaxLineBytes = 1 << 20

// parseOVProbeOutput scans probe stdout for marker lines, returning the device
// names, the properties of each device (parallel to the names), whether the
// completion marker was seen, and any read error. Non-marker lines (logging
// from the CLI bootstrap) are ignored, and so is an info line that is malformed
// or names a device that was not listed.
func parseOVProbeOutput(r io.Reader) (devices []string, info []OpenVINODevice, sawOK bool, err error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), ovProbeMaxLineBytes)
	props := map[string]OpenVINODevice{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == ovProbeOKMarker:
			sawOK = true
		case strings.HasPrefix(line, ovProbeDeviceMarker):
			if name := strings.TrimPrefix(line, ovProbeDeviceMarker); name != "" {
				devices = append(devices, name)
			}
		case strings.HasPrefix(line, ovProbeDeviceInfoMarker):
			if d, ok := parseOVProbeInfoLine(strings.TrimPrefix(line, ovProbeDeviceInfoMarker)); ok {
				props[d.Name] = d
			}
		}
	}
	for _, name := range devices {
		d := props[name] // the zero value when no info line named the device
		d.Name = name
		info = append(info, d)
	}
	return devices, info, sawOK, scanner.Err()
}

// parseOVProbeInfoLine parses the payload of a device info line,
// "<name>|<type>|<full name>". The full name is last, so a separator inside it
// stays part of the name.
func parseOVProbeInfoLine(payload string) (OpenVINODevice, bool) {
	fields := strings.SplitN(payload, ovProbeInfoFieldSeparator, ovProbeInfoFieldCount)
	if len(fields) != ovProbeInfoFieldCount || fields[0] == "" {
		return OpenVINODevice{}, false
	}
	return OpenVINODevice{Name: fields[0], Type: fields[1], FullName: fields[2]}, true
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
// loads the OpenVINO core in THIS process, enumerates devices and reads their
// properties, and writes the marker lines the parent parses to w. It is the
// only intended in-process caller of the enumeration and the property reads on
// the planning path; everything else consults the cached probe result.
func RunOVProbeChild(libraryPath string, w io.Writer) error {
	if err := InitOpenVINO(libraryPath); err != nil {
		return err
	}
	names, err := ov.AvailableDevices()
	if err != nil {
		return err
	}
	devices := make([]OpenVINODevice, 0, len(names))
	for _, name := range names {
		// A device that lacks a property is normal (the CPU plugin has no
		// adapter name worth showing), so a failed read leaves the field empty.
		devType, _ := ov.DeviceProperty(name, ovPropertyDeviceType)
		fullName, _ := ov.DeviceProperty(name, ovPropertyFullDeviceName)
		devices = append(devices, OpenVINODevice{Name: name, Type: devType, FullName: fullName})
	}
	return writeOVProbeOutput(w, devices)
}

// ovProbeLineBreaks replaces the characters that would split a marker line;
// a CR LF pair becomes one space.
var ovProbeLineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

// sanitizeOVProbeValue makes a property value safe to write on one marker line.
func sanitizeOVProbeValue(v string) string {
	return strings.TrimSpace(ovProbeLineBreaks.Replace(v))
}

// writeOVProbeOutput writes the marker lines for devices to w: one device line
// each, then one info line each, then the completion marker. Property values
// have their line breaks replaced with spaces so a value cannot forge or split
// a marker line.
func writeOVProbeOutput(w io.Writer, devices []OpenVINODevice) error {
	for _, d := range devices {
		if _, err := fmt.Fprintf(w, "%s%s\n", ovProbeDeviceMarker, d.Name); err != nil {
			return errors.Newf("openvino probe: write device line: %v", err).
				Category(errors.CategorySystem).Build()
		}
	}
	for _, d := range devices {
		if _, err := fmt.Fprintf(w, "%s%s%s%s%s%s\n", ovProbeDeviceInfoMarker,
			d.Name, ovProbeInfoFieldSeparator,
			sanitizeOVProbeValue(d.Type), ovProbeInfoFieldSeparator,
			sanitizeOVProbeValue(d.FullName)); err != nil {
			return errors.Newf("openvino probe: write device info line: %v", err).
				Category(errors.CategorySystem).Build()
		}
	}
	if _, err := fmt.Fprintln(w, ovProbeOKMarker); err != nil {
		return errors.Newf("openvino probe: write completion marker: %v", err).
			Category(errors.CategorySystem).Build()
	}
	return nil
}
