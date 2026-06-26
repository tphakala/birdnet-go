package audiocore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real /proc/asound/cards fixtures captured from the project test hosts.
const (
	// amd64 dev host: a snd-aloop Loopback (card 0) and a ZOOM AMS-24 USB
	// interface (card 1).
	procCardsAMD64 = ` 0 [Loopback       ]: Loopback - Loopback
                      Loopback 1
 1 [AMS24          ]: USB-Audio - AMS-24
                      ZOOM Corporation AMS-24 at usb-0000:00:14.0-3, high speed
`
	// arm64 Raspberry Pi 5: a C-Media USB Audio Device (card 0) and two HDMI
	// outputs (cards 1 and 2).
	procCardsRPi5 = ` 0 [Device         ]: USB-Audio - USB Audio Device
                      C-Media Electronics Inc. USB Audio Device at usb-xhci-hcd.0-1, full speed
 1 [vc4hdmi0       ]: vc4-hdmi - vc4-hdmi-0
                      vc4-hdmi-0
 2 [vc4hdmi1       ]: vc4-hdmi - vc4-hdmi-1
                      vc4-hdmi-1
`
)

func TestParseALSACardNumber(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"colon card,device", ":3,0", 3},
		{"colon card 0", ":0,0", 0},
		{"double digit", ":10,0", 10},
		{"hw prefix", "hw:2,0", 2},
		{"bare card,device", "5,0", 5},
		{"bare card", "7", 7},
		{"default token", "default", -1},
		{"sysdefault token", "sysdefault", -1},
		{"pcm node form", "pcmC0D0c", -1},
		{"empty", "", -1},
		{"colon only", ":", -1},
		{"negative is rejected", ":-1,0", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, parseALSACardNumber(tt.in))
		})
	}
}

func TestParseProcAsoundCards_AMD64(t *testing.T) {
	t.Parallel()
	cards := parseProcAsoundCards(procCardsAMD64)

	// Loopback (card 0) is not USB.
	loop, ok := cards[0]
	require.True(t, ok)
	assert.False(t, loop.isUSB)
	assert.Empty(t, loop.busPath)

	// AMS-24 (card 1) is USB with the x86-style bus path.
	ams, ok := cards[1]
	require.True(t, ok)
	assert.True(t, ams.isUSB)
	assert.Equal(t, "usb-0000:00:14.0-3", ams.busPath)
}

func TestParseProcAsoundCards_RPi5(t *testing.T) {
	t.Parallel()
	cards := parseProcAsoundCards(procCardsRPi5)

	// C-Media (card 0) is USB with the Pi-style bus path.
	dev, ok := cards[0]
	require.True(t, ok)
	assert.True(t, dev.isUSB)
	assert.Equal(t, "usb-xhci-hcd.0-1", dev.busPath)

	// HDMI outputs (cards 1, 2) are not USB.
	for _, idx := range []int{1, 2} {
		hdmi, found := cards[idx]
		require.True(t, found)
		assert.False(t, hdmi.isUSB)
		assert.Empty(t, hdmi.busPath)
	}
}

func TestParseProcAsoundCards_EdgeCases(t *testing.T) {
	t.Parallel()
	assert.Empty(t, parseProcAsoundCards(""), "empty input yields no cards")

	// A detail line with " at " for a non-USB reason must not be taken as a bus path.
	const pci = ` 0 [PCH            ]: HDA-Intel - HDA Intel PCH
                      HDA Intel PCH at 0xf0440000 irq 145
`
	cards := parseProcAsoundCards(pci)
	entry, ok := cards[0]
	require.True(t, ok)
	assert.False(t, entry.isUSB, "PCI card is not USB")
	assert.Empty(t, entry.busPath, "non usb- bus path is ignored")
}

func TestExtractBusPathFromDetail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"x86", "ZOOM Corporation AMS-24 at usb-0000:00:14.0-3, high speed", "usb-0000:00:14.0-3"},
		{"pi", "C-Media ... USB Audio Device at usb-xhci-hcd.0-1, full speed", "usb-xhci-hcd.0-1"},
		{"no at segment", "Loopback 1", ""},
		{"at without comma", "thing at usb-0000:00:14.0-3", "usb-0000:00:14.0-3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, extractBusPathFromDetail(tt.in))
		})
	}
}

func TestParseUSBID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		in              string
		vendor, product string
	}{
		{"c-media", "0d8c:0014\n", "0d8c", "0014"},
		{"zoom", "0b33:0024", "0b33", "0024"},
		{"no newline", "1234:abcd", "1234", "abcd"},
		{"malformed no colon", "0d8c0014", "", ""},
		{"too short", "d8c:14", "", ""},
		{"not hex", "zzzz:0014", "", ""},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, p := parseUSBID(tt.in)
			assert.Equal(t, tt.vendor, v)
			assert.Equal(t, tt.product, p)
		})
	}
}

func TestMatchesDevice(t *testing.T) {
	t.Parallel()
	// A live USB device with a known identity (as resolved from /proc).
	usbDev := usbIdentity{BusPath: "usb-xhci-hcd.0-1", VendorID: "0d8c", ProductID: "0014"}
	const decodedID = ":1,0"
	const name = "USB Audio Device"

	tests := []struct {
		name     string
		ident    usbIdentity
		devName  string
		deviceID string
		want     bool
	}{
		// USB bus-path token: matches the resolved bus path regardless of the
		// current ALSA index (the core reboot-survival case).
		{"usb-path matches", usbDev, name, "usb-path:usb-xhci-hcd.0-1", true},
		{"usb-path mismatch", usbDev, name, "usb-path:usb-0000:00:14.0-3", false},
		{"usb-path against non-usb device", usbIdentity{}, name, "usb-path:usb-xhci-hcd.0-1", false},
		{"empty usb-path never matches", usbIdentity{}, name, "usb-path:", false},
		// USB hw-id token: vendor:product:serial.
		{"usb-id matches", usbDev, name, "usb-id:0d8c:0014:", true},
		{"usb-id mismatch product", usbDev, name, "usb-id:0d8c:9999:", false},
		{"usb-id against non-usb device", usbIdentity{}, name, "usb-id:0d8c:0014:", false},
		// Legacy tiers preserved.
		{"legacy exact id", usbDev, name, ":1,0", true},
		{"legacy wrong id (the bug: index shifted)", usbDev, name, ":3,0", false},
		{"legacy name substring", usbDev, name, "USB Audio", true},
		{"legacy name miss", usbDev, name, "Webcam", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := matchesDevice(decodedID, tt.ident, tt.devName, false, tt.deviceID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUSBIdentity_StableToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		ident usbIdentity
		want  string
	}{
		{
			name:  "bus path preferred over vid:pid",
			ident: usbIdentity{BusPath: "usb-xhci-hcd.0-1", VendorID: "0d8c", ProductID: "0014"},
			want:  "usb-path:usb-xhci-hcd.0-1",
		},
		{
			name:  "vid:pid:serial when no bus path",
			ident: usbIdentity{VendorID: "0b33", ProductID: "0024", Serial: "ABC123"},
			want:  "usb-id:0b33:0024:ABC123",
		},
		{
			name:  "vid:pid with empty serial",
			ident: usbIdentity{VendorID: "0d8c", ProductID: "0014"},
			want:  "usb-id:0d8c:0014:",
		},
		{
			name:  "no identity yields empty token",
			ident: usbIdentity{},
			want:  "",
		},
		{
			name:  "vendor without product yields empty token",
			ident: usbIdentity{VendorID: "0d8c"},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.ident.stableToken())
			assert.Equal(t, tt.want != "", tt.ident.hasStableID())
		})
	}
}
