package hwprofile

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
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

// mergeTrees combines fixtures into one tree definition, so a test can compose
// "a host with a discrete card that also has a render node" without restating
// either fixture.
func mergeTrees(trees ...map[string]string) map[string]string {
	merged := make(map[string]string)
	for _, tree := range trees {
		maps.Copy(merged, tree)
	}
	return merged
}

// skipIfPermissionBitsIneffective skips a test that depends on chmod actually
// denying access.
//
// Two hosts cannot honour that. Root bypasses the bits entirely. Windows, which
// this repo's CI does run this package on, maps chmod onto the read-only
// attribute and keeps reads working, and its Geteuid returns -1 so a root check
// alone would not catch it: the test would fail on CI rather than skip.
func skipIfPermissionBitsIneffective(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not deny reads on Windows, so the unreadable-path case cannot be staged")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the permission bits this test relies on")
	}
}

// makeUnreadable strips permissions from a path inside a fixture tree, so a
// probe sees something that exists but cannot be read. The mode is restored on
// cleanup so t.TempDir can remove the tree.
func makeUnreadable(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, rel)
	info, err := os.Stat(full)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(full, 0o000))
	t.Cleanup(func() {
		assert.NoError(t, os.Chmod(full, info.Mode().Perm()), "restoring the mode lets t.TempDir clean up")
	})
}
