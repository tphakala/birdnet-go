package soundlevel

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const testSampleRate = 48000

// generateSineWave produces a sine wave at the given frequency, amplitude, and
// sample count, normalised to [-1, 1].
func generateSineWave(freq, amplitude float64, sampleRate, count int) []float64 {
	samples := make([]float64, count)
	for i := range count {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate))
	}
	return samples
}

// generateSilence returns a zero-valued sample slice.
func generateSilence(count int) []float64 {
	return make([]float64, count)
}

// TestNewProcessor_ValidArgs verifies that a processor is constructed correctly
// for well-formed arguments.
func TestNewProcessor_ValidArgs(t *testing.T) {
	t.Parallel()

	p, err := NewProcessor("src1", "Test Source", testSampleRate, 5)
	require.NoError(t, err)
	require.NotNil(t, p)

	assert.Equal(t, "src1", p.source)
	assert.Equal(t, "Test Source", p.name)
	assert.Equal(t, testSampleRate, p.sampleRate)
	assert.Equal(t, 5, p.interval)

	// Filters should cover all bands below Nyquist.
	nyquist := float64(testSampleRate) / 2.0
	expectedFilters := 0
	for _, f := range octaveBandCenterFreqs {
		if f < nyquist {
			expectedFilters++
		}
	}
	assert.Len(t, p.filters, expectedFilters)

	// One second buffer per filter.
	assert.Len(t, p.secondBuffers, len(p.filters))
	for _, buf := range p.secondBuffers {
		assert.Equal(t, testSampleRate, buf.targetSampleCount)
		assert.Empty(t, buf.samples)
	}

	// Interval aggregator properly initialised.
	assert.Len(t, p.intervalBuffer.secondMeasurements, 5)
	for _, m := range p.intervalBuffer.secondMeasurements {
		assert.NotNil(t, m)
	}
	assert.Equal(t, 0, p.intervalBuffer.currentIndex)
	assert.Equal(t, 0, p.intervalBuffer.measurementCount)
	assert.False(t, p.intervalBuffer.full)
}

// TestNewProcessor_InvalidSampleRate verifies that a non-positive sample rate is rejected.
func TestNewProcessor_InvalidSampleRate(t *testing.T) {
	t.Parallel()

	p, err := NewProcessor("src", "name", 0, 5)
	require.Error(t, err)
	assert.Nil(t, p)

	p, err = NewProcessor("src", "name", -1, 5)
	require.Error(t, err)
	assert.Nil(t, p)
}

// TestNewProcessor_IntervalClamping verifies that intervals below 1 are clamped.
func TestNewProcessor_IntervalClamping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		interval         int
		expectedInterval int
	}{
		{"zero", 0, 1},
		{"negative", -5, 1},
		{"one", 1, 1},
		{"five", 5, 5},
		{"sixty", 60, 60},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := NewProcessor("src", "name", testSampleRate, tt.interval)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedInterval, p.interval)
		})
	}
}

// TestSoundLevelProcessor_ProcessSamples feeds a known tone through the processor
// and verifies that the 1 kHz octave band output is higher than adjacent bands.
func TestSoundLevelProcessor_ProcessSamples(t *testing.T) {
	t.Parallel()

	p, err := NewProcessor("src", "test", testSampleRate, 1)
	require.NoError(t, err)

	// Generate exactly 1 second of a 1 kHz sine wave at -6 dB amplitude (~0.5).
	samples := generateSineWave(1000, 0.5, testSampleRate, testSampleRate)

	data, err := p.ProcessSamples(samples)
	require.NoError(t, err, "1-second interval should complete after one call")
	require.NotNil(t, data)

	assert.Equal(t, "src", data.Source)
	assert.Equal(t, "test", data.Name)
	assert.Equal(t, 1, data.Duration)
	assert.NotEmpty(t, data.OctaveBands)

	// The 1 kHz band key.
	const kHz1Key = "1.0_kHz"
	band1k, ok := data.OctaveBands[kHz1Key]
	require.True(t, ok, "1 kHz band must be present (key=%s)", kHz1Key)

	// 1 kHz band should have a meaningful dB level (above noise floor -100 dB).
	assert.Greater(t, band1k.Mean, -60.0, "1 kHz band should reflect the injected tone")

	// Bands far from 1 kHz (e.g. 25 Hz, 16 kHz) should be much quieter.
	const lowKey = "25.0_Hz"
	if lowBand, exists := data.OctaveBands[lowKey]; exists {
		assert.Less(t, lowBand.Mean, band1k.Mean,
			"25 Hz band should be quieter than the 1 kHz band when driving a 1 kHz tone")
	}

	// SampleCount should equal the number of 1-second measurements in the interval.
	assert.Equal(t, 1, band1k.SampleCount)
}

// TestSoundLevelProcessor_IntervalAggregation verifies that the processor
// accumulates multiple 1-second measurements and returns SoundLevelData only
// when the full interval is complete.
func TestSoundLevelProcessor_IntervalAggregation(t *testing.T) {
	t.Parallel()

	const intervalSecs = 5
	p, err := NewProcessor("src", "test", testSampleRate, intervalSecs)
	require.NoError(t, err)

	oneSecond := generateSilence(testSampleRate)

	// Send interval-1 seconds worth of data; none should produce a result yet.
	for i := range intervalSecs - 1 {
		data, processErr := p.ProcessSamples(oneSecond)
		assert.True(t, errors.Is(processErr, ErrIntervalIncomplete),
			"second %d should return ErrIntervalIncomplete", i+1)
		assert.Nil(t, data, "no result expected before interval completes (second %d)", i+1)
	}

	// The final second should complete the interval.
	data, err := p.ProcessSamples(oneSecond)
	require.NoError(t, err, "final second of interval should not return an error")
	require.NotNil(t, data, "final second should produce SoundLevelData")

	assert.Equal(t, "src", data.Source)
	assert.Equal(t, "test", data.Name)
	assert.Equal(t, intervalSecs, data.Duration)
	assert.NotEmpty(t, data.OctaveBands)

	// All bands in the result should have SampleCount == intervalSecs.
	for key, band := range data.OctaveBands {
		assert.Equal(t, intervalSecs, band.SampleCount,
			"band %s should have %d measurements", key, intervalSecs)
	}

	// After completion the processor resets; the next call should return ErrIntervalIncomplete.
	data2, err2 := p.ProcessSamples(oneSecond)
	assert.True(t, errors.Is(err2, ErrIntervalIncomplete),
		"processor should reset after producing data")
	assert.Nil(t, data2)
}

// TestSoundLevelProcessor_EmptyInput verifies ErrNoAudioData on empty slices.
func TestSoundLevelProcessor_EmptyInput(t *testing.T) {
	t.Parallel()

	p, err := NewProcessor("src", "test", testSampleRate, 5)
	require.NoError(t, err)

	data, err := p.ProcessSamples([]float64{})
	assert.True(t, errors.Is(err, ErrNoAudioData))
	assert.Nil(t, data)

	data, err = p.ProcessSamples(nil)
	assert.True(t, errors.Is(err, ErrNoAudioData))
	assert.Nil(t, data)
}

// TestSoundLevelProcessor_Timestamp verifies that SoundLevelData.Timestamp is
// set and is close to the current time.
func TestSoundLevelProcessor_Timestamp(t *testing.T) {
	t.Parallel()

	p, err := NewProcessor("src", "test", testSampleRate, 1)
	require.NoError(t, err)

	before := time.Now()
	data, err := p.ProcessSamples(generateSilence(testSampleRate))
	after := time.Now()

	require.NoError(t, err)
	require.NotNil(t, data)

	assert.False(t, data.Timestamp.Before(before), "timestamp must not be before test start")
	assert.False(t, data.Timestamp.After(after), "timestamp must not be after test end")
}

// TestSoundLevelProcessor_Reset verifies that Reset clears accumulated state.
func TestSoundLevelProcessor_Reset(t *testing.T) {
	t.Parallel()

	const intervalSecs = 3
	p, err := NewProcessor("src", "test", testSampleRate, intervalSecs)
	require.NoError(t, err)

	oneSecond := generateSilence(testSampleRate)

	// Partially fill the interval.
	_, _ = p.ProcessSamples(oneSecond)
	assert.Equal(t, 1, p.intervalBuffer.measurementCount)

	// Reset should clear state.
	p.Reset()
	assert.Equal(t, 0, p.intervalBuffer.measurementCount)
	assert.Equal(t, 0, p.intervalBuffer.currentIndex)
	assert.False(t, p.intervalBuffer.full)

	// After reset a full interval should still produce data.
	var postResetData *SoundLevelData
	var postResetErr error
	for range intervalSecs {
		postResetData, postResetErr = p.ProcessSamples(oneSecond)
	}
	// The last iteration should have completed the interval.
	if postResetData == nil {
		// If not triggered yet, one more second will push it over.
		postResetData, postResetErr = p.ProcessSamples(oneSecond)
	}
	require.NoError(t, postResetErr)
	assert.NotNil(t, postResetData, "processor should produce data after reset and a full interval")
}

// TestSoundLevelProcessor_OctaveBandKeys verifies that expected band keys are
// present in the output.
func TestSoundLevelProcessor_OctaveBandKeys(t *testing.T) {
	t.Parallel()

	p, err := NewProcessor("src", "test", testSampleRate, 1)
	require.NoError(t, err)

	data, err := p.ProcessSamples(generateSilence(testSampleRate))
	require.NoError(t, err)
	require.NotNil(t, data)

	// Spot-check a selection of band keys.
	expectedKeys := []string{
		"25.0_Hz", "100.0_Hz", "500.0_Hz", "1.0_kHz", "4.0_kHz", "8.0_kHz",
	}
	for _, key := range expectedKeys {
		_, ok := data.OctaveBands[key]
		assert.True(t, ok, "expected band key %q to be present", key)
	}
}

// TestFormatBandKey verifies the key formatting helper.
func TestFormatBandKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		freq     float64
		expected string
	}{
		{25, "25.0_Hz"},
		{31.5, "31.5_Hz"},
		{999.9, "999.9_Hz"},
		{1000, "1.0_kHz"},
		{1000.0, "1.0_kHz"},
		{12500, "12.5_kHz"},
		{20000, "20.0_kHz"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatBandKey(tt.freq))
		})
	}
}
