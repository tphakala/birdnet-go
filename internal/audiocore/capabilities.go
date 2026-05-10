// Package audiocore provides the core audio infrastructure for BirdNET-Go.
// capabilities.go - device sample rate capability probing for high-frequency capture.
package audiocore

import (
	"fmt"
	"maps"
	"runtime"
	"slices"

	"github.com/gen2brain/malgo"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// CandidateSampleRates are the rates tested during device probing.
// Must remain sorted ascending.
var CandidateSampleRates = []int{48000, 96000, 192000, 256000, 384000}

// MinCaptureSampleRate is the lowest rate considered useful for capture.
const MinCaptureSampleRate = 48000

// MaxCaptureSampleRate is the highest rate probed during capability discovery.
const MaxCaptureSampleRate = 384000

// ErrDeviceNotFound is returned when ProbeDeviceCapabilities cannot find a
// device matching the requested ID.
var ErrDeviceNotFound = errors.Newf("device not found").
	Component("audiocore.capabilities").Category(errors.CategoryAudioSource).Build()

// DeviceCapabilities describes the sample rates a capture device supports.
type DeviceCapabilities struct {
	DeviceID    string `json:"deviceId"`
	DeviceName  string `json:"deviceName"`
	SampleRates []int  `json:"sampleRates"`
	Verified    bool   `json:"verified"`
}

// ProbeDeviceCapabilities queries supported sample rates for a device.
// Fast path: reads from the device's Formats array (nativeDataFormats).
// Slow path (ALSA only): probes by attempting InitDevice at each candidate rate.
// On non-ALSA backends (macOS/Windows), returns unverified candidates if Formats are unavailable.
func ProbeDeviceCapabilities(deviceID string, log logger.Logger) (*DeviceCapabilities, error) {
	backend := platformBackend()

	malgoCtx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore.capabilities").
			Category(errors.CategoryAudioSource).
			Context("operation", "probe_context_init").
			Context("device_id", deviceID).
			Build()
	}
	defer uninitAndFreeContext(malgoCtx, log)

	infos, err := malgoCtx.Devices(malgo.Capture)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore.capabilities").
			Category(errors.CategoryAudioSource).
			Context("operation", "probe_enumerate_devices").
			Context("device_id", deviceID).
			Build()
	}

	// Find the matching device.
	var selectedInfo *malgo.DeviceInfo
	var deviceName string

	for i := range infos {
		decodedID, decErr := hexToASCII(infos[i].ID.String())
		if decErr != nil {
			log.Warn("failed to decode device ID during probe",
				logger.Int("device_index", i),
				logger.Error(decErr))
			continue
		}
		if matchesDevice(decodedID, &infos[i], deviceID) {
			selectedInfo = &infos[i]
			deviceName = infos[i].Name()
			break
		}
	}

	if selectedInfo == nil {
		return nil, fmt.Errorf("probe device %q: %w", deviceID, ErrDeviceNotFound)
	}

	// Fast path: extract rates from the device's native format list.
	rates := extractSampleRates(selectedInfo.Formats)

	if len(rates) > 0 {
		log.Debug("device capabilities from native formats",
			logger.String("device_id", deviceID),
			logger.String("device_name", deviceName),
			logger.Any("sample_rates", rates))
		return &DeviceCapabilities{
			DeviceID:    deviceID,
			DeviceName:  deviceName,
			SampleRates: rates,
			Verified:    true,
		}, nil
	}

	// Slow path: formats are all zero or empty.
	if runtime.GOOS == captureOSLinux {
		log.Info("probing device by init (ALSA zero-rate formats)",
			logger.String("device_id", deviceID),
			logger.String("device_name", deviceName))
		rates = probeByInit(malgoCtx, selectedInfo, log)
		if len(rates) > 0 {
			return &DeviceCapabilities{
				DeviceID:    deviceID,
				DeviceName:  deviceName,
				SampleRates: rates,
				Verified:    true,
			}, nil
		}
	}

	// Non-Linux or probing yielded nothing: return unverified fallback.
	log.Warn("returning unverified sample rate capabilities",
		logger.String("device_id", deviceID),
		logger.String("device_name", deviceName))
	return unverifiedFallback(deviceID, deviceName), nil
}

// extractSampleRates returns deduplicated, sorted sample rates that appear in
// CandidateSampleRates from the device's native format list.
func extractSampleRates(formats []malgo.DataFormat) []int {
	if len(formats) == 0 {
		return nil
	}

	seen := make(map[int]struct{}, len(formats))
	for i := range formats {
		rate := int(formats[i].SampleRate)
		if slices.Contains(CandidateSampleRates, rate) {
			seen[rate] = struct{}{}
		}
	}

	if len(seen) == 0 {
		return nil
	}

	rates := slices.Collect(maps.Keys(seen))
	slices.Sort(rates)
	return rates
}

// probeByInit attempts to initialize the device at each candidate sample rate.
// Rates that succeed are considered supported. This is the slow path used on
// ALSA when the device reports SampleRate=0 (meaning "hardware resampler handles any rate").
func probeByInit(malgoCtx *malgo.AllocatedContext, deviceInfo *malgo.DeviceInfo, log logger.Logger) []int {
	supported := make([]int, 0, len(CandidateSampleRates))

	// Allocate the C device ID pointer once (ID.Pointer() calls C.CBytes which
	// allocates on the C heap). Reusing a single allocation for all probe
	// iterations avoids leaking one C block per candidate rate.
	devIDPtr := deviceInfo.ID.Pointer()
	defer freeDeviceIDPtr(devIDPtr)

	for _, rate := range CandidateSampleRates {
		deviceCfg := malgo.DefaultDeviceConfig(malgo.Capture)
		deviceCfg.Capture.Channels = 1
		deviceCfg.SampleRate = uint32(rate) //nolint:gosec // G115: rate is bounded by CandidateSampleRates
		deviceCfg.Capture.DeviceID = devIDPtr
		deviceCfg.Alsa.NoMMap = 1

		// Use S32 format for rates above the standard BirdNET rate.
		if rate > conf.SampleRate {
			deviceCfg.Capture.Format = malgo.FormatS32
		}

		callbacks := malgo.DeviceCallbacks{}
		device, err := malgo.InitDevice(malgoCtx.Context, deviceCfg, callbacks)
		if err != nil {
			log.Debug("probe: rate not supported",
				logger.Int("sample_rate", rate),
				logger.Error(err))
			continue
		}
		device.Uninit()
		supported = append(supported, rate)
		log.Debug("probe: rate supported",
			logger.Int("sample_rate", rate))
	}

	return supported
}

// unverifiedFallback returns a DeviceCapabilities with all candidate rates
// marked as unverified. Used on non-Linux platforms where init-probing is
// unreliable or unnecessary (macOS/Windows handle resampling transparently).
func unverifiedFallback(deviceID, deviceName string) *DeviceCapabilities {
	// Return a copy so callers cannot mutate the package-level slice.
	rates := make([]int, len(CandidateSampleRates))
	copy(rates, CandidateSampleRates)
	return &DeviceCapabilities{
		DeviceID:    deviceID,
		DeviceName:  deviceName,
		SampleRates: rates,
		Verified:    false,
	}
}
