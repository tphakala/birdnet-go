package hwprofile

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
// hardware facts they carry, and whether their render node is reachable.
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
				Accessible: true,
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
				Reasons:    []string{ReasonRenderNodeUnavailable},
			}},
		},
		{
			// The card is reachable, but no build ships a runtime for it, so the
			// panel reports both facts rather than implying it will be used.
			name: "discrete nvidia card is reachable but has no runtime",
			tree: mergeTrees(fixtureNvidiaDGPU, map[string]string{
				"sys/class/drm/renderD128/device/uevent": "DRIVER=nvidia\nPCI_SLOT_NAME=0000:01:00.0\n",
				"dev/dri/renderD128":                     "",
			}),
			want: []Accelerator{{
				Kind:       AcceleratorDGPU,
				Vendor:     VendorNVIDIA,
				Name:       "NVIDIA Graphics [10de:2504]",
				Accessible: true,
				Reasons:    []string{ReasonNoRuntime},
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

// TestDetectAcceleratorsReportsTwoIdenticalCards guards the multi-GPU case the
// rest of the stack has to survive: two cards of the same model produce
// identical names, because the name carries no PCI slot.
func TestDetectAcceleratorsReportsTwoIdenticalCards(t *testing.T) {
	t.Parallel()

	root := writeTree(t, map[string]string{
		"sys/class/drm/card0/device/vendor": "0x10de\n",
		"sys/class/drm/card0/device/device": "0x2504\n",
		"sys/class/drm/card0/device/uevent": "DRIVER=nvidia\nPCI_SLOT_NAME=0000:01:00.0\n",
		"sys/class/drm/card1/device/vendor": "0x10de\n",
		"sys/class/drm/card1/device/device": "0x2504\n",
		"sys/class/drm/card1/device/uevent": "DRIVER=nvidia\nPCI_SLOT_NAME=0000:02:00.0\n",
	})

	accelerators, _ := detectAccelerators(root)

	require.Len(t, accelerators, 2)
	assert.Equal(t, accelerators[0].Name, accelerators[1].Name,
		"identical cards share a name, so nothing downstream may treat the name as unique")
}

// TestApplyAccessibility covers the decision that turns a render-node state and
// a vendor into "can this GPU be an inference target, and if not, what has to
// change".
func TestApplyAccessibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		vendor         string
		render         renderNodeState
		wantAccessible bool
		wantReasons    []string
	}{
		{
			name:           "intel igpu with an open render node",
			vendor:         VendorIntel,
			render:         renderNodeOpen,
			wantAccessible: true,
		},
		{
			name:        "device never mapped into the container",
			vendor:      VendorIntel,
			render:      renderNodeMissing,
			wantReasons: []string{ReasonRenderNodeUnavailable},
		},
		{
			name:        "device mapped but not openable",
			vendor:      VendorIntel,
			render:      renderNodeDenied,
			wantReasons: []string{ReasonRenderNodePermission},
		},
		{
			// Both facts are reported: the card cannot be used by any build, and
			// separately it was never mapped in. Reporting one at a time costs
			// the user a restart each.
			name:        "nvidia card that is also unmapped reports both blockers",
			vendor:      VendorNVIDIA,
			render:      renderNodeMissing,
			wantReasons: []string{ReasonNoRuntime, ReasonRenderNodeUnavailable},
		},
		{
			name:           "amd card is reachable but has no runtime",
			vendor:         VendorAMD,
			render:         renderNodeOpen,
			wantAccessible: true,
			wantReasons:    []string{ReasonNoRuntime},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			accelerator := Accelerator{Vendor: tt.vendor}

			applyAccessibility(&accelerator, tt.render)

			assert.Equal(t, tt.wantAccessible, accelerator.Accessible)
			assert.Equal(t, tt.wantReasons, accelerator.Reasons)
		})
	}
}

// TestDetectAcceleratorsInDefaultContainerInstall covers the deployment
// BirdNET-Go actually ships: a Docker container. Its /sys is the host's, so the
// iGPU is always visible there, while /dev/dri only appears when the operator
// maps it in.
func TestDetectAcceleratorsInDefaultContainerInstall(t *testing.T) {
	t.Parallel()

	t.Run("no device mapping", func(t *testing.T) {
		t.Parallel()
		root := writeTree(t, fixtureContainerSysfs)

		accelerators, _ := detectAccelerators(root)

		require.Len(t, accelerators, 1)
		assert.False(t, accelerators[0].Accessible)
		assert.Equal(t, []string{ReasonRenderNodeUnavailable}, accelerators[0].Reasons)
	})

	t.Run("device mapped but not accessible to the container user", func(t *testing.T) {
		t.Parallel()
		skipIfPermissionBitsIneffective(t)
		root := writeTree(t, fixtureContainerSysfs, map[string]string{"dev/dri/renderD128": ""})
		makeUnreadable(t, root, "dev/dri/renderD128")

		accelerators, _ := detectAccelerators(root)

		require.Len(t, accelerators, 1)
		assert.False(t, accelerators[0].Accessible)
		// Distinct from a missing node: the fix is granting the render group,
		// not adding the device.
		assert.Equal(t, []string{ReasonRenderNodePermission}, accelerators[0].Reasons)
	})

	t.Run("device mapped and accessible", func(t *testing.T) {
		t.Parallel()
		root := writeTree(t, fixtureContainerSysfs, map[string]string{"dev/dri/renderD128": ""})

		accelerators, _ := detectAccelerators(root)

		require.Len(t, accelerators, 1)
		assert.True(t, accelerators[0].Accessible)
		assert.Empty(t, accelerators[0].Reasons)
	})
}

// TestDetectAcceleratorsRecordsIssueWhenDrmIsUnreadable mirrors the board
// probe's equivalent test. A DRM directory that exists but cannot be listed is
// "could not tell", which must not be reported as "this host has no GPU".
func TestDetectAcceleratorsRecordsIssueWhenDrmIsUnreadable(t *testing.T) {
	t.Parallel()
	skipIfPermissionBitsIneffective(t)

	root := writeTree(t, fixtureContainerSysfs)
	makeUnreadable(t, root, drmClassDir)

	accelerators, issues := detectAccelerators(root)

	assert.Empty(t, accelerators)
	assert.Equal(t, []Issue{{Probe: ProbeAccelerators, Reason: ReasonReadFailed}}, issues)
}

func TestDetectAcceleratorsSkipsConnectorEntries(t *testing.T) {
	t.Parallel()

	// The connector carries a vendor attribute here, so the only thing that can
	// exclude it is isCardDir. Without it the panel would list a display output
	// as a GPU.
	root := writeTree(t, fixtureAMD64Desktop, map[string]string{
		"sys/class/drm/card0-HDMI-A-1/device/vendor": "0x8086\n",
	})

	accelerators, _ := detectAccelerators(root)

	require.Len(t, accelerators, 1)
	assert.Equal(t, VendorIntel, accelerators[0].Vendor)
}

func TestIsCardDir(t *testing.T) {
	t.Parallel()

	assert.True(t, isCardDir("card0"))
	assert.True(t, isCardDir("card12"))
	assert.False(t, isCardDir("card0-HDMI-A-1"), "connector entries are not cards")
	assert.False(t, isCardDir("card"), "the bare prefix is not a card")
	assert.False(t, isCardDir("card+1"), "a sign is not a card index")
	assert.False(t, isCardDir("card-1"), "a sign is not a card index")
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
		{name: "intel igpu sits on the root complex", vendor: VendorIntel, slot: "0000:00:02.0", want: AcceleratorIGPU},
		{name: "behind a bridge means discrete", vendor: VendorNVIDIA, slot: "0000:01:00.0", want: AcceleratorDGPU},
		{
			// Renoir and later place the integrated Radeon behind an internal
			// bridge, so the bus heuristic calls it discrete. Asserted as-is so
			// the limitation is visible rather than assumed away.
			name:   "modern amd apu is misread as discrete by the bus heuristic",
			vendor: VendorAMD, slot: "0000:04:00.0", want: AcceleratorDGPU,
		},
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

func TestAcceleratorName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Intel Graphics [8086:9a49]", acceleratorName(VendorIntel, "0x8086", "0x9a49"))
	assert.Equal(t, "AMD Graphics [1002:73ff]", acceleratorName(VendorAMD, "0x1002", "0x73ff"))
	assert.Equal(t, "NVIDIA Graphics [10de:2504]", acceleratorName(VendorNVIDIA, "0x10de", "0x2504"))
	assert.Equal(t, "Intel Graphics [8086]", acceleratorName(VendorIntel, "0x8086", ""),
		"an unreadable device id still yields a usable name")
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
		{name: "alder lake n100", vendor: VendorIntel, deviceID: "0x46d4", want: 12},
		{name: "ice lake", vendor: VendorIntel, deviceID: "0x8a52", want: 11},
		{name: "kaby lake", vendor: VendorIntel, deviceID: "0x5916", want: 9},
		{name: "jasper lake", vendor: VendorIntel, deviceID: "0x4e61", want: 11},
		{name: "gemini lake", vendor: VendorIntel, deviceID: "0x3185", want: 9},
		{name: "apollo lake", vendor: VendorIntel, deviceID: "0x5a85", want: 9},
		{
			// Outside the table on purpose: an unknown part suppresses the
			// per-generation token rather than guessing a wrong one.
			name: "unknown intel part", vendor: VendorIntel, deviceID: "0x0042", want: 0,
		},
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

// TestIntelGenerationsTableKeys pins the two invariants a maintainer adding a
// row has to satisfy. Both are silent failures: a key that does not match the
// shape the lookup produces simply never fires.
func TestIntelGenerationsTableKeys(t *testing.T) {
	t.Parallel()

	for key, generation := range intelGenerations {
		assert.Len(t, key, 4, "key %q must be 0x plus the device ID's high byte", key)
		assert.Equal(t, strings.ToLower(key), key, "key %q must be lowercase to match the normalized lookup", key)
		assert.True(t, strings.HasPrefix(key, "0x"), "key %q must keep the 0x prefix", key)
		assert.Positive(t, generation, "key %q must map to a real generation", key)
	}
}

func TestPciSlot(t *testing.T) {
	t.Parallel()

	root := writeTree(t, fixtureAMD64Desktop)

	assert.Equal(t, "0000:00:02.0", pciSlot(filepath.Join(root, drmClassDir, "card0", "device", "uevent")))
	assert.Empty(t, pciSlot(filepath.Join(root, drmClassDir, "card0", "device", "missing")),
		"a missing uevent yields no slot")
}

func TestRenderNodeIndexFallsBackWhenSysfsExposesNoMapping(t *testing.T) {
	t.Parallel()

	// Some kernels expose the device node without a sysfs renderD entry that
	// resolves to a PCI slot. The index then cannot attribute it to a card, so
	// presence of any render node is the best available answer rather than a
	// false "you never mapped the device in".
	root := writeTree(t, map[string]string{
		"sys/class/drm/card0/device/vendor": "0x8086\n",
		"sys/class/drm/card0/device/device": "0x9a49\n",
		"sys/class/drm/card0/device/uevent": "DRIVER=i915\nPCI_SLOT_NAME=0000:00:02.0\n",
		// Present in sysfs but with no slot to attribute it to.
		"sys/class/drm/renderD128/device/uevent": "DRIVER=i915\n",
		"dev/dri/renderD128":                     "",
	})

	accelerators, _ := detectAccelerators(root)

	require.Len(t, accelerators, 1)
	assert.True(t, accelerators[0].Accessible,
		"an unattributable render node must not be read as an absent one")
}
