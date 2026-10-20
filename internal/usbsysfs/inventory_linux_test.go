package usbsysfs

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const serialMarker = "SERIAL-MARKER-0123"

// writeUSBDevice creates a device node with the given attribute files under
// <root>/sys/devices/<name> and links it as <root>/sys/bus/usb/devices/<name>.
// A serial file holding serialMarker is always written, so a reader that leaks
// it shows up in the output.
func writeUSBDevice(t *testing.T, root, name string, attrs map[string]string) string {
	t.Helper()
	dev := filepath.Join(root, "sys", "devices", "pci0", name)
	require.NoError(t, os.MkdirAll(dev, 0o755))
	for attr, value := range attrs {
		require.NoError(t, os.WriteFile(filepath.Join(dev, attr), []byte(value+"\n"), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dev, "serial"), []byte(serialMarker+"\n"), 0o644))
	linkDir := filepath.Join(root, "sys", "bus", "usb", "devices")
	require.NoError(t, os.MkdirAll(linkDir, 0o755))
	require.NoError(t, os.Symlink(dev, filepath.Join(linkDir, name)))
	return dev
}

func TestDevices_ListsDevicesSortedWithoutInterfaces(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	full := map[string]string{
		"idVendor": "0d8c", "idProduct": "0014", "manufacturer": "C-Media", "product": "USB Audio",
		"speed": "480", "bDeviceClass": "00",
	}
	writeUSBDevice(t, root, "usb1", map[string]string{"idVendor": "1d6b", "idProduct": "0002", "speed": "480", "bDeviceClass": "09"})
	writeUSBDevice(t, root, "1-1", full)
	writeUSBDevice(t, root, "1-1.2", map[string]string{"idVendor": "0d8c", "idProduct": "0015"})
	writeUSBDevice(t, root, "1-10", map[string]string{"idVendor": "0001", "idProduct": "0002"})
	writeUSBDevice(t, root, "1-1:1.0", map[string]string{"idVendor": "ffff"})
	writeUSBDevice(t, root, "1-0:1.0", map[string]string{})

	got, truncated, err := Devices(t.Context(), root)

	require.NoError(t, err)
	assert.False(t, truncated)
	want := []Device{
		{BusID: "1-1", VendorID: "0d8c", ProductID: "0014", Manufacturer: "C-Media", Product: "USB Audio", SpeedMbps: "480", DeviceClass: "00"},
		{BusID: "1-1.2", VendorID: "0d8c", ProductID: "0015"},
		{BusID: "1-10", VendorID: "0001", ProductID: "0002"},
		{BusID: "usb1", VendorID: "1d6b", ProductID: "0002", SpeedMbps: "480", DeviceClass: "09"},
	}
	assert.Equal(t, want, got)
	assert.NotContains(t, strings.Join(deviceStrings(got), "|"), serialMarker)
}

func deviceStrings(devs []Device) []string {
	out := make([]string, 0, len(devs)*7)
	for _, d := range devs {
		out = append(out, d.BusID, d.VendorID, d.ProductID, d.Manufacturer, d.Product, d.SpeedMbps, d.DeviceClass)
	}
	return out
}

func TestDevices_ValidatesAttributes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		attrs map[string]string
		want  Device
	}{
		{"well formed", map[string]string{"idVendor": "0D8c", "idProduct": "00aF", "speed": "1.5", "bDeviceClass": "ef"},
			Device{VendorID: "0D8c", ProductID: "00aF", SpeedMbps: "1.5", DeviceClass: "ef"}},
		{"short vendor", map[string]string{"idVendor": "0d8"}, Device{}},
		{"long vendor", map[string]string{"idVendor": "0d8c0"}, Device{}},
		{"short class", map[string]string{"bDeviceClass": "e"}, Device{}},
		{"unknown speed", map[string]string{"speed": "unknown"}, Device{}},
		{"too many speed decimals", map[string]string{"speed": "1.555"}, Device{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeUSBDevice(t, root, "1-1", tt.attrs)

			got, _, err := Devices(t.Context(), root)

			require.NoError(t, err)
			tt.want.BusID = "1-1"
			assert.Equal(t, []Device{tt.want}, got)
		})
	}
}

func TestSanitizeText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"clean", "USB Audio Device", "USB Audio Device"},
		{"invalid utf8 replaced", "a\xffb", "a�b"},
		{"tab and newline become spaces", "a\tb\nc", "a b c"},
		{"escape dropped", "a\x1bb", "ab"},
		{"bidi mark dropped", "a\u200eb", "ab"},
		{"128 runes kept", strings.Repeat("x", 128), strings.Repeat("x", 128)},
		{"129 runes cut", strings.Repeat("x", 129), strings.Repeat("x", 128)},
		{"space after cut trimmed", strings.Repeat("x", 127) + "  y", strings.Repeat("x", 127)},
		{"leading spaces after a dropped mark trimmed", "\u200e  Foo", "Foo"},
		{"trailing space before a dropped control trimmed", "Foo \x1b", "Foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, sanitizeText(tt.in))
		})
	}
}

// writeSoundCard creates card n with the given extra entries and links it. A
// non-empty usbDev makes the card a child of that device directory.
func writeSoundCard(t *testing.T, root string, n int, id, usbDev string, entries ...string) {
	t.Helper()
	parent := filepath.Join(root, "sys", "devices", "platform", "snd"+strconv.Itoa(n))
	if usbDev != "" {
		parent = usbDev
	}
	cardDir := filepath.Join(parent, "sound", "card"+strconv.Itoa(n))
	require.NoError(t, os.MkdirAll(cardDir, 0o755))
	if id != "" {
		require.NoError(t, os.WriteFile(filepath.Join(cardDir, "id"), []byte(id+"\n"), 0o644))
	}
	for _, e := range entries {
		require.NoError(t, os.MkdirAll(filepath.Join(cardDir, e), 0o755))
	}
	linkCard(t, root, n, cardDir)
}

func TestSoundCards_NumericOrderUSBLinkAndCapture(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dev2 := writeUSBDevice(t, root, "3-2", map[string]string{"idVendor": "0d8c"})
	dev10 := writeUSBDevice(t, root, "3-3", map[string]string{"idVendor": "0d8c"})
	writeSoundCard(t, root, 0, "Loopback", "", "pcmC0D0p")
	writeSoundCard(t, root, 2, "Device", dev2, "pcmC2D0c", "pcmC2D0p")
	writeSoundCard(t, root, 10, "", dev10, "pcmC10D0p")
	classDir := filepath.Join(root, "sys", "class", "sound")
	for _, n := range []string{"controlC0", "pcmC0D0p", "midiC1D0", "timer", "card01"} {
		require.NoError(t, os.WriteFile(filepath.Join(classDir, n), nil, 0o644))
	}

	got, truncated, err := SoundCards(t.Context(), root)

	require.NoError(t, err)
	assert.False(t, truncated)
	want := []SoundCard{
		{Index: 0, ID: "Loopback"},
		{Index: 2, ID: "Device", USBBusID: "3-2", HasCapture: true},
		{Index: 10, USBBusID: "3-3"},
	}
	assert.Equal(t, want, got)
}

func TestSoundCards_IgnoresUSBAncestorWithoutBusIDName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// An ancestor with idVendor whose name is not a kernel USB device name.
	dev := filepath.Join(root, "sys", "devices", "pci0", "not a bus id")
	require.NoError(t, os.MkdirAll(dev, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dev, "idVendor"), []byte("0d8c\n"), 0o644))
	writeSoundCard(t, root, 0, "Odd", dev)

	got, _, err := SoundCards(t.Context(), root)

	require.NoError(t, err)
	assert.Equal(t, []SoundCard{{Index: 0, ID: "Odd"}}, got)
}

func TestInventory_Caps(t *testing.T) {
	t.Parallel()
	makeDevices := func(t *testing.T, n int) string {
		t.Helper()
		root := t.TempDir()
		dir := filepath.Join(root, "sys", "bus", "usb", "devices")
		require.NoError(t, os.MkdirAll(dir, 0o755))
		for i := range n {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "1-"+strconv.Itoa(1000+i)), nil, 0o644))
		}
		return root
	}
	makeCards := func(t *testing.T, n int) string {
		t.Helper()
		root := t.TempDir()
		dir := filepath.Join(root, "sys", "class", "sound")
		require.NoError(t, os.MkdirAll(dir, 0o755))
		for i := range n {
			require.NoError(t, os.Mkdir(filepath.Join(dir, "card"+strconv.Itoa(i)), 0o755))
		}
		return root
	}

	t.Run("128 devices fit", func(t *testing.T) {
		t.Parallel()
		got, truncated, err := Devices(t.Context(), makeDevices(t, 128))
		require.NoError(t, err)
		assert.Len(t, got, 128)
		assert.False(t, truncated)
	})
	t.Run("129 devices cut to the lexically first 128", func(t *testing.T) {
		t.Parallel()
		got, truncated, err := Devices(t.Context(), makeDevices(t, 129))
		require.NoError(t, err)
		require.Len(t, got, 128)
		assert.True(t, truncated)
		assert.Equal(t, "1-1000", got[0].BusID)
		assert.Equal(t, "1-1127", got[127].BusID)
	})
	t.Run("device directory over 1024 entries flags truncation", func(t *testing.T) {
		t.Parallel()
		// Mostly interface entries and four devices, so only the directory
		// listing cap, not the device cap, can set truncated.
		root := makeDevices(t, 0)
		dir := filepath.Join(root, "sys", "bus", "usb", "devices")
		for i := range 1021 {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "1-1:1."+strconv.Itoa(i)), nil, 0o644))
		}
		for i := range 4 {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "2-"+strconv.Itoa(i+1)), nil, 0o644))
		}
		got, truncated, err := Devices(t.Context(), root)
		require.NoError(t, err)
		assert.True(t, truncated)
		assert.LessOrEqual(t, len(got), 4)
	})
	t.Run("sound class directory over 1024 entries flags truncation", func(t *testing.T) {
		t.Parallel()
		// No cards at all, so only the directory listing cap can set truncated.
		root := makeCards(t, 0)
		dir := filepath.Join(root, "sys", "class", "sound")
		for i := range 1025 {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "controlC"+strconv.Itoa(i)), nil, 0o644))
		}
		got, truncated, err := SoundCards(t.Context(), root)
		require.NoError(t, err)
		assert.True(t, truncated)
		assert.Empty(t, got)
	})
	t.Run("32 cards fit", func(t *testing.T) {
		t.Parallel()
		got, truncated, err := SoundCards(t.Context(), makeCards(t, 32))
		require.NoError(t, err)
		assert.Len(t, got, 32)
		assert.False(t, truncated)
	})
	t.Run("33 cards cut to 32", func(t *testing.T) {
		t.Parallel()
		got, truncated, err := SoundCards(t.Context(), makeCards(t, 33))
		require.NoError(t, err)
		require.Len(t, got, 32)
		assert.True(t, truncated)
		assert.Equal(t, 31, got[31].Index)
	})
	t.Run("unlistable card directory flags truncation", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeSoundCard(t, root, 0, "Ok", "", "pcmC0D0c")
		classDir := filepath.Join(root, "sys", "class", "sound")
		require.NoError(t, os.WriteFile(filepath.Join(classDir, "card1"), nil, 0o644))
		got, truncated, err := SoundCards(t.Context(), root)
		require.NoError(t, err)
		assert.True(t, truncated)
		assert.Equal(t, []SoundCard{{Index: 0, ID: "Ok", HasCapture: true}, {Index: 1}}, got)
	})
	t.Run("card directory with more than 1024 entries flags truncation", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		entries := make([]string, 0, 1025)
		for i := range 1025 {
			entries = append(entries, "e"+strconv.Itoa(i))
		}
		writeSoundCard(t, root, 0, "Big", "", entries...)
		_, truncated, err := SoundCards(t.Context(), root)
		require.NoError(t, err)
		assert.True(t, truncated)
	})
}

func TestInventory_DirectoryStates(t *testing.T) {
	t.Parallel()
	listers := map[string]func(ctx context.Context, root string) (int, bool, error){
		"devices": func(ctx context.Context, root string) (int, bool, error) {
			got, tr, err := Devices(ctx, root)
			return len(got), tr, err
		},
		"cards": func(ctx context.Context, root string) (int, bool, error) {
			got, tr, err := SoundCards(ctx, root)
			return len(got), tr, err
		},
	}
	dirs := map[string]string{"devices": filepath.Join("sys", "bus", "usb", "devices"), "cards": filepath.Join("sys", "class", "sound")}
	for name, list := range listers {
		t.Run(name+" missing", func(t *testing.T) {
			t.Parallel()
			_, _, err := list(t.Context(), t.TempDir())
			require.ErrorIs(t, err, fs.ErrNotExist)
		})
		t.Run(name+" empty", func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, dirs[name]), 0o755))
			n, truncated, err := list(t.Context(), root)
			require.NoError(t, err)
			assert.Zero(t, n)
			assert.False(t, truncated)
		})
		t.Run(name+" is a regular file", func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, dirs[name])), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, dirs[name]), nil, 0o644))
			_, _, err := list(t.Context(), root)
			require.Error(t, err)
			assert.NotErrorIs(t, err, fs.ErrNotExist)
		})
		t.Run(name+" cancelled before start", func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			n, truncated, err := list(ctx, filepath.Join(t.TempDir(), "absent"))
			require.ErrorIs(t, err, context.Canceled)
			assert.Zero(t, n)
			assert.False(t, truncated)
		})
	}
}

// countdownCtx is a context whose Done channel reports cancellation after a
// fixed number of polls, so a test cancels the listers part way through their
// loops without timing.
type countdownCtx struct {
	context.Context
	remaining int
	closed    chan struct{}
}

func newCountdownCtx(t *testing.T, open int) *countdownCtx {
	t.Helper()
	closed := make(chan struct{})
	close(closed)
	return &countdownCtx{Context: t.Context(), remaining: open, closed: closed}
}

func (c *countdownCtx) Done() <-chan struct{} {
	if c.remaining > 0 {
		c.remaining--
		return nil // a nil channel never becomes ready
	}
	return c.closed
}

func TestInventory_CancelledMidIteration(t *testing.T) {
	t.Parallel()
	t.Run("devices", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		for _, name := range []string{"1-1", "1-2", "1-3"} {
			writeUSBDevice(t, root, name, map[string]string{"idVendor": "0d8c"})
		}
		// Entry check and the first two items pass; the third item's check cancels.
		got, truncated, err := Devices(newCountdownCtx(t, 3), root)
		require.NoError(t, err)
		assert.True(t, truncated)
		require.Len(t, got, 2)
		assert.Equal(t, "1-1", got[0].BusID)
		assert.Equal(t, "1-2", got[1].BusID)
	})
	t.Run("cards", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		for n := range 3 {
			writeSoundCard(t, root, n, "c"+strconv.Itoa(n), "")
		}
		got, truncated, err := SoundCards(newCountdownCtx(t, 3), root)
		require.NoError(t, err)
		assert.True(t, truncated)
		require.Len(t, got, 2)
		assert.Equal(t, 0, got[0].Index)
		assert.Equal(t, 1, got[1].Index)
	})
}

func TestInventory_SanitisesFreeText(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	long := strings.Repeat("p", 200)
	dev := writeUSBDevice(t, root, "1-1", map[string]string{
		"manufacturer": "Acme\x1b[31m\u200eCo",
		"product":      long,
	})
	writeSoundCard(t, root, 0, "Card\x1b\u200eId", dev)

	devices, _, err := Devices(t.Context(), root)
	require.NoError(t, err)
	require.Len(t, devices, 1)
	assert.Equal(t, "Acme[31mCo", devices[0].Manufacturer)
	assert.Equal(t, strings.Repeat("p", 128), devices[0].Product)

	cards, _, err := SoundCards(t.Context(), root)
	require.NoError(t, err)
	require.Len(t, cards, 1)
	assert.Equal(t, "CardId", cards[0].ID)
}
