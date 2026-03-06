package alerting

import "strings"

// ErrorClass represents a classified error with an i18n key suffix and
// English fallback message for user-friendly notification rendering.
type ErrorClass struct {
	Key      string // i18n key suffix, e.g., "timeout"
	Fallback string // English fallback message
}

// errorPatterns maps substring patterns to their classification.
// Order matters: more specific patterns should appear before general ones.
//
// Patterns use simple substring matching against the lowercased error string.
// HTTP status codes use "status NNN" format to avoid false positives with
// port numbers (e.g., ":5003") or durations (e.g., "500ms"). The actual
// error strings from BirdWeather, MQTT, and stream clients always include
// "status" context (e.g., "failed with status 500", "status code 401").
var errorPatterns = []struct {
	patterns []string
	class    ErrorClass
}{
	{
		patterns: []string{"status 401", "status 403", "status: 401", "status: 403", "unauthorized", "forbidden", "authentication failed"},
		class:    ErrorClass{Key: "authError", Fallback: "Authentication failed — check your API key or credentials"},
	},
	{
		patterns: []string{"status 429", "status: 429", "rate limit", "too many requests"},
		class:    ErrorClass{Key: "rateLimited", Fallback: "Rate limited — requests will slow down automatically"},
	},
	{
		patterns: []string{"status 504", "status: 504", "gateway timeout"},
		class:    ErrorClass{Key: "gatewayTimeout", Fallback: "Service may be experiencing high load"},
	},
	{
		patterns: []string{"status 500", "status 502", "status 503", "status: 500", "status: 502", "status: 503", "internal server error", "bad gateway", "service unavailable"},
		class:    ErrorClass{Key: "serverError", Fallback: "Service is temporarily unavailable"},
	},
	{
		patterns: []string{"connection refused"},
		class:    ErrorClass{Key: "connectionRefused", Fallback: "Service is unreachable — check the address and that it's running"},
	},
	{
		patterns: []string{"no such host", "dns lookup", "lookup failed"},
		class:    ErrorClass{Key: "dnsError", Fallback: "Could not resolve hostname — check the address"},
	},
	{
		patterns: []string{"certificate", "x509", "tls:"},
		class:    ErrorClass{Key: "tlsError", Fallback: "TLS/certificate error — check your security settings"},
	},
	{
		patterns: []string{"timeout", "deadline exceeded"},
		class:    ErrorClass{Key: "timeout", Fallback: "Connection timed out — the service may be slow or unreachable"},
	},
	{
		patterns: []string{" eof", "unexpected eof", "connection reset", "broken pipe", "connection closed"},
		class:    ErrorClass{Key: "connectionInterrupted", Fallback: "Connection was interrupted — will retry automatically"},
	},
	{
		patterns: []string{"no space left", "disk full"},
		class:    ErrorClass{Key: "diskFull", Fallback: "Disk is full — free some space"},
	},
	{
		patterns: []string{"permission denied", "access denied"},
		class:    ErrorClass{Key: "permissionDenied", Fallback: "Permission denied — check file or service permissions"},
	},
}

// classifyError pattern-matches a raw error string and returns a
// user-friendly ErrorClass. Returns nil for unrecognized errors.
func classifyError(errMsg string) *ErrorClass {
	if errMsg == "" {
		return nil
	}
	lower := strings.ToLower(errMsg)
	for i := range errorPatterns {
		for _, pattern := range errorPatterns[i].patterns {
			if strings.Contains(lower, pattern) {
				return &errorPatterns[i].class
			}
		}
	}
	return nil
}
