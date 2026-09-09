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
	// ReasonRenderNodeUnavailable marks a GPU whose DRM render node is not
	// present in this mount namespace, the usual cause being a container started
	// without --device /dev/dri.
	ReasonRenderNodeUnavailable = "render-node-unavailable"
	// ReasonRenderNodePermission marks a GPU whose DRM render node is present
	// but cannot be opened. Two container misconfigurations produce it: the
	// runtime user is not in the node's owning group, or the device was
	// bind-mounted without being granted through the device cgroup, which is
	// what "-v /dev/dri" does where "--device /dev/dri" was meant.
	ReasonRenderNodePermission = "render-node-permission"
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
	// Accessible reports whether this process can open the device's DRM render
	// node, which every runtime needs and is the strongest claim a filesystem
	// probe can make.
	//
	// It deliberately does not predict whether inference will run here. That
	// depends on the configured backend and device preference and on what the
	// OpenVINO core enumerates once loaded, all of which the classifier's device
	// planner already decides; this package has no settings dependency and
	// cannot second-guess it without contradicting it.
	Accessible bool
	// Reasons lists every reason code explaining why this GPU is not an
	// inference target, most fundamental first, and is empty when nothing this
	// probe can see stands in the way. It is a list rather than a single code
	// because the blockers stack in the default containerised deployment, and a
	// user fixing them one restart at a time is the outcome worth avoiding.
	Reasons []string
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

// Detect returns the host profile, probing the hardware on the first call and
// serving a cached copy afterwards. Backend availability is probed on every
// call, because it changes without the hardware changing: installing an ONNX
// Runtime library, or loading a model that initializes the OpenVINO core, both
// take effect without a restart. It is safe for concurrent use.
//
// A caller that has already probed the backends itself should use Hardware and
// WithBackends instead, so one probe decides every field of its answer.
func Detect() Profile {
	return hardwareProfile(false).WithBackends(probeBackends())
}

// Refresh discards the cached hardware facts and probes them again. Use it when
// the host may have changed underneath a running process, for instance after a
// GPU device node is mapped in or its group membership is granted.
func Refresh() Profile {
	return hardwareProfile(true).WithBackends(probeBackends())
}

// Hardware returns the cached hardware facts with no backend state attached.
// Pair it with WithBackends when the caller already holds an authoritative
// backend probe; Detect is the convenience form for callers that do not.
func Hardware() Profile {
	return hardwareProfile(false)
}

// WithBackends returns a copy of the profile carrying the given backend state.
// Backends feed capability-token derivation, so a caller whose probe honours a
// user-configured library path passes it here rather than letting this package
// re-probe with defaults and silently drop the corresponding token.
//
// The value receiver is what makes this safe: it returns a modified copy, so a
// caller can attach backends to the shared cached snapshot without writing
// through to the cache. A pointer receiver would.
//
//nolint:gocritic // hugeParam: the copy is the point; see the note above.
func (p Profile) WithBackends(backends Backends) Profile {
	p.Backends = backends
	return p
}

// hardwareProfile returns the cached hardware facts, probing them when the
// cache is empty or when force is set.
//
// The returned Profile is a copy whose slices are cloned, so a caller may sort,
// filter or append to any of them without writing through to the cache. A plain
// struct copy would share the backing arrays: detectSIMD builds with spare
// capacity, so a single append on a returned profile would land in the cached
// array and be visible to every later caller.
func hardwareProfile(force bool) Profile {
	hardwareMu.Lock()
	defer hardwareMu.Unlock()
	if force || cachedHardware == nil {
		probed := probeHardware(rootFS)
		cachedHardware = &probed
	}
	snapshot := *cachedHardware
	snapshot.SIMD = slices.Clone(cachedHardware.SIMD)
	snapshot.Issues = slices.Clone(cachedHardware.Issues)
	snapshot.Accelerators = slices.Clone(cachedHardware.Accelerators)
	// Cloning the accelerator slice copies the structs, whose Reasons field is
	// itself a slice header still pointing at the cached array. applyAccessibility
	// builds it with spare capacity, so a single append by a caller would land in
	// the cache.
	for i := range snapshot.Accelerators {
		snapshot.Accelerators[i].Reasons = slices.Clone(snapshot.Accelerators[i].Reasons)
	}
	return snapshot
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
