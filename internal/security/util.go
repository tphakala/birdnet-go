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
		providedPath = strings.TrimSuffix(providedPath, "/")
	}
	expectedPath := expectedURI.Path
	if expectedPath != "/" {
		expectedPath = strings.TrimSuffix(expectedPath, "/")
	}

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
