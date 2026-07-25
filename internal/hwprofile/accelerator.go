package hwprofile

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Accelerator kinds reported in Accelerator.Kind.
const (
	// AcceleratorIGPU is a GPU integrated into the CPU package.
	AcceleratorIGPU = "igpu"
	// AcceleratorDGPU is a discrete GPU on its own PCI card.
	AcceleratorDGPU = "dgpu"
)

// Accelerator vendors reported in Accelerator.Vendor, resolved from the PCI
// vendor ID in sysfs.
const (
	// VendorIntel is an Intel GPU, the only vendor this project can currently
	// accelerate inference on.
	VendorIntel = "intel"
	// VendorAMD is an AMD GPU. Reported for diagnostics; no build ships a ROCm
	// runtime.
	VendorAMD = "amd"
	// VendorNVIDIA is an NVIDIA GPU. Reported for diagnostics; no build ships a
	// CUDA runtime.
	VendorNVIDIA = "nvidia"
)

// Sysfs and devfs paths, relative to the filesystem root.
const (
	drmClassDir   = "sys/class/drm"
	devDRIDir     = "dev/dri"
	cardPrefix    = "card"
	renderPrefix  = "renderD"
	ueventSlotKey = "PCI_SLOT_NAME="
)

// pciVendors maps the PCI vendor ID exposed in sysfs to the vendor name used in
// Accelerator.Vendor. Anything not listed here is not a GPU this project can
// ever run inference on (virtio-gpu, vmwgfx, ASPEED BMC display), so it is
// skipped rather than reported as an unusable accelerator.
var pciVendors = map[string]string{
	"0x8086": VendorIntel,
	"0x1002": VendorAMD,
	"0x10de": VendorNVIDIA,
}

// intelGenerations maps an Intel GPU's PCI device ID prefix to its graphics
// generation. An Intel GPU outside the table reports generation 0, which
// suppresses the per-generation capability token rather than guessing.
//
// Adding a row has three requirements, all of which fail silently if missed and
// all of which TestIntelGenerationsTableKeys enforces: the key is lowercase,
// keeps the 0x prefix, and is exactly the first four characters of the sysfs
// device ID (0x plus the high byte), because that is the slice the lookup takes.
//
// The table is not exhaustive and does not try to be. It covers the parts that
// plausibly run an always-on detector, which is why the low-power SoCs that end
// up in fanless mini-PCs are here alongside the mainstream mobile parts.
var intelGenerations = map[string]int{
	"0x16": 8,  // Broadwell
	"0x19": 9,  // Skylake
	"0x59": 9,  // Kaby Lake
	"0x3e": 9,  // Coffee Lake, Whiskey Lake
	"0x9b": 9,  // Comet Lake
	"0x87": 9,  // Amber Lake
	"0x31": 9,  // Gemini Lake
	"0x5a": 9,  // Apollo Lake
	"0x8a": 11, // Ice Lake
	"0x4e": 11, // Jasper Lake
	"0x45": 11, // Elkhart Lake
	"0x9a": 12, // Tiger Lake
	"0x4c": 12, // Rocket Lake
	"0x46": 12, // Alder Lake
	"0xa7": 12, // Raptor Lake
	"0x56": 12, // DG2 / Arc
	"0x7d": 12, // Meteor Lake, Arrow Lake
}

// detectAccelerators enumerates the GPUs on the DRM bus under root and records
// how reachable each one is. Everything it reports is a property of the
// hardware and of this mount namespace, so the result is stable for the
// process lifetime and is cached with the rest of the hardware facts.
//
// Enumeration is deliberately independent of the inference build tags, so a
// user whose GPU is present but unreachable is told so rather than shown
// nothing at all.
func detectAccelerators(root string) (accelerators []Accelerator, issues []Issue) {
	cards, err := os.ReadDir(filepath.Join(root, drmClassDir))
	if err != nil {
		// No DRM class directory is normal on a headless server and on any
		// non-Linux host, so only a real read failure is worth recording.
		if !errors.Is(err, fs.ErrNotExist) {
			issues = append(issues, Issue{Probe: ProbeAccelerators, Reason: ReasonReadFailed})
		}
		return nil, issues
	}

	render := indexRenderNodes(root, cards)

	for _, card := range cards {
		if !isCardDir(card.Name()) {
			continue
		}
		devDir := filepath.Join(root, drmClassDir, card.Name(), "device")
		vendorID := readSysfsValue(filepath.Join(devDir, "vendor"))
		vendor, known := pciVendors[strings.ToLower(vendorID)]
		if !known {
			continue
		}
		deviceID := strings.ToLower(readSysfsValue(filepath.Join(devDir, "device")))
		slot := pciSlot(filepath.Join(devDir, "uevent"))

		accelerator := Accelerator{
			Kind:       acceleratorKind(vendor, slot),
			Vendor:     vendor,
			Name:       acceleratorName(vendor, vendorID, deviceID),
			Generation: intelGeneration(vendor, deviceID),
		}
		applyAccessibility(&accelerator, render.state(slot))
		accelerators = append(accelerators, accelerator)
	}

	return accelerators, issues
}

// applyAccessibility records whether this process can reach the GPU's DRM
// render node, and every reason it cannot use the device for inference.
//
// It deliberately answers only what a filesystem probe can establish. Whether
// inference actually runs on a GPU is decided by the classifier's device
// planner, which consults the configured backend and device preference and
// loads the OpenVINO core to enumerate real devices. Deriving a second verdict
// here from backend flags alone produced one that could contradict the
// per-model compute device shown on the same page, and that reported a missing
// GPU driver on images that ship one.
//
// All applicable blockers are reported rather than only the first, because in
// the default containerised deployment they stack, and learning about them one
// restart at a time is the outcome worth avoiding. Order is most fundamental
// first, so the list reads as a checklist.
func applyAccessibility(acc *Accelerator, render renderNodeState) {
	reasons := make([]string, 0, 2)

	// No BirdNET-Go build ships a CUDA or ROCm runtime, so an AMD or NVIDIA GPU
	// is reported as present but never an inference target, rather than hidden.
	if acc.Vendor != VendorIntel {
		reasons = append(reasons, ReasonNoRuntime)
	}

	switch render {
	case renderNodeMissing:
		reasons = append(reasons, ReasonRenderNodeUnavailable)
	case renderNodeDenied:
		reasons = append(reasons, ReasonRenderNodePermission)
	case renderNodeOpen:
		acc.Accessible = true
	}

	if len(reasons) > 0 {
		acc.Reasons = reasons
	}
}

// renderNodeState is how reachable a GPU's DRM render node is from this
// process. The three states are distinct fixes in a containerised deployment,
// which is how BirdNET-Go is installed by default: a missing node means the
// device was never mapped in, whereas a node that cannot be opened means it was
// mapped but the container user is not in the owning group.
type renderNodeState int

const (
	// renderNodeMissing means no render device node exists in this mount
	// namespace.
	renderNodeMissing renderNodeState = iota
	// renderNodeDenied means the device node exists but this process may not
	// open it.
	renderNodeDenied
	// renderNodeOpen means the device node exists and can be opened.
	renderNodeOpen
)

// renderNodeIndex answers how reachable each GPU's DRM render node is. The
// distinction matters inside containers: the kernel exposes the render node in
// sysfs regardless of what the container can reach, so sysfs alone cannot tell
// "no GPU" from "GPU not passed through".
type renderNodeIndex struct {
	// bySlot maps a PCI slot to the state of the render node belonging to it.
	// Only populated when sysfs exposed the render-node-to-device mapping.
	bySlot map[string]renderNodeState
	// mapped reports that sysfs exposed at least one render node, so bySlot is
	// authoritative.
	mapped bool
	// fallback is the best state found among the render nodes under /dev/dri,
	// used when sysfs exposed no mapping to attribute them to a card.
	fallback renderNodeState
}

// state reports how reachable the render node of the GPU at the given PCI slot
// is. A slot the index says nothing about falls back to the best state seen
// anywhere, because "sysfs did not attribute a render node to this card" is not
// evidence that the card has none. Reading the map without the comma-ok would
// turn that silence into renderNodeMissing, which is the false negative that
// tells a user to map in a device they already mapped in.
func (r renderNodeIndex) state(slot string) renderNodeState {
	if r.mapped && slot != "" {
		if state, ok := r.bySlot[slot]; ok {
			return state
		}
	}
	return r.fallback
}

// indexRenderNodes correlates the render nodes sysfs reports with the device
// nodes actually present under /dev/dri, and with whether this process may open
// them. entries is the already-read listing of the DRM class directory, reused
// so the directory is not walked twice.
func indexRenderNodes(root string, entries []os.DirEntry) renderNodeIndex {
	states := make(map[string]renderNodeState)
	idx := renderNodeIndex{bySlot: make(map[string]renderNodeState)}

	if devEntries, err := os.ReadDir(filepath.Join(root, devDRIDir)); err == nil {
		for _, e := range devEntries {
			if !strings.HasPrefix(e.Name(), renderPrefix) {
				continue
			}
			state := renderNodeDenied
			if canOpenRenderNode(filepath.Join(root, devDRIDir, e.Name())) {
				state = renderNodeOpen
			}
			states[e.Name()] = state
			idx.fallback = max(idx.fallback, state)
		}
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, renderPrefix) {
			continue
		}
		// mapped is set only once a render node is actually attributed to a PCI
		// slot. Setting it for any renderD entry would make an empty bySlot
		// authoritative for cards it says nothing about.
		if slot := pciSlot(filepath.Join(root, drmClassDir, name, "device", "uevent")); slot != "" {
			idx.bySlot[slot] = states[name]
			idx.mapped = true
		}
	}
	return idx
}

// canOpenRenderNode reports whether this process can actually open the render
// node for reading and writing, which is what submitting work to it requires.
//
// It performs a real open rather than a permission check. faccessat answers only
// the mode-bit and ACL question, and an open can still fail after that passes: a
// container's device cgroup denies the open without touching the permission
// bits, which is exactly what "docker run -v /dev/dri:/dev/dri" produces when
// the operator meant "--device /dev/dri". Reporting that GPU as reachable would
// be a false green on the very misconfiguration this panel exists to catch.
// SELinux and AppArmor deny at the same layer.
//
// The cost accepted for that accuracy is that opening a DRM node can resume a
// runtime-suspended GPU. That happens at most once per process, behind the
// hardware cache, and only when someone loads the page.
//
// O_NONBLOCK keeps a wedged driver from parking the probe in an uninterruptible
// open while it holds the process-wide hardware mutex.
func canOpenRenderNode(path string) bool {
	file, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

// isCardDir reports whether a DRM class entry is a card device rather than one
// of the per-connector entries the same directory holds ("card0-HDMI-A-1").
func isCardDir(name string) bool {
	suffix, ok := strings.CutPrefix(name, cardPrefix)
	if !ok || suffix == "" {
		return false
	}
	// Digits only. strconv.Atoi would accept a leading sign, so "card+1" and
	// "card-1" would both parse; the intent is "cardN", not "parses as an int".
	for i := range len(suffix) {
		if suffix[i] < '0' || suffix[i] > '9' {
			return false
		}
	}
	return true
}

// acceleratorKind classifies a GPU as integrated or discrete from its PCI
// location: integrated graphics sit on the root complex (bus 00), discrete
// cards sit behind a bridge. When the location is unknown the vendor's usual
// packaging is the fallback.
func acceleratorKind(vendor, slot string) string {
	switch {
	case slot == "":
		if vendor == VendorIntel {
			return AcceleratorIGPU
		}
		return AcceleratorDGPU
	case pciBus(slot) == "00":
		return AcceleratorIGPU
	default:
		return AcceleratorDGPU
	}
}

// pciBus returns the bus component of a PCI slot name ("0000:00:02.0" -> "00"),
// or empty when the slot is not in that form.
func pciBus(slot string) string {
	parts := strings.Split(slot, ":")
	if len(parts) != 3 {
		return ""
	}
	return parts[1]
}

// acceleratorName builds a display name. Marketing names would need a PCI ID
// database this project has no reason to carry, so the name pairs the vendor
// with the raw PCI IDs, which is unambiguous when a host has two GPUs from the
// same vendor and is exactly what a support dump needs.
func acceleratorName(vendor, vendorID, deviceID string) string {
	var label string
	switch vendor {
	case VendorIntel:
		label = "Intel"
	case VendorAMD:
		label = "AMD"
	case VendorNVIDIA:
		label = "NVIDIA"
	default:
		label = vendor
	}
	ids := strings.TrimPrefix(vendorID, "0x")
	if deviceID != "" {
		ids += ":" + strings.TrimPrefix(deviceID, "0x")
	}
	return label + " Graphics [" + ids + "]"
}

// intelGeneration resolves the Intel graphics generation from the PCI device
// ID, returning 0 for other vendors and for device IDs outside the known table.
func intelGeneration(vendor, deviceID string) int {
	if vendor != VendorIntel || len(deviceID) < 4 {
		return 0
	}
	// Device IDs are four hex digits prefixed with 0x; the high byte selects the
	// architecture family.
	return intelGenerations[deviceID[:4]]
}

// pciSlot reads the PCI slot name from a sysfs uevent file, returning empty
// when the file is missing or carries no slot (a non-PCI GPU such as the
// Raspberry Pi's VideoCore).
func pciSlot(ueventPath string) string {
	data, err := os.ReadFile(ueventPath)
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), ueventSlotKey); found {
			return value
		}
	}
	return ""
}

// readSysfsValue reads a single-line sysfs attribute, returning empty on any
// failure. Every sysfs read here is best-effort by design.
func readSysfsValue(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
