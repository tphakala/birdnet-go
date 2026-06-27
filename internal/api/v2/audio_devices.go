// internal/api/v2/audio_devices.go
package api

import (
	goerrors "errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// This file holds the /api/v2/system/audio/* device-management handlers
// (GetAudioDevices, GetDeviceCapabilities, GetActiveAudioDevice,
// GetEqualizerConfig). They physically lived in system.go but belong to the
// audio/streaming domain; they stay in package api until that domain is
// extracted in its own phase. They are registered by the trimmed initSystemRoutes
// in system_routes.go.

// Audio device constants
const (
	defaultAudioSampleRate = 48000 // Standard BirdNET audio sample rate
	defaultAudioBitDepth   = 16    // Standard audio bit depth
)

// AudioDeviceInfo wraps the audiocore.DeviceInfo struct for API responses
type AudioDeviceInfo struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	ID    string `json:"id"`
	// StableID is the reboot-stable identifier to persist for this device
	// ("usb-path:..." / "usb-id:..."), empty when no stable USB identity exists.
	// Clients should save this in preference to ID so the selection survives
	// reboots (GH #3651).
	StableID string `json:"stableId,omitempty"`
	// BusPath is the USB bus path for display (e.g. "usb-0000:00:14.0-3"),
	// empty for non-USB devices and non-Linux platforms.
	BusPath string `json:"busPath,omitempty"`
	// VendorID and ProductID are the 4-hex-digit USB ids, empty when unavailable.
	VendorID  string `json:"vendorId,omitempty"`
	ProductID string `json:"productId,omitempty"`
}

// ActiveAudioDevice represents the currently active audio device
type ActiveAudioDevice struct {
	Name       string `json:"name"`
	ID         string `json:"id"`
	SampleRate int    `json:"sample_rate"`
	BitDepth   int    `json:"bit_depth"`
	Channels   int    `json:"channels"`
}

// GetAudioDevices handles GET /api/v2/system/audio/devices
func (c *Controller) GetAudioDevices(ctx echo.Context) error {
	c.LogInfoIfEnabled("Getting audio devices",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get audio devices
	devices, err := audiocore.ListCaptureDevices()
	if err != nil {
		c.LogErrorIfEnabled("Failed to list audio devices",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to list audio devices", http.StatusInternalServerError)
	}

	// Check if no devices were found
	if len(devices) == 0 {
		c.Debug("No audio devices found on the system")
		c.LogWarnIfEnabled("No audio devices found on the system",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
			logger.String("os", runtime.GOOS),
		)
		return ctx.JSON(http.StatusOK, []AudioDeviceInfo{}) // Return empty array instead of null
	}

	// Convert to API response format
	apiDevices := make([]AudioDeviceInfo, len(devices))
	for i, device := range devices {
		apiDevices[i] = AudioDeviceInfo{
			Index:     device.Index,
			Name:      device.Name,
			ID:        device.ID,
			StableID:  device.StableID,
			BusPath:   device.BusPath,
			VendorID:  device.VendorID,
			ProductID: device.ProductID,
		}
	}

	deviceNames := make([]string, len(devices))
	for i, device := range devices {
		deviceNames[i] = device.Name
	}

	c.LogInfoIfEnabled("Audio devices retrieved successfully",
		logger.Int("device_count", len(apiDevices)),
		logger.String("devices", strings.Join(deviceNames, ", ")),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, apiDevices)
}

// GetDeviceCapabilities handles GET /api/v2/system/audio/devices/capabilities
// Probes a specific audio device to discover supported sample rates.
func (c *Controller) GetDeviceCapabilities(ctx echo.Context) error {
	deviceID := ctx.QueryParam("deviceId")
	if deviceID == "" {
		return c.HandleError(ctx, nil, "deviceId query parameter is required", http.StatusBadRequest)
	}

	c.LogInfoIfEnabled("Probing device capabilities",
		logger.String("device_id", deviceID),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	caps, err := audiocore.ProbeDeviceCapabilities(deviceID, c.APILogger)
	if err != nil {
		if goerrors.Is(err, audiocore.ErrDeviceNotFound) {
			return c.HandleError(ctx, err, "Device not found", http.StatusNotFound)
		}
		c.LogErrorIfEnabled("Failed to probe device capabilities",
			logger.Error(err),
			logger.String("device_id", deviceID),
		)
		return c.HandleError(ctx, err, "Failed to probe device capabilities", http.StatusInternalServerError)
	}

	c.LogInfoIfEnabled("Device capabilities retrieved",
		logger.String("device_id", caps.DeviceID),
		logger.String("device_name", caps.DeviceName),
		logger.Int("rate_count", len(caps.SampleRates)),
		logger.Bool("verified", caps.Verified),
	)

	return ctx.JSON(http.StatusOK, caps)
}

// GetActiveAudioDevice handles GET /api/v2/system/audio/active
func (c *Controller) GetActiveAudioDevice(ctx echo.Context) error {
	c.LogInfoIfEnabled("Getting active audio device",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get active audio device from settings (first configured source)
	var deviceName string
	settings := c.CurrentSettings()
	if settings != nil && len(settings.Realtime.Audio.Sources) > 0 {
		deviceName = settings.Realtime.Audio.Sources[0].Device
	}

	// Check if no device is configured
	if deviceName == "" {
		c.LogInfoIfEnabled("No audio device currently active",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return ctx.JSON(http.StatusOK, map[string]any{
			"device":   nil,
			"active":   false,
			"verified": false,
			"message":  "No audio device currently active",
		})
	}

	// Create response with default values
	activeDevice := ActiveAudioDevice{
		Name:       deviceName,
		SampleRate: defaultAudioSampleRate, // Standard BirdNET sample rate
		BitDepth:   defaultAudioBitDepth,   // Assuming 16-bit as per the capture.go implementation
		Channels:   1,                      // Assuming mono as per the capture.go implementation
	}

	// Diagnostic information map
	diagnostics := map[string]any{
		"os":                runtime.GOOS,
		"check_time":        time.Now().Format(time.RFC3339),
		"error_details":     nil,
		"device_found":      false,
		"available_devices": []string{},
	}

	// Try to get additional device info and validate the device exists
	devices, err := audiocore.ListCaptureDevices()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to list audio devices: %v", err)
		c.Debug("%s", errorMsg)

		// Add more detailed diagnostics
		diagnostics["error_details"] = errorMsg

		// OS-specific additional checks
		switch runtime.GOOS {
		case OSWindows:
			diagnostics["note"] = "On Windows, check that audio drivers are properly installed and the device is not disabled in Sound settings"
		case OSDarwin:
			diagnostics["note"] = "On macOS, check System Preferences > Sound and ensure the device has proper permissions"
		case OSLinux:
			diagnostics["note"] = "On Linux, check if PulseAudio/ALSA is running and the user has proper permissions"
		}

		c.LogWarnIfEnabled("Failed to list audio devices for verification",
			logger.String("device_name", deviceName),
			logger.Error(err),
			logger.String("os", runtime.GOOS),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)

		// Still return the configured device, but note that we couldn't verify it exists
		return ctx.JSON(http.StatusOK, map[string]any{
			"device":      activeDevice,
			"active":      true,
			"verified":    false,
			"message":     "Device configured but could not verify if it exists",
			"diagnostics": diagnostics,
		})
	}

	// Populate available devices for diagnostics
	availableDevices := make([]string, len(devices))
	for i, device := range devices {
		availableDevices[i] = device.Name
	}
	diagnostics["available_devices"] = availableDevices

	// Check if the configured device exists in the system
	deviceFound := false
	for _, device := range devices {
		if device.ID != deviceName && device.Name != deviceName {
			continue
		}

		activeDevice.Name = device.Name
		activeDevice.ID = device.ID
		deviceFound = true
		diagnostics["device_found"] = true

		break
	}

	if !deviceFound {
		// Device is configured but not found on the system
		errorMsg := "Configured audio device not found on the system"
		diagnostics["suggested_action"] = "Check if the device is properly connected and recognized by the system"

		if len(devices) > 0 {
			diagnostics["suggestion"] = fmt.Sprintf("Consider using one of the available devices: %s", strings.Join(availableDevices, ", "))
		}

		c.LogWarnIfEnabled("Configured audio device not found on system",
			logger.String("configured_device", deviceName),
			logger.String("available_devices", strings.Join(availableDevices, ", ")),
			logger.String("os", runtime.GOOS),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)

		return ctx.JSON(http.StatusOK, map[string]any{
			"device":      activeDevice,
			"active":      true,
			"verified":    false,
			"message":     errorMsg,
			"diagnostics": diagnostics,
		})
	}

	c.LogInfoIfEnabled("Active audio device verified",
		logger.String("device_name", deviceName),
		logger.String("device_id", activeDevice.ID),
		logger.Int("sample_rate", activeDevice.SampleRate),
		logger.Int("bit_depth", activeDevice.BitDepth),
		logger.Int("channels", activeDevice.Channels),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Device is configured and verified to exist
	return ctx.JSON(http.StatusOK, map[string]any{
		"device":      activeDevice,
		"active":      true,
		"verified":    true,
		"diagnostics": diagnostics,
	})
}

// GetEqualizerConfig handles GET /api/v2/system/audio/equalizer/config
func (c *Controller) GetEqualizerConfig(ctx echo.Context) error {
	c.LogInfoIfEnabled("Getting equalizer filter configuration",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Set cache headers for static configuration data
	ctx.Response().Header().Set("Cache-Control", "public, max-age=3600")

	// Return the equalizer filter configuration
	return ctx.JSON(http.StatusOK, conf.EqFilterConfig)
}
