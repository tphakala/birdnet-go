package myaudio

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractErrorContext_ConnectionTimeout(t *testing.T) {
	stderrOutput := `[tcp @ 0x556404ebeb40] Starting connection attempt to 192.168.44.3 port 8554
[tcp @ 0x556404ebeb40] Connection attempt to 192.168.44.3 port 8554 failed: Connection timed out
[tcp @ 0x556404ebeb40] Connection to tcp://192.168.44.3:8554?timeout=15000000 failed: Connection timed out
Error opening input files: Connection timed out`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "connection_timeout", ctx.ErrorType)
	assert.Equal(t, "192.168.44.3", ctx.TargetHost)
	assert.Equal(t, 8554, ctx.TargetPort)
	assert.Equal(t, 15*time.Second, ctx.TimeoutDuration)
	assert.True(t, ctx.ShouldRestart(), "Connection timeout should trigger restart")
	assert.False(t, ctx.ShouldOpenCircuit(), "Connection timeout should not open circuit breaker")
	assert.Contains(t, ctx.UserFacingMsg, "Connection to 192.168.44.3:8554 timed out")
	assert.NotEmpty(t, ctx.TroubleShooting, "Expected troubleshooting steps")
}

func TestExtractErrorContext_RTSP404(t *testing.T) {
	stderrOutput := `[rtsp @ 0x55cc5f603980] method DESCRIBE failed: 404 Not Found
[in#0 @ 0x55cc5f603640] Error opening input: Server returned 404 Not Found
Error opening input file rtsp://localhost:8554/mystream.
Error opening input files: Server returned 404 Not Found`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "rtsp_404", ctx.ErrorType)
	assert.Equal(t, 404, ctx.HTTPStatus)
	assert.Equal(t, "DESCRIBE", ctx.RTSPMethod)
	assert.True(t, ctx.ShouldOpenCircuit(), "RTSP 404 should open circuit breaker")
	assert.False(t, ctx.ShouldRestart(), "RTSP 404 should not trigger restart (permanent failure)")
	assert.Contains(t, ctx.UserFacingMsg, "RTSP stream not found (404)")
}

func TestExtractErrorContext_ConnectionRefused(t *testing.T) {
	stderrOutput := `[tcp @ 0x5583d3a7c680] Connection to tcp://localhost:8553?timeout=0 failed: Connection refused
[in#0 @ 0x5583d3a79640] Error opening input: Connection refused
Error opening input file rtsp://localhost:8553/stream.`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "connection_refused", ctx.ErrorType)
	assert.Equal(t, "localhost", ctx.TargetHost)
	assert.Equal(t, 8553, ctx.TargetPort)
	assert.True(t, ctx.ShouldOpenCircuit(), "Connection refused should open circuit breaker")
	assert.False(t, ctx.ShouldRestart(), "Connection refused should not trigger restart (permanent failure)")
}

func TestExtractErrorContext_AuthFailure(t *testing.T) {
	stderrOutput := `[rtsp @ 0x...] method DESCRIBE failed: 401 Unauthorized
[in#0 @ 0x...] Error opening input: Server returned 401 Unauthorized
Error opening input file rtsp://camera.example.com/stream.`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "auth_failed", ctx.ErrorType)
	assert.Equal(t, 401, ctx.HTTPStatus)
	assert.True(t, ctx.ShouldOpenCircuit(), "Auth failure should open circuit breaker")
	assert.Contains(t, ctx.UserFacingMsg, "Authentication required")
}

func TestExtractErrorContext_NoError(t *testing.T) {
	// Test with normal FFmpeg output (no errors)
	stderrOutput := `Input #0, rtsp, from 'rtsp://localhost:8554/test':
  Duration: N/A, start: 0.000000, bitrate: N/A
    Stream #0:0: Video: h264 (Main), yuv420p(progressive), 640x480
    Stream #0:1: Audio: aac (LC), 48000 Hz, mono
Output #0, s16le, to 'pipe:':
    Stream #0:0: Audio: pcm_s16le, 48000 Hz, mono`

	ctx := ExtractErrorContext(stderrOutput)

	assert.Nil(t, ctx, "Expected no error context for normal output")
}

func TestExtractErrorContext_EmptyString(t *testing.T) {
	ctx := ExtractErrorContext("")

	assert.Nil(t, ctx, "Expected nil for empty string")
}

func TestFormatForConsole(t *testing.T) {
	ctx := &ErrorContext{
		ErrorType:      "connection_timeout",
		PrimaryMessage: "Connection timed out",
		TargetHost:     "192.168.1.1",
		TargetPort:     8554,
		UserFacingMsg:  "🔌 Test error message\n   Additional context line",
		TroubleShooting: []string{
			"Step 1",
			"Step 2",
			"Step 3",
		},
	}

	output := ctx.FormatForConsole()

	// Test 1: User message is present
	assert.Contains(t, output, "Test error message", "Console output missing user message")

	// Test 2: All troubleshooting steps are present
	assert.Contains(t, output, "Step 1", "Console output missing Step 1")
	assert.Contains(t, output, "Step 2", "Console output missing Step 2")
	assert.Contains(t, output, "Step 3", "Console output missing Step 3")

	// Test 3: Bullet points are used for formatting
	bulletCount := strings.Count(output, "•")
	assert.Equal(t, len(ctx.TroubleShooting), bulletCount, "Bullet point count mismatch")

	// Test 4: Troubleshooting section header is present
	assert.Contains(t, output, "Troubleshooting steps:", "Console output missing 'Troubleshooting steps:' header")

	// Test 5: Output structure - message comes before troubleshooting
	msgIndex := strings.Index(output, "Test error message")
	troubleshootingIndex := strings.Index(output, "Troubleshooting steps:")
	assert.NotEqual(t, -1, msgIndex, "User message not found in output")
	assert.NotEqual(t, -1, troubleshootingIndex, "Troubleshooting header not found in output")
	assert.Less(t, msgIndex, troubleshootingIndex, "User message should come before troubleshooting")

	// Test 6: Each troubleshooting step is on its own line with proper indentation
	for _, step := range ctx.TroubleShooting {
		expectedLine := fmt.Sprintf("   • %s", step)
		assert.Contains(t, output, expectedLine, "Console output missing properly formatted step")
	}

	// Test 7: Emoji support (optional but nice to verify)
	assert.Contains(t, output, "🔌", "Console output missing emoji from user message")

	// Test 8: Newline formatting is preserved
	lines := strings.Split(output, "\n")
	assert.GreaterOrEqual(t, len(lines), 4, "Expected at least 4 lines in output")
}

// TestFormatForConsole_NoTroubleshooting tests formatting when no troubleshooting steps are provided
func TestFormatForConsole_NoTroubleshooting(t *testing.T) {
	ctx := &ErrorContext{
		ErrorType:       "unknown",
		PrimaryMessage:  "Unknown error",
		UserFacingMsg:   "An unknown error occurred",
		TroubleShooting: nil, // No troubleshooting steps
	}

	output := ctx.FormatForConsole()

	// Should still contain the user message
	assert.Contains(t, output, "An unknown error occurred", "Console output missing user message")

	// Should not contain troubleshooting header when there are no steps
	assert.NotContains(t, output, "Troubleshooting steps:", "Console output should not have troubleshooting header when no steps provided")

	// Should not contain bullet points
	assert.NotContains(t, output, "•", "Console output should not have bullet points when no steps provided")
}

func TestErrorContext_ShouldOpenCircuit(t *testing.T) {
	tests := []struct {
		errorType      string
		shouldOpen     bool
		description    string
	}{
		{"rtsp_404", true, "404 errors should open circuit"},
		{"auth_failed", true, "Auth failures should open circuit"},
		{"auth_forbidden", true, "403 errors should open circuit"},
		{"connection_refused", true, "Connection refused should open circuit"},
		{"no_route", true, "No route errors should open circuit"},
		{"protocol_error", true, "Protocol errors should open circuit"},
		{"dns_resolution_failed", true, "DNS errors should open circuit"},
		{"operation_not_permitted", true, "Operation not permitted should open circuit"},
		{"ssl_error", true, "SSL errors should open circuit"},
		{"connection_timeout", false, "Connection timeout should not open circuit (transient)"},
		{"invalid_data", false, "Invalid data should not open circuit (transient)"},
		{"eof", false, "EOF should not open circuit (transient)"},
		{"network_unreachable", false, "Network unreachable should not open circuit (transient)"},
		{"rtsp_503", false, "503 errors should not open circuit (transient)"},
		{"unknown", false, "Unknown errors should not open circuit"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctx := &ErrorContext{ErrorType: tt.errorType}
			assert.Equal(t, tt.shouldOpen, ctx.ShouldOpenCircuit(), tt.description)
		})
	}
}

func TestErrorContext_ShouldRestart(t *testing.T) {
	tests := []struct {
		errorType       string
		shouldRestart   bool
		description     string
	}{
		{"connection_timeout", true, "Timeout should trigger restart (transient)"},
		{"invalid_data", true, "Invalid data should trigger restart (transient)"},
		{"eof", true, "EOF should trigger restart (transient)"},
		{"network_unreachable", true, "Network unreachable should trigger restart (transient)"},
		{"rtsp_503", true, "503 should trigger restart (transient)"},
		{"rtsp_404", false, "404 should not restart (permanent)"},
		{"auth_failed", false, "Auth failure should not restart (permanent)"},
		{"connection_refused", false, "Connection refused should not restart (permanent)"},
		{"dns_resolution_failed", false, "DNS errors should not restart (permanent)"},
		{"operation_not_permitted", false, "Operation not permitted should not restart (permanent)"},
		{"ssl_error", false, "SSL errors should not restart (permanent)"},
		{"unknown", false, "Unknown errors should not restart"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctx := &ErrorContext{ErrorType: tt.errorType}
			assert.Equal(t, tt.shouldRestart, ctx.ShouldRestart(), tt.description)
		})
	}
}

func TestExtractErrorContext_DNSResolutionFailed(t *testing.T) {
	stderrOutput := `[tcp @ 0x563f02f73b40] No default whitelist set
[tcp @ 0x563f02f73b40] Failed to resolve hostname sfsdfds: Name or service not known
[in#0 @ 0x563f02f706c0] Error opening input: Input/output error
Error opening input file rtsp://sfsdfds:8554/mystreama.
Error opening input files: Input/output error`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "dns_resolution_failed", ctx.ErrorType)
	assert.Equal(t, "sfsdfds", ctx.TargetHost)
	assert.True(t, ctx.ShouldOpenCircuit(), "DNS resolution failure should open circuit breaker")
	assert.False(t, ctx.ShouldRestart(), "DNS resolution failure should not trigger restart (permanent failure)")
	assert.Contains(t, ctx.UserFacingMsg, "DNS resolution failed")
	assert.Contains(t, ctx.UserFacingMsg, "sfsdfds", "User-facing message should contain hostname")
}

func TestExtractErrorContext_NetworkUnreachable(t *testing.T) {
	stderrOutput := `[tcp @ 0x...] Connection to tcp://192.168.1.100:8554?timeout=0 failed: Network unreachable
[in#0 @ 0x...] Error opening input: Network unreachable
Error opening input file rtsp://192.168.1.100:8554/stream.`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "network_unreachable", ctx.ErrorType)
	assert.Equal(t, "192.168.1.100", ctx.TargetHost)
	assert.False(t, ctx.ShouldOpenCircuit(), "Network unreachable should not open circuit breaker (transient)")
	assert.True(t, ctx.ShouldRestart(), "Network unreachable should trigger restart (transient failure)")
	assert.Contains(t, ctx.UserFacingMsg, "Network unreachable")
}

func TestExtractErrorContext_OperationNotPermitted(t *testing.T) {
	stderrOutput := `[tcp @ 0x...] Connection to tcp://camera.local:554?timeout=0 failed: Operation not permitted
[in#0 @ 0x...] Error opening input: Operation not permitted
Error opening input file rtsp://camera.local:554/stream.`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "operation_not_permitted", ctx.ErrorType)
	assert.True(t, ctx.ShouldOpenCircuit(), "Operation not permitted should open circuit breaker")
	assert.False(t, ctx.ShouldRestart(), "Operation not permitted should not trigger restart (permanent failure)")
	assert.Contains(t, ctx.UserFacingMsg, "Operation not permitted")
}

func TestExtractErrorContext_SSLError(t *testing.T) {
	stderrOutput := `[tls @ 0x...] SSL connection error
[in#0 @ 0x...] Error opening input: SSL error
Error opening input file rtsps://secure-camera.example.com:8322/stream.
Error opening input files: SSL error`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "ssl_error", ctx.ErrorType)
	assert.True(t, ctx.ShouldOpenCircuit(), "SSL error should open circuit breaker")
	assert.False(t, ctx.ShouldRestart(), "SSL error should not trigger restart (permanent failure)")
	assert.Contains(t, ctx.UserFacingMsg, "SSL/TLS")
}

func TestExtractErrorContext_RTSP503(t *testing.T) {
	stderrOutput := `[rtsp @ 0x...] method DESCRIBE failed: 503 Service Unavailable
[in#0 @ 0x...] Error opening input: Server returned 503 Service Unavailable
Error opening input file rtsp://overloaded.example.com:8554/stream.
Error opening input files: Server returned 503 Service Unavailable`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "rtsp_503", ctx.ErrorType)
	assert.Equal(t, 503, ctx.HTTPStatus)
	assert.False(t, ctx.ShouldOpenCircuit(), "RTSP 503 should not open circuit breaker (transient)")
	assert.True(t, ctx.ShouldRestart(), "RTSP 503 should trigger restart (transient failure)")
	assert.Contains(t, ctx.UserFacingMsg, "service unavailable")
}

// TestExtractErrorContext_OverlappingPatterns tests error precedence when multiple patterns match
func TestExtractErrorContext_OverlappingPatterns(t *testing.T) {
	tests := []struct {
		name           string
		stderrOutput   string
		expectedType   string
		explanation    string
	}{
		{
			name: "Timeout takes precedence over Network unreachable",
			stderrOutput: `[tcp @ 0x...] Connection attempt to 192.168.1.1 port 8554 failed: Connection timed out
[tcp @ 0x...] Network unreachable
Error opening input files: Connection timed out`,
			expectedType: "connection_timeout",
			explanation:  "Connection timeout is more specific than network unreachable",
		},
		{
			name: "No route to host takes precedence over Network unreachable",
			stderrOutput: `[tcp @ 0x...] Connection to tcp://192.168.1.1:8554 failed: No route to host
[tcp @ 0x...] Network unreachable
Error opening input files: No route to host`,
			expectedType: "no_route",
			explanation:  "EHOSTUNREACH (no route) is more specific than ENETUNREACH (network unreachable)",
		},
		{
			name: "RTSP 404 takes precedence over Connection refused",
			stderrOutput: `[rtsp @ 0x...] method DESCRIBE failed: 404 Not Found
[tcp @ 0x...] Connection refused
Error opening input file rtsp://localhost:8554/stream.`,
			expectedType: "rtsp_404",
			explanation:  "Application-layer RTSP error is more diagnostic than socket-layer connection refused",
		},
		{
			name: "Connection timeout beats DNS error",
			stderrOutput: `[tcp @ 0x...] Connection timed out
[tcp @ 0x...] Name or service not known
Error opening input files: Connection timed out`,
			expectedType: "connection_timeout",
			explanation:  "Socket-level timeout is checked before DNS errors",
		},
		{
			name: "Connection refused beats Invalid data",
			stderrOutput: `[tcp @ 0x...] Connection refused
Invalid data found when processing input
Error opening input file rtsp://localhost:8554/stream.`,
			expectedType: "connection_refused",
			explanation:  "Connection errors are more specific than data errors",
		},
		{
			name: "Auth 401 beats Protocol error",
			stderrOutput: `[rtsp @ 0x...] method DESCRIBE failed: 401 Unauthorized
Protocol not found
Error opening input file rtsp://camera.example.com/stream.`,
			expectedType: "auth_failed",
			explanation:  "RTSP auth failure is more specific than generic protocol error",
		},
		{
			name: "SSL error beats EOF",
			stderrOutput: `SSL connection error
End of file
Error opening input file rtsps://secure.example.com/stream.`,
			expectedType: "ssl_error",
			explanation:  "Security/permission errors take precedence over data errors",
		},
		{
			name: "DNS error beats Protocol error",
			stderrOutput: `Name or service not known
Protocol not found
Error opening input files: Input/output error`,
			expectedType: "dns_resolution_failed",
			explanation:  "DNS errors are more specific than generic protocol errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := ExtractErrorContext(tt.stderrOutput)

			require.NotNil(t, ctx, "Expected error context, got nil")
			assert.Equal(t, tt.expectedType, ctx.ErrorType,
				"Explanation: %s\nStderr: %s", tt.explanation, tt.stderrOutput)
		})
	}
}

// TestExtractErrorContext_NetworkErrorPrecedence specifically tests network error ordering
func TestExtractErrorContext_NetworkErrorPrecedence(t *testing.T) {
	// Test: "No route to host" should win over "Network unreachable" when both appear
	stderrOutput := `[tcp @ 0x...] Starting connection attempt to 192.168.1.100 port 8554
[tcp @ 0x...] No route to host
[tcp @ 0x...] Network unreachable
Error opening input files: No route to host`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "no_route", ctx.ErrorType,
		"Rationale: EHOSTUNREACH (no route) indicates a specific routing table problem, "+
			"while ENETUNREACH (network unreachable) is a broader network configuration issue. "+
			"The more specific error provides better diagnostic value.")
}

func TestExtractErrorContext_ZeroTimeout(t *testing.T) {
	// Test case: timeout=0 means infinite timeout, but TCP stack gave up
	stderrOutput := `[tcp @ 0x556404ebeb40] Starting connection attempt to 192.168.44.3 port 8554
[tcp @ 0x556404ebeb40] Connection attempt to 192.168.44.3 port 8554 failed: Connection timed out
[tcp @ 0x556404ebeb40] Connection to tcp://192.168.44.3:8554?timeout=0 failed: Connection timed out
Error opening input files: Connection timed out`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "connection_timeout", ctx.ErrorType)
	assert.Equal(t, time.Duration(0), ctx.TimeoutDuration, "Zero timeout should be explicitly handled")
	assert.Contains(t, ctx.UserFacingMsg, "TCP stack timeout", "User message should mention TCP stack timeout for zero timeout")
}

// TestExtractErrorContext_CredentialSanitization tests that credentials are properly stripped
func TestExtractErrorContext_CredentialSanitization(t *testing.T) {
	tests := []struct {
		name           string
		stderrOutput   string
		expectedType   string
		checkHost      bool
		hostShouldNot  string // What the host should NOT contain
		explanation    string
	}{
		{
			name: "RTSP 404 with credentials in URL",
			stderrOutput: `[rtsp @ 0x...] method DESCRIBE failed: 404 Not Found
Error opening input file rtsp://admin:password123@camera.example.com:554/stream.
Error opening input files: Server returned 404 Not Found`,
			expectedType:  "rtsp_404",
			checkHost:     true,
			hostShouldNot: "admin",
			explanation:   "TargetHost should not contain username",
		},
		{
			name: "DNS error with credentials",
			stderrOutput: `Error opening input file rtsp://user:pass@badhost.local/stream
Name or service not known`,
			expectedType:  "dns_resolution_failed",
			checkHost:     true,
			hostShouldNot: "user:pass",
			explanation:   "TargetHost should not contain credentials",
		},
		{
			name: "SSL error with credentials",
			// NOTE: rtsps:// is currently NOT sanitized by privacy.SanitizeFFmpegError
			// because the regex only matches rtsp://. This is a known limitation.
			// Our extractHostWithoutCredentials() still protects TargetHost field.
			stderrOutput: `SSL connection error
Error opening input file rtsp://admin:secret@secure.camera.com/stream`,
			expectedType:  "ssl_error",
			checkHost:     true,
			hostShouldNot: "secret",
			explanation:   "TargetHost should not contain password",
		},
		{
			name: "RTSP 503 with complex password",
			stderrOutput: `[rtsp @ 0x...] method DESCRIBE failed: 503 Service Unavailable
Error opening input file rtsp://user:p@ssw0rd!@host.local:8554/live`,
			expectedType:  "rtsp_503",
			checkHost:     true,
			hostShouldNot: "p@ssw0rd",
			explanation:   "TargetHost should not contain password with special characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := ExtractErrorContext(tt.stderrOutput)

			require.NotNil(t, ctx, "Expected error context, got nil")
			assert.Equal(t, tt.expectedType, ctx.ErrorType)

			// Check that RawFFmpegOutput is sanitized
			assert.NotContains(t, ctx.RawFFmpegOutput, "password", "RawFFmpegOutput contains unsanitized 'password'")
			assert.NotContains(t, ctx.RawFFmpegOutput, "secret", "RawFFmpegOutput contains unsanitized 'secret'")
			assert.NotContains(t, ctx.RawFFmpegOutput, "p@ssw0rd", "RawFFmpegOutput contains unsanitized 'p@ssw0rd'")

			// Check that credentials should be replaced with ***
			if !strings.Contains(ctx.RawFFmpegOutput, "***") {
				t.Log("Note: RawFFmpegOutput should contain *** placeholders for credentials")
			}

			// Check TargetHost is clean
			if tt.checkHost {
				assert.NotContains(t, ctx.TargetHost, tt.hostShouldNot,
					"TargetHost contains credentials. Explanation: %s", tt.explanation)
				assert.NotContains(t, ctx.TargetHost, "@", "TargetHost contains @ symbol (likely has userinfo)")

				if strings.Contains(ctx.TargetHost, ":") {
					// TargetHost should only be the hostname, not host:port
					t.Logf("Warning: TargetHost may contain port: %s (TargetPort should be %d)",
						ctx.TargetHost, ctx.TargetPort)
				}
			}
		})
	}
}

// TestExtractHostWithoutCredentials directly tests the helper function
func TestExtractHostWithoutCredentials(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"rtsp://camera.local:554/stream", "camera.local"},
		{"rtsp://admin:pass@camera.local:554/stream", "camera.local"},
		{"rtsp://user:p@ssw0rd!@192.168.1.100:8554/live", "192.168.1.100"},
		{"rtsps://admin:secret@secure.cam.com/feed", "secure.cam.com"},
		{"admin:pass@camera.local:554", "camera.local"},           // Without scheme
		{"camera.local:554", "camera.local"},                      // Just host:port
		{"camera.local", "camera.local"},                          // Just host
		{"user:pass@host.local", "host.local"},                    // userinfo without port
		{"rtsp://[2001:db8::1]:554/stream", "2001:db8::1"},       // IPv6
		{"rtsp://user:pass@[2001:db8::1]:554/stream", "2001:db8::1"}, // IPv6 with credentials
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractHostWithoutCredentials(tt.input)
			assert.Equal(t, tt.expected, result,
				"extractHostWithoutCredentials(%q) returned unexpected value", tt.input)

			// Ensure no credentials leaked
			assert.NotContains(t, result, "admin", "Result contains 'admin'")
			assert.NotContains(t, result, "pass", "Result contains 'pass'")
			assert.NotContains(t, result, "secret", "Result contains 'secret'")
			assert.NotContains(t, result, "@", "Result contains @ symbol")
		})
	}
}

// TestExtractHostAndPortFromConnectionURL tests the new URL-based extraction
func TestExtractHostAndPortFromConnectionURL(t *testing.T) {
	tests := []struct {
		name         string
		connectionURL string
		expectedHost string
		expectedPort int
		shouldSucceed bool
	}{
		{
			name:          "Simple IPv4",
			connectionURL: "tcp://192.168.1.1:8554",
			expectedHost:  "192.168.1.1",
			expectedPort:  8554,
			shouldSucceed: true,
		},
		{
			name:          "IPv6 with brackets",
			connectionURL: "tcp://[2001:db8::1]:8554",
			expectedHost:  "2001:db8::1",
			expectedPort:  8554,
			shouldSucceed: true,
		},
		{
			name:          "IPv6 localhost",
			connectionURL: "tcp://[::1]:554",
			expectedHost:  "::1",
			expectedPort:  554,
			shouldSucceed: true,
		},
		{
			name:          "With query parameters",
			connectionURL: "tcp://camera.local:8554?timeout=10000000",
			expectedHost:  "camera.local",
			expectedPort:  8554,
			shouldSucceed: true,
		},
		{
			name:          "With credentials (should strip)",
			connectionURL: "tcp://user:pass@camera.local:8554",
			expectedHost:  "camera.local",
			expectedPort:  8554,
			shouldSucceed: true,
		},
		{
			name:          "IPv6 with credentials",
			connectionURL: "tcp://user:pass@[2001:db8::1]:8554?timeout=0",
			expectedHost:  "2001:db8::1",
			expectedPort:  8554,
			shouldSucceed: true,
		},
		{
			name:          "Hostname",
			connectionURL: "tcp://camera.example.com:554",
			expectedHost:  "camera.example.com",
			expectedPort:  554,
			shouldSucceed: true,
		},
		{
			name:          "Invalid - no port",
			connectionURL: "tcp://camera.local",
			shouldSucceed: false,
		},
		{
			name:          "Invalid - malformed",
			connectionURL: "not a url",
			shouldSucceed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, ok := extractHostAndPortFromConnectionURL(tt.connectionURL)

			assert.Equal(t, tt.shouldSucceed, ok, "Success flag mismatch")

			if !tt.shouldSucceed {
				return // Expected to fail, and it did
			}

			assert.Equal(t, tt.expectedHost, host, "Host mismatch")
			assert.Equal(t, tt.expectedPort, port, "Port mismatch")

			// Security check: ensure no credentials leaked
			assert.NotContains(t, host, "user", "Host contains 'user'")
			assert.NotContains(t, host, "pass", "Host contains 'pass'")
			assert.NotContains(t, host, "@", "Host contains @ symbol")
		})
	}
}

// TestConnectionRefused_IPv6 tests that IPv6 addresses are handled correctly
func TestConnectionRefused_IPv6(t *testing.T) {
	stderrOutput := `[tcp @ 0x...] Connection to tcp://[2001:db8::1]:8554?timeout=0 failed: Connection refused
Error opening input files: Connection refused`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "connection_refused", ctx.ErrorType)
	assert.Equal(t, "2001:db8::1", ctx.TargetHost, "Should extract IPv6 address without brackets")
	assert.Equal(t, 8554, ctx.TargetPort)
}

// TestConnectionErrors_WithCredentials tests that credentials don't leak in connection errors
func TestConnectionErrors_WithCredentials(t *testing.T) {
	tests := []struct {
		name         string
		stderrOutput string
		errorType    string
	}{
		{
			name: "Connection refused with credentials",
			stderrOutput: `[tcp @ 0x...] Connection to tcp://admin:secret@camera.local:8554 failed: Connection refused
Error opening input files: Connection refused`,
			errorType: "connection_refused",
		},
		{
			name: "No route with credentials",
			stderrOutput: `[tcp @ 0x...] Connection to tcp://user:pass@192.168.1.1:554 failed: No route to host
Error opening input files: No route to host`,
			errorType: "no_route",
		},
		{
			name: "Network unreachable with credentials",
			stderrOutput: `[tcp @ 0x...] Connection to tcp://admin:p@ssw0rd@host.local:8554 failed: Network unreachable
Error opening input files: Network unreachable`,
			errorType: "network_unreachable",
		},
		{
			name: "Operation not permitted with credentials",
			stderrOutput: `[tcp @ 0x...] Connection to tcp://user:pass@[::1]:554 failed: Operation not permitted
Error opening input files: Operation not permitted`,
			errorType: "operation_not_permitted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := ExtractErrorContext(tt.stderrOutput)

			require.NotNil(t, ctx, "Expected error context, got nil")
			assert.Equal(t, tt.errorType, ctx.ErrorType)

			// Verify no credentials in TargetHost
			assert.NotContains(t, ctx.TargetHost, "admin", "TargetHost contains 'admin'")
			assert.NotContains(t, ctx.TargetHost, "user", "TargetHost contains 'user'")
			assert.NotContains(t, ctx.TargetHost, "pass", "TargetHost contains 'pass'")
			assert.NotContains(t, ctx.TargetHost, "secret", "TargetHost contains 'secret'")
			assert.NotContains(t, ctx.TargetHost, "@", "TargetHost contains @ symbol")

			// Verify port was extracted
			assert.NotZero(t, ctx.TargetPort, "TargetPort should be extracted and non-zero")
		})
	}
}

// TestDNSError_TCPFallback_Credentials tests the tcp:// fallback doesn't leak credentials
func TestDNSError_TCPFallback_Credentials(t *testing.T) {
	// This tests the specific case CodeRabbit found: tcp:// fallback in DNS errors
	stderrOutput := `[tcp @ 0x...] Connection to tcp://user:pass@bad.hostname.local:8554?timeout=0 failed
Name or service not known
Error opening input files: Input/output error`

	ctx := ExtractErrorContext(stderrOutput)

	require.NotNil(t, ctx, "Expected error context, got nil")
	assert.Equal(t, "dns_resolution_failed", ctx.ErrorType)

	// CRITICAL: TargetHost must NOT contain credentials
	assert.NotContains(t, ctx.TargetHost, "user", "SECURITY ISSUE: TargetHost contains 'user'")
	assert.NotContains(t, ctx.TargetHost, "pass", "SECURITY ISSUE: TargetHost contains 'pass'")
	assert.NotContains(t, ctx.TargetHost, "@", "SECURITY ISSUE: TargetHost contains @ symbol")
	assert.NotContains(t, ctx.TargetHost, "tcp://", "SECURITY ISSUE: TargetHost contains URL scheme")

	// Should extract just the hostname
	assert.Equal(t, "bad.hostname.local", ctx.TargetHost)

	// Should also extract the port
	assert.Equal(t, 8554, ctx.TargetPort)
}
