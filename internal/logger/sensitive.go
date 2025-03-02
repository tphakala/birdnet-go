// sensitive.go
package logger

import (
	"regexp"
	"strings"
)

// SensitiveDataPatterns contains regex patterns for sensitive data that should be redacted in logs
var SensitiveDataPatterns = []*regexp.Regexp{
	// Auth tokens (Bearer, JWT, etc.)
	regexp.MustCompile(`(?i)(bearer\s+)([A-Za-z0-9-._~+/]+=*)`),
	regexp.MustCompile(`(?i)(eyJ[a-zA-Z0-9_-]{5,}\.eyJ[a-zA-Z0-9_-]{5,})\.[a-zA-Z0-9_-]{5,}`),

	// API keys, tokens and secrets
	regexp.MustCompile(`(?i)((api|access|auth|token|secret|key|passw(or)?d)[0-9a-z\-_\.]*[\s:=]+)([^;,\s]{5,})`),

	// Common cookie patterns
	regexp.MustCompile(`(?i)(session|auth|token|csrf|sid)=([^;,\s]{5,})`),

	// CSRF tokens
	regexp.MustCompile(`(?i)(csrf[-_]?token[\s:=]+)([^;,\s"]{5,})`),
}

// SensitiveKeywords are keywords that indicate fields may contain sensitive data
var SensitiveKeywords = []string{
	"password", "passwd", "secret", "credential", "token", "auth", "key", "api_key",
	"apikey", "access_token", "secret_key", "authorization", "cookie", "session", "csrf",
}

// RedactSensitiveData replaces sensitive information with "[REDACTED]"
func RedactSensitiveData(input string) string {
	if input == "" {
		return input
	}

	// Apply all regex patterns
	for _, pattern := range SensitiveDataPatterns {
		input = pattern.ReplaceAllString(input, "$1[REDACTED]")
	}

	return input
}

// RedactSensitiveFields checks if field keys indicate sensitive data and redacts the values
func RedactSensitiveFields(fields []interface{}) []interface{} {
	// Create a copy to avoid modifying the original slice
	result := make([]interface{}, len(fields))
	copy(result, fields)

	// Process in key-value pairs
	for i := 0; i < len(result)-1; i += 2 {
		// Check if this is a key (string) followed by a value
		if key, ok := result[i].(string); ok {
			keyLower := strings.ToLower(key)

			// Check if the key indicates sensitive data
			for _, sensitiveKey := range SensitiveKeywords {
				if strings.Contains(keyLower, sensitiveKey) {
					// If the value is a string, redact it
					if value, ok := result[i+1].(string); ok && value != "" {
						result[i+1] = "[REDACTED]"
					}
					break
				}
			}
		}
	}

	return result
}
