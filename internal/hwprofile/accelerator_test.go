package hwprofile

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openvinoBackends builds a Backends value with OpenVINO in the requested
// state, so the usability tests can vary only the fact under test.
func openvinoBackends(supported bool, devices ...string) Backends {
	return Backends{
		TFLite:   BackendStatus{Available: true},
		OpenVINO: OpenVINOStatus{Supported: supported, Devices: devices},
	}
}

// evaluateFixture probes a fixture tree and runs it through the production
// backend evaluation, so these tests exercise the same path Detect does rather
// than a reimplementation of it.
func evaluateFixture(t *testing.T, root string, backends Backends) []Accelerator {
	t.Helper()
	accelerators, issues := detectAccelerators(root)
	require.Empty(t, issues)
	return applyBackends(Profile{Accelerators: accelerators}, backends).Accelerators
}

// A discrete NVIDIA card: behind a PCI bridge, so not on bus 00.
var fixtureNvidiaDGPU = map[string]string{
	"sys/class/drm/card1/device/vendor": "0x10de\n",
	"sys/class/drm/card1/device/device": "0x2504\n",
	"sys/class/drm/card1/device/uevent": "DRIVER=nvidia\nPCI_SLOT_NAME=0000:01:00.0\n",
}

// The host sysfs a container sees. Docker mounts the host /sys, so the iGPU is
// visible whether or not the operator mapped /dev/dri in.
var fixtureContainerSysfs = map[string]string{
	"sys/class/drm/card0/device/vendor":      "0x8086\n",
	"sys/class/drm/card0/device/device":      "0x46a6\n",
	"sys/class/drm/card0/device/uevent":      "DRIVER=i915\nPCI_SLOT_NAME=0000:00:02.0\n",
	"sys/class/drm/renderD128/device/uevent": "DRIVER=i915\nPCI_SLOT_NAME=0000:00:02.0\n",
}

// TestDetectAccelerators covers enumeration: which DRM entries are GPUs, what
// hardware facts they carry, and how reachable their render nodes are.
// Usability is decided separately and is covered by TestApplyUsability.
func TestDetectAccelerators(t *testing.T) {
	t.Parallel()

	// A paravirtualised display adapter: present on the DRM bus, never an
	// inference target.
	virtioGPU := map[string]string{
		"sys/class/drm/card0/device/vendor": "0x1af4\n",
		"sys/class/drm/card0/device/uevent": "DRIVER=virtio_gpu\nPCI_SLOT_NAME=0000:00:01.0\n",
	}

	tests := []struct {
		name string
		tree map[string]string
		want []Accelerator
	}{
		{
			name: "intel igpu with an accessible render node",
			tree: fixtureAMD64Desktop,
			want: []Accelerator{{
				Kind:       AcceleratorIGPU,
				Vendor:     VendorIntel,
				Name:       "Intel Graphics [8086:9a49]",
				Generation: 12,
				renderNode: renderNodeOpen,
			}},
		},
		{
			name: "intel igpu in a container with no /dev/dri",
			tree: fixtureContainerSysfs,
			want: []Accelerator{{
				Kind:       AcceleratorIGPU,
				Vendor:     VendorIntel,
				Name:       "Intel Graphics [8086:46a6]",
				Generation: 12,
				renderNode: renderNodeMissing,
			}},
		},
		{
			name: "discrete nvidia card",
			tree: fixtureNvidiaDGPU,
			want: []Accelerator{{
				Kind:       AcceleratorDGPU,
				Vendor:     VendorNVIDIA,
				Name:       "NVIDIA Graphics [10de:2504]",
				renderNode: renderNodeMissing,
			}},
		},
		{
			name: "paravirtualised display adapter is not an accelerator",
			tree: virtioGPU,
			want: nil,
		},
		{
			name: "host with no drm bus",
			tree: nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := writeTree(t, tt.tree)

			accelerators, issues := detectAccelerators(root)

			assert.Equal(t, tt.want, accelerators)
			assert.Empty(t, issues)
		})
	}
}

// TestApplyUsability covers the decision that turns hardware plus backend state
// into "can inference run here, and if not, what has to change".
func TestApplyUsability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		vendor      string
		render      renderNodeState
		backends    Backends
		wantUsable  bool
		wantVia     string
		wantReasons []string
	}{
		{
			name:       "intel igpu reachable through openvino",
			vendor:     VendorIntel,
			render:     renderNodeOpen,
			backends:   openvinoBackends(true, deviceCPU, deviceGPU),
			wantUsable: true,
			wantVia:    ViaOpenVINO,
		},
		{
			name:        "build without openvino",
			vendor:      VendorIntel,
			render:      renderNodeOpen,
			backends:    openvinoBackends(false),
			wantReasons: []string{ReasonOpenVINONotBuilt},
		},
		{
			name:        "openvino build that does not enumerate a gpu",
			vendor:      VendorIntel,
			render:      renderNodeOpen,
			backends:    openvinoBackends(true, deviceCPU),
			wantReasons: []string{ReasonOpenVINODeviceMissing},
		},
		{
			name:        "device never mapped into the container",
			vendor:      VendorIntel,
			render:      renderNodeMissing,
			backends:    openvinoBackends(true, deviceCPU, deviceGPU),
			wantReasons: []string{ReasonRenderNodeUnavailable},
		},
		{
			name:        "device mapped but not openable",
			vendor:      VendorIntel,
			render:      renderNodeDenied,
			backends:    openvinoBackends(true, deviceCPU, deviceGPU),
			wantReasons: []string{ReasonRenderNodePermission},
		},
		{
			// The default install: stock image, no device mapping. Both have to
			// be fixed, so reporting one at a time costs the user a restart each.
			name:        "stock image with no device mapping reports both blockers",
			vendor:      VendorIntel,
			render:      renderNodeMissing,
			backends:    openvinoBackends(false),
			wantReasons: []string{ReasonOpenVINONotBuilt, ReasonRenderNodeUnavailable},
		},
		{
			// With the device unreachable, "OpenVINO lists no GPU" is noise: it
			// could not list one either way.
			name:        "unreachable device suppresses the device-missing reason",
			vendor:      VendorIntel,
			render:      renderNodeMissing,
			backends:    openvinoBackends(true, deviceCPU),
			wantReasons: []string{ReasonRenderNodeUnavailable},
		},
		{
			name:        "nvidia has no runtime in any build",
			vendor:      VendorNVIDIA,
			render:      renderNodeOpen,
			backends:    openvinoBackends(true, deviceCPU, deviceGPU),
			wantReasons: []string{ReasonNoRuntime},
		},
		{
			name:        "amd has no runtime in any build",
			vendor:      VendorAMD,
			render:      renderNodeOpen,
			backends:    openvinoBackends(true, deviceCPU, deviceGPU),
			wantReasons: []string{ReasonNoRuntime},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			accelerator := Accelerator{Vendor: tt.vendor}
			openvinoGPU := tt.backends.OpenVINO.Supported &&
				slices.Contains(tt.backends.OpenVINO.Devices, deviceGPU)

			applyUsability(&accelerator, tt.backends, openvinoGPU, tt.render)

			assert.Equal(t, tt.wantUsable, accelerator.Usable)
			assert.Equal(t, tt.wantVia, accelerator.Via)
			assert.Equal(t, tt.wantReasons, accelerator.Reasons)
		})
	}
}

// TestApplyBackendsUsesTheProbedRenderNodeState joins the two halves: the
// render-node state recorded at probe time has to survive into the usability
// decision, since that decision is made later and never re-reads the
// filesystem.
func TestApplyBackendsUsesTheProbedRenderNodeState(t *testing.T) {
	t.Parallel()

	t.Run("container without a device mapping", func(t *testing.T) {
		t.Parallel()
		root := writeTree(t, fixtureContainerSysfs)

		accelerators := evaluateFixture(t, root, openvinoBackends(false))

		require.Len(t, accelerators, 1)
		assert.False(t, accelerators[0].Usable)
		assert.Equal(t,
			[]string{ReasonOpenVINONotBuilt, ReasonRenderNodeUnavailable},
			accelerators[0].Reasons)
	})

	t.Run("device mapped but not openable by the container user", func(t *testing.T) {
		t.Parallel()
		if isRunningAsRoot() {
			t.Skip("root bypasses the permission bits this test relies on")
		}
		root := writeTree(t, fixtureContainerSysfs, map[string]string{"dev/dri/renderD128": ""})
		makeUnreadable(t, root, "dev/dri/renderD128")

		accelerators := evaluateFixture(t, root, openvinoBackends(true, deviceCPU, deviceGPU))

		require.Len(t, accelerators, 1)
		// Distinct from a missing node: the fix is granting the render group,
		// not adding the device.
		assert.Equal(t, []string{ReasonRenderNodePermission}, accelerators[0].Reasons)
	})

	t.Run("device mapped and openable", func(t *testing.T) {
		t.Parallel()
		root := writeTree(t, fixtureContainerSysfs, map[string]string{"dev/dri/renderD128": ""})

		accelerators := evaluateFixture(t, root, openvinoBackends(true, deviceCPU, deviceGPU))

		require.Len(t, accelerators, 1)
		assert.True(t, accelerators[0].Usable)
		assert.Equal(t, ViaOpenVINO, accelerators[0].Via)
		assert.Empty(t, accelerators[0].Reasons)
	})
}

// TestApplyBackendsDoesNotMutateItsInput guards the cache: Detect evaluates the
// shared hardware snapshot on every call, so writing usability through to it
// would let one call's backend state leak into the next.
func TestApplyBackendsDoesNotMutateItsInput(t *testing.T) {
	t.Parallel()

	hardware := Profile{Accelerators: []Accelerator{
		{Vendor: VendorIntel, renderNode: renderNodeOpen},
	}}

	usable := applyBackends(hardware, openvinoBackends(true, deviceCPU, deviceGPU))
	require.True(t, usable.Accelerators[0].Usable)

	assert.False(t, hardware.Accelerators[0].Usable, "the input profile must be untouched")
	assert.Empty(t, hardware.Accelerators[0].Reasons)

	// Re-evaluating the same untouched snapshot against a weaker backend state
	// must produce the weaker answer, not the earlier one.
	unusable := applyBackends(hardware, openvinoBackends(false))
	assert.False(t, unusable.Accelerators[0].Usable)
	assert.Equal(t, []string{ReasonOpenVINONotBuilt}, unusable.Accelerators[0].Reasons)
}

func TestDetectAcceleratorsSkipsConnectorEntries(t *testing.T) {
	t.Parallel()

	root := writeTree(t, fixtureAMD64Desktop)

	accelerators, _ := detectAccelerators(root)

	// card0-HDMI-A-1 sits in the same directory as card0 but is a connector,
	// not a device, and has no vendor attribute to read.
	require.Len(t, accelerators, 1)
	assert.Equal(t, VendorIntel, accelerators[0].Vendor)
}

func TestIsCardDir(t *testing.T) {
	t.Parallel()

	assert.True(t, isCardDir("card0"))
	assert.True(t, isCardDir("card12"))
	assert.False(t, isCardDir("card0-HDMI-A-1"), "connector entries are not cards")
	assert.False(t, isCardDir("card"), "the bare prefix is not a card")
	assert.False(t, isCardDir("renderD128"))
	assert.False(t, isCardDir("version"))
}

func TestAcceleratorKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		vendor string
		slot   string
		want   string
	}{
		{name: "root complex means integrated", vendor: VendorIntel, slot: "0000:00:02.0", want: AcceleratorIGPU},
		{name: "behind a bridge means discrete", vendor: VendorNVIDIA, slot: "0000:01:00.0", want: AcceleratorDGPU},
		{name: "amd apu on the root complex", vendor: VendorAMD, slot: "0000:00:01.0", want: AcceleratorIGPU},
		{name: "unknown location falls back to intel packaging", vendor: VendorIntel, slot: "", want: AcceleratorIGPU},
		{name: "unknown location falls back to nvidia packaging", vendor: VendorNVIDIA, slot: "", want: AcceleratorDGPU},
		{name: "malformed slot falls back to packaging", vendor: VendorAMD, slot: "garbage", want: AcceleratorDGPU},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, acceleratorKind(tt.vendor, tt.slot))
		})
	}
}

func TestIntelGeneration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		vendor   string
		deviceID string
		want     int
	}{
		{name: "tiger lake iris xe", vendor: VendorIntel, deviceID: "0x9a49", want: 12},
		{name: "alder lake", vendor: VendorIntel, deviceID: "0x46a6", want: 12},
		{name: "ice lake", vendor: VendorIntel, deviceID: "0x8a52", want: 11},
		{name: "kaby lake", vendor: VendorIntel, deviceID: "0x5916", want: 9},
		{name: "unknown intel part", vendor: VendorIntel, deviceID: "0x0042", want: 0},
		{name: "non-intel vendor never has a generation", vendor: VendorNVIDIA, deviceID: "0x9a49", want: 0},
		{name: "missing device id", vendor: VendorIntel, deviceID: "", want: 0},
		{name: "truncated device id", vendor: VendorIntel, deviceID: "0x9", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, intelGeneration(tt.vendor, tt.deviceID))
		})
	}
}

func TestPciSlot(t *testing.T) {
	t.Parallel()

	root := writeTree(t, fixtureAMD64Desktop)

	assert.Equal(t, "0000:00:02.0", pciSlot(root+"/sys/class/drm/card0/device/uevent"))
	assert.Empty(t, pciSlot(root+"/sys/class/drm/card0/device/missing"), "a missing uevent yields no slot")
}

func TestRenderNodeIndexFallsBackWhenSysfsExposesNoMapping(t *testing.T) {
	t.Parallel()

	// Some kernels expose the device node without a sysfs renderD entry. The
	// index then cannot attribute it to a card, so presence of any render node
	// is the best available answer rather than reporting none.
	root := writeTree(t, map[string]string{
		"sys/class/drm/card0/device/vendor": "0x8086\n",
		"sys/class/drm/card0/device/device": "0x9a49\n",
		"sys/class/drm/card0/device/uevent": "DRIVER=i915\nPCI_SLOT_NAME=0000:00:02.0\n",
		"dev/dri/renderD128":                "",
	})

	accelerators := evaluateFixture(t, root, openvinoBackends(true, deviceCPU, deviceGPU))

	require.Len(t, accelerators, 1)
	assert.True(t, accelerators[0].Usable)
	assert.Equal(t, ViaOpenVINO, accelerators[0].Via)
}
