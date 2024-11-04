package security

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
)

func (s *OAuth2Server) HandleBasicAuthorize(c echo.Context) error {
	clientID := c.QueryParam("client_id")
	redirectURI := c.QueryParam("redirect_uri")

	if clientID != s.Settings.Security.BasicAuth.ClientID {
		return c.String(http.StatusBadRequest, "Invalid client_id")
	}

	if redirectURI != s.Settings.Security.BasicAuth.RedirectURI {
		return c.String(http.StatusBadRequest, "Invalid redirect_uri")
	}

	// Generate an auth code
	authCode, err := s.GenerateAuthCode()
	if err != nil {
		return c.String(http.StatusInternalServerError, "Error generating auth code")
	}

	return c.Redirect(http.StatusFound, redirectURI+"?code="+authCode)

}

func (s *OAuth2Server) HandleBasicAuthToken(c echo.Context) error {
	grantType := c.FormValue("grant_type")
	code := c.FormValue("code")
	redirectURI := c.FormValue("redirect_uri")

	// Verify client credentials from Authorization header
	clientID, clientSecret, ok := c.Request().BasicAuth()
	if !ok || clientID != s.Settings.Security.BasicAuth.ClientID || clientSecret != s.Settings.Security.BasicAuth.ClientSecret {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Invalid client id or secret"})
	}

	// Check for required fields
	if grantType == "" || code == "" || redirectURI == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Missing required fields"})
	}

	// Verify grant type
	if grantType != "authorization_code" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unsupported grant type"})
	}

	// Verify redirect URI
	if !strings.Contains(redirectURI, s.Settings.Security.Host) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid host for redirect URI"})
	}

	// Exchange the authorization code for an access token
	accessToken, err := s.ExchangeAuthCode(code)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid authorization code"})
	}

	// Store the access token in Gothic session
	if err := gothic.StoreInSession("access_token", accessToken, c.Request(), c.Response()); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to store access token in session")
	}

	// Return the access token in the response body
	return c.JSON(http.StatusOK, map[string]string{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   s.Settings.Security.BasicAuth.AccessTokenExp.String(),
	})
}

func (s *OAuth2Server) HandleBasicAuthCallback(c echo.Context) error {
	code := c.QueryParam("code")
	redirect := c.QueryParam("redirect")
	if code == "" {
		return c.String(http.StatusBadRequest, "Missing authorization code")
	}

	// Instead of exchanging the code here, we'll pass it to the client
	return c.Render(http.StatusOK, "callback", map[string]interface{}{
		"Code":        code,
		"RedirectURL": redirect,
		"ClientID":    s.Settings.Security.BasicAuth.ClientID,
		"Secret":      s.Settings.Security.BasicAuth.ClientSecret,
	})
}
