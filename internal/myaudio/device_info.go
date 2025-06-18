package myaudio

import (
	"fmt"

	"github.com/tphakala/malgo"
)

// printDeviceInfo prints format information about the initialized capture device.
func printDeviceInfo(dev *malgo.Device, format malgo.FormatType) {
	fmt.Println("Device information:")
	fmt.Printf("  Format: %v\n", formatToString(format))
	fmt.Printf("  Sample Rate: %d\n", dev.SampleRate)
}

// formatToString converts a malgo format type to a human-readable string
func formatToString(format malgo.FormatType) string {
	switch format {
	case malgo.FormatS16:
		return "16-bit signed PCM"
	case malgo.FormatU8:
		return "8-bit unsigned PCM"
	case malgo.FormatS24:
		return "24-bit signed PCM"
	case malgo.FormatS32:
		return "32-bit signed PCM"
	case malgo.FormatF32:
		return "32-bit floating point"
	default:
		return fmt.Sprintf("Unknown format (%d)", format)
	}
}
