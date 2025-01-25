package myaudio

import (
	"fmt"
)

// validateFFmpegPath checks if FFmpeg is available
func validateFFmpegPath(ffmpegPath string) error {
	if ffmpegPath == "" {
		return fmt.Errorf("FFmpeg is not available")
	}
	return nil
}

// getFFmpegCompatibleValues returns FFmpeg-compatible values for sample rate, channels, and bit depth
func getFFmpegFormat(sampleRate, numChannels, bitDepth int) (sampleRateStr, channelsStr, formatStr string) {
	// FFmpeg supports most common sample rates, so we can use it as-is
	sampleRateStr = fmt.Sprintf("%d", sampleRate)

	// FFmpeg supports any number of channels, so we can use it as-is
	channelsStr = fmt.Sprintf("%d", numChannels)

	// Map bit depth to FFmpeg-compatible format
	switch bitDepth {
	case 16:
		formatStr = "s16le"
	case 24:
		formatStr = "s24le"
	case 32:
		formatStr = "s32le"
	default:
		formatStr = "s16le" // Default to 16-bit if unsupported
	}

	return
}
