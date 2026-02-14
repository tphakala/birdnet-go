package myaudio

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/privacy"
)

// FFmpeg error type constants for categorizing error conditions.
const (
	ErrTypeConnectionTimeout   = "connection_timeout"
	ErrTypeNetworkUnreachable  = "network_unreachable"
	ErrTypeRTSP503             = "rtsp_503"
	ErrTypeInvalidData         = "invalid_data"
	ErrTypeEOF                 = "eof"
	ErrTypeRTSP404             = "rtsp_404"
	ErrTypeConnectionRefused   = "connection_refused"
	ErrTypeAuthFailed          = "auth_failed"
	ErrTypeAuthForbidden       = "auth_forbidden"
	ErrTypeNoRoute             = "no_route"
	ErrTypeOperationNotPermit  = "operation_not_permitted"
	ErrTypeSSLError            = "ssl_error"
	ErrTypeDNSResolutionFailed = "dns_resolution_failed"
	ErrTypeProtocolError       = "protocol_error"
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
	// Capture full TCP URL including credentials, IPv6, query params
	// Pattern: "Connection to tcp://..." - captures everything after "Connection to "
	reConnectionToTCP    = regexp.MustCompile(`Connection to (tcp://\S+)`)
	reRTSPStatus         = regexp.MustCompile(`Server returned (\d+)`)
	reSSLError           = regexp.MustCompile(`SSL.*error|TLS.*error|certificate.*error`)
)

// ErrorContext contains rich information extracted from FFmpeg output.
// It provides detailed diagnostics for user-facing error messages and troubleshooting.
type ErrorContext struct {
	ErrorType       string        // "connection_timeout", "rtsp_404", "auth_failed", etc.
	PrimaryMessage  string        // Main error message
	TargetHost      string        // Extracted host/IP (sanitized - no credentials)
	TargetPort      int           // Extracted port
	TimeoutDuration time.Duration // Extracted timeout (if applicable)
	HTTPStatus      int           // HTTP/RTSP status code (if applicable)
	RTSPMethod      string        // RTSP method that failed (if applicable)
	// RawFFmpegOutput stores the sanitized FFmpeg stderr output for debugging.
	// SECURITY: This field is sanitized using privacy.SanitizeFFmpegError() to remove
	// credentials from RTSP URLs (e.g., rtsp://user:pass@host → rtsp://***:***@host).
	// The json:"-" tag prevents accidental credential leakage via JSON marshaling.
	RawFFmpegOutput string `json:"-"` // Full FFmpeg output for debugging (sanitized)
	UserFacingMsg   string            // Friendly message for user
	TroubleShooting []string          // List of troubleshooting steps
	Timestamp       time.Time         // When this error was detected
}

// extractHostWithoutCredentials safely extracts hostname from a URL string,
// stripping any userinfo (username:password) to prevent credential leakage.
// Returns empty string if parsing fails.
//
// SECURITY: This prevents credentials from leaking into TargetHost field and logs.
// Example: "user:pass@camera.local:554" → "camera.local"
func extractHostWithoutCredentials(rawURL string) string {
	// Handle URLs that may not have a scheme
	if !strings.Contains(rawURL, "://") {
		rawURL = "rtsp://" + rawURL
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		// If parsing fails, try manual extraction but strip everything before @
		if atIdx := strings.LastIndex(rawURL, "@"); atIdx != -1 {
			rawURL = rawURL[atIdx+1:]
		}
		// Remove port if present for safety
		if host, _, found := strings.Cut(rawURL, ":"); found {
			return host
		}
		return rawURL
	}

	// Use url.Hostname() which automatically strips port and userinfo
	return parsed.Hostname()
}

// extractHostAndPortFromConnectionURL extracts sanitized host and port from a connection URL.
// This handles URLs like "tcp://host:port?timeout=..." properly using URL parsing.
//
// SECURITY: Uses url.Parse() to correctly handle IPv6, credentials, and query parameters.
// The old regex `tcp://([^:?]+):(\d+)` would fail on:
//   - IPv6: tcp://[::1]:8554 (stops at first colon)
//   - Credentials: tcp://user:pass@host:8554 (extracts "user" as host)
//
// Returns: (hostname, port, success)
func extractHostAndPortFromConnectionURL(connectionURL string) (hostname string, port int, ok bool) {
	parsed, err := url.Parse(connectionURL)
	if err != nil {
		return "", 0, false
	}

	// Extract hostname (strips userinfo and port automatically)
	hostname = parsed.Hostname()

	// Extract port
	portStr := parsed.Port()
	if portStr == "" {
		return hostname, 0, false
	}

	port, err = strconv.Atoi(portStr)
	if err != nil {
		return hostname, 0, false
	}

	return hostname, port, true
}

// ExtractErrorContext analyzes FFmpeg stderr and extracts context.
// It returns nil if no recognizable error pattern is found.
func ExtractErrorContext(stderrOutput string) *ErrorContext {
	if stderrOutput == "" {
		return nil
	}

	// SECURITY: Sanitize FFmpeg output to remove credentials from RTSP URLs
	// before storing. This prevents credential leakage via logs, health APIs, etc.
	sanitizedOutput := privacy.SanitizeFFmpegError(stderrOutput)

	ctx := &ErrorContext{
		RawFFmpegOutput: sanitizedOutput,
		Timestamp:       time.Now(),
	}

	// Check error patterns in order of specificity and diagnostic value.
	//
	// Precedence rules (most specific → most general):
	// 1. Socket-level errors (connection refused, timeout) - Most specific about network failure
	// 2. RTSP protocol errors (404, 401, 403, 503) - Application-layer specific issues
	// 3. Network-layer errors (no route, network unreachable) - Broader network configuration
	// 4. DNS errors - Name resolution layer
	// 5. Security/permission errors (SSL, operation not permitted) - System policy layer
	// 6. Data errors (invalid data, EOF) - Stream content issues
	// 7. Generic errors (protocol not found) - Catch-all patterns
	//
	// Why this order matters:
	// - "Connection timed out" is checked before "Network unreachable" because a timeout
	//   provides more specific diagnostic value (server is unreachable vs network routing issue)
	// - "No route to host" is checked before "Network unreachable" because EHOSTUNREACH
	//   indicates a specific routing table problem, while ENETUNREACH is broader
	// - RTSP 4xx/5xx status codes are checked early to provide application-layer context
	// - DNS errors are checked after connection errors to avoid masking connectivity issues
	//
	// Note: FFmpeg error messages can contain multiple patterns. The first match wins,
	// so order determines which error type is reported when patterns overlap.

	// Connection timeout - very common with unreachable hosts
	if strings.Contains(stderrOutput, "Connection timed out") {
		ctx.ErrorType = ErrTypeConnectionTimeout
		ctx.extractConnectionTimeout(stderrOutput)
		ctx.buildConnectionTimeoutMessage()
		return ctx
	}

	// RTSP 404 - stream path doesn't exist
	if strings.Contains(stderrOutput, "404 Not Found") {
		ctx.ErrorType = ErrTypeRTSP404
		ctx.extractRTSP404(stderrOutput)
		ctx.buildRTSP404Message()
		return ctx
	}

	// Connection refused - server not listening
	if strings.Contains(stderrOutput, "Connection refused") {
		ctx.ErrorType = ErrTypeConnectionRefused
		ctx.extractConnectionRefused(stderrOutput)
		ctx.buildConnectionRefusedMessage()
		return ctx
	}

	// Authentication failure
	if strings.Contains(stderrOutput, "401 Unauthorized") {
		ctx.ErrorType = ErrTypeAuthFailed
		ctx.extractAuthFailure(stderrOutput)
		ctx.buildAuthFailureMessage()
		return ctx
	}

	// 403 Forbidden
	if strings.Contains(stderrOutput, "403 Forbidden") {
		ctx.ErrorType = ErrTypeAuthForbidden
		ctx.extractAuthForbidden(stderrOutput)
		ctx.buildAuthForbiddenMessage()
		return ctx
	}

	// No route to host
	if strings.Contains(stderrOutput, "No route to host") {
		ctx.ErrorType = ErrTypeNoRoute
		ctx.extractNoRoute(stderrOutput)
		ctx.buildNoRouteMessage()
		return ctx
	}

	// Network unreachable - different from no route
	if strings.Contains(stderrOutput, "Network unreachable") {
		ctx.ErrorType = ErrTypeNetworkUnreachable
		ctx.extractNetworkUnreachable(stderrOutput)
		ctx.buildNetworkUnreachableMessage()
		return ctx
	}

	// Operation not permitted - firewall/SELinux
	if strings.Contains(stderrOutput, "Operation not permitted") {
		ctx.ErrorType = ErrTypeOperationNotPermit
		ctx.extractOperationNotPermitted(stderrOutput)
		ctx.buildOperationNotPermittedMessage()
		return ctx
	}

	// SSL/TLS errors for rtsps://
	if reSSLError.MatchString(stderrOutput) {
		ctx.ErrorType = ErrTypeSSLError
		ctx.extractSSLError(stderrOutput)
		ctx.buildSSLErrorMessage()
		return ctx
	}

	// RTSP 503 Service Unavailable - server overload
	if strings.Contains(stderrOutput, "503 Service Unavailable") {
		ctx.ErrorType = ErrTypeRTSP503
		ctx.extractRTSP503(stderrOutput)
		ctx.buildRTSP503Message()
		return ctx
	}

	// DNS resolution failures - common with typos in hostnames
	if strings.Contains(stderrOutput, "Name or service not known") ||
		strings.Contains(stderrOutput, "nodename nor servname provided") ||
		strings.Contains(stderrOutput, "Temporary failure in name resolution") ||
		strings.Contains(stderrOutput, "Could not resolve hostname") {
		ctx.ErrorType = ErrTypeDNSResolutionFailed
		ctx.extractDNSError(stderrOutput)
		ctx.buildDNSErrorMessage()
		return ctx
	}

	// Invalid data - stream corruption
	if strings.Contains(stderrOutput, "Invalid data found") {
		ctx.ErrorType = ErrTypeInvalidData
		ctx.extractInvalidData(stderrOutput)
		ctx.buildInvalidDataMessage()
		return ctx
	}

	// End of file - stream ended unexpectedly
	if strings.Contains(stderrOutput, "End of file") {
		ctx.ErrorType = ErrTypeEOF
		ctx.PrimaryMessage = "Stream ended unexpectedly"
		ctx.buildEOFMessage()
		return ctx
	}

	// Protocol not found
	if strings.Contains(stderrOutput, "Protocol not found") {
		ctx.ErrorType = ErrTypeProtocolError
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
	// Handle timeout duration display
	// - Positive duration: Show explicit timeout value (e.g., "10s")
	// - Zero duration: Indicates infinite timeout (no timeout was set, but connection still failed)
	//   This is unusual but can occur with timeout=0 in FFmpeg URLs
	// - Not extracted: Fall back to generic "configured timeout"
	timeout := "configured timeout"
	if ctx.TimeoutDuration > 0 {
		timeout = fmt.Sprintf("%.0fs", ctx.TimeoutDuration.Seconds())
	} else if ctx.TimeoutDuration == 0 {
		// Zero timeout means infinite wait was configured, but connection still timed out
		// This suggests the TCP stack itself gave up (typically after ~75 seconds on Linux)
		timeout = "TCP stack timeout (no application timeout was set)"
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
	// SECURITY: Use safe extraction to prevent credential leakage
	if matches := reErrorOpeningInput.FindStringSubmatch(output); len(matches) == 2 {
		ctx.TargetHost = extractHostWithoutCredentials(matches[1])
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
	// Use URL parsing to handle IPv6, credentials, and query parameters correctly
	if matches := reConnectionToTCP.FindStringSubmatch(output); len(matches) == 2 {
		if host, port, ok := extractHostAndPortFromConnectionURL(matches[1]); ok {
			ctx.TargetHost = host
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
	// Use URL parsing to handle IPv6, credentials, and query parameters correctly
	if matches := reConnectionToTCP.FindStringSubmatch(output); len(matches) == 2 {
		if host, port, ok := extractHostAndPortFromConnectionURL(matches[1]); ok {
			ctx.TargetHost = host
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
	// Use URL parsing to handle IPv6, credentials, and query parameters correctly
	if matches := reConnectionToTCP.FindStringSubmatch(output); len(matches) == 2 {
		if host, port, ok := extractHostAndPortFromConnectionURL(matches[1]); ok {
			ctx.TargetHost = host
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
	// Use URL parsing to handle IPv6, credentials, and query parameters correctly
	if matches := reConnectionToTCP.FindStringSubmatch(output); len(matches) == 2 {
		if host, port, ok := extractHostAndPortFromConnectionURL(matches[1]); ok {
			ctx.TargetHost = host
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
	// SECURITY: Use safe extraction to prevent credential leakage
	if matches := reErrorOpeningInput.FindStringSubmatch(output); len(matches) == 2 {
		ctx.TargetHost = extractHostWithoutCredentials(matches[1])
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
	// SECURITY: Use safe extraction to prevent credential leakage
	if matches := reErrorOpeningInput.FindStringSubmatch(output); len(matches) == 2 {
		ctx.TargetHost = extractHostWithoutCredentials(matches[1])
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
	// SECURITY: Use safe extraction to prevent credential leakage
	if matches := reErrorOpeningInput.FindStringSubmatch(output); len(matches) == 2 {
		ctx.TargetHost = extractHostWithoutCredentials(matches[1])
	}

	// Also try to extract from tcp:// pattern as fallback
	// SECURITY: Use URL parsing to prevent credential leakage, even though
	// DNS errors don't typically have credentials in tcp:// URLs
	if ctx.TargetHost == "" {
		if matches := reConnectionToTCP.FindStringSubmatch(output); len(matches) >= 2 {
			// Try to extract host and port from the connection URL
			if host, port, ok := extractHostAndPortFromConnectionURL(matches[1]); ok {
				ctx.TargetHost = host
				ctx.TargetPort = port
			} else {
				// Fallback: just extract hostname without port
				ctx.TargetHost = extractHostWithoutCredentials(matches[1])
			}
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
//
// Network unreachable handling:
// ENETUNREACH is treated as transient because it often occurs during network transitions
// (interface coming up, gateway being configured, switching networks). Unlike EHOSTUNREACH
// (no_route), which indicates a specific routing table problem, ENETUNREACH suggests
// the entire network is currently unavailable but may recover. We allow retry with
// exponential backoff via the existing circuit breaker graduated failure thresholds,
// which will eventually open the circuit if the network remains unreachable.
func (ctx *ErrorContext) ShouldOpenCircuit() bool {
	switch ctx.ErrorType {
	case ErrTypeRTSP404, ErrTypeAuthFailed, ErrTypeAuthForbidden, ErrTypeConnectionRefused,
		ErrTypeNoRoute, ErrTypeProtocolError, ErrTypeDNSResolutionFailed,
		ErrTypeOperationNotPermit, ErrTypeSSLError:
		return true // Permanent failures - require configuration fix
	case ErrTypeConnectionTimeout, ErrTypeInvalidData, ErrTypeEOF, ErrTypeNetworkUnreachable, ErrTypeRTSP503:
		return false // Transient failures - allow retry with backoff
	default:
		return false
	}
}

// ShouldRestart determines if this error should trigger an automatic restart.
// Returns true for transient failures that might recover on retry.
//
// Network unreachable is treated as transient with bounded retry:
// - Allows restart (returns true) to handle network transitions
// - Circuit breaker's graduated failure thresholds provide bounded retry
// - After circuitBreakerRapidThreshold (5) failures in < 5 seconds, circuit opens
// - This prevents infinite restarts while allowing recovery from brief network issues
func (ctx *ErrorContext) ShouldRestart() bool {
	switch ctx.ErrorType {
	case ErrTypeConnectionTimeout, ErrTypeInvalidData, ErrTypeEOF, ErrTypeNetworkUnreachable, ErrTypeRTSP503:
		return true // Transient failures - might recover with bounded retry
	default:
		return false
	}
}
