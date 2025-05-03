package security

import (
	"fmt"
	"net/url" // Import path for Clean
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

	// Normalize paths: Keep "/" as is, otherwise trim trailing slash for consistent comparison
	providedPath := parsedProvidedURI.Path
	if providedPath != "/" {
		providedPath = strings.TrimSuffix(providedPath, "/") // Reverted: path.Clean not needed here
	}
	expectedPath := expectedURI.Path
	if expectedPath != "/" {
		expectedPath = strings.TrimSuffix(expectedPath, "/") // Reverted: path.Clean not needed here
	}

	// Normalize ports
	providedPort := normalizePort(parsedProvidedURI.Scheme, parsedProvidedURI.Port())
	expectedPort := normalizePort(expectedURI.Scheme, expectedURI.Port())

	// Compare Scheme, Hostname, normalized Port, Path, RawQuery, and Fragment
	if parsedProvidedURI.Scheme != expectedURI.Scheme ||
		parsedProvidedURI.Hostname() != expectedURI.Hostname() || // Compare Hostname instead of Host
		providedPort != expectedPort || // Compare normalized ports
		providedPath != expectedPath ||
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
