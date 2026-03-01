package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtendedCaptureSettings_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		settings    ExtendedCaptureSettings
		preCapture  int
		wantError   bool
		errContains string
	}{
		{
			name:       "disabled is always valid",
			settings:   ExtendedCaptureSettings{Enabled: false},
			preCapture: 6,
			wantError:  false,
		},
		{
			name:       "valid default config",
			settings:   ExtendedCaptureSettings{Enabled: true, MaxDuration: 120, CaptureBufferSeconds: 0},
			preCapture: 6,
			wantError:  false,
		},
		{
			name:        "maxDuration exceeds cap",
			settings:    ExtendedCaptureSettings{Enabled: true, MaxDuration: 1500},
			preCapture:  6,
			wantError:   true,
			errContains: "1200",
		},
		{
			name:       "maxDuration zero uses default",
			settings:   ExtendedCaptureSettings{Enabled: true, MaxDuration: 0},
			preCapture: 6,
			wantError:  false,
		},
		{
			name:        "buffer too small for maxDuration + preCapture",
			settings:    ExtendedCaptureSettings{Enabled: true, MaxDuration: 120, CaptureBufferSeconds: 130},
			preCapture:  10,
			wantError:   true,
			errContains: "capture buffer",
		},
		{
			name:       "buffer auto-calculated when zero",
			settings:   ExtendedCaptureSettings{Enabled: true, MaxDuration: 120, CaptureBufferSeconds: 0},
			preCapture: 6,
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.settings.Validate(tt.preCapture)
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExtendedCaptureSettings_AutoCalculateBuffer(t *testing.T) {
	t.Parallel()

	s := ExtendedCaptureSettings{Enabled: true, MaxDuration: 120, CaptureBufferSeconds: 0}
	err := s.Validate(6)
	require.NoError(t, err)
	assert.Equal(t, 186, s.CaptureBufferSeconds)
}

func TestExtendedCaptureSettings_DefaultMaxDuration(t *testing.T) {
	t.Parallel()

	s := ExtendedCaptureSettings{Enabled: true, MaxDuration: 0}
	err := s.Validate(6)
	require.NoError(t, err)
	assert.Equal(t, DefaultExtendedCaptureMaxDuration, s.MaxDuration)
}
