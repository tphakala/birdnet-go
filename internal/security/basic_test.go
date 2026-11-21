package security

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// parseURLOrFail parses a URL string and fails the test if parsing fails
func parseURLOrFail(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsedURL, err := url.Parse(rawURL)
	require.NoError(t, err, "Failed to parse URL '%s'", rawURL)
	return parsedURL
}

// setupOAuth2ServerTest creates a test OAuth2 server with configurable client credentials
func setupOAuth2ServerTest(t *testing.T, requestClientID, requestRedirectURI, expectedClientID, expectedRedirectURI string) (*OAuth2Server, echo.Context, *httptest.ResponseRecorder) {
	t.Helper()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?client_id="+requestClientID+"&redirect_uri="+requestRedirectURI, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	server := &OAuth2Server{
		settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					ClientID:    expectedClientID,
					RedirectURI: expectedRedirectURI,
				},
			},
		},
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	// Publish the settings as the global snapshot so currentSettings() (used by
	// the basic-auth handlers after the issue #3370 fix) returns these values
	// rather than a snapshot leaked by a sibling test.
	conftest.SetTestSettings(server.settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	return server, c, rec
}

// setupOAuth2ServerTestWithValidCredentials creates a test OAuth2 server with matching client credentials
func setupOAuth2ServerTestWithValidCredentials(t *testing.T, clientID, redirectURI string) (*OAuth2Server, echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	return setupOAuth2ServerTest(t, clientID, redirectURI, clientID, redirectURI)
}

// Correct client_id and redirect_uri result in redirection with auth code
func TestHandleBasicAuthorizeSuccess(t *testing.T) {
	server, c, rec := setupOAuth2ServerTestWithValidCredentials(t, "validClientID", "http://valid.redirect")

	err := server.HandleBasicAuthorize(c)
	require.NoError(t, err, "HandleBasicAuthorize should not return error")

	assert.Equal(t, http.StatusFound, rec.Code)

	location := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(location, "http://valid.redirect?code="), "unexpected redirect location: %s", location)
}

// Invalid client_id returns a 400 Bad Request
func TestHandleBasicAuthorizeInvalidClientID(t *testing.T) {
	// Use an explicitly invalid client ID in request, but valid one in server config
	server, c, rec := setupOAuth2ServerTest(t, "invalidClientID", "http://valid.redirect", "validClientID", "http://valid.redirect")

	_ = server.HandleBasicAuthorize(c) // Error is checked via HTTP response code in test
	resp := rec.Result()
	require.NotNil(t, resp, "expected a response")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "Invalid client_id", rec.Body.String())
}

// Auth code generation succeeds without errors
func TestHandleBasicAuthorizeAuthCodeGeneration(t *testing.T) {
	server, c, rec := setupOAuth2ServerTestWithValidCredentials(t, "validClientID", "http://valid.redirect")

	err := server.HandleBasicAuthorize(c)
	require.NoError(t, err, "HandleBasicAuthorize should not return error")

	assert.Equal(t, http.StatusFound, rec.Code)

	location := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(location, "http://valid.redirect?code="), "unexpected redirect location: %s", location)
}

// Valid client_id and redirect_uri parameters are correctly parsed from query
func TestHandleBasicAuthorizeValidParameters(t *testing.T) {
	server, c, rec := setupOAuth2ServerTestWithValidCredentials(t, "validClientID", "http://valid.redirect")

	err := server.HandleBasicAuthorize(c)
	require.NoError(t, err, "HandleBasicAuthorize should not return error")

	assert.Equal(t, http.StatusFound, rec.Code)

	location := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(location, "http://valid.redirect?code="), "unexpected redirect location: %s", location)
}

// TestIsInLocalSubnet tests the local subnet detection
func TestIsInLocalSubnet(t *testing.T) {
	tests := []struct {
		name     string
		ip       net.IP
		expected bool
	}{
		{
			name:     "nil IP returns false",
			ip:       nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsInLocalSubnet(tt.ip)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsInLocalSubnetIPv6LinkLocal tests IPv6 link-local detection
func TestIsInLocalSubnetIPv6LinkLocal(t *testing.T) {
	tests := []struct {
		name     string
		ip       net.IP
		expected bool
	}{
		{
			name:     "nil IP returns false",
			ip:       nil,
			expected: false,
		},
		{
			name:     "IPv6 loopback is not link-local",
			ip:       net.ParseIP("::1"),
			expected: false,
		},
		{
			name:     "IPv6 link-local returns true",
			ip:       net.ParseIP("fe80::1"),
			expected: true,
		},
		{
			name:     "IPv6 link-local with long address returns true",
			ip:       net.ParseIP("fe80::1cb6:63bc:5462:71c5"),
			expected: true,
		},
		{
			name:     "IPv6 global unicast is not link-local",
			ip:       net.ParseIP("2001:db8::1"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := IsInLocalSubnet(tt.ip)
			if tt.ip == nil {
				assert.False(t, result, "nil IP should always return false")
			} else if tt.expected {
				assert.True(t, result, "Expected link-local IP to be treated as local subnet")
			}
			// Note: for non-link-local IPs, result depends on actual network interfaces,
			// so we only assert the positive cases here.
		})
	}
}

// TestValidateRedirectURI tests the redirect URI validation
func TestValidateRedirectURI(t *testing.T) {
	tests := []struct {
		name        string
		provided    string
		expected    *url.URL
		expectError bool
	}{
		{
			name:        "nil expected URI returns error",
			provided:    "http://localhost/callback",
			expected:    nil,
			expectError: true,
		},
		{
			name:        "matching URIs pass validation",
			provided:    "http://localhost:8080/callback",
			expected:    parseURLOrFail(t, "http://localhost:8080/callback"),
			expectError: false,
		},
		{
			name:        "different scheme fails",
			provided:    "http://localhost:8080/callback",
			expected:    parseURLOrFail(t, "https://localhost:8080/callback"),
			expectError: true,
		},
		{
			name:        "different host fails",
			provided:    "http://evil.com/callback",
			expected:    parseURLOrFail(t, "http://localhost/callback"),
			expectError: true,
		},
		{
			name:        "different port fails",
			provided:    "http://localhost:9090/callback",
			expected:    parseURLOrFail(t, "http://localhost:8080/callback"),
			expectError: true,
		},
		{
			name:        "different path fails",
			provided:    "http://localhost:8080/other",
			expected:    parseURLOrFail(t, "http://localhost:8080/callback"),
			expectError: true,
		},
		{
			name:        "query parameters in provided fails",
			provided:    "http://localhost:8080/callback?param=value",
			expected:    parseURLOrFail(t, "http://localhost:8080/callback"),
			expectError: true,
		},
		{
			name:        "fragment in provided fails",
			provided:    "http://localhost:8080/callback#section",
			expected:    parseURLOrFail(t, "http://localhost:8080/callback"),
			expectError: true,
		},
		{
			name:        "case insensitive scheme comparison",
			provided:    "HTTP://localhost:8080/callback",
			expected:    parseURLOrFail(t, "http://localhost:8080/callback"),
			expectError: false,
		},
		{
			name:        "case insensitive host comparison",
			provided:    "http://LOCALHOST:8080/callback",
			expected:    parseURLOrFail(t, "http://localhost:8080/callback"),
			expectError: false,
		},
		{
			name:        "default port normalization for https",
			provided:    "https://localhost/callback",
			expected:    parseURLOrFail(t, "https://localhost:443/callback"),
			expectError: false,
		},
		{
			name:        "default port normalization for http",
			provided:    "http://localhost/callback",
			expected:    parseURLOrFail(t, "http://localhost:80/callback"),
			expectError: false,
		},
		{
			name:        "path normalization with trailing slash",
			provided:    "http://localhost/callback/",
			expected:    parseURLOrFail(t, "http://localhost/callback"),
			expectError: false, // path.Clean normalizes both to /callback, so they match
		},
		{
			name:        "invalid provided URI format",
			provided:    "://invalid",
			expected:    parseURLOrFail(t, "http://localhost/callback"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRedirectURI(tt.provided, tt.expected)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
