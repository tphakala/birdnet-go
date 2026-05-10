// Package audiocore, capabilities_test.go.
// Unit tests for device sample rate capabilities probing helpers.
package audiocore

import (
	"slices"
	"testing"

	"github.com/gen2brain/malgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
