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
	devices, truncated, err := usbsysfs.Devices(ctx, root)
	if err != nil {
		usb.Reason = hardwareReason(err)
	} else {
		usb.Available = true
		usb.Truncated = truncated
		if len(devices) > 0 {
			usb.Devices = make([]USBDeviceInfo, 0, len(devices))
			for _, d := range devices {
				usb.Devices = append(usb.Devices, USBDeviceInfo{
					BusID:        d.BusID,
					VendorID:     d.VendorID,
					ProductID:    d.ProductID,
					Manufacturer: d.Manufacturer,
					Product:      d.Product,
					SpeedMbps:    d.Speed,
					DeviceClass:  d.DeviceClass,
				})
			}
		}
	}

	sound := &SoundCardInventory{}
	cards, truncated, err := usbsysfs.SoundCards(ctx, root)
	if err != nil {
		sound.Reason = hardwareReason(err)
	} else {
		sound.Available = true
		sound.Truncated = truncated
		if len(cards) > 0 {
			sound.Cards = make([]SoundCardInfo, 0, len(cards))
			for _, c := range cards {
				sound.Cards = append(sound.Cards, SoundCardInfo{
					Index:      c.Index,
					ID:         c.ID,
					USBBusID:   c.USBBusID,
					HasCapture: c.HasCapture,
				})
			}
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
