package malgo

import (
	"encoding/hex"
	"runtime"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/malgo"
)

// AudioDeviceInfo holds information about an audio device
type AudioDeviceInfo struct {
	Index int
	Name  string
	ID    string
}

// getBackendForPlatform returns the appropriate malgo backend for the current platform
func getBackendForPlatform() (malgo.Backend, error) {
	switch runtime.GOOS {
	case "linux":
		return malgo.BackendAlsa, nil
	case "windows":
		return malgo.BackendWasapi, nil
	case "darwin":
		return malgo.BackendCoreaudio, nil
	default:
		return malgo.BackendNull, errors.New(nil).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("error", "unsupported operating system").
			Context("os", runtime.GOOS).
			Build()
	}
}

// EnumerateDevices returns a list of available audio capture devices
func EnumerateDevices() ([]AudioDeviceInfo, error) {
	// Determine backend
	backend, err := getBackendForPlatform()
	if err != nil {
		return nil, err
	}

	// Initialize context
	ctx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("operation", "init_context").
			Context("backend", runtime.GOOS).
			Build()
	}
	defer func() { _ = ctx.Uninit() }()

	// Get capture devices
	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("operation", "enumerate_devices").
			Build()
	}

	// Pre-allocate slice
	devices := make([]AudioDeviceInfo, 0, len(infos))

	// Process each device
	for i := range infos {
		// Skip the discard/null device
		if strings.Contains(infos[i].Name(), "Discard all samples") {
			continue
		}

		// Decode device ID
		decodedID, err := hexToASCII(infos[i].ID.String())
		if err != nil {
			// Log error but continue
			decodedID = infos[i].ID.String()
		}

		devices = append(devices, AudioDeviceInfo{
			Index: i,
			Name:  infos[i].Name(),
			ID:    decodedID,
		})
	}

	return devices, nil
}

// SelectDevice finds a device matching the given name or ID
func SelectDevice(devices []malgo.DeviceInfo, deviceName string) (*malgo.DeviceInfo, error) {
	if deviceName == "" || deviceName == "default" || deviceName == "sysdefault" {
		// Find default device
		for i := range devices {
			if devices[i].IsDefault == 1 {
				return &devices[i], nil
			}
		}
		// No default found, use first device
		if len(devices) > 0 {
			return &devices[0], nil
		}
	}

	// Find by exact name match first
	for i := range devices {
		if devices[i].Name() == deviceName {
			return &devices[i], nil
		}
	}

	// Find by decoded ID match
	for i := range devices {
		decodedID, err := hexToASCII(devices[i].ID.String())
		if err == nil && decodedID == deviceName {
			return &devices[i], nil
		}
	}

	// Find by partial name match
	for i := range devices {
		if strings.Contains(devices[i].Name(), deviceName) {
			return &devices[i], nil
		}
	}

	// Windows special case: check for default device alias
	if runtime.GOOS == "windows" && deviceName == "sysdefault" {
		for i := range devices {
			if devices[i].IsDefault == 1 {
				return &devices[i], nil
			}
		}
	}

	return nil, errors.New(nil).
		Component("audiocore").
		Category(errors.CategoryValidation).
		Context("device_name", deviceName).
		Context("available_devices", len(devices)).
		Context("error", "no matching audio device found").
		Build()
}

// TestDevice attempts to initialize and start a device to verify it works
func TestDevice(ctx *malgo.AllocatedContext, deviceInfo *malgo.DeviceInfo) error {
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Channels = 1
	deviceConfig.Capture.DeviceID = deviceInfo.ID.Pointer()
	deviceConfig.SampleRate = 48000
	deviceConfig.Alsa.NoMMap = 1

	// Try to initialize the device
	device, err := malgo.InitDevice(ctx.Context, deviceConfig, malgo.DeviceCallbacks{})
	if err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("device_name", deviceInfo.Name()).
			Context("operation", "test_init_device").
			Build()
	}
	defer device.Uninit()

	// Try to start the device
	if err := device.Start(); err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("device_name", deviceInfo.Name()).
			Context("operation", "test_start_device").
			Build()
	}

	// Stop the device
	_ = device.Stop()
	return nil
}

// hexToASCII converts a hexadecimal string to an ASCII string
func hexToASCII(hexStr string) (string, error) {
	bytes, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// isHardwareDevice checks if the device ID indicates a hardware device
func isHardwareDevice(decodedID string) bool {
	// On Linux, hardware devices have IDs in the format ":X,Y"
	if runtime.GOOS == "linux" {
		return strings.Contains(decodedID, ":") && strings.Contains(decodedID, ",")
	}
	// On Windows and macOS, consider all devices as potential hardware devices
	return true
}

// GetHardwareDevices filters devices to return only hardware devices
func GetHardwareDevices() ([]AudioDeviceInfo, error) {
	devices, err := EnumerateDevices()
	if err != nil {
		return nil, err
	}

	hardwareDevices := make([]AudioDeviceInfo, 0, len(devices))
	for _, device := range devices {
		if isHardwareDevice(device.ID) {
			hardwareDevices = append(hardwareDevices, device)
		}
	}

	return hardwareDevices, nil
}

// GetDefaultDevice returns the system default capture device
func GetDefaultDevice() (*AudioDeviceInfo, error) {
	// Determine backend
	backend, err := getBackendForPlatform()
	if err != nil {
		return nil, err
	}

	// Initialize context
	ctx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("operation", "init_context").
			Build()
	}
	defer func() { _ = ctx.Uninit() }()

	// Get capture devices
	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("operation", "enumerate_devices").
			Build()
	}

	// Find default device
	for i := range infos {
		if infos[i].IsDefault == 1 {
			decodedID, _ := hexToASCII(infos[i].ID.String())
			return &AudioDeviceInfo{
				Index: i,
				Name:  infos[i].Name(),
				ID:    decodedID,
			}, nil
		}
	}

	// No default found, use first device if available
	if len(infos) > 0 {
		decodedID, _ := hexToASCII(infos[0].ID.String())
		return &AudioDeviceInfo{
			Index: 0,
			Name:  infos[0].Name(),
			ID:    decodedID,
		}, nil
	}

	return nil, errors.New(nil).
		Component("audiocore").
		Category(errors.CategoryAudio).
		Context("error", "no audio capture devices found").
		Build()
}
