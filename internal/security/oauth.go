package security

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/github"
	gothGoogle "github.com/markbates/goth/providers/google"
	"golang.org/x/oauth2"

	"github.com/tphakala/birdnet-go/internal/conf"
)

type AuthCode struct {
	Code      string
	ExpiresAt time.Time
}

type AccessToken struct {
	Token     string
	ExpiresAt time.Time
}

type OAuth2Server struct {
	Settings     *conf.Settings
	authCodes    map[string]AuthCode
	accessTokens map[string]AccessToken
	mutex        sync.RWMutex
	debug        bool

	GithubConfig *oauth2.Config
	GoogleConfig *oauth2.Config

	// Token persistence
	tokensFile    string
	persistTokens bool
}

// For testing purposes
var testConfigPath string

func NewOAuth2Server() *OAuth2Server {
	settings := conf.GetSettings()
	debug := settings.Security.Debug

	server := &OAuth2Server{
		Settings:     settings,
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
		debug:        debug,
	}

	// Initialize Gothic with the provided configuration
	InitializeGoth(settings)

	// Set up token persistence
	configPaths, err := conf.GetDefaultConfigPaths()
	if err == nil {
		server.tokensFile = filepath.Join(configPaths[0], "tokens.json")
		server.persistTokens = true

		// Load any existing tokens
		server.loadTokens()
	}

	// Clean up expired tokens every hour
	server.StartAuthCleanup(time.Hour)

	return server
}

// InitializeGoth initializes social authentication providers.
func InitializeGoth(settings *conf.Settings) {
	// Get path for storing sessions
	var sessionPath string

	if testConfigPath != "" {
		// Use test path if set
		sessionPath = filepath.Join(testConfigPath, "sessions")
	} else {
		// Get path for storing sessions
		configPaths, err := conf.GetDefaultConfigPaths()
		if err != nil {
			log.Printf("Warning: Failed to get config paths for session store: %v", err)
			// Fallback to in-memory store if config paths can't be retrieved
			gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))
			goto initProviders
		}
		sessionPath = filepath.Join(configPaths[0], "sessions")
	}

	// Ensure directory exists
	if err := os.MkdirAll(sessionPath, 0o755); err != nil {
		log.Printf("Warning: Failed to create session directory: %v", err)
		gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))
	} else {
		// Create persistent session store
		gothic.Store = sessions.NewFilesystemStore(
			sessionPath,
			[]byte(settings.Security.SessionSecret),
			[]byte(settings.Security.SessionSecret+"encryption"),
		)

		// Configure session store options
		store := gothic.Store.(*sessions.FilesystemStore)
		store.Options = &sessions.Options{
			Path:     "/",
			MaxAge:   86400 * 7, // 7 days
			HttpOnly: true,
			Secure:   settings.Security.RedirectToHTTPS,
			SameSite: http.SameSiteLaxMode,
		}

		// Set reasonable values for session cookie storage
		store.MaxLength(1024 * 1024) // 1MB max size
	}

initProviders:
	// Initialize Gothic providers
	googleProvider :=
		gothGoogle.New(settings.Security.GoogleAuth.ClientID,
			settings.Security.GoogleAuth.ClientSecret,
			settings.Security.GoogleAuth.RedirectURI,
			"https://www.googleapis.com/auth/userinfo.email",
		)
	googleProvider.SetAccessType("offline")

	goth.UseProviders(
		googleProvider,
		github.New(settings.Security.GithubAuth.ClientID,
			settings.Security.GithubAuth.ClientSecret,
			settings.Security.GithubAuth.RedirectURI,
			"user:email",
		),
	)
}

// SetTestConfigPath sets a test path for testing session persistence
// It should be called before InitializeGoth and reset after the test
func SetTestConfigPath(path string) {
	testConfigPath = path
}

func (s *OAuth2Server) UpdateProviders() {
	InitializeGoth(s.Settings)
}

// IsUserAuthenticated checks if the user is authenticated
func (s *OAuth2Server) IsUserAuthenticated(c echo.Context) bool {
	if clientIP := net.ParseIP(c.RealIP()); IsInLocalSubnet(clientIP) {
		// For clients in the local subnet, consider them authenticated
		s.Debug("User authenticated from local subnet")
		return true
	}

	if token, err := gothic.GetFromSession("access_token", c.Request()); err == nil &&
		token != "" && s.ValidateAccessToken(token) {
		s.Debug("User was authenticated with valid access_token")
		return true
	}

	userId, _ := gothic.GetFromSession("userId", c.Request())
	if s.Settings.Security.GoogleAuth.Enabled {
		if googleUser, _ := gothic.GetFromSession("google", c.Request()); isValidUserId(s.Settings.Security.GoogleAuth.UserId, userId) && googleUser != "" {
			s.Debug("User was authenticated with valid Google user")
			return true
		}
	}
	if s.Settings.Security.GithubAuth.Enabled {
		if githubUser, _ := gothic.GetFromSession("github", c.Request()); isValidUserId(s.Settings.Security.GithubAuth.UserId, userId) && githubUser != "" {
			s.Debug("User was authenticated with valid GitHub user")
			return true
		}
	}
	return false
}

func isValidUserId(configuredIds, providedId string) bool {
	if configuredIds == "" || providedId == "" {
		return false
	}

	// Split configured IDs and trim spaces
	allowedIds := strings.Split(configuredIds, ",")
	for i := range allowedIds {
		if strings.TrimSpace(allowedIds[i]) == providedId {
			return true
		}
	}

	return false
}

// GenerateAuthCode generates a new authorization code with CSRF protection
func (s *OAuth2Server) GenerateAuthCode() (string, error) {
	code := make([]byte, 32)
	_, err := rand.Read(code)
	if err != nil {
		return "", err
	}
	authCode := base64.URLEncoding.EncodeToString(code)

	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.authCodes[authCode] = AuthCode{
		Code:      authCode,
		ExpiresAt: time.Now().Add(s.Settings.Security.BasicAuth.AuthCodeExp),
	}
	return authCode, nil
}

// ExchangeAuthCode exchanges an authorization code for an access token with CSRF validation
func (s *OAuth2Server) ExchangeAuthCode(code string) (string, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	authCode, exists := s.authCodes[code]
	if !exists || time.Now().After(authCode.ExpiresAt) {
		return "", errors.New("invalid or expired auth code")
	}
	delete(s.authCodes, code)

	token := make([]byte, 32)
	_, err := rand.Read(token)
	if err != nil {
		return "", err
	}
	accessToken := base64.URLEncoding.EncodeToString(token)
	s.accessTokens[accessToken] = AccessToken{
		Token:     accessToken,
		ExpiresAt: time.Now().Add(s.Settings.Security.BasicAuth.AccessTokenExp),
	}

	// Save tokens after creating a new one
	go s.saveTokens()

	return accessToken, nil
}

// ValidateAccessToken validates an access token
func (s *OAuth2Server) ValidateAccessToken(token string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	accessToken, exists := s.accessTokens[token]
	if !exists {
		return false
	}

	return time.Now().Before(accessToken.ExpiresAt)
}

// IsAuthenticationEnabled checks if authentication is enabled from given IP
func (s *OAuth2Server) IsAuthenticationEnabled(ip string) bool {
	// Check if authentication is enabled
	isAuthenticationEnabled := s.Settings.Security.BasicAuth.Enabled ||
		s.Settings.Security.GoogleAuth.Enabled ||
		s.Settings.Security.GithubAuth.Enabled

	if isAuthenticationEnabled && s.IsRequestFromAllowedSubnet(ip) {
		return false
	}

	return isAuthenticationEnabled
}

// isRequestFromAllowedSubnet checks if the request is coming from an allowed subnet
func (s *OAuth2Server) IsRequestFromAllowedSubnet(ip string) bool {
	// Check if subnet bypass is enabled
	allowedSubnet := s.Settings.Security.AllowSubnetBypass
	if !allowedSubnet.Enabled {
		return false
	}

	clientIP := net.ParseIP(ip)
	if clientIP == nil {
		s.Debug("Invalid IP address: %s", ip)
		return false
	}

	// The allowedSubnets string is expected to be a comma-separated list of CIDR ranges.
	subnets := strings.Split(allowedSubnet.Subnet, ",")

	for _, subnet := range subnets {
		_, ipNet, err := net.ParseCIDR(strings.TrimSpace(subnet))
		if err == nil && ipNet.Contains(clientIP) {
			s.Debug("Access allowed for IP %s", clientIP)
			return true
		}
	}

	s.Debug("IP %s is not in the allowed subnet", clientIP)
	return false
}

// loadTokens loads persisted access tokens from disk
func (s *OAuth2Server) loadTokens() {
	if !s.persistTokens {
		return
	}

	s.Debug("Loading tokens from %s", s.tokensFile)

	data, err := os.ReadFile(s.tokensFile)
	if err != nil {
		if !os.IsNotExist(err) {
			s.Debug("Error loading tokens: %v", err)
		}
		return
	}

	var tokens map[string]AccessToken
	if err := json.Unmarshal(data, &tokens); err != nil {
		s.Debug("Error unmarshaling tokens: %v", err)
		return
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Only load valid tokens
	now := time.Now()
	for token, accessToken := range tokens {
		if now.Before(accessToken.ExpiresAt) {
			s.accessTokens[token] = accessToken
		}
	}

	s.Debug("Loaded %d valid tokens", len(s.accessTokens))
}

// saveTokens persists access tokens to disk
func (s *OAuth2Server) saveTokens() {
	if !s.persistTokens {
		return
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Only save valid tokens
	validTokens := make(map[string]AccessToken)
	now := time.Now()
	for token, accessToken := range s.accessTokens {
		if now.Before(accessToken.ExpiresAt) {
			validTokens[token] = accessToken
		}
	}

	data, err := json.MarshalIndent(validTokens, "", "  ")
	if err != nil {
		s.Debug("Error marshaling tokens: %v", err)
		return
	}

	if err := os.WriteFile(s.tokensFile, data, 0o600); err != nil {
		s.Debug("Error saving tokens: %v", err)
	}
}

// StartAuthCleanup starts a goroutine to periodically clean up expired tokens
func (s *OAuth2Server) StartAuthCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			now := time.Now()
			s.mutex.Lock()

			// Clean up expired auth codes
			for code, ac := range s.authCodes {
				if now.After(ac.ExpiresAt) {
					delete(s.authCodes, code)
				}
			}

			// Clean up expired access tokens
			for token, at := range s.accessTokens {
				if now.After(at.ExpiresAt) {
					delete(s.accessTokens, token)
				}
			}

			s.mutex.Unlock()

			// Save valid tokens after cleanup
			s.saveTokens()
		}
	}()
}

// Debug logs debug messages if debug mode is enabled
func (s *OAuth2Server) Debug(format string, v ...interface{}) {
	if s.debug {
		prefix := "[security/oauth] "
		if len(v) == 0 {
			log.Print(prefix + format)
		} else {
			log.Printf(prefix+format, v...)
		}
	}
}
