// Package privacy provides privacy-focused utility functions for handling sensitive data
// such as URL sanitization, message scrubbing, and system ID generation.
package privacy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Constants for system ID generation and validation
const (
	// systemIDRandomBytes is the number of random bytes used to generate a system ID
	// (6 bytes = 12 hex characters, formatted as XXXX-XXXX-XXXX)
	systemIDRandomBytes = 6

	// systemIDLength is the total length of a formatted system ID including hyphens
	systemIDLength = 14
)

// Constants for domain categorization
const (
	// minDomainParts is the minimum number of parts required for a valid domain
	minDomainParts = 2

	// minPartsForTwoPartTLD is the minimum number of parts to check for two-part TLDs
	minPartsForTwoPartTLD = 3
)

// Constants for API token scrubbing
const (
	// minTokenParts is the minimum number of parts expected in a "with token" pattern
	minTokenParts = 2
)

// Redaction markers for consistent output across all sanitization functions
const (
	// RedactedMarker is the standard marker for completely redacted sensitive data
	RedactedMarker = "[REDACTED]"

	// EmptyUserMarker is used when an empty username is provided
	EmptyUserMarker = "[EMPTY_USER]"

	// EmptyPasswordMarker is used when an empty password is provided
	EmptyPasswordMarker = "[EMPTY_PASSWORD]"

	// EmptyTokenMarker is used when an empty token is provided
	EmptyTokenMarker = "[EMPTY_TOKEN]"
)

// Constants for hash slice lengths used in anonymization
const (
	// hashLenShort is 4 bytes (8 hex chars) used for path segment hashes
	hashLenShort = 4

	// hashLenMedium is 8 bytes (16 hex chars) used for general anonymization hashes
	hashLenMedium = 8

	// hashLenLong is 12 bytes (24 hex chars) used for URL hashes
	hashLenLong = 12
)

// Constants for URL normalization
const (
	// maxURLParts is the maximum number of parts in a normalized URL
	// (scheme, host type, port, path structure)
	maxURLParts = 4
)

// Constants for system ID format positions
const (
	// systemIDSegmentLen is the length of each segment in the system ID (XXXX)
	systemIDSegmentLen = 4

	// firstHyphenPos is the position of the first hyphen in XXXX-XXXX-XXXX format
	firstHyphenPos = systemIDSegmentLen

	// secondHyphenPos is the position of the second hyphen in XXXX-XXXX-XXXX format
	secondHyphenPos = 9
)

// Constants for path handling
const (
	// minWindowsPathLen is the minimum length for a Windows drive path (e.g., "C:")
	minWindowsPathLen = 2
)

// Constants for user agent parsing
const (
	// maxUserAgentComponents is the maximum number of components in a parsed user agent
	// (Bot + Browser + OS)
	maxUserAgentComponents = 3
)

// Path separator constants
const (
	// unixPathSeparator is the path separator used on Unix-like systems
	unixPathSeparator = "/"

	// windowsPathSeparator is the path separator used on Windows systems
	windowsPathSeparator = "\\"
)

// Pre-compiled patterns for better performance
// Avoiding regex compilation on every call (see issue #825: regex allocation in hot paths)
var (
	// URL pattern for finding URLs in text
	urlPattern = regexp.MustCompile(`\b(?:https?|rtsp|rtmp)://\S+`)

	// Email pattern - matches standard email addresses
	emailPattern = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`)

	// UUID pattern - matches standard UUID formats (8-4-4-4-12)
	uuidPattern = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)

	// Standalone IP address pattern - matches IPv4 and IPv6 addresses not in URLs
	// We use word boundaries and check context in the replacement function
	ipv4Pattern = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\b`)
	ipv6Pattern = regexp.MustCompile(`\b(?:[0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}\b`)

	// GPS coordinates pattern - matches decimal degree coordinates
	coordinatesPattern = regexp.MustCompile(`(?:lat(?:itude)?|lng|lon|longitude)[:=]?\s*-?\d{1,3}\.?\d*[,\s]+(?:lng|lon|longitude)[:=]?\s*-?\d{1,3}\.?\d*|(?:lat(?:itude)?[:=]?\s*)?-?\d{1,3}\.?\d*[,\s]+-?\d{1,3}\.?\d*`)

	// Enhanced API token/key pattern - includes bearer tokens and more formats
	// Requires a separator (: or =) between the key name and the token value
	apiTokenPattern = regexp.MustCompile(`(?i)(?:(?:api[_-]?key|token|secret|auth)[:=]\s*|(?:bearer)(?:\s+token)?[:=\s]+|(?:with\s+(?:token|key|secret|auth))\s+)([A-Za-z0-9+/\-_]{8,}[A-Za-z0-9+/=]*)`)

	// Token extraction pattern for replacing just the token part
	tokenRegex = regexp.MustCompile(`([A-Za-z0-9+/\-_]{8,}[A-Za-z0-9+/=]*)`)

	// Separator normalization pattern for API tokens
	separatorRegex = regexp.MustCompile(`[:=]\s*`)

	// RTSP URL pattern for finding and sanitizing RTSP URLs with credentials
	// Supports various formats including IPv6 addresses in brackets
	rtspURLPattern = regexp.MustCompile(`rtsp://(?:[^:]+:[^@]+@)?(?:\[[0-9a-fA-F:]+\]|[^/:\s]+)(?::[0-9]+)?(?:/[^\s]*)?`)

	// FFmpeg error prefix pattern - matches memory addresses like [rtsp @ 0x55d4a4808980]
	ffmpegPrefixPattern = regexp.MustCompile(`\[\w+\s*@\s*0x[0-9a-fA-F]+\]\s*`)

	// URL credential scrubbing patterns (for fallback when URL parsing fails)
	urlCredPattern  = regexp.MustCompile(`(://)[^:@/]+:[^@/]+@`)
	urlTokenPattern = regexp.MustCompile(`(://)[^:@/]+@`)

	// Notification URL token patterns (Telegram bot tokens, Discord webhooks, etc.)
	botTokenPattern = regexp.MustCompile(`/bot[A-Za-z0-9:_-]{20,}/`)
	webhookPattern  = regexp.MustCompile(`/\d{15,}/[A-Za-z0-9_-]{50,}`)
)

// Common two-part TLDs that need special handling
var commonTwoPartTLDs = map[string]bool{
	"co.uk": true, "co.nz": true, "co.za": true, "co.jp": true,
	"gov.uk": true, "gov.au": true, "gov.ca": true,
	"ac.uk": true, "edu.au": true, "org.uk": true,
	"net.au": true, "com.au": true,
}

// commonStreamNames contains common, non-sensitive stream name patterns
var commonStreamNames = []string{"stream", "live", "rtsp", "video", "audio", "feed", "cam", "camera"}

// userAgentPattern represents a pattern for detecting browser or OS in user agent strings
type userAgentPattern struct {
	name      string
	pattern   *regexp.Regexp
	isBrowser bool
}

// Pre-compiled user agent patterns for browser and OS detection
// Order matters - check more specific patterns first
var userAgentPatterns = []userAgentPattern{
	// Browsers (check Edge before Chrome since Edge contains Chrome string)
	{"Edge", regexp.MustCompile(`(?i)Edg/[\d.]+`), true},
	{"Opera", regexp.MustCompile(`(?i)Opera/[\d.]+|OPR/[\d.]+`), true},
	{"Chrome", regexp.MustCompile(`(?i)Chrome/[\d.]+`), true},
	{"Firefox", regexp.MustCompile(`(?i)Firefox/[\d.]+`), true},
	{"Safari", regexp.MustCompile(`(?i)Safari/[\d.]+`), true},
	// Operating Systems
	{"Windows", regexp.MustCompile(`(?i)Windows NT [\d.]+`), false},
	{"Mac", regexp.MustCompile(`(?i)Mac OS X [\d._]+`), false},
	{"Android", regexp.MustCompile(`(?i)Android [\d.]+`), false},
	{"iOS", regexp.MustCompile(`(?i)iPhone OS [\d._]+`), false},
	{"Linux", regexp.MustCompile(`(?i)Linux`), false},
}

// ScrubMessage removes or anonymizes sensitive information from telemetry messages
// It finds URLs and other sensitive data in the message and replaces them with anonymized versions
func ScrubMessage(message string) string {
	// Apply all scrubbing functions in sequence
	result := urlPattern.ReplaceAllStringFunc(message, AnonymizeURL)
	result = ScrubEmails(result)
	result = ScrubUUIDs(result)
	result = ScrubStandaloneIPs(result)
	result = ScrubCoordinates(result)
	result = ScrubAPITokens(result)
	return result
}

// AnonymizeURL converts a URL to an anonymized form while preserving debugging value
// It maintains the URL structure but removes sensitive information like credentials,
// hostnames, and paths while preserving categorization for debugging
func AnonymizeURL(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		// If parsing fails, create a hash of the raw string
		hash := sha256.Sum256([]byte(rawURL))
		return fmt.Sprintf("url-hash-%x", hash[:hashLenMedium])
	}

	// Create a normalized version for hashing
	// Include scheme, host pattern, and path structure but remove sensitive data
	normalizedParts := make([]string, 0, maxURLParts)

	// Include scheme (rtsp, http, etc.)
	if parsedURL.Scheme != "" {
		normalizedParts = append(normalizedParts, parsedURL.Scheme)
	}

	// Anonymize hostname/IP
	host := parsedURL.Hostname()
	if host != "" {
		hostType := categorizeHost(host)
		normalizedParts = append(normalizedParts, hostType)
	}

	// Include port if present
	if parsedURL.Port() != "" {
		normalizedParts = append(normalizedParts, "port-"+parsedURL.Port())
	}

	// Include path structure (without sensitive details)
	if parsedURL.Path != "" && parsedURL.Path != "/" {
		pathStructure := anonymizeURLPath(parsedURL.Path)
		normalizedParts = append(normalizedParts, pathStructure)
	}

	// Create consistent hash
	normalized := strings.Join(normalizedParts, ":")
	hash := sha256.Sum256([]byte(normalized))

	return fmt.Sprintf("url-%x", hash[:hashLenLong])
}

// SanitizeRTSPUrl removes sensitive information from RTSP URL and returns a display-friendly version
// It strips credentials while preserving the host, port, and path for debugging
func SanitizeRTSPUrl(source string) string {
	// Parse the URL using standard library
	parsedURL, err := url.Parse(source)
	if err != nil {
		// If parsing fails, return original to avoid data loss
		return source
	}

	// Only process RTSP URLs
	if parsedURL.Scheme != "rtsp" {
		return source
	}

	// Remove user credentials only (keeps path, query, and fragment for debugging)
	parsedURL.User = nil

	return parsedURL.String()
}

// SanitizeRTSPUrls finds and sanitizes all RTSP URLs in a given text
// It uses regex pattern matching to identify RTSP URLs and replaces them with sanitized versions
func SanitizeRTSPUrls(text string) string {
	return rtspURLPattern.ReplaceAllStringFunc(text, SanitizeRTSPUrl)
}

// SanitizeFFmpegError removes memory addresses from FFmpeg error messages to enable proper deduplication
// It removes prefixes like "[rtsp @ 0x55d4a4808980]" which contain unique memory addresses
func SanitizeFFmpegError(text string) string {
	// First remove FFmpeg memory address prefixes
	text = ffmpegPrefixPattern.ReplaceAllString(text, "")
	// Then sanitize any RTSP URLs
	return SanitizeRTSPUrls(text)
}

// GenerateSystemID creates a unique system identifier
// The ID is 12 characters long, URL-safe, and case-insensitive
// Format: XXXX-XXXX-XXXX (14 chars total with hyphens)
func GenerateSystemID() (string, error) {
	// Generate random bytes (will become 12 hex characters)
	bytes := make([]byte, systemIDRandomBytes)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Convert to hex string (12 characters)
	id := hex.EncodeToString(bytes)

	// Format as XXXX-XXXX-XXXX for readability
	formatted := fmt.Sprintf("%s-%s-%s",
		id[0:systemIDSegmentLen],
		id[systemIDSegmentLen:2*systemIDSegmentLen],
		id[2*systemIDSegmentLen:])

	return strings.ToUpper(formatted), nil
}

// IsValidSystemID checks if a system ID has the correct format
func IsValidSystemID(id string) bool {
	// Check format: XXXX-XXXX-XXXX (14 chars total)
	if len(id) != systemIDLength {
		return false
	}

	// Check hyphens at correct positions
	if id[firstHyphenPos] != '-' || id[secondHyphenPos] != '-' {
		return false
	}

	// Check that all other characters are hex
	for i, char := range id {
		if i == firstHyphenPos || i == secondHyphenPos {
			continue // Skip hyphens
		}
		if !isHexChar(char) {
			return false
		}
	}

	return true
}

// ScrubEmails removes or anonymizes email addresses from text messages
// It replaces email addresses with [EMAIL] placeholder
func ScrubEmails(message string) string {
	return emailPattern.ReplaceAllString(message, "[EMAIL]")
}

// ScrubUUIDs removes or anonymizes UUIDs from text messages
// It replaces UUIDs with [UUID] placeholder
func ScrubUUIDs(message string) string {
	return uuidPattern.ReplaceAllString(message, "[UUID]")
}

// ScrubStandaloneIPs removes or anonymizes standalone IP addresses from text messages
// It handles both IPv4 and IPv6 addresses that are not part of URLs
func ScrubStandaloneIPs(message string) string {
	// First mark all URLs to avoid processing IPs within them
	urlPositions := make(map[int]int) // start -> end position of URLs
	urlMatches := urlPattern.FindAllStringIndex(message, -1)
	for _, match := range urlMatches {
		urlPositions[match[0]] = match[1]
	}

	// Helper function to check if position is within a URL
	isInURL := func(start, end int) bool {
		for urlStart, urlEnd := range urlPositions {
			if start >= urlStart && end <= urlEnd {
				return true
			}
		}
		return false
	}

	// Find all IP matches in the original message to ensure coordinates
	// are consistent with URL positions (avoids stale coordinate bug)
	ipv4Matches := ipv4Pattern.FindAllStringIndex(message, -1)
	ipv6Matches := ipv6Pattern.FindAllStringIndex(message, -1)

	// Combine and sort by position to process in order
	allMatches := make([][]int, 0, len(ipv4Matches)+len(ipv6Matches))
	allMatches = append(allMatches, ipv4Matches...)
	allMatches = append(allMatches, ipv6Matches...)
	sort.Slice(allMatches, func(i, j int) bool {
		return allMatches[i][0] < allMatches[j][0]
	})

	return replaceIPMatches(message, allMatches, isInURL)
}

// ScrubCoordinates removes or anonymizes GPS coordinates from text messages
// It replaces coordinate pairs with generic placeholders while preserving message structure
func ScrubCoordinates(message string) string {
	return coordinatesPattern.ReplaceAllString(message, "[LAT],[LON]")
}

// ScrubAPITokens removes or anonymizes API tokens, keys, and secrets from text messages
// It replaces tokens with generic placeholders while preserving message structure
func ScrubAPITokens(message string) string {
	return apiTokenPattern.ReplaceAllStringFunc(message, func(match string) string {
		// Check if it's a bearer token
		lowerMatch := strings.ToLower(match)
		if strings.Contains(lowerMatch, "bearer") {
			return "Bearer [TOKEN]"
		}
		// Check if it's "with token/key/etc" pattern
		if strings.HasPrefix(lowerMatch, "with ") {
			// Extract the keyword (token, key, etc)
			parts := strings.Fields(match)
			if len(parts) >= minTokenParts {
				return parts[0] + " " + parts[1] + " [TOKEN]"
			}
		}
		// Use pre-compiled regex to find and replace just the token part within the match
		result := tokenRegex.ReplaceAllString(match, "[TOKEN]")
		// Normalize separators to colon for consistency using pre-compiled regex
		result = separatorRegex.ReplaceAllString(result, ": ")
		return result
	})
}

// ScrubUsername anonymizes usernames for safe logging.
// Returns a consistent hash prefix to enable log correlation without exposing the actual username.
// The same username will always produce the same hash, allowing correlation across log entries.
func ScrubUsername(username string) string {
	if username == "" {
		return EmptyUserMarker
	}
	hash := sha256.Sum256([]byte(username))
	return fmt.Sprintf("user-%x", hash[:hashLenShort])
}

// ScrubPassword completely redacts passwords for safe logging.
// Always returns RedactedMarker regardless of input to prevent any information leakage.
func ScrubPassword(password string) string {
	if password == "" {
		return EmptyPasswordMarker
	}
	return RedactedMarker
}

// ScrubToken redacts tokens with a length hint for debugging.
// The length hint helps with debugging without exposing the actual token value.
func ScrubToken(token string) string {
	if token == "" {
		return EmptyTokenMarker
	}
	return fmt.Sprintf("[TOKEN:len=%d]", len(token))
}

// ScrubCredentialURL sanitizes URLs that may contain embedded credentials.
// Handles common notification service URL formats (telegram://, discord://, slack://, etc.)
// and generic URLs with userinfo (user:pass@host).
func ScrubCredentialURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		// If parsing fails, do a simple credential pattern replacement
		return scrubURLCredentialsSimple(rawURL)
	}

	// Remove userinfo (credentials) from URL
	if parsedURL.User != nil {
		parsedURL.User = url.User(RedactedMarker)
	}

	// Also check for common notification URL patterns with tokens in path/query
	result := parsedURL.String()

	// Scrub any remaining token-like patterns in the path
	result = scrubURLTokenPatterns(result)

	return result
}

// scrubURLCredentialsSimple performs simple regex-based credential scrubbing
// for cases where URL parsing fails.
// Uses pre-compiled patterns for performance (see issue #825).
func scrubURLCredentialsSimple(rawURL string) string {
	// Pattern: scheme://user:pass@host or scheme://token@host
	result := urlCredPattern.ReplaceAllString(rawURL, "$1[REDACTED]@")

	// Pattern: scheme://token@host (single value before @)
	result = urlTokenPattern.ReplaceAllString(result, "$1[REDACTED]@")

	return result
}

// scrubURLTokenPatterns removes token-like patterns from URL paths and queries.
// Uses pre-compiled patterns for performance (see issue #825).
func scrubURLTokenPatterns(urlStr string) string {
	// Common bot token patterns (Telegram: bot<token>, Discord webhooks, etc.)
	result := botTokenPattern.ReplaceAllString(urlStr, "/bot[TOKEN]/")

	// Webhook ID:token patterns
	result = webhookPattern.ReplaceAllString(result, "/[WEBHOOK_ID]/[TOKEN]")

	return result
}

// categorizeHost anonymizes hostnames while preserving useful categorization
func categorizeHost(host string) string {
	// Check for localhost patterns
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return "localhost"
	}

	// Check for private IP ranges using RFC-compliant detection
	if IsPrivateIP(host) {
		return "private-ip"
	}

	// Check for public IP
	if isIPAddress(host) {
		return "public-ip"
	}

	// For domain names, handle multi-part TLDs properly
	return categorizeDomain(host)
}

// categorizeDomain properly handles domain classification including multi-part TLDs
func categorizeDomain(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < minDomainParts {
		return "unknown-host"
	}

	// Check for common two-part TLDs (e.g., co.uk, gov.au)
	if len(parts) >= minPartsForTwoPartTLD {
		twoPartTLD := parts[len(parts)-2] + "." + parts[len(parts)-1]
		if commonTwoPartTLDs[strings.ToLower(twoPartTLD)] {
			return "domain-" + strings.ToLower(twoPartTLD)
		}
	}

	// Use the last part as TLD for regular domains
	tld := parts[len(parts)-1]
	return "domain-" + strings.ToLower(tld)
}

// anonymizeURLPath creates a structure-preserving but privacy-safe path representation for URLs
func anonymizeURLPath(path string) string {
	// Remove leading/trailing slashes for processing
	path = strings.Trim(path, "/")
	if path == "" {
		return "path-root"
	}

	// Split path into segments
	segments := strings.Split(path, "/")
	anonymizedSegments := make([]string, 0, len(segments))

	for _, segment := range segments {
		if segment == "" {
			continue
		}

		// Check for common patterns that might be safe to preserve
		switch {
		case isCommonStreamName(segment):
			anonymizedSegments = append(anonymizedSegments, "path-stream")
		case isNumeric(segment):
			anonymizedSegments = append(anonymizedSegments, "path-numeric")
		default:
			// Hash individual segments to maintain path structure
			hash := sha256.Sum256([]byte(segment))
			anonymizedSegments = append(anonymizedSegments, fmt.Sprintf("path-seg-%x", hash[:hashLenShort]))
		}
	}

	return strings.Join(anonymizedSegments, "/")
}

// IsPrivateIP checks if the host is a private IP address using net.ParseIP and enhanced classification
func IsPrivateIP(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	// Check for RFC 1918 private addresses using IsPrivate()
	if ip.IsPrivate() {
		return true
	}

	// Check for additional "internal" ranges that should be considered private for privacy purposes
	if ip.IsLoopback() {
		return true
	}

	if ip.IsLinkLocalUnicast() {
		return true
	}

	// Check for IPv6 multicast that should be considered internal
	if ip.IsMulticast() && ip.To4() == nil {
		return true
	}

	return false
}

// isIPAddress checks if the host is a valid IP address using net.ParseIP
func isIPAddress(host string) bool {
	return net.ParseIP(host) != nil
}

// isCommonStreamName checks if a path segment is a common, non-sensitive stream name
func isCommonStreamName(segment string) bool {
	segment = strings.ToLower(segment)
	for _, name := range commonStreamNames {
		if strings.Contains(segment, name) {
			return true
		}
	}
	return false
}

// isNumeric checks if a string is purely numeric
func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// isHexChar checks if a rune is a valid hex character
func isHexChar(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') || (r >= 'a' && r <= 'f')
}

// replaceIPMatches replaces IP addresses in a message with their anonymized versions
// It takes the original message, regex matches, and a function to check if a position is within a URL
func replaceIPMatches(message string, matches [][]int, isInURL func(int, int) bool) string {
	if len(matches) == 0 {
		return message
	}

	var offset int
	result := message
	for _, match := range matches {
		if isInURL(match[0], match[1]) {
			continue
		}
		ip := message[match[0]:match[1]]
		anonymized := AnonymizeIP(ip)
		adjustedStart := match[0] + offset
		adjustedEnd := match[1] + offset
		result = result[:adjustedStart] + anonymized + result[adjustedEnd:]
		offset += len(anonymized) - (match[1] - match[0])
	}
	return result
}

// AnonymizeIP anonymizes IP addresses while preserving type information
// It distinguishes between private and public IPs and applies consistent hashing
func AnonymizeIP(ipStr string) string {
	if ipStr == "" {
		return ""
	}

	// Try to parse as IP first
	ip := net.ParseIP(ipStr)
	if ip == nil {
		// Not a valid IP, return a generic hash
		hash := sha256.Sum256([]byte(ipStr))
		return fmt.Sprintf("invalid-ip-%x", hash[:hashLenMedium])
	}

	// Categorize the IP
	category := categorizeHost(ip.String())

	// Create a hash of the IP
	hash := sha256.Sum256([]byte(ip.String()))

	// Return categorized anonymized IP
	return fmt.Sprintf("%s-%x", category, hash[:hashLenMedium])
}

// isAbsolutePath checks if a path is absolute (Unix or Windows)
func isAbsolutePath(path string) bool {
	return strings.HasPrefix(path, unixPathSeparator) || (len(path) >= minWindowsPathLen && path[1] == ':')
}

// getPathSeparator returns the appropriate path separator based on the original path
func getPathSeparator(path string) string {
	if strings.Contains(path, windowsPathSeparator) {
		return windowsPathSeparator
	}
	return unixPathSeparator
}

// anonymizePathSegment anonymizes a single path segment, preserving file extensions for the last segment
func anonymizePathSegment(segment string, isLastSegment bool) string {
	if segment == "" {
		return ""
	}

	// Keep file extensions visible for debugging
	ext := ""
	if isLastSegment {
		if idx := strings.LastIndex(segment, "."); idx > 0 {
			ext = segment[idx:]
			segment = segment[:idx]
		}
	}

	// Hash the segment
	hash := sha256.Sum256([]byte(segment))
	return fmt.Sprintf("path-%x%s", hash[:hashLenShort], ext)
}

// AnonymizePath anonymizes file paths while preserving structure information
// It replaces path segments with hashes but maintains the path hierarchy
func AnonymizePath(path string) string {
	if path == "" {
		return ""
	}

	isAbsolute := isAbsolutePath(path)

	segments := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})

	if len(segments) == 0 {
		return "empty-path"
	}

	anonymized := make([]string, len(segments))
	for i, segment := range segments {
		anonymized[i] = anonymizePathSegment(segment, i == len(segments)-1)
	}

	separator := getPathSeparator(path)
	result := strings.Join(anonymized, separator)

	if isAbsolute && !strings.HasPrefix(result, separator) {
		result = separator + result
	}

	return result
}

// isBot checks if the user agent string indicates a bot, crawler, or spider
func isBot(userAgent string) bool {
	lowerUA := strings.ToLower(userAgent)
	return strings.Contains(lowerUA, "bot") ||
		strings.Contains(lowerUA, "crawler") ||
		strings.Contains(lowerUA, "spider")
}

// extractUserAgentComponents extracts browser and OS components from a user agent string
// isBotAgent indicates whether the user agent was identified as a bot/crawler
func extractUserAgentComponents(userAgent string, isBotAgent bool) []string {
	components := make([]string, 0, maxUserAgentComponents)
	foundBrowser := isBotAgent
	foundOS := false

	if isBotAgent {
		components = append(components, "Bot")
	}

	for _, p := range userAgentPatterns {
		if !p.pattern.MatchString(userAgent) {
			continue
		}

		if p.isBrowser && !foundBrowser {
			components = append(components, p.name)
			foundBrowser = true
		} else if !p.isBrowser && !foundOS {
			components = append(components, p.name)
			foundOS = true
		}

		if foundBrowser && foundOS {
			break
		}
	}

	return components
}

// RedactUserAgent anonymizes user agent strings to prevent tracking
// It preserves browser and OS type information while removing version details
func RedactUserAgent(userAgent string) string {
	if userAgent == "" {
		return ""
	}

	components := extractUserAgentComponents(userAgent, isBot(userAgent))

	if len(components) == 0 {
		hash := sha256.Sum256([]byte(userAgent))
		return fmt.Sprintf("ua-%x", hash[:hashLenMedium])
	}

	return strings.Join(components, " ")
}
