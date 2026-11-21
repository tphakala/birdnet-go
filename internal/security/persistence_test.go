package security

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
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
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.NewTestSettings().Apply() })

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
// by EndSession stays ended in the file even when a persist that took its
// snapshot before the logout is still writing after it: the save lock makes the
// logout's save wait for that persist, so the stale snapshot cannot be the last
// one written. The first save pauses after its snapshot until the logout has
// either finished (no save lock) or had time to block on the lock.
func TestEndSessionSurvivesRestartAfterRacingPersist(t *testing.T) {
	prevStore := gothic.Store
	t.Cleanup(func() { gothic.Store = prevStore })
	gothic.Store = sessions.NewCookieStore([]byte("test-secret-32-bytes-minimum-len"))
	tokensFile := filepath.Join(t.TempDir(), "tokens.json")
	settings := persistenceTestSettings()
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.NewTestSettings().Apply() })

	server := newPersistingServer(tokensFile, settings)
	token := seedSession(t, server, &AccessToken{})
	require.NoError(t, server.saveTokens(ctx))

	// Only the first save after this point pauses; later ones pass through.
	paused, release := make(chan struct{}), make(chan struct{})
	var saves atomic.Int32
	server.afterSnapshot = func() {
		if saves.Add(1) == 1 {
			close(paused)
			<-release
		}
	}

	// A renewal persist takes its snapshot, which still holds the session.
	var persists sync.WaitGroup
	persists.Go(server.persistTokensIfEnabled)
	<-paused

	// The user logs out while that persist is paused.
	loggedOut := make(chan struct{})
	go func() {
		defer close(loggedOut)
		server.EndSession(httptest.NewRecorder(), sessionRequest(token))
	}()
	select {
	case <-loggedOut:
	case <-time.After(lockWaitSettleTime):
	}
	close(release)
	persists.Wait()
	<-loggedOut

	restarted := newPersistingServer(tokensFile, settings)
	require.NoError(t, restarted.loadTokens(ctx))
	_, err := restarted.liveRecord(token)
	require.ErrorIs(t, err, ErrTokenNotFound, "a logged-out session must not come back after a restart")
}

// readStoredTokens returns what the token file at path holds.
func readStoredTokens(t *testing.T, path string) tokenFile {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test file path from t.TempDir()
	require.NoError(t, err)
	var stored tokenFile
	require.NoError(t, json.Unmarshal(data, &stored))
	return stored
}

// TestRestartWithChangedCredentialsEndsSessions verifies the startup path: a
// server that loads a token file written under other basic-auth credentials ends
// every session and code and saves the new credentials ID, and one that loads it
// under the same credentials keeps them.
func TestRestartWithChangedCredentialsEndsSessions(t *testing.T) {
	tests := []struct {
		name    string
		changed bool
	}{
		{"changed credentials end the sessions", true},
		{"unchanged credentials keep them", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokensFile := filepath.Join(t.TempDir(), "tokens.json")
			before := persistenceTestSettings()
			conftest.SetTestSettings(before)
			t.Cleanup(func() { conftest.NewTestSettings().Apply() })

			server := newPersistingServer(tokensFile, before)
			token := seedSession(t, server, &AccessToken{})
			server.mutex.Lock()
			server.authCodes["code"] = AuthCode{Code: "code", ExpiresAt: time.Now().Add(time.Hour)}
			server.mutex.Unlock()
			require.NoError(t, server.saveTokens(ctx))
			require.Equal(t, basicCredentialsID(before), readStoredTokens(t, tokensFile).CredentialsID)

			// The server restarts after the password changed (or did not).
			after := conf.CloneSettings(before)
			if tt.changed {
				after.Security.BasicAuth.Password = "changed"
			}
			conftest.SetTestSettings(after)
			restarted := newPersistingServer(tokensFile, after)
			require.NoError(t, restarted.loadTokens(ctx))
			restarted.syncCredentials()

			_, err := restarted.liveRecord(token)
			stored := readStoredTokens(t, tokensFile)
			assert.Equal(t, basicCredentialsID(after), stored.CredentialsID, "the live credentials ID must be saved")
			if tt.changed {
				require.ErrorIs(t, err, ErrTokenNotFound)
				assert.Empty(t, restarted.authCodes)
				assert.Empty(t, stored.Sessions)
				assert.Empty(t, stored.AuthCodes)
				return
			}
			require.NoError(t, err)
			assert.Len(t, restarted.authCodes, 1)
			assert.Len(t, stored.Sessions, 1)
		})
	}
}

// lockWaitSettleTime is how long a test waits to see whether a call that should be
// blocked on the save lock returns anyway. TESTING.md asks for at least 500 ms for
// async waits, so a loaded CI runner still schedules the call inside the window.
const lockWaitSettleTime = 500 * time.Millisecond

// TestSignInRecordIsOnDiskBeforeCookie verifies that a sign-in writes its record
// to the token file before it returns, so the cookie is never sent for a record
// that a restart would lose. The test holds the save lock: a sign-in that waits
// for the write cannot return while it is held.
func TestSignInRecordIsOnDiskBeforeCookie(t *testing.T) {
	tests := []struct {
		name  string
		start func(s *OAuth2Server, w http.ResponseWriter) (token string, err error)
	}{
		{"password sign-in", func(s *OAuth2Server, w http.ResponseWriter) (string, error) {
			token := seedSession(t, s, &AccessToken{})
			return token, s.StartSession(w, sessionRequest(""), token)
		}},
		{"provider sign-in", func(s *OAuth2Server, w http.ResponseWriter) (string, error) {
			// Settle the credentials first, so the only save left to wait for is
			// the one that carries the new record.
			settleCredentials(s)
			return "", s.StartProviderSession(w, sessionRequest(""),
				&AccessToken{Provider: ProviderGoogle, UserID: "user@example.com", ProviderUserID: "12345"}, true)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokensFile := filepath.Join(t.TempDir(), "tokens.json")
			settings := persistenceTestSettings()
			conftest.SetTestSettings(settings)
			t.Cleanup(func() { conftest.NewTestSettings().Apply() })
			server := newPersistingServer(tokensFile, settings)

			server.saveMu.Lock()
			w := httptest.NewRecorder()
			done := make(chan struct{})
			var token string
			var startErr error
			go func() {
				defer close(done)
				token, startErr = tt.start(server, w)
			}()
			select {
			case <-done:
				server.saveMu.Unlock()
				require.Fail(t, "the sign-in returned while the token file could not be written")
			case <-time.After(lockWaitSettleTime):
			}
			server.saveMu.Unlock()
			<-done

			require.NoError(t, startErr)
			require.NotNil(t, findCookie(w, SessionCookieName))
			stored := readStoredTokens(t, tokensFile)
			require.Len(t, stored.Sessions, 1, "the record must be on disk when the sign-in returns")
			if token != "" {
				assert.Contains(t, stored.Sessions, sessionTokenKey(token))
			}
		})
	}
}

// TestEndSessionLogsFailedSave verifies that a logout whose deletion cannot be
// written says so at error level, since the record would return after a restart.
func TestEndSessionLogsFailedSave(t *testing.T) {
	tokensFile := filepath.Join(t.TempDir(), "missing", "tokens.json")
	server := newPersistingServer(tokensFile, persistenceTestSettings())
	token := seedSession(t, server, &AccessToken{})

	output := logtest.Capture(t, func() {
		server.EndSession(httptest.NewRecorder(), sessionRequest(token))
	})

	// saveTokens logs its own write failure at error level too, so the level is
	// checked on the logout line itself.
	var logoutLine string
	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, "Failed to persist the end of a session") {
			logoutLine = line
		}
	}
	require.NotEmpty(t, logoutLine, "the failed logout save must be logged")
	assert.Contains(t, logoutLine, "level=ERROR")
	assert.NotContains(t, output, token, "a session token must never be logged")
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
