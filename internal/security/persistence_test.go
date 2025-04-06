package security

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestTokenPersistence tests saving and loading of access tokens
func TestTokenPersistence(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "birdnet-test-tokens")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test server with custom token file
	server := &OAuth2Server{
		Settings:      &conf.Settings{Security: conf.Security{BasicAuth: conf.BasicAuth{AccessTokenExp: time.Hour}}},
		accessTokens:  make(map[string]AccessToken),
		authCodes:     make(map[string]AuthCode),
		tokensFile:    filepath.Join(tempDir, "tokens.json"),
		persistTokens: true,
		debug:         true,
	}

	// Add some test tokens
	testTokens := map[string]time.Time{
		"valid_token":   time.Now().Add(time.Hour),
		"expired_token": time.Now().Add(-time.Hour),
	}

	// Add tokens to the server
	for token, expiry := range testTokens {
		server.accessTokens[token] = AccessToken{
			Token:     token,
			ExpiresAt: expiry,
		}
	}

	// Save tokens
	err = server.saveTokens()
	if err != nil {
		t.Fatalf("Failed to save tokens: %v", err)
	}

	// Create a new server instance to load tokens
	newServer := &OAuth2Server{
		Settings:      &conf.Settings{},
		accessTokens:  make(map[string]AccessToken),
		tokensFile:    filepath.Join(tempDir, "tokens.json"),
		persistTokens: true,
		debug:         true,
	}

	// Load tokens
	err = newServer.loadTokens()
	if err != nil {
		t.Fatalf("Failed to load tokens: %v", err)
	}

	// Verify only valid tokens were loaded
	assert.True(t, newServer.ValidateAccessToken("valid_token"), "Valid token should be loaded and validated")
	assert.False(t, newServer.ValidateAccessToken("expired_token"), "Expired token should not be loaded or should be invalid")

	// Check token file contents directly
	data, err := os.ReadFile(filepath.Join(tempDir, "tokens.json"))
	if err != nil {
		t.Fatalf("Failed to read tokens file: %v", err)
	}

	var savedTokens map[string]AccessToken
	err = json.Unmarshal(data, &savedTokens)
	if err != nil {
		t.Fatalf("Failed to parse tokens file: %v", err)
	}

	// Verify file permissions
	info, err := os.Stat(filepath.Join(tempDir, "tokens.json"))
	if err != nil {
		t.Fatalf("Failed to stat tokens file: %v", err)
	}
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "Tokens file should have 0600 permissions")
}

// TestFilesystemStore tests that the FilesystemStore is initialized correctly
func TestFilesystemStore(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "birdnet-test-sessions")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Set test config path and restore after test
	SetTestConfigPath(tempDir)
	defer SetTestConfigPath("")

	// Setup test settings
	settings := &conf.Settings{
		Security: conf.Security{
			SessionSecret:   "test-secret",
			RedirectToHTTPS: false,
		},
	}

	// Initialize gothic with these settings
	InitializeGoth(settings)

	// Verify that gothic.Store is a FilesystemStore
	_, ok := gothic.Store.(*sessions.FilesystemStore)
	assert.True(t, ok, "Gothic store should be a FilesystemStore")

	// Check FilesystemStore options
	store := gothic.Store.(*sessions.FilesystemStore)
	assert.NotNil(t, store.Options, "Store options should not be nil")
	assert.Equal(t, "/", store.Options.Path, "Path should be /")
	assert.Equal(t, 86400*7, store.Options.MaxAge, "MaxAge should be 7 days")
	assert.Equal(t, false, store.Options.Secure, "Secure should match RedirectToHTTPS")
	assert.Equal(t, true, store.Options.HttpOnly, "HttpOnly should be true")

	// Verify the sessions directory was created with correct permissions
	sessionsDir := filepath.Join(tempDir, "sessions")
	info, err := os.Stat(sessionsDir)
	if err != nil {
		t.Fatalf("Failed to stat sessions directory: %v", err)
	}
	assert.True(t, info.IsDir(), "Sessions path should be a directory")
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "Sessions directory should have 0755 permissions")
}

// TestLocalNetworkCookieStore tests configuring cookie store for local network access
func TestLocalNetworkCookieStore(t *testing.T) {
	// Test with CookieStore
	gothic.Store = sessions.NewCookieStore([]byte("test-secret"))
	configureLocalNetworkCookieStore()

	cookieStore, ok := gothic.Store.(*sessions.CookieStore)
	assert.True(t, ok, "Gothic store should be a CookieStore")
	assert.Equal(t, false, cookieStore.Options.Secure, "Secure should be false for local network")

	// Test with FilesystemStore
	tempDir, err := os.MkdirTemp("", "birdnet-test-local")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	gothic.Store = sessions.NewFilesystemStore(tempDir, []byte("test-secret"))
	configureLocalNetworkCookieStore()

	fileStore, ok := gothic.Store.(*sessions.FilesystemStore)
	assert.True(t, ok, "Gothic store should be a FilesystemStore")
	assert.Equal(t, false, fileStore.Options.Secure, "Secure should be false for local network")
}

// TestConfigureLocalNetworkWithUnknownStore tests handling of unknown store types
func TestConfigureLocalNetworkWithUnknownStore(t *testing.T) {
	// Create a mock store that doesn't match our expected types
	type mockStore struct {
		sessions.Store
	}

	// Save original log output to restore it later
	originalOutput := log.Writer()
	defer log.SetOutput(originalOutput)

	// Capture logs to verify warning message
	var logBuffer bytes.Buffer
	log.SetOutput(&logBuffer)

	// Set the mock store
	gothic.Store = &mockStore{}

	// This should not panic and should log a warning
	configureLocalNetworkCookieStore()

	// Verify that appropriate warning was logged
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, "Warning: Unknown session store type")
	assert.Contains(t, logOutput, "mockStore")
}
