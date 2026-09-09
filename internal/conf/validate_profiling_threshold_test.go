package conf

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateProfilingSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		profiling   ProfilingConfig
		wantErr     bool
		errContains string
	}{
		{
			name:      "zero rates disable sampling and pass",
			profiling: ProfilingConfig{BlockRate: 0, MutexFraction: 0},
		},
		{
			name:      "positive rates pass",
			profiling: ProfilingConfig{BlockRate: 10000, MutexFraction: 100},
		},
		{
			name:      "large positive rate passes (clamped at point of use)",
			profiling: ProfilingConfig{BlockRate: 1_000_000_000_000_000, MutexFraction: 1},
		},
		{
			name:        "negative block rate rejected",
			profiling:   ProfilingConfig{BlockRate: -1, MutexFraction: 100},
			wantErr:     true,
			errContains: "blockRate must be >= 0",
		},
		{
			name:        "negative mutex fraction rejected",
			profiling:   ProfilingConfig{BlockRate: 0, MutexFraction: -5},
			wantErr:     true,
			errContains: "mutexFraction must be >= 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := tt.profiling
			err := validateProfilingSettings(&p)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateProfilingSettings_Nil(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateProfilingSettings(nil))
}

func TestValidateModelThresholds(t *testing.T) {
	t.Parallel()

	// base returns a Settings with all three model thresholds at a valid default.
	base := func() *Settings {
		s := &Settings{}
		s.Bat.Threshold = 0.5
		s.Perch.Threshold = 0.5
		s.BirdNETV3.Threshold = 0.5
		return s
	}

	tests := []struct {
		name        string
		mutate      func(s *Settings)
		wantErr     bool
		errContains string
	}{
		{
			name:   "all defaults pass",
			mutate: func(*Settings) {},
		},
		{
			name:   "boundary values 0 and 1 pass",
			mutate: func(s *Settings) { s.Bat.Threshold = 0; s.Perch.Threshold = 1 },
		},
		{
			name:        "bat threshold above 1 rejected",
			mutate:      func(s *Settings) { s.Bat.Threshold = 5.0 },
			wantErr:     true,
			errContains: "bat.threshold must be between",
		},
		{
			name:        "perch threshold below 0 rejected",
			mutate:      func(s *Settings) { s.Perch.Threshold = -1 },
			wantErr:     true,
			errContains: "perch.threshold must be between",
		},
		{
			name:        "birdnetv3 threshold above 1 rejected",
			mutate:      func(s *Settings) { s.BirdNETV3.Threshold = 1.5 },
			wantErr:     true,
			errContains: "birdnetv3.threshold must be between",
		},
		{
			name:        "NaN threshold rejected (comparison would silently pass)",
			mutate:      func(s *Settings) { s.Bat.Threshold = math.NaN() },
			wantErr:     true,
			errContains: "bat.threshold must be between",
		},
		{
			name:        "positive infinity threshold rejected",
			mutate:      func(s *Settings) { s.Perch.Threshold = math.Inf(1) },
			wantErr:     true,
			errContains: "perch.threshold must be between",
		},
		{
			name: "out-of-range perch rejected even when override is off",
			mutate: func(s *Settings) {
				s.Perch.OverrideThreshold = false
				s.Perch.Threshold = 2.0
			},
			wantErr:     true,
			errContains: "perch.threshold must be between",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := base()
			tt.mutate(s)
			err := validateModelThresholds(s)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestNormalizeProfiling verifies the load-path leniency: a negative rate is
// silently normalized to 0 (disabled) with a recorded warning, so an aged config
// boots instead of being rejected, while the API path still rejects it.
func TestNormalizeProfiling(t *testing.T) {
	t.Parallel()

	t.Run("negative rates normalized to zero with warnings", func(t *testing.T) {
		t.Parallel()
		s := &Settings{}
		s.Diagnostics.Profiling.BlockRate = -1
		s.Diagnostics.Profiling.MutexFraction = -5

		s.normalizeProfiling()

		assert.Equal(t, 0, s.Diagnostics.Profiling.BlockRate)
		assert.Equal(t, 0, s.Diagnostics.Profiling.MutexFraction)
		assert.Len(t, s.ValidationWarnings, 2)

		// After normalization the strict validator no longer fires.
		require.NoError(t, validateProfilingSettings(&s.Diagnostics.Profiling))
	})

	t.Run("valid rates untouched and no warnings", func(t *testing.T) {
		t.Parallel()
		s := &Settings{}
		s.Diagnostics.Profiling.BlockRate = 10000
		s.Diagnostics.Profiling.MutexFraction = 100

		s.normalizeProfiling()

		assert.Equal(t, 10000, s.Diagnostics.Profiling.BlockRate)
		assert.Equal(t, 100, s.Diagnostics.Profiling.MutexFraction)
		assert.Empty(t, s.ValidationWarnings)
	})
}

// TestNormalizeModelThresholds verifies the load-path leniency for out-of-range
// model thresholds: they are reset to the default with a warning so an aged config
// boots, while an in-range value is left untouched. After normalization the strict
// validator must pass (no boot-brick).
func TestNormalizeModelThresholds(t *testing.T) {
	t.Parallel()

	t.Run("out-of-range and NaN thresholds reset to default with warnings", func(t *testing.T) {
		t.Parallel()
		s := &Settings{}
		s.Bat.Threshold = 1.5  // above range
		s.Perch.Threshold = -1 // below range
		s.BirdNETV3.Threshold = math.NaN()

		s.normalizeModelThresholds()

		assert.InEpsilon(t, defaultModelThreshold, s.Bat.Threshold, 1e-9)
		assert.InEpsilon(t, defaultModelThreshold, s.Perch.Threshold, 1e-9)
		assert.InEpsilon(t, defaultModelThreshold, s.BirdNETV3.Threshold, 1e-9)
		assert.Len(t, s.ValidationWarnings, 3)

		// The strict validator must now pass, i.e. an aged config would boot.
		require.NoError(t, validateModelThresholds(s))
	})

	t.Run("in-range thresholds untouched and no warnings", func(t *testing.T) {
		t.Parallel()
		s := &Settings{}
		s.Bat.Threshold = 0 // boundary, valid
		s.Perch.Threshold = 0.5
		s.BirdNETV3.Threshold = 1 // boundary, valid

		s.normalizeModelThresholds()

		assert.Zero(t, s.Bat.Threshold)
		assert.InEpsilon(t, 0.5, s.Perch.Threshold, 1e-9)
		assert.InEpsilon(t, 1.0, s.BirdNETV3.Threshold, 1e-9)
		assert.Empty(t, s.ValidationWarnings)
	})
}
