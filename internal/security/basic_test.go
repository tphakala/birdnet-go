package security

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
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

	parsedExpectedURI := parseURLOrFail(t, expectedRedirectURI)

	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					ClientID:    expectedClientID,
					RedirectURI: expectedRedirectURI,
				},
			},
		},
		authCodes:                make(map[string]AuthCode),
		accessTokens:             make(map[string]AccessToken),
		ExpectedBasicRedirectURI: parsedExpectedURI,
	}

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

// Successfully authenticate with valid client credentials and receive an access token
func TestHandleBasicAuthTokenSuccess(t *testing.T) {
	e := echo.New()
	formData := strings.NewReader("grant_type=authorization_code&code=validCode&redirect_uri=http://example.com/callback")
	req := httptest.NewRequest(http.MethodPost, "/", formData)
	req.Header.Set(echo.HeaderAuthorization, "Basic "+base64.StdEncoding.EncodeToString([]byte("validClientID:validClientSecret")))
	req.Header.Set(echo.HeaderContentType, "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Initialize Gothic session
	gothic.Store = sessions.NewFilesystemStore(os.TempDir(), []byte("secret-key"))

	parsedExpectedCallbackURI := parseURLOrFail(t, "http://example.com/callback")

	s := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					ClientID:       "validClientID",
					ClientSecret:   "validClientSecret",
					AccessTokenExp: time.Hour,
				},
				Host: "example.com",
			},
		},
		authCodes:                make(map[string]AuthCode),
		accessTokens:             make(map[string]AccessToken),
		ExpectedBasicRedirectURI: parsedExpectedCallbackURI,
	}

	// Pre-populate a valid auth code
	s.authCodes["validCode"] = AuthCode{
		Code:      "validCode",
		ExpiresAt: time.Now().Add(time.Minute),
	}

	err := s.HandleBasicAuthToken(c)
	require.NoError(t, err, "HandleBasicAuthToken should not return error")

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err, "should unmarshal response JSON")

	assert.NotEmpty(t, response["access_token"], "expected access token in response")
}

// Handle missing grant_type, code, or redirect_uri fields gracefully
func TestHandleBasicAuthTokenMissingFields(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req.Header.Set(echo.HeaderAuthorization, "Basic "+base64.StdEncoding.EncodeToString([]byte("validClientID:validClientSecret")))
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	s := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					ClientID:     "validClientID",
					ClientSecret: "validClientSecret",
				},
			},
		},
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	c.SetParamNames("grant_type", "code", "redirect_uri")
	c.SetParamValues("", "", "")

	err := s.HandleBasicAuthToken(c)
	require.NoError(t, err, "HandleBasicAuthToken should not return error")

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var response map[string]string
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err, "should unmarshal response JSON")

	assert.Equal(t, "Missing required fields", response["error"])
}

// TestHandleBasicAuthCallback tests the basic authorization callback handler
func TestHandleBasicAuthCallback(t *testing.T) {
	// Initialize Gothic session store for tests
	gothic.Store = sessions.NewFilesystemStore(os.TempDir(), []byte("test-secret-key"))

	tests := []struct {
		name           string
		code           string
		redirect       string
		setupServer    func(*OAuth2Server)
		expectedStatus int
		expectedBody   string
		checkRedirect  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:           "missing authorization code",
			code:           "",
			redirect:       "/dashboard",
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Missing authorization code",
		},
		{
			name:     "valid code with valid redirect",
			code:     "valid_code",
			redirect: "/dashboard",
			setupServer: func(s *OAuth2Server) {
				s.authCodes["valid_code"] = AuthCode{
					Code:      "valid_code",
					ExpiresAt: time.Now().Add(time.Minute),
				}
			},
			expectedStatus: http.StatusFound,
			checkRedirect: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				location := rec.Header().Get("Location")
				assert.Equal(t, "/dashboard", location)
			},
		},
		{
			name:     "valid code with empty redirect uses default",
			code:     "valid_code",
			redirect: "",
			setupServer: func(s *OAuth2Server) {
				s.authCodes["valid_code"] = AuthCode{
					Code:      "valid_code",
					ExpiresAt: time.Now().Add(time.Minute),
				}
			},
			expectedStatus: http.StatusFound,
			checkRedirect: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				location := rec.Header().Get("Location")
				assert.Equal(t, "/", location)
			},
		},
		{
			name:     "expired authorization code",
			code:     "expired_code",
			redirect: "/dashboard",
			setupServer: func(s *OAuth2Server) {
				s.authCodes["expired_code"] = AuthCode{
					Code:      "expired_code",
					ExpiresAt: time.Now().Add(-time.Minute), // Already expired
				}
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "Unable to complete login at this time. Please try again.",
		},
		{
			name:           "invalid authorization code",
			code:           "nonexistent_code",
			redirect:       "/dashboard",
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "Unable to complete login at this time. Please try again.",
		},
		{
			name:     "redirect with protocol-relative URL rejected",
			code:     "valid_code",
			redirect: "//evil.com/attack",
			setupServer: func(s *OAuth2Server) {
				s.authCodes["valid_code"] = AuthCode{
					Code:      "valid_code",
					ExpiresAt: time.Now().Add(time.Minute),
				}
			},
			expectedStatus: http.StatusFound,
			checkRedirect: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				location := rec.Header().Get("Location")
				assert.Equal(t, "/", location, "should use default redirect for protocol-relative URL")
			},
		},
		{
			name:     "redirect with absolute URL rejected",
			code:     "valid_code",
			redirect: "https://evil.com/attack",
			setupServer: func(s *OAuth2Server) {
				s.authCodes["valid_code"] = AuthCode{
					Code:      "valid_code",
					ExpiresAt: time.Now().Add(time.Minute),
				}
			},
			expectedStatus: http.StatusFound,
			checkRedirect: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				location := rec.Header().Get("Location")
				assert.Equal(t, "/", location, "should use default redirect for absolute URL")
			},
		},
		{
			name:     "redirect with backslash-relative URL rejected",
			code:     "valid_code",
			redirect: "/\\evil.com",
			setupServer: func(s *OAuth2Server) {
				s.authCodes["valid_code"] = AuthCode{
					Code:      "valid_code",
					ExpiresAt: time.Now().Add(time.Minute),
				}
			},
			expectedStatus: http.StatusFound,
			checkRedirect: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				location := rec.Header().Get("Location")
				assert.Equal(t, "/", location, "should use default redirect for backslash URL")
			},
		},
		{
			name:     "redirect with CRLF injection rejected",
			code:     "valid_code",
			redirect: "/dashboard%0d%0aSet-Cookie:evil=true",
			setupServer: func(s *OAuth2Server) {
				s.authCodes["valid_code"] = AuthCode{
					Code:      "valid_code",
					ExpiresAt: time.Now().Add(time.Minute),
				}
			},
			expectedStatus: http.StatusFound,
			checkRedirect: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				location := rec.Header().Get("Location")
				assert.Equal(t, "/", location, "should use default redirect for CRLF injection attempt")
			},
		},
		{
			name:     "redirect with query parameters preserved",
			code:     "valid_code",
			redirect: "/dashboard?tab=settings&view=all",
			setupServer: func(s *OAuth2Server) {
				s.authCodes["valid_code"] = AuthCode{
					Code:      "valid_code",
					ExpiresAt: time.Now().Add(time.Minute),
				}
			},
			expectedStatus: http.StatusFound,
			checkRedirect: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				location := rec.Header().Get("Location")
				assert.Equal(t, "/dashboard?tab=settings&view=all", location)
			},
		},
		{
			name:     "valid redirect to root path",
			code:     "valid_code",
			redirect: "/",
			setupServer: func(s *OAuth2Server) {
				s.authCodes["valid_code"] = AuthCode{
					Code:      "valid_code",
					ExpiresAt: time.Now().Add(time.Minute),
				}
			},
			expectedStatus: http.StatusFound,
			checkRedirect: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				location := rec.Header().Get("Location")
				assert.Equal(t, "/", location)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()

			// Build URL with query parameters
			reqURL := "/?code=" + url.QueryEscape(tt.code)
			if tt.redirect != "" {
				reqURL += "&redirect=" + url.QueryEscape(tt.redirect)
			}

			req := httptest.NewRequest(http.MethodGet, reqURL, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			server := &OAuth2Server{
				Settings: &conf.Settings{
					Security: conf.Security{
						BasicAuth: conf.BasicAuth{
							Enabled:        true,
							AccessTokenExp: time.Hour,
						},
					},
				},
				authCodes:    make(map[string]AuthCode),
				accessTokens: make(map[string]AccessToken),
			}

			if tt.setupServer != nil {
				tt.setupServer(server)
			}

			err := server.HandleBasicAuthCallback(c)
			require.NoError(t, err, "HandleBasicAuthCallback should not return error")

			assert.Equal(t, tt.expectedStatus, rec.Code, "unexpected status code")

			if tt.expectedBody != "" {
				assert.Equal(t, tt.expectedBody, rec.Body.String())
			}

			if tt.checkRedirect != nil {
				tt.checkRedirect(t, rec)
			}
		})
	}
}

// TestHandleBasicAuthCallbackSessionRegeneration tests that the session is regenerated after successful login
func TestHandleBasicAuthCallbackSessionRegeneration(t *testing.T) {
	tempDir := t.TempDir()
	gothic.Store = sessions.NewFilesystemStore(tempDir, []byte("test-secret-key"))

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?code=valid_code&redirect=/dashboard", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					Enabled:        true,
					AccessTokenExp: time.Hour,
				},
			},
		},
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	// Add valid auth code
	server.authCodes["valid_code"] = AuthCode{
		Code:      "valid_code",
		ExpiresAt: time.Now().Add(time.Minute),
	}

	err := server.HandleBasicAuthCallback(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, rec.Code)

	// Verify a session cookie was set
	cookies := rec.Result().Cookies()
	assert.NotEmpty(t, cookies, "should set session cookie")

	// Verify an access token was generated and stored
	assert.NotEmpty(t, server.accessTokens, "should have generated an access token")
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

// TestGetIPv4Subnet tests the IPv4 subnet extraction
func TestGetIPv4Subnet(t *testing.T) {
	tests := []struct {
		name     string
		ip       net.IP
		expected string
	}{
		{
			name:     "nil IP returns nil",
			ip:       nil,
			expected: "",
		},
		{
			name:     "IPv6 only returns nil",
			ip:       net.ParseIP("::1"),
			expected: "",
		},
		{
			name:     "valid IPv4 returns /24 subnet",
			ip:       net.ParseIP("192.168.1.100"),
			expected: "192.168.1.0",
		},
		{
			name:     "different subnet",
			ip:       net.ParseIP("10.0.5.42"),
			expected: "10.0.5.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subnet := getIPv4Subnet(tt.ip)
			var result string
			if subnet != nil {
				result = subnet.String()
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

// testBasicLogger is a mock logger that implements logger.Logger for basic.go tests
type testBasicLogger struct {
	debugCalls []string
	warnCalls  []string
	infoCalls  []string
	errorCalls []string
}

func newTestBasicLogger() *testBasicLogger {
	return &testBasicLogger{}
}

func (l *testBasicLogger) Module(_ string) logger.Logger {
	return l
}

func (l *testBasicLogger) Trace(_ string, _ ...logger.Field) {}

func (l *testBasicLogger) Debug(msg string, _ ...logger.Field) {
	l.debugCalls = append(l.debugCalls, msg)
}

func (l *testBasicLogger) Warn(msg string, _ ...logger.Field) {
	l.warnCalls = append(l.warnCalls, msg)
}

func (l *testBasicLogger) Info(msg string, _ ...logger.Field) {
	l.infoCalls = append(l.infoCalls, msg)
}

func (l *testBasicLogger) Error(msg string, _ ...logger.Field) {
	l.errorCalls = append(l.errorCalls, msg)
}

func (l *testBasicLogger) With(_ ...logger.Field) logger.Logger {
	return l
}

func (l *testBasicLogger) WithContext(_ context.Context) logger.Logger {
	return l
}

func (l *testBasicLogger) Log(_ logger.LogLevel, _ string, _ ...logger.Field) {}

func (l *testBasicLogger) Flush() error {
	return nil
}

// TestExchangeCodeWithTimeout tests the code exchange timeout wrapper
func TestExchangeCodeWithTimeout(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		setupServer func(*OAuth2Server)
		expectError bool
	}{
		{
			name: "valid code exchanges successfully",
			code: "valid_code",
			setupServer: func(s *OAuth2Server) {
				s.authCodes["valid_code"] = AuthCode{
					Code:      "valid_code",
					ExpiresAt: time.Now().Add(time.Minute),
				}
			},
			expectError: false,
		},
		{
			name: "expired code returns error",
			code: "expired_code",
			setupServer: func(s *OAuth2Server) {
				s.authCodes["expired_code"] = AuthCode{
					Code:      "expired_code",
					ExpiresAt: time.Now().Add(-time.Minute),
				}
			},
			expectError: true,
		},
		{
			name:        "non-existent code returns error",
			code:        "nonexistent",
			setupServer: func(_ *OAuth2Server) {},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &OAuth2Server{
				Settings: &conf.Settings{
					Security: conf.Security{
						BasicAuth: conf.BasicAuth{
							AccessTokenExp: time.Hour,
						},
					},
				},
				authCodes:    make(map[string]AuthCode),
				accessTokens: make(map[string]AccessToken),
			}
			tt.setupServer(server)

			ctx := t.Context()
			token, err := server.exchangeCodeWithTimeout(ctx, tt.code)

			if tt.expectError {
				require.Error(t, err)
				assert.Empty(t, token)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, token)
			}
		})
	}
}

// TestHandleTokenExchangeError tests error handling for token exchange
func TestHandleTokenExchangeError(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "deadline exceeded returns gateway timeout",
			err:            context.DeadlineExceeded,
			expectedStatus: http.StatusGatewayTimeout,
			expectedBody:   "Login timed out. Please try again.",
		},
		{
			name:           "generic error returns internal server error",
			err:            errors.New("some error"),
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "Unable to complete login at this time. Please try again.",
		},
		{
			name:           "wrapped deadline exceeded returns gateway timeout",
			err:            fmt.Errorf("wrapped: %w", context.DeadlineExceeded),
			expectedStatus: http.StatusGatewayTimeout,
			expectedBody:   "Login timed out. Please try again.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			server := &OAuth2Server{
				Settings: &conf.Settings{},
			}

			log := newTestBasicLogger()
			err := server.handleTokenExchangeError(c, tt.err, log)

			require.NoError(t, err, "handler should not return error")
			assert.Equal(t, tt.expectedStatus, rec.Code)
			assert.Equal(t, tt.expectedBody, rec.Body.String())
			assert.NotEmpty(t, log.warnCalls, "should have logged a warning")
		})
	}
}

// TestRegenerateAndStoreToken tests session regeneration and token storage
func TestRegenerateAndStoreToken(t *testing.T) {
	tests := []struct {
		name           string
		accessToken    string
		expectError    bool
		expectedStatus int
	}{
		{
			name:        "successfully stores token",
			accessToken: "valid_access_token",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			gothic.Store = sessions.NewFilesystemStore(tempDir, []byte("test-secret-key"))

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			server := &OAuth2Server{
				Settings: &conf.Settings{},
			}

			log := newTestBasicLogger()
			err := server.regenerateAndStoreToken(c, tt.accessToken, log)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				// Verify session cookie was set
				cookies := rec.Result().Cookies()
				assert.NotEmpty(t, cookies, "should set session cookie")
			}
		})
	}
}

// TestExchangeCodeWithTimeoutContextCancellation tests context cancellation
func TestExchangeCodeWithTimeoutContextCancellation(t *testing.T) {
	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					AccessTokenExp: time.Hour,
				},
			},
		},
		authCodes: map[string]AuthCode{
			"valid_code": {
				Code:      "valid_code",
				ExpiresAt: time.Now().Add(time.Minute),
			},
		},
		accessTokens: make(map[string]AccessToken),
	}

	// Create an already cancelled context
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	token, err := server.exchangeCodeWithTimeout(ctx, "valid_code")
	require.Error(t, err)
	assert.Empty(t, token)
	assert.ErrorIs(t, err, context.Canceled)
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
