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

// ValidateBaseURL checks a base URL as BIRDNET_URL and security.baseUrl accept it:
// non-empty after trimming, an http or https scheme, a hostname and no userinfo.
// Every error is a fixed message that quotes nothing from the value, which may
// carry credentials, so callers can log it as it is.
func ValidateBaseURL(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("URL must not be empty")
	}

	parsed, err := url.Parse(value)
	if err != nil {
		// The parser's reason can quote part of the value (a port token that is
		// really a password), so none of it is kept.
		return fmt.Errorf("invalid URL format")
	}

	if parsed.Scheme == "" {
		return fmt.Errorf("URL must include scheme (http:// or https://)")
	}

	if parsed.Scheme != SchemeHTTP && parsed.Scheme != SchemeHTTPS {
		return fmt.Errorf("URL scheme must be http or https")
	}

	if parsed.Hostname() == "" {
		return fmt.Errorf("URL must include a valid hostname")
	}

	// A base URL ends up in links, redirect URIs and logs, so credentials in it
	// would be published with them.
	if parsed.User != nil {
		return fmt.Errorf("URL must not include credentials (user or password before the host)")
	}

	return nil
}

// trimmedBaseURL returns BaseURL without surrounding whitespace or a trailing
// slash, whether or not it is usable. A value that is only a slash is kept, so
// it counts as set (and unusable) rather than unset.
func (s *Security) trimmedBaseURL() string {
	baseURL := strings.TrimSpace(s.BaseURL)
	if trimmed := strings.TrimSuffix(baseURL, "/"); trimmed != "" {
		return trimmed
	}
	return baseURL
}

// trimmedHost returns Host without surrounding whitespace or a trailing slash.
func (s *Security) trimmedHost() string {
	return strings.TrimSuffix(strings.TrimSpace(s.Host), "/")
}

// schemeSeparator separates a URL scheme from the rest of the URL.
const schemeSeparator = "://"

// hostHasScheme reports whether a trimmed Host already carries a scheme, any
// scheme, because a Host is a bare name or a full origin and nothing in between.
// Whether that scheme is one we accept is hasUnusableHost's decision.
func hostHasScheme(host string) bool {
	return strings.Contains(host, schemeSeparator)
}

// hasUnusableHost reports whether Host is set but cannot be used. One that
// carries a scheme must be an absolute http(s) URL with a host and no credentials
// (ValidateBaseURL), as a baseUrl must be; a bare one must not contain an at
// sign, which would put userinfo in front of the host in every link built from
// it. OAuthOrigin, GetBaseURL and the hostname getters all ask here, so they
// agree on a scheme or credentials in Host.
func (s *Security) hasUnusableHost() bool {
	host := s.trimmedHost()
	if host == "" {
		return false
	}
	if hostHasScheme(host) {
		return ValidateBaseURL(host) != nil
	}
	return strings.Contains(host, "@")
}

// hasUnusableBaseURL reports whether BaseURL is set but not a usable URL, the one
// case where it is ignored without Host taking over.
func (s *Security) hasUnusableBaseURL() bool {
	baseURL := s.trimmedBaseURL()
	return baseURL != "" && ValidateBaseURL(baseURL) != nil
}

// oauthOriginCandidate returns the origin OAuth callbacks are built from, before
// it is checked, and the setting it came from: BaseURL when set, otherwise Host
// with https assumed when it has no scheme. It returns "" for both when neither
// is set.
//
// A bare Host gets https here but follows the TLS mode and port in GetBaseURL.
// Callback URLs registered at identity providers depend on the first, links in
// notifications on the second, so the two are kept apart on purpose.
func (s *Security) oauthOriginCandidate() (field, origin string) {
	if baseURL := s.trimmedBaseURL(); baseURL != "" {
		return "security.baseUrl", baseURL
	}
	host := s.trimmedHost()
	if host == "" {
		return "", ""
	}
	if !hostHasScheme(host) {
		host = SchemeHTTPS + schemeSeparator + host
	}
	return ConfigKeySecurityHost, host
}

// OAuthOrigin returns the origin OAuth redirect URIs are built from, or "" when
// none can be: nothing is set, or the value that wins is not a usable URL (a Host
// is also unusable when it holds an at sign, see hasUnusableHost). A BaseURL that
// is set but unusable does not fall back to Host.
func (s *Security) OAuthOrigin() string {
	field, origin := s.oauthOriginCandidate()
	if origin == "" || ValidateBaseURL(origin) != nil {
		return ""
	}
	if field == ConfigKeySecurityHost && s.hasUnusableHost() {
		return ""
	}
	return origin
}

// GetBaseURL returns the base URL for notifications and OAuth redirects.
// Priority order:
//  1. BaseURL field (if set, trimmed; empty when it is not a usable URL)
//  2. Host as given when it carries an http(s) scheme (empty when it is not a
//     usable URL), otherwise built from Host + port + TLSMode scheme
//  3. Empty string if no host is available
//
// This method does NOT fall back to localhost - callers should handle empty returns.
func (s *Security) GetBaseURL(port string) string {
	// Priority 1: BaseURL, which wins even when unusable
	if baseURL := s.trimmedBaseURL(); baseURL != "" {
		if s.hasUnusableBaseURL() {
			return ""
		}
		return baseURL
	}

	// Priority 2: Host as given when it has a scheme, otherwise built from Host + port + TLSMode
	host := s.trimmedHost()
	if host == "" || s.hasUnusableHost() {
		return ""
	}
	if hostHasScheme(host) {
		// Already a full, validated origin, used as given.
		return host
	}

	return s.buildURLFromHost(host, port)
}

// buildURLFromHost constructs a URL from host, port, and TLSMode settings.
// Default ports (80 for HTTP, 443 for HTTPS) are omitted for cleaner URLs.
func (s *Security) buildURLFromHost(host, port string) string {
	scheme := SchemeHTTP
	if s.TLSMode != TLSModeNone {
		scheme = SchemeHTTPS
	}

	// Omit default ports for cleaner URLs
	if (scheme == SchemeHTTPS && port == DefaultHTTPSPort) || (scheme == SchemeHTTP && port == DefaultHTTPPort) {
		return fmt.Sprintf("%s://%s", scheme, host)
	}

	return fmt.Sprintf("%s://%s:%s", scheme, host, port)
}

// GetHostnameForCertificates extracts the hostname for TLS certificate generation.
// Priority order:
//  1. Host field (if set and usable; a Host with an http(s) scheme is reduced to
//     its hostname, dropping scheme, port and path; an unusable one is skipped)
//  2. Hostname extracted from BaseURL (without port)
//  3. Empty string if neither is available
//
// IPv6 addresses are returned without brackets.
func (s *Security) GetHostnameForCertificates() string {
	// Priority 1: Use Host if set
	if host := s.trimmedHost(); host != "" && !s.hasUnusableHost() {
		if !hostHasScheme(host) {
			return host
		}
		return hostnameOf(host)
	}

	// Priority 2: Extract hostname from BaseURL
	return hostnameOf(s.trimmedBaseURL())
}

// hostnameOf returns the hostname of a URL without port, or "" when it does not
// parse or has none. IPv6 addresses come back without brackets.
func hostnameOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// GetExternalHost returns the external host:port for backward compatibility.
// This is useful for cases where the full host:port is needed (e.g., HTTP Host header).
// Priority order:
//  1. Host:port extracted from BaseURL (if it is a usable URL)
//  2. Host field as fallback (host:port when it carries an http(s) scheme, empty
//     when it is unusable)
//  3. Empty string if neither is available
func (s *Security) GetExternalHost() string {
	// Priority 1: Extract from BaseURL if usable
	if baseURL := s.trimmedBaseURL(); baseURL != "" && ValidateBaseURL(baseURL) == nil {
		parsed, _ := url.Parse(baseURL) // cannot fail: ValidateBaseURL parsed it
		return parsed.Host
	}

	// Priority 2: Fall back to Host, reduced to host:port when it has a scheme
	host := s.trimmedHost()
	if s.hasUnusableHost() {
		return ""
	}
	if !hostHasScheme(host) {
		return host
	}
	parsed, _ := url.Parse(host) // cannot fail: hasUnusableHost validated it
	return parsed.Host
}

// IsHTTPSBaseURL reports whether the canonical external BaseURL uses the https
// scheme, which indicates that a reverse proxy terminates TLS upstream while the
// app itself may serve plain HTTP. It is used to decide whether clients reach the
// app over HTTPS in reverse-proxy deployments (e.g. for the Secure cookie flag).
//
// A bare Host without an https BaseURL is deliberately NOT treated as HTTPS: Host
// alone is ambiguous (it may front a plain-HTTP LAN deployment), so relying on it
// could over-set Secure and drop the session cookie over HTTP.
func (s *Security) IsHTTPSBaseURL() bool {
	if s == nil || s.BaseURL == "" {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(s.BaseURL))
	// Require a real authority: url.Parse treats "https:" and the opaque
	// "https:host" as scheme https with an empty Host, which is not a usable
	// external URL and must not be treated as HTTPS.
	return err == nil && u.Scheme == SchemeHTTPS && u.Host != ""
}
