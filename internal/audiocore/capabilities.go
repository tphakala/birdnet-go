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
	"golang.org/x/sync/singleflight"
)

// capabilitiesCache stores probed device capabilities keyed by device name.
// Populated at startup before capture begins, read by the API at runtime.
var (
	capabilitiesCache   = make(map[string]*DeviceCapabilities)
	capabilitiesCacheMu sync.RWMutex

	// probeGroup collapses concurrent live probes for the same device so only one opens
	// the ALSA device in malgo.Exclusive mode; the rest wait and read the freshly-cached
	// result instead of racing into a "device busy" ALSA error.
	probeGroup singleflight.Group

	// probeLiveFn is the live-probe entry point, indirected through a package var so tests
	// can substitute a stub. The real implementation calls malgo/CGO and cannot run in a
	// unit test.
	probeLiveFn = probeDeviceCapabilitiesLive
)

// CandidateSampleRates are the rates tested during device probing.
// Derived from conf.ValidSampleRates to keep validation and probing in sync.
var CandidateSampleRates = conf.ValidSampleRates()

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
		// Also key by the stable USB token so a config persisted as "usb-path:..."
		// hits this startup cache instead of falling through to a live exclusive-mode
		// probe that contends with capture already holding the device (GH #3651).
		if dev.StableID != "" {
			capabilitiesCache[dev.StableID] = caps
		}
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

// ProbeDeviceCapabilities returns cached capabilities if available, otherwise probes the
// device live. The cache is populated at startup; live probing is the fallback for devices
// plugged in after startup. Concurrent live probes for the same device are collapsed so the
// device is opened in exclusive mode by at most one goroutine at a time.
func ProbeDeviceCapabilities(deviceID string, log logger.Logger) (*DeviceCapabilities, error) {
	// An empty deviceID would match any cached device via the name-substring scan in
	// lookupCachedCapabilities (strings.Contains(name, "") is always true), so reject it as
	// not-found rather than returning an arbitrary device.
	if deviceID == "" {
		return nil, fmt.Errorf("probe device: %w", ErrDeviceNotFound)
	}

	// Fast path: the cache covers devices probed at startup.
	if caps, ok := lookupCachedCapabilities(deviceID); ok {
		return cloneCapabilities(caps), nil
	}

	// Cache miss (device likely plugged in after startup): probe live. Collapse concurrent
	// probes for the same device through singleflight so only one opens the ALSA device in
	// malgo.Exclusive mode; the rest wait and read the freshly-cached result instead of both
	// racing into a "device busy" failure.
	result, err, _ := probeGroup.Do(deviceID, func() (any, error) {
		// Re-check the cache: a probe that completed while we waited for the group slot
		// already populated it, so we skip a redundant second live probe.
		if caps, ok := lookupCachedCapabilities(deviceID); ok {
			return caps, nil
		}
		caps, probeErr := probeLiveFn(deviceID, log)
		if probeErr == nil && caps != nil {
			capabilitiesCacheMu.Lock()
			capabilitiesCache[caps.DeviceID] = caps
			capabilitiesCacheMu.Unlock()
		}
		return caps, probeErr
	})
	if err != nil {
		return nil, err
	}
	// The shared result is one *DeviceCapabilities handed to every waiter; clone it so no
	// caller can mutate another's copy (matching the cache-hit return above).
	caps, _ := result.(*DeviceCapabilities)
	return cloneCapabilities(caps), nil
}

// lookupCachedCapabilities returns the cached capabilities for deviceID and true on a hit, or
// (nil, false) on a miss. The cache is keyed by both the decoded ALSA id and the stable USB
// token, so a direct lookup hits for a "usb-path:"/"usb-id:" config; the name-substring scan
// is a legacy fallback for name-based configs. The returned pointer is the cached value (the
// caller clones before handing it out); cached entries are never mutated in place.
func lookupCachedCapabilities(deviceID string) (*DeviceCapabilities, bool) {
	capabilitiesCacheMu.RLock()
	defer capabilitiesCacheMu.RUnlock()
	if caps, ok := capabilitiesCache[deviceID]; ok {
		return caps, true
	}
	for _, caps := range capabilitiesCache {
		if caps != nil && (caps.DeviceName == deviceID || strings.Contains(caps.DeviceName, deviceID)) {
			return caps, true
		}
	}
	return nil, false
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
			Context("device_id", redactDeviceID(deviceID)).
			Build()
	}
	defer uninitAndFreeContext(malgoCtx, log)

	infos, err := malgoCtx.Devices(malgo.Capture)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore.capabilities").
			Category(errors.CategoryAudioSource).
			Context("operation", "probe_enumerate_devices").
			Context("device_id", redactDeviceID(deviceID)).
			Build()
	}

	// Find the matching device.
	var selectedInfo *malgo.DeviceInfo
	var deviceName string

	// Only a stable USB token needs the resolved USB identity to match; legacy
	// id/name configs match without it. Parse the cards map once when needed.
	needIdent := isUSBDeviceToken(deviceID)
	var cards map[int]procCardEntry
	if needIdent {
		cards = readProcAsoundCards(defaultProcRoot)
	}
	for i := range infos {
		decodedID, decErr := hexToASCII(infos[i].ID.String())
		if decErr != nil {
			log.Warn("failed to decode device ID during probe",
				logger.Int("device_index", i),
				logger.Error(decErr))
			continue
		}
		var ident usbIdentity
		if needIdent {
			ident = usbIdentityForCard(parseALSACardNumber(decodedID), cards, defaultProcRoot)
		}
		if matchesDevice(decodedID, ident, infos[i].Name(), infos[i].IsDefault == 1, deviceID) {
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
			logger.String("device_id", redactDeviceID(deviceID)),
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
			logger.String("device_id", redactDeviceID(deviceID)),
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
