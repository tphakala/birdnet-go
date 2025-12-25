package security

import "time"

// Security-related constants
const (
	// OAuth provider names (used as session keys)
	ProviderGoogle    = "google"
	ProviderGitHub    = "github"
	ProviderMicrosoft = "microsoftonline"

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
