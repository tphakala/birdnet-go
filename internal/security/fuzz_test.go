package security

import (
	"net"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// =============================================================================
// Fuzz Tests for URL Validation
// =============================================================================

// FuzzValidateRedirectURI tests redirect URI validation with random inputs.
// Goals:
// - No panics on any input
// - Consistent behavior (same input = same output)
// - Rejects obviously malicious URIs
func FuzzValidateRedirectURI(f *testing.F) {
	// Seed corpus with interesting inputs
	seedInputs := []string{
		// Valid URIs
		"http://localhost/callback",
		"https://example.com:8080/auth/callback",
		"http://localhost:80/callback",
		"https://localhost:443/callback",
		// Attack patterns
		"http://evil.com/callback",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"http://localhost/callback?evil=param",
		"http://localhost/callback#fragment",
		// Malformed URIs
		"://invalid",
		"http://",
		"",
		"   ",
		"\x00\x00\x00",
		"http://localhost/\x00callback",
		// Unicode attacks
		"http://localhost/callback\u202e",
		"http://lοcalhost/callback", // Greek omicron instead of 'o'
		// Very long input
		"http://localhost/" + strings.Repeat("a", 10000),
		// Special characters
		"http://localhost/callback%00",
		"http://localhost/callback%0d%0a",
		"http://localhost/callback\r\n",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	expectedURI, _ := url.Parse("http://localhost/callback")

	f.Fuzz(func(t *testing.T, input string) {
		// The function should never panic
		result := ValidateRedirectURI(input, expectedURI)

		// If input matches expected, result should be nil
		// Otherwise, result should be an error
		if result == nil {
			// Verify the input actually matches expected
			parsed, err := url.Parse(input)
			if err != nil {
				t.Errorf("ValidateRedirectURI returned nil for unparseable URI: %q", input)
			}
			// Basic sanity check - scheme and host should match
			if !strings.EqualFold(parsed.Scheme, expectedURI.Scheme) {
				t.Errorf("ValidateRedirectURI returned nil but scheme mismatch: got %q, want %q", parsed.Scheme, expectedURI.Scheme)
			}
		}

		// Consistency check - same input should give same result type
		result2 := ValidateRedirectURI(input, expectedURI)
		if (result == nil) != (result2 == nil) {
			t.Errorf("Inconsistent results for input %q", input)
		}
	})
}

// FuzzValidateRedirectURINilExpected tests behavior with nil expected URI.
func FuzzValidateRedirectURINilExpected(f *testing.F) {
	f.Add("http://localhost/callback")
	f.Add("")
	f.Add("javascript:alert(1)")

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic and should always return error for nil expected
		result := ValidateRedirectURI(input, nil)
		assert.Error(t, result, "Should return error for nil expected URI")
	})
}

// =============================================================================
// Fuzz Tests for Path Validation
// =============================================================================

// FuzzIsSafePath tests path safety validation with random inputs.
// Goals:
// - No panics
// - Rejects path traversal attempts
// - Rejects encoded attacks
// - Consistent behavior
func FuzzIsSafePath(f *testing.F) {
	seedInputs := []string{
		// Valid paths
		"/",
		"/dashboard",
		"/admin/settings",
		"/path/to/resource",
		// Path traversal
		"/../etc/passwd",
		"/path/../secret",
		"/path/..%2f..%2fetc/passwd",
		"/..",
		// Encoded traversal
		"/%2e%2e/etc/passwd",
		"/%2e./etc/passwd",
		"/.%2e/etc/passwd",
		// Double encoding
		"/%252e%252e/etc/passwd",
		// Null bytes
		"/path\x00",
		"/%00",
		"/path%00.txt",
		// Protocol specifiers
		"/path://evil",
		"//evil.com",
		// Backslashes
		"/path\\to\\resource",
		"/%5c",
		// Double slashes
		"/path//to",
		"/%2f%2f",
		// Invalid - no leading slash
		"path",
		"",
		// Very long paths
		"/" + strings.Repeat("a", 600),
		// Unicode
		"/настройки",
		"/path\u202e",
		// CRLF
		"/path\r\n",
		"/path%0d%0a",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic
		result := IsSafePath(input)

		// Invariants that must hold
		if result {
			// Must start with /
			assert.True(t, strings.HasPrefix(input, "/"), "Safe path must start with /")

			// Must be under length limit
			assert.Less(t, len(input), MaxSafePathLength, "Safe path must be under MaxSafePathLength chars")

			// Must not contain dangerous patterns
			lower := strings.ToLower(input)
			assert.NotContains(t, input, "..", "Safe path must not contain ..")
			assert.NotContains(t, lower, "%2e%2e", "Safe path must not contain encoded ..")
			assert.NotContains(t, input, "//", "Safe path must not contain //")
			assert.NotContains(t, input, "\\", "Safe path must not contain \\")
			assert.NotContains(t, input, "\x00", "Safe path must not contain null")
			assert.NotContains(t, lower, "%00", "Safe path must not contain encoded null")
		}

		// Consistency check
		result2 := IsSafePath(input)
		assert.Equal(t, result, result2, "Inconsistent results for: %q", input)
	})
}

// FuzzValidateAuthCallbackRedirect tests callback redirect validation.
func FuzzValidateAuthCallbackRedirect(f *testing.F) {
	seedInputs := []string{
		// Valid redirects
		"/",
		"/dashboard",
		"/admin/settings?tab=users",
		// Attack patterns - should return "/"
		"//evil.com",
		"//evil.com/path",
		"http://evil.com",
		"https://evil.com/path",
		"javascript:alert(1)",
		"/\\evil.com",
		"\\\\evil.com",
		// CRLF injection
		"/path\r\nSet-Cookie:evil=true",
		"/path%0d%0aSet-Cookie:evil=true",
		"/path%250d%250a",
		// Empty and special
		"",
		"   ",
		"\x00",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic
		result := ValidateAuthCallbackRedirect(input)

		// Result must always be a valid relative path or default "/"
		assert.True(t, strings.HasPrefix(result, "/"), "Result must start with /")

		// Result must not be a protocol-relative URL
		if len(result) > 1 {
			assert.NotEqual(t, '/', result[1], "Result must not be protocol-relative (//...)")
			assert.NotEqual(t, '\\', result[1], "Result must not start with /\\")
		}

		// Result must not contain CRLF
		assert.False(t, strings.ContainsAny(result, "\r\n"), "Result must not contain CRLF")

		// Result must not have a scheme
		if parsed, err := url.Parse(result); err == nil {
			assert.Empty(t, parsed.Scheme, "Result must not have a scheme")
			assert.Empty(t, parsed.Host, "Result must not have a host")
		}

		// Consistency check
		result2 := ValidateAuthCallbackRedirect(input)
		assert.Equal(t, result, result2, "Inconsistent results")
	})
}

// =============================================================================
// Fuzz Tests for CRLF Detection
// =============================================================================

// FuzzContainsCRLF tests CRLF detection with random inputs.
func FuzzContainsCRLF(f *testing.F) {
	seedInputs := []string{
		// No CRLF
		"",
		"hello world",
		"/path/to/resource",
		"normal%20encoding",
		// Raw CRLF
		"hello\rworld",
		"hello\nworld",
		"hello\r\nworld",
		// Encoded CRLF
		"hello%0dworld",
		"hello%0aworld",
		"hello%0d%0aworld",
		"%0D%0A",
		// Double encoded
		"%250d",
		"%250a",
		"%250d%250a",
		// Triple encoded
		"%25250d",
		"%25250a",
		// Mixed case
		"%0D%0a",
		"%0d%0A",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic
		result := containsCRLF(input)

		// If input contains actual CRLF, result must be true
		if strings.ContainsAny(input, "\r\n") {
			assert.True(t, result, "Must detect raw CRLF in: %q", input)
		}

		// If input contains encoded CRLF (case insensitive), result must be true
		lower := strings.ToLower(input)
		if strings.Contains(lower, "%0d") || strings.Contains(lower, "%0a") {
			assert.True(t, result, "Must detect encoded CRLF in: %q", input)
		}

		// If input contains double-encoded CRLF, result must be true
		if strings.Contains(lower, "%250d") || strings.Contains(lower, "%250a") {
			assert.True(t, result, "Must detect double-encoded CRLF in: %q", input)
		}

		// Consistency check
		result2 := containsCRLF(input)
		assert.Equal(t, result, result2, "Inconsistent results")
	})
}

// =============================================================================
// Fuzz Tests for User ID Validation
// =============================================================================

// FuzzIsValidUserId tests user ID validation with random inputs.
func FuzzIsValidUserId(f *testing.F) {
	seedInputs := []struct {
		configured string
		provided   string
	}{
		// Valid cases
		{"user@example.com", "user@example.com"},
		{"USER@EXAMPLE.COM", "user@example.com"},
		{"user1,user2,user3", "user2"},
		// Whitespace handling
		{"  user@example.com  ", "user@example.com"},
		{"user1 , user2 , user3", "user2"},
		// Empty cases
		{"", "user@example.com"},
		{"user@example.com", ""},
		{"", ""},
		// Special characters
		{"user+tag@example.com", "user+tag@example.com"},
		// Unicode
		{"пользователь@example.com", "пользователь@example.com"},
		// Injection attempts
		{"user@example.com,", "user@example.com"},
		{",user@example.com", "user@example.com"},
		{",,user@example.com,,", "user@example.com"},
		// Very long inputs
		{strings.Repeat("a", 10000), "a"},
		{"user", strings.Repeat("a", 10000)},
	}

	for _, s := range seedInputs {
		f.Add(s.configured, s.provided)
	}

	f.Fuzz(func(t *testing.T, configured, provided string) {
		// Should never panic
		result := isValidUserId(configured, provided)

		// If either is empty, result must be false
		if configured == "" || provided == "" {
			assert.False(t, result, "Empty input should return false")
		}

		// If provided is only whitespace after trimming, should be false
		if strings.TrimSpace(provided) == "" {
			assert.False(t, result, "Whitespace-only provided should return false")
		}

		// If result is true, verify the provided ID is actually in configured list
		if result {
			providedTrimmed := strings.TrimSpace(provided)
			found := false
			for allowedId := range strings.SplitSeq(configured, ",") {
				if strings.EqualFold(strings.TrimSpace(allowedId), providedTrimmed) {
					found = true
					break
				}
			}
			assert.True(t, found, "Valid result but ID not found in configured list")
		}

		// Consistency check
		result2 := isValidUserId(configured, provided)
		assert.Equal(t, result, result2, "Inconsistent results")
	})
}

// =============================================================================
// Fuzz Tests for IP/Subnet Handling
// =============================================================================

// FuzzGetIPv4Subnet tests IPv4 subnet extraction.
func FuzzGetIPv4Subnet(f *testing.F) {
	seedIPs := []string{
		"192.168.1.1",
		"10.0.0.1",
		"172.16.0.1",
		"127.0.0.1",
		"0.0.0.0",
		"255.255.255.255",
		"::1",
		"::ffff:192.168.1.1",
		"fe80::1",
		"invalid",
		"",
		"256.256.256.256",
		"1.2.3.4.5",
		"-1.0.0.0",
	}

	for _, ip := range seedIPs {
		f.Add(ip)
	}

	f.Fuzz(func(t *testing.T, ipStr string) {
		ip := net.ParseIP(ipStr)
		// Should never panic even with nil
		result := getIPv4Subnet(ip)

		if ip == nil {
			assert.Nil(t, result, "Nil IP should return nil subnet")
			return
		}

		// If result is not nil, verify it's a valid /24 subnet
		if result != nil {
			// Result should be IPv4
			assert.NotNil(t, result.To4(), "Subnet should be IPv4")
			// Last octet should be 0 for /24
			assert.Equal(t, byte(0), result[len(result)-1], "Last octet should be 0 for /24 subnet")
		}

		// Consistency check
		result2 := getIPv4Subnet(ip)
		if result == nil {
			assert.Nil(t, result2)
		} else {
			assert.True(t, result.Equal(result2), "Inconsistent results")
		}
	})
}

// FuzzIsRequestFromAllowedSubnet tests allowed subnet checking.
func FuzzIsRequestFromAllowedSubnet(f *testing.F) {
	type testInput struct {
		ip      string
		subnets string
		enabled bool
	}

	seedInputs := []testInput{
		// Valid cases
		{"192.168.1.100", "192.168.1.0/24", true},
		{"10.0.0.50", "10.0.0.0/8", true},
		{"127.0.0.1", "127.0.0.0/8", true},
		// Not in subnet
		{"192.168.1.100", "10.0.0.0/8", true},
		// Multiple subnets
		{"192.168.1.100", "10.0.0.0/8, 192.168.1.0/24", true},
		// Invalid CIDR
		{"192.168.1.100", "invalid", true},
		{"192.168.1.100", "192.168.1.0/33", true},
		// Disabled
		{"192.168.1.100", "192.168.1.0/24", false},
		// Empty inputs
		{"", "192.168.1.0/24", true},
		{"192.168.1.100", "", true},
		// Loopback
		{"127.0.0.1", "", true},
		// Invalid IP
		{"invalid", "192.168.1.0/24", true},
		{"256.256.256.256", "192.168.1.0/24", true},
	}

	for _, input := range seedInputs {
		f.Add(input.ip, input.subnets, input.enabled)
	}

	f.Fuzz(func(t *testing.T, ipStr, subnets string, enabled bool) {
		server := &OAuth2Server{
			Settings: &conf.Settings{
				Security: conf.Security{
					AllowSubnetBypass: conf.AllowSubnetBypass{
						Enabled: enabled,
						Subnet:  subnets,
					},
				},
			},
		}

		// Should never panic
		result := server.IsRequestFromAllowedSubnet(ipStr)

		// If disabled, result must be false (unless loopback)
		if !enabled {
			parsedIP := net.ParseIP(ipStr)
			if parsedIP == nil || !parsedIP.IsLoopback() {
				assert.False(t, result, "Disabled subnet bypass should return false")
			}
		}

		// If IP is empty or invalid, result must be false
		if ipStr == "" {
			assert.False(t, result, "Empty IP should return false")
		}
		if net.ParseIP(ipStr) == nil {
			assert.False(t, result, "Invalid IP should return false")
		}

		// Consistency check
		result2 := server.IsRequestFromAllowedSubnet(ipStr)
		assert.Equal(t, result, result2, "Inconsistent results")
	})
}

// FuzzIsInLocalSubnet tests local subnet detection.
func FuzzIsInLocalSubnet(f *testing.F) {
	seedIPs := []string{
		"192.168.1.1",
		"10.0.0.1",
		"172.16.0.1",
		"127.0.0.1",
		"0.0.0.0",
		"255.255.255.255",
		"::1",
		"fe80::1",
	}

	for _, ip := range seedIPs {
		f.Add(ip)
	}

	f.Fuzz(func(t *testing.T, ipStr string) {
		ip := net.ParseIP(ipStr)
		// Should never panic even with nil
		result := IsInLocalSubnet(ip)

		if ip == nil {
			assert.False(t, result, "Nil IP should return false")
		}

		// Consistency check
		result2 := IsInLocalSubnet(ip)
		assert.Equal(t, result, result2, "Inconsistent results")
	})
}

// =============================================================================
// Fuzz Tests for Session Key Generation
// =============================================================================

// FuzzCreateSessionKey tests session key generation.
func FuzzCreateSessionKey(f *testing.F) {
	seedInputs := []string{
		"secret",
		"",
		"a",
		strings.Repeat("a", 10000),
		"secret\x00with\x00nulls",
		"unicode: 日本語",
		"special: !@#$%^&*()",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, seed string) {
		// Should never panic
		result := createSessionKey(seed)

		// Result must be exactly 32 bytes (SHA-256)
		assert.Len(t, result, 32, "Session key must be 32 bytes")

		// Result must be deterministic
		result2 := createSessionKey(seed)
		assert.Equal(t, result, result2, "Session key must be deterministic")

		// Different seeds should (usually) produce different keys
		// This is probabilistic but with SHA-256 collisions are extremely rare
		if seed != "" {
			differentSeed := seed + "x"
			differentResult := createSessionKey(differentSeed)
			assert.NotEqual(t, result, differentResult, "Different seeds should produce different keys")
		}
	})
}

// =============================================================================
// Property-Based Tests for Security Invariants
// =============================================================================

// TestSecurityInvariantsWithMalformedInput tests that security functions
// handle malformed inputs without panicking or exposing vulnerabilities.
func TestSecurityInvariantsWithMalformedInput(t *testing.T) {
	t.Parallel()

	// Collection of potentially problematic inputs
	malformedInputs := []string{
		// Null bytes
		"\x00",
		"test\x00test",
		string([]byte{0x00, 0x01, 0x02}),
		// Unicode edge cases
		"\uFEFF",                   // BOM
		"\u202E",                   // RTL override
		"\u0000",                   // Null
		"\uFFFF",                   // Invalid
		string([]byte{0xC0, 0xAF}), // Overlong encoding
		// Control characters
		"\x01\x02\x03\x04\x05",
		"\x1B[31m", // ANSI escape
		// Very long inputs
		strings.Repeat("a", 1<<16),
		strings.Repeat("/", 1<<16),
		// Repeated special patterns
		strings.Repeat("..", 1000),
		strings.Repeat("%00", 1000),
		strings.Repeat("\r\n", 1000),
	}

	for _, input := range malformedInputs {
		// Test IsSafePath
		t.Run("IsSafePath", func(t *testing.T) {
			// Should not panic
			result := IsSafePath(input)
			// Most malformed inputs should be rejected
			//nolint:gocritic // Explicit check mirrors IsSafePath logic (filepath.IsLocal not suitable here)
			if strings.Contains(input, "\x00") || strings.Contains(input, "..") {
				assert.False(t, result, "Should reject input with null/traversal")
			}
		})

		// Test containsCRLF
		t.Run("containsCRLF", func(t *testing.T) {
			// Should not panic
			_ = containsCRLF(input)
		})

		// Test ValidateAuthCallbackRedirect
		t.Run("ValidateAuthCallbackRedirect", func(t *testing.T) {
			// Should not panic and should return safe default
			result := ValidateAuthCallbackRedirect(input)
			assert.True(t, strings.HasPrefix(result, "/"), "Should return path starting with /")
		})

		// Test isValidUserId
		t.Run("isValidUserId", func(t *testing.T) {
			// Should not panic
			_ = isValidUserId(input, "test")
			_ = isValidUserId("test", input)
			_ = isValidUserId(input, input)
		})
	}
}

// TestUnicodeSecurityBypass tests that unicode characters don't bypass security.
func TestUnicodeSecurityBypass(t *testing.T) {
	t.Parallel()

	// Homograph attacks - characters that look like ASCII but aren't
	homographs := []struct {
		name  string
		input string
	}{
		{"Greek omicron for o", "/lοcalhost"},  // ο = Greek omicron
		{"Cyrillic a for a", "/locаlhost"},     // а = Cyrillic a
		{"Full-width slash", "/path／test"},     // ／ = full-width slash
		{"Fraction slash", "/path⁄test"},       // ⁄ = fraction slash
		{"Division slash", "/path∕test"},       // ∕ = division slash
		{"Combining dot", "/path\u0307\u0307"}, // combining dots
	}

	for _, tc := range homographs {
		t.Run(tc.name, func(t *testing.T) {
			// These should be handled consistently
			result := IsSafePath(tc.input)
			// Note: We're not asserting true/false, just that it doesn't panic
			// and behaves consistently
			result2 := IsSafePath(tc.input)
			assert.Equal(t, result, result2, "Inconsistent handling of %s", tc.name)
		})
	}
}

// TestInvalidUTF8Handling tests that invalid UTF-8 sequences don't cause issues.
func TestInvalidUTF8Handling(t *testing.T) {
	t.Parallel()

	invalidUTF8 := [][]byte{
		{0xC0, 0xAF},             // Overlong encoding of '/'
		{0xC0, 0xAE},             // Overlong encoding of '.'
		{0xE0, 0x80, 0xAF},       // Overlong encoding
		{0xF0, 0x80, 0x80, 0xAF}, // Overlong encoding
		{0xFE, 0xFE, 0xFF, 0xFF}, // Invalid sequence
		{0x80, 0x81, 0x82},       // Continuation bytes without start
	}

	for i, invalid := range invalidUTF8 {
		input := "/" + string(invalid)
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			// Verify it's actually invalid UTF-8
			assert.False(t, utf8.ValidString(input), "Test input should be invalid UTF-8")

			// Functions should handle without panic
			_ = IsSafePath(input)
			_ = containsCRLF(input)
			_ = ValidateAuthCallbackRedirect(input)
			_ = isValidUserId(input, "test")
			_ = createSessionKey(input)
		})
	}
}

// =============================================================================
// Extended Edge Case Tests
// =============================================================================

// TestAdvancedPathTraversalAttacks tests sophisticated path traversal attempts.
func TestAdvancedPathTraversalAttacks(t *testing.T) {
	t.Parallel()

	attacks := []struct {
		name     string
		path     string
		expected bool // should IsSafePath return true?
	}{
		// Windows-style paths
		{"Windows absolute path", "/C:\\Windows\\System32", false},
		{"Windows UNC path", "/\\\\server\\share", false},
		{"Mixed slashes Windows", "/path\\..\\..\\etc", false},

		// Null byte injection (bypass extension checks)
		{"Null byte with extension", "/file.txt\x00.jpg", false},
		{"Encoded null with extension", "/file.txt%00.jpg", false},

		// Double encoding attacks
		{"Double encoded dot", "/path/%252e%252e/etc", false},
		{"Double encoded slash", "/path%252f%252fetc", false},

		// Triple encoding
		{"Triple encoded traversal", "/path/%25252e%25252e/etc", false},

		// Unicode normalization attacks - NFKC normalizes these to ASCII equivalents
		{"Unicode dot (fullwidth)", "/path\uff0e\uff0e/etc", false}, // ．．→ .. after NFKC
		{"Unicode slash (fullwidth)", "/path\uff0fetc", true},       // ／ → / after NFKC, becomes /path/etc (valid)
		{"Unicode double slash", "/\uff0f/evil.com", false},         // ／/ → // after NFKC (protocol-relative)
		{"Unicode traversal", "/path/\uff0e\uff0e/etc", false},      // ．．→ .. after NFKC

		// Case variation attacks
		{"Mixed case encoded", "/path/%2E%2e/etc", false},
		{"Uppercase path traversal", "/PATH/../etc", false},

		// Whitespace injection
		{"Tab in path", "/path\t/file", true}, // tabs aren't dangerous per se
		{"Space padding", "/ /etc/passwd", true},

		// URL fragment/query manipulation - IsSafePath checks raw string, so ".." anywhere is rejected
		{"Hash in path", "/path#fragment/../etc", false}, // ".." found in raw string
		{"Query traversal", "/path?q=/../etc", false},    // ".." found in raw string

		// Overlong paths with patterns
		{"Long path with traversal", "/" + strings.Repeat("a/", 100) + "../etc", false},
		{"Pattern at end", "/valid/path/here/..", false},
		{"Pattern at start", "/../valid/path", false},
	}

	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := IsSafePath(tc.path)
			assert.Equal(t, tc.expected, result, "Path: %q", tc.path)
		})
	}
}

// TestAdvancedCRLFInjectionAttacks tests sophisticated CRLF injection attempts.
func TestAdvancedCRLFInjectionAttacks(t *testing.T) {
	t.Parallel()

	attacks := []struct {
		name     string
		input    string
		expected bool // should containsCRLF return true?
	}{
		// Unicode line terminators
		{"Unicode line separator", "test\u2028test", false},      // Line Separator
		{"Unicode paragraph separator", "test\u2029test", false}, // Paragraph Separator
		{"Unicode next line", "test\u0085test", false},           // Next Line (NEL)
		{"Vertical tab", "test\x0Btest", false},                  // VT - not a line ending
		{"Form feed", "test\x0Ctest", false},                     // FF - not a line ending

		// Various CRLF combinations
		{"LF only", "test\ntest", true},
		{"CR only", "test\rtest", true},
		{"CRLF", "test\r\ntest", true},
		{"LFCR (reversed)", "test\n\rtest", true},
		{"Multiple CRLF", "test\r\n\r\ntest", true},

		// Encoded in different positions
		{"Encoded at start", "%0d%0atest", true},
		{"Encoded at end", "test%0d%0a", true},
		{"Encoded in middle", "te%0d%0ast", true},

		// Mixed encoding levels
		{"Single + double encoded", "%0d%250a", true},
		{"Double + single encoded", "%250d%0a", true},

		// Header injection patterns
		{"Set-Cookie injection", "value%0d%0aSet-Cookie: evil=true", true},
		{"Location injection", "value%0d%0aLocation: http://evil.com", true},
		{"Content-Type injection", "value%0d%0aContent-Type: text/html", true},

		// Partial patterns (should NOT match)
		{"Just percent-0", "test%0test", false},
		{"Just percent-2", "test%2test", false},
		{"Number 0d0a literally", "test0d0atest", false},
	}

	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := containsCRLF(tc.input)
			assert.Equal(t, tc.expected, result, "Input: %q", tc.input)
		})
	}
}

// TestAdvancedRedirectAttacks tests sophisticated open redirect attempts.
func TestAdvancedRedirectAttacks(t *testing.T) {
	t.Parallel()

	attacks := []struct {
		name           string
		redirect       string
		expectedPrefix string // what the result should start with
		shouldBeRoot   bool   // should result be exactly "/"
	}{
		// Protocol-relative variations
		{"Double slash", "//evil.com", "/", true},
		{"Triple slash", "///evil.com", "/", true},
		{"Backslash protocol-relative", "/\\evil.com", "/", true},
		{"Mixed slashes", "/\\/evil.com", "/", true},
		{"Encoded double slash", "/%2f/evil.com", "/", true},

		// Scheme attacks
		{"JavaScript scheme", "javascript:alert(1)", "/", true},
		{"Data scheme", "data:text/html,<script>alert(1)</script>", "/", true},
		{"VBScript scheme", "vbscript:msgbox(1)", "/", true},
		{"File scheme", "file:///etc/passwd", "/", true},
		{"FTP scheme", "ftp://evil.com/file", "/", true},

		// Domain confusion
		{"Domain with @", "//@evil.com", "/", true},
		{"Userinfo attack", "//user:pass@evil.com/path", "/", true},
		{"Port confusion", "//evil.com:80/path", "/", true},

		// Encoded schemes
		{"Encoded http", "http%3a//evil.com", "/", true},
		{"Encoded colon", "http%3A//evil.com", "/", true},

		// Whitespace attacks
		{"Tab before scheme", "\thttp://evil.com", "/", true},
		{"Newline before scheme", "\nhttp://evil.com", "/", true},
		{"Space in scheme", "java script:alert(1)", "/", true},

		// Valid redirects that should pass
		{"Simple path", "/dashboard", "/dashboard", false},
		{"Path with query", "/path?param=value", "/path?param=value", false},
		{"Nested path", "/admin/users/list", "/admin/users/list", false},
		{"Root path", "/", "/", true},
	}

	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateAuthCallbackRedirect(tc.redirect)

			if tc.shouldBeRoot {
				assert.Equal(t, "/", result, "Should be root for: %q", tc.redirect)
			} else {
				assert.True(t, strings.HasPrefix(result, tc.expectedPrefix),
					"Result %q should start with %q for input %q", result, tc.expectedPrefix, tc.redirect)
			}

			// All results must be safe relative paths
			assert.True(t, strings.HasPrefix(result, "/"), "Must start with /")
			if len(result) > 1 {
				assert.NotEqual(t, '/', result[1], "Must not be protocol-relative")
			}
		})
	}
}

// TestAdvancedIPAddressEdgeCases tests IP address parsing edge cases.
func TestAdvancedIPAddressEdgeCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ip      string
		isValid bool // should net.ParseIP return non-nil?
		isIPv4  bool // if valid, is it IPv4?
	}{
		// Standard formats
		{"Standard IPv4", "192.168.1.1", true, true},
		{"Standard IPv6", "2001:db8::1", true, false},
		{"IPv6 loopback", "::1", true, false},
		{"IPv4 loopback", "127.0.0.1", true, true},

		// IPv4-mapped IPv6
		{"IPv4-mapped IPv6", "::ffff:192.168.1.1", true, true}, // To4() works
		{"IPv4-compatible IPv6", "::192.168.1.1", true, false}, // Go parses as IPv6, To4() returns nil

		// Edge values
		{"All zeros IPv4", "0.0.0.0", true, true},
		{"All ones IPv4", "255.255.255.255", true, true},
		{"Max IPv6", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", true, false},

		// Invalid formats
		{"Octal IPv4", "0300.0250.01.01", false, false},   // Go doesn't support octal
		{"Hex IPv4", "0xC0.0xA8.0x01.0x01", false, false}, // Go doesn't support hex
		{"Overflow IPv4", "256.256.256.256", false, false},
		{"Negative IPv4", "-1.0.0.0", false, false},
		{"Too many octets", "1.2.3.4.5", false, false},
		{"Too few octets", "1.2.3", false, false},
		{"Empty string", "", false, false},
		{"Just dots", "...", false, false},
		{"Letters in IPv4", "192.168.a.1", false, false},

		// Unusual but valid
		{"IPv6 with zone", "fe80::1%eth0", false, false}, // ParseIP doesn't handle zones
		{"Full IPv6", "2001:0db8:0000:0000:0000:0000:0000:0001", true, false},
		{"Compressed IPv6", "2001:db8::1", true, false},

		// Potential injection
		{"Space in IP", "192.168.1.1 ", false, false},
		{"Null in IP", "192.168.1.1\x00", false, false},
		{"Newline in IP", "192.168.1.1\n", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(tc.ip)

			if tc.isValid {
				assert.NotNil(t, ip, "Should parse: %q", tc.ip)
				if tc.isIPv4 {
					assert.NotNil(t, ip.To4(), "Should be IPv4: %q", tc.ip)
				}
			} else {
				assert.Nil(t, ip, "Should not parse: %q", tc.ip)
			}

			// Test subnet extraction doesn't panic
			subnet := getIPv4Subnet(ip)
			if ip != nil && ip.To4() != nil {
				assert.NotNil(t, subnet, "IPv4 should have subnet")
			}

			// Test IsInLocalSubnet doesn't panic
			_ = IsInLocalSubnet(ip)
		})
	}
}

// TestCIDRParsingEdgeCases tests CIDR notation parsing edge cases.
func TestCIDRParsingEdgeCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cidr    string
		isValid bool
	}{
		// Valid CIDRs
		{"Standard /24", "192.168.1.0/24", true},
		{"Host /32", "192.168.1.1/32", true},
		{"Network /0", "0.0.0.0/0", true},
		{"IPv6 /64", "2001:db8::/64", true},
		{"IPv6 /128", "::1/128", true},

		// Invalid CIDRs
		{"Missing prefix", "192.168.1.0", false},
		{"Negative prefix", "192.168.1.0/-1", false},
		{"Too large prefix IPv4", "192.168.1.0/33", false},
		{"Too large prefix IPv6", "::1/129", false},
		{"Invalid IP in CIDR", "256.0.0.0/24", false},
		{"Empty string", "", false},
		{"Just slash", "/24", false},
		{"Double slash", "192.168.1.0//24", false},
		{"Letters in prefix", "192.168.1.0/ab", false},
		{"Decimal prefix", "192.168.1.0/24.5", false},
		{"Negative prefix2", "192.168.1.0/-8", false},

		// Whitespace
		{"Leading space", " 192.168.1.0/24", false},
		{"Trailing space", "192.168.1.0/24 ", false},
		{"Space in middle", "192.168.1.0 /24", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := net.ParseCIDR(tc.cidr)

			if tc.isValid {
				assert.NoError(t, err, "Should parse: %q", tc.cidr)
			} else {
				assert.Error(t, err, "Should not parse: %q", tc.cidr)
			}
		})
	}
}

// TestUserIDValidationEdgeCases tests user ID validation edge cases.
func TestUserIDValidationEdgeCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		configured string
		provided   string
		expected   bool
	}{
		// Basic matching
		{"Exact match", "user@example.com", "user@example.com", true},
		{"Case insensitive", "USER@EXAMPLE.COM", "user@example.com", true},
		{"Mixed case", "User@Example.Com", "USER@example.COM", true},

		// Whitespace handling
		{"Configured with spaces", "  user@example.com  ", "user@example.com", true},
		{"Provided with spaces", "user@example.com", "  user@example.com  ", true},
		{"Both with spaces", "  user@example.com  ", "  user@example.com  ", true},
		{"Only whitespace configured", "   ", "user@example.com", false},
		{"Only whitespace provided", "user@example.com", "   ", false},
		{"Tab characters", "\tuser@example.com\t", "user@example.com", true},

		// Multiple IDs
		{"First in list", "user1,user2,user3", "user1", true},
		{"Middle in list", "user1,user2,user3", "user2", true},
		{"Last in list", "user1,user2,user3", "user3", true},
		{"Not in list", "user1,user2,user3", "user4", false},
		{"Empty entries in list", "user1,,user2", "user2", true},
		{"Only commas", ",,,", "user", false},
		{"Trailing comma", "user1,user2,", "user2", true},
		{"Leading comma", ",user1,user2", "user1", true},

		// Empty and null
		{"Empty configured", "", "user@example.com", false},
		{"Empty provided", "user@example.com", "", false},
		{"Both empty", "", "", false},

		// Special characters
		{"Plus addressing", "user+tag@example.com", "user+tag@example.com", true},
		{"Dots in local part", "user.name@example.com", "user.name@example.com", true},
		{"Subdomain", "user@sub.example.com", "user@sub.example.com", true},

		// Unicode
		{"Unicode email", "пользователь@example.com", "пользователь@example.com", true},
		{"Unicode case", "ПОЛЬЗОВАТЕЛЬ@example.com", "пользователь@example.com", true},

		// Injection attempts
		{"SQL injection", "user@example.com", "user@example.com'; DROP TABLE users;--", false},
		{"LDAP injection", "user@example.com", "user@example.com)(uid=*", false},
		{"Null byte", "user@example.com", "user@example.com\x00admin", false},
		{"Newline", "user@example.com", "user@example.com\nadmin", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := isValidUserId(tc.configured, tc.provided)
			assert.Equal(t, tc.expected, result,
				"configured=%q, provided=%q", tc.configured, tc.provided)
		})
	}
}

// TestResourceExhaustion tests that functions handle resource exhaustion attempts.
func TestResourceExhaustion(t *testing.T) {
	t.Parallel()

	// Very long inputs
	longInputs := []struct {
		name   string
		length int
	}{
		{"1KB", 1024},
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
		{"1MB", 1024 * 1024},
	}

	for _, tc := range longInputs {
		t.Run("LongInput_"+tc.name, func(t *testing.T) {
			t.Parallel()
			input := strings.Repeat("a", tc.length)

			// Should complete without hanging or panicking
			start := time.Now()
			_ = IsSafePath("/" + input)
			_ = containsCRLF(input)
			_ = ValidateAuthCallbackRedirect("/" + input)
			_ = isValidUserId(input, input)
			_ = createSessionKey(input)
			elapsed := time.Since(start)

			// Should complete in reasonable time (< 1 second)
			assert.Less(t, elapsed, time.Second, "Operations took too long for %s input", tc.name)
		})
	}

	// Repeated pattern inputs
	t.Run("RepeatedPatterns", func(t *testing.T) {
		t.Parallel()
		patterns := []string{
			strings.Repeat("../", 10000),
			strings.Repeat("%2e%2e/", 10000),
			strings.Repeat("\r\n", 10000),
			strings.Repeat("%0d%0a", 10000),
			strings.Repeat("//", 10000),
		}

		for i, pattern := range patterns {
			start := time.Now()
			_ = IsSafePath("/" + pattern)
			_ = containsCRLF(pattern)
			_ = ValidateAuthCallbackRedirect("/" + pattern)
			elapsed := time.Since(start)

			assert.Less(t, elapsed, time.Second, "Pattern %d took too long", i)
		}
	})

	// Many comma-separated IDs
	t.Run("ManyUserIDs", func(t *testing.T) {
		t.Parallel()
		ids := make([]string, 10000)
		for i := range ids {
			ids[i] = "user" + string(rune('0'+i%10)) + "@example.com"
		}
		configuredIDs := strings.Join(ids, ",")

		start := time.Now()
		_ = isValidUserId(configuredIDs, "user0@example.com")
		_ = isValidUserId(configuredIDs, "nonexistent@example.com")
		elapsed := time.Since(start)

		assert.Less(t, elapsed, time.Second, "User ID validation with many IDs took too long")
	})
}
