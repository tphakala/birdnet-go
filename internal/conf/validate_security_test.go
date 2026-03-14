package conf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

			if tt.wantErr {
				require.Error(t, err)
				assertValidationError(t, err, tt.errType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateSecuritySettings_TLSMode tests TLS mode validation rules
func TestValidateSecuritySettings_TLSMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security Security
		wantErr  bool
		errType  string
	}{
		{
			name: "TLSMode autotls without host - should fail",
			security: Security{
				Host:            "",
				TLSMode:         TLSModeAutoTLS,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-autotls-host",
		},
		{
			name: "TLSMode autotls with valid host - should pass",
			security: Security{
				Host:            "birdnet.example.com",
				TLSMode:         TLSModeAutoTLS,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "TLSMode none without host - should pass",
			security: Security{
				Host:            "",
				TLSMode:         TLSModeNone,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "TLSMode autotls with IP address host - should pass",
			security: Security{
				Host:            "192.168.1.100",
				TLSMode:         TLSModeAutoTLS,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "TLSMode autotls with subdomain - should pass",
			security: Security{
				Host:            "birdnet.home.arpa",
				TLSMode:         TLSModeAutoTLS,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "TLSMode manual - should pass",
			security: Security{
				TLSMode:         TLSModeManual,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "TLSMode selfsigned - should pass",
			security: Security{
				TLSMode:         TLSModeSelfSigned,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "TLSMode invalid value - should fail",
			security: Security{
				TLSMode:         TLSMode("bogus"),
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-tlsmode-invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateSecuritySettings(&tt.security)

			if tt.wantErr {
				require.Error(t, err)
				assertValidationError(t, err, tt.errType)
			} else {
				assert.NoError(t, err)
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

			if tt.wantErr {
				require.Error(t, err)
				assertValidationError(t, err, tt.errType)
			} else {
				assert.NoError(t, err)
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

			if tt.wantErr {
				require.Error(t, err)
				assertValidationError(t, err, tt.errType)
			} else {
				assert.NoError(t, err)
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
			name: "TLSMode autotls and OAuth both enabled with same host - should pass",
			security: Security{
				Host:    "birdnet.example.com",
				TLSMode: TLSModeAutoTLS,
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
				TLSMode: TLSModeNone,
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
				TLSMode:         TLSModeAutoTLS,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateSecuritySettings(&tt.security)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestMigrateTLSConfig_AutoTLSToTLSMode tests migration from AutoTLS=true to TLSMode
func TestMigrateTLSConfig_AutoTLSToTLSMode(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Security.AutoTLS = true
	settings.Security.TLSMode = ""

	migrated := settings.MigrateTLSConfig()

	assert.True(t, migrated, "migration should have occurred")
	assert.Equal(t, TLSModeAutoTLS, settings.Security.TLSMode, "TLSMode should be set to autotls")
}

// TestMigrateTLSConfig_AlreadyMigrated tests that migration is skipped when TLSMode is already set
func TestMigrateTLSConfig_AlreadyMigrated(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Security.AutoTLS = true
	settings.Security.TLSMode = TLSModeManual

	migrated := settings.MigrateTLSConfig()

	assert.False(t, migrated, "migration should not have occurred")
	assert.Equal(t, TLSModeManual, settings.Security.TLSMode, "TLSMode should remain manual")
}

// TestMigrateTLSConfig_NoAutoTLS tests that no migration occurs when AutoTLS is false
func TestMigrateTLSConfig_NoAutoTLS(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Security.AutoTLS = false
	settings.Security.TLSMode = ""

	migrated := settings.MigrateTLSConfig()

	assert.False(t, migrated, "migration should not have occurred")
	assert.Equal(t, TLSModeNone, settings.Security.TLSMode, "TLSMode should remain empty")
}
