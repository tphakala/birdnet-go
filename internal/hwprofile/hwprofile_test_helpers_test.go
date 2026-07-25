package hwprofile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The fixtures below are sysfs/procfs trees captured from the hosts this
// project actually runs on. They are declared as Go literals rather than
// committed files because device-tree properties are NUL-terminated, and a
// NUL-bearing file in the repository is both invisible in review and easy for
// an editor to silently mangle.

// Device-tree fixtures. The trailing NUL on `model` and the NUL separators in
// `compatible` are exactly what the kernel exposes; parsing them is the point.
var (
	fixturePi5 = map[string]string{
		"proc/device-tree/model":      "Raspberry Pi 5 Model B Rev 1.0\x00",
		"proc/device-tree/compatible": "raspberrypi,5-model-b\x00brcm,bcm2712\x00",
	}
	fixturePi4 = map[string]string{
		"proc/device-tree/model":      "Raspberry Pi 4 Model B Rev 1.4\x00",
		"proc/device-tree/compatible": "raspberrypi,4-model-b\x00brcm,bcm2711\x00",
	}
	fixturePi3 = map[string]string{
		"proc/device-tree/model":      "Raspberry Pi 3 Model B Plus Rev 1.3\x00",
		"proc/device-tree/compatible": "raspberrypi,3-model-b-plus\x00brcm,bcm2837\x00",
	}
	// fixturePi5SysfsOnly exposes the device tree only through the sysfs
	// firmware node, which is the layout on kernels that do not mount the
	// /proc/device-tree alias.
	fixturePi5SysfsOnly = map[string]string{
		"sys/firmware/devicetree/base/model":      "Raspberry Pi 5 Model B Rev 1.0\x00",
		"sys/firmware/devicetree/base/compatible": "raspberrypi,5-model-b\x00brcm,bcm2712\x00",
	}
)

// fixtureAMD64Desktop is a generic x86 host with a Tiger Lake Iris Xe iGPU and
// a usable render node. The card0-HDMI-A-1 entry is a DRM connector, not a
// card, and must be skipped during enumeration.
var fixtureAMD64Desktop = map[string]string{
	"sys/class/drm/card0/device/vendor":      "0x8086\n",
	"sys/class/drm/card0/device/device":      "0x9a49\n",
	"sys/class/drm/card0/device/uevent":      "DRIVER=i915\nPCI_ID=8086:9A49\nPCI_SLOT_NAME=0000:00:02.0\n",
	"sys/class/drm/card0-HDMI-A-1/status":    "connected\n",
	"sys/class/drm/renderD128/device/uevent": "DRIVER=i915\nPCI_ID=8086:9A49\nPCI_SLOT_NAME=0000:00:02.0\n",
	"dev/dri/renderD128":                     "",
}

// fixtureCgroupLimited is a container capped at 512 MiB through cgroup v2, the
// shape that makes host RAM reporting misleading.
var fixtureCgroupLimited = map[string]string{
	"proc/self/cgroup":         "0::/\n",
	"sys/fs/cgroup/memory.max": "536870912\n",
}

// cgroupLimitBytes is the memory ceiling fixtureCgroupLimited declares.
const cgroupLimitBytes int64 = 512 * 1024 * 1024

// writeTree materializes a fixture as a temporary filesystem tree and returns
// its root. Entries whose value is the empty string still create a file, which
// is how device nodes such as /dev/dri/renderD128 are represented: only their
// presence matters.
func writeTree(t *testing.T, trees ...map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, tree := range trees {
		for rel, content := range tree {
			full := filepath.Join(root, rel)
			require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
			require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
		}
	}
	return root
}

// isRunningAsRoot reports whether the test process can read files regardless of
// their permission bits, which makes any "unreadable file" assertion vacuous.
func isRunningAsRoot() bool {
	return os.Geteuid() == 0
}

// makeUnreadable strips read permission from a file inside a fixture tree, so a
// probe sees a path that exists but cannot be opened.
func makeUnreadable(t *testing.T, root, rel string) {
	t.Helper()
	require.NoError(t, os.Chmod(filepath.Join(root, rel), 0o000))
}
