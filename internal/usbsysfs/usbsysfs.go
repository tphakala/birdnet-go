// Package usbsysfs reads USB and USB-audio facts from the Linux sysfs tree.
//
// Functions that locate a sysfs node take a caller-supplied root that is
// prepended to the /sys paths, so tests can point them at a fake tree. The
// package has no build tag and depends only on the standard library and
// internal/errors; on a host without the sysfs paths the readers report "not
// found" rather than failing.
// The inventory listers Devices and SoundCards are the exception: they return
// the os error when their top-level directory cannot be listed, and ctx.Err()
// when the context is done before it is listed. Devices never reads the serial
// attribute.
package usbsysfs

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// maxWalkDepth bounds the upward sysfs walk in CardUSBDevice; the USB device node
// is normally only 3-4 levels above the card directory, so this is a generous cap
// that keeps the loop finite.
const maxWalkDepth = 12

// maxAttrBytes bounds how much of a sysfs attribute ReadAttr reads. The
// attributes this package reads (ids, speed, class, names) are far shorter.
const maxAttrBytes = 4096

// CardUSBDevice walks up from <root>/sys/class/sound/cardN to the USB device node and
// returns its directory. The device node is the first ancestor exposing an "idVendor" file; the
// walk stops there so a parent hub or host controller (e.g. "xhci-hcd.0" on a root hub) is never
// mistaken for the device, and is bounded to the <root>/sys subtree so a symlink that resolves
// outside sysfs cannot send it wandering into unrelated system directories. Returns ("", false)
// when the card is absent or is not backed by a USB device.
func CardUSBDevice(root string, card int) (string, bool) {
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
	for range maxWalkDepth {
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

// ReadAttr reads a single sysfs attribute file and returns its trimmed contents, or "" on
// any read error. It reads at most maxAttrBytes bytes; longer content is truncated.
func ReadAttr(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxAttrBytes))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
