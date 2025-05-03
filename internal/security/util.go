package security

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// normalizePort returns the port string, substituting the default port if empty.
func normalizePort(scheme, port string) string {
	if port == "" {
		if scheme == "https" {
			return "443"
		} else if scheme == "http" {
			return "80"
		}
	}
	return port
}

// ValidateRedirectURI parses the provided URI string and compares its essential components
// (Scheme, Hostname, normalized Port, Path) against the pre-parsed expected URI.
// It returns an error if the URIs do not match or if parsing fails.
func ValidateRedirectURI(providedURIString string, expectedURI *url.URL) error {
	if expectedURI == nil {
		// This indicates a configuration error occurred during startup where the expected
		// URI could not be parsed. Fail validation immediately.
		return fmt.Errorf("internal configuration error: expected redirect URI was not successfully parsed at startup")
	}

	parsedProvidedURI, err := url.Parse(providedURIString)
	if err != nil {
		return fmt.Errorf("invalid redirect_uri format: %w", err)
	}

	// Normalize paths: Use path.Clean and trim trailing slash for consistent comparison
	// Note: path.Clean removes trailing slashes unless the path is "/"
	providedPath := path.Clean(parsedProvidedURI.Path)
	expectedPath := path.Clean(expectedURI.Path)

	// Normalize ports
	providedPort := normalizePort(parsedProvidedURI.Scheme, parsedProvidedURI.Port())
	expectedPort := normalizePort(expectedURI.Scheme, expectedURI.Port())

	// Compare Scheme (case-insensitive), Hostname (case-insensitive), normalized Port, Path
	// RFC 3986 Section 3.1: Scheme names are case-insensitive.
	// RFC 3986 Section 3.2.2: Host names are case-insensitive.
	if strings.ToLower(parsedProvidedURI.Scheme) != strings.ToLower(expectedURI.Scheme) ||
		strings.ToLower(parsedProvidedURI.Hostname()) != strings.ToLower(expectedURI.Hostname()) || // Compare Hostname case-insensitively
		providedPort != expectedPort || // Compare normalized ports
		providedPath != expectedPath || // Compare cleaned paths
		parsedProvidedURI.RawQuery != "" || // Ensure provided URI has no query parameters
		parsedProvidedURI.Fragment != "" { // Ensure provided URI has no fragment

		// Construct a clearer error message showing the difference
		// Include Hostname, Port, RawQuery and Fragment in the error message for clarity
		return fmt.Errorf("invalid redirect_uri: provided '%s' (parsed as Scheme=%s, Hostname=%s, Port=%s, Path=%s, Query=%s, Fragment=%s)"+
			" does not match expected base URI (Scheme=%s, Hostname=%s, Port=%s, Path=%s) or contains disallowed query/fragment components",
			providedURIString,
			parsedProvidedURI.Scheme, parsedProvidedURI.Hostname(), providedPort, providedPath, parsedProvidedURI.RawQuery, parsedProvidedURI.Fragment,
			expectedURI.Scheme, expectedURI.Hostname(), expectedPort, expectedPath)
	}

	return nil // URIs match
}
