package security

import (
	"net/http"
	"net/http/httptest"
	"time"

	"testing"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/conf"
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
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Store token using gothic's method
	gothic.StoreInSession("access_token", "valid_token", req, rec)

	// Add cookie to request
	req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))

	// Add token to OAuth2Server's valid tokens
	s.accessTokens["valid_token"] = AccessToken{
		Token:     "valid_token",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	isAuthenticated := s.IsUserAuthenticated(c)

	if !isAuthenticated {
		t.Errorf("Expected IsUserAuthenticated to return true, got false")
	}
}

// TestIsUserAuthenticatedInvalidAccessToken tests the IsUserAuthenticated function with an invalid access token
func TestIsUserAuthenticated(t *testing.T) {
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
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Store token using gothic's method
			gothic.StoreInSession("access_token", tt.token, req, rec)

			// Add cookie to request
			req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))

			// Add token to OAuth2Server's valid tokens
			s.accessTokens[tt.token] = AccessToken{
				Token:     tt.token,
				ExpiresAt: time.Now().Add(tt.expires),
			}

			got := s.IsUserAuthenticated(c)
			if got != tt.want {
				t.Errorf("IsUserAuthenticated() = %v, want %v", got, tt.want)
			}
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
				if err != nil {
					t.Fatalf("Failed to generate auth code: %v", err)
				}

				token, err := s.ExchangeAuthCode(code)
				if err != nil {
					t.Fatalf("Failed to exchange auth code: %v", err)
				}

				if !s.ValidateAccessToken(token) {
					t.Error("Token validation failed")
				}
			},
		},
		{
			name: "subnet bypass validation",
			test: func(t *testing.T, s *OAuth2Server) {
				s.Settings.Security.AllowSubnetBypass = conf.AllowSubnetBypass{
					Enabled: true,
					Subnet:  "192.168.1.0/24",
				}

				if !s.IsRequestFromAllowedSubnet("192.168.1.100") {
					t.Error("Expected IP to be allowed")
				}

				if s.IsRequestFromAllowedSubnet("10.0.0.1") {
					t.Error("Expected IP to be denied")
				}
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
