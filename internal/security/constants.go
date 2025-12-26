package security

import "time"

// Security-related constants
const (
	// Config provider IDs (used in OAuthProviderConfig.Provider)
	// These match what the frontend sends and what's stored in config
	ConfigGoogle    = "google"
	ConfigGitHub    = "github"
	ConfigMicrosoft = "microsoft"

	// Goth provider names (used for session keys and goth registration)
	// These match the goth library provider names
	ProviderGoogle    = "google"       // Same as config
	ProviderGitHub    = "github"       // Same as config
	ProviderMicrosoft = "microsoftonline" // Different from config!

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
	InitialProviderCapacity = 3

	// Path validation limits
	MaxSafePathLength = 512
)

// ConfigToGothProvider maps config provider IDs to goth provider names.
// Most providers use the same name, but Microsoft is different.
var ConfigToGothProvider = map[string]string{
	ConfigGoogle:    ProviderGoogle,
	ConfigGitHub:    ProviderGitHub,
	ConfigMicrosoft: ProviderMicrosoft,
}

// GetGothProviderName converts a config provider ID to the goth provider name.
// Falls back to the config ID if no mapping exists.
func GetGothProviderName(configProvider string) string {
	if gothName, ok := ConfigToGothProvider[configProvider]; ok {
		return gothName
	}
	return configProvider
}
