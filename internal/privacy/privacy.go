// Package privacy provides privacy-focused utility functions for handling sensitive data
// such as URL sanitization, message scrubbing, and system ID generation.
package privacy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Pre-compiled patterns for better performance (avoiding issue #825)
var (
	// URL pattern for finding URLs in text
	urlPattern = regexp.MustCompile(`\b(?:https?|rtsp|rtmp)://\S+`)
	
	// IPv4 pattern for IP address detection
	ipv4Pattern = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)
)

// ScrubMessage removes or anonymizes sensitive information from telemetry messages
// It finds URLs in the message and replaces them with anonymized versions
func ScrubMessage(message string) string {
	return urlPattern.ReplaceAllStringFunc(message, AnonymizeURL)
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
	// If not an RTSP URL, return as is
	if !strings.HasPrefix(source, "rtsp://") {
		return source
	}

	// Find the @ symbol that separates credentials from host
	atIndex := -1
	for i := len("rtsp://"); i < len(source); i++ {
		if source[i] == '@' {
			atIndex = i
			break
		}
	}

	if atIndex > -1 {
		// Keep only rtsp:// and everything after @
		source = "rtsp://" + source[atIndex+1:]
	}

	// Find the first slash after the host:port
	slashIndex := -1
	for i := len("rtsp://"); i < len(source); i++ {
		if source[i] == '/' {
			slashIndex = i
			break
		}
	}

	if slashIndex > -1 {
		// Keep only up to the first slash
		source = source[:slashIndex]
	}

	return source
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

// categorizeHost anonymizes hostnames while preserving useful categorization
func categorizeHost(host string) string {
	// Check for localhost patterns
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return "localhost"
	}

	// Check for private IP ranges
	if isPrivateIP(host) {
		return "private-ip"
	}

	// Check for public IP
	if isIPAddress(host) {
		return "public-ip"
	}

	// For domain names, preserve TLD only
	parts := strings.Split(host, ".")
	if len(parts) >= 2 {
		tld := parts[len(parts)-1]
		return "domain-" + tld
	}

	return "unknown-host"
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

// isPrivateIP checks if the host is a private IP address (both IPv4 and IPv6)
func isPrivateIP(host string) bool {
	privateRanges := []string{
		// IPv4 private ranges
		"10.", "172.16.", "172.17.", "172.18.", "172.19.", "172.20.", "172.21.", "172.22.", "172.23.",
		"172.24.", "172.25.", "172.26.", "172.27.", "172.28.", "172.29.", "172.30.", "172.31.",
		"192.168.", "169.254.",
		// IPv6 private ranges
		"fc00:", "fd00:", // Unique local addresses
		"fe80:",                   // Link-local addresses
		"::1",                     // Loopback
		"ff00:", "ff01:", "ff02:", // Multicast
	}

	for _, prefix := range privateRanges {
		if strings.HasPrefix(strings.ToLower(host), strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

// isIPAddress checks if the host looks like an IP address
func isIPAddress(host string) bool {
	// Check for IPv4 using pre-compiled pattern
	if ipv4Pattern.MatchString(host) {
		return true
	}

	// Check for IPv6 (contains colons)
	return strings.Contains(host, ":")
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