package usbsysfs

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// linkCard creates <root>/sys/class/sound/card<n> as a symlink to cardDir, the way
// the kernel exposes sound cards.
func linkCard(t *testing.T, root string, n int, cardDir string) {
	t.Helper()
	classDir := filepath.Join(root, "sys", "class", "sound")
	require.NoError(t, os.MkdirAll(classDir, 0o755))
	require.NoError(t, os.Symlink(cardDir, filepath.Join(classDir, "card"+strconv.Itoa(n))))
}

func TestCardUSBDevice_StopsAtDeviceNode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hub := filepath.Join(root, "sys", "devices", "platform", "xhci-hcd.0", "usb1")
	dev := filepath.Join(hub, "1-1")
	cardDir := filepath.Join(dev, "1-1:1.0", "sound", "card0")
	require.NoError(t, os.MkdirAll(cardDir, 0o755))
	// Both the root hub and the device behind it carry an idVendor; the walk must stop at
	// the nearest one, the device.
	require.NoError(t, os.WriteFile(filepath.Join(hub, "idVendor"), []byte("1d6b\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dev, "idVendor"), []byte("0d8c\n"), 0o644))
	linkCard(t, root, 0, cardDir)

	got, ok := CardUSBDevice(root, 0)

	require.True(t, ok)
	// Compare resolved paths: the temp dir may itself sit behind a symlink.
	want, err := filepath.EvalSymlinks(dev)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestCardUSBDevice_NotFound(t *testing.T) {
	t.Parallel()

	t.Run("card absent", func(t *testing.T) {
		t.Parallel()
		_, ok := CardUSBDevice(t.TempDir(), 0)
		assert.False(t, ok)
	})

	t.Run("card not backed by a USB device", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		cardDir := filepath.Join(root, "sys", "devices", "platform", "snd_aloop.0", "sound", "card0")
		require.NoError(t, os.MkdirAll(cardDir, 0o755))
		linkCard(t, root, 0, cardDir)

		_, ok := CardUSBDevice(root, 0)
		assert.False(t, ok)
	})

	t.Run("card resolving outside the sysfs root", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		// A directory outside <root>/sys that looks like a USB device must never be returned.
		outsideDev := filepath.Join(t.TempDir(), "device")
		outsideCard := filepath.Join(outsideDev, "sound", "card0")
		require.NoError(t, os.MkdirAll(outsideCard, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(outsideDev, "idVendor"), []byte("0d8c\n"), 0o644))
		linkCard(t, root, 0, outsideCard)

		_, ok := CardUSBDevice(root, 0)
		assert.False(t, ok)
	})
}

func TestReadAttr(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "idVendor")
	require.NoError(t, os.WriteFile(path, []byte("  0d8c\n"), 0o644))

	assert.Equal(t, "0d8c", ReadAttr(path))
	assert.Empty(t, ReadAttr(filepath.Join(dir, "missing")))
}
