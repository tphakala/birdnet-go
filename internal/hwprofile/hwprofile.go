// Package hwprofile probes the host for the hardware facts that decide which
// classifier model builds can run on it, and expresses them as capability
// tokens.
//
// The probes themselves are mostly a rehome of detection that already lived in
// sysinfo, cpuspec, mempolicy and inference. What this package adds is a single
// snapshot type, board identification from the device tree, GPU detection that
// does not depend on the build tag of the inference backend, and the token
// vocabulary that joins a host to the published model manifests.
//
// Probing is best-effort throughout. A probe that cannot complete yields a zero
// value and appends an Issue describing why; nothing here returns an error that
// could block startup.
package hwprofile

import (
	"runtime"
	"slices"
	"sync"

	"github.com/tphakala/birdnet-go/internal/cpuspec"
	"github.com/tphakala/birdnet-go/internal/mempolicy"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// rootFS is the filesystem root the probes read from in production. Every
// filesystem-reading probe takes it as a parameter so tests can point at a
// fixture tree, matching the sysinfo.DetectEnvironment(rootPath) precedent.
const rootFS = "/"

// Go architecture identifiers (runtime.GOARCH) the token derivation switches on.
const (
	archAMD64 = "amd64"
	archARM64 = "arm64"
	archARM   = "arm"
	arch386   = "386"
)

// Probe names used in Issue.Probe.
const (
	ProbeBoard        = "board"
	ProbeMemory       = "memory"
	ProbeAccelerators = "accelerators"
)

// Reason codes used in Issue.Reason and Accelerator.Reason. They are stable
// identifiers, not prose: the frontend renders them through the i18n catalog.
const (
	// ReasonReadFailed is recorded when a sysfs or procfs path exists but could
	// not be read.
	ReasonReadFailed = "read-failed"
	// ReasonUnavailable is recorded when a probe returned no usable value on a
	// host where one was expected (e.g. total RAM could not be determined).
	ReasonUnavailable = "unavailable"
	// ReasonOpenVINONotBuilt marks an Intel GPU that is physically present but
	// unreachable because this binary was not built with the OpenVINO backend.
	ReasonOpenVINONotBuilt = "openvino-not-built"
	// ReasonRenderNodeUnavailable marks a GPU whose DRM render node is not
	// present in this mount namespace, the usual cause being a container started
	// without --device /dev/dri.
	ReasonRenderNodeUnavailable = "render-node-unavailable"
	// ReasonRenderNodePermission marks a GPU whose DRM render node is present
	// but cannot be opened, the usual cause being a container that maps the
	// device without adding the container user to the node's owning group.
	ReasonRenderNodePermission = "render-node-permission"
	// ReasonOpenVINODeviceMissing marks an Intel GPU that OpenVINO is built for
	// but does not enumerate as a device, typically a missing compute runtime.
	ReasonOpenVINODeviceMissing = "openvino-device-missing"
	// ReasonNoRuntime marks a GPU for which this build ships no inference
	// runtime at all (any AMD or NVIDIA GPU today).
	ReasonNoRuntime = "no-runtime"
)

// Profile is a snapshot of the host's inference-relevant hardware.
type Profile struct {
	// Arch is the Go architecture identifier ("amd64", "arm64", "arm"). Token
	// derivation switches on it; CPUArch carries the human-readable name.
	Arch string
	// CPUArch is the conventional architecture name ("x86_64", "aarch64",
	// "armv7l"), identical to sysinfo.GetCPUArch and to what the API already
	// reports as HardwareInfo.Arch.
	CPUArch string
	// CPUModel is the CPU brand string, empty when it could not be read.
	CPUModel string
	// Environment is the runtime environment ("Docker", "Bare Metal", ...).
	Environment string
	// PhysicalCores is the physical core count, falling back to the logical
	// count when the physical count is unavailable.
	PhysicalCores int
	// PerfCores is the performance-core count on hybrid CPUs, 0 when the CPU is
	// not a known hybrid part.
	PerfCores int
	// TotalRAMBytes is the effective memory ceiling: host RAM clamped by any
	// cgroup limit. 0 when unknown.
	TotalRAMBytes int64
	// HasNativeF16 reports native half-precision SIMD (ASIMDHP on arm64).
	HasNativeF16 bool
	// SIMD lists the SIMD extensions relevant to inference ("avx2", "avx512",
	// "neon", "sve").
	SIMD []string
	// Board identifies the single-board computer this runs on, when the device
	// tree names one.
	Board Board
	// Accelerators lists the GPUs found on the PCI/DRM bus, whether or not this
	// build can use them.
	Accelerators []Accelerator
	// Backends reports which inference backends this binary can reach.
	Backends Backends
	// Issues records probes that could not complete. Empty on a fully probed
	// host; a non-empty list never means the profile is unusable.
	Issues []Issue
}

// Board identifies the host board from the device tree. A host without a device
// tree (any PC) is Kind BoardGeneric with an empty Tier.
type Board struct {
	// Kind is the board family ("raspberry-pi", "generic").
	Kind string
	// Model is the device-tree model string, e.g. "Raspberry Pi 5 Model B Rev 1.0".
	Model string
	// SoC is the system-on-chip identifier from the device-tree compatible list,
	// e.g. "bcm2712".
	SoC string
	// Tier is the performance band derived from the SoC ("pi5", "pi4", "pi3"),
	// empty when the board is not one the model catalog distinguishes.
	Tier string
}

// Accelerator is one GPU found on the host, reported whether or not this build
// can run inference on it. Detection is deliberately independent of the
// inference build tags so the UI can tell a user their hardware would work with
// a different image.
type Accelerator struct {
	// Kind is "igpu" or "dgpu".
	Kind string
	// Vendor is "intel", "amd" or "nvidia".
	Vendor string
	// Name is a human-readable device name when one can be derived, otherwise
	// the PCI device ID.
	Name string
	// Generation is the Intel graphics generation (9, 11, 12), 0 for other
	// vendors and for Intel parts not in the known-generation table.
	Generation int
	// Via is the runtime that would execute inference on this device
	// ("openvino"), empty when no runtime in this build can reach it.
	Via string
	// Usable reports whether inference can actually run on this device now.
	Usable bool
	// Reasons lists every reason code explaining Usable == false, most
	// fundamental first, and is empty when the device is usable. It is a list
	// rather than a single code because the blockers stack in the default
	// containerised deployment, and a user fixing them one round trip at a time
	// is the outcome worth avoiding.
	Reasons []string

	// renderNode is how reachable this GPU's DRM render node is. It is a
	// property of the hardware and the mount namespace, so it is probed once
	// and kept, and it is unexported because callers should read the conclusion
	// (Usable, Reasons) rather than re-derive it.
	renderNode renderNodeState
}

// Backends reports the availability of each inference backend in this build.
type Backends struct {
	TFLite   BackendStatus
	ONNX     BackendStatus
	OpenVINO OpenVINOStatus
}

// BackendStatus is the availability of one inference backend.
type BackendStatus struct {
	// Available reports that the backend can be used on this host.
	Available bool
	// Initialized reports that the backend runtime has already been set up.
	Initialized bool
	// Version is the detected runtime version, empty when unknown.
	Version string
}

// OpenVINOStatus is the OpenVINO backend status plus the devices it enumerates.
type OpenVINOStatus struct {
	// Supported reports that this binary links the OpenVINO backend.
	Supported bool
	// Active reports that at least one classifier is currently running on it.
	Active bool
	// Devices lists the OpenVINO device names available ("CPU", "GPU").
	Devices []string
}

// Issue records a probe that could not complete, so a caller can distinguish
// "not present" from "could not tell".
type Issue struct {
	// Probe is the probe that failed (one of the Probe* constants).
	Probe string
	// Reason is the reason code (one of the Reason* constants).
	Reason string
}

var (
	// hardwareMu guards cachedHardware. A mutex rather than sync.Once because
	// Refresh has to be able to replace the cached value, which Once cannot
	// express; the effect for Detect is the same, one probe for the process
	// lifetime, with concurrent first callers collapsing into it.
	hardwareMu     sync.Mutex
	cachedHardware *Profile
)

// Detect returns the host profile. The hardware facts are probed on the first
// call and cached; the inference backends are probed on every call, because
// backend availability changes without the hardware changing (loading a model
// initializes the OpenVINO core, at which point its GPU device appears) and a
// cached answer would leave the UI contradicting itself. It is safe for
// concurrent use.
func Detect() Profile {
	return evaluateBackends(hardwareProfile(false))
}

// Refresh re-probes the hardware as well, discarding the cache. It exists for
// the settings hot-reload path; the backend state that changes most often is
// already re-probed by every Detect call.
func Refresh() Profile {
	return evaluateBackends(hardwareProfile(true))
}

// hardwareProfile returns the cached hardware facts, probing them when the
// cache is empty or when force is set.
func hardwareProfile(force bool) Profile {
	hardwareMu.Lock()
	defer hardwareMu.Unlock()
	if force || cachedHardware == nil {
		probed := probeHardware(rootFS)
		cachedHardware = &probed
	}
	return *cachedHardware
}

// probeHardware collects everything that cannot change while the process runs.
// root parameterizes the filesystem-backed probes (board, accelerators,
// memory); the architecture and CPU probes always describe the running process.
// The returned accelerators carry hardware facts only: whether inference can
// run on them depends on the backends and is decided in evaluateBackends.
func probeHardware(root string) Profile {
	p := Profile{
		Arch:          runtime.GOARCH,
		CPUArch:       sysinfo.GetCPUArch(),
		CPUModel:      sysinfo.GetCPUModel(),
		PerfCores:     cpuspec.GetCPUSpec().PerformanceCores,
		PhysicalCores: physicalCores(),
		HasNativeF16:  cpuspec.HasNativeF16(),
		SIMD:          detectSIMD(),
	}
	p.Environment, _ = sysinfo.GetEnvironment()

	p.TotalRAMBytes = mempolicy.DetectTotalMemoryAt(root)
	if p.TotalRAMBytes <= 0 {
		p.TotalRAMBytes = 0
		p.Issues = append(p.Issues, Issue{Probe: ProbeMemory, Reason: ReasonUnavailable})
	}

	board, boardIssues := detectBoard(root)
	p.Board = board
	p.Issues = append(p.Issues, boardIssues...)

	accelerators, accIssues := detectAccelerators(root)
	p.Accelerators = accelerators
	p.Issues = append(p.Issues, accIssues...)

	return p
}

// evaluateBackends probes the inference backends and decides each
// accelerator's usability against them.
func evaluateBackends(p Profile) Profile {
	return applyBackends(p, probeBackends())
}

// applyBackends returns a copy of the profile carrying the given backend state,
// with every accelerator's usability decided against it. The returned profile
// shares no mutable accelerator state with the input, so evaluating the cached
// hardware snapshot never writes through to the cache.
func applyBackends(p Profile, backends Backends) Profile {
	p.Backends = backends
	if len(p.Accelerators) == 0 {
		return p
	}

	openvinoGPU := backends.OpenVINO.Supported && slices.Contains(backends.OpenVINO.Devices, deviceGPU)
	evaluated := make([]Accelerator, len(p.Accelerators))
	for i := range p.Accelerators {
		accelerator := p.Accelerators[i]
		applyUsability(&accelerator, backends, openvinoGPU, accelerator.renderNode)
		evaluated[i] = accelerator
	}
	p.Accelerators = evaluated
	return p
}
