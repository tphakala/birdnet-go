package security

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"os"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Correct client_id and redirect_uri result in redirection with auth code
func TestHandleBasicAuthorizeSuccess(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?client_id=validClientID&redirect_uri=http://valid.redirect", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					ClientID:    "validClientID",
					RedirectURI: "http://valid.redirect",
				},
			},
		},
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	err := server.HandleBasicAuthorize(c)
	if err != nil {
		t.Fatalf("HandleBasicAuthorize returned an error: %v", err)
	}

	if rec.Code != http.StatusFound {
		t.Errorf("expected status %d, got %d", http.StatusFound, rec.Code)
	}

	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "http://valid.redirect?code=") {
		t.Errorf("unexpected redirect location: %s", location)
	}
}

// Invalid client_id returns a 400 Bad Request
func TestHandleBasicAuthorizeInvalidClientID(t *testing.T) {
	e := echo.New()
	// Use an explicitly invalid client ID
	req := httptest.NewRequest(http.MethodGet, "/?client_id=invalidClientID&redirect_uri=http://valid.redirect", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					ClientID:    "validClientID", // Set the expected valid client ID
					RedirectURI: "http://valid.redirect",
				},
			},
		},
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	server.HandleBasicAuthorize(c)
	resp := rec.Result()

	if resp == nil {
		t.Fatal("expected an error but got none: ")
	}

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	expectedBody := "Invalid client_id"
	if rec.Body.String() != expectedBody {
		t.Errorf("unexpected response body: got %s, want %s", rec.Body.String(), expectedBody)
	}
}

// Auth code generation succeeds without errors
func TestHandleBasicAuthorizeAuthCodeGeneration(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?client_id=validClientID&redirect_uri=http://valid.redirect", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					ClientID:    "validClientID",
					RedirectURI: "http://valid.redirect",
				},
			},
		},
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	err := server.HandleBasicAuthorize(c)
	if err != nil {
		t.Fatalf("HandleBasicAuthorize returned an error: %v", err)
	}

	if rec.Code != http.StatusFound {
		t.Errorf("expected status %d, got %d", http.StatusFound, rec.Code)
	}

	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "http://valid.redirect?code=") {
		t.Errorf("unexpected redirect location: %s", location)
	}
}

// Valid client_id and redirect_uri parameters are correctly parsed from query
func TestHandleBasicAuthorizeValidParameters(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?client_id=validClientID&redirect_uri=http://valid.redirect", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				BasicAuth: conf.BasicAuth{
					ClientID:    "validClientID",
					RedirectURI: "http://valid.redirect",
				},
			},
		},
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	err := server.HandleBasicAuthorize(c)
	if err != nil {
		t.Fatalf("HandleBasicAuthorize returned an error: %v", err)
	}

	if rec.Code != http.StatusFound {
		t.Errorf("expected status %d, got %d", http.StatusFound, rec.Code)
	}

	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "http://valid.redirect?code=") {
		t.Errorf("unexpected redirect location: %s", location)
	}
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
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	// Pre-populate a valid auth code
	s.authCodes["validCode"] = AuthCode{
		Code:      "validCode",
		ExpiresAt: time.Now().Add(time.Minute),
	}

	err := s.HandleBasicAuthToken(c)
	if err != nil {
		t.Fatalf("HandleBasicAuthToken failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response["access_token"] == "" {
		t.Error("expected access token in response")
	}
}

// Handle missing grant_type, code, or redirect_uri fields gracefully
func TestHandleBasicAuthTokenMissingFields(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
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
	if err != nil {
		t.Fatalf("HandleBasicAuthToken failed: %v", err)
	}

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response["error"] != "Missing required fields" {
		t.Errorf("expected error 'Missing required fields', got '%s'", response["error"])
	}
}
