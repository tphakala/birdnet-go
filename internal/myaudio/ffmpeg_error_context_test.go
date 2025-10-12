package myaudio

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestExtractErrorContext_ConnectionTimeout(t *testing.T) {
	stderrOutput := `[tcp @ 0x556404ebeb40] Starting connection attempt to 192.168.44.3 port 8554
[tcp @ 0x556404ebeb40] Connection attempt to 192.168.44.3 port 8554 failed: Connection timed out
[tcp @ 0x556404ebeb40] Connection to tcp://192.168.44.3:8554?timeout=15000000 failed: Connection timed out
Error opening input files: Connection timed out`

	ctx := ExtractErrorContext(stderrOutput)

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "connection_timeout" {
		t.Errorf("Expected error type 'connection_timeout', got %s", ctx.ErrorType)
	}

	if ctx.TargetHost != "192.168.44.3" {
		t.Errorf("Expected target host '192.168.44.3', got %s", ctx.TargetHost)
	}

	if ctx.TargetPort != 8554 {
		t.Errorf("Expected target port 8554, got %d", ctx.TargetPort)
	}

	expectedTimeout := 15 * time.Second
	if ctx.TimeoutDuration != expectedTimeout {
		t.Errorf("Expected timeout %v, got %v", expectedTimeout, ctx.TimeoutDuration)
	}

	if !ctx.ShouldRestart() {
		t.Error("Connection timeout should trigger restart")
	}

	if ctx.ShouldOpenCircuit() {
		t.Error("Connection timeout should not open circuit breaker")
	}

	if !strings.Contains(ctx.UserFacingMsg, "Connection to 192.168.44.3:8554 timed out") {
		t.Errorf("User-facing message missing expected content: %s", ctx.UserFacingMsg)
	}

	if len(ctx.TroubleShooting) == 0 {
		t.Error("Expected troubleshooting steps, got none")
	}
}

func TestExtractErrorContext_RTSP404(t *testing.T) {
	stderrOutput := `[rtsp @ 0x55cc5f603980] method DESCRIBE failed: 404 Not Found
[in#0 @ 0x55cc5f603640] Error opening input: Server returned 404 Not Found
Error opening input file rtsp://localhost:8554/mystream.
Error opening input files: Server returned 404 Not Found`

	ctx := ExtractErrorContext(stderrOutput)

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "rtsp_404" {
		t.Errorf("Expected error type 'rtsp_404', got %s", ctx.ErrorType)
	}

	if ctx.HTTPStatus != 404 {
		t.Errorf("Expected HTTP status 404, got %d", ctx.HTTPStatus)
	}

	if ctx.RTSPMethod != "DESCRIBE" {
		t.Errorf("Expected RTSP method 'DESCRIBE', got %s", ctx.RTSPMethod)
	}

	if !ctx.ShouldOpenCircuit() {
		t.Error("RTSP 404 should open circuit breaker")
	}

	if ctx.ShouldRestart() {
		t.Error("RTSP 404 should not trigger restart (permanent failure)")
	}

	if !strings.Contains(ctx.UserFacingMsg, "RTSP stream not found (404)") {
		t.Errorf("User-facing message missing expected content: %s", ctx.UserFacingMsg)
	}
}

func TestExtractErrorContext_ConnectionRefused(t *testing.T) {
	stderrOutput := `[tcp @ 0x5583d3a7c680] Connection to tcp://localhost:8553?timeout=0 failed: Connection refused
[in#0 @ 0x5583d3a79640] Error opening input: Connection refused
Error opening input file rtsp://localhost:8553/stream.`

	ctx := ExtractErrorContext(stderrOutput)

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "connection_refused" {
		t.Errorf("Expected error type 'connection_refused', got %s", ctx.ErrorType)
	}

	if ctx.TargetHost != "localhost" {
		t.Errorf("Expected target host 'localhost', got %s", ctx.TargetHost)
	}

	if ctx.TargetPort != 8553 {
		t.Errorf("Expected target port 8553, got %d", ctx.TargetPort)
	}

	if !ctx.ShouldOpenCircuit() {
		t.Error("Connection refused should open circuit breaker")
	}

	if ctx.ShouldRestart() {
		t.Error("Connection refused should not trigger restart (permanent failure)")
	}
}

func TestExtractErrorContext_AuthFailure(t *testing.T) {
	stderrOutput := `[rtsp @ 0x...] method DESCRIBE failed: 401 Unauthorized
[in#0 @ 0x...] Error opening input: Server returned 401 Unauthorized
Error opening input file rtsp://camera.example.com/stream.`

	ctx := ExtractErrorContext(stderrOutput)

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "auth_failed" {
		t.Errorf("Expected error type 'auth_failed', got %s", ctx.ErrorType)
	}

	if ctx.HTTPStatus != 401 {
		t.Errorf("Expected HTTP status 401, got %d", ctx.HTTPStatus)
	}

	if !ctx.ShouldOpenCircuit() {
		t.Error("Auth failure should open circuit breaker")
	}

	if !strings.Contains(ctx.UserFacingMsg, "Authentication required") {
		t.Errorf("User-facing message missing expected content: %s", ctx.UserFacingMsg)
	}
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

	if ctx != nil {
		t.Errorf("Expected no error context for normal output, got error type: %s", ctx.ErrorType)
	}
}

func TestExtractErrorContext_EmptyString(t *testing.T) {
	ctx := ExtractErrorContext("")

	if ctx != nil {
		t.Error("Expected nil for empty string")
	}
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
	if !strings.Contains(output, "Test error message") {
		t.Error("Console output missing user message")
	}

	// Test 2: All troubleshooting steps are present
	if !strings.Contains(output, "Step 1") || !strings.Contains(output, "Step 2") || !strings.Contains(output, "Step 3") {
		t.Error("Console output missing one or more troubleshooting steps")
	}

	// Test 3: Bullet points are used for formatting
	bulletCount := strings.Count(output, "•")
	if bulletCount != len(ctx.TroubleShooting) {
		t.Errorf("Expected %d bullet points, got %d", len(ctx.TroubleShooting), bulletCount)
	}

	// Test 4: Troubleshooting section header is present
	if !strings.Contains(output, "Troubleshooting steps:") {
		t.Error("Console output missing 'Troubleshooting steps:' header")
	}

	// Test 5: Output structure - message comes before troubleshooting
	msgIndex := strings.Index(output, "Test error message")
	troubleshootingIndex := strings.Index(output, "Troubleshooting steps:")
	if msgIndex == -1 || troubleshootingIndex == -1 || msgIndex >= troubleshootingIndex {
		t.Error("Console output structure incorrect: user message should come before troubleshooting")
	}

	// Test 6: Each troubleshooting step is on its own line with proper indentation
	for _, step := range ctx.TroubleShooting {
		expectedLine := fmt.Sprintf("   • %s", step)
		if !strings.Contains(output, expectedLine) {
			t.Errorf("Console output missing properly formatted step: %q", expectedLine)
		}
	}

	// Test 7: Emoji support (optional but nice to verify)
	if !strings.Contains(output, "🔌") {
		t.Error("Console output missing emoji from user message")
	}

	// Test 8: Newline formatting is preserved
	lines := strings.Split(output, "\n")
	if len(lines) < 4 { // At least: message, blank line, header, steps
		t.Errorf("Expected at least 4 lines in output, got %d", len(lines))
	}
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
	if !strings.Contains(output, "An unknown error occurred") {
		t.Error("Console output missing user message")
	}

	// Should not contain troubleshooting header when there are no steps
	if strings.Contains(output, "Troubleshooting steps:") {
		t.Error("Console output should not have troubleshooting header when no steps provided")
	}

	// Should not contain bullet points
	if strings.Contains(output, "•") {
		t.Error("Console output should not have bullet points when no steps provided")
	}
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
			if ctx.ShouldOpenCircuit() != tt.shouldOpen {
				t.Errorf("%s: expected ShouldOpenCircuit() = %v, got %v",
					tt.description, tt.shouldOpen, ctx.ShouldOpenCircuit())
			}
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
			if ctx.ShouldRestart() != tt.shouldRestart {
				t.Errorf("%s: expected ShouldRestart() = %v, got %v",
					tt.description, tt.shouldRestart, ctx.ShouldRestart())
			}
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

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "dns_resolution_failed" {
		t.Errorf("Expected error type 'dns_resolution_failed', got %s", ctx.ErrorType)
	}

	if ctx.TargetHost != "sfsdfds" {
		t.Errorf("Expected target host 'sfsdfds', got %s", ctx.TargetHost)
	}

	if !ctx.ShouldOpenCircuit() {
		t.Error("DNS resolution failure should open circuit breaker")
	}

	if ctx.ShouldRestart() {
		t.Error("DNS resolution failure should not trigger restart (permanent failure)")
	}

	if !strings.Contains(ctx.UserFacingMsg, "DNS resolution failed") {
		t.Errorf("User-facing message missing expected content: %s", ctx.UserFacingMsg)
	}

	if !strings.Contains(ctx.UserFacingMsg, "sfsdfds") {
		t.Errorf("User-facing message should contain hostname 'sfsdfds': %s", ctx.UserFacingMsg)
	}
}

func TestExtractErrorContext_NetworkUnreachable(t *testing.T) {
	stderrOutput := `[tcp @ 0x...] Connection to tcp://192.168.1.100:8554?timeout=0 failed: Network unreachable
[in#0 @ 0x...] Error opening input: Network unreachable
Error opening input file rtsp://192.168.1.100:8554/stream.`

	ctx := ExtractErrorContext(stderrOutput)

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "network_unreachable" {
		t.Errorf("Expected error type 'network_unreachable', got %s", ctx.ErrorType)
	}

	if ctx.TargetHost != "192.168.1.100" {
		t.Errorf("Expected target host '192.168.1.100', got %s", ctx.TargetHost)
	}

	if ctx.ShouldOpenCircuit() {
		t.Error("Network unreachable should not open circuit breaker (transient)")
	}

	if !ctx.ShouldRestart() {
		t.Error("Network unreachable should trigger restart (transient failure)")
	}

	if !strings.Contains(ctx.UserFacingMsg, "Network unreachable") {
		t.Errorf("User-facing message missing expected content: %s", ctx.UserFacingMsg)
	}
}

func TestExtractErrorContext_OperationNotPermitted(t *testing.T) {
	stderrOutput := `[tcp @ 0x...] Connection to tcp://camera.local:554?timeout=0 failed: Operation not permitted
[in#0 @ 0x...] Error opening input: Operation not permitted
Error opening input file rtsp://camera.local:554/stream.`

	ctx := ExtractErrorContext(stderrOutput)

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "operation_not_permitted" {
		t.Errorf("Expected error type 'operation_not_permitted', got %s", ctx.ErrorType)
	}

	if !ctx.ShouldOpenCircuit() {
		t.Error("Operation not permitted should open circuit breaker")
	}

	if ctx.ShouldRestart() {
		t.Error("Operation not permitted should not trigger restart (permanent failure)")
	}

	if !strings.Contains(ctx.UserFacingMsg, "Operation not permitted") {
		t.Errorf("User-facing message missing expected content: %s", ctx.UserFacingMsg)
	}
}

func TestExtractErrorContext_SSLError(t *testing.T) {
	stderrOutput := `[tls @ 0x...] SSL connection error
[in#0 @ 0x...] Error opening input: SSL error
Error opening input file rtsps://secure-camera.example.com:8322/stream.
Error opening input files: SSL error`

	ctx := ExtractErrorContext(stderrOutput)

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "ssl_error" {
		t.Errorf("Expected error type 'ssl_error', got %s", ctx.ErrorType)
	}

	if !ctx.ShouldOpenCircuit() {
		t.Error("SSL error should open circuit breaker")
	}

	if ctx.ShouldRestart() {
		t.Error("SSL error should not trigger restart (permanent failure)")
	}

	if !strings.Contains(ctx.UserFacingMsg, "SSL/TLS") {
		t.Errorf("User-facing message missing expected content: %s", ctx.UserFacingMsg)
	}
}

func TestExtractErrorContext_RTSP503(t *testing.T) {
	stderrOutput := `[rtsp @ 0x...] method DESCRIBE failed: 503 Service Unavailable
[in#0 @ 0x...] Error opening input: Server returned 503 Service Unavailable
Error opening input file rtsp://overloaded.example.com:8554/stream.
Error opening input files: Server returned 503 Service Unavailable`

	ctx := ExtractErrorContext(stderrOutput)

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "rtsp_503" {
		t.Errorf("Expected error type 'rtsp_503', got %s", ctx.ErrorType)
	}

	if ctx.HTTPStatus != 503 {
		t.Errorf("Expected HTTP status 503, got %d", ctx.HTTPStatus)
	}

	if ctx.ShouldOpenCircuit() {
		t.Error("RTSP 503 should not open circuit breaker (transient)")
	}

	if !ctx.ShouldRestart() {
		t.Error("RTSP 503 should trigger restart (transient failure)")
	}

	if !strings.Contains(ctx.UserFacingMsg, "service unavailable") {
		t.Errorf("User-facing message missing expected content: %s", ctx.UserFacingMsg)
	}
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

			if ctx == nil {
				t.Fatal("Expected error context, got nil")
			}

			if ctx.ErrorType != tt.expectedType {
				t.Errorf("Expected error type '%s', got '%s'\nExplanation: %s\nStderr: %s",
					tt.expectedType, ctx.ErrorType, tt.explanation, tt.stderrOutput)
			}
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

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "no_route" {
		t.Errorf("Expected 'no_route' for overlapping network errors, got %s\n"+
			"Rationale: EHOSTUNREACH (no route) indicates a specific routing table problem,\n"+
			"while ENETUNREACH (network unreachable) is a broader network configuration issue.\n"+
			"The more specific error provides better diagnostic value.",
			ctx.ErrorType)
	}
}

func TestExtractErrorContext_ZeroTimeout(t *testing.T) {
	// Test case: timeout=0 means infinite timeout, but TCP stack gave up
	stderrOutput := `[tcp @ 0x556404ebeb40] Starting connection attempt to 192.168.44.3 port 8554
[tcp @ 0x556404ebeb40] Connection attempt to 192.168.44.3 port 8554 failed: Connection timed out
[tcp @ 0x556404ebeb40] Connection to tcp://192.168.44.3:8554?timeout=0 failed: Connection timed out
Error opening input files: Connection timed out`

	ctx := ExtractErrorContext(stderrOutput)

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "connection_timeout" {
		t.Errorf("Expected error type 'connection_timeout', got %s", ctx.ErrorType)
	}

	// Zero timeout should be explicitly handled
	if ctx.TimeoutDuration != 0 {
		t.Errorf("Expected timeout duration 0, got %v", ctx.TimeoutDuration)
	}

	// User message should mention TCP stack timeout
	if !strings.Contains(ctx.UserFacingMsg, "TCP stack timeout") {
		t.Errorf("Expected message to mention TCP stack timeout for zero timeout, got: %s", ctx.UserFacingMsg)
	}
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

			if ctx == nil {
				t.Fatal("Expected error context, got nil")
			}

			if ctx.ErrorType != tt.expectedType {
				t.Errorf("Expected error type '%s', got '%s'", tt.expectedType, ctx.ErrorType)
			}

			// Check that RawFFmpegOutput is sanitized
			if strings.Contains(ctx.RawFFmpegOutput, "password") ||
				strings.Contains(ctx.RawFFmpegOutput, "secret") ||
				strings.Contains(ctx.RawFFmpegOutput, "p@ssw0rd") {
				t.Errorf("RawFFmpegOutput contains unsanitized credentials: %s", ctx.RawFFmpegOutput)
			}

			// Check that credentials should be replaced with ***
			if !strings.Contains(ctx.RawFFmpegOutput, "***") {
				t.Log("Note: RawFFmpegOutput should contain *** placeholders for credentials")
			}

			// Check TargetHost is clean
			if tt.checkHost {
				if strings.Contains(ctx.TargetHost, tt.hostShouldNot) {
					t.Errorf("TargetHost contains credentials: got '%s', should not contain '%s'\n"+
						"Explanation: %s",
						ctx.TargetHost, tt.hostShouldNot, tt.explanation)
				}

				if strings.Contains(ctx.TargetHost, "@") {
					t.Errorf("TargetHost contains @ symbol (likely has userinfo): %s", ctx.TargetHost)
				}

				if strings.Contains(ctx.TargetHost, ":") && tt.checkHost {
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
			if result != tt.expected {
				t.Errorf("extractHostWithoutCredentials(%q) = %q, want %q",
					tt.input, result, tt.expected)
			}

			// Ensure no credentials leaked
			if strings.Contains(result, "admin") || strings.Contains(result, "pass") ||
				strings.Contains(result, "secret") || strings.Contains(result, "@") {
				t.Errorf("Result contains credentials or @ symbol: %q", result)
			}
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

			if ok != tt.shouldSucceed {
				t.Errorf("Expected success=%v, got %v", tt.shouldSucceed, ok)
				return
			}

			if !tt.shouldSucceed {
				return // Expected to fail, and it did
			}

			if host != tt.expectedHost {
				t.Errorf("Expected host %q, got %q", tt.expectedHost, host)
			}

			if port != tt.expectedPort {
				t.Errorf("Expected port %d, got %d", tt.expectedPort, port)
			}

			// Security check: ensure no credentials leaked
			if strings.Contains(host, "user") || strings.Contains(host, "pass") || strings.Contains(host, "@") {
				t.Errorf("Host contains credentials or @ symbol: %q", host)
			}
		})
	}
}

// TestConnectionRefused_IPv6 tests that IPv6 addresses are handled correctly
func TestConnectionRefused_IPv6(t *testing.T) {
	stderrOutput := `[tcp @ 0x...] Connection to tcp://[2001:db8::1]:8554?timeout=0 failed: Connection refused
Error opening input files: Connection refused`

	ctx := ExtractErrorContext(stderrOutput)

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "connection_refused" {
		t.Errorf("Expected error type 'connection_refused', got %s", ctx.ErrorType)
	}

	// Should extract IPv6 address without brackets
	if ctx.TargetHost != "2001:db8::1" {
		t.Errorf("Expected host '2001:db8::1', got '%s'", ctx.TargetHost)
	}

	if ctx.TargetPort != 8554 {
		t.Errorf("Expected port 8554, got %d", ctx.TargetPort)
	}
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

			if ctx == nil {
				t.Fatal("Expected error context, got nil")
			}

			if ctx.ErrorType != tt.errorType {
				t.Errorf("Expected error type '%s', got '%s'", tt.errorType, ctx.ErrorType)
			}

			// Verify no credentials in TargetHost
			if strings.Contains(ctx.TargetHost, "admin") ||
				strings.Contains(ctx.TargetHost, "user") ||
				strings.Contains(ctx.TargetHost, "pass") ||
				strings.Contains(ctx.TargetHost, "secret") ||
				strings.Contains(ctx.TargetHost, "@") {
				t.Errorf("TargetHost contains credentials: %s", ctx.TargetHost)
			}

			// Verify port was extracted
			if ctx.TargetPort == 0 {
				t.Error("TargetPort should be extracted and non-zero")
			}
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

	if ctx == nil {
		t.Fatal("Expected error context, got nil")
	}

	if ctx.ErrorType != "dns_resolution_failed" {
		t.Errorf("Expected error type 'dns_resolution_failed', got %s", ctx.ErrorType)
	}

	// CRITICAL: TargetHost must NOT contain credentials
	if strings.Contains(ctx.TargetHost, "user") ||
		strings.Contains(ctx.TargetHost, "pass") ||
		strings.Contains(ctx.TargetHost, "@") ||
		strings.Contains(ctx.TargetHost, "tcp://") {
		t.Errorf("SECURITY ISSUE: TargetHost contains credentials or URL scheme: '%s'", ctx.TargetHost)
	}

	// Should extract just the hostname
	if ctx.TargetHost != "bad.hostname.local" {
		t.Errorf("Expected TargetHost 'bad.hostname.local', got '%s'", ctx.TargetHost)
	}

	// Should also extract the port
	if ctx.TargetPort != 8554 {
		t.Errorf("Expected TargetPort 8554, got %d", ctx.TargetPort)
	}
}
