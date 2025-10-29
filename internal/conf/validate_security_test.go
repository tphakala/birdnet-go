package conf

import (
	stderrors "errors"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestValidateSecuritySettings_OAuth tests OAuth-related validation rules
func TestValidateSecuritySettings_OAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security Security
		wantErr  bool
		errType  string
	}{
		{
			name: "Google OAuth enabled without host - should fail",
			security: Security{
				Host: "",
				GoogleAuth: SocialProvider{
					Enabled:      true,
					ClientID:     "test-client-id",
					ClientSecret: "test-secret",
					RedirectURI:  "https://example.com/callback",
				},
			},
			wantErr: true,
			errType: "security-oauth-host",
		},
		{
			name: "GitHub OAuth enabled without host - should fail",
			security: Security{
				Host: "",
				GithubAuth: SocialProvider{
					Enabled:      true,
					ClientID:     "test-client-id",
					ClientSecret: "test-secret",
					RedirectURI:  "https://example.com/callback",
				},
			},
			wantErr: true,
			errType: "security-oauth-host",
		},
		{
			name: "Both OAuth providers enabled without host - should fail",
			security: Security{
				Host: "",
				GoogleAuth: SocialProvider{
					Enabled:      true,
					ClientID:     "google-id",
					ClientSecret: "google-secret",
					RedirectURI:  "https://example.com/google/callback",
				},
				GithubAuth: SocialProvider{
					Enabled:      true,
					ClientID:     "github-id",
					ClientSecret: "github-secret",
					RedirectURI:  "https://example.com/github/callback",
				},
			},
			wantErr: true,
			errType: "security-oauth-host",
		},
		{
			name: "Google OAuth enabled with valid host - should pass",
			security: Security{
				Host: "birdnet.example.com",
				GoogleAuth: SocialProvider{
					Enabled:      true,
					ClientID:     "test-client-id",
					ClientSecret: "test-secret",
					RedirectURI:  "https://birdnet.example.com/callback",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "GitHub OAuth enabled with valid host - should pass",
			security: Security{
				Host: "birdnet.example.com",
				GithubAuth: SocialProvider{
					Enabled:      true,
					ClientID:     "test-client-id",
					ClientSecret: "test-secret",
					RedirectURI:  "https://birdnet.example.com/callback",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "OAuth disabled without host - should pass",
			security: Security{
				Host: "",
				GoogleAuth: SocialProvider{
					Enabled: false,
				},
				GithubAuth: SocialProvider{
					Enabled: false,
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "OAuth configured but disabled - should pass even without host",
			security: Security{
				Host: "",
				GoogleAuth: SocialProvider{
					Enabled:      false,
					ClientID:     "test-client-id",
					ClientSecret: "test-secret",
					RedirectURI:  "https://example.com/callback",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateSecuritySettings(&tt.security)

			if (err != nil) != tt.wantErr {
				t.Errorf("validateSecuritySettings() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil {
				var enhancedErr *errors.EnhancedError
				if !stderrors.As(err, &enhancedErr) {
					t.Errorf("expected EnhancedError type, got %T", err)
					return
				}

				if ctx, exists := enhancedErr.Context["validation_type"]; exists {
					if ctx != tt.errType {
						t.Errorf("expected validation_type = %s, got %s", tt.errType, ctx)
					}
				} else {
					t.Errorf("expected validation_type context to be set")
				}

				if enhancedErr.Category != errors.CategoryValidation {
					t.Errorf("expected error category = %s, got %s", errors.CategoryValidation, enhancedErr.Category)
				}
			}
		})
	}
}

// TestValidateSecuritySettings_AutoTLS tests AutoTLS validation rules
func TestValidateSecuritySettings_AutoTLS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security Security
		wantErr  bool
		errType  string
	}{
		{
			name: "AutoTLS enabled without host - should fail",
			security: Security{
				Host:            "",
				AutoTLS:         true,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-autotls-host",
		},
		{
			name: "AutoTLS enabled with valid host - should pass",
			security: Security{
				Host:            "birdnet.example.com",
				AutoTLS:         true,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "AutoTLS disabled without host - should pass",
			security: Security{
				Host:            "",
				AutoTLS:         false,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "AutoTLS enabled with IP address host - should pass",
			security: Security{
				Host:            "192.168.1.100",
				AutoTLS:         true,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "AutoTLS enabled with subdomain - should pass",
			security: Security{
				Host:            "birdnet.home.arpa",
				AutoTLS:         true,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateSecuritySettings(&tt.security)

			if (err != nil) != tt.wantErr {
				t.Errorf("validateSecuritySettings() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil {
				var enhancedErr *errors.EnhancedError
				if !stderrors.As(err, &enhancedErr) {
					t.Errorf("expected EnhancedError type, got %T", err)
					return
				}

				if ctx, exists := enhancedErr.Context["validation_type"]; exists {
					if ctx != tt.errType {
						t.Errorf("expected validation_type = %s, got %s", tt.errType, ctx)
					}
				} else {
					t.Errorf("expected validation_type context to be set")
				}
			}
		})
	}
}

// TestValidateSecuritySettings_SubnetBypass tests subnet bypass validation
func TestValidateSecuritySettings_SubnetBypass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security Security
		wantErr  bool
		errType  string
	}{
		{
			name: "Valid single CIDR subnet - should pass",
			security: Security{
				AllowSubnetBypass: AllowSubnetBypass{
					Enabled: true,
					Subnet:  "192.168.1.0/24",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Valid multiple CIDR subnets - should pass",
			security: Security{
				AllowSubnetBypass: AllowSubnetBypass{
					Enabled: true,
					Subnet:  "192.168.1.0/24,10.0.0.0/8,172.16.0.0/12",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Invalid CIDR format - should fail",
			security: Security{
				AllowSubnetBypass: AllowSubnetBypass{
					Enabled: true,
					Subnet:  "192.168.1.0/33", // Invalid CIDR mask
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-subnet-format",
		},
		{
			name: "Invalid IP address - should fail",
			security: Security{
				AllowSubnetBypass: AllowSubnetBypass{
					Enabled: true,
					Subnet:  "999.999.999.999/24",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-subnet-format",
		},
		{
			name: "Missing CIDR notation - should fail",
			security: Security{
				AllowSubnetBypass: AllowSubnetBypass{
					Enabled: true,
					Subnet:  "192.168.1.0", // Missing /24
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-subnet-format",
		},
		{
			name: "Subnet bypass disabled - should pass even with invalid subnet",
			security: Security{
				AllowSubnetBypass: AllowSubnetBypass{
					Enabled: false,
					Subnet:  "invalid-subnet",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "IPv6 CIDR subnet - should pass",
			security: Security{
				AllowSubnetBypass: AllowSubnetBypass{
					Enabled: true,
					Subnet:  "2001:db8::/32",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Mixed IPv4 and IPv6 subnets - should pass",
			security: Security{
				AllowSubnetBypass: AllowSubnetBypass{
					Enabled: true,
					Subnet:  "192.168.1.0/24,2001:db8::/32",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateSecuritySettings(&tt.security)

			if (err != nil) != tt.wantErr {
				t.Errorf("validateSecuritySettings() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil {
				var enhancedErr *errors.EnhancedError
				if !stderrors.As(err, &enhancedErr) {
					t.Errorf("expected EnhancedError type, got %T", err)
					return
				}

				if ctx, exists := enhancedErr.Context["validation_type"]; exists {
					if ctx != tt.errType {
						t.Errorf("expected validation_type = %s, got %s", tt.errType, ctx)
					}
				}
			}
		})
	}
}

// TestValidateSecuritySettings_SessionDuration tests session duration validation
func TestValidateSecuritySettings_SessionDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security Security
		wantErr  bool
		errType  string
	}{
		{
			name: "Zero session duration - should fail",
			security: Security{
				SessionDuration: 0,
			},
			wantErr: true,
			errType: "security-session-duration",
		},
		{
			name: "Negative session duration - should fail",
			security: Security{
				SessionDuration: -1,
			},
			wantErr: true,
			errType: "security-session-duration",
		},
		{
			name: "Valid positive session duration - should pass",
			security: Security{
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateSecuritySettings(&tt.security)

			if (err != nil) != tt.wantErr {
				t.Errorf("validateSecuritySettings() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil {
				var enhancedErr *errors.EnhancedError
				if !stderrors.As(err, &enhancedErr) {
					t.Errorf("expected EnhancedError type, got %T", err)
					return
				}

				if ctx, exists := enhancedErr.Context["validation_type"]; exists {
					if ctx != tt.errType {
						t.Errorf("expected validation_type = %s, got %s", tt.errType, ctx)
					}
				}
			}
		})
	}
}

// TestValidateSecuritySettings_EdgeCases tests edge cases and combinations
func TestValidateSecuritySettings_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security Security
		wantErr  bool
	}{
		{
			name: "AutoTLS and OAuth both enabled with same host - should pass",
			security: Security{
				Host:    "birdnet.example.com",
				AutoTLS: true,
				GoogleAuth: SocialProvider{
					Enabled:      true,
					ClientID:     "test-id",
					ClientSecret: "test-secret",
					RedirectURI:  "https://birdnet.example.com/callback",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "BasicAuth enabled without host - should pass (BasicAuth doesn't require host)",
			security: Security{
				Host: "",
				BasicAuth: BasicAuth{
					Enabled:      true,
					ClientID:     "test-id",
					ClientSecret: "test-secret",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "All features disabled - should pass",
			security: Security{
				Host:    "",
				AutoTLS: false,
				GoogleAuth: SocialProvider{
					Enabled: false,
				},
				GithubAuth: SocialProvider{
					Enabled: false,
				},
				BasicAuth: BasicAuth{
					Enabled: false,
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Host with whitespace - should pass (normalized by caller)",
			security: Security{
				Host:            "  birdnet.example.com  ",
				AutoTLS:         true,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateSecuritySettings(&tt.security)

			if (err != nil) != tt.wantErr {
				t.Errorf("validateSecuritySettings() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
