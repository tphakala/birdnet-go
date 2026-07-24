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

	// validateSecuritySettings no longer carries an OAuth-specific rule. A provider
	// that cannot complete a sign-in is reported by normalizeIncompleteFeatures on
	// the load path, which this entry point does not run; see
	// TestNormalizeOAuthProviders_LegacyCredentialedBlockWarnsAfterMigration for the
	// behaviour that replaced the deleted host rule, and validateOIDCProviders for
	// the one provider kind where an unusable configuration is still rejected here.
	tests := []struct {
		name     string
		security Security
		wantErr  bool
		errType  string
	}{
		{
			name: "Google OAuth enabled without host - not rejected here",
			security: Security{
				Host: "",
				GoogleAuth: SocialProvider{
					Enabled:      true,
					ClientID:     "test-client-id",
					ClientSecret: "test-secret",
					RedirectURI:  "https://example.com/callback",
				},
			},
			wantErr: false,
		},
		{
			name: "GitHub OAuth enabled without host - not rejected here",
			security: Security{
				Host: "",
				GithubAuth: SocialProvider{
					Enabled:      true,
					ClientID:     "test-client-id",
					ClientSecret: "test-secret",
					RedirectURI:  "https://example.com/callback",
				},
			},
			wantErr: false,
		},
		{
			name: "Both OAuth providers enabled without host - not rejected here",
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
			wantErr: false,
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
			name: "TLSMode autotls with IP address - should fail",
			security: Security{
				Host:            "192.168.1.100",
				TLSMode:         TLSModeAutoTLS,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-autotls-hostname",
		},
		{
			name: "TLSMode autotls with bare hostname - should fail",
			security: Security{
				Host:            "myserver",
				TLSMode:         TLSModeAutoTLS,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-autotls-hostname",
		},
		{
			name: "TLSMode autotls with localhost - should fail",
			security: Security{
				Host:            "localhost",
				TLSMode:         TLSModeAutoTLS,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-autotls-hostname",
		},
		{
			name: "TLSMode autotls with .local domain - should fail",
			security: Security{
				Host:            "birdnet.local",
				TLSMode:         TLSModeAutoTLS,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-autotls-hostname",
		},
		{
			name: "TLSMode autotls with .internal domain - should fail",
			security: Security{
				Host:            "birdnet.internal",
				TLSMode:         TLSModeAutoTLS,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-autotls-hostname",
		},
		{
			name: "TLSMode autotls with valid public domain - should pass",
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
		{
			// Regression: an empty Subnet string previously produced
			// "invalid CIDR address: (empty)" because every split token was
			// passed to net.ParseCIDR. Empty means "no CIDRs configured".
			name: "Enabled with empty subnet string - should pass",
			security: Security{
				AllowSubnetBypass: AllowSubnetBypass{
					Enabled: true,
					Subnet:  "",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Enabled with whitespace-only subnet - should pass",
			security: Security{
				AllowSubnetBypass: AllowSubnetBypass{
					Enabled: true,
					Subnet:  "   ",
				},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			// Regression: trailing/embedded empty tokens (double comma,
			// trailing comma, whitespace-only token) must not error.
			name: "Mixed valid and empty subnet entries - should pass",
			security: Security{
				AllowSubnetBypass: AllowSubnetBypass{
					Enabled: true,
					Subnet:  "10.0.0.0/8, ,192.168.0.0/24,",
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

// TestValidateSecuritySettings_TrustedProxies tests trusted-proxy CIDR validation.
func TestValidateSecuritySettings_TrustedProxies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security Security
		wantErr  bool
		errType  string
	}{
		{
			name: "Empty list - should pass",
			security: Security{
				TrustedProxies:  nil,
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Valid IPv4 and IPv6 CIDRs - should pass",
			security: Security{
				TrustedProxies:  []string{"10.0.0.0/8", "2001:db8::/32"},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Cloudflare preset token - should pass",
			security: Security{
				TrustedProxies:  []string{TrustedProxyCloudflarePreset},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Cloudflare preset is case-insensitive - should pass",
			security: Security{
				TrustedProxies:  []string{"CloudFlare"},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Blank entries are skipped - should pass",
			security: Security{
				TrustedProxies:  []string{"", "  ", "192.168.0.0/24"},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Invalid CIDR mask - should fail",
			security: Security{
				TrustedProxies:  []string{"192.168.1.0/33"},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-trustedproxies-format",
		},
		{
			name: "Bare IPv4 without CIDR notation - should pass",
			security: Security{
				TrustedProxies:  []string{"192.168.1.1"},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Bare IPv6 without CIDR notation - should pass",
			security: Security{
				TrustedProxies:  []string{"2001:db8::1"},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: false,
		},
		{
			name: "Garbage entry - should fail",
			security: Security{
				TrustedProxies:  []string{"not-an-ip-or-cidr"},
				SessionDuration: 24 * time.Hour,
			},
			wantErr: true,
			errType: "security-trustedproxies-format",
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
		name          string
		security      Security
		wantErr       bool
		errType       string
		wantDuration  time.Duration // expected duration after normalization
		checkDuration bool          // whether to assert the normalized value
	}{
		{
			name: "Zero session duration - normalized to default",
			security: Security{
				SessionDuration: 0,
			},
			wantErr:       false,
			checkDuration: true,
			wantDuration:  DefaultSessionDuration,
		},
		{
			name: "Negative session duration - normalized to default",
			security: Security{
				SessionDuration: -1,
			},
			wantErr:       false,
			checkDuration: true,
			wantDuration:  DefaultSessionDuration,
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
				if tt.checkDuration {
					assert.Equal(t, tt.wantDuration, tt.security.SessionDuration,
						"session duration should be normalized to default")
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
