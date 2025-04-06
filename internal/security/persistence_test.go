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
	// Create test server with settings
	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				SessionSecret: "test-secret",
			},
		},
		debug: true,
	}

	// Test with CookieStore
	gothic.Store = sessions.NewCookieStore([]byte("test-secret"))
	server.configureLocalNetworkCookieStore()

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
	server.configureLocalNetworkCookieStore()

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

	// Create test server with settings
	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				SessionSecret: "test-secret",
			},
		},
		debug: true,
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
	server.configureLocalNetworkCookieStore()

	// Verify that appropriate warning was logged
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, "Warning: Unknown session store type")
	assert.Contains(t, logOutput, "mockStore")
}

// TestConfigureLocalNetworkWithMissingSessionSecret tests handling of missing session secret
func TestConfigureLocalNetworkWithMissingSessionSecret(t *testing.T) {
	// Create test server with empty session secret
	server := &OAuth2Server{
		Settings: &conf.Settings{
			Security: conf.Security{
				SessionSecret: "", // Empty session secret
			},
		},
		debug: true,
	}

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

	// This should not panic and should log appropriate warnings
	server.configureLocalNetworkCookieStore()

	// Verify that appropriate warning was logged
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, "Warning: Unknown session store type")
	assert.Contains(t, logOutput, "Warning: No session secret configured")
}

// TestLoadCorruptedTokensFile tests handling of corrupted tokens file
func TestLoadCorruptedTokensFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "birdnet-test-corrupt")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a corrupted tokens file
	tokensFile := filepath.Join(tempDir, "tokens.json")
	err = os.WriteFile(tokensFile, []byte("this is not valid json"), 0o600)
	if err != nil {
		t.Fatalf("Failed to write corrupted tokens file: %v", err)
	}

	server := &OAuth2Server{
		Settings: &conf.Settings{},
		accessTokens: map[string]AccessToken{
			"test_token": {
				Token:     "test_token",
				ExpiresAt: time.Now().Add(time.Hour),
			},
		},
		tokensFile:    tokensFile,
		persistTokens: true,
		debug:         true,
	}

	// Should handle error gracefully
	err = server.loadTokens()
	assert.Error(t, err, "Loading corrupted file should return error")
	assert.Contains(t, err.Error(), "failed to parse token file")
}

// Let's also add a test for unwritable directories
func TestUnwritableTokensDirectory(t *testing.T) {
	// Skip on Windows as permission handling differs
	if os.Getenv("GOOS") == "windows" {
		t.Skip("Skipping on Windows as permission handling is different")
	}

	tempDir, err := os.MkdirTemp("", "birdnet-test-unwritable")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a token file path in a subdirectory that we'll make unwritable
	unwritableDir := filepath.Join(tempDir, "unwritable")
	if err := os.Mkdir(unwritableDir, 0o755); err != nil {
		t.Fatalf("Failed to create unwritable directory: %v", err)
	}

	tokensFile := filepath.Join(unwritableDir, "tokens.json")

	// Make the directory read-only
	if err := os.Chmod(unwritableDir, 0o500); err != nil { // r-x --- ---
		t.Fatalf("Failed to make directory unwritable: %v", err)
	}
	defer os.Chmod(unwritableDir, 0o755) // Restore permissions for cleanup

	server := &OAuth2Server{
		Settings: &conf.Settings{},
		accessTokens: map[string]AccessToken{
			"test_token": {
				Token:     "test_token",
				ExpiresAt: time.Now().Add(time.Hour),
			},
		},
		tokensFile:    tokensFile,
		persistTokens: true,
		debug:         true,
	}

	// Should handle error gracefully
	err = server.saveTokens()
	assert.Error(t, err, "Saving tokens to unwritable directory should return error")
	assert.Contains(t, err.Error(), "failed to write tokens file")
}

// TestAtomicTokenSaving tests that tokens are saved using atomic file operations
func TestAtomicTokenSaving(t *testing.T) {
	t.Parallel()

	// Create a temporary directory for testing
	tempDir := t.TempDir() // Using t.TempDir() as recommended in Go test best practices

	tokensFile := filepath.Join(tempDir, "tokens.json")

	server := &OAuth2Server{
		Settings: &conf.Settings{},
		accessTokens: map[string]AccessToken{
			"test_token": {
				Token:     "test_token",
				ExpiresAt: time.Now().Add(time.Hour),
			},
		},
		tokensFile:    tokensFile,
		persistTokens: true,
		debug:         true,
	}

	// Save tokens
	err := server.saveTokens()
	assert.NoError(t, err, "Should save tokens without errors")

	// Verify the main file exists
	_, err = os.Stat(tokensFile)
	assert.NoError(t, err, "Token file should exist")

	// Verify the temp file does not exist (should be cleaned up)
	_, err = os.Stat(tokensFile + ".tmp")
	assert.True(t, os.IsNotExist(err), "Temp file should not exist after successful save")

	// Now test that contents are correct
	var tokens map[string]AccessToken
	data, err := os.ReadFile(tokensFile)
	assert.NoError(t, err, "Should be able to read token file")

	err = json.Unmarshal(data, &tokens)
	assert.NoError(t, err, "Token file should contain valid JSON")
	assert.Contains(t, tokens, "test_token", "Tokens file should contain the test token")
}
