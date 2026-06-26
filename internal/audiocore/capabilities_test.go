// Package audiocore, capabilities_test.go.
// Unit tests for device sample rate capabilities probing helpers.
package audiocore

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger"
)

func TestExtractSampleRates_ExplicitFormats(t *testing.T) {
	t.Parallel()

	formats := []malgo.DataFormat{
		{Format: malgo.FormatS16, Channels: 2, SampleRate: 48000},
		{Format: malgo.FormatS16, Channels: 2, SampleRate: 96000},
		{Format: malgo.FormatS32, Channels: 2, SampleRate: 96000}, // duplicate rate
		{Format: malgo.FormatS16, Channels: 2, SampleRate: 192000},
		{Format: malgo.FormatS32, Channels: 2, SampleRate: 384000},
	}

	rates := extractSampleRates(formats)

	require.NotEmpty(t, rates)
	assert.Equal(t, []int{48000, 96000, 192000, 384000}, rates)
	// Verify sorted (ascending)
	assert.True(t, slices.IsSorted(rates), "rates must be sorted ascending")
}

func TestExtractSampleRates_AllZero(t *testing.T) {
	t.Parallel()

	formats := []malgo.DataFormat{
		{Format: malgo.FormatS16, Channels: 1, SampleRate: 0},
		{Format: malgo.FormatS32, Channels: 2, SampleRate: 0},
	}

	rates := extractSampleRates(formats)

	assert.Empty(t, rates)
}

func TestExtractSampleRates_Empty(t *testing.T) {
	t.Parallel()

	rates := extractSampleRates(nil)
	assert.Empty(t, rates)

	rates = extractSampleRates([]malgo.DataFormat{})
	assert.Empty(t, rates)
}

func TestExtractSampleRates_FiltersBelow48k(t *testing.T) {
	t.Parallel()

	formats := []malgo.DataFormat{
		{Format: malgo.FormatS16, Channels: 1, SampleRate: 8000},
		{Format: malgo.FormatS16, Channels: 1, SampleRate: 16000},
		{Format: malgo.FormatS16, Channels: 1, SampleRate: 44100},
		{Format: malgo.FormatS16, Channels: 1, SampleRate: 48000},
		{Format: malgo.FormatS16, Channels: 1, SampleRate: 96000},
	}

	rates := extractSampleRates(formats)

	assert.Equal(t, []int{48000, 96000}, rates)
}

func TestDeviceCapabilities_UnverifiedFallback(t *testing.T) {
	t.Parallel()

	caps := unverifiedFallback("hw:0,0", "Test USB Microphone")

	require.NotNil(t, caps)
	assert.Equal(t, "hw:0,0", caps.DeviceID)
	assert.Equal(t, "Test USB Microphone", caps.DeviceName)
	assert.False(t, caps.Verified)
	assert.Equal(t, CandidateSampleRates, caps.SampleRates)
}

func TestCandidateSampleRates_Sorted(t *testing.T) {
	t.Parallel()

	assert.True(t, slices.IsSorted(CandidateSampleRates),
		"CandidateSampleRates must be sorted ascending")
	// Also verify it starts at or above MinCaptureSampleRate
	if len(CandidateSampleRates) > 0 {
		assert.GreaterOrEqual(t, CandidateSampleRates[0], MinCaptureSampleRate)
		assert.LessOrEqual(t, CandidateSampleRates[len(CandidateSampleRates)-1], MaxCaptureSampleRate)
	}
}

// TestProbeDeviceCapabilities_SingleFlightCollapsesConcurrentProbes verifies that several
// concurrent cache-miss probes for the same device collapse into a single live probe (so the
// device is opened in exclusive mode only once and the rest do not hit a "device busy" error),
// and that each caller receives an independent clone of the result. It mutates package globals
// (the cache and the live-probe seam), so it must not run in parallel.
func TestProbeDeviceCapabilities_SingleFlightCollapsesConcurrentProbes(t *testing.T) {
	log := logger.Global().Module("audiocore_test")

	const deviceID = "test-device-uncached"
	const wantRate = 48000

	// Start from an empty cache so deviceID is a guaranteed miss; restore on cleanup.
	capabilitiesCacheMu.Lock()
	savedCache := capabilitiesCache
	capabilitiesCache = make(map[string]*DeviceCapabilities)
	capabilitiesCacheMu.Unlock()
	t.Cleanup(func() {
		capabilitiesCacheMu.Lock()
		capabilitiesCache = savedCache
		capabilitiesCacheMu.Unlock()
		probeGroup.Forget(deviceID)
	})

	// Stub the live probe with a call counter; it blocks briefly so concurrent callers pile
	// up behind the single in-flight probe (virtual time under synctest makes this exact).
	var liveCalls atomic.Int32
	savedFn := probeLiveFn
	probeLiveFn = func(id string, _ logger.Logger) (*DeviceCapabilities, error) {
		liveCalls.Add(1)
		time.Sleep(10 * time.Millisecond)
		return &DeviceCapabilities{
			DeviceID:    id,
			DeviceName:  "Stub Mic",
			SampleRates: []int{wantRate},
			Verified:    true,
		}, nil
	}
	t.Cleanup(func() { probeLiveFn = savedFn })

	synctest.Test(t, func(t *testing.T) {
		const concurrentCallers = 8
		results := make([]*DeviceCapabilities, concurrentCallers)
		errs := make([]error, concurrentCallers)

		var wg sync.WaitGroup
		for i := range concurrentCallers {
			wg.Go(func() {
				results[i], errs[i] = ProbeDeviceCapabilities(deviceID, log)
			})
		}
		wg.Wait()

		assert.Equal(t, int32(1), liveCalls.Load(),
			"concurrent probes for the same device must collapse to a single live probe")

		for i := range concurrentCallers {
			require.NoErrorf(t, errs[i], "caller %d", i)
			require.NotNilf(t, results[i], "caller %d", i)
			assert.Equalf(t, []int{wantRate}, results[i].SampleRates, "caller %d sample rates", i)
		}
		// Every caller must get its own clone, never a shared pointer into the cache.
		assert.NotSame(t, results[0], results[1], "callers must receive independent clones")
	})

	// The probe populated the cache, so a follow-up call is served from it without a new
	// live probe.
	got, err := ProbeDeviceCapabilities(deviceID, log)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int32(1), liveCalls.Load(),
		"a cached follow-up must not trigger another live probe")
}
