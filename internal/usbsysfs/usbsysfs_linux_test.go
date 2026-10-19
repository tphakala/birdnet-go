package usbsysfs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// walkDepthLimit is the upward-walk bound (12 in maxWalkDepth) as a literal rather than
// maxWalkDepth, so changing the bound fails the depth test instead of silently
// moving its fixture. The walk checks the card directory first, then its parents,
// for this many directories in total.
const walkDepthLimit = 12

func TestCardUSBDevice_DepthBound(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		vendorHeight int // levels above the card directory that carry the idVendor
		wantFound    bool
	}{
		{"last directory the walk checks", walkDepthLimit - 1, true},
		{"first directory beyond the bound", walkDepthLimit, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			// The chain is deeper than the bound, so the sysfs root never ends the walk first.
			cardDir := filepath.Join(root, "sys", "devices")
			for i := range walkDepthLimit + 3 {
				cardDir = filepath.Join(cardDir, "d"+strconv.Itoa(i))
			}
			require.NoError(t, os.MkdirAll(cardDir, 0o755))
			vendorDir := cardDir
			for range tt.vendorHeight {
				vendorDir = filepath.Dir(vendorDir)
			}
			require.NoError(t, os.WriteFile(filepath.Join(vendorDir, "idVendor"), []byte("0d8c\n"), 0o644))
			linkCard(t, root, 0, cardDir)

			got, ok := CardUSBDevice(root, 0)

			require.Equal(t, tt.wantFound, ok)
			if tt.wantFound {
				want, err := filepath.EvalSymlinks(vendorDir)
				require.NoError(t, err)
				assert.Equal(t, want, got)
			}
		})
	}
}

func TestCardUSBDevice_MultiDigitCardNumber(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	devices := make(map[int]string)
	for _, card := range []int{2, 10} {
		dev := filepath.Join(root, "sys", "devices", "dev"+strconv.Itoa(card))
		cardDir := filepath.Join(dev, "sound", "card"+strconv.Itoa(card))
		require.NoError(t, os.MkdirAll(cardDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dev, "idVendor"), []byte("0d8c\n"), 0o644))
		linkCard(t, root, card, cardDir)
		devices[card] = dev
	}

	for card, dev := range devices {
		got, ok := CardUSBDevice(root, card)

		require.True(t, ok, "card %d", card)
		want, err := filepath.EvalSymlinks(dev)
		require.NoError(t, err)
		assert.Equal(t, want, got, "card %d", card)
	}
}

func TestCardUSBDevice_SymlinkedRoot(t *testing.T) {
	t.Parallel()
	realRoot := t.TempDir()
	dev := filepath.Join(realRoot, "sys", "devices", "usb1")
	cardDir := filepath.Join(dev, "sound", "card0")
	require.NoError(t, os.MkdirAll(cardDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dev, "idVendor"), []byte("0d8c\n"), 0o644))
	linkCard(t, realRoot, 0, cardDir)
	// Reach the same tree through a symlinked root, as a symlinked temp dir or a macOS
	// /var -> /private/var path would.
	linkRoot := filepath.Join(t.TempDir(), "root")
	require.NoError(t, os.Symlink(realRoot, linkRoot))

	got, ok := CardUSBDevice(linkRoot, 0)

	require.True(t, ok)
	want, err := filepath.EvalSymlinks(dev)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestReadAttr(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "idVendor")
	require.NoError(t, os.WriteFile(path, []byte("  0d8c\n"), 0o644))

	assert.Equal(t, "0d8c", ReadAttr(path))
	assert.Empty(t, ReadAttr(filepath.Join(dir, "missing")))

	// The bound is a literal so changing maxAttrBytes fails this test.
	big := filepath.Join(dir, "big")
	require.NoError(t, os.WriteFile(big, []byte(strings.Repeat("a", 5000)), 0o644))
	assert.Len(t, ReadAttr(big), 4096)
}
