// Package sources provides audio source implementations
package sources

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/sources/malgo"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// CreateSource creates an audio source based on the provided configuration
func CreateSource(config audiocore.SourceConfig, bufferPool audiocore.BufferPool) (audiocore.AudioSource, error) {
	switch config.Type {
	case "malgo", "soundcard":
		// Extract malgo-specific configuration
		malgoConfig := malgo.MalgoConfig{
			DeviceName:   config.Device,
			SampleRate:   uint32(config.Format.SampleRate),
			Channels:     uint8(config.Format.Channels),
			BufferFrames: 512, // Default buffer frames
			Gain:         config.Gain,
		}

		// Check for buffer frames in extra config
		if frames, ok := config.ExtraConfig["buffer_frames"].(uint32); ok {
			malgoConfig.BufferFrames = frames
		} else if frames, ok := config.ExtraConfig["buffer_frames"].(int); ok {
			malgoConfig.BufferFrames = uint32(frames)
		}

		return malgo.NewMalgoSource(config.ID, malgoConfig, bufferPool)

	case "rtsp":
		// TODO: Implement RTSP source
		return nil, errors.New(nil).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("source_type", config.Type).
			Context("error", "RTSP source not yet implemented").
			Build()

	case "file":
		// TODO: Implement file source
		return nil, errors.New(nil).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("source_type", config.Type).
			Context("error", "file source not yet implemented").
			Build()

	default:
		return nil, errors.New(nil).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("source_type", config.Type).
			Context("error", fmt.Sprintf("unknown source type: %s", config.Type)).
			Build()
	}
}

// ListAvailableDevices returns a list of available audio capture devices
func ListAvailableDevices() ([]malgo.AudioDeviceInfo, error) {
	return malgo.EnumerateDevices()
}

// GetDefaultDevice returns the system default audio capture device
func GetDefaultDevice() (*malgo.AudioDeviceInfo, error) {
	return malgo.GetDefaultDevice()
}