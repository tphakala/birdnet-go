package myaudio

import (
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
		UserFacingMsg:  "Test message",
		TroubleShooting: []string{
			"Step 1",
			"Step 2",
		},
	}

	output := ctx.FormatForConsole()

	if !strings.Contains(output, "Test message") {
		t.Error("Console output missing user message")
	}

	if !strings.Contains(output, "Step 1") || !strings.Contains(output, "Step 2") {
		t.Error("Console output missing troubleshooting steps")
	}

	if !strings.Contains(output, "•") {
		t.Error("Console output missing bullet points for troubleshooting")
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
