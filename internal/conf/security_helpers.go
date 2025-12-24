// Package conf provides security helper methods for URL handling in reverse proxy setups.
package conf

import (
	"fmt"
	"net/url"
	"strings"
)

// URL scheme constants for URL construction.
const (
	SchemeHTTP  = "http"
	SchemeHTTPS = "https"
)

// Default port constants for URL construction.
const (
	DefaultHTTPPort  = "80"
	DefaultHTTPSPort = "443"
)

// GetBaseURL returns the base URL for notifications and OAuth redirects.
// Priority order:
//  1. BaseURL field (if set, used as-is with trailing slash trimmed)
//  2. Constructed from Host + port + AutoTLS scheme
//  3. Empty string if no host is available
//
// This method does NOT fall back to localhost - callers should handle empty returns.
func (s *Security) GetBaseURL(port string) string {
	// Priority 1: Use BaseURL if set
	if baseURL := strings.TrimSuffix(strings.TrimSpace(s.BaseURL), "/"); baseURL != "" {
		return baseURL
	}

	// Priority 2: Construct from Host + port + AutoTLS
	if s.Host == "" {
		return ""
	}

	return s.buildURLFromHost(port)
}

// buildURLFromHost constructs a URL from Host, port, and AutoTLS settings.
// Default ports (80 for HTTP, 443 for HTTPS) are omitted for cleaner URLs.
func (s *Security) buildURLFromHost(port string) string {
	scheme := SchemeHTTP
	if s.AutoTLS {
		scheme = SchemeHTTPS
	}

	// Omit default ports for cleaner URLs
	if (scheme == SchemeHTTPS && port == DefaultHTTPSPort) || (scheme == SchemeHTTP && port == DefaultHTTPPort) {
		return fmt.Sprintf("%s://%s", scheme, s.Host)
	}

	return fmt.Sprintf("%s://%s:%s", scheme, s.Host, port)
}

// GetHostnameForCertificates extracts the hostname for TLS certificate generation.
// Priority order:
//  1. Host field (if set)
//  2. Hostname extracted from BaseURL (without port)
//  3. Empty string if neither is available
//
// IPv6 addresses are returned without brackets.
func (s *Security) GetHostnameForCertificates() string {
	// Priority 1: Use Host if set
	if s.Host != "" {
		return s.Host
	}

	// Priority 2: Extract hostname from BaseURL
	if s.BaseURL == "" {
		return ""
	}

	parsed, err := url.Parse(strings.TrimSpace(s.BaseURL))
	if err != nil || parsed.Host == "" {
		return ""
	}

	return parsed.Hostname()
}

// GetExternalHost returns the external host:port for backward compatibility.
// This is useful for cases where the full host:port is needed (e.g., HTTP Host header).
// Priority order:
//  1. Host:port extracted from BaseURL (if valid)
//  2. Host field as fallback
//  3. Empty string if neither is available
func (s *Security) GetExternalHost() string {
	// Priority 1: Extract from BaseURL if valid
	if s.BaseURL != "" {
		parsed, err := url.Parse(strings.TrimSpace(s.BaseURL))
		if err == nil && parsed.Host != "" {
			return parsed.Host
		}
	}

	// Priority 2: Fall back to Host
	return s.Host
}
