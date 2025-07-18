package security

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"os"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// parseURLOrFail parses a URL string and fails the test if parsing fails
func parseURLOrFail(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("Failed to parse URL '%s': %v", rawURL, err)
	}
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
	// Use an explicitly invalid client ID in request, but valid one in server config
	server, c, rec := setupOAuth2ServerTest(t, "invalidClientID", "http://valid.redirect", "validClientID", "http://valid.redirect")

	_ = server.HandleBasicAuthorize(c) // Error is checked via HTTP response code in test
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
	server, c, rec := setupOAuth2ServerTestWithValidCredentials(t, "validClientID", "http://valid.redirect")

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
	server, c, rec := setupOAuth2ServerTestWithValidCredentials(t, "validClientID", "http://valid.redirect")

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
	if err != nil {
		t.Fatalf("HandleBasicAuthToken failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var response map[string]interface{}
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
