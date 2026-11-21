package security

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Security-related constants
const (
	// Config provider IDs (used in OAuthProviderConfig.Provider)
	// These match what the frontend sends and what's stored in config; conf owns
	// the list so that settings validation uses the same one.
	ConfigGoogle    = conf.OAuthProviderGoogle
	ConfigGitHub    = conf.OAuthProviderGitHub
	ConfigMicrosoft = conf.OAuthProviderMicrosoft
	ConfigLine      = conf.OAuthProviderLine
	ConfigKakao     = conf.OAuthProviderKakao
	ConfigOIDC      = conf.OAuthProviderOIDC

	// Goth provider names (used for session keys and goth registration)
	// These match the goth library provider names
	ProviderGoogle    = "google"          // Same as config
	ProviderGitHub    = "github"          // Same as config
	ProviderMicrosoft = "microsoftonline" // Different from config!
	ProviderLine      = "line"            // Same as config
	ProviderKakao     = "kakao"           // Same as config
	ProviderOIDC      = "openid-connect"  // Different from config!

	// Session and cookie settings
	DefaultSessionMaxAgeDays    = 7
	DefaultSessionMaxAgeSeconds = 86400 * DefaultSessionMaxAgeDays // 7 days in seconds

	// SessionCookieName is the cookie that names the signed-in browser's session
	// record. The gothic session cookie only carries the OAuth handshake.
	SessionCookieName = "birdnet_session"

	// RememberedSessionDuration is how long a session that was signed in with
	// "Remember this browser" ticked lasts after its last renewal.
	RememberedSessionDuration = 30 * 24 * time.Hour

	// RememberedCookieMaxAge is the Max-Age of a remembered session's cookie,
	// written once at sign-in: the longest lifetime browsers keep (400 days). The
	// server record, not the cookie, decides when the session ends, so the cookie
	// is never re-sent when the record is renewed.
	RememberedCookieMaxAge = 400 * 24 * time.Hour

	// sessionIdleTimeout is how long a session that was signed in with
	// "Remember this browser" unticked lasts after its last renewal.
	sessionIdleTimeout = 8 * time.Hour

	// rememberedSessionRenewInterval is the most often a remembered session is
	// extended (and persisted) per browser.
	rememberedSessionRenewInterval = 24 * time.Hour

	// idleSessionRenewInterval is the most often an unticked session is
	// extended (and persisted) per browser.
	idleSessionRenewInterval = 15 * time.Minute

	// RememberQueryParam is the query parameter of GET /auth/:provider that
	// carries the "Remember this browser" choice, and RememberQueryValue the
	// value that means ticked.
	RememberQueryParam = "remember"
	RememberQueryValue = "1"

	// rememberChoiceCookieName holds the remember choice between the start of a
	// provider sign-in and its callback.
	rememberChoiceCookieName = "birdnet_remember_browser"

	// rememberChoiceMaxAgeSeconds bounds how long the choice cookie lives; it
	// only has to outlast the round trip to the identity provider.
	rememberChoiceMaxAgeSeconds = 10 * 60

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

	// Provider capacity hint
	InitialProviderCapacity = 6

	// Path validation limits
	MaxSafePathLength = 512

	// MaxSafeRedirectLength bounds a full redirect target (path plus query).
	// The path component is bounded more tightly by MaxSafePathLength; the
	// larger overall budget accommodates legitimate, heavily-filtered query
	// strings without dropping the user back to the base path after login.
	MaxSafeRedirectLength = 2048

	// OIDC discovery retry settings
	OIDCRetryInitialBackoff = 5 * time.Second
	OIDCRetryMaxBackoff     = 60 * time.Second
	OIDCRetryMaxDuration    = 5 * time.Minute
	OIDCRetryBackoffFactor  = 2

	// OIDCHTTPTimeout bounds each request to the identity provider (discovery,
	// token exchange, userinfo and token refresh).
	OIDCHTTPTimeout = 10 * time.Second
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
