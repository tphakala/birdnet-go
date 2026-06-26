//go:build linux

package audiocore

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// maxUSBSysfsWalkDepth bounds the upward sysfs walk in findUSBDeviceNode; the USB
// device node is normally only 3-4 levels above the card directory, so this is a
// generous cap that simply guarantees termination.
const maxUSBSysfsWalkDepth = 12

// resolveUSBIdentity maps an ALSA decoded id (":3,0") to the USB identity of its
// card. It is a thin wrapper over usbIdentityForCard that reads/parses
// /proc/asound/cards for the single lookup. Callers enumerating many devices
// should read the cards map once with readProcAsoundCards and call
// usbIdentityForCard per card to avoid re-reading the global file.
//
// It returns a zero usbIdentity (never an error) on any failure, so device
// matching falls back to the legacy id/name tiers and capture is never broken by
// a probing failure. On a native install the bus path and vendor:product come from
// /proc/asound (cards + cardN/usbid). In a Docker container /proc/asound is masked
// with an empty tmpfs even with --device /dev/snd, so usbIdentityForCard falls back
// to /sys/class/sound (still exposed) for vendor:product:serial. A usb-id: token
// (vendor:product:serial) therefore resolves in both; a usb-path: token is
// /proc-only because the sysfs bus-path format differs, so it resolves natively but
// not in a masked-/proc container, where capture reports the device missing rather
// than risking a wrong-device match.
func resolveUSBIdentity(decodedID, root string) usbIdentity {
	card := parseALSACardNumber(decodedID)
	if card < 0 {
		return usbIdentity{}
	}
	return usbIdentityForCard(card, readProcAsoundCards(root), root)
}

// readProcAsoundCards reads and parses <root>/proc/asound/cards into a card-index
// map. Returns nil on any read error (callers treat nil as "no USB cards").
func readProcAsoundCards(root string) map[int]procCardEntry {
	data, err := os.ReadFile(filepath.Join(root, "proc", "asound", "cards"))
	if err != nil {
		return nil
	}
	return parseProcAsoundCards(string(data))
}

// usbIdentityForCard resolves the USB identity of a single ALSA card. When the card is present
// in the parsed /proc/asound/cards map it uses /proc (bus path from the map, vendor:product from
// /proc/asound/cardN/usbid) plus a best-effort sysfs serial. When the card is ABSENT from that
// map (Docker masks /proc/asound with an empty tmpfs, so /proc/asound/cards does not exist, or
// /proc is otherwise unreadable) it falls back to deriving vendor:product:serial from /sys via
// usbIdentityFromSysfs, so the usb-id token still resolves in a container. Returns a zero
// usbIdentity for a card the /proc map lists as non-USB.
func usbIdentityForCard(card int, cards map[int]procCardEntry, root string) usbIdentity {
	entry, ok := cards[card]
	if !ok {
		// Card not listed in /proc/asound/cards (masked or unavailable): derive from /sys,
		// which Docker still exposes. Yields a usb-id (vendor:product:serial) token only.
		return usbIdentityFromSysfs(root, card)
	}
	if !entry.isUSB {
		return usbIdentity{}
	}
	ident := usbIdentity{BusPath: entry.busPath}

	cardDir := "card" + strconv.Itoa(card)
	if usbid, err := os.ReadFile(filepath.Join(root, "proc", "asound", cardDir, "usbid")); err == nil {
		ident.VendorID, ident.ProductID = parseUSBID(string(usbid))
	}

	ident.Serial = readUSBSerial(root, card)
	return ident
}

// findUSBDeviceNode walks up from <root>/sys/class/sound/cardN to the USB device node and
// returns its directory. The device node is the first ancestor exposing an "idVendor" file; the
// walk stops there so a parent hub or host controller (e.g. "xhci-hcd.0" on a root hub) is never
// mistaken for the device, and is bounded to the <root>/sys subtree so a symlink that resolves
// outside sysfs cannot send it wandering into unrelated system directories. Returns ("", false)
// when the card is absent or is not backed by a USB device.
func findUSBDeviceNode(root string, card int) (string, bool) {
	sysRoot := filepath.Join(root, "sys")
	// Resolve sysRoot the same way as target below; otherwise a symlinked element in root (a
	// test temp dir, or a path like macOS /var -> /private/var) would leave the resolved target
	// without the literal sysRoot prefix and the walk would stop immediately.
	if resolved, symErr := filepath.EvalSymlinks(sysRoot); symErr == nil {
		sysRoot = resolved
	}
	target, err := filepath.EvalSymlinks(filepath.Join(sysRoot, "class", "sound", "card"+strconv.Itoa(card)))
	if err != nil {
		return "", false
	}
	dir := target
	for range maxUSBSysfsWalkDepth {
		// Stay strictly below the sysfs root: the USB device node is always a descendant of
		// <root>/sys, so stop at sysRoot itself or anything symlink resolution placed outside.
		if dir == sysRoot || !strings.HasPrefix(dir, sysRoot+string(os.PathSeparator)) {
			break
		}
		if _, statErr := os.Stat(filepath.Join(dir, "idVendor")); statErr == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// readSysfsAttr reads a single sysfs attribute file and returns its trimmed contents, or "" on
// any read error.
func readSysfsAttr(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readUSBSerial returns the serial of the USB device backing ALSA card N, or "" when the device
// reports none (common on inexpensive audio adapters) or cannot be located.
func readUSBSerial(root string, card int) string {
	dir, ok := findUSBDeviceNode(root, card)
	if !ok {
		return ""
	}
	return readSysfsAttr(filepath.Join(dir, "serial"))
}

// usbIdentityFromSysfs derives a card's USB identity entirely from <root>/sys, the fallback used
// when /proc/asound is unavailable (Docker masks /proc/asound with an empty tmpfs, so
// /proc/asound/cards and /proc/asound/cardN/usbid do not exist even with --device /dev/snd,
// while /sys/class/sound is still exposed). It returns vendor:product:serial; the BusPath is left
// empty on purpose, because the sysfs bus-path string (e.g. "1-1") differs in format from the
// /proc bus path (e.g. "usb-xhci-hcd.0-1"), so a sysfs value could not match a /proc-derived
// usb-path token. The usb-id token (vendor:product:serial) is identical from both sources, so it
// stays a stable cross-reboot identifier here. Returns a zero usbIdentity for a non-USB card or
// when the vendor/product ids are missing or malformed.
func usbIdentityFromSysfs(root string, card int) usbIdentity {
	dir, ok := findUSBDeviceNode(root, card)
	if !ok {
		return usbIdentity{}
	}
	vendor := readSysfsAttr(filepath.Join(dir, "idVendor"))
	if !hex4Re.MatchString(vendor) {
		return usbIdentity{}
	}
	product := readSysfsAttr(filepath.Join(dir, "idProduct"))
	if !hex4Re.MatchString(product) {
		return usbIdentity{}
	}
	return usbIdentity{
		VendorID:  vendor,
		ProductID: product,
		Serial:    readSysfsAttr(filepath.Join(dir, "serial")),
	}
}
