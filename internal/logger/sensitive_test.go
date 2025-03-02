package logger

import (
	"testing"
)

func TestRedactSensitiveData(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "no sensitive data",
			input:    "This is a regular log message without sensitive data",
			expected: "This is a regular log message without sensitive data",
		},
		{
			name:     "bearer token",
			input:    "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
			expected: "Authorization: Bearer [REDACTED]",
		},
		{
			name:     "jwt token",
			input:    "Token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
			expected: "Token: [REDACTED]",
		},
		{
			name:     "api key",
			input:    "Using API_KEY=sk_test_BQokikJOvBiI2HlWgH4olfQ2",
			expected: "Using API_KEY=[REDACTED]",
		},
		{
			name:     "password",
			input:    "password=SuperSecretPassword123",
			expected: "password=[REDACTED]",
		},
		{
			name:     "session cookie",
			input:    "Cookie: session=1234567890abcdef; Path=/; HttpOnly",
			expected: "Cookie: session=[REDACTED]; Path=/; HttpOnly",
		},
		{
			name:     "csrf token",
			input:    "csrf-token=abc123def456ghi789",
			expected: "csrf-token=[REDACTED]",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := RedactSensitiveData(tc.input)
			if result != tc.expected {
				t.Errorf("RedactSensitiveData(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestRedactSensitiveFields(t *testing.T) {
	testCases := []struct {
		name     string
		input    []interface{}
		expected []interface{}
	}{
		{
			name:     "empty slice",
			input:    []interface{}{},
			expected: []interface{}{},
		},
		{
			name:     "no sensitive fields",
			input:    []interface{}{"method", "GET", "path", "/api/users"},
			expected: []interface{}{"method", "GET", "path", "/api/users"},
		},
		{
			name:     "password field",
			input:    []interface{}{"username", "johndoe", "password", "secretpass"},
			expected: []interface{}{"username", "johndoe", "password", "[REDACTED]"},
		},
		{
			name:     "token field",
			input:    []interface{}{"token", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.signature", "valid", true},
			expected: []interface{}{"token", "[REDACTED]", "valid", true},
		},
		{
			name:     "multiple sensitive fields",
			input:    []interface{}{"user", "admin", "api_key", "123456", "csrf_token", "abcdef"},
			expected: []interface{}{"user", "admin", "api_key", "[REDACTED]", "csrf_token", "[REDACTED]"},
		},
		{
			name:     "mixed case sensitive keywords",
			input:    []interface{}{"Username", "johndoe", "Password", "secretpass"},
			expected: []interface{}{"Username", "johndoe", "Password", "[REDACTED]"},
		},
		{
			name:     "non-string values",
			input:    []interface{}{"password", 12345, "count", 42},
			expected: []interface{}{"password", 12345, "count", 42}, // Only string values are redacted
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := RedactSensitiveFields(tc.input)

			// Check the length first
			if len(result) != len(tc.expected) {
				t.Fatalf("RedactSensitiveFields returned %d elements, want %d", len(result), len(tc.expected))
			}

			// Compare each element
			for i := range result {
				if result[i] != tc.expected[i] {
					t.Errorf("Element %d: got %v, want %v", i, result[i], tc.expected[i])
				}
			}
		})
	}
}
