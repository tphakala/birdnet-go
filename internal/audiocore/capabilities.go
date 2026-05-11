// Package audiocore provides the core audio infrastructure for BirdNET-Go.
// capabilities.go - device sample rate capability probing for high-frequency capture.
package audiocore

import (
	"fmt"
	"maps"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/gen2brain/malgo"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// capabilitiesCache stores probed device capabilities keyed by device name.
// Populated at startup before capture begins, read by the API at runtime.
var (
	capabilitiesCache   = make(map[string]*DeviceCapabilities)
	capabilitiesCacheMu sync.RWMutex
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

func cloneCapabilities(c *DeviceCapabilities) *DeviceCapabilities {
	if c == nil {
		return nil
	}
	cp := *c
	cp.SampleRates = slices.Clone(c.SampleRates)
	return &cp
}

// ProbeAllDeviceCapabilities probes all available capture devices and populates
// the capabilities cache. Call this at startup before capture begins so that
// exclusive mode probing can access devices without contention.
func ProbeAllDeviceCapabilities(log logger.Logger) {
	devices, err := ListCaptureDevices()
	if err != nil {
		log.Warn("failed to list devices for capability probing", logger.Error(err))
		return
	}

	for _, dev := range devices {
		caps, err := probeDeviceCapabilitiesLive(dev.ID, log)
		if err != nil {
			log.Warn("failed to probe device capabilities",
				logger.String("device", dev.Name),
				logger.String("device_id", dev.ID),
				logger.Error(err))
			continue
		}
		capabilitiesCacheMu.Lock()
		capabilitiesCache[dev.ID] = caps
		capabilitiesCacheMu.Unlock()
		log.Info("cached device capabilities",
			logger.String("device", dev.Name),
			logger.Any("sample_rates", caps.SampleRates),
			logger.Bool("verified", caps.Verified))
	}
}

// GetCachedCapabilities returns cached capabilities for a device, or nil if
// the device was not probed at startup.
func GetCachedCapabilities(deviceName string) *DeviceCapabilities {
	capabilitiesCacheMu.RLock()
	defer capabilitiesCacheMu.RUnlock()
	for _, caps := range capabilitiesCache {
		if caps.DeviceName == deviceName {
			return cloneCapabilities(caps)
		}
	}
	return nil
}

// ProbeDeviceCapabilities returns cached capabilities if available, otherwise
// probes the device live. The cache is populated at startup; live probing is
// the fallback for devices plugged in after startup.
func ProbeDeviceCapabilities(deviceID string, log logger.Logger) (*DeviceCapabilities, error) {
	// Check cache first (covers devices probed at startup).
	// Direct lookup by ID, then iterate for name/substring match
	// (same matching logic as matchesDevice in capture.go).
	capabilitiesCacheMu.RLock()
	if caps, ok := capabilitiesCache[deviceID]; ok {
		capabilitiesCacheMu.RUnlock()
		return cloneCapabilities(caps), nil
	}
	for _, caps := range capabilitiesCache {
		if caps.DeviceName == deviceID ||
			strings.Contains(caps.DeviceName, deviceID) {
			capabilitiesCacheMu.RUnlock()
			return cloneCapabilities(caps), nil
		}
	}
	capabilitiesCacheMu.RUnlock()

	// Cache miss: probe live (device may have been plugged in after startup).
	caps, err := probeDeviceCapabilitiesLive(deviceID, log)
	if err == nil && caps != nil {
		capabilitiesCacheMu.Lock()
		capabilitiesCache[caps.DeviceID] = caps
		capabilitiesCacheMu.Unlock()
	}
	return caps, err
}

// probeDeviceCapabilitiesLive queries supported sample rates for a device.
// On Linux, always uses init-probing in exclusive mode to get accurate hardware rates.
// On other platforms, reads from the device's native Formats array.
func probeDeviceCapabilitiesLive(deviceID string, log logger.Logger) (*DeviceCapabilities, error) {
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
	// On Linux, skip the fast path entirely because ALSA's device info
	// is queried through dsnoop (shared mode), which constrains the
	// reported formats to 48kHz regardless of what the hardware supports.
	rates := extractSampleRates(selectedInfo.Formats)

	if len(rates) > 0 && runtime.GOOS != captureOSLinux {
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

	// On Linux, always probe by init because the fast path is unreliable.
	// On other platforms, this is the fallback when formats are all zero.
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
			if runtime.GOOS == captureOSLinux {
				deviceCfg.Capture.ShareMode = malgo.Exclusive
				if nativeCh := nativeCaptureChannels(deviceInfo); nativeCh > 0 {
					deviceCfg.Capture.Channels = nativeCh
				}
			}
		}

		callbacks := malgo.DeviceCallbacks{}
		device, err := malgo.InitDevice(malgoCtx.Context, deviceCfg, callbacks)
		// If exclusive mode init fails, retry with stereo. Many USB audio
		// devices only support 2-channel capture on the hw: interface;
		// miniaudio handles the downmix to mono internally.
		if err != nil && deviceCfg.Capture.ShareMode == malgo.Exclusive && deviceCfg.Capture.Channels != 2 {
			log.Debug("probe: exclusive mono failed, retrying with stereo",
				logger.Int("sample_rate", rate))
			deviceCfg.Capture.Channels = 2
			device, err = malgo.InitDevice(malgoCtx.Context, deviceCfg, callbacks)
		}
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
