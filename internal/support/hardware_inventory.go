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
		for _, d := range devices {
			usb.Devices = append(usb.Devices, USBDeviceInfo(d))
		}
	}

	sound := &SoundCardInventory{}
	if cards, truncated, err := usbsysfs.SoundCards(ctx, root); err != nil {
		sound.Reason = hardwareReason(err)
	} else {
		sound.Available, sound.Truncated = true, truncated
		for _, c := range cards {
			sound.Cards = append(sound.Cards, SoundCardInfo(c))
		}
	}
	return usb, sound
}

// hardwareReason maps a sysfs listing error to a reason value.
func hardwareReason(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return hardwareReasonNotVisible
	}
	return hardwareReasonUnreadable
}
