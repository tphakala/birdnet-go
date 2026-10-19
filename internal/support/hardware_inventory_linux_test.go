package support

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hardwareSerialMarker = "SERIAL-MARKER-0123"

// populateHardwareTree builds a fake sysfs with one USB audio device behind
// card 1 and a loopback card 0, every device carrying a serial file.
func populateHardwareTree(t *testing.T, root string) {
	t.Helper()
	dev := filepath.Join(root, "sys", "devices", "pci0", "3-2")
	cardDir := filepath.Join(dev, "sound", "card1")
	loopDir := filepath.Join(root, "sys", "devices", "platform", "snd_aloop.0", "sound", "card0")
	linkDir := filepath.Join(root, "sys", "bus", "usb", "devices")
	classDir := filepath.Join(root, "sys", "class", "sound")
	for _, d := range []string{filepath.Join(cardDir, "pcmC1D0c"), loopDir, linkDir, classDir} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	files := map[string]string{
		"idVendor": "0d8c", "idProduct": "0014", "manufacturer": "C-Media", "product": "USB Audio",
		"speed": "480", "bDeviceClass": "00", "serial": hardwareSerialMarker,
	}
	for name, value := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dev, name), []byte(value+"\n"), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(cardDir, "id"), []byte("Device\n"), 0o644))
	require.NoError(t, os.Symlink(dev, filepath.Join(linkDir, "3-2")))
	require.NoError(t, os.Symlink(cardDir, filepath.Join(classDir, "card1")))
	require.NoError(t, os.Symlink(loopDir, filepath.Join(classDir, "card0")))
}

func TestCollectHardwareInventory(t *testing.T) {
	t.Parallel()

	t.Run("non-linux does not read the filesystem", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		populateHardwareTree(t, root)
		usb, cards := collectHardwareInventory(t.Context(), "darwin", root)
		assert.Equal(t, &USBInventory{Reason: hardwareReasonUnsupportedOS}, usb)
		assert.Equal(t, &SoundCardInventory{Reason: hardwareReasonUnsupportedOS}, cards)
	})

	t.Run("sysfs not visible", func(t *testing.T) {
		t.Parallel()
		usb, cards := collectHardwareInventory(t.Context(), osLinux, t.TempDir())
		assert.Equal(t, &USBInventory{Reason: hardwareReasonNotVisible}, usb)
		assert.Equal(t, &SoundCardInventory{Reason: hardwareReasonNotVisible}, cards)
	})

	t.Run("sysfs directories unreadable", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		for _, p := range []string{filepath.Join("sys", "bus", "usb", "devices"), filepath.Join("sys", "class", "sound")} {
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, p), nil, 0o644))
		}
		usb, cards := collectHardwareInventory(t.Context(), osLinux, root)
		assert.Equal(t, &USBInventory{Reason: hardwareReasonUnreadable}, usb)
		assert.Equal(t, &SoundCardInventory{Reason: hardwareReasonUnreadable}, cards)
	})

	t.Run("empty directories", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "sys", "bus", "usb", "devices"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "sys", "class", "sound"), 0o755))
		usb, cards := collectHardwareInventory(t.Context(), osLinux, root)
		assert.Equal(t, &USBInventory{Available: true}, usb)
		assert.Equal(t, &SoundCardInventory{Available: true}, cards)
	})

	t.Run("cancelled context marks both sections truncated", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		usb, cards := collectHardwareInventory(ctx, osLinux, t.TempDir())
		assert.Equal(t, &USBInventory{Available: true, Truncated: true}, usb)
		assert.Equal(t, &SoundCardInventory{Available: true, Truncated: true}, cards)
	})

	t.Run("truncation is reported per section", func(t *testing.T) {
		t.Parallel()
		// 129 USB devices exceed the device cap while the sound card
		// directory stays empty, and the reverse for the second root.
		usbRoot := t.TempDir()
		usbDir := filepath.Join(usbRoot, "sys", "bus", "usb", "devices")
		require.NoError(t, os.MkdirAll(usbDir, 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(usbRoot, "sys", "class", "sound"), 0o755))
		for i := range 129 {
			require.NoError(t, os.WriteFile(filepath.Join(usbDir, "1-"+strconv.Itoa(1000+i)), nil, 0o644))
		}
		usb, cards := collectHardwareInventory(t.Context(), osLinux, usbRoot)
		assert.True(t, usb.Truncated)
		assert.Len(t, usb.Devices, 128)
		assert.False(t, cards.Truncated)

		soundRoot := t.TempDir()
		soundDir := filepath.Join(soundRoot, "sys", "class", "sound")
		require.NoError(t, os.MkdirAll(soundDir, 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(soundRoot, "sys", "bus", "usb", "devices"), 0o755))
		for i := range 33 {
			require.NoError(t, os.WriteFile(filepath.Join(soundDir, "card"+strconv.Itoa(i)), nil, 0o644))
		}
		usb, cards = collectHardwareInventory(t.Context(), osLinux, soundRoot)
		assert.False(t, usb.Truncated)
		assert.True(t, cards.Truncated)
		assert.Len(t, cards.Cards, 32)
	})

	t.Run("populated tree maps fields and never leaks the serial", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		populateHardwareTree(t, root)
		usb, cards := collectHardwareInventory(t.Context(), osLinux, root)
		assert.Equal(t, &USBInventory{Available: true, Devices: []USBDeviceInfo{{
			BusID: "3-2", VendorID: "0d8c", ProductID: "0014", Manufacturer: "C-Media",
			Product: "USB Audio", SpeedMbps: "480", DeviceClass: "00",
		}}}, usb)
		assert.Equal(t, &SoundCardInventory{Available: true, Cards: []SoundCardInfo{
			{Index: 0, ID: ""},
			{Index: 1, ID: "Device", USBBusID: "3-2", HasCapture: true},
		}}, cards)

		raw, err := json.Marshal(SystemInfo{USB: usb, SoundCards: cards})
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"usb":`)
		assert.Contains(t, string(raw), `"sound_cards":`)
		assert.Contains(t, string(raw), `"speed_mbps":"480"`)
		assert.NotContains(t, string(raw), hardwareSerialMarker)
	})
}
