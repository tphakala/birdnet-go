package security

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// TestIsUserAuthenticatedValidAccessToken tests the IsUserAuthenticated function with a valid access token
func TestIsUserAuthenticatedValidAccessToken(t *testing.T) {
	// Set the settings instance
	conf.Setting()

	settings := &conf.Settings{
		Security: conf.Security{
			SessionSecret: "test-secret",
		},
	}

	s := NewOAuth2Server()

	// Initialize gothic exactly as in production
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Store token using gothic's method
	_ = gothic.StoreInSession("access_token", "valid_token", req, rec) // Test setup, error not relevant

	// Add cookie to request
	req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))

	// Add token to OAuth2Server's valid tokens
	s.accessTokens["valid_token"] = AccessToken{
		Token:     "valid_token",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	isAuthenticated := s.IsUserAuthenticated(c)
	assert.True(t, isAuthenticated, "expected IsUserAuthenticated to return true")
}

// TestIsUserAuthenticatedTableDriven tests the IsUserAuthenticated function with a table-driven approach
func TestIsUserAuthenticatedTableDriven(t *testing.T) {
	// Set the settings instance
	conf.Setting()

	tests := []struct {
		name    string
		token   string
		expires time.Duration
		want    bool
	}{
		{
			name:    "valid token",
			token:   "valid_token",
			expires: time.Hour,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &conf.Settings{
				Security: conf.Security{
					SessionSecret: "test-secret",
				},
			}

			s := NewOAuth2Server()

			// Initialize gothic exactly as in production
			gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Store token using gothic's method
			_ = gothic.StoreInSession("access_token", tt.token, req, rec) // Test setup, error not relevant

			// Add cookie to request
			req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))

			// Add token to OAuth2Server's valid tokens
			s.accessTokens[tt.token] = AccessToken{
				Token:     tt.token,
				ExpiresAt: time.Now().Add(tt.expires),
			}

			got := s.IsUserAuthenticated(c)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOAuth2Server(t *testing.T) {
	// Set the settings instance
	conf.Setting()

	tests := []struct {
		name string
		test func(*testing.T, *OAuth2Server)
	}{
		{
			name: "generate and validate auth code",
			test: func(t *testing.T, s *OAuth2Server) {
				t.Helper()
				// Initialize settings
				s.Settings = &conf.Settings{
					Security: conf.Security{
						BasicAuth: conf.BasicAuth{
							Enabled:        true,
							ClientID:       "test-client",
							ClientSecret:   "test-secret",
							AuthCodeExp:    10 * time.Minute,
							AccessTokenExp: 1 * time.Hour,
						},
					},
				}

				// Generate and immediately use the auth code
				code, err := s.GenerateAuthCode()
				require.NoError(t, err, "should generate auth code")

				// Pass test context to ExchangeAuthCode
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				token, err := s.ExchangeAuthCode(ctx, code)
				require.NoError(t, err, "should exchange auth code")

				err = s.ValidateAccessToken(token)
				assert.NoError(t, err, "token should be valid")
			},
		},
		{
			name: "subnet bypass validation",
			test: func(t *testing.T, s *OAuth2Server) {
				t.Helper()
				s.Settings.Security.AllowSubnetBypass = conf.AllowSubnetBypass{
					Enabled: true,
					Subnet:  "192.168.1.0/24",
				}

				assert.True(t, s.IsRequestFromAllowedSubnet("192.168.1.100"), "expected IP to be allowed")
				assert.False(t, s.IsRequestFromAllowedSubnet("10.0.0.1"), "expected IP to be denied")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewOAuth2Server()
			tt.test(t, s)
		})
	}
}

// TestNewOAuth2ServerForTesting tests the test constructor
func TestNewOAuth2ServerForTesting(t *testing.T) {
	settings := &conf.Settings{
		Security: conf.Security{
			Debug: true,
			BasicAuth: conf.BasicAuth{
				Enabled:        true,
				ClientID:       "test-client",
				ClientSecret:   "test-secret",
				AuthCodeExp:    10 * time.Minute,
				AccessTokenExp: time.Hour,
			},
		},
	}

	server := NewOAuth2ServerForTesting(settings)

	require.NotNil(t, server)
	assert.Equal(t, settings, server.Settings)
	assert.NotNil(t, server.authCodes)
	assert.NotNil(t, server.accessTokens)
	assert.NotNil(t, server.throttledMessages)
}

// TestIsUserAuthenticatedExpiredToken tests with an expired access token
func TestIsUserAuthenticatedExpiredToken(t *testing.T) {
	conf.Setting()

	settings := &conf.Settings{
		Security: conf.Security{
			SessionSecret: "test-secret-32-bytes-minimum-len",
		},
	}

	server := NewOAuth2ServerForTesting(settings)
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Store token using gothic's method
	_ = gothic.StoreInSession("access_token", "expired_token", req, rec)
	req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))

	// Add expired token to OAuth2Server
	server.accessTokens["expired_token"] = AccessToken{
		Token:     "expired_token",
		ExpiresAt: time.Now().Add(-time.Hour), // Expired
	}

	isAuthenticated := server.IsUserAuthenticated(c)
	assert.False(t, isAuthenticated, "expired token should not authenticate")
}

// TestIsUserAuthenticatedNoToken tests with no access token
func TestIsUserAuthenticatedNoToken(t *testing.T) {
	conf.Setting()

	settings := &conf.Settings{
		Security: conf.Security{
			SessionSecret: "test-secret-32-bytes-minimum-len",
		},
	}

	server := NewOAuth2ServerForTesting(settings)
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// No token stored
	isAuthenticated := server.IsUserAuthenticated(c)
	assert.False(t, isAuthenticated, "no token should not authenticate")
}

// TestIsUserAuthenticatedGoogleAuth tests Google authentication path
// Note: Testing social auth with Gothic requires complex session setup.
// The isValidUserId function is tested separately in TestIsValidUserId.
// The code paths are exercised in integration tests.
func TestIsUserAuthenticatedGoogleAuth(t *testing.T) {
	t.Skip("Skipping: Gothic session handling requires integration test setup")
}

// TestIsUserAuthenticatedGoogleAuthWrongUser tests Google auth with wrong user
// Note: Testing social auth with Gothic requires complex session setup.
func TestIsUserAuthenticatedGoogleAuthWrongUser(t *testing.T) {
	t.Skip("Skipping: Gothic session handling requires integration test setup")
}

// TestIsUserAuthenticatedGithubAuth tests GitHub authentication path
// Note: Testing social auth with Gothic requires complex session setup.
func TestIsUserAuthenticatedGithubAuth(t *testing.T) {
	t.Skip("Skipping: Gothic session handling requires integration test setup")
}

// TestIsValidUserId tests the isValidUserId helper function
func TestIsValidUserId(t *testing.T) {
	tests := []struct {
		name          string
		configuredIds string
		providedId    string
		expected      bool
	}{
		{
			name:          "empty configured IDs",
			configuredIds: "",
			providedId:    "user@example.com",
			expected:      false,
		},
		{
			name:          "empty provided ID",
			configuredIds: "user@example.com",
			providedId:    "",
			expected:      false,
		},
		{
			name:          "exact match",
			configuredIds: "user@example.com",
			providedId:    "user@example.com",
			expected:      true,
		},
		{
			name:          "case insensitive match",
			configuredIds: "User@Example.com",
			providedId:    "user@example.com",
			expected:      true,
		},
		{
			name:          "multiple IDs with match",
			configuredIds: "admin@example.com, user@example.com, guest@example.com",
			providedId:    "user@example.com",
			expected:      true,
		},
		{
			name:          "multiple IDs no match",
			configuredIds: "admin@example.com, user@example.com",
			providedId:    "other@example.com",
			expected:      false,
		},
		{
			name:          "whitespace handling",
			configuredIds: "  user@example.com  ",
			providedId:    "  user@example.com  ",
			expected:      true,
		},
		{
			name:          "only whitespace in provided ID",
			configuredIds: "user@example.com",
			providedId:    "   ",
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidUserId(tt.configuredIds, tt.providedId)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestValidateAccessToken tests the ValidateAccessToken method
func TestValidateAccessToken(t *testing.T) {
	server := &OAuth2Server{
		Settings:     &conf.Settings{},
		accessTokens: make(map[string]AccessToken),
	}

	// Add a valid token
	server.accessTokens["valid_token"] = AccessToken{
		Token:     "valid_token",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	// Add an expired token
	server.accessTokens["expired_token"] = AccessToken{
		Token:     "expired_token",
		ExpiresAt: time.Now().Add(-time.Hour),
	}

	tests := []struct {
		name        string
		token       string
		expectedErr error
	}{
		{
			name:        "valid token",
			token:       "valid_token",
			expectedErr: nil,
		},
		{
			name:        "expired token",
			token:       "expired_token",
			expectedErr: ErrTokenExpired,
		},
		{
			name:        "non-existent token",
			token:       "nonexistent",
			expectedErr: ErrTokenNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := server.ValidateAccessToken(tt.token)
			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGenerateAuthCode tests the GenerateAuthCode method
func TestGenerateAuthCode(t *testing.T) {
	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					AuthCodeExp: 10 * time.Minute,
				},
			},
		},
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	code, err := server.GenerateAuthCode()
	require.NoError(t, err)
	assert.NotEmpty(t, code)

	// Verify code was stored
	assert.Len(t, server.authCodes, 1)

	// Verify code exists and has valid expiration
	authCode, exists := server.authCodes[code]
	assert.True(t, exists)
	assert.True(t, authCode.ExpiresAt.After(time.Now()))
}

// TestExchangeAuthCode tests the ExchangeAuthCode method
func TestExchangeAuthCode(t *testing.T) {
	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					AuthCodeExp:    10 * time.Minute,
					AccessTokenExp: time.Hour,
				},
			},
		},
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	// Add a valid auth code
	server.authCodes["valid_code"] = AuthCode{
		Code:      "valid_code",
		ExpiresAt: time.Now().Add(time.Minute),
	}

	// Add an expired auth code
	server.authCodes["expired_code"] = AuthCode{
		Code:      "expired_code",
		ExpiresAt: time.Now().Add(-time.Minute),
	}

	tests := []struct {
		name        string
		code        string
		expectError bool
	}{
		{
			name:        "valid code",
			code:        "valid_code",
			expectError: false,
		},
		{
			name:        "expired code",
			code:        "expired_code",
			expectError: true,
		},
		{
			name:        "non-existent code",
			code:        "nonexistent",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			token, err := server.ExchangeAuthCode(ctx, tt.code)

			if tt.expectError {
				require.Error(t, err)
				assert.Empty(t, token)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, token)

				// Verify auth code was consumed
				_, exists := server.authCodes[tt.code]
				assert.False(t, exists, "auth code should be deleted after exchange")

				// Verify access token was created
				assert.Contains(t, server.accessTokens, token)
			}
		})
	}
}

// TestExchangeAuthCodeContextCancellation tests context cancellation handling
func TestExchangeAuthCodeContextCancellation(t *testing.T) {
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

	server.authCodes["valid_code"] = AuthCode{
		Code:      "valid_code",
		ExpiresAt: time.Now().Add(time.Minute),
	}

	// Create a cancelled context
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	token, err := server.ExchangeAuthCode(ctx, "valid_code")
	require.Error(t, err)
	assert.Empty(t, token)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestIsAuthenticationEnabled tests the IsAuthenticationEnabled method
func TestIsAuthenticationEnabled(t *testing.T) {
	tests := []struct {
		name     string
		settings *conf.Settings
		ip       string
		expected bool
	}{
		{
			name: "no auth enabled",
			settings: &conf.Settings{
				Security: conf.Security{
					BasicAuth:  conf.BasicAuth{Enabled: false},
					GoogleAuth: conf.SocialProvider{Enabled: false},
					GithubAuth: conf.SocialProvider{Enabled: false},
				},
			},
			ip:       "192.168.1.100",
			expected: false,
		},
		{
			name: "basic auth enabled",
			settings: &conf.Settings{
				Security: conf.Security{
					BasicAuth: conf.BasicAuth{Enabled: true},
				},
			},
			ip:       "192.168.1.100",
			expected: true,
		},
		{
			name: "google auth enabled",
			settings: &conf.Settings{
				Security: conf.Security{
					OAuthProviders: []conf.OAuthProviderConfig{
						{Provider: "google", Enabled: true, ClientID: "test", ClientSecret: "test"},
					},
				},
			},
			ip:       "192.168.1.100",
			expected: true,
		},
		{
			name: "github auth enabled",
			settings: &conf.Settings{
				Security: conf.Security{
					OAuthProviders: []conf.OAuthProviderConfig{
						{Provider: "github", Enabled: true, ClientID: "test", ClientSecret: "test"},
					},
				},
			},
			ip:       "192.168.1.100",
			expected: true,
		},
		{
			name: "auth enabled but subnet bypass active",
			settings: &conf.Settings{
				Security: conf.Security{
					BasicAuth: conf.BasicAuth{Enabled: true},
					AllowSubnetBypass: conf.AllowSubnetBypass{
						Enabled: true,
						Subnet:  "192.168.1.0/24",
					},
				},
			},
			ip:       "192.168.1.100",
			expected: false, // Subnet bypass means auth not required
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewOAuth2ServerForTesting(tt.settings)
			result := server.IsAuthenticationEnabled(tt.ip)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsRequestFromAllowedSubnet tests the IsRequestFromAllowedSubnet method
func TestIsRequestFromAllowedSubnet(t *testing.T) {
	tests := []struct {
		name     string
		settings *conf.Settings
		ip       string
		expected bool
	}{
		{
			name: "subnet bypass disabled",
			settings: &conf.Settings{
				Security: conf.Security{
					AllowSubnetBypass: conf.AllowSubnetBypass{
						Enabled: false,
						Subnet:  "192.168.1.0/24",
					},
				},
			},
			ip:       "192.168.1.100",
			expected: false,
		},
		{
			name: "empty IP string",
			settings: &conf.Settings{
				Security: conf.Security{
					AllowSubnetBypass: conf.AllowSubnetBypass{
						Enabled: true,
						Subnet:  "192.168.1.0/24",
					},
				},
			},
			ip:       "",
			expected: false,
		},
		{
			name: "invalid IP string",
			settings: &conf.Settings{
				Security: conf.Security{
					AllowSubnetBypass: conf.AllowSubnetBypass{
						Enabled: true,
						Subnet:  "192.168.1.0/24",
					},
				},
			},
			ip:       "invalid",
			expected: false,
		},
		{
			name: "loopback IP",
			settings: &conf.Settings{
				Security: conf.Security{
					AllowSubnetBypass: conf.AllowSubnetBypass{
						Enabled: true,
						Subnet:  "192.168.1.0/24",
					},
				},
			},
			ip:       "127.0.0.1",
			expected: true,
		},
		{
			name: "IP in allowed subnet",
			settings: &conf.Settings{
				Security: conf.Security{
					AllowSubnetBypass: conf.AllowSubnetBypass{
						Enabled: true,
						Subnet:  "192.168.1.0/24",
					},
				},
			},
			ip:       "192.168.1.100",
			expected: true,
		},
		{
			name: "IP not in allowed subnet",
			settings: &conf.Settings{
				Security: conf.Security{
					AllowSubnetBypass: conf.AllowSubnetBypass{
						Enabled: true,
						Subnet:  "192.168.1.0/24",
					},
				},
			},
			ip:       "10.0.0.1",
			expected: false,
		},
		{
			name: "multiple subnets with match",
			settings: &conf.Settings{
				Security: conf.Security{
					AllowSubnetBypass: conf.AllowSubnetBypass{
						Enabled: true,
						Subnet:  "192.168.1.0/24, 10.0.0.0/8",
					},
				},
			},
			ip:       "10.0.0.1",
			expected: true,
		},
		{
			name: "empty subnet string",
			settings: &conf.Settings{
				Security: conf.Security{
					AllowSubnetBypass: conf.AllowSubnetBypass{
						Enabled: true,
						Subnet:  "",
					},
				},
			},
			ip:       "192.168.1.100",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewOAuth2ServerForTesting(tt.settings)
			result := server.IsRequestFromAllowedSubnet(tt.ip)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestValidateSessionSecret tests the session secret validation helper
func TestValidateSessionSecret(t *testing.T) {
	tests := []struct {
		name           string
		sessionSecret  string
		expectModified bool // Whether the secret should be modified (empty case)
	}{
		{
			name:           "empty secret gets replaced",
			sessionSecret:  "",
			expectModified: true,
		},
		{
			name:           "short secret triggers warning but not replacement",
			sessionSecret:  "short",
			expectModified: false,
		},
		{
			name:           "adequate secret (32 chars) is valid",
			sessionSecret:  "12345678901234567890123456789012",
			expectModified: false,
		},
		{
			name:           "long secret is valid",
			sessionSecret:  "this-is-a-very-long-session-secret-that-exceeds-minimum-length",
			expectModified: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &conf.Settings{
				Security: conf.Security{
					SessionSecret: tt.sessionSecret,
				},
			}

			originalSecret := settings.Security.SessionSecret
			validateSessionSecret(settings)

			if tt.expectModified {
				assert.NotEqual(t, originalSecret, settings.Security.SessionSecret, "empty secret should be replaced")
				assert.NotEmpty(t, settings.Security.SessionSecret, "secret should not be empty after validation")
			} else {
				assert.Equal(t, originalSecret, settings.Security.SessionSecret, "non-empty secret should not be modified")
			}
		})
	}
}

// TestParseBasicAuthRedirectURI tests the redirect URI parsing helper
func TestParseBasicAuthRedirectURI(t *testing.T) {
	tests := []struct {
		name        string
		redirectURI string
		expectNil   bool
		expectedURI string
	}{
		{
			name:        "empty URI returns nil",
			redirectURI: "",
			expectNil:   true,
		},
		{
			name:        "valid HTTP URI",
			redirectURI: "http://localhost:8080/callback",
			expectNil:   false,
			expectedURI: "http://localhost:8080/callback",
		},
		{
			name:        "valid HTTPS URI",
			redirectURI: "https://example.com/oauth/callback",
			expectNil:   false,
			expectedURI: "https://example.com/oauth/callback",
		},
		{
			name:        "URI with query parameters rejected",
			redirectURI: "http://localhost:8080/callback?extra=param",
			expectNil:   true,
		},
		{
			name:        "URI with fragment rejected",
			redirectURI: "http://localhost:8080/callback#section",
			expectNil:   true,
		},
		{
			name:        "URI with both query and fragment rejected",
			redirectURI: "http://localhost:8080/callback?param=value#section",
			expectNil:   true,
		},
		{
			name:        "invalid URI format returns nil",
			redirectURI: "://invalid",
			expectNil:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &conf.Settings{
				Security: conf.Security{
					BasicAuth: conf.BasicAuth{
						RedirectURI: tt.redirectURI,
					},
				},
			}

			result := parseBasicAuthRedirectURI(settings)

			if tt.expectNil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.expectedURI, result.String())
			}
		})
	}
}

// testLogger is a mock logger that implements the logger.Logger interface for testing
type testLogger struct{}

func (testLogger) Module(_ string) logger.Logger                      { return testLogger{} }
func (testLogger) Trace(_ string, _ ...logger.Field)                  {}
func (testLogger) Debug(_ string, _ ...logger.Field)                  {}
func (testLogger) Info(_ string, _ ...logger.Field)                   {}
func (testLogger) Warn(_ string, _ ...logger.Field)                   {}
func (testLogger) Error(_ string, _ ...logger.Field)                  {}
func (testLogger) With(_ ...logger.Field) logger.Logger               { return testLogger{} }
func (testLogger) WithContext(_ context.Context) logger.Logger        { return testLogger{} }
func (testLogger) Log(_ logger.LogLevel, _ string, _ ...logger.Field) {}
func (testLogger) Flush() error                                       { return nil }

// TestCheckBasicAuthToken tests the basic auth token validation helper
func TestCheckBasicAuthToken(t *testing.T) {
	tests := []struct {
		name           string
		setupServer    func(*OAuth2Server)
		setupSession   func(*http.Request, *httptest.ResponseRecorder)
		expectedResult bool
	}{
		{
			name: "valid token returns true",
			setupServer: func(s *OAuth2Server) {
				s.accessTokens["valid_token"] = AccessToken{
					Token:     "valid_token",
					ExpiresAt: time.Now().Add(time.Hour),
				}
			},
			setupSession: func(req *http.Request, rec *httptest.ResponseRecorder) {
				_ = gothic.StoreInSession("access_token", "valid_token", req, rec)
				req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
			},
			expectedResult: true,
		},
		{
			name:        "no token in session returns false",
			setupServer: func(_ *OAuth2Server) {},
			setupSession: func(_ *http.Request, _ *httptest.ResponseRecorder) {
				// No session setup
			},
			expectedResult: false,
		},
		{
			name: "expired token returns false",
			setupServer: func(s *OAuth2Server) {
				s.accessTokens["expired_token"] = AccessToken{
					Token:     "expired_token",
					ExpiresAt: time.Now().Add(-time.Hour),
				}
			},
			setupSession: func(req *http.Request, rec *httptest.ResponseRecorder) {
				_ = gothic.StoreInSession("access_token", "expired_token", req, rec)
				req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
			},
			expectedResult: false,
		},
		{
			name:        "non-existent token returns false",
			setupServer: func(_ *OAuth2Server) {},
			setupSession: func(req *http.Request, rec *httptest.ResponseRecorder) {
				_ = gothic.StoreInSession("access_token", "nonexistent", req, rec)
				req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
			},
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup Gothic store
			gothic.Store = sessions.NewCookieStore([]byte("test-secret-32-bytes-minimum-len"))

			server := &OAuth2Server{
				Settings:     &conf.Settings{},
				accessTokens: make(map[string]AccessToken),
			}
			tt.setupServer(server)

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			rec := httptest.NewRecorder()
			tt.setupSession(req, rec)

			result := server.checkBasicAuthToken(req, testLogger{})
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

// TestCheckSocialAuthSessions tests the social auth sessions validation helper.
// It verifies that checkSocialAuthSessions iterates over configured OAuth providers.
func TestCheckSocialAuthSessions(t *testing.T) {
	tests := []struct {
		name           string
		providers      []conf.OAuthProviderConfig
		expectedResult bool
	}{
		{
			name:           "no providers configured returns false",
			providers:      nil,
			expectedResult: false,
		},
		{
			name: "google enabled but no session returns false",
			providers: []conf.OAuthProviderConfig{
				{Provider: ConfigGoogle, Enabled: true, UserID: "user@example.com"},
			},
			expectedResult: false,
		},
		{
			name: "github enabled but no session returns false",
			providers: []conf.OAuthProviderConfig{
				{Provider: ConfigGitHub, Enabled: true, UserID: "user@example.com"},
			},
			expectedResult: false,
		},
		{
			name: "multiple providers enabled but no session returns false",
			providers: []conf.OAuthProviderConfig{
				{Provider: ConfigGoogle, Enabled: true, UserID: "user@example.com"},
				{Provider: ConfigGitHub, Enabled: true, UserID: "user@example.com"},
				{Provider: ConfigMicrosoft, Enabled: true, UserID: "user@example.com"},
			},
			expectedResult: false,
		},
		{
			name: "provider disabled returns false",
			providers: []conf.OAuthProviderConfig{
				{Provider: ConfigGoogle, Enabled: false, UserID: "user@example.com"},
			},
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gothic.Store = sessions.NewCookieStore([]byte("test-secret-32-bytes-minimum-len"))

			server := &OAuth2Server{
				Settings: &conf.Settings{
					Security: conf.Security{
						OAuthProviders: tt.providers,
					},
				},
			}

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			result := server.checkSocialAuthSessions(req, testLogger{})
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

// TestCheckProviderAuth tests the generic provider auth validation function
func TestCheckProviderAuth(t *testing.T) {
	tests := []struct {
		name           string
		config         providerAuthConfig
		userId         string
		expectedResult bool
	}{
		{
			name: "disabled provider returns false",
			config: providerAuthConfig{
				providerName:   ProviderGoogle,
				enabled:        false,
				allowedUserIds: "user@example.com",
			},
			userId:         "user@example.com",
			expectedResult: false,
		},
		{
			name: "enabled but no session returns false",
			config: providerAuthConfig{
				providerName:   ProviderGoogle,
				enabled:        true,
				allowedUserIds: "user@example.com",
			},
			userId:         "",
			expectedResult: false,
		},
		{
			name: "works with github provider",
			config: providerAuthConfig{
				providerName:   ProviderGitHub,
				enabled:        false,
				allowedUserIds: "user@example.com",
			},
			userId:         "user@example.com",
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gothic.Store = sessions.NewCookieStore([]byte("test-secret-32-bytes-minimum-len"))

			server := &OAuth2Server{
				Settings: &conf.Settings{},
			}

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			result := server.checkProviderAuth(req, tt.userId, testLogger{}, tt.config)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}
