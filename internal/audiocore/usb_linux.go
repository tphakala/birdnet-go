//go:build linux

package audiocore

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// maxUSBSysfsWalkDepth bounds the upward sysfs walk in readUSBSerial; the USB
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
// a probing failure. Bus path and USB detection come from /proc/asound/cards and
// vendor:product from /proc/asound/cardN/usbid (regular files, available even in
// Docker containers using --device /dev/snd). The serial is a best-effort sysfs
// read that is often empty. A persisted usb-path:/usb-id: token therefore depends
// on /proc/asound being readable; when it is not, the token matches nothing and
// capture reports the device as missing rather than risking a wrong-device match.
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

// usbIdentityForCard resolves the USB identity of a single ALSA card from an
// already-parsed cards map. Returns a zero usbIdentity for a non-USB card or an
// absent entry.
func usbIdentityForCard(card int, cards map[int]procCardEntry, root string) usbIdentity {
	entry, ok := cards[card]
	if !ok || !entry.isUSB {
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

// readUSBSerial walks up from <root>/sys/class/sound/cardN to the USB device node
// and returns that device's serial, or "" when the device reports none. The
// device node is the first ancestor exposing an "idVendor" file; the serial (when
// present) lives alongside it. The walk stops there so a parent hub or host
// controller's serial (e.g. "xhci-hcd.0" on a root hub) is never mistaken for the
// device's, and is bounded to the <root>/sys subtree so a symlink that resolves
// outside sysfs cannot send it wandering into unrelated system directories.
// Returns "" on any failure; the USB serial is optional and many audio adapters
// do not report one.
func readUSBSerial(root string, card int) string {
	sysRoot := filepath.Join(root, "sys")
	target, err := filepath.EvalSymlinks(filepath.Join(sysRoot, "class", "sound", "card"+strconv.Itoa(card)))
	if err != nil {
		return ""
	}
	dir := target
	for range maxUSBSysfsWalkDepth {
		// Stay within the sysfs subtree; if symlink resolution escaped it, stop.
		if dir != sysRoot && !strings.HasPrefix(dir, sysRoot+string(os.PathSeparator)) {
			break
		}
		if _, statErr := os.Stat(filepath.Join(dir, "idVendor")); statErr == nil {
			// Reached the USB device node. Its serial is optional.
			data, readErr := os.ReadFile(filepath.Join(dir, "serial"))
			if readErr != nil {
				return ""
			}
			return strings.TrimSpace(string(data))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
