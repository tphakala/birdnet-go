package conf

import (
	stderrors "errors"
	"testing"

	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestValidateSoundLevelSettings(t *testing.T) {
	// Define test cases using table-driven tests
	tests := []struct {
		name     string
		settings SoundLevelSettings
		wantErr  bool
		errType  string // expected error type for validation
	}{
		{
			name: "disabled sound level - should pass regardless of interval",
			settings: SoundLevelSettings{
				Enabled:  false,
				Interval: 1, // Less than 5 seconds but should pass because disabled
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
				Interval: 3600, // 1 hour
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

	// Run test cases
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the validation function
			err := validateSoundLevelSettings(&tt.settings)

			// Check if error occurred as expected
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSoundLevelSettings() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// If we expected an error, verify it's the correct type
			if tt.wantErr && err != nil {
				// Check if it's an enhanced error with proper context
				var enhancedErr *errors.EnhancedError
				if !stderrors.As(err, &enhancedErr) {
					t.Errorf("expected EnhancedError type, got %T", err)
					return
				}

				// Verify the validation type context
				if ctx, exists := enhancedErr.Context["validation_type"]; exists {
					if ctx != tt.errType {
						t.Errorf("expected validation_type = %s, got %s", tt.errType, ctx)
					}
				} else {
					t.Errorf("expected validation_type context to be set")
				}

				// Verify the error category
				if enhancedErr.Category != errors.CategoryValidation {
					t.Errorf("expected error category = %s, got %s", errors.CategoryValidation, enhancedErr.Category)
				}

				// Verify interval context is set for interval errors
				if tt.errType == "sound-level-interval" {
					if _, exists := enhancedErr.Context["interval"]; !exists {
						t.Errorf("expected interval context to be set")
					}
					if _, exists := enhancedErr.Context["minimum_interval"]; !exists {
						t.Errorf("expected minimum_interval context to be set")
					}
				}
			}
		})
	}
}

func TestValidateSoundLevelSettingsEdgeCases(t *testing.T) {
	// Test boundary values specifically
	boundaryTests := []struct {
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

	for _, tt := range boundaryTests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &SoundLevelSettings{
				Enabled:  tt.enabled,
				Interval: tt.interval,
			}
			
			err := validateSoundLevelSettings(settings)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSoundLevelSettings() for interval %d, enabled %v: error = %v, wantErr %v", 
					tt.interval, tt.enabled, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSoundLevelSettingsErrorMessage(t *testing.T) {
	// Test specific error message content
	settings := &SoundLevelSettings{
		Enabled:  true,
		Interval: 3,
	}
	
	err := validateSoundLevelSettings(settings)
	if err == nil {
		t.Fatal("expected error for interval < 5 seconds, got nil")
	}
	
	// Check error message contains expected content
	expectedMsg := "sound level interval must be at least 5 seconds to avoid excessive CPU usage, got 3"
	if err.Error() != expectedMsg {
		t.Errorf("expected error message = %q, got %q", expectedMsg, err.Error())
	}
}

func BenchmarkValidateSoundLevelSettings(b *testing.B) {
	// Create test settings
	settings := &SoundLevelSettings{
		Enabled:  true,
		Interval: 10,
	}
	
	b.ResetTimer()
	
	// Run validation N times
	for i := 0; i < b.N; i++ {
		_ = validateSoundLevelSettings(settings)
	}
}

func BenchmarkValidateSoundLevelSettingsWithError(b *testing.B) {
	// Create test settings that will generate an error
	settings := &SoundLevelSettings{
		Enabled:  true,
		Interval: 2,
	}
	
	b.ResetTimer()
	
	// Run validation N times
	for i := 0; i < b.N; i++ {
		_ = validateSoundLevelSettings(settings)
	}
}