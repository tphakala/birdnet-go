package conf

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// ultrasonicFilter returns an enabled filter config with the given STFT fields.
func ultrasonicFilter(fftSize, hopSize, splitHz int) UltrasonicFilterConfig {
	return UltrasonicFilterConfig{
		Enabled:          true,
		CVThreshold:      0.15,
		FFTSize:          fftSize,
		HopSize:          hopSize,
		FrequencySplitHz: splitHz,
	}
}

func TestValidateSettings_UltrasonicFilterSTFT(t *testing.T) {
	tests := []struct {
		name        string
		cfg         UltrasonicFilterConfig
		wantErr     bool
		errContains string
	}{
		{name: "defaults", cfg: ultrasonicFilter(8192, 4096, 20000)},
		{name: "hop equal to fft size", cfg: ultrasonicFilter(8192, 8192, 20000)},
		{name: "hop above fft size", cfg: ultrasonicFilter(8192, 8193, 20000), wantErr: true, errContains: "bat.ultrasonicfilter.hopsize"},
		{name: "hop zero", cfg: ultrasonicFilter(8192, 0, 20000), wantErr: true, errContains: "bat.ultrasonicfilter.hopsize"},
		{name: "fft size not a power of two", cfg: ultrasonicFilter(6000, 3000, 20000), wantErr: true, errContains: "bat.ultrasonicfilter.fftsize"},
		{name: "fft size one", cfg: ultrasonicFilter(1, 1, 20000), wantErr: true, errContains: "bat.ultrasonicfilter.fftsize"},
		{name: "zero split", cfg: ultrasonicFilter(8192, 4096, 0), wantErr: true, errContains: "bat.ultrasonicfilter.frequencysplithz"},
		{name: "negative split", cfg: ultrasonicFilter(8192, 4096, -1), wantErr: true, errContains: "bat.ultrasonicfilter.frequencysplithz"},
		{
			name: "disabled filter is not validated",
			cfg: UltrasonicFilterConfig{
				Enabled: false, CVThreshold: 0.15, FFTSize: 8192, HopSize: 16384, FrequencySplitHz: 20000,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := createMinimalValidSettings()
			settings.Bat.UltrasonicFilter = tt.cfg

			err := ValidateSettings(settings)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestValidateSettings_UltrasonicFilterHopMessageNamesBothValues(t *testing.T) {
	err := ultrasonicFilter(8192, 16384, 20000).ValidateSTFT()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bat.ultrasonicfilter.hopsize must be between 1 and bat.ultrasonicfilter.fftsize (8192), got 16384")
}

func TestValidateSettings_EnablingFilterWithInvalidSTFTIsRejected(t *testing.T) {
	settings := createMinimalValidSettings()
	settings.Bat.UltrasonicFilter = ultrasonicFilter(8192, 16384, 20000)
	settings.Bat.UltrasonicFilter.Enabled = false
	require.NoError(t, ValidateSettings(settings), "a disabled filter keeps its stored values")

	settings.Bat.UltrasonicFilter.Enabled = true
	err := ValidateSettings(settings)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bat.ultrasonicfilter.hopsize")
}

func TestNormalizeUltrasonicFilter_RepairsOnLoad(t *testing.T) {
	ultrasonicWarnings := func(s *Settings) []string {
		var out []string
		for _, w := range s.ValidationWarnings {
			if strings.HasPrefix(w, warnComponentModels+": ") && strings.Contains(w, "ultrasonic") {
				out = append(out, w)
			}
		}
		return out
	}

	t.Run("enabled invalid pair is reset and then validates", func(t *testing.T) {
		s := createMinimalValidSettings()
		s.Bat.UltrasonicFilter = ultrasonicFilter(8192, 16384, 20000)

		normalizeIncompleteFeatures(s)

		assert.Equal(t, DefaultUltrasonicFFTSize, s.Bat.UltrasonicFilter.FFTSize)
		assert.Equal(t, DefaultUltrasonicHopSize, s.Bat.UltrasonicFilter.HopSize)
		assert.Equal(t, 20000, s.Bat.UltrasonicFilter.FrequencySplitHz)
		assert.True(t, s.Bat.UltrasonicFilter.Enabled, "repair never disables the filter")
		require.Len(t, ultrasonicWarnings(s), 1)
		assert.Contains(t, ultrasonicWarnings(s)[0], "16384")
		require.NoError(t, ValidateSettings(s))
	})

	t.Run("disabled invalid pair is repaired too", func(t *testing.T) {
		s := createMinimalValidSettings()
		s.Bat.UltrasonicFilter = ultrasonicFilter(6000, 3000, 20000)
		s.Bat.UltrasonicFilter.Enabled = false

		normalizeIncompleteFeatures(s)

		assert.Equal(t, DefaultUltrasonicFFTSize, s.Bat.UltrasonicFilter.FFTSize)
		assert.Equal(t, DefaultUltrasonicHopSize, s.Bat.UltrasonicFilter.HopSize)
		s.Bat.UltrasonicFilter.Enabled = true
		require.NoError(t, ValidateSettings(s), "enabling it through the API afterwards succeeds")
	})

	t.Run("unwritten struct takes the defaults silently", func(t *testing.T) {
		s := createMinimalValidSettings()
		s.Bat.UltrasonicFilter = UltrasonicFilterConfig{Enabled: true, CVThreshold: 0.15}

		normalizeIncompleteFeatures(s)

		assert.Equal(t, DefaultUltrasonicFFTSize, s.Bat.UltrasonicFilter.FFTSize)
		assert.Equal(t, DefaultUltrasonicHopSize, s.Bat.UltrasonicFilter.HopSize)
		assert.Equal(t, DefaultUltrasonicSplitHz, s.Bat.UltrasonicFilter.FrequencySplitHz)
		assert.Empty(t, ultrasonicWarnings(s), "an unwritten struct is not worth a warning")
	})

	t.Run("zero split next to a valid pair is repaired with a warning", func(t *testing.T) {
		s := createMinimalValidSettings()
		s.Bat.UltrasonicFilter = ultrasonicFilter(4096, 2048, 0)

		normalizeIncompleteFeatures(s)

		assert.Equal(t, 4096, s.Bat.UltrasonicFilter.FFTSize)
		assert.Equal(t, 2048, s.Bat.UltrasonicFilter.HopSize)
		assert.Equal(t, DefaultUltrasonicSplitHz, s.Bat.UltrasonicFilter.FrequencySplitHz)
		assert.Len(t, ultrasonicWarnings(s), 1)
		require.NoError(t, ValidateSettings(s))
	})

	t.Run("valid custom pair survives a bad split", func(t *testing.T) {
		s := createMinimalValidSettings()
		s.Bat.UltrasonicFilter = ultrasonicFilter(4096, 2048, -1)

		normalizeIncompleteFeatures(s)

		assert.Equal(t, 4096, s.Bat.UltrasonicFilter.FFTSize)
		assert.Equal(t, 2048, s.Bat.UltrasonicFilter.HopSize)
		assert.Equal(t, DefaultUltrasonicSplitHz, s.Bat.UltrasonicFilter.FrequencySplitHz)
		assert.Len(t, ultrasonicWarnings(s), 1)
	})

	t.Run("valid config records nothing", func(t *testing.T) {
		s := createMinimalValidSettings()
		s.Bat.UltrasonicFilter = ultrasonicFilter(4096, 2048, 18000)

		normalizeIncompleteFeatures(s)

		assert.Equal(t, ultrasonicFilter(4096, 2048, 18000), s.Bat.UltrasonicFilter)
		assert.Empty(t, ultrasonicWarnings(s))
	})

	t.Run("a second pass adds no duplicate warning", func(t *testing.T) {
		s := createMinimalValidSettings()
		s.Bat.UltrasonicFilter = ultrasonicFilter(8192, 16384, -5)

		normalizeIncompleteFeatures(s)
		first := len(ultrasonicWarnings(s))
		normalizeIncompleteFeatures(s)

		assert.Equal(t, 2, first, "one warning for the pair, one for the split")
		assert.Len(t, ultrasonicWarnings(s), first)
	})
}

func TestValidateSTFT_ErrorsCarryComponentAndOperation(t *testing.T) {
	tests := []struct {
		name string
		cfg  UltrasonicFilterConfig
	}{
		{name: "fft size", cfg: ultrasonicFilter(6000, 3000, 20000)},
		{name: "hop size", cfg: ultrasonicFilter(8192, 16384, 20000)},
		{name: "split", cfg: ultrasonicFilter(8192, 4096, -1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var enhanced *errors.EnhancedError
			require.ErrorAs(t, tt.cfg.ValidateSTFT(), &enhanced)
			assert.Equal(t, "config", enhanced.GetComponent())
			assert.Equal(t, "validate_ultrasonic_filter", enhanced.GetContext()["operation"])
		})
	}
}
