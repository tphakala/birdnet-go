package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
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
	intErrors "github.com/tphakala/birdnet-go/internal/errors"
)

type AuthCode struct {
	Code      string
	ExpiresAt time.Time
}

type AccessToken struct {
	Token     string
	ExpiresAt time.Time
}

// providerAuthConfig holds the configuration for validating a provider's auth session.
// This allows checkProviderAuth to work generically with any OAuth provider.
type providerAuthConfig struct {
	providerName   string // Session key (e.g., ProviderGoogle, ProviderGitHub)
	enabled        bool   // Whether this provider is enabled
	allowedUserIds string // Comma-separated list of allowed user IDs
}

type OAuth2Server struct {
	Settings     *conf.Settings
	authCodes    map[string]AuthCode
	accessTokens map[string]AccessToken
	mutex        sync.RWMutex

	GithubConfig *oauth2.Config
	GoogleConfig *oauth2.Config

	// Token persistence
	tokensFile    string
	persistTokens bool

	// Expected Redirect URI for Basic Auth (pre-parsed)
	ExpectedBasicRedirectURI *url.URL

	// Throttling
	throttledMessages map[string]time.Time
}

// For testing purposes
var testConfigPath string

// NewOAuth2ServerForTesting creates an OAuth2Server with the provided settings for testing.
// This bypasses conf.GetSettings() and allows custom settings to be injected.
func NewOAuth2ServerForTesting(settings *conf.Settings) *OAuth2Server {
	return &OAuth2Server{
		Settings:          settings,
		authCodes:         make(map[string]AuthCode),
		accessTokens:      make(map[string]AccessToken),
		throttledMessages: make(map[string]time.Time),
	}
}

// Pre-defined errors for token validation
var (
	ErrTokenNotFound = errors.New("token not found")
	ErrTokenExpired  = errors.New("token expired")
)

func NewOAuth2Server() *OAuth2Server {
	// Use the security logger from the start
	logger().Info("Initializing OAuth2 server")
	settings := conf.GetSettings()

	server := &OAuth2Server{
		Settings:     settings,
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	// Validate and potentially fix session secret
	validateSessionSecret(settings)

	// Pre-parse the Basic Auth Redirect URI
	server.ExpectedBasicRedirectURI = parseBasicAuthRedirectURI(settings)

	// Initialize Gothic with the provided configuration
	InitializeGoth(settings)

	// Set up token persistence
	server.setupTokenPersistence()

	// Clean up expired tokens every hour
	// TODO: Pass application shutdown context for graceful cleanup termination
	server.StartAuthCleanup(context.Background(), time.Hour)

	logger().Info("OAuth2 server initialization complete")
	return server
}

// validateSessionSecret checks the session secret strength and generates a temporary one if needed
func validateSessionSecret(settings *conf.Settings) {
	if settings.Security.SessionSecret == "" {
		handleEmptySessionSecret(settings)
		return
	}
	if len(settings.Security.SessionSecret) < MinSessionSecretLength {
		handleWeakSessionSecret(settings)
	}
}

// handleEmptySessionSecret generates a temporary secret when none is configured
func handleEmptySessionSecret(settings *conf.Settings) {
	logger().Error("CRITICAL SECURITY WARNING: SessionSecret is empty. A temporary secret will be generated, but this should be fixed in configuration.",
		"debug_mode", settings.WebServer.Debug,
		"recommendation", "BirdNET-Go will auto-generate a SessionSecret on next restart")

	tempSecret := conf.GenerateRandomSecret()
	settings.Security.SessionSecret = tempSecret
	logger().Info("Generated temporary SessionSecret for this session")

	_ = intErrors.New(
		errors.New("session secret is empty")).
		Component("security").
		Category(intErrors.CategoryConfiguration).
		Context("debug_mode", settings.WebServer.Debug).
		Context("action", "auto_generated_temporary_secret").
		Build()
}

// handleWeakSessionSecret logs a warning for weak session secrets
func handleWeakSessionSecret(settings *conf.Settings) {
	logger().Warn("Security Recommendation: SessionSecret is potentially weak",
		"current_length", len(settings.Security.SessionSecret),
		"recommended_length", MinSessionSecretLength,
		"debug_mode", settings.WebServer.Debug,
		"recommendation", "Consider regenerating with a stronger secret")

	_ = intErrors.New(
		fmt.Errorf("session secret is potentially weak: %d characters", len(settings.Security.SessionSecret))).
		Component("security").
		Category(intErrors.CategoryConfiguration).
		Context("current_length", len(settings.Security.SessionSecret)).
		Context("recommended_length", MinSessionSecretLength).
		Context("debug_mode", settings.WebServer.Debug).
		Build()
}

// parseBasicAuthRedirectURI parses and validates the Basic Auth redirect URI
func parseBasicAuthRedirectURI(settings *conf.Settings) *url.URL {
	if settings.Security.BasicAuth.RedirectURI == "" {
		logger().Warn("Basic Auth Redirect URI is not configured. Basic authentication might not function correctly.")
		return nil
	}

	parsedURI, err := url.Parse(settings.Security.BasicAuth.RedirectURI)
	if err != nil {
		logger().Error("CRITICAL CONFIGURATION ERROR: Failed to parse BasicAuth.RedirectURI. Basic authentication will likely fail.",
			"uri", settings.Security.BasicAuth.RedirectURI, "error", err)
		return nil
	}

	if parsedURI.RawQuery != "" || parsedURI.Fragment != "" {
		logger().Error("CRITICAL CONFIGURATION ERROR: BasicAuth.RedirectURI must not contain query parameters or fragments.",
			"uri", settings.Security.BasicAuth.RedirectURI)
		return nil
	}

	logger().Info("Pre-parsed and validated Basic Auth Redirect URI", "uri", parsedURI.String())
	return parsedURI
}

// setupTokenPersistence configures token persistence for the OAuth2 server
func (s *OAuth2Server) setupTokenPersistence() {
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		logger().Warn("Failed to get config paths for token persistence, persistence disabled", "error", err)
		return
	}

	s.tokensFile = filepath.Join(configPaths[0], "tokens.json")
	s.persistTokens = true
	logger().Info("Token persistence configured", "file", s.tokensFile)

	if err := os.MkdirAll(filepath.Dir(s.tokensFile), DirPermissions); err != nil {
		logger().Error("Failed to create directory for token persistence, persistence disabled",
			"path", filepath.Dir(s.tokensFile), "error", err)
		s.persistTokens = false
		return
	}

	if err := s.loadTokens(context.Background()); err != nil {
		logger().Warn("Failed to load persisted tokens", "file", s.tokensFile, "error", err)
	}
}

// InitializeGoth initializes social authentication providers.
func InitializeGoth(settings *conf.Settings) {
	logger().Info("Initializing Goth providers")

	// Setup session store (filesystem or fallback to cookie-based)
	setupSessionStore(settings)

	// Initialize OAuth providers
	initializeProviders(settings)
}

// setupSessionStore configures the Gothic session store.
// It attempts to use a filesystem store, falling back to an in-memory cookie store on failure.
func setupSessionStore(settings *conf.Settings) {
	sessionPath, ok := getSessionPath()
	if !ok {
		// Fallback to in-memory store if config paths can't be retrieved
		gothic.Store = sessions.NewCookieStore(createSessionKey(settings.Security.SessionSecret))
		return
	}

	// Ensure directory exists
	if err := os.MkdirAll(sessionPath, DirPermissions); err != nil {
		logger().Error("Failed to create session directory, falling back to in-memory cookie store", "path", sessionPath, "error", err)
		gothic.Store = sessions.NewCookieStore(createSessionKey(settings.Security.SessionSecret))
		return
	}

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
	maxAge := DefaultSessionMaxAgeSeconds
	if settings.Security.SessionDuration > 0 {
		maxAge = int(settings.Security.SessionDuration.Seconds())
	}
	secureCookie := settings.Security.RedirectToHTTPS
	store.Options = buildSessionOptions(secureCookie, maxAge)
	logger().Info("Filesystem session store configured", "path", sessionPath, "max_age_seconds", maxAge, "secure", secureCookie)

	// Set reasonable values for session cookie storage
	store.MaxLength(MaxSessionSizeBytes)
	logger().Debug("Set session store max length", "max_bytes", MaxSessionSizeBytes)
}

// getSessionPath returns the path for session storage and whether it was successfully determined.
func getSessionPath() (string, bool) {
	if testConfigPath != "" {
		logger().Info("Using test config path for session storage", "path", testConfigPath)
		return filepath.Join(testConfigPath, "sessions"), true
	}

	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		logger().Warn("Failed to get config paths for session store, using in-memory cookie store", "error", err)
		return "", false
	}

	sessionPath := filepath.Join(configPaths[0], "sessions")
	logger().Info("Using filesystem session store", "path", sessionPath)
	return sessionPath, true
}

// initializeProviders sets up the OAuth providers (Google, GitHub).
func initializeProviders(settings *conf.Settings) {
	logger().Info("Configuring Goth providers")
	providers := make([]goth.Provider, 0, InitialProviderCapacity)

	if settings.Security.GoogleAuth.Enabled && settings.Security.GoogleAuth.ClientID != "" && settings.Security.GoogleAuth.ClientSecret != "" {
		logger().Info("Enabling Google Auth provider")
		googleProvider := gothGoogle.New(
			settings.Security.GoogleAuth.ClientID,
			settings.Security.GoogleAuth.ClientSecret,
			settings.Security.GoogleAuth.RedirectURI,
			"https://www.googleapis.com/auth/userinfo.email", // Scope for email
		)
		googleProvider.SetAccessType("offline")
		providers = append(providers, googleProvider)
	} else {
		logger().Info("Google Auth provider disabled or not configured")
	}

	if settings.Security.GithubAuth.Enabled && settings.Security.GithubAuth.ClientID != "" && settings.Security.GithubAuth.ClientSecret != "" {
		logger().Info("Enabling GitHub Auth provider")
		providers = append(providers, github.New(
			settings.Security.GithubAuth.ClientID,
			settings.Security.GithubAuth.ClientSecret,
			settings.Security.GithubAuth.RedirectURI,
			"user:email", // Scope for email
		))
	} else {
		logger().Info("GitHub Auth provider disabled or not configured")
	}

	if len(providers) > 0 {
		goth.UseProviders(providers...)
		logger().Info("Goth providers initialized", "count", len(providers))
	} else {
		logger().Warn("No Goth providers enabled or configured")
	}
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
	logger().Debug("Setting test config path", "path", path)
	testConfigPath = path
}

func (s *OAuth2Server) UpdateProviders() {
	logger().Info("Updating Goth providers based on potentially changed settings")
	InitializeGoth(s.Settings)
}

// IsUserAuthenticated checks if the user is authenticated
func (s *OAuth2Server) IsUserAuthenticated(c echo.Context) bool {
	clientIP := net.ParseIP(c.RealIP())
	log := logger().With("client_ip", c.RealIP())
	log.Debug("Checking user authentication status")

	if IsInLocalSubnet(clientIP) {
		log.Info("User authenticated: request from local subnet")
		return true
	}

	if s.checkBasicAuthToken(c.Request(), log) {
		return true
	}

	if s.checkSocialAuthSessions(c.Request(), log) {
		return true
	}

	log.Info("User not authenticated")
	return false
}

// checkBasicAuthToken validates the basic auth access token from the session
func (s *OAuth2Server) checkBasicAuthToken(r *http.Request, log SecurityLogger) bool {
	token, err := gothic.GetFromSession("access_token", r)
	if err != nil || token == "" {
		return false
	}

	log.Debug("Found access_token in session, validating...")
	if s.ValidateAccessToken(token) == nil {
		log.Info("User authenticated: valid access_token found in session")
		return true
	}
	log.Warn("Invalid or expired access_token found in session")
	return false
}

// checkSocialAuthSessions checks for valid Google or GitHub authentication sessions
func (s *OAuth2Server) checkSocialAuthSessions(r *http.Request, log SecurityLogger) bool {
	userId, err := gothic.GetFromSession("userId", r)
	if err != nil {
		log.Debug("No userId found in session")
	} else {
		log.Debug("Found userId in session, checking provider sessions")
	}

	if s.checkGoogleAuth(r, userId, log) {
		return true
	}

	return s.checkGithubAuth(r, userId, log)
}

// checkGoogleAuth validates Google OAuth session
func (s *OAuth2Server) checkGoogleAuth(r *http.Request, userId string, log SecurityLogger) bool {
	return s.checkProviderAuth(r, userId, log, providerAuthConfig{
		providerName:   ProviderGoogle,
		enabled:        s.Settings.Security.GoogleAuth.Enabled,
		allowedUserIds: s.Settings.Security.GoogleAuth.UserId,
	})
}

// checkGithubAuth validates GitHub OAuth session
func (s *OAuth2Server) checkGithubAuth(r *http.Request, userId string, log SecurityLogger) bool {
	return s.checkProviderAuth(r, userId, log, providerAuthConfig{
		providerName:   ProviderGitHub,
		enabled:        s.Settings.Security.GithubAuth.Enabled,
		allowedUserIds: s.Settings.Security.GithubAuth.UserId,
	})
}

// checkProviderAuth validates an OAuth provider session generically.
// This is the shared implementation used by checkGoogleAuth and checkGithubAuth.
func (s *OAuth2Server) checkProviderAuth(r *http.Request, userId string, log SecurityLogger, cfg providerAuthConfig) bool {
	if !cfg.enabled {
		return false
	}

	sessionUser, err := gothic.GetFromSession(cfg.providerName, r)
	if err != nil || sessionUser == "" {
		return false
	}

	log.Debug("Found provider session key", "provider", cfg.providerName)
	if isValidUserId(cfg.allowedUserIds, userId) {
		log.Info("User authenticated: valid session found for allowed user ID", "provider", cfg.providerName)
		return true
	}
	log.Warn("Provider session found, but userId does not match allowed IDs", "provider", cfg.providerName, "allowed_ids", cfg.allowedUserIds)
	return false
}

func isValidUserId(configuredIds, providedId string) bool {
	if configuredIds == "" || providedId == "" {
		return false
	}

	// Trim whitespace from the ID we are checking
	trimmedProvidedId := strings.TrimSpace(providedId)
	if trimmedProvidedId == "" {
		return false // Don't match empty string after trimming
	}

	// Split configured IDs and trim spaces from each allowed ID once upfront
	allowedIdsRaw := strings.Split(configuredIds, ",")
	allowedIdsTrimmed := make([]string, 0, len(allowedIdsRaw))
	for _, allowedId := range allowedIdsRaw {
		trimmed := strings.TrimSpace(allowedId)
		if trimmed != "" { // Avoid adding empty strings to the comparison list
			allowedIdsTrimmed = append(allowedIdsTrimmed, trimmed)
		}
	}

	// Compare the provided ID against the pre-trimmed list using case-insensitive comparison
	for _, allowedId := range allowedIdsTrimmed {
		if strings.EqualFold(allowedId, trimmedProvidedId) {
			return true
		}
	}

	return false
}

// GenerateAuthCode generates a new authorization code
func (s *OAuth2Server) GenerateAuthCode() (string, error) {
	logger().Debug("Generating new authorization code")
	code := make([]byte, AuthCodeByteLength)
	_, err := rand.Read(code)
	if err != nil {
		logger().Error("Failed to read random bytes for auth code", "error", err)
		return "", err
	}
	authCode := base64.URLEncoding.EncodeToString(code)

	s.mutex.Lock()
	defer s.mutex.Unlock()

	expiresAt := time.Now().Add(s.Settings.Security.BasicAuth.AuthCodeExp)
	s.authCodes[authCode] = AuthCode{
		Code:      authCode,
		ExpiresAt: expiresAt,
	}
	// Do not log the authCode itself
	logger().Info("Generated and stored new authorization code", "expires_at", expiresAt)
	go s.persistTokensIfEnabled() // Persist changes
	return authCode, nil
}

// ExchangeAuthCode exchanges an authorization code for an access token
// It now accepts a context, although it's not directly used for internal operations yet.
func (s *OAuth2Server) ExchangeAuthCode(ctx context.Context, code string) (string, error) {
	// Do not log the code
	logger := logger().With("operation", "ExchangeAuthCode")

	// Fast-fail if the client already gave up
	if err := ctx.Err(); err != nil {
		logger.Warn("Context cancelled before acquiring lock", "error", err)
		return "", err
	}

	logger.Debug("Attempting to exchange authorization code")
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Re-check context after acquiring the lock
	if err := ctx.Err(); err != nil {
		logger.Warn("Context cancelled after acquiring lock", "error", err)
		return "", err // Mutex is released by defer
	}

	authCode, ok := s.authCodes[code]
	if !ok {
		logger.Warn("Authorization code not found")
		return "", errors.New("authorization code not found")
	}

	if time.Now().After(authCode.ExpiresAt) {
		logger.Warn("Authorization code expired", "expired_at", authCode.ExpiresAt)
		delete(s.authCodes, code)     // Clean up expired code
		go s.persistTokensIfEnabled() // Persist removal
		return "", errors.New("authorization code expired")
	}

	// Generate access token
	tokenBytes := make([]byte, AccessTokenByteLength)
	_, err := rand.Read(tokenBytes)
	if err != nil {
		logger.Error("Failed to read random bytes for access token", "error", err)
		return "", err
	}
	accessToken := base64.URLEncoding.EncodeToString(tokenBytes)
	expiresAt := time.Now().Add(s.Settings.Security.BasicAuth.AccessTokenExp)

	s.accessTokens[accessToken] = AccessToken{
		Token:     accessToken,
		ExpiresAt: expiresAt,
	}

	// Invalidate the auth code after use
	delete(s.authCodes, code)

	// Do not log the accessToken
	logger.Info("Exchanged authorization code for new access token", "expires_at", expiresAt)
	go s.persistTokensIfEnabled() // Persist changes
	return accessToken, nil
}

// ValidateAccessToken checks if an access token is valid and returns an error if not.
func (s *OAuth2Server) ValidateAccessToken(token string) error {
	// Do not log the token
	logger().Debug("Validating access token")
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	accessToken, ok := s.accessTokens[token]
	if !ok {
		logger().Debug("Access token not found")
		return ErrTokenNotFound // Return specific error
	}

	if time.Now().After(accessToken.ExpiresAt) {
		logger().Debug("Access token expired", "expired_at", accessToken.ExpiresAt)
		// No need to delete here, cleanup routine handles it
		return ErrTokenExpired // Return specific error
	}

	logger().Debug("Access token is valid")
	return nil // Return nil on success
}

// IsAuthenticationEnabled checks if any authentication method is enabled
func (s *OAuth2Server) IsAuthenticationEnabled(ip string) bool {
	logger := logger().With("ip", ip)
	logger.Debug("Checking if authentication is enabled for IP")
	if s.IsRequestFromAllowedSubnet(ip) {
		logger.Info("Authentication bypassed: request from allowed subnet")
		return false // Authentication not required for allowed subnets
	}
	if s.Settings.Security.BasicAuth.Enabled || s.Settings.Security.GoogleAuth.Enabled || s.Settings.Security.GithubAuth.Enabled {
		logger.Info("Authentication required: at least one provider enabled and IP not in allowed subnet",
			"basic_enabled", s.Settings.Security.BasicAuth.Enabled,
			"google_enabled", s.Settings.Security.GoogleAuth.Enabled,
			"github_enabled", s.Settings.Security.GithubAuth.Enabled,
		)
		return true
	}
	logger.Info("Authentication not enabled: no providers configured and IP not in allowed subnet")
	return false
}

// IsRequestFromAllowedSubnet checks if the request IP is within allowed subnets
func (s *OAuth2Server) IsRequestFromAllowedSubnet(ipStr string) bool {
	logger := logger().With("ip", ipStr)
	logger.Debug("Checking if IP is in allowed subnet")

	// Check if subnet bypass is enabled first
	if !s.Settings.Security.AllowSubnetBypass.Enabled {
		logger.Debug("Allowed subnet check: subnet bypass is disabled in settings")
		return false
	}

	if ipStr == "" {
		logger.Debug("Allowed subnet check: empty IP string")
		return false
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		logger.Warn("Failed to parse IP address for allowed subnet check", "ip_string", ipStr)
		return false
	}

	// Check loopback explicitly - often implicitly allowed
	if ip.IsLoopback() {
		logger.Debug("Allowed subnet check: IP is loopback")
		return true
	}

	// The allowedSubnets string is expected to be a comma-separated list of CIDR ranges.
	allowedSubnetsStr := s.Settings.Security.AllowSubnetBypass.Subnet
	if allowedSubnetsStr == "" {
		logger.Debug("Allowed subnet check: no allowed subnets configured (subnet string is empty)")
		return false
	}

	allowedSubnets := strings.Split(allowedSubnetsStr, ",")
	if len(allowedSubnets) == 0 {
		// This case should ideally not happen if the string is not empty, but check defensively
		logger.Debug("Allowed subnet check: no allowed subnets configured (split result is empty)")
		return false
	}

	for _, cidr := range allowedSubnets {
		trimmedCIDR := strings.TrimSpace(cidr)
		if trimmedCIDR == "" {
			continue // Skip empty entries
		}
		_, subnet, err := net.ParseCIDR(trimmedCIDR)
		if err != nil {
			logger.Warn("Failed to parse allowed subnet CIDR", "cidr", trimmedCIDR, "error", err)
			continue
		}
		logger.Debug("Checking against allowed subnet", "cidr", trimmedCIDR)
		if subnet.Contains(ip) {
			logger.Debug("IP is within allowed subnet", "cidr", trimmedCIDR)
			return true
		}
	}

	logger.Debug("IP is not in any allowed subnet")
	return false
}

// persistTokensIfEnabled saves tokens if persistence is enabled
func (s *OAuth2Server) persistTokensIfEnabled() {
	if s.persistTokens {
		// Use a short background context for saving, as it runs in a goroutine
		ctx, cancel := context.WithTimeout(context.Background(), TokenSaveTimeout)
		defer cancel()
		if err := s.saveTokens(ctx); err != nil {
			// Throttle logging for repeated save errors
			s.logThrottledError("save_tokens_error", "Failed to save tokens", err, ThrottleLogInterval)
		}
	}
}

// loadTokens loads tokens from the persistence file
func (s *OAuth2Server) loadTokens(ctx context.Context) error {
	if !s.persistTokens {
		logger().Debug("Token persistence is disabled, skipping load")
		return nil
	}

	logger().Info("Attempting to load tokens from file", "file", s.tokensFile)

	// Check context before potentially long file read
	select {
	case <-ctx.Done():
		logger().Warn("Context cancelled before loading tokens", "error", ctx.Err())
		return ctx.Err()
	default:
	}

	// Read file outside the lock
	data, err := os.ReadFile(s.tokensFile)
	if err != nil {
		if os.IsNotExist(err) {
			logger().Info("Token file does not exist, skipping load", "file", s.tokensFile)
			return nil // Not an error if the file doesn't exist yet
		}
		logger().Error("Failed to read token file", "file", s.tokensFile, "error", err)
		return fmt.Errorf("failed to read token file %s: %w", s.tokensFile, err)
	}

	if len(data) == 0 {
		logger().Info("Token file is empty, skipping load", "file", s.tokensFile)
		return nil // Empty file is fine
	}

	// Unmarshal data outside the lock
	var storedData struct {
		AuthCodes    map[string]AuthCode    `json:"auth_codes"`
		AccessTokens map[string]AccessToken `json:"access_tokens"`
	}

	if err := json.Unmarshal(data, &storedData); err != nil {
		logger().Error("Failed to unmarshal token data from file", "file", s.tokensFile, "error", err)
		return fmt.Errorf("failed to unmarshal token data from %s: %w", s.tokensFile, err)
	}

	// Acquire lock only to update internal maps
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Restore non-expired tokens
	loadedAuthCodes := 0
	now := time.Now()
	for code, authCode := range storedData.AuthCodes {
		if now.Before(authCode.ExpiresAt) {
			s.authCodes[code] = authCode
			loadedAuthCodes++
		} else {
			logger().Debug("Ignoring expired auth code during load", "expired_at", authCode.ExpiresAt)
		}
	}

	loadedAccessTokens := 0
	for token, accessToken := range storedData.AccessTokens {
		if now.Before(accessToken.ExpiresAt) {
			s.accessTokens[token] = accessToken
			loadedAccessTokens++
		} else {
			logger().Debug("Ignoring expired access token during load", "expired_at", accessToken.ExpiresAt)
		}
	}

	logger().Info("Successfully loaded tokens from file",
		"file", s.tokensFile,
		"auth_codes_loaded", loadedAuthCodes,
		"access_tokens_loaded", loadedAccessTokens,
	)
	return nil
}

// saveTokens saves the current tokens to the persistence file
func (s *OAuth2Server) saveTokens(ctx context.Context) error {
	if !s.persistTokens {
		logger().Debug("Token persistence is disabled, skipping save")
		return nil
	}

	logger().Debug("Attempting to save tokens to file", "file", s.tokensFile)
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Create copies to avoid holding lock during marshaling/writing
	authCodesCopy := make(map[string]AuthCode, len(s.authCodes))
	maps.Copy(authCodesCopy, s.authCodes)
	accessTokensCopy := make(map[string]AccessToken, len(s.accessTokens))
	maps.Copy(accessTokensCopy, s.accessTokens)

	// Check context before marshaling/writing
	select {
	case <-ctx.Done():
		logger().Warn("Context cancelled before saving tokens", "error", ctx.Err())
		return ctx.Err()
	default:
	}

	storedData := struct {
		AuthCodes    map[string]AuthCode    `json:"auth_codes"`
		AccessTokens map[string]AccessToken `json:"access_tokens"`
	}{
		AuthCodes:    authCodesCopy,
		AccessTokens: accessTokensCopy,
	}

	data, err := json.MarshalIndent(storedData, "", "  ")
	if err != nil {
		logger().Error("Failed to marshal tokens for persistence", "error", err)
		return fmt.Errorf("failed to marshal tokens: %w", err)
	}

	// Write atomically if possible (write to temp file, then rename)
	tempFile := s.tokensFile + ".tmp"
	if err := os.WriteFile(tempFile, data, FilePermissions); err != nil {
		logger().Error("Failed to write tokens to temporary file", "file", tempFile, "error", err)
		return fmt.Errorf("failed to write tokens to temp file %s: %w", tempFile, err)
	}

	if err := os.Rename(tempFile, s.tokensFile); err != nil {
		logger().Error("Failed to rename temporary token file to final destination", "temp_file", tempFile, "final_file", s.tokensFile, "error", err)
		// Attempt to clean up temp file
		_ = os.Remove(tempFile)
		return fmt.Errorf("failed to rename temp token file %s to %s: %w", tempFile, s.tokensFile, err)
	}

	logger().Debug("Successfully saved tokens to file",
		"file", s.tokensFile,
		"auth_codes_saved", len(authCodesCopy),
		"access_tokens_saved", len(accessTokensCopy),
	)
	return nil
}

// StartAuthCleanup starts a background goroutine to clean up expired codes and tokens.
// The cleanup goroutine will stop when the provided context is canceled,
// enabling graceful shutdown.
func (s *OAuth2Server) StartAuthCleanup(ctx context.Context, interval time.Duration) {
	logger().Info("Starting periodic cleanup of expired tokens and codes", "interval", interval)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger().Info("Stopping auth cleanup goroutine", "reason", ctx.Err())
				return
			case <-ticker.C:
				s.cleanupExpired()
			}
		}
	}()
}

// cleanupExpired removes expired codes and tokens
func (s *OAuth2Server) cleanupExpired() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	now := time.Now()
	authCodesExpired := 0
	accessTokensExpired := 0
	needsSave := false

	logger().Debug("Running cleanup for expired tokens and codes")

	for code, authCode := range s.authCodes {
		if now.After(authCode.ExpiresAt) {
			delete(s.authCodes, code)
			authCodesExpired++
			needsSave = true
		}
	}

	for token, accessToken := range s.accessTokens {
		if now.After(accessToken.ExpiresAt) {
			delete(s.accessTokens, token)
			accessTokensExpired++
			needsSave = true
		}
	}

	if authCodesExpired > 0 || accessTokensExpired > 0 {
		logger().Info("Cleaned up expired entries",
			"auth_codes_removed", authCodesExpired,
			"access_tokens_removed", accessTokensExpired,
		)
		if needsSave {
			go s.persistTokensIfEnabled() // Persist removals in background
		}
	} else {
		logger().Debug("No expired entries found during cleanup")
	}
}

// logThrottledError logs an error message, but only once per specified interval for a given key
func (s *OAuth2Server) logThrottledError(key, msg string, err error, interval time.Duration) {
	shouldLog := false

	s.mutex.Lock()
	if s.throttledMessages == nil {
		s.throttledMessages = make(map[string]time.Time)
	}
	lastLogTime, exists := s.throttledMessages[key]
	if !exists || time.Since(lastLogTime) > interval {
		// Update timestamp while holding lock to prevent race condition
		s.throttledMessages[key] = time.Now()
		shouldLog = true
	}
	s.mutex.Unlock()

	// Log outside the lock to avoid blocking other operations
	if shouldLog {
		logger().Error(msg, "key", key, "error", err)
	}
}
