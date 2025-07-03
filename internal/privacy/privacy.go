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
	"strings"
)

// Pre-compiled patterns for better performance (avoiding issue #825)
var (
	// URL pattern for finding URLs in text
	urlPattern = regexp.MustCompile(`\b(?:https?|rtsp|rtmp)://\S+`)
	
	
	// GPS coordinates pattern - matches decimal degree coordinates  
	coordinatesPattern = regexp.MustCompile(`(?:lat(?:itude)?|lng|lon|longitude)[:=]?\s*-?\d{1,3}\.?\d*[,\s]+(?:lng|lon|longitude)[:=]?\s*-?\d{1,3}\.?\d*|(?:lat(?:itude)?[:=]?\s*)?-?\d{1,3}\.?\d*[,\s]+-?\d{1,3}\.?\d*`)
	
	// Generic API token/key pattern - matches tokens with clear context
	apiTokenPattern = regexp.MustCompile(`(?:api[_-]?key|token|secret|auth)[:=]\s*([A-Za-z0-9+/]{8,}[A-Za-z0-9+/=]*)|(?:with\s+(?:token|key|secret|auth)\s+)([A-Za-z0-9+/]{8,}[A-Za-z0-9+/=]*)`)
	
	// Token extraction pattern for replacing just the token part
	tokenRegex = regexp.MustCompile(`([A-Za-z0-9+/]{8,}[A-Za-z0-9+/=]*)`)
	
	// Separator normalization pattern for API tokens
	separatorRegex = regexp.MustCompile(`=\s*`)
)

// Common two-part TLDs that need special handling
var commonTwoPartTLDs = map[string]bool{
	"co.uk": true, "co.nz": true, "co.za": true, "co.jp": true,
	"gov.uk": true, "gov.au": true, "gov.ca": true,
	"ac.uk": true, "edu.au": true, "org.uk": true,
	"net.au": true, "com.au": true,
}

// ScrubMessage removes or anonymizes sensitive information from telemetry messages
// It finds URLs and other sensitive data in the message and replaces them with anonymized versions
func ScrubMessage(message string) string {
	// Apply all scrubbing functions in sequence
	result := urlPattern.ReplaceAllStringFunc(message, AnonymizeURL)
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
		return fmt.Sprintf("url-hash-%x", hash[:8])
	}

	// Create a normalized version for hashing
	// Include scheme, host pattern, and path structure but remove sensitive data
	var normalizedParts []string

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
		pathStructure := anonymizePath(parsedURL.Path)
		normalizedParts = append(normalizedParts, pathStructure)
	}

	// Create consistent hash
	normalized := strings.Join(normalizedParts, ":")
	hash := sha256.Sum256([]byte(normalized))

	return fmt.Sprintf("url-%x", hash[:12])
}

// SanitizeRTSPUrl removes sensitive information from RTSP URL and returns a display-friendly version
// It strips credentials and path information while preserving the host and port for debugging
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

	// Remove user credentials
	parsedURL.User = nil
	
	// Remove path and query components
	parsedURL.Path = ""
	parsedURL.RawPath = ""
	parsedURL.RawQuery = ""
	parsedURL.Fragment = ""
	
	// Return sanitized URL
	return parsedURL.String()
}

// GenerateSystemID creates a unique system identifier
// The ID is 12 characters long, URL-safe, and case-insensitive
// Format: XXXX-XXXX-XXXX (14 chars total with hyphens)
func GenerateSystemID() (string, error) {
	// Generate 6 random bytes (will become 12 hex characters)
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Convert to hex string (12 characters)
	id := hex.EncodeToString(bytes)

	// Format as XXXX-XXXX-XXXX for readability
	formatted := fmt.Sprintf("%s-%s-%s", id[0:4], id[4:8], id[8:12])

	return strings.ToUpper(formatted), nil
}

// IsValidSystemID checks if a system ID has the correct format
func IsValidSystemID(id string) bool {
	// Check format: XXXX-XXXX-XXXX (14 chars total)
	if len(id) != 14 {
		return false
	}

	// Check hyphens at correct positions
	if id[4] != '-' || id[9] != '-' {
		return false
	}

	// Check that all other characters are hex
	for i, char := range id {
		if i == 4 || i == 9 {
			continue // Skip hyphens
		}
		if !isHexChar(char) {
			return false
		}
	}

	return true
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
		// Use pre-compiled regex to find and replace just the token part within the match
		result := tokenRegex.ReplaceAllString(match, "[API_TOKEN]")
		// Normalize separators to colon for consistency using pre-compiled regex
		result = separatorRegex.ReplaceAllString(result, ": ")
		return result
	})
}


// categorizeHost anonymizes hostnames while preserving useful categorization
func categorizeHost(host string) string {
	// Check for localhost patterns
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return "localhost"
	}

	// Check for private IP ranges using RFC-compliant detection
	if isPrivateIP(host) {
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
	if len(parts) < 2 {
		return "unknown-host"
	}

	// Check for common two-part TLDs (e.g., co.uk, gov.au)
	if len(parts) >= 3 {
		twoPartTLD := parts[len(parts)-2] + "." + parts[len(parts)-1]
		if commonTwoPartTLDs[strings.ToLower(twoPartTLD)] {
			return "domain-" + strings.ToLower(twoPartTLD)
		}
	}

	// Use the last part as TLD for regular domains
	tld := parts[len(parts)-1]
	return "domain-" + strings.ToLower(tld)
}

// anonymizePath creates a structure-preserving but privacy-safe path representation
func anonymizePath(path string) string {
	// Remove leading/trailing slashes for processing
	path = strings.Trim(path, "/")
	if path == "" {
		return "root"
	}

	// Split path into segments
	segments := strings.Split(path, "/")
	var anonymizedSegments []string

	for _, segment := range segments {
		if segment == "" {
			continue
		}

		// Check for common patterns that might be safe to preserve
		switch {
		case isCommonStreamName(segment):
			anonymizedSegments = append(anonymizedSegments, "stream")
		case isNumeric(segment):
			anonymizedSegments = append(anonymizedSegments, "numeric")
		default:
			// Hash individual segments to maintain path structure
			hash := sha256.Sum256([]byte(segment))
			anonymizedSegments = append(anonymizedSegments, fmt.Sprintf("seg-%x", hash[:4]))
		}
	}

	return strings.Join(anonymizedSegments, "/")
}

// isPrivateIP checks if the host is a private IP address using net.ParseIP and enhanced classification
func isPrivateIP(host string) bool {
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
	commonNames := []string{"stream", "live", "rtsp", "video", "audio", "feed", "cam", "camera"}
	segment = strings.ToLower(segment)

	for _, name := range commonNames {
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
		return fmt.Sprintf("invalid-ip-%x", hash[:8])
	}
	
	// Categorize the IP
	category := categorizeHost(ip.String())
	
	// Create a hash of the IP
	hash := sha256.Sum256([]byte(ip.String()))
	
	// Return categorized anonymized IP
	return fmt.Sprintf("%s-%x", category, hash[:8])
}

// AnonymizePath anonymizes file paths while preserving structure information
// It replaces path segments with hashes but maintains the path hierarchy
func AnonymizePath(path string) string {
	if path == "" {
		return ""
	}
	
	// Preserve absolute/relative nature of the path
	isAbsolute := strings.HasPrefix(path, "/") || (len(path) > 2 && path[1] == ':') // Unix or Windows
	
	// Split path into segments
	segments := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	
	if len(segments) == 0 {
		return "empty-path"
	}
	
	// Anonymize each segment
	anonymized := make([]string, len(segments))
	for i, segment := range segments {
		if segment == "" {
			continue
		}
		
		// Keep file extensions visible for debugging
		ext := ""
		if i == len(segments)-1 { // Last segment (filename)
			if idx := strings.LastIndex(segment, "."); idx > 0 {
				ext = segment[idx:]
				segment = segment[:idx]
			}
		}
		
		// Hash the segment
		hash := sha256.Sum256([]byte(segment))
		anonymized[i] = fmt.Sprintf("path-%x%s", hash[:4], ext)
	}
	
	// Reconstruct path with appropriate separator
	separator := "/"
	if strings.Contains(path, "\\") {
		separator = "\\"
	}
	
	result := strings.Join(anonymized, separator)
	if isAbsolute && !strings.HasPrefix(result, separator) {
		result = separator + result
	}
	
	return result
}

// RedactUserAgent anonymizes user agent strings to prevent tracking
// It preserves browser and OS type information while removing version details
func RedactUserAgent(userAgent string) string {
	if userAgent == "" {
		return ""
	}
	
	// Common patterns to extract browser/OS info
	// These patterns match major browsers and operating systems
	// Order matters - check more specific patterns first
	patterns := []struct {
		name    string
		pattern *regexp.Regexp
		isBrowser bool
	}{
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
	
	// Extract basic browser and OS info
	var components []string
	var foundBrowser, foundOS bool
	
	// Check for bot/crawler patterns
	if strings.Contains(strings.ToLower(userAgent), "bot") ||
		strings.Contains(strings.ToLower(userAgent), "crawler") ||
		strings.Contains(strings.ToLower(userAgent), "spider") {
		components = append(components, "Bot")
		foundBrowser = true // Bot is considered a browser type
	}
	
	// Extract browser and OS type
	for _, p := range patterns {
		if p.pattern.MatchString(userAgent) {
			if p.isBrowser && !foundBrowser {
				components = append(components, p.name)
				foundBrowser = true
			} else if !p.isBrowser && !foundOS {
				components = append(components, p.name)
				foundOS = true
			}
			
			// Stop if we found both browser and OS
			if foundBrowser && foundOS {
				break
			}
		}
	}
	
	// If no components found, return a generic hash
	if len(components) == 0 {
		hash := sha256.Sum256([]byte(userAgent))
		return fmt.Sprintf("ua-%x", hash[:8])
	}
	
	// Return redacted user agent with basic info only
	return strings.Join(components, " ")
}