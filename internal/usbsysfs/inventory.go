package usbsysfs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// maxDevices caps the number of USB devices Devices returns.
	maxDevices = 128
	// maxSoundCards caps the number of ALSA cards SoundCards returns.
	maxSoundCards = 32
	// maxDirEntries caps how many entries of one directory listing are examined.
	maxDirEntries = 1024
	// maxTextRunes caps the length of a free-text field such as a product name.
	maxTextRunes = 128
)

// Sysfs directories (relative to root) and attribute names the inventory reads.
// The serial attribute is deliberately absent.
const (
	usbDevicesDir = "sys/bus/usb/devices"
	soundClassDir = "sys/class/sound"

	attrIDVendor     = "idVendor"
	attrIDProduct    = "idProduct"
	attrManufacturer = "manufacturer"
	attrProduct      = "product"
	attrSpeed        = "speed"
	attrDeviceClass  = "bDeviceClass"
	attrCardID       = "id"
)

var (
	// busIDRe matches kernel USB device names: usbN for root hubs and
	// B-P[.P...] for devices. Interface names (B-P:C.I) do not match.
	busIDRe = regexp.MustCompile(`^(usb\d+|\d+-\d+(\.\d+)*)$`)
	// hex4Re matches a four digit hex id (idVendor, idProduct).
	hex4Re = regexp.MustCompile(`^[0-9a-fA-F]{4}$`)
	// hex2Re matches a two digit hex value (bDeviceClass).
	hex2Re = regexp.MustCompile(`^[0-9a-fA-F]{2}$`)
	// speedRe matches the kernel speed in Mbit/s, such as 1.5, 480 or 20000.
	speedRe = regexp.MustCompile(`^\d{1,6}(\.\d{1,2})?$`)
	// cardNameRe matches canonical ALSA card directory names and captures the index.
	cardNameRe = regexp.MustCompile(`^card(0|[1-9]\d*)$`)
	// captureRe matches a capture PCM entry inside a card directory.
	captureRe = regexp.MustCompile(`^pcmC\d+D\d+c$`)
)

// Device is one USB device node listed under <root>/sys/bus/usb/devices. Every
// field except BusID is empty when its attribute is absent or malformed.
type Device struct {
	BusID        string // kernel device name, for example "1-1.2" or "usb1"
	VendorID     string // idVendor, four hex digits
	ProductID    string // idProduct, four hex digits
	Manufacturer string // sanitised free text
	Product      string // sanitised free text
	SpeedMbps    string // speed in Mbit/s as the kernel reports it, for example "480" or "1.5"
	DeviceClass  string // bDeviceClass, two hex digits
}

// SoundCard is one ALSA card listed under <root>/sys/class/sound.
type SoundCard struct {
	Index      int
	ID         string // sanitised card id
	USBBusID   string // BusID of the backing USB device, "" for a non-USB card
	HasCapture bool   // the card has at least one capture PCM
}

// Devices lists the USB devices (hubs included, interfaces excluded) under
// <root>/sys/bus/usb/devices, sorted by BusID as plain text and capped at
// maxDevices. truncated reports a cap, an oversized directory or cancellation
// after listing. err is ctx.Err() when ctx is done before the directory is listed, or the os
// error when the directory cannot be listed; nothing else is an error. A ctx
// cancelled after listing returns what was collected so far and a nil error.
// It never reads the serial attribute.
func Devices(ctx context.Context, root string) (devices []Device, truncated bool, err error) {
	if cancelled(ctx) {
		return nil, false, ctx.Err()
	}
	base := filepath.Join(root, usbDevicesDir)
	names, truncated, err := listDir(base)
	if err != nil {
		return nil, false, err
	}
	names = slices.DeleteFunc(names, func(n string) bool { return !busIDRe.MatchString(n) })
	slices.Sort(names)
	if len(names) > maxDevices {
		names, truncated = names[:maxDevices], true
	}
	devices = make([]Device, 0, len(names))
	for _, name := range names {
		if cancelled(ctx) {
			return devices, true, nil
		}
		dir := filepath.Join(base, name)
		devices = append(devices, Device{
			BusID:        name,
			VendorID:     validOrEmpty(hex4Re, ReadAttr(filepath.Join(dir, attrIDVendor))),
			ProductID:    validOrEmpty(hex4Re, ReadAttr(filepath.Join(dir, attrIDProduct))),
			Manufacturer: sanitizeText(ReadAttr(filepath.Join(dir, attrManufacturer))),
			Product:      sanitizeText(ReadAttr(filepath.Join(dir, attrProduct))),
			SpeedMbps:    validOrEmpty(speedRe, ReadAttr(filepath.Join(dir, attrSpeed))),
			DeviceClass:  validOrEmpty(hex2Re, ReadAttr(filepath.Join(dir, attrDeviceClass))),
		})
	}
	return devices, truncated, nil
}

// SoundCards lists the ALSA cards under <root>/sys/class/sound in numeric
// order, capped at maxSoundCards. USBBusID links a card to its USB device via
// CardUSBDevice. truncated reports a cap, an oversized directory (the class
// directory or a card directory), a card directory that cannot be listed or
// cancellation after listing. err is ctx.Err() when ctx is
// done before the class directory is listed, or the os error when it cannot be
// listed; nothing else is an error. A ctx cancelled after listing returns what
// was collected so far and a nil error.
func SoundCards(ctx context.Context, root string) (cards []SoundCard, truncated bool, err error) {
	if cancelled(ctx) {
		return nil, false, ctx.Err()
	}
	base := filepath.Join(root, soundClassDir)
	names, truncated, err := listDir(base)
	if err != nil {
		return nil, false, err
	}
	type cardEntry struct {
		index int
		name  string
	}
	entries := make([]cardEntry, 0, len(names))
	for _, name := range names {
		m := cardNameRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		idx, convErr := strconv.Atoi(m[1])
		if convErr != nil {
			continue
		}
		entries = append(entries, cardEntry{index: idx, name: name})
	}
	slices.SortFunc(entries, func(a, b cardEntry) int { return a.index - b.index })
	if len(entries) > maxSoundCards {
		entries, truncated = entries[:maxSoundCards], true
	}
	cards = make([]SoundCard, 0, len(entries))
	for _, e := range entries {
		if cancelled(ctx) {
			return cards, true, nil
		}
		dir := filepath.Join(base, e.name)
		card := SoundCard{Index: e.index, ID: sanitizeText(ReadAttr(filepath.Join(dir, attrCardID)))}
		if dev, ok := CardUSBDevice(root, e.index); ok {
			if busID := filepath.Base(dev); busIDRe.MatchString(busID) {
				card.USBBusID = busID
			}
		}
		if entries, cut, listErr := listDir(dir); listErr == nil {
			card.HasCapture = slices.ContainsFunc(entries, captureRe.MatchString)
			truncated = truncated || cut
		} else {
			truncated = true // HasCapture is unknown for this card
		}
		cards = append(cards, card)
	}
	return cards, truncated, nil
}

// cancelled reports whether ctx is done. Once a listing has started the
// listers return partial results without an error on cancellation, so this is
// a bool rather than ctx.Err().
func cancelled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// listDir returns up to maxDirEntries entry names of dir in directory order.
// truncated is set when the directory holds more. An empty directory is not an
// error.
func listDir(dir string) (names []string, truncated bool, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	names, readErr := f.Readdirnames(maxDirEntries + 1)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, false, readErr
	}
	if len(names) > maxDirEntries {
		return names[:maxDirEntries], true, nil
	}
	return names, false, nil
}

// validOrEmpty returns s when it matches re, else "".
func validOrEmpty(re *regexp.Regexp, s string) string {
	if re.MatchString(s) {
		return s
	}
	return ""
}

// sanitizeText makes vendor-controlled free text safe for the unscrubbed
// support dump: invalid UTF-8 becomes U+FFFD, each whitespace character becomes
// one space (the ends are trimmed), control and format characters (such as the
// ESC that starts an escape sequence, and bidi marks) are dropped while the
// printable rest of an escape sequence stays, and the result is cut to
// maxTextRunes runes.
func sanitizeText(s string) string {
	s = strings.ToValidUTF8(s, string(utf8.RuneError))
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxTextRunes {
		s = strings.TrimSpace(string([]rune(s)[:maxTextRunes]))
	}
	return s
}
