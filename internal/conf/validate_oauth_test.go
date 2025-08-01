package conf

import (
	"errors"
	"testing"
)

func TestValidateSecuritySettings_OAuthHostMissing(t *testing.T) {
	tests := []struct {
		name           string
		settings       Security
		wantError      bool
		wantWarning    bool
		expectedErrMsg string
	}{
		{
			name: "OAuth disabled - no host required",
			settings: Security{
				GoogleAuth: SocialProvider{Enabled: false},
				GithubAuth: SocialProvider{Enabled: false},
				BasicAuth:  BasicAuth{Enabled: true},
				Host:       "",
			},
			wantError:   false,
			wantWarning: false,
		},
		{
			name: "OAuth enabled with host - no error",
			settings: Security{
				GoogleAuth: SocialProvider{Enabled: true},
				Host:       "https://example.com",
			},
			wantError:   false,
			wantWarning: false,
		},
		{
			name: "OAuth enabled without host - warning not error",
			settings: Security{
				GoogleAuth: SocialProvider{Enabled: true},
				Host:       "",
			},
			wantError:      true, // ValidationError is returned but it's a warning
			wantWarning:    true,
			expectedErrMsg: "OAuth authentication warning",
		},
		{
			name: "GitHub OAuth enabled without host - warning not error",
			settings: Security{
				GithubAuth: SocialProvider{Enabled: true},
				Host:       "",
			},
			wantError:      true, // ValidationError is returned but it's a warning
			wantWarning:    true,
			expectedErrMsg: "OAuth authentication warning",
		},
		{
			name: "Both OAuth providers enabled without host - warning not error",
			settings: Security{
				GoogleAuth: SocialProvider{Enabled: true},
				GithubAuth: SocialProvider{Enabled: true},
				Host:       "",
			},
			wantError:      true, // ValidationError is returned but it's a warning
			wantWarning:    true,
			expectedErrMsg: "OAuth authentication warning",
		},
		{
			name: "BasicAuth enabled without host - no error",
			settings: Security{
				BasicAuth: BasicAuth{
					Enabled:  true,
					ClientID: "birdnet-client",
					Password: "secret",
				},
				Host: "",
			},
			wantError:   false,
			wantWarning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set session duration to a valid value for all tests
			tt.settings.SessionDuration = 24 * 3600 // 24 hours in seconds
			
			err := validateSecuritySettings(&tt.settings)
			
			if tt.wantError {
				if err == nil {
					t.Errorf("validateSecuritySettings() expected error, got nil")
					return
				}
				
				// Check if it's a ValidationError (warning)
				if tt.wantWarning {
					var validationErr *ValidationError
					if !errors.As(err, &validationErr) {
						t.Errorf("validateSecuritySettings() expected ValidationError, got %T", err)
						return
					}
					
					if tt.expectedErrMsg != "" && err.Error() == "" {
						t.Errorf("validateSecuritySettings() expected error message containing %q, got empty", tt.expectedErrMsg)
						return
					}
				}
			} else if err != nil {
				t.Errorf("validateSecuritySettings() unexpected error = %v", err)
			}
		})
	}
}

func TestLoad_OAuthValidationWarnings(t *testing.T) {
	// This test would require mocking viper config loading
	// For now, we'll just document the expected behavior:
	// 1. When OAuth is enabled without host, Load() should succeed
	// 2. The ValidationWarnings slice should contain the OAuth warning
	// 3. The warning should be sent as a notification after the notification service is initialized
	
	t.Skip("Integration test - requires viper setup")
}