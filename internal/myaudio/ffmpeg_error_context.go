package myaudio

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Pre-compiled regular expressions for FFmpeg error parsing
// Compiling these at package initialization improves performance during error detection
var (
	reConnectionAttempt  = regexp.MustCompile(`Connection attempt to (\S+) port (\d+) failed`)
	reTimeoutDuration    = regexp.MustCompile(`timeout=(\d+)`)
	reRTSPMethod         = regexp.MustCompile(`method (\w+) failed: (\d+)`)
	reRTSPMethod404      = regexp.MustCompile(`method (\w+) failed: 404`)
	reRTSPMethod401      = regexp.MustCompile(`method (\w+) failed: 401`)
	reErrorOpeningInput  = regexp.MustCompile(`Error opening input file (rtsps?://\S+)`)
	reConnectionToTCP    = regexp.MustCompile(`Connection to tcp://([^:?]+):(\d+)`)
	reRTSPStatus         = regexp.MustCompile(`Server returned (\d+)`)
	reSSLError           = regexp.MustCompile(`SSL.*error|TLS.*error|certificate.*error`)
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

	// Network unreachable - different from no route
	if strings.Contains(stderrOutput, "Network unreachable") {
		ctx.ErrorType = "network_unreachable"
		ctx.extractNetworkUnreachable(stderrOutput)
		ctx.buildNetworkUnreachableMessage()
		return ctx
	}

	// Operation not permitted - firewall/SELinux
	if strings.Contains(stderrOutput, "Operation not permitted") {
		ctx.ErrorType = "operation_not_permitted"
		ctx.extractOperationNotPermitted(stderrOutput)
		ctx.buildOperationNotPermittedMessage()
		return ctx
	}

	// SSL/TLS errors for rtsps://
	if reSSLError.MatchString(stderrOutput) {
		ctx.ErrorType = "ssl_error"
		ctx.extractSSLError(stderrOutput)
		ctx.buildSSLErrorMessage()
		return ctx
	}

	// RTSP 503 Service Unavailable - server overload
	if strings.Contains(stderrOutput, "503 Service Unavailable") {
		ctx.ErrorType = "rtsp_503"
		ctx.extractRTSP503(stderrOutput)
		ctx.buildRTSP503Message()
		return ctx
	}

	// DNS resolution failures - common with typos in hostnames
	if strings.Contains(stderrOutput, "Name or service not known") ||
		strings.Contains(stderrOutput, "nodename nor servname provided") ||
		strings.Contains(stderrOutput, "Temporary failure in name resolution") ||
		strings.Contains(stderrOutput, "Could not resolve hostname") {
		ctx.ErrorType = "dns_resolution_failed"
		ctx.extractDNSError(stderrOutput)
		ctx.buildDNSErrorMessage()
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
	if matches := reConnectionAttempt.FindStringSubmatch(output); len(matches) == 3 {
		ctx.TargetHost = matches[1]
		if port, err := strconv.Atoi(matches[2]); err == nil {
			ctx.TargetPort = port
		}
	}

	// Extract timeout duration from URL
	// Pattern: "tcp://192.168.44.3:8554?timeout=15000000"
	if matches := reTimeoutDuration.FindStringSubmatch(output); len(matches) == 2 {
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
	if matches := reRTSPMethod404.FindStringSubmatch(output); len(matches) == 2 {
		ctx.RTSPMethod = matches[1]
	}

	// Extract URL from "Error opening input file rtsp://..."
	if matches := reErrorOpeningInput.FindStringSubmatch(output); len(matches) == 2 {
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
	if matches := reConnectionToTCP.FindStringSubmatch(output); len(matches) == 3 {
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
	if matches := reRTSPMethod401.FindStringSubmatch(output); len(matches) == 2 {
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
	if matches := reConnectionToTCP.FindStringSubmatch(output); len(matches) == 3 {
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

// extractNetworkUnreachable parses network unreachable details
func (ctx *ErrorContext) extractNetworkUnreachable(output string) {
	// Similar pattern to connection refused
	if matches := reConnectionToTCP.FindStringSubmatch(output); len(matches) == 3 {
		ctx.TargetHost = matches[1]
		if port, err := strconv.Atoi(matches[2]); err == nil {
			ctx.TargetPort = port
		}
	}

	ctx.PrimaryMessage = "Network unreachable"
}

func (ctx *ErrorContext) buildNetworkUnreachableMessage() {
	ctx.UserFacingMsg = fmt.Sprintf(
		"🌐 Network unreachable: %s\n"+
			"   The network is unreachable from this host.\n"+
			"   This typically indicates a network configuration or connectivity issue.",
		ctx.TargetHost,
	)

	ctx.TroubleShooting = []string{
		fmt.Sprintf("Check if %s is reachable: ping %s", ctx.TargetHost, ctx.TargetHost),
		"Verify your network connection is active",
		"Check if the correct network interface is being used",
		"Verify network gateway configuration",
		"Check if you're connected to the correct network (WiFi/Ethernet)",
		"Verify DNS resolution is working",
	}
}

// extractOperationNotPermitted parses operation not permitted details
func (ctx *ErrorContext) extractOperationNotPermitted(output string) {
	// Try to extract target info
	if matches := reConnectionToTCP.FindStringSubmatch(output); len(matches) == 3 {
		ctx.TargetHost = matches[1]
		if port, err := strconv.Atoi(matches[2]); err == nil {
			ctx.TargetPort = port
		}
	}

	ctx.PrimaryMessage = "Operation not permitted"
}

func (ctx *ErrorContext) buildOperationNotPermittedMessage() {
	ctx.UserFacingMsg = "🔒 Operation not permitted\n" +
		"   The system denied the network operation.\n" +
		"   This is typically caused by firewall rules or security policies (SELinux/AppArmor)."

	ctx.TroubleShooting = []string{
		"Check firewall rules: sudo iptables -L",
		"Verify SELinux is not blocking the connection: sestatus",
		"Check AppArmor status: sudo aa-status",
		"Review system security policies",
		"Try running with appropriate permissions",
		"Check if the application needs specific capabilities (CAP_NET_RAW, etc.)",
	}
}

// extractSSLError parses SSL/TLS error details
func (ctx *ErrorContext) extractSSLError(output string) {
	// Try to extract URL to get host info
	if matches := reErrorOpeningInput.FindStringSubmatch(output); len(matches) == 2 {
		urlParts := strings.Split(matches[1], "/")
		if len(urlParts) > 2 {
			hostPort := strings.TrimPrefix(urlParts[2], "@")
			ctx.TargetHost = hostPort
		}
	}

	ctx.PrimaryMessage = "SSL/TLS error"
}

func (ctx *ErrorContext) buildSSLErrorMessage() {
	ctx.UserFacingMsg = "🔐 SSL/TLS connection error\n" +
		"   Failed to establish a secure connection to the RTSP server.\n" +
		"   This could be due to certificate issues or TLS configuration problems."

	ctx.TroubleShooting = []string{
		"Verify the server certificate is valid and not expired",
		"Check if the server uses a self-signed certificate (may need to trust it)",
		"Ensure the system's CA certificates are up to date",
		"Try using rtsp:// instead of rtsps:// if encryption is not required",
		"Check if the server supports the TLS version FFmpeg is using",
		"Review server SSL/TLS configuration",
	}
}

// extractRTSP503 parses RTSP 503 error details
func (ctx *ErrorContext) extractRTSP503(output string) {
	ctx.HTTPStatus = 503

	// Extract RTSP method if available
	if matches := reRTSPMethod.FindStringSubmatch(output); len(matches) == 3 {
		ctx.RTSPMethod = matches[1]
	}

	// Extract URL
	if matches := reErrorOpeningInput.FindStringSubmatch(output); len(matches) == 2 {
		urlParts := strings.Split(matches[1], "/")
		if len(urlParts) > 2 {
			hostPort := strings.TrimPrefix(urlParts[2], "@")
			ctx.TargetHost = hostPort
		}
	}

	ctx.PrimaryMessage = "RTSP service unavailable"
}

func (ctx *ErrorContext) buildRTSP503Message() {
	ctx.UserFacingMsg = "⏳ RTSP service unavailable (503)\n" +
		"   The RTSP server is temporarily unable to handle the request.\n" +
		"   This typically indicates server overload or maintenance."

	ctx.TroubleShooting = []string{
		"Wait a few moments and try again",
		"Check if the server is under heavy load",
		"Verify the server has sufficient resources (CPU, memory, bandwidth)",
		"Check if there are connection limits on the server",
		"Review RTSP server logs for errors",
		"Contact server administrator if the issue persists",
	}
}

// extractDNSError parses DNS resolution error details
func (ctx *ErrorContext) extractDNSError(output string) {
	// Try to extract hostname from URL
	if matches := reErrorOpeningInput.FindStringSubmatch(output); len(matches) == 2 {
		urlParts := strings.Split(matches[1], "/")
		if len(urlParts) > 2 {
			hostPort := strings.TrimPrefix(urlParts[2], "@")
			// Extract just the hostname (remove port if present)
			if colonIdx := strings.Index(hostPort, ":"); colonIdx != -1 {
				ctx.TargetHost = hostPort[:colonIdx]
			} else {
				ctx.TargetHost = hostPort
			}
		}
	}

	// Also try to extract from tcp:// pattern
	if ctx.TargetHost == "" {
		if matches := reConnectionToTCP.FindStringSubmatch(output); len(matches) >= 2 {
			ctx.TargetHost = matches[1]
		}
	}

	ctx.PrimaryMessage = "DNS resolution failed"
}

func (ctx *ErrorContext) buildDNSErrorMessage() {
	hostInfo := "the hostname"
	if ctx.TargetHost != "" {
		hostInfo = fmt.Sprintf("'%s'", ctx.TargetHost)
	}

	ctx.UserFacingMsg = fmt.Sprintf(
		"🌐 DNS resolution failed for %s\n"+
			"   The hostname could not be resolved to an IP address.\n"+
			"   This often indicates a typo in the hostname or DNS configuration issues.",
		hostInfo,
	)

	troubleshooting := []string{
		"Double-check the hostname for typos (common with FQDNs)",
		"Verify the hostname is correct and exists",
	}

	if ctx.TargetHost != "" {
		troubleshooting = append(troubleshooting,
			fmt.Sprintf("Test DNS resolution: nslookup %s", ctx.TargetHost),
			fmt.Sprintf("Try ping: ping %s", ctx.TargetHost),
		)
	}

	troubleshooting = append(troubleshooting,
		"Check your DNS server configuration (/etc/resolv.conf)",
		"Verify network connectivity is working",
		"Try using IP address instead of hostname as a workaround",
		"Check if the domain name is registered and active",
	)

	ctx.TroubleShooting = troubleshooting
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
// Returns true for permanent failures (404, auth, connection refused, DNS errors, etc.)
func (ctx *ErrorContext) ShouldOpenCircuit() bool {
	switch ctx.ErrorType {
	case "rtsp_404", "auth_failed", "auth_forbidden", "connection_refused",
		"no_route", "protocol_error", "dns_resolution_failed",
		"operation_not_permitted", "ssl_error":
		return true // Permanent failures - require configuration fix
	case "connection_timeout", "invalid_data", "eof", "network_unreachable", "rtsp_503":
		return false // Transient failures - allow retry
	default:
		return false
	}
}

// ShouldRestart determines if this error should trigger an automatic restart.
// Returns true for transient failures that might recover on retry.
func (ctx *ErrorContext) ShouldRestart() bool {
	switch ctx.ErrorType {
	case "connection_timeout", "invalid_data", "eof", "network_unreachable", "rtsp_503":
		return true // Transient failures - might recover
	default:
		return false
	}
}
