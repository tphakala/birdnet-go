package support

import (
	"context"
	"io/fs"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/usbsysfs"
)

// hardwareInventoryRoot is the filesystem root the inventory reads /sys under.
const hardwareInventoryRoot = "/"

// Values of the reason field of the usb and sound_cards sections.
const (
	hardwareReasonUnsupportedOS = "unsupported_os"
	hardwareReasonNotVisible    = "not_visible" // the sysfs directory does not exist, for example masked in a container
	hardwareReasonUnreadable    = "unreadable"  // it exists but cannot be listed
	hardwareReasonCancelled     = "cancelled"   // the dump context ended before the directory was listed
)

// collectHardwareInventory lists USB devices and ALSA cards under root. It never
// fails: an unsupported OS or an unreadable sysfs is reported through the Reason
// field, and nothing is sent to telemetry because an absent sysfs is expected.
func collectHardwareInventory(ctx context.Context, goos, root string) (*USBInventory, *SoundCardInventory) {
	if goos != osLinux {
		return &USBInventory{Reason: hardwareReasonUnsupportedOS},
			&SoundCardInventory{Reason: hardwareReasonUnsupportedOS}
	}

	usb := &USBInventory{}
	if devices, truncated, err := usbsysfs.Devices(ctx, root); err != nil {
		usb.Reason = hardwareReason(err)
	} else {
		usb.Available, usb.Truncated = true, truncated
		if len(devices) > 0 {
			usb.Devices = make([]USBDeviceInfo, 0, len(devices))
		}
		for _, d := range devices {
			usb.Devices = append(usb.Devices, USBDeviceInfo(d))
		}
	}

	sound := &SoundCardInventory{}
	if cards, truncated, err := usbsysfs.SoundCards(ctx, root); err != nil {
		sound.Reason = hardwareReason(err)
	} else {
		sound.Available, sound.Truncated = true, truncated
		if len(cards) > 0 {
			sound.Cards = make([]SoundCardInfo, 0, len(cards))
		}
		for _, c := range cards {
			sound.Cards = append(sound.Cards, SoundCardInfo(c))
		}
	}
	return usb, sound
}

// hardwareReason maps a sysfs listing error, or the context error returned when
// the dump context ended before listing, to a reason value.
func hardwareReason(err error) string {
	if errors.IsContextCancellation(err) {
		return hardwareReasonCancelled
	}
	if errors.Is(err, fs.ErrNotExist) {
		return hardwareReasonNotVisible
	}
	return hardwareReasonUnreadable
}
