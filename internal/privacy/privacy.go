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
	"strconv"
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

	// stacktracePathSegments is the number of trailing path segments preserved by
	// AnonymizeStacktracePath for debugging context (e.g., "telemetry/sentry.go").
	stacktracePathSegments = 2

	// redactedPathPrefix is the prefix used to replace leading path segments in
	// AnonymizeStacktracePath.
	redactedPathPrefix = "<redacted>"
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

	// dateTimePattern matches the date-and-time prefix of a log line, in ISO,
	// US, European and dotted forms ("2026-10-16 00:12:34", "10/16/2026 00:12"),
	// with optional fractional seconds (".015573" or ",123") and a Z or numeric
	// UTC offset. An offset needs minutes ("+0200", "-05:00") so that a negative
	// latitude after the time ("-45.12") is not read as one. The date and the
	// time are separated by "T", a space or ", " ("10/16/2026, 00:12:34").
	// coordinatesPattern's keyword-less alternative reads the "-16 00" of such
	// a prefix as a coordinate pair, so ScrubCoordinates keeps these spans
	// out of the text it scrubs. Slash dates without a time are protected
	// through standaloneSlashDateSpans.
	dateTimePattern = regexp.MustCompile(`\d{1,4}[-/.]\d{1,2}[-/.]\d{1,4}(?:T| |, )\d{1,2}:\d{2}(?::\d{2})?(?:\.\d+|,\d{3,9})?(?:Z|\s?[+-]\d{2}:?\d{2})?`)

	// syslogTimePattern matches the "Oct 16 00:12:34" prefix of a syslog line,
	// for the same reason as dateTimePattern.
	syslogTimePattern = regexp.MustCompile(`(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+\d{1,2}:\d{2}(?::\d{2})?(?:\.\d+|,\d{3,9})?`)

	// slashDatePattern finds slash date candidates such as "10/16/2026" or
	// "2026/10/16". The boundary rules that tell a date from a piece of a path
	// or of a longer number live in standaloneSlashDateSpans, because RE2 has
	// no lookaround.
	slashDatePattern = regexp.MustCompile(`\b\d{1,4}/\d{1,2}/\d{1,4}`)

	// Enhanced API token/key pattern - includes bearer tokens and more formats
	// Requires a separator (: or =) between the key name and the token value
	apiTokenPattern = regexp.MustCompile(`(?i)(?:(?:api[_-]?key|token|secret|auth)[:=]\s*|(?:bearer)(?:\s+token)?[:=\s]+|(?:with\s+(?:token|key|secret|auth))\s+)([A-Za-z0-9+/\-_]{8,}[A-Za-z0-9+/=]*)`)

	// Token extraction pattern for replacing just the token part
	tokenRegex = regexp.MustCompile(`([A-Za-z0-9+/\-_]{8,}[A-Za-z0-9+/=]*)`)

	// Separator normalization pattern for API tokens
	separatorRegex = regexp.MustCompile(`[:=]\s*`)

	// streamURLPattern for finding and sanitizing stream URLs with credentials
	// Supports RTSP, RTSPS, HTTP, HTTPS, RTMP, RTMPS, UDP, and RTP URLs
	// including IPv6 addresses in brackets
	streamURLPattern = regexp.MustCompile(`(?:rtsps?|https?|rtmps?|udp|rtp)://(?:[^:]+:[^@]+@)?(?:\[[0-9a-fA-F:]+\]|[^/:\s]+)(?::[0-9]+)?(?:/[^\s]*)?`)

	// FFmpeg error prefix pattern - matches memory addresses like [rtsp @ 0x55d4a4808980]
	ffmpegPrefixPattern = regexp.MustCompile(`\[\w+\s*@\s*0x[0-9a-fA-F]+\]\s*`)

	// URL credential scrubbing patterns (for fallback when URL parsing fails)
	urlCredPattern  = regexp.MustCompile(`(://)[^:@/]+:[^@/]+@`)
	urlTokenPattern = regexp.MustCompile(`(://)[^:@/]+@`)

	// Notification URL token patterns (Telegram bot tokens, Discord webhooks, etc.)
	botTokenPattern = regexp.MustCompile(`/bot[A-Za-z0-9:_-]{20,}/`)
	webhookPattern  = regexp.MustCompile(`/\d{15,}/[A-Za-z0-9_-]{50,}`)

	// File path patterns - matches absolute Unix and Windows paths
	// Unix: /home/user/..., /var/lib/...
	// Windows: C:\Users\John Doe\AppData\... (supports spaces in path segments)
	filePathPattern = regexp.MustCompile(`(?:(?:/[a-zA-Z0-9 ._-]+){2,}(?:\.[a-zA-Z0-9]+)?|[a-zA-Z]:\\(?:[a-zA-Z0-9 ._-]+\\){1,}[a-zA-Z0-9 ._-]+)`)
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
// It finds URLs and other sensitive data in the message and replaces them with anonymized versions.
// USB serial numbers in "usb-id:" device tokens are masked first, which keeps
// the vendor and product ids readable for the IP address stage after it.
func ScrubMessage(message string) string {
	// Apply all scrubbing functions in sequence
	result := scrubUSBIDSerials(message)
	result = urlPattern.ReplaceAllStringFunc(result, AnonymizeURL)
	result = ScrubEmails(result)
	result = ScrubUUIDs(result)
	result = ScrubStandaloneIPs(result)
	result = ScrubCoordinates(result)
	result = ScrubAPITokens(result)
	result = ScrubFilePaths(result)
	return result
}

// ScrubQueryString redacts credential-bearing values from a raw URL query string
// before it is logged. It percent-decodes the query first because ScrubMessage's
// token scrubber pattern does not span percent-escapes, so an encoded token value
// (for example token=ab%2Bcd1234) would otherwise slip through unredacted; on a
// decode error it falls back to scrubbing the raw string. url.PathUnescape is used
// rather than url.QueryUnescape because the latter turns a literal '+' into a space,
// which would split a base64 token value (token=ab+cd...) and leak its tail past the
// scrubber; PathUnescape still decodes %2B to '+' but preserves a literal '+', so the
// whole token value stays a single matchable run. ScrubMessage also redacts URLs,
// emails, UUIDs, IPs, coordinates, and file paths.
func ScrubQueryString(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	decoded, err := url.PathUnescape(rawQuery)
	if err != nil {
		decoded = rawQuery
	}
	return ScrubMessage(decoded)
}

// ScrubFilePaths anonymizes file paths in text messages using AnonymizePath.
// Standalone slash dates such as "10/16/2026" are not paths and are left as
// they are; a path match that runs into such a date ends where the date begins.
func ScrubFilePaths(message string) string {
	loc := filePathPattern.FindStringIndex(message)
	if loc == nil {
		return message
	}
	if !strings.Contains(message, "/") {
		return filePathPattern.ReplaceAllStringFunc(message, AnonymizePath)
	}
	spans := standaloneSlashDateSpans(message)
	if len(spans) == 0 {
		return filePathPattern.ReplaceAllStringFunc(message, AnonymizePath)
	}

	var out strings.Builder
	pos := 0
	next := 0 // index of the first span that may still overlap a match
	for loc != nil {
		start, end := pos+loc[0], pos+loc[1]
		for next < len(spans) && spans[next][1] <= start {
			next++
		}
		if next < len(spans) && spans[next][0] < end {
			// The match overlaps a date: anonymize the part before it, keep the
			// date, and resume after it.
			span := spans[next]
			if span[0] > start {
				out.WriteString(message[pos:start])
				out.WriteString(anonymizePathHead(message[start:span[0]]))
			} else {
				out.WriteString(message[pos:span[0]])
			}
			out.WriteString(message[span[0]:span[1]])
			pos = span[1]
		} else {
			out.WriteString(message[pos:start])
			out.WriteString(AnonymizePath(message[start:end]))
			pos = end
		}
		loc = filePathPattern.FindStringIndex(message[pos:])
	}
	out.WriteString(message[pos:])
	return out.String()
}

// anonymizePathHead anonymizes the head of a path match that was cut at a slash
// date. Trailing spaces stay outside the hash, and a head made only of
// separators and spaces is returned unchanged.
func anonymizePathHead(head string) string {
	trimmed := strings.TrimRight(head, " ")
	if strings.Trim(trimmed, `/\ `) == "" {
		return head
	}
	return AnonymizePath(trimmed) + head[len(trimmed):]
}

// maxClockHourDigits is the most digits a clock hour carries ("00" to "23").
const maxClockHourDigits = 2

// isPathSegmentByte reports whether b is in the segment class
// [a-zA-Z0-9 ._-] of filePathPattern. The two must stay in sync.
func isPathSegmentByte(b byte) bool {
	return isWordByte(b) || b == ' ' || b == '.' || b == '-'
}

// isPathSeparator reports whether b is a Unix or Windows path separator.
func isPathSeparator(b byte) bool {
	return b == '/' || b == '\\'
}

// isDigitByte reports whether b is an ASCII digit.
func isDigitByte(b byte) bool {
	return b >= '0' && b <= '9'
}

// standaloneSlashDateSpans returns the [start, end) spans of slash dates in
// message that cannot be part of a path or of a longer number, in order. A
// candidate from slashDatePattern is kept only when:
//  1. the byte before it is not a path separator (a date-shaped run inside
//     "/data/2026/10/16/x.wav" is a run of directories);
//  2. the byte after it is not a digit, nor "." or "," followed by a digit
//     ("1/2/60.1699,24.9384" and "10/16/60,24.9384" are coordinate pairs, not
//     dates);
//  3. the run of path-segment bytes after it does not end at a path separator
//     ("10/16/2026/john.wav", "10/16/2026 /john.wav");
//  4. when the date is joined to a preceding path, it follows a space and what
//     follows it is empty or a clock time ("T" or a space, 1-2 digits, then
//     ":"); otherwise the text around it could be the rest of a path segment
//     ("/home/john-10/16/2026");
//  5. the run of path-segment bytes after it has no file extension shape, a
//     "." followed by a word byte ("2026/10/16 john smith.wav" is a relative
//     file name, not a date followed by prose).
//
// A rejected candidate keeps the behavior of the path stage for that text.
func standaloneSlashDateSpans(message string) [][]int {
	candidates := slashDatePattern.FindAllStringIndex(message, -1)
	kept := candidates[:0]
	for _, span := range candidates {
		start, end := span[0], span[1]
		if start > 0 && isPathSeparator(message[start-1]) {
			continue
		}
		if end < len(message) && isDigitByte(message[end]) {
			continue
		}
		if end+1 < len(message) && (message[end] == '.' || message[end] == ',') && isDigitByte(message[end+1]) {
			continue
		}
		runEnd := end
		for runEnd < len(message) && isPathSegmentByte(message[runEnd]) {
			runEnd++
		}
		if runEnd < len(message) && isPathSeparator(message[runEnd]) {
			continue
		}
		if hasExtensionDot(message[end:runEnd]) {
			continue
		}
		if joinedToPath(message, start) && (message[start-1] != ' ' || !isClockTimeRun(message, end, runEnd)) {
			continue
		}
		kept = append(kept, span)
	}
	return kept
}

// hasExtensionDot reports whether run contains a "." followed by a word byte,
// the shape of a file extension such as ".wav".
func hasExtensionDot(run string) bool {
	for i := range len(run) - 1 {
		if run[i] == '.' && isWordByte(run[i+1]) {
			return true
		}
	}
	return false
}

// joinedToPath reports whether scanning back from start over path-segment bytes
// reaches a path separator, that is, whether the text at start could be the
// continuation of a path.
func joinedToPath(message string, start int) bool {
	i := start - 1
	for i >= 0 && isPathSegmentByte(message[i]) {
		i--
	}
	return i >= 0 && isPathSeparator(message[i])
}

// isClockTimeRun reports whether message[end:runEnd], the path-segment run after
// a date, is empty or the start of a clock time: "T" or a space, one or two
// hour digits, and a ":" right after the run.
func isClockTimeRun(message string, end, runEnd int) bool {
	run := message[end:runEnd]
	if run == "" {
		return true
	}
	if run[0] != 'T' && run[0] != ' ' {
		return false
	}
	hour := run[1:]
	return len(hour) >= 1 && len(hour) <= maxClockHourDigits && isNumeric(hour) &&
		runEnd < len(message) && message[runEnd] == ':'
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

// streamSchemes defines the URL schemes that should be sanitized for credentials
var streamSchemes = map[string]bool{
	"rtsp": true, "rtsps": true,
	"http": true, "https": true,
	"rtmp": true, "rtmps": true,
	"udp": true, "rtp": true,
}

// SanitizeRTSPUrl removes sensitive information from stream URLs and returns a display-friendly version.
// It strips credentials while preserving the host, port, and path for debugging.
// Despite the name, this function handles all stream protocols (RTSP, HTTP, HLS, RTMP, UDP, RTP).
// Kept for backward compatibility - prefer SanitizeStreamUrl for new code.
func SanitizeRTSPUrl(source string) string {
	return SanitizeStreamUrl(source)
}

// SanitizeStreamUrl removes sensitive information from stream URLs and returns a display-friendly version.
// It strips credentials while preserving the host, port, and path for debugging.
// Supports RTSP, RTSPS, HTTP, HTTPS, RTMP, RTMPS, UDP, and RTP URLs.
func SanitizeStreamUrl(source string) string {
	// Parse the URL using standard library
	parsedURL, err := url.Parse(source)
	if err != nil {
		// If parsing fails, return original to avoid data loss
		return source
	}

	// Only process supported stream URL schemes
	if !streamSchemes[parsedURL.Scheme] {
		return source
	}

	// Remove user credentials only (keeps path, query, and fragment for debugging)
	parsedURL.User = nil

	return parsedURL.String()
}

// SanitizeRTSPUrls finds and sanitizes all stream URLs in a given text.
// Despite the name, it handles all stream protocols. Kept for backward compatibility.
func SanitizeRTSPUrls(text string) string {
	return SanitizeStreamUrls(text)
}

// SanitizeStreamUrls finds and sanitizes all stream URLs in a given text.
// It uses regex pattern matching to identify stream URLs and replaces them with sanitized versions.
func SanitizeStreamUrls(text string) string {
	return streamURLPattern.ReplaceAllStringFunc(text, SanitizeStreamUrl)
}

// SanitizeFFmpegError removes memory addresses from FFmpeg error messages to enable proper deduplication
// It removes prefixes like "[rtsp @ 0x55d4a4808980]" which contain unique memory addresses
func SanitizeFFmpegError(text string) string {
	// First remove FFmpeg memory address prefixes
	text = ffmpegPrefixPattern.ReplaceAllString(text, "")
	// Then sanitize any stream URLs
	return SanitizeStreamUrls(text)
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
// It handles both IPv4 and IPv6 addresses that are not part of URLs.
// Time-like candidates such as the "12:34:56" part of a timestamp are left
// unchanged, and so are the vendor and product ids of usb-id tokens and the PCI
// address of usb-path tokens (see isUSBDeviceToken).
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
	ipv6Matches := dropNonAddressMatches(message, ipv6Pattern.FindAllStringIndex(message, -1))

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
// It replaces coordinate pairs with generic placeholders while preserving message structure.
// Date-time and syslog time prefixes are not coordinate pairs; they are kept
// as they are and the text around them is scrubbed on its own, so a pair
// that follows a timestamp is still replaced. Standalone slash dates are kept
// too, and so are integer ALSA selectors such as "hw:1,0" (see isALSASelector).
// A number left over after a pair is replaced with it (see scrubCoordinatePairs).
func ScrubCoordinates(message string) string {
	protected := protectedDateTimeSpans(message)
	if len(protected) == 0 {
		return scrubCoordinatePairs(message, message, 0)
	}

	var out strings.Builder
	pos := 0
	for _, span := range protected {
		out.WriteString(scrubCoordinatePairs(message, message[pos:span[0]], pos))
		out.WriteString(message[span[0]:span[1]])
		pos = span[1]
	}
	out.WriteString(scrubCoordinatePairs(message, message[pos:], pos))
	return out.String()
}

// coordinatePlaceholder replaces a coordinate pair in scrubbed text.
const coordinatePlaceholder = "[LAT],[LON]"

// alsaSelectorPrefixes are the ALSA PCM names that take a positional "CARD,DEV"
// selector. The integer pair right after one of them is a device address, not a
// coordinate; conf/validate.go already treats integer pairs as selectors.
var alsaSelectorPrefixes = []string{"hw:", "plughw:", "dsnoop:", "dmix:"}

// trailingNumberPattern matches the separator and one number of any length
// after a replaced coordinate match (see scrubCoordinatePairs).
var trailingNumberPattern = regexp.MustCompile(`^[,\s]+-?\d+\.?\d*`)

// scrubCoordinatePairs replaces the coordinate pairs in seg, which is
// message[offset:offset+len(seg)], except integer ALSA selectors. The selector
// check sees the whole message, so the text before the segment is context. It
// returns seg itself when nothing matches.
//
// coordinatesPattern pairs the numbers of a run left to right, so in a run of an
// odd count (a count before a real pair: "count 5, 60.1699,24.9384") the last
// number is not part of any match and would stay in clear. A replaced match
// therefore also absorbs one number that follows it after commas or spaces,
// unless that number is where the next match starts. This fails closed: no
// digit of a number run survives, and a number right after a pair is lost too.
func scrubCoordinatePairs(message, seg string, offset int) string {
	matches := coordinatesPattern.FindAllStringIndex(seg, -1)
	if len(matches) == 0 {
		return seg
	}
	var out strings.Builder
	pos := 0
	for i, m := range matches {
		out.WriteString(seg[pos:m[0]])
		pos = m[1]
		if isALSASelector(message, offset+m[0], offset+m[1]) {
			out.WriteString(seg[m[0]:m[1]])
			continue
		}
		out.WriteString(coordinatePlaceholder)
		next := len(seg)
		if i+1 < len(matches) {
			next = matches[i+1][0]
		}
		if tail := trailingNumberPattern.FindStringIndex(seg[pos:]); len(tail) > 0 && pos+tail[1] <= next {
			pos += tail[1]
		}
	}
	out.WriteString(seg[pos:])
	return out.String()
}

// isWordByte reports whether b is in the regexp word class [A-Za-z0-9_].
func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || isDigitByte(b) || b == '_'
}

// isALSASelector reports whether message[start:end], a coordinatesPattern match,
// is an ALSA selector: two integers separated by a comma, with no sign, decimal
// point or space, right after one of alsaSelectorPrefixes. The byte before the
// prefix must not be a word byte, so "myhw:60,24" is still scrubbed. A pair
// after a bare colon is not kept: whole-degree coordinates must not leak.
func isALSASelector(message string, start, end int) bool {
	card, device, ok := strings.Cut(message[start:end], ",")
	if !ok || !isNumeric(card) || !isNumeric(device) {
		return false
	}
	before := message[:start]
	for _, prefix := range alsaSelectorPrefixes {
		if !strings.HasSuffix(before, prefix) {
			continue
		}
		boundary := len(before) - len(prefix)
		if boundary == 0 || !isWordByte(before[boundary-1]) {
			return true
		}
	}
	return false
}

// trimGluedCoordinate returns the end of the time prefix message[start:end]
// without a trailing comma fraction that is really the whole-degree part of a
// coordinate: ",123" is a fraction of a second, but ",151.2093" is a longitude
// written right after the time.
func trimGluedCoordinate(message string, start, end int) int {
	if end+1 >= len(message) || message[end] != '.' || message[end+1] < '0' || message[end+1] > '9' {
		return end
	}
	comma := strings.LastIndexByte(message[start:end], ',')
	if comma < 0 {
		return end
	}
	for _, c := range message[start+comma+1 : end] {
		if c < '0' || c > '9' {
			return end
		}
	}
	return start + comma
}

// protectedDateTimeSpans returns the date-time prefixes, syslog time prefixes
// and standalone slash dates in message as sorted, non-overlapping [start, end)
// spans. A trailing comma fraction that is really the start of a coordinate
// (see trimGluedCoordinate) is not part of a span.
func protectedDateTimeSpans(message string) [][]int {
	spans := dateTimePattern.FindAllStringIndex(message, -1)
	spans = append(spans, syslogTimePattern.FindAllStringIndex(message, -1)...)
	if strings.Contains(message, "/") {
		spans = append(spans, standaloneSlashDateSpans(message)...)
	}
	for _, span := range spans {
		span[1] = trimGluedCoordinate(message, span[0], span[1])
	}
	if len(spans) < 2 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	merged := spans[:1]
	for _, span := range spans[1:] {
		last := merged[len(merged)-1]
		if span[0] <= last[1] {
			last[1] = max(last[1], span[1])
			continue
		}
		merged = append(merged, span)
	}
	return merged
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

// maxTimeTokenColons is the most colons a clock time or position token such as
// "12:34:56" or ":123:45" carries. A longer digit-only token is not a time.
const maxTimeTokenColons = 2

// isTimeLikeToken reports whether s is made of digits and colons only, has no
// "::" and no more than maxTimeTokenColons colons: the shape of the "12:34:56"
// part of a timestamp or the ":123:45" of a file position. Tokens with hex
// letters (MAC addresses, IPv6 prefixes cut off by the regex) or more groups
// are not time-like.
func isTimeLikeToken(s string) bool {
	if strings.Contains(s, "::") || strings.Count(s, ":") > maxTimeTokenColons {
		return false
	}
	hasDigit := false
	for _, r := range s {
		switch {
		case r == ':':
		case r >= '0' && r <= '9':
			hasDigit = true
		default:
			return false
		}
	}
	return hasDigit
}

// usbIDTokenPrefix starts the stable device token "usb-id:<vendor>:<product>:
// <serial or "<redacted>">" that internal/audiocore/usb.go builds (hwIDToken).
// privacy cannot import audiocore (audiocore imports privacy), so the prefix is
// repeated here. RedactUSBDeviceID is the shared masking that audiocore and the
// support dump use for a whole token; scrubUSBIDSerials masks tokens in text.
const usbIDTokenPrefix = "usb-id:"

// redactedUSBSerial replaces the serial of a usb-id device token.
const redactedUSBSerial = "<redacted>"

// usbIDHexGroup matches one usb-id vendor or product id: usbIDHexLen hex digits.
var usbIDHexGroup = "[0-9A-Fa-f]{" + strconv.Itoa(usbIDHexLen) + "}"

// usbIDTokenPattern matches a usb-id token up to its serial:
// "usb-id:<vendor>:<product>:".
var usbIDTokenPattern = regexp.MustCompile(regexp.QuoteMeta(usbIDTokenPrefix) + usbIDHexGroup + ":" + usbIDHexGroup + ":")

// usbSerialQuotes are the quote bytes that, right before a usb-id token, make
// its serial a quoted value (see usbIDSerialEnd).
const usbSerialQuotes = "\"'`"

// RedactUSBDeviceID masks the serial of a whole "usb-id:<vendor>:<product>:<serial>"
// device token, giving "usb-id:<vendor>:<product>:<redacted>". Anything that is
// not such a token with a non-empty serial is returned unchanged. The value is
// taken to be exactly one identifier (a log field or config value), so the
// serial is everything after the second colon, spaces included; use
// ScrubMessage for free text.
func RedactUSBDeviceID(deviceID string) string {
	rest, ok := strings.CutPrefix(deviceID, usbIDTokenPrefix)
	if !ok {
		return deviceID
	}
	vendor, after, found := strings.Cut(rest, ":")
	if !found {
		return deviceID
	}
	product, serial, found := strings.Cut(after, ":")
	if !found || serial == "" {
		return deviceID
	}
	return usbIDTokenPrefix + vendor + ":" + product + ":" + redactedUSBSerial
}

// isSerialTerminator reports whether b ends an unquoted usb-id serial: ASCII
// whitespace or one of the quote, list and bracket characters that follow a
// value in log text and JSON. "<" and ">" do not end a serial.
func isSerialTerminator(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f', '"', '\'', '`', ',', ';', ')', ']', '}', '\\':
		return true
	}
	return false
}

// usbIDSerialEnd returns the index just past the serial that starts at s in the
// token whose prefix starts at i.
//
// When the byte before the token is a quote (", ' or `), the serial is quoted:
// it runs to the next matching quote, to a backslash directly followed by that
// quote (a JSON value nested in another string), or to the end of the text.
// Any other backslash escape is skipped as a pair and stays in the serial. A
// quoted serial may contain spaces.
//
// Otherwise the serial ends at the first isSerialTerminator byte, so an
// unquoted serial that contains a space is masked only up to the space.
func usbIDSerialEnd(message string, i, s int) int {
	if i > 0 && strings.IndexByte(usbSerialQuotes, message[i-1]) >= 0 {
		quote := message[i-1]
		for k := s; k < len(message); k++ {
			switch message[k] {
			case quote:
				return k
			case '\\':
				if k+1 < len(message) && message[k+1] == quote {
					return k
				}
				k++
			}
		}
		return len(message)
	}
	k := s
	for k < len(message) && !isSerialTerminator(message[k]) {
		k++
	}
	return k
}

// isMaskedSerialAt reports whether the serial at s is already the redaction
// marker: the marker followed by the end of text, an isSerialTerminator byte,
// or one ":" or "." that is itself followed by the end of text or a terminator
// (an error suffix or the end of a sentence). A marker followed by any other
// text is the start of a serial and is masked like one.
func isMaskedSerialAt(message string, s int) bool {
	rest, ok := strings.CutPrefix(message[s:], redactedUSBSerial)
	if !ok {
		return false
	}
	if rest != "" && (rest[0] == ':' || rest[0] == '.') {
		rest = rest[1:]
	}
	return rest == "" || isSerialTerminator(rest[0])
}

// scrubUSBIDSerials masks the serial of every usb-id token in message (see
// usbIDTokenPattern), giving "usb-id:<vendor>:<product>:<redacted>", so vendor
// and product stay readable for later stages (see isUSBDeviceToken). A token
// whose serial is empty or already masked (see isMaskedSerialAt) is left alone,
// which keeps the stage idempotent; any other serial is masked, failing closed.
// usbIDSerialEnd decides where a serial ends. It returns message itself when
// there is nothing to mask.
func scrubUSBIDSerials(message string) string {
	if !strings.Contains(message, usbIDTokenPrefix) {
		return message
	}
	var out strings.Builder
	pos := 0
	for _, m := range usbIDTokenPattern.FindAllStringIndex(message, -1) {
		i, s := m[0], m[1]
		// A token inside a serial masked already is gone with that serial.
		if i < pos || isMaskedSerialAt(message, s) {
			continue
		}
		e := usbIDSerialEnd(message, i, s)
		if e == s {
			continue
		}
		if pos == 0 {
			out.Grow(len(message))
		}
		out.WriteString(message[pos:s])
		out.WriteString(redactedUSBSerial)
		pos = e
	}
	if pos == 0 {
		return message
	}
	out.WriteString(message[pos:])
	return out.String()
}

// usbPathTokenPrefix is the text before the PCI address in a "usb-path:" token
// such as "usb-path:usb-0000:00:1d.0-1".
const usbPathTokenPrefix = "usb-"

// USB vendor and product ids are 4 hex digits; a PCI address is a 4 digit
// domain, a 2 digit bus and a 2 digit device, all hex.
const (
	usbIDHexLen     = 4
	pciBusHexLen    = 2
	pciDeviceHexLen = 2
)

// ipv6Pattern groups: a usb-id match is "" (before the colon of the prefix),
// vendor, product; a PCI address match is domain, bus, device.
const usbTokenGroupCount = 3

// isUSBDeviceToken reports whether the ipv6Pattern match message[start:end] is
// the id part of a USB device token and not an address. Two exact shapes pass:
//   - ":VVVV:PPPP" right after the literal "usb-id" (the match starts at the
//     colon of "usb-id:"). A serial that starts with a word byte after the pair
//     ("usb-id:046d:0825:SN12") puts a trailing colon into the match, which
//     fails the shape, so ScrubStandaloneIPs alone anonymizes the ids with the
//     serial; ScrubMessage masks the serial first, so the ids survive there;
//   - "DDDD:BB:dd" right after "usb-" with a "." after it, the PCI address of a
//     "usb-path:usb-0000:00:1d.0-1" token.
//
// A longer match, such as an address glued after the prefix, fails both shapes
// and is anonymized. The match comes from ipv6Pattern, so it holds only hex
// digits and colons and the group checks compare lengths only.
func isUSBDeviceToken(message string, start, end int) bool {
	groups := strings.Split(message[start:end], ":")
	if strings.HasSuffix(message[:start+1], usbIDTokenPrefix) {
		return len(groups) == usbTokenGroupCount && groups[0] == "" && len(groups[1]) == usbIDHexLen && len(groups[2]) == usbIDHexLen
	}
	return strings.HasSuffix(message[:start], usbPathTokenPrefix) && end < len(message) && message[end] == '.' &&
		len(groups) == usbTokenGroupCount && len(groups[0]) == usbIDHexLen && len(groups[1]) == pciBusHexLen && len(groups[2]) == pciDeviceHexLen
}

// dropNonAddressMatches removes the matches that are not addresses: time-like
// tokens and the ids of USB device tokens.
// ipv6Pattern also matches the "12:34:56" part of a timestamp and the
// "0d8c:0014" of "usb-id:0d8c:0014:<redacted>"; those are left untouched
// instead of being hashed. Every other match, including the ones net.ParseIP
// rejects (MAC addresses, truncated IPv6 prefixes), is still anonymized.
func dropNonAddressMatches(message string, matches [][]int) [][]int {
	kept := matches[:0]
	for _, match := range matches {
		if !isTimeLikeToken(message[match[0]:match[1]]) && !isUSBDeviceToken(message, match[0], match[1]) {
			kept = append(kept, match)
		}
	}
	return kept
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

// AnonymizeStacktracePath strips absolute path prefixes from file paths used in
// stacktraces, preserving only the last two segments for debugging context
// (e.g., "/home/user/go/src/birdnet-go/internal/telemetry/sentry.go" becomes
// "<redacted>/internal/telemetry"). Windows backslash separators are normalized
// to forward slashes. Paths with two or fewer segments are returned as-is after
// normalization, since they contain no sensitive directory information.
func AnonymizeStacktracePath(path string) string {
	if path == "" {
		return path
	}
	// Normalize Windows paths
	normalized := strings.ReplaceAll(path, windowsPathSeparator, unixPathSeparator)
	parts := strings.Split(normalized, unixPathSeparator)
	if len(parts) <= stacktracePathSegments {
		return normalized // Short paths like "telemetry/sentry.go" are safe
	}
	return redactedPathPrefix + unixPathSeparator + strings.Join(parts[len(parts)-stacktracePathSegments:], unixPathSeparator)
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
