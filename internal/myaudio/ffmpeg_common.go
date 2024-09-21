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
func getFFmpegFormat(sampleRate, numChannels, bitDepth int) (string, string, string) {
	// FFmpeg supports most common sample rates, so we can use it as-is
	ffmpegSampleRate := fmt.Sprintf("%d", sampleRate)

	// FFmpeg supports any number of channels, so we can use it as-is
	ffmpegNumChannels := fmt.Sprintf("%d", numChannels)

	// Map bit depth to FFmpeg-compatible format
	var ffmpegFormat string
	switch bitDepth {
	case 16:
		ffmpegFormat = "s16le"
	case 24:
		ffmpegFormat = "s24le"
	case 32:
		ffmpegFormat = "s32le"
	default:
		ffmpegFormat = "s16le" // Default to 16-bit if unsupported
	}

	return ffmpegSampleRate, ffmpegNumChannels, ffmpegFormat
}
