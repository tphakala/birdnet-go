// Package conf provides security helper methods for URL handling in reverse proxy setups.
package conf

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
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
// non-empty after trimming, an http or https scheme, and a hostname. The error
// does not repeat the whole value, which may carry credentials, but it can quote short fragments of it (the scheme, a port, an escape sequence), so callers that log it must not pass it on unredacted.
func ValidateBaseURL(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("URL must not be empty")
	}

	parsed, err := url.Parse(value)
	if err != nil {
		// url.Error repeats the whole value; keep only the reason.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return fmt.Errorf("invalid URL format: %w", err)
	}

	if parsed.Scheme == "" {
		return fmt.Errorf("URL must include scheme (http:// or https://)")
	}

	if parsed.Scheme != SchemeHTTP && parsed.Scheme != SchemeHTTPS {
		return fmt.Errorf("URL scheme must be http or https, got: %s", parsed.Scheme)
	}

	if parsed.Hostname() == "" {
		return fmt.Errorf("URL must include a valid hostname")
	}

	return nil
}

// trimmedBaseURL returns BaseURL without surrounding whitespace or a trailing
// slash, whether or not it is usable.
func (s *Security) trimmedBaseURL() string {
	return strings.TrimSuffix(strings.TrimSpace(s.BaseURL), "/")
}

// trimmedHost returns Host without surrounding whitespace or a trailing slash.
func (s *Security) trimmedHost() string {
	return strings.TrimSuffix(strings.TrimSpace(s.Host), "/")
}

// hasPrefixFold reports whether s starts with prefix, ignoring ASCII case.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// hostHasScheme reports whether a trimmed Host already starts with an http or
// https scheme, in any case. Such a host is a full origin and is used as given;
// OAuthOrigin and GetBaseURL both ask here so they cannot disagree about it.
func hostHasScheme(host string) bool {
	return hasPrefixFold(host, SchemeHTTP+"://") || hasPrefixFold(host, SchemeHTTPS+"://")
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
		host = SchemeHTTPS + "://" + host
	}
	return "security.host", host
}

// OAuthOrigin returns the origin OAuth redirect URIs are built from, or "" when
// none can be: nothing is set, or the value that wins is not a usable URL. A
// BaseURL that is set but unusable does not fall back to Host.
func (s *Security) OAuthOrigin() string {
	_, origin := s.oauthOriginCandidate()
	if origin == "" || ValidateBaseURL(origin) != nil {
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
	if host == "" {
		return ""
	}
	if hostHasScheme(host) {
		// Already a full origin: used as given when usable, as the OAuth rule does.
		if ValidateBaseURL(host) != nil {
			return ""
		}
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
