package hwprofile

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Accelerator kinds reported in Accelerator.Kind.
const (
	// AcceleratorIGPU is a GPU integrated into the CPU package.
	AcceleratorIGPU = "igpu"
	// AcceleratorDGPU is a discrete GPU on its own PCI card.
	AcceleratorDGPU = "dgpu"
)

// Accelerator vendors reported in Accelerator.Vendor.
const (
	VendorIntel  = "intel"
	VendorAMD    = "amd"
	VendorNVIDIA = "nvidia"
)

// ViaOpenVINO is the Accelerator.Via value for a GPU reached through OpenVINO.
const ViaOpenVINO = "openvino"

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

// intelGenerations maps the high byte of an Intel GPU's PCI device ID to its
// graphics generation. The table covers the parts that can plausibly run this
// project; an Intel GPU outside it reports generation 0, which suppresses the
// per-generation capability token rather than guessing.
var intelGenerations = map[string]int{
	"0x19": 9,  // Skylake
	"0x59": 9,  // Kaby Lake
	"0x3e": 9,  // Coffee Lake
	"0x9b": 9,  // Comet Lake
	"0x87": 9,  // Amber / Whiskey Lake
	"0x8a": 11, // Ice Lake
	"0x9a": 12, // Tiger Lake
	"0x4c": 12, // Rocket Lake
	"0x46": 12, // Alder Lake
	"0xa7": 12, // Raptor Lake
	"0x56": 12, // DG2 / Arc
	"0x7d": 12, // Meteor Lake
}

// detectAccelerators enumerates the GPUs on the DRM bus under root and records
// how reachable each one's render node is. It deliberately stops short of
// deciding usability, which depends on backend state that outlives no cache;
// evaluateBackends makes that call.
//
// Enumeration is deliberately independent of the inference build tags: OpenVINO
// device queries only compile into an OpenVINO build, so a stock image would
// otherwise give a user with an Intel iGPU no signal at all that different
// packaging would give them GPU offload.
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

		accelerators = append(accelerators, Accelerator{
			Kind:       acceleratorKind(vendor, slot),
			Vendor:     vendor,
			Name:       acceleratorName(vendor, vendorID, deviceID),
			Generation: intelGeneration(vendor, deviceID),
			renderNode: render.state(slot),
		})
	}

	return accelerators, issues
}

// applyUsability decides whether inference can run on acc now, and records
// every reason it cannot.
//
// All applicable blockers are reported rather than only the first, because the
// default deployment is a Docker container and its two failure modes stack: a
// user on the stock image with no /dev/dri mapping has to fix both the image
// and the compose file, and being told about one at a time costs a round trip
// each. Order is most fundamental first, so the panel reads as a checklist.
func applyUsability(acc *Accelerator, backends Backends, openvinoGPU bool, render renderNodeState) {
	if acc.Vendor != VendorIntel {
		// No BirdNET-Go build ships a CUDA or ROCm runtime, so an AMD or NVIDIA
		// GPU is reported as present but unreachable rather than hidden.
		acc.Reasons = []string{ReasonNoRuntime}
		return
	}

	reasons := make([]string, 0, 2)
	if !backends.OpenVINO.Supported {
		reasons = append(reasons, ReasonOpenVINONotBuilt)
	}
	switch render {
	case renderNodeMissing:
		reasons = append(reasons, ReasonRenderNodeUnavailable)
	case renderNodeDenied:
		reasons = append(reasons, ReasonRenderNodePermission)
	case renderNodeOpen:
		// The device is reachable, so a missing OpenVINO GPU device is a runtime
		// problem rather than a packaging one, and only worth reporting on a
		// build that could have used it.
		if backends.OpenVINO.Supported && !openvinoGPU {
			reasons = append(reasons, ReasonOpenVINODeviceMissing)
		}
	}

	if len(reasons) == 0 {
		acc.Usable = true
		acc.Via = ViaOpenVINO
		return
	}
	acc.Reasons = reasons
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
// is. An unmapped or unknown slot falls back to the best state seen anywhere,
// which is the most useful answer available rather than a false negative.
func (r renderNodeIndex) state(slot string) renderNodeState {
	if r.mapped && slot != "" {
		return r.bySlot[slot]
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
			if canOpenReadWrite(filepath.Join(root, devDRIDir, e.Name())) {
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
		idx.mapped = true
		if slot := pciSlot(filepath.Join(root, drmClassDir, name, "device", "uevent")); slot != "" {
			idx.bySlot[slot] = states[name]
		}
	}
	return idx
}

// canOpenReadWrite reports whether this process may open path for reading and
// writing. Submitting work to a DRM render node needs read-write access, so a
// container that maps the device without granting its group can see the node
// and still not use it.
func canOpenReadWrite(path string) bool {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
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
	_, err := strconv.Atoi(suffix)
	return err == nil
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
