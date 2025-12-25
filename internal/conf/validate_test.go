package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestValidateSoundLevelSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings SoundLevelSettings
		wantErr  bool
		errType  string
	}{
		{
			name: "disabled sound level - should pass regardless of interval",
			settings: SoundLevelSettings{
				Enabled:  false,
				Interval: 1,
			},
			wantErr: false,
		},
		{
			name: "enabled with interval less than 5 seconds - should fail",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: 4,
			},
			wantErr: true,
			errType: "sound-level-interval",
		},
		{
			name: "enabled with interval exactly 5 seconds - should pass",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: 5,
			},
			wantErr: false,
		},
		{
			name: "enabled with interval greater than 5 seconds - should pass",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: 10,
			},
			wantErr: false,
		},
		{
			name: "enabled with zero interval - should fail",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: 0,
			},
			wantErr: true,
			errType: "sound-level-interval",
		},
		{
			name: "enabled with negative interval - should fail",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: -5,
			},
			wantErr: true,
			errType: "sound-level-interval",
		},
		{
			name: "enabled with very high interval - should pass",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: 3600,
			},
			wantErr: false,
		},
		{
			name: "disabled with zero interval - should pass",
			settings: SoundLevelSettings{
				Enabled:  false,
				Interval: 0,
			},
			wantErr: false,
		},
		{
			name: "disabled with negative interval - should pass",
			settings: SoundLevelSettings{
				Enabled:  false,
				Interval: -10,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSoundLevelSettings(&tt.settings)

			if tt.wantErr {
				enhanced := requireEnhancedError(t, err)

				// Verify validation type context
				ctx, exists := enhanced.Context["validation_type"]
				assert.True(t, exists, "expected validation_type context to be set")
				assert.Equal(t, tt.errType, ctx)

				// Verify error category
				assert.Equal(t, errors.CategoryValidation, enhanced.Category)

				// Verify interval context for interval errors
				if tt.errType == "sound-level-interval" {
					assert.Contains(t, enhanced.Context, "interval")
					assert.Contains(t, enhanced.Context, "minimum_interval")
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSoundLevelSettingsEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		interval int
		enabled  bool
		wantErr  bool
	}{
		{"boundary: 4 seconds enabled", 4, true, true},
		{"boundary: 5 seconds enabled", 5, true, false},
		{"boundary: 6 seconds enabled", 6, true, false},
		{"boundary: 4 seconds disabled", 4, false, false},
		{"boundary: 5 seconds disabled", 5, false, false},
		{"boundary: 6 seconds disabled", 6, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &SoundLevelSettings{
				Enabled:  tt.enabled,
				Interval: tt.interval,
			}

			err := validateSoundLevelSettings(settings)
			if tt.wantErr {
				assert.Error(t, err, "interval %d with enabled=%v should fail", tt.interval, tt.enabled)
			} else {
				assert.NoError(t, err, "interval %d with enabled=%v should pass", tt.interval, tt.enabled)
			}
		})
	}
}

func TestValidateSoundLevelSettingsErrorMessage(t *testing.T) {
	settings := &SoundLevelSettings{
		Enabled:  true,
		Interval: 3,
	}

	err := validateSoundLevelSettings(settings)
	require.Error(t, err, "expected error for interval < 5 seconds")

	expectedMsg := "sound level interval must be at least 5 seconds to avoid excessive CPU usage, got 3"
	assert.Equal(t, expectedMsg, err.Error())
}

func BenchmarkValidateSoundLevelSettings(b *testing.B) {
	settings := &SoundLevelSettings{
		Enabled:  true,
		Interval: 10,
	}

	for b.Loop() {
		_ = validateSoundLevelSettings(settings)
	}
}

func BenchmarkValidateSoundLevelSettingsWithError(b *testing.B) {
	settings := &SoundLevelSettings{
		Enabled:  true,
		Interval: 2,
	}

	for b.Loop() {
		_ = validateSoundLevelSettings(settings)
	}
}
