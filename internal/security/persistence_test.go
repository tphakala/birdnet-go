package security

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Shared context for tests in this file
var ctx = context.Background()

// osWindows is the runtime.GOOS value for Windows, used to guard assertions
// that rely on Unix file-permission or read-only-directory semantics.
const osWindows = "windows"

// persistenceTestSettings returns settings with basic authentication enabled,
// the shape the persisted password sessions below need to validate.
func persistenceTestSettings() *conf.Settings {
	return &conf.Settings{Security: conf.Security{
		SessionSecret: "test-secret-32-bytes-minimum-len",
		BasicAuth:     conf.BasicAuth{Enabled: true, ClientID: "client", Password: "pw"},
	}}
}

// newPersistingServer returns a server that persists to tokensFile.
func newPersistingServer(tokensFile string, settings *conf.Settings) *OAuth2Server {
	return &OAuth2Server{
		settings:      settings,
		accessTokens:  make(map[string]AccessToken),
		authCodes:     make(map[string]AuthCode),
		tokensFile:    tokensFile,
		persistTokens: true,
	}
}

// TestTokenPersistence tests saving and loading of session records
func TestTokenPersistence(t *testing.T) {
	tempDir := t.TempDir()
	tokensFile := filepath.Join(tempDir, "tokens.json")
	settings := persistenceTestSettings()

	server := newPersistingServer(tokensFile, settings)
	validToken := seedSession(t, server, &AccessToken{Remember: true})
	expiredToken := seedSession(t, server, &AccessToken{ExpiresAt: time.Now().Add(-time.Hour)})
	socialToken := seedSession(t, server, &AccessToken{
		Provider: ProviderGoogle, UserID: "user@example.com", ProviderUserID: "12345", IDToken: "id-token-value",
	})

	require.NoError(t, server.saveTokens(ctx), "Failed to save tokens")

	// A new server instance loads them
	newServer := newPersistingServer(tokensFile, settings)
	require.NoError(t, newServer.loadTokens(ctx), "Failed to load tokens")

	require.NoError(t, newServer.ValidateAccessToken(validToken), "Valid token should be loaded and validated")
	require.ErrorIs(t, newServer.ValidateAccessToken(expiredToken), ErrTokenNotFound, "Expired token should not be loaded, thus not found")

	loaded, err := newServer.liveRecord(validToken)
	require.NoError(t, err)
	assert.True(t, loaded.Remember, "the remember choice must survive a restart")

	social, err := newServer.liveRecord(socialToken)
	require.NoError(t, err)
	assert.Equal(t, ProviderGoogle, social.Provider)
	assert.Equal(t, "user@example.com", social.UserID)
	assert.Equal(t, "12345", social.ProviderUserID)
	assert.Equal(t, "id-token-value", social.IDToken)

	// The file holds only token hashes, never a token
	data, err := os.ReadFile(tokensFile) //nolint:gosec // test file path from t.TempDir()
	require.NoError(t, err, "Failed to read tokens file")
	for _, token := range []string{validToken, expiredToken, socialToken} {
		assert.NotContains(t, string(data), token, "a raw session token must never be persisted")
	}
	assert.Contains(t, string(data), sessionTokenKey(validToken))

	// Verify file permissions
	info, err := os.Stat(tokensFile)
	require.NoError(t, err, "Failed to stat tokens file")
	// Windows reports 0666 for regular files regardless of the mode saveTokens
	// requests; the 0600 bits are not observable on NTFS. Skip only the perm
	// comparison there.
	if runtime.GOOS != osWindows {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "Tokens file should have 0600 permissions")
	}
}

// TestTokenPersistenceIgnoresOldShape verifies a tokens.json written by an
// older build (access tokens under "access_tokens") loads with no sessions: the
// browser signs in once, and the old token is never accepted.
func TestTokenPersistenceIgnoresOldShape(t *testing.T) {
	tokensFile := filepath.Join(t.TempDir(), "tokens.json")
	old := `{"auth_codes":{},"access_tokens":{"old-token":{"Token":"old-token","ExpiresAt":"` +
		time.Now().Add(time.Hour).Format(time.RFC3339) + `"}}}`
	require.NoError(t, os.WriteFile(tokensFile, []byte(old), FilePermissions))

	server := newPersistingServer(tokensFile, persistenceTestSettings())
	require.NoError(t, server.loadTokens(ctx))

	assert.Empty(t, server.accessTokens)
	assert.ErrorIs(t, server.ValidateAccessToken("old-token"), ErrTokenNotFound)
}

// TestEndSessionSurvivesRestartAfterRacingPersist verifies that a session ended
// by EndSession stays ended in the file even when a persist that began before
// the logout finishes after it.
func TestEndSessionSurvivesRestartAfterRacingPersist(t *testing.T) {
	gothic.Store = sessions.NewCookieStore([]byte("test-secret-32-bytes-minimum-len"))
	tokensFile := filepath.Join(t.TempDir(), "tokens.json")
	settings := persistenceTestSettings()

	server := newPersistingServer(tokensFile, settings)
	token := seedSession(t, server, &AccessToken{})
	require.NoError(t, server.saveTokens(ctx))

	// A renewal persist starts, then the user logs out.
	var wg sync.WaitGroup
	wg.Go(server.persistTokensIfEnabled)
	server.EndSession(httptest.NewRecorder(), sessionRequest(token))
	wg.Wait()

	restarted := newPersistingServer(tokensFile, settings)
	require.NoError(t, restarted.loadTokens(ctx))
	_, err := restarted.liveRecord(token)
	require.ErrorIs(t, err, ErrTokenNotFound, "a logged-out session must not come back after a restart")
}

// TestFilesystemStore tests that the FilesystemStore is initialized correctly
func TestFilesystemStore(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

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

	// Initialize gothic with these settings; no effective TLS -> non-Secure cookie.
	InitializeGoth(settings, false)

	// Verify that gothic.Store is a FilesystemStore
	store, ok := unwrapSessionStore(gothic.Store).(*sessions.FilesystemStore)
	require.True(t, ok, "Gothic store should be a FilesystemStore")

	// Check FilesystemStore options
	assert.NotNil(t, store.Options, "Store options should not be nil")
	assert.Equal(t, "/", store.Options.Path, "Path should be /")
	assert.Equal(t, 86400*7, store.Options.MaxAge, "MaxAge should be 7 days")
	assert.False(t, store.Options.Secure, "Secure should be false when no effective TLS is configured")
	assert.True(t, store.Options.HttpOnly, "HttpOnly should be true")

	// Verify the sessions directory was created with correct permissions
	sessionsDir := filepath.Join(tempDir, "sessions")
	info, err := os.Stat(sessionsDir)
	require.NoError(t, err, "Failed to stat sessions directory")
	assert.True(t, info.IsDir(), "Sessions path should be a directory")
	// Windows reports 0777 for directories regardless of the mode requested; the
	// DirPermissions bits are not observable on NTFS. Skip only the perm
	// comparison there.
	if runtime.GOOS != osWindows {
		assert.Equal(t, os.FileMode(DirPermissions), info.Mode().Perm(), "Sessions directory should have secure permissions")
	}
}

// TestFilesystemStore_SecureCookieFlag verifies that the secureCookies argument
// threaded into InitializeGoth reaches the session cookie's Secure attribute. The
// decision of WHAT that flag should be (from the effective TLS config) is tested
// in internal/api (TestConfig_SessionCookiesSecure).
func TestFilesystemStore_SecureCookieFlag(t *testing.T) {
	// Not parallel: InitializeGoth mutates the global gothic.Store and the test
	// config path.
	tempDir := t.TempDir()
	SetTestConfigPath(tempDir)
	t.Cleanup(func() { SetTestConfigPath("") })

	tests := []struct {
		name   string
		secure bool
	}{
		{"secureCookies false", false},
		{"secureCookies true", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Subtests share the global gothic.Store; keep them sequential.
			settings := &conf.Settings{Security: conf.Security{SessionSecret: "test-secret"}}
			InitializeGoth(settings, tt.secure)

			store, ok := unwrapSessionStore(gothic.Store).(*sessions.FilesystemStore)
			require.True(t, ok, "gothic store should be a FilesystemStore")
			require.NotNil(t, store.Options, "store options should not be nil")
			assert.Equal(t, tt.secure, store.Options.Secure,
				"Secure flag should match the secureCookies argument")
		})
	}
}

// TestLoadCorruptedTokensFile tests handling of corrupted tokens file
func TestLoadCorruptedTokensFile(t *testing.T) {
	tempDir := t.TempDir()

	// Create a corrupted tokens file
	tokensFile := filepath.Join(tempDir, "tokens.json")
	err := os.WriteFile(tokensFile, []byte("this is not valid json"), 0o600)
	require.NoError(t, err, "Failed to write corrupted tokens file")

	server := &OAuth2Server{
		settings: &conf.Settings{},
		accessTokens: map[string]AccessToken{
			"test_token": {
				ExpiresAt: time.Now().Add(time.Hour),
			},
		},
		tokensFile:    tokensFile,
		persistTokens: true,
	}

	// Should handle error gracefully
	err = server.loadTokens(ctx)
	require.Error(t, err, "Loading corrupted file should return error")
	// Check for a more specific part of the error
	assert.Contains(t, err.Error(), "failed to unmarshal token data", "Error message should indicate unmarshal failure")
}

// Let's also add a test for unwritable directories
func TestUnwritableTokensDirectory(t *testing.T) {
	// Skip on Windows: chmod cannot make a directory unwritable for its owner
	// there, so saveTokens succeeds and no error is returned. (The previous
	// guard used os.Getenv("GOOS"), which is always empty because GOOS is a
	// build constant, not an environment variable, so the skip never fired.)
	if runtime.GOOS == osWindows {
		t.Skip("Skipping on Windows as permission handling is different")
	}

	tempDir := t.TempDir()

	// Create a token file path in a subdirectory that we'll make unwritable
	unwritableDir := filepath.Join(tempDir, "unwritable")
	err := os.Mkdir(unwritableDir, DirPermissions)
	require.NoError(t, err, "Failed to create unwritable directory")

	tokensFile := filepath.Join(unwritableDir, "tokens.json")

	// Make the directory read-only
	err = os.Chmod(unwritableDir, 0o500) //nolint:gosec // intentionally restrictive for test
	require.NoError(t, err, "Failed to make directory unwritable")
	defer func() {
		_ = os.Chmod(unwritableDir, DirPermissions) //nolint:gosec // restore permissions for cleanup
	}()

	server := &OAuth2Server{
		settings: &conf.Settings{},
		accessTokens: map[string]AccessToken{
			"test_token": {
				ExpiresAt: time.Now().Add(time.Hour),
			},
		},
		tokensFile:    tokensFile,
		persistTokens: true,
	}

	// Should handle error gracefully
	err = server.saveTokens(ctx)
	require.Error(t, err, "Saving tokens to unwritable directory should return error")
	// Check for a more specific part of the error related to file writing/renaming
	assert.Contains(t, err.Error(), "failed to write tokens to temp file", "Error message should indicate temp file write failure or rename failure")
}

// TestAtomicTokenSaving tests that tokens are saved using atomic file operations
func TestAtomicTokenSaving(t *testing.T) {
	t.Parallel()

	// Create a temporary directory for testing
	tempDir := t.TempDir() // Using t.TempDir() as recommended in Go test best practices

	tokensFile := filepath.Join(tempDir, "tokens.json")

	server := &OAuth2Server{
		settings: &conf.Settings{},
		accessTokens: map[string]AccessToken{
			"test_token": {
				ExpiresAt: time.Now().Add(time.Hour),
			},
		},
		tokensFile:    tokensFile,
		persistTokens: true,
	}

	// Save tokens
	err := server.saveTokens(ctx)
	require.NoError(t, err, "Should save tokens without errors")

	// Verify the main file exists
	_, err = os.Stat(tokensFile)
	require.NoError(t, err, "Token file should exist")

	// Verify the temp file does not exist (should be cleaned up)
	_, err = os.Stat(tokensFile + ".tmp")
	assert.True(t, os.IsNotExist(err), "Temp file should not exist after successful save")

	// Now test that contents are correct
	var storedData tokenFile

	data, err := os.ReadFile(tokensFile) //nolint:gosec // test file path from t.TempDir()
	require.NoError(t, err, "Should be able to read token file")

	err = json.Unmarshal(data, &storedData)
	require.NoError(t, err, "Token file should contain valid JSON matching the tokenFile struct")
	assert.NotNil(t, storedData.Sessions, "Sessions map should not be nil")
	_, ok := storedData.Sessions["test_token"]
	assert.True(t, ok, "Tokens file should contain the test_token in the sessions map")
}
