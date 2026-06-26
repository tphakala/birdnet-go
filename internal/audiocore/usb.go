// Package audiocore provides the core audio infrastructure for BirdNET-Go.
// usb.go - stable USB hardware identity for ALSA capture devices.
//
// Linux assigns ALSA card indices in USB enumeration order at boot, so the same
// physical USB sound card can be ":1,0" on one boot and ":3,0" on the next. A
// configuration that stores the decoded ALSA index therefore breaks after a
// reboot (GH #3651). Matching on a stable USB identifier (the kernel USB bus
// path, or vendor:product:serial) instead keeps the saved selection valid.
//
// This file holds the platform-independent parsing/token logic so it can be
// unit-tested everywhere. The actual proc/sysfs reads live in usb_linux.go.
package audiocore

import (
	"regexp"
	"strconv"
	"strings"
)

// Stable token prefixes persisted in conf.AudioSourceConfig.Device. A device
// string carrying one of these prefixes is matched against a live device's
// resolved USB identity rather than its (unstable) ALSA index.
const (
	usbPathTokenPrefix = "usb-path:"
	usbIDTokenPrefix   = "usb-id:"
)

// defaultProcRoot is the production filesystem root for the proc/sysfs reads in
// resolveUSBIdentity. Tests pass a fake root instead.
const defaultProcRoot = "/"

// usbIdentity holds the stable USB hardware identity of an audio capture device.
// All fields are empty on non-Linux platforms or for non-USB devices.
type usbIdentity struct {
	// BusPath is the kernel USB topology path (e.g. "usb-0000:00:14.0-3" on x86,
	// "usb-xhci-hcd.0-1" on a Pi). It is stable across reboots as long as the
	// device stays in the same physical port.
	BusPath string

	// VendorID is the 4-hex-digit USB vendor id (e.g. "0d8c").
	VendorID string

	// ProductID is the 4-hex-digit USB product id (e.g. "0014").
	ProductID string

	// Serial is the USB serial number, or "" when the device reports none
	// (common on inexpensive USB audio adapters).
	Serial string
}

// hasStableID reports whether this identity yields a reboot-stable token.
func (u usbIdentity) hasStableID() bool {
	return u.BusPath != "" || (u.VendorID != "" && u.ProductID != "")
}

// isUSBDeviceToken reports whether a persisted device string is a stable USB
// token (rather than a legacy ALSA index or device name). Used to skip the
// proc/sysfs resolution work entirely for the common legacy configurations.
func isUSBDeviceToken(deviceID string) bool {
	return strings.HasPrefix(deviceID, usbPathTokenPrefix) ||
		strings.HasPrefix(deviceID, usbIDTokenPrefix)
}

// deviceDedupKey returns the key used to collapse the several ALSA pseudo-devices
// that one physical card enumerates as, while keeping two physically distinct
// cards that share a display name separate (e.g. two identical USB mics). It
// prefers the stable USB token, then the ALSA card number, then the bare name.
// A name-only key would hide the second identical device, the exact case the
// stable-id selection exists to fix.
func deviceDedupKey(name string, cardNumber int, ident usbIdentity) string {
	switch {
	case ident.stableToken() != "":
		return name + "\x00" + ident.stableToken()
	case cardNumber >= 0:
		return name + "\x00card:" + strconv.Itoa(cardNumber)
	default:
		return name
	}
}

// redactDeviceID returns a log-safe form of a configured device identifier. A
// usb-id token embeds the USB serial number, a persistent per-unit hardware
// identifier, so the serial is masked before the value reaches logs, error
// context, or support bundles. Bus-path and legacy ALSA/name identifiers carry
// no such per-unit id and are returned unchanged.
func redactDeviceID(deviceID string) string {
	rest, ok := strings.CutPrefix(deviceID, usbIDTokenPrefix)
	if !ok {
		return deviceID
	}
	vendor, after, found := strings.Cut(rest, ":")
	if !found {
		return deviceID
	}
	product, serial, found := strings.Cut(after, ":")
	if !found || serial == "" {
		return deviceID
	}
	return usbIDTokenPrefix + vendor + ":" + product + ":<redacted>"
}

// stableToken returns the preferred persisted identifier for this device, or ""
// when no stable USB identity is available. The bus path is preferred because it
// disambiguates multiple identical devices (same vendor:product, no serial) by
// physical port, which vendor:product:serial alone cannot.
func (u usbIdentity) stableToken() string {
	if u.BusPath != "" {
		return usbPathTokenPrefix + u.BusPath
	}
	return u.hwIDToken()
}

// hwIDToken returns the "usb-id:vendor:product:serial" token, or "" when the
// vendor or product id is unknown.
func (u usbIdentity) hwIDToken() string {
	if u.VendorID == "" || u.ProductID == "" {
		return ""
	}
	return usbIDTokenPrefix + u.VendorID + ":" + u.ProductID + ":" + u.Serial
}

// parseALSACardNumber extracts the ALSA card index from a decoded device id.
// It accepts the forms malgo produces and a few defensive variants: ":3,0",
// "hw:3,0", "3,0", "3". Returns -1 when no leading card integer is present
// (e.g. "default", "sysdefault", "pcmC0D0c", "").
func parseALSACardNumber(decodedID string) int {
	s := strings.TrimPrefix(decodedID, "hw")
	s = strings.TrimPrefix(s, ":")
	// The card number is the run of digits up to the first ',' (or end).
	if comma := strings.IndexByte(s, ','); comma >= 0 {
		s = s[:comma]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return -1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// procCardEntry is one card parsed from /proc/asound/cards.
type procCardEntry struct {
	// busPath is the USB bus path ("usb-..."), empty for non-USB cards.
	busPath string
	// isUSB is true when the card is a USB-Audio device.
	isUSB bool
}

// procCardHeaderRe matches a card header line in /proc/asound/cards, e.g.
// " 0 [Device         ]: USB-Audio - USB Audio Device". Capture group 1 is the
// card index.
var procCardHeaderRe = regexp.MustCompile(`^\s*(\d+)\s+\[`)

// parseProcAsoundCards parses the contents of /proc/asound/cards into a map of
// card index -> entry. Each card spans a header line and a detail line:
//
//	0 [Device         ]: USB-Audio - USB Audio Device
//	                     C-Media ... USB Audio Device at usb-xhci-hcd.0-1, full speed
//
// The bus path is the token between " at " and the next "," on the detail line;
// only paths beginning with "usb-" are recorded (and mark the card as USB).
func parseProcAsoundCards(content string) map[int]procCardEntry {
	result := make(map[int]procCardEntry)
	curCard := -1
	for line := range strings.Lines(content) {
		if m := procCardHeaderRe.FindStringSubmatch(line); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				curCard = -1
				continue
			}
			curCard = n
			result[n] = procCardEntry{isUSB: strings.Contains(line, "USB-Audio")}
			continue
		}
		if curCard < 0 {
			continue
		}
		if bp := extractBusPathFromDetail(line); strings.HasPrefix(bp, "usb-") {
			e := result[curCard]
			e.busPath = bp
			e.isUSB = true
			result[curCard] = e
		}
	}
	return result
}

// extractBusPathFromDetail returns the bus path from a /proc/asound/cards detail
// line such as "... at usb-0000:00:14.0-3, high speed" -> "usb-0000:00:14.0-3".
// Returns "" when the line has no " at <path>," segment. It anchors on the LAST
// " at " so a device name that itself contains " at " does not mis-split the
// trailing bus-path segment.
func extractBusPathFromDetail(line string) string {
	const sep = " at "
	idx := strings.LastIndex(line, sep)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(sep):]
	if comma := strings.IndexByte(rest, ','); comma >= 0 {
		rest = rest[:comma]
	}
	return strings.TrimSpace(rest)
}

// hex4Re matches exactly four hexadecimal digits (a USB vid/pid).
var hex4Re = regexp.MustCompile(`^[0-9a-fA-F]{4}$`)

// parseUSBID parses the contents of /proc/asound/cardN/usbid ("VVVV:PPPP") into
// vendor and product ids. Returns empty strings when the content is malformed.
func parseUSBID(content string) (vendor, product string) {
	v, p, found := strings.Cut(strings.TrimSpace(content), ":")
	if !found {
		return "", ""
	}
	v = strings.TrimSpace(v)
	p = strings.TrimSpace(p)
	if !hex4Re.MatchString(v) || !hex4Re.MatchString(p) {
		return "", ""
	}
	return v, p
}
