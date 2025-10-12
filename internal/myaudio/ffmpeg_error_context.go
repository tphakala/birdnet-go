package myaudio

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrorContext contains rich information extracted from FFmpeg output.
// It provides detailed diagnostics for user-facing error messages and troubleshooting.
type ErrorContext struct {
	ErrorType       string        // "connection_timeout", "rtsp_404", "auth_failed", etc.
	PrimaryMessage  string        // Main error message
	TargetHost      string        // Extracted host/IP
	TargetPort      int           // Extracted port
	TimeoutDuration time.Duration // Extracted timeout (if applicable)
	HTTPStatus      int           // HTTP/RTSP status code (if applicable)
	RTSPMethod      string        // RTSP method that failed (if applicable)
	RawFFmpegOutput string        // Full FFmpeg output for debugging (sanitized)
	UserFacingMsg   string        // Friendly message for user
	TroubleShooting []string      // List of troubleshooting steps
	Timestamp       time.Time     // When this error was detected
}

// ExtractErrorContext analyzes FFmpeg stderr and extracts context.
// It returns nil if no recognizable error pattern is found.
func ExtractErrorContext(stderrOutput string) *ErrorContext {
	if stderrOutput == "" {
		return nil
	}

	ctx := &ErrorContext{
		RawFFmpegOutput: stderrOutput,
		Timestamp:       time.Now(),
	}

	// Check error patterns in order of specificity
	// More specific patterns should be checked first

	// Connection timeout - very common with unreachable hosts
	if strings.Contains(stderrOutput, "Connection timed out") {
		ctx.ErrorType = "connection_timeout"
		ctx.extractConnectionTimeout(stderrOutput)
		ctx.buildConnectionTimeoutMessage()
		return ctx
	}

	// RTSP 404 - stream path doesn't exist
	if strings.Contains(stderrOutput, "404 Not Found") {
		ctx.ErrorType = "rtsp_404"
		ctx.extractRTSP404(stderrOutput)
		ctx.buildRTSP404Message()
		return ctx
	}

	// Connection refused - server not listening
	if strings.Contains(stderrOutput, "Connection refused") {
		ctx.ErrorType = "connection_refused"
		ctx.extractConnectionRefused(stderrOutput)
		ctx.buildConnectionRefusedMessage()
		return ctx
	}

	// Authentication failure
	if strings.Contains(stderrOutput, "401 Unauthorized") {
		ctx.ErrorType = "auth_failed"
		ctx.extractAuthFailure(stderrOutput)
		ctx.buildAuthFailureMessage()
		return ctx
	}

	// 403 Forbidden
	if strings.Contains(stderrOutput, "403 Forbidden") {
		ctx.ErrorType = "auth_forbidden"
		ctx.extractAuthForbidden(stderrOutput)
		ctx.buildAuthForbiddenMessage()
		return ctx
	}

	// No route to host
	if strings.Contains(stderrOutput, "No route to host") {
		ctx.ErrorType = "no_route"
		ctx.extractNoRoute(stderrOutput)
		ctx.buildNoRouteMessage()
		return ctx
	}

	// Invalid data - stream corruption
	if strings.Contains(stderrOutput, "Invalid data found") {
		ctx.ErrorType = "invalid_data"
		ctx.extractInvalidData(stderrOutput)
		ctx.buildInvalidDataMessage()
		return ctx
	}

	// End of file - stream ended unexpectedly
	if strings.Contains(stderrOutput, "End of file") {
		ctx.ErrorType = "eof"
		ctx.PrimaryMessage = "Stream ended unexpectedly"
		ctx.buildEOFMessage()
		return ctx
	}

	// Protocol not found
	if strings.Contains(stderrOutput, "Protocol not found") {
		ctx.ErrorType = "protocol_error"
		ctx.PrimaryMessage = "Unsupported protocol"
		ctx.buildProtocolErrorMessage()
		return ctx
	}

	// No recognizable error pattern
	return nil
}

// extractConnectionTimeout parses connection timeout details
func (ctx *ErrorContext) extractConnectionTimeout(output string) {
	// Extract target host and port
	// Pattern: "Connection attempt to 192.168.44.3 port 8554 failed"
	re := regexp.MustCompile(`Connection attempt to (\S+) port (\d+) failed`)
	if matches := re.FindStringSubmatch(output); len(matches) == 3 {
		ctx.TargetHost = matches[1]
		if port, err := strconv.Atoi(matches[2]); err == nil {
			ctx.TargetPort = port
		}
	}

	// Extract timeout duration from URL
	// Pattern: "tcp://192.168.44.3:8554?timeout=15000000"
	re = regexp.MustCompile(`timeout=(\d+)`)
	if matches := re.FindStringSubmatch(output); len(matches) == 2 {
		if micros, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
			ctx.TimeoutDuration = time.Duration(micros) * time.Microsecond
		}
	}

	ctx.PrimaryMessage = "Connection timed out"
}

func (ctx *ErrorContext) buildConnectionTimeoutMessage() {
	timeout := "configured timeout"
	if ctx.TimeoutDuration > 0 {
		timeout = fmt.Sprintf("%.0fs", ctx.TimeoutDuration.Seconds())
	}

	ctx.UserFacingMsg = fmt.Sprintf(
		"🔌 Connection to %s:%d timed out after %s\n"+
			"   The RTSP server did not respond within the timeout period.",
		ctx.TargetHost, ctx.TargetPort, timeout,
	)

	ctx.TroubleShooting = []string{
		fmt.Sprintf("Verify the IP address %s and port %d are correct", ctx.TargetHost, ctx.TargetPort),
		"Check if the RTSP server is running and accessible",
		fmt.Sprintf("Test connectivity: ping %s", ctx.TargetHost),
		"Check for firewall rules blocking RTSP traffic",
		"Verify network routing to the target host",
	}
}

// extractRTSP404 parses RTSP 404 error details
func (ctx *ErrorContext) extractRTSP404(output string) {
	ctx.HTTPStatus = 404

	// Extract RTSP method
	// Pattern: "method DESCRIBE failed: 404 Not Found"
	re := regexp.MustCompile(`method (\w+) failed: 404`)
	if matches := re.FindStringSubmatch(output); len(matches) == 2 {
		ctx.RTSPMethod = matches[1]
	}

	// Extract URL from "Error opening input file rtsp://..."
	re = regexp.MustCompile(`Error opening input file (rtsp://\S+)`)
	if matches := re.FindStringSubmatch(output); len(matches) == 2 {
		// Extract just the host from the URL
		urlParts := strings.Split(matches[1], "/")
		if len(urlParts) > 2 {
			hostPort := strings.TrimPrefix(urlParts[2], "@") // Remove auth prefix if present
			ctx.TargetHost = hostPort
		}
	}

	ctx.PrimaryMessage = "RTSP stream not found"
}

func (ctx *ErrorContext) buildRTSP404Message() {
	method := ctx.RTSPMethod
	if method == "" {
		method = "DESCRIBE"
	}

	ctx.UserFacingMsg = fmt.Sprintf(
		"📹 RTSP stream not found (404)\n"+
			"   The RTSP server responded with 404 Not Found during %s method.\n"+
			"   This means the stream path does not exist on the server.",
		method,
	)

	ctx.TroubleShooting = []string{
		"Check if the stream name is correct (case-sensitive)",
		"Verify the stream path in your RTSP URL",
		"List available streams on the RTSP server",
		"Confirm the stream is published and active",
		"Check RTSP server logs for more information",
	}
}

// extractConnectionRefused parses connection refused details
func (ctx *ErrorContext) extractConnectionRefused(output string) {
	// Pattern: "Connection to tcp://localhost:8553?timeout=0 failed"
	re := regexp.MustCompile(`Connection to tcp://([^:?]+):(\d+)`)
	if matches := re.FindStringSubmatch(output); len(matches) == 3 {
		ctx.TargetHost = matches[1]
		if port, err := strconv.Atoi(matches[2]); err == nil {
			ctx.TargetPort = port
		}
	}

	ctx.PrimaryMessage = "Connection refused"
}

func (ctx *ErrorContext) buildConnectionRefusedMessage() {
	ctx.UserFacingMsg = fmt.Sprintf(
		"🚫 Connection refused: %s:%d\n"+
			"   The connection was actively refused by the target.\n"+
			"   This typically means no RTSP server is listening on this port.",
		ctx.TargetHost, ctx.TargetPort,
	)

	ctx.TroubleShooting = []string{
		"Verify the RTSP server is running",
		fmt.Sprintf("Check if port %d is correct (common RTSP ports: 554, 8554)", ctx.TargetPort),
		fmt.Sprintf("Test if port is listening: netstat -an | grep %d", ctx.TargetPort),
		"Check RTSP server startup logs for errors",
		"Verify the server is configured to accept connections",
	}
}

// extractAuthFailure parses authentication failure details
func (ctx *ErrorContext) extractAuthFailure(output string) {
	ctx.HTTPStatus = 401
	ctx.PrimaryMessage = "Authentication required"

	// Extract RTSP method
	re := regexp.MustCompile(`method (\w+) failed: 401`)
	if matches := re.FindStringSubmatch(output); len(matches) == 2 {
		ctx.RTSPMethod = matches[1]
	}
}

func (ctx *ErrorContext) buildAuthFailureMessage() {
	ctx.UserFacingMsg = "🔐 Authentication required (401 Unauthorized)\n" +
		"   The RTSP server requires username and password.\n" +
		"   Credentials were not provided or are incorrect."

	ctx.TroubleShooting = []string{
		"Add credentials to URL: rtsp://username:password@host:port/stream",
		"Verify username and password are correct",
		"Check if credentials have special characters that need URL encoding",
		"Contact RTSP server administrator if unsure of credentials",
		"Check server logs for authentication details",
	}
}

// extractAuthForbidden parses forbidden access details
func (ctx *ErrorContext) extractAuthForbidden(output string) {
	ctx.HTTPStatus = 403
	ctx.PrimaryMessage = "Access forbidden"
}

func (ctx *ErrorContext) buildAuthForbiddenMessage() {
	ctx.UserFacingMsg = "⛔ Access forbidden (403)\n" +
		"   The RTSP server understood the request but refuses to authorize it.\n" +
		"   The credentials may be valid but lack permissions."

	ctx.TroubleShooting = []string{
		"Verify the user account has permissions to access this stream",
		"Check RTSP server access control lists (ACLs)",
		"Contact RTSP server administrator about access permissions",
		"Check if IP address is whitelisted on the server",
	}
}

// extractNoRoute parses no route to host details
func (ctx *ErrorContext) extractNoRoute(output string) {
	// Similar pattern to connection refused
	re := regexp.MustCompile(`Connection to tcp://([^:?]+):(\d+)`)
	if matches := re.FindStringSubmatch(output); len(matches) == 3 {
		ctx.TargetHost = matches[1]
		if port, err := strconv.Atoi(matches[2]); err == nil {
			ctx.TargetPort = port
		}
	}

	ctx.PrimaryMessage = "No route to host"
}

func (ctx *ErrorContext) buildNoRouteMessage() {
	ctx.UserFacingMsg = fmt.Sprintf(
		"🗺️  No route to host: %s\n"+
			"   The network cannot find a route to reach the target host.\n"+
			"   This is a network routing issue.",
		ctx.TargetHost,
	)

	ctx.TroubleShooting = []string{
		fmt.Sprintf("Check if %s is reachable: ping %s", ctx.TargetHost, ctx.TargetHost),
		"Verify the IP address is correct",
		"Check network routing configuration",
		"Verify network interfaces are up",
		"Check if VPN or network changes affected routing",
	}
}

// extractInvalidData parses invalid data details
func (ctx *ErrorContext) extractInvalidData(output string) {
	ctx.PrimaryMessage = "Invalid or corrupted stream data"
}

func (ctx *ErrorContext) buildInvalidDataMessage() {
	ctx.UserFacingMsg = "📺 Invalid or corrupted stream data\n" +
		"   FFmpeg detected invalid data when processing the stream.\n" +
		"   The stream may be corrupted or using an unsupported format."

	ctx.TroubleShooting = []string{
		"Verify the stream is properly encoded",
		"Check if the RTSP server is functioning correctly",
		"Try restarting the RTSP server",
		"Check network quality (packet loss, latency)",
		"Verify codec compatibility",
	}
}

// buildEOFMessage for unexpected stream end
func (ctx *ErrorContext) buildEOFMessage() {
	ctx.UserFacingMsg = "📡 Stream ended unexpectedly\n" +
		"   The RTSP stream terminated without proper closure."

	ctx.TroubleShooting = []string{
		"Check if the RTSP server is still running",
		"Verify the stream source is active",
		"Check network connectivity",
		"Review RTSP server logs for errors",
	}
}

// buildProtocolErrorMessage for unsupported protocols
func (ctx *ErrorContext) buildProtocolErrorMessage() {
	ctx.UserFacingMsg = "🔌 Unsupported protocol\n" +
		"   FFmpeg does not support the requested protocol.\n" +
		"   This usually indicates an incorrect URL format."

	ctx.TroubleShooting = []string{
		"Verify the URL starts with rtsp:// or rtsps://",
		"Check if the URL format is correct",
		"Ensure FFmpeg was compiled with RTSP support",
		"Try a different transport method (TCP vs UDP)",
	}
}

// FormatForConsole returns a formatted string for console output
func (ctx *ErrorContext) FormatForConsole() string {
	var sb strings.Builder

	// User-facing message
	sb.WriteString(ctx.UserFacingMsg)
	sb.WriteString("\n")

	// Troubleshooting steps
	if len(ctx.TroubleShooting) > 0 {
		sb.WriteString("\n   Troubleshooting steps:\n")
		for _, step := range ctx.TroubleShooting {
			sb.WriteString(fmt.Sprintf("   • %s\n", step))
		}
	}

	return sb.String()
}

// ShouldOpenCircuit determines if this error should immediately open the circuit breaker.
// Returns true for permanent failures (404, auth, connection refused, etc.)
func (ctx *ErrorContext) ShouldOpenCircuit() bool {
	switch ctx.ErrorType {
	case "rtsp_404", "auth_failed", "auth_forbidden", "connection_refused",
		"no_route", "protocol_error":
		return true // Permanent failures
	case "connection_timeout", "invalid_data", "eof":
		return false // Transient failures - allow retry
	default:
		return false
	}
}

// ShouldRestart determines if this error should trigger an automatic restart.
// Returns true for transient failures that might recover on retry.
func (ctx *ErrorContext) ShouldRestart() bool {
	switch ctx.ErrorType {
	case "connection_timeout", "invalid_data", "eof":
		return true // Transient failures
	default:
		return false
	}
}
