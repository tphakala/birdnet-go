package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTelemetrySettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		settings    TelemetrySettings
		wantErr     bool
		errContains string
	}{
		{
			name:     "disabled telemetry - any listen value passes",
			settings: TelemetrySettings{Enabled: false, Listen: "garbage"},
			wantErr:  false,
		},
		{
			name:     "valid listen address",
			settings: TelemetrySettings{Enabled: true, Listen: "0.0.0.0:8090"},
			wantErr:  false,
		},
		{
			name:     "valid localhost",
			settings: TelemetrySettings{Enabled: true, Listen: "127.0.0.1:9090"},
			wantErr:  false,
		},
		{
			name:     "valid empty host (all interfaces)",
			settings: TelemetrySettings{Enabled: true, Listen: ":8090"},
			wantErr:  false,
		},
		{
			name:     "valid IPv6 loopback",
			settings: TelemetrySettings{Enabled: true, Listen: "[::1]:8090"},
			wantErr:  false,
		},
		{
			name:     "valid IPv6 all interfaces",
			settings: TelemetrySettings{Enabled: true, Listen: "[::]:8090"},
			wantErr:  false,
		},
		{
			name:        "empty listen when enabled",
			settings:    TelemetrySettings{Enabled: true, Listen: ""},
			wantErr:     true,
			errContains: "cannot be empty",
		},
		{
			name:        "missing port",
			settings:    TelemetrySettings{Enabled: true, Listen: "0.0.0.0"},
			wantErr:     true,
			errContains: "invalid format",
		},
		{
			name:        "non-numeric port",
			settings:    TelemetrySettings{Enabled: true, Listen: "0.0.0.0:abc"},
			wantErr:     true,
			errContains: "not a valid number",
		},
		{
			name:        "port zero",
			settings:    TelemetrySettings{Enabled: true, Listen: "0.0.0.0:0"},
			wantErr:     true,
			errContains: "between 1 and 65535",
		},
		{
			name:        "port too high",
			settings:    TelemetrySettings{Enabled: true, Listen: "0.0.0.0:70000"},
			wantErr:     true,
			errContains: "between 1 and 65535",
		},
		{
			name:     "port at max boundary",
			settings: TelemetrySettings{Enabled: true, Listen: "0.0.0.0:65535"},
			wantErr:  false,
		},
		{
			name:     "port at min boundary",
			settings: TelemetrySettings{Enabled: true, Listen: "0.0.0.0:1"},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateTelemetrySettings(&tt.settings)
			if tt.wantErr {
				assert.False(t, result.Valid, "expected validation to fail")
				require.NotEmpty(t, result.Errors, "expected at least one error")
				assert.Contains(t, result.Errors[0], tt.errContains,
					"error message should contain %q", tt.errContains)
			} else {
				assert.True(t, result.Valid, "expected validation to pass, got errors: %v", result.Errors)
			}
		})
	}
}
