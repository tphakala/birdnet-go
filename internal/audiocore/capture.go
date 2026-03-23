// Package audiocore provides the core audio infrastructure for BirdNET-Go.
// capture.go — per-device malgo capture initialisation and callback.
package audiocore

import (
	"context"
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Platform OS name constants used for backend selection.
const (
	captureOSLinux   = "linux"
	captureOSWindows = "windows"
	captureOSDarwin  = "darwin"
)

// platformBackend returns the preferred malgo backend for the current OS.
func platformBackend() malgo.Backend {
	switch runtime.GOOS {
	case captureOSLinux:
		return malgo.BackendAlsa
	case captureOSWindows:
		return malgo.BackendWasapi
	case captureOSDarwin:
		return malgo.BackendCoreaudio
	default:
		return malgo.BackendNull
	}
}

// hexToASCII converts a hexadecimal string (as returned by malgo's
// DeviceID.String()) to its ASCII representation.
func hexToASCII(hexStr string) (string, error) {
	decoded, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// listDevices enumerates capture devices on the host using malgo.
// It skips null/discard devices and deduplicates by name (ALSA pseudo-devices).
func listDevices(log logger.Logger) ([]DeviceInfo, error) {
	backend := platformBackend()

	ctx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize audio context: %w", err)
	}
	defer func() {
		if uninitErr := ctx.Uninit(); uninitErr != nil {
			log.Error("failed to uninitialize audio context", logger.Error(uninitErr))
		}
	}()

	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate capture devices: %w", err)
	}

	devices := make([]DeviceInfo, 0, len(infos))
	seenNames := make(map[string]bool, len(infos))

	for i := range infos {
		name := infos[i].Name()
		if strings.Contains(name, "Discard all samples") {
			continue
		}

		decodedID, err := hexToASCII(infos[i].ID.String())
		if err != nil {
			log.Error("error decoding device ID",
				logger.Int("device_index", i),
				logger.Error(err))
			continue
		}

		// ALSA enumerates multiple pseudo-devices per card; deduplicate by name.
		if seenNames[name] {
			continue
		}
		seenNames[name] = true

		devices = append(devices, DeviceInfo{
			Index: i,
			Name:  name,
			ID:    decodedID,
		})
	}

	return devices, nil
}

// matchesDevice reports whether the device identified by (decodedID, info)
// should be selected for the requested deviceID string.
//
// On Windows and macOS the "default"/"sysdefault" token selects the system
// default device. Otherwise, either a substring of the name or an exact ID
// match is accepted.
func matchesDevice(decodedID string, info *malgo.DeviceInfo, deviceID string) bool {
	if (runtime.GOOS == captureOSWindows || runtime.GOOS == captureOSDarwin) &&
		(deviceID == "sysdefault" || deviceID == "default") {
		return info.IsDefault == 1
	}
	return decodedID == deviceID || strings.Contains(info.Name(), deviceID)
}

// startCapture locates the requested device, initialises a malgo context and
// device, and starts the capture goroutine. It returns the DeviceInfo for the
// selected device and a done channel that is closed when the capture goroutine
// exits.
//
// The capture goroutine will be stopped when ctx is cancelled.
func startCapture(
	ctx context.Context,
	sourceID string,
	deviceID string,
	cfg DeviceConfig,
	dispatcher AudioDispatcher,
	log logger.Logger,
) (DeviceInfo, chan struct{}, error) {

	backend := platformBackend()

	malgoCtx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
	if err != nil {
		return DeviceInfo{}, nil, fmt.Errorf("audio context init failed: %w", err)
	}

	infos, err := malgoCtx.Devices(malgo.Capture)
	if err != nil {
		_ = malgoCtx.Uninit()
		return DeviceInfo{}, nil, fmt.Errorf("enumerate capture devices: %w", err)
	}

	// Find the device matching deviceID.
	var selectedInfo *malgo.DeviceInfo
	var selectedDevInfo DeviceInfo
	for i := range infos {
		decodedID, decErr := hexToASCII(infos[i].ID.String())
		if decErr != nil {
			continue
		}
		if matchesDevice(decodedID, &infos[i], deviceID) {
			selectedInfo = &infos[i]
			selectedDevInfo = DeviceInfo{
				Index: i,
				Name:  infos[i].Name(),
				ID:    decodedID,
			}
			break
		}
	}

	if selectedInfo == nil {
		_ = malgoCtx.Uninit()
		return DeviceInfo{}, nil, fmt.Errorf("no device found matching %q", deviceID)
	}

	deviceCfg := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceCfg.Capture.Channels = uint32(cfg.Channels)
	deviceCfg.SampleRate = uint32(cfg.SampleRate)
	deviceCfg.Alsa.NoMMap = 1
	deviceCfg.Capture.DeviceID = selectedInfo.ID.Pointer()

	var captureDevice *malgo.Device
	var formatType malgo.FormatType

	onReceiveFrames := func(_, pSamples []byte, _ uint32) {
		if len(pSamples) == 0 {
			return
		}

		data := make([]byte, len(pSamples))
		copy(data, pSamples)

		frame := AudioFrame{
			SourceID:   sourceID,
			SourceName: selectedDevInfo.Name,
			Data:       data,
			SampleRate: cfg.SampleRate,
			BitDepth:   formatBitDepth(formatType, cfg.BitDepth),
			Channels:   cfg.Channels,
			Timestamp:  time.Now(),
		}

		dispatcher.Dispatch(frame)
	}

	callbacks := malgo.DeviceCallbacks{
		Data: onReceiveFrames,
	}

	captureDevice, err = malgo.InitDevice(malgoCtx.Context, deviceCfg, callbacks)
	if err != nil {
		_ = malgoCtx.Uninit()
		return DeviceInfo{}, nil, fmt.Errorf("device init failed for %q: %w", selectedDevInfo.Name, err)
	}

	// Capture the actual format reported by the device after init.
	formatType = captureDevice.CaptureFormat()

	if err = captureDevice.Start(); err != nil {
		captureDevice.Uninit()
		_ = malgoCtx.Uninit()
		return DeviceInfo{}, nil, fmt.Errorf("device start failed for %q: %w", selectedDevInfo.Name, err)
	}

	log.Info("malgo capture device started",
		logger.String("source_id", sourceID),
		logger.String("device", selectedDevInfo.Name),
		logger.Int("sample_rate", cfg.SampleRate),
		logger.Int("channels", cfg.Channels))

	// done is closed when the capture goroutine exits, allowing callers to
	// wait for graceful device teardown.
	done := make(chan struct{})

	// Background goroutine that owns device and context lifetime.
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic in capture goroutine",
					logger.String("source_id", sourceID),
					logger.Any("panic", r))
			}
		}()
		defer func() {
			_ = captureDevice.Stop()
			captureDevice.Uninit()
			_ = malgoCtx.Uninit()
			log.Info("malgo capture device stopped",
				logger.String("source_id", sourceID),
				logger.String("device", selectedDevInfo.Name))
		}()

		<-ctx.Done()
	}()

	return selectedDevInfo, done, nil
}

// formatBitDepth returns the bit depth implied by the malgo format type,
// falling back to the requested bit depth when the format is not recognised.
func formatBitDepth(format malgo.FormatType, requested int) int {
	switch format {
	case malgo.FormatU8:
		return 8
	case malgo.FormatS16:
		return 16
	case malgo.FormatS24:
		return 24
	case malgo.FormatS32:
		return 32
	case malgo.FormatF32:
		return 32
	default:
		return requested
	}
}
