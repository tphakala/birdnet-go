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
	settings := &conf.Settings{
		Security: conf.Security{
			SessionSecret: "test-secret",
		},
	}

	s := NewOAuth2Server(settings)

	// Initialize gothic exactly as in production
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))
	gothic.SetState = func(req *http.Request) string {
		return ""
	}

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

			s := NewOAuth2Server(settings)

			// Initialize gothic exactly as in production
			gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))
			gothic.SetState = func(req *http.Request) string {
				return ""
			}

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
