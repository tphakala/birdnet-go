//go:build linux

package audiocore

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFakeProc builds a fake <root>/proc/asound tree for resolveUSBIdentity to
// read: the cards listing plus an optional usbid file per card.
func writeFakeProc(t *testing.T, cards string, usbids map[int]string) string {
	t.Helper()
	root := t.TempDir()
	asound := filepath.Join(root, "proc", "asound")
	require.NoError(t, os.MkdirAll(asound, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(asound, "cards"), []byte(cards), 0o644))
	for card, id := range usbids {
		dir := filepath.Join(asound, "card"+strconv.Itoa(card))
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "usbid"), []byte(id), 0o644))
	}
	return root
}

func TestResolveUSBIdentity_AMD64(t *testing.T) {
	t.Parallel()
	root := writeFakeProc(t, procCardsAMD64, map[int]string{1: "0b33:0024\n"})

	// Card 1 is the ZOOM AMS-24 USB interface.
	ident := resolveUSBIdentity(":1,0", root)
	assert.Equal(t, "usb-0000:00:14.0-3", ident.BusPath)
	assert.Equal(t, "0b33", ident.VendorID)
	assert.Equal(t, "0024", ident.ProductID)
	assert.Empty(t, ident.Serial, "no sysfs serial in fake tree")
	assert.Equal(t, "usb-path:usb-0000:00:14.0-3", ident.stableToken())
}

func TestResolveUSBIdentity_RPi5(t *testing.T) {
	t.Parallel()
	root := writeFakeProc(t, procCardsRPi5, map[int]string{0: "0d8c:0014\n"})

	// Card 0 is the C-Media USB Audio Device.
	ident := resolveUSBIdentity(":0,0", root)
	assert.Equal(t, "usb-xhci-hcd.0-1", ident.BusPath)
	assert.Equal(t, "0d8c", ident.VendorID)
	assert.Equal(t, "0014", ident.ProductID)
	assert.Equal(t, "usb-path:usb-xhci-hcd.0-1", ident.stableToken())
}

func TestResolveUSBIdentity_NonUSBCardYieldsZero(t *testing.T) {
	t.Parallel()
	root := writeFakeProc(t, procCardsAMD64, nil)

	// Card 0 is the snd-aloop Loopback (not USB).
	ident := resolveUSBIdentity(":0,0", root)
	assert.Empty(t, ident.BusPath)
	assert.Empty(t, ident.VendorID)
	assert.False(t, ident.hasStableID())
	assert.Empty(t, ident.stableToken())
}

func TestResolveUSBIdentity_MissingCardYieldsZero(t *testing.T) {
	t.Parallel()
	root := writeFakeProc(t, procCardsRPi5, nil)

	assert.False(t, resolveUSBIdentity(":9,0", root).hasStableID(), "absent card")
	assert.False(t, resolveUSBIdentity("default", root).hasStableID(), "unparseable id")
}

func TestResolveUSBIdentity_MissingUsbidStillHasBusPath(t *testing.T) {
	t.Parallel()
	// USB card present in cards listing but no usbid file: bus path still resolves,
	// vid/pid stay empty, and the bus-path token is still usable.
	root := writeFakeProc(t, procCardsRPi5, nil)

	ident := resolveUSBIdentity(":0,0", root)
	assert.Equal(t, "usb-xhci-hcd.0-1", ident.BusPath)
	assert.Empty(t, ident.VendorID)
	assert.Equal(t, "usb-path:usb-xhci-hcd.0-1", ident.stableToken())
}

func TestResolveUSBIdentity_MissingProcYieldsZero(t *testing.T) {
	t.Parallel()
	// An empty root (no /proc/asound) must never error, just yield a zero identity.
	ident := resolveUSBIdentity(":0,0", t.TempDir())
	assert.False(t, ident.hasStableID())
}

func TestReadUSBSerial_AbsentSysfsReturnsEmpty(t *testing.T) {
	t.Parallel()
	// No /sys tree in the fake root -> best-effort serial read returns "".
	assert.Empty(t, readUSBSerial(t.TempDir(), 0))
}

// TestReadUSBSerial_StopsAtDeviceNode reproduces a real-hardware bug found on a
// Raspberry Pi 5: a serial-less C-Media card sits under a root hub whose own
// "serial" file reads "xhci-hcd.0". The walk must stop at the device node (the
// idVendor-bearing dir) and never pick up the hub/controller serial.
func TestReadUSBSerial_StopsAtDeviceNode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hub := filepath.Join(root, "sys", "devices", "platform", "xhci-hcd.0", "usb1")
	dev := filepath.Join(hub, "1-1")
	cardDir := filepath.Join(dev, "1-1:1.0", "sound", "card0")
	require.NoError(t, os.MkdirAll(cardDir, 0o755))
	// Root hub: has its own idVendor and a serial that must NOT be selected.
	require.NoError(t, os.WriteFile(filepath.Join(hub, "idVendor"), []byte("1d6b\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(hub, "serial"), []byte("xhci-hcd.0\n"), 0o644))
	// USB device node: has idVendor but (initially) no serial.
	require.NoError(t, os.WriteFile(filepath.Join(dev, "idVendor"), []byte("0d8c\n"), 0o644))

	classDir := filepath.Join(root, "sys", "class", "sound")
	require.NoError(t, os.MkdirAll(classDir, 0o755))
	require.NoError(t, os.Symlink(cardDir, filepath.Join(classDir, "card0")))

	// Device reports no serial: must return "" rather than the hub's "xhci-hcd.0".
	assert.Empty(t, readUSBSerial(root, 0))

	// Device that does report a serial: that value is returned.
	require.NoError(t, os.WriteFile(filepath.Join(dev, "serial"), []byte("DEVICE123\n"), 0o644))
	assert.Equal(t, "DEVICE123", readUSBSerial(root, 0))
}
