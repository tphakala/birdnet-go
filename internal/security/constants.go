package security

import "time"

// Security-related constants
const (
	// Config provider IDs (used in OAuthProviderConfig.Provider)
	// These match what the frontend sends and what's stored in config
	ConfigGoogle    = "google"
	ConfigGitHub    = "github"
	ConfigMicrosoft = "microsoft"
	ConfigLine      = "line"
	ConfigKakao     = "kakao"
	ConfigOIDC      = "oidc"

	// Goth provider names (used for session keys and goth registration)
	// These match the goth library provider names
	ProviderGoogle    = "google"          // Same as config
	ProviderGitHub    = "github"          // Same as config
	ProviderMicrosoft = "microsoftonline" // Different from config!
	ProviderLine      = "line"            // Same as config
	ProviderKakao     = "kakao"           // Same as config
	ProviderOIDC      = "openid-connect"  // Different from config!

	// SessionKeyAuthProvider is the session key used to store the active OAuth provider name.
	// This avoids iterating all providers when looking up the active session.
	SessionKeyAuthProvider = "auth_provider"

	// Session and cookie settings
	DefaultSessionMaxAgeDays    = 7
	DefaultSessionMaxAgeSeconds = 86400 * DefaultSessionMaxAgeDays // 7 days in seconds

	// Cryptographic settings
	MinSessionSecretLength = 32
	AuthCodeByteLength     = 32
	AccessTokenByteLength  = 32

	// File permissions
	DirPermissions  = 0o750 // rwxr-x---
	FilePermissions = 0o600 // rw-------

	// Timeouts
	TokenExchangeTimeout = 15 * time.Second
	TokenSaveTimeout     = 10 * time.Second
	ThrottleLogInterval  = 5 * time.Minute

	// Session store settings
	MaxSessionSizeBytes = 1024 * 1024 // 1MB max size

	// CIDR mask bits for IPv4 /24 subnet
	IPv4SubnetMaskBits   = 24
	IPv4TotalAddressBits = 32

	// Provider capacity hint
	InitialProviderCapacity = 6

	// Path validation limits
	MaxSafePathLength = 512

	// OIDC discovery retry settings
	OIDCRetryInitialBackoff = 5 * time.Second
	OIDCRetryMaxBackoff     = 60 * time.Second
	OIDCRetryMaxDuration    = 5 * time.Minute
	OIDCRetryBackoffFactor  = 2
)

// ConfigToGothProvider maps config provider IDs to goth provider names.
// Most providers use the same name, but Microsoft is different.
var ConfigToGothProvider = map[string]string{
	ConfigGoogle:    ProviderGoogle,
	ConfigGitHub:    ProviderGitHub,
	ConfigMicrosoft: ProviderMicrosoft,
	ConfigLine:      ProviderLine,
	ConfigKakao:     ProviderKakao,
	ConfigOIDC:      ProviderOIDC,
}

// GothToConfigProvider maps goth provider names back to config provider IDs.
// This is the reverse of ConfigToGothProvider and is used when looking up
// provider config from a stored goth provider name.
var GothToConfigProvider = func() map[string]string {
	m := make(map[string]string, len(ConfigToGothProvider))
	for config, goth := range ConfigToGothProvider {
		m[goth] = config
	}
	return m
}()

// GetGothProviderName converts a config provider ID to the goth provider name.
// Falls back to the config ID if no mapping exists.
func GetGothProviderName(configProvider string) string {
	if gothName, ok := ConfigToGothProvider[configProvider]; ok {
		return gothName
	}
	return configProvider
}

// gothToConfigProvider converts a goth provider name to a config provider ID.
// Falls back to the goth name if no mapping exists (most providers use the same name).
func gothToConfigProvider(gothProvider string) string {
	if configName, ok := GothToConfigProvider[gothProvider]; ok {
		return configName
	}
	return gothProvider
}
