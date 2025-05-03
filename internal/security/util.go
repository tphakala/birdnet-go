package security

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateRedirectURI parses the provided URI string and compares its essential components
// (Scheme, Host, Path) against the pre-parsed expected URI.
// It returns an error if the URIs do not match or if parsing fails.
func ValidateRedirectURI(providedURIString string, expectedURI *url.URL) error {
	if expectedURI == nil {
		// This should ideally not happen if initialization is correct
		return fmt.Errorf("internal error: expected redirect URI is nil")
	}

	parsedProvidedURI, err := url.Parse(providedURIString)
	if err != nil {
		return fmt.Errorf("invalid redirect_uri format: %w", err)
	}

	// Normalize paths by removing trailing slash for consistent comparison
	providedPath := strings.TrimSuffix(parsedProvidedURI.Path, "/")
	expectedPath := strings.TrimSuffix(expectedURI.Path, "/")

	// Compare Scheme, Host, and Path
	if parsedProvidedURI.Scheme != expectedURI.Scheme ||
		parsedProvidedURI.Host != expectedURI.Host ||
		providedPath != expectedPath {
		// Construct a clearer error message showing the difference
		return fmt.Errorf("invalid redirect_uri: provided '%s' (parsed as Scheme=%s, Host=%s, Path=%s) does not match expected (Scheme=%s, Host=%s, Path=%s)",
			providedURIString,
			parsedProvidedURI.Scheme, parsedProvidedURI.Host, providedPath,
			expectedURI.Scheme, expectedURI.Host, expectedPath)
	}

	return nil // URIs match
}
