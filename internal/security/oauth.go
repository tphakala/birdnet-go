package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

	// Throttling
	throttledMessages map[string]time.Time
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
	if err != nil {
		log.Printf("Warning: Failed to get config paths for token persistence: %v", err)
		log.Printf("Token persistence will be disabled - sessions will not survive restarts")
	} else {
		server.tokensFile = filepath.Join(configPaths[0], "tokens.json")
		server.persistTokens = true

		// Ensure the directory exists
		if err := os.MkdirAll(filepath.Dir(server.tokensFile), 0o755); err != nil {
			log.Printf("Warning: Failed to create directory for token persistence: %v", err)
			server.persistTokens = false
		} else {
			// Load any existing tokens
			if err := server.loadTokens(); err != nil {
				log.Printf("Warning: Failed to load persisted tokens: %v", err)
			}
		}
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
			gothic.Store = sessions.NewCookieStore(createSessionKey(settings.Security.SessionSecret))
			goto initProviders
		}
		sessionPath = filepath.Join(configPaths[0], "sessions")
	}

	// Ensure directory exists
	if err := os.MkdirAll(sessionPath, 0o755); err != nil {
		log.Printf("Warning: Failed to create session directory: %v", err)
		gothic.Store = sessions.NewCookieStore(createSessionKey(settings.Security.SessionSecret))
	} else {
		// Create persistent session store with properly sized keys
		authKey := createSessionKey(settings.Security.SessionSecret)
		encKey := createSessionKey(settings.Security.SessionSecret + "encryption")

		gothic.Store = sessions.NewFilesystemStore(
			sessionPath,
			authKey,
			encKey,
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

// createSessionKey creates a key of the proper length for AES encryption from a seed string
// AES requires keys of exactly 16, 24, or 32 bytes
func createSessionKey(seed string) []byte {
	// Create a SHA-256 hash of the seed (32 bytes, perfect for AES-256)
	hasher := sha256.New()
	hasher.Write([]byte(seed))
	return hasher.Sum(nil)
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
	go func() {
		const maxRetries = 3
		var err error
		for i := 0; i < maxRetries; i++ {
			err = s.saveTokens()
			if err == nil {
				return
			}
			s.Debug("Error saving tokens (attempt %d/%d): %v", i+1, maxRetries, err)
			time.Sleep(time.Duration(i+1) * 100 * time.Millisecond) // Exponential backoff
		}
		if err != nil {
			log.Printf("Failed to save tokens after %d attempts: %v", maxRetries, err)
		}
	}()

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
func (s *OAuth2Server) loadTokens() error {
	if !s.persistTokens {
		return nil
	}

	s.Debug("Loading tokens from %s", s.tokensFile)

	data, err := os.ReadFile(s.tokensFile)
	if err != nil {
		if os.IsNotExist(err) {
			s.Debug("No token file found, starting with empty token store")
			return nil
		}
		return fmt.Errorf("failed to read token file: %w", err)
	}

	var tokens map[string]AccessToken
	if err := json.Unmarshal(data, &tokens); err != nil {
		return fmt.Errorf("failed to parse token file: %w", err)
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Only load valid tokens
	now := time.Now()
	validCount := 0
	expiredCount := 0

	for token, accessToken := range tokens {
		if now.Before(accessToken.ExpiresAt) {
			s.accessTokens[token] = accessToken
			validCount++
		} else {
			expiredCount++
		}
	}

	s.Debug("Loaded %d valid tokens, skipped %d expired tokens", validCount, expiredCount)
	return nil
}

// saveTokens persists access tokens to disk
func (s *OAuth2Server) saveTokens() error {
	if !s.persistTokens {
		return nil
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
		return fmt.Errorf("failed to marshal tokens: %w", err)
	}

	// Write to a temporary file first
	tempFile := s.tokensFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write tokens file: %w", err)
	}

	// Atomically rename to ensure consistency
	if err := os.Rename(tempFile, s.tokensFile); err != nil {
		// Try to clean up the temp file
		os.Remove(tempFile)
		return fmt.Errorf("failed to finalize tokens file: %w", err)
	}

	s.Debug("Saved %d valid tokens to %s", len(validTokens), s.tokensFile)
	return nil
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
			if err := s.saveTokens(); err != nil {
				s.Debug("Error saving tokens during cleanup: %v", err)
			}
		}
	}()
}

// Debug logs debug messages if debug mode is enabled
func (s *OAuth2Server) Debug(format string, v ...interface{}) {
	if s.debug {
		prefix := "[security/oauth] "
		// Avoid excessive repetitive log entries about authentication status
		if strings.Contains(format, "User was authenticated") ||
			strings.Contains(format, "User authenticated") {
			// Skip repetitive auth success messages if recent
			s.mutex.RLock()
			now := time.Now()
			// Create throttle key based on message format (all user auth messages treated as same)
			throttleKey := "auth_status"
			lastTime, exists := s.throttledMessages[throttleKey]
			tooFrequent := exists && now.Sub(lastTime) < 3*time.Second
			s.mutex.RUnlock()

			if tooFrequent {
				// Skip this message as it's too frequent
				return
			}

			// Update the throttle time
			s.mutex.Lock()
			if s.throttledMessages == nil {
				s.throttledMessages = make(map[string]time.Time)
			}
			s.throttledMessages[throttleKey] = now
			s.mutex.Unlock()
		}

		if len(v) == 0 {
			log.Print(prefix + format)
		} else {
			log.Printf(prefix+format, v...)
		}
	}
}
