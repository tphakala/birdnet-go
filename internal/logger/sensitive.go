// Package logger provides structured logging with privacy-aware field constructors.
package logger

import "github.com/tphakala/birdnet-go/internal/privacy"

// Username creates a field with a hashed username for safe logging.
// The username is converted to a hash prefix (e.g., "user-a1b2c3d4") that enables
// log correlation for the same user without exposing the actual username.
//
// Example:
//
//	log.Info("Login attempt", logger.Username("admin"))
//	// Output: {"username": "user-8c6976e5"}
func Username(value string) Field {
	return Field{Key: internKey("username"), Value: privacy.ScrubUsername(value)}
}

// Password creates a field indicating a password was present (always redacted).
// This field is useful for logging that a password was provided without exposing it.
//
// Example:
//
//	log.Info("Auth request", logger.Password("secret123"))
//	// Output: {"password": "[REDACTED]"}
func Password(value string) Field {
	return Field{Key: internKey("password"), Value: privacy.ScrubPassword(value)}
}

// Token creates a field with a redacted token value.
// Shows the token length for debugging without exposing the actual value.
//
// Example:
//
//	log.Info("API call", logger.Token("api_key", "sk_live_abc123..."))
//	// Output: {"api_key": "[TOKEN:len=32]"}
func Token(key, value string) Field {
	return Field{Key: internKey(key), Value: privacy.ScrubToken(value)}
}

// URL creates a field with a sanitized URL.
// Removes credentials and sensitive path components while preserving
// useful debugging information like scheme, host category, and path structure.
//
// Example:
//
//	log.Info("Request", logger.URL("endpoint", "https://user:pass@api.example.com/v1/users"))
//	// Output: {"endpoint": "url-abc123def456"}
func URL(key, value string) Field {
	return Field{Key: internKey(key), Value: privacy.AnonymizeURL(value)}
}

// CredentialURL creates a field with a sanitized URL that may contain credentials.
// Specifically designed for notification service URLs (telegram://, discord://, etc.)
// that often embed tokens in the URL structure.
//
// Example:
//
//	log.Info("Notification", logger.CredentialURL("service", "telegram://bot123456:ABC-DEF@telegram.org"))
//	// Output: {"service": "telegram://[REDACTED]@telegram.org"}
func CredentialURL(key, value string) Field {
	return Field{Key: internKey(key), Value: privacy.ScrubCredentialURL(value)}
}

// SanitizedString creates a field with fully scrubbed content.
// Applies all privacy scrubbing (URLs, emails, IPs, tokens, etc.) to the value.
// Use this for free-form text that may contain any type of sensitive data.
//
// Example:
//
//	log.Error("Request failed", logger.SanitizedString("details", errorMsg))
func SanitizedString(key, value string) Field {
	return Field{Key: internKey(key), Value: privacy.ScrubMessage(value)}
}

// SanitizedError creates an error field with a scrubbed message.
// The error message is sanitized to remove any sensitive data like
// URLs with credentials, API tokens, or email addresses.
//
// Example:
//
//	if err := doSomething(); err != nil {
//	    log.Error("Operation failed", logger.SanitizedError(err))
//	}
func SanitizedError(err error) Field {
	if err == nil {
		return Field{Key: errorKey, Value: nil}
	}
	return Field{Key: errorKey, Value: privacy.ScrubMessage(err.Error())}
}

// Credential creates a completely redacted field for sensitive credentials.
// Use this when you need to log that a credential exists without any information about it.
//
// Example:
//
//	log.Info("Config loaded", logger.Credential("mqtt_password"))
//	// Output: {"mqtt_password": "[REDACTED]"}
func Credential(key string) Field {
	return Field{Key: internKey(key), Value: privacy.RedactedMarker}
}
