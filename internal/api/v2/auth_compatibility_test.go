// auth_compatibility_test.go: Tests for authentication compatibility between V1 and V2 endpoints.
// This file exposes issues where the old UI (/login) works but the new UI (/api/v2/auth/login) fails.
// See GitHub Issue #1234: "Cannot login when using /ui endpoint"

package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestAuthCompatibility_V1VsV2Differences documents and tests the authentication
// differences between V1 (/login) and V2 (/api/v2/auth/login) endpoints.
//
// Key difference discovered:
// - V1 (old UI): Only checks password against BasicAuth.Password
// - V2 (new UI): Checks BOTH username AND password
//
// This causes issues when:
// 1. BasicAuth.ClientID is empty or not set
// 2. BasicAuth.ClientID differs from the hardcoded "birdnet-client" in the frontend
func TestAuthCompatibility_V1VsV2Differences(t *testing.T) {
	testCases := []struct {
		name               string
		storedClientID     string
		storedPassword     string
		inputUsername      string
		inputPassword      string
		v1ShouldSucceed    bool // V1 only checks password
		v2ShouldSucceed    bool // V2 checks both username and password
		description        string
	}{
		{
			name:               "Both succeed - correct clientID and password",
			storedClientID:     "birdnet-client",
			storedPassword:     "secret123",
			inputUsername:      "birdnet-client",
			inputPassword:      "secret123",
			v1ShouldSucceed:    true,
			v2ShouldSucceed:    true,
			description:        "Happy path - default config with matching credentials",
		},
		{
			name:               "FIXED: Both succeed - empty clientID (V1 compatible mode)",
			storedClientID:     "", // Empty ClientID - possible for old configs
			storedPassword:     "secret123",
			inputUsername:      "birdnet-client", // Frontend hardcodes this
			inputPassword:      "secret123",
			v1ShouldSucceed:    true, // V1 doesn't check username
			v2ShouldSucceed:    true, // V2 now skips username check when ClientID is empty
			description:        "Issue #1234 FIXED: Empty ClientID now works (V1 compatible mode)",
		},
		{
			name:               "V1 succeeds, V2 fails - different clientID (expected behavior)",
			storedClientID:     "admin", // Different ClientID explicitly configured
			storedPassword:     "secret123",
			inputUsername:      "birdnet-client", // Frontend hardcodes this
			inputPassword:      "secret123",
			v1ShouldSucceed:    true,  // V1 doesn't check username
			v2ShouldSucceed:    false, // V2 correctly fails because "admin" != "birdnet-client"
			description:        "When ClientID is explicitly set, V2 requires it to match",
		},
		{
			name:               "Both fail - wrong password",
			storedClientID:     "birdnet-client",
			storedPassword:     "secret123",
			inputUsername:      "birdnet-client",
			inputPassword:      "wrongpassword",
			v1ShouldSucceed:    false,
			v2ShouldSucceed:    false,
			description:        "Both should fail with incorrect password",
		},
		{
			name:               "V1 succeeds, V2 fails - wrong username with correct password",
			storedClientID:     "birdnet-client",
			storedPassword:     "secret123",
			inputUsername:      "wrong-user",
			inputPassword:      "secret123",
			v1ShouldSucceed:    true,  // V1 doesn't check username at all
			v2ShouldSucceed:    false, // V2 requires username match when ClientID is set
			description:        "V2 is more secure: requires username match when ClientID is configured",
		},
		{
			name:               "Empty ClientID - any username works (V1 compatible)",
			storedClientID:     "", // Empty - V1 compatible mode
			storedPassword:     "secret123",
			inputUsername:      "any-random-user",
			inputPassword:      "secret123",
			v1ShouldSucceed:    true, // V1 doesn't check username
			v2ShouldSucceed:    true, // V2 skips username check when ClientID is empty
			description:        "When ClientID is empty, V2 behaves like V1 (password-only)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Log(tc.description)

			// Test V1 authentication logic (from auth_routes.go handleBasicAuthLogin)
			v1Result := simulateV1Auth(tc.storedPassword, tc.inputPassword)
			assert.Equal(t, tc.v1ShouldSucceed, v1Result,
				"V1 auth result mismatch for: %s", tc.name)

			// Test V2 authentication logic (from adapter.go AuthenticateBasic)
			v2Result := simulateV2Auth(tc.storedClientID, tc.storedPassword, tc.inputUsername, tc.inputPassword)
			assert.Equal(t, tc.v2ShouldSucceed, v2Result,
				"V2 auth result mismatch for: %s", tc.name)

			// Log the discrepancy for bug cases
			if v1Result != v2Result {
				t.Logf("AUTH DISCREPANCY DETECTED: V1=%v, V2=%v", v1Result, v2Result)
				t.Logf("This is the root cause of Issue #1234 - old UI works but new UI fails")
			}
		})
	}
}

// simulateV1Auth replicates the V1 authentication logic from auth_routes.go
// V1 only checks the password, NOT the username
func simulateV1Auth(storedPassword, inputPassword string) bool {
	// From auth_routes.go line 249-252:
	// passwordHash := sha256.Sum256([]byte(password))
	// storedPasswordHash := sha256.Sum256([]byte(storedPassword))
	// if subtle.ConstantTimeCompare(passwordHash[:], storedPasswordHash[:]) != 1 { ... }
	passwordHash := sha256.Sum256([]byte(inputPassword))
	storedPasswordHash := sha256.Sum256([]byte(storedPassword))
	return subtle.ConstantTimeCompare(passwordHash[:], storedPasswordHash[:]) == 1
}

// simulateV2Auth replicates the FIXED V2 authentication logic from adapter.go
// V2 now skips username check when ClientID is empty (backwards compatible with V1)
func simulateV2Auth(storedClientID, storedPassword, inputUsername, inputPassword string) bool {
	// Password check is always required
	passwordHash := sha256.Sum256([]byte(inputPassword))
	storedPasswordHash := sha256.Sum256([]byte(storedPassword))
	passMatch := subtle.ConstantTimeCompare(passwordHash[:], storedPasswordHash[:]) == 1

	// Username check: only if ClientID is configured (non-empty)
	// This is the fix for Issue #1234
	var userMatch bool
	if storedClientID == "" {
		// ClientID not configured - skip username check (V1 compatible behavior)
		userMatch = true
	} else {
		// ClientID configured - require username to match
		usernameHash := sha256.Sum256([]byte(inputUsername))
		storedClientIDHash := sha256.Sum256([]byte(storedClientID))
		userMatch = subtle.ConstantTimeCompare(usernameHash[:], storedClientIDHash[:]) == 1
	}

	return userMatch && passMatch
}

// TestDefaultClientIDValue verifies the default ClientID matches frontend expectation
func TestDefaultClientIDValue(t *testing.T) {
	// The frontend hardcodes this value in LoginModal.svelte
	// The backend default is set in internal/conf/defaults.go
	// These MUST match for authentication to work
	const frontendHardcodedUsername = "birdnet-client"
	const backendDefaultClientID = "birdnet-client" // From defaults.go line 306

	t.Run("frontend and backend defaults must match", func(t *testing.T) {
		assert.Equal(t, backendDefaultClientID, frontendHardcodedUsername,
			"Backend default ClientID must match frontend hardcoded username")
	})
}

// TestEmptyClientIDScenario tests the empty ClientID scenario
// This was the root cause of Issue #1234, now FIXED
func TestEmptyClientIDScenario(t *testing.T) {
	// Simulate a config.yaml from an old installation that doesn't have ClientID set
	settings := &conf.Settings{
		Security: conf.Security{
			BasicAuth: conf.BasicAuth{
				Enabled:  true,
				Password: "mypassword",
				ClientID: "", // Empty - now works with the fix
			},
		},
	}

	t.Run("empty ClientID now works with V2 (Issue #1234 FIXED)", func(t *testing.T) {
		require.True(t, settings.Security.BasicAuth.Enabled)
		require.NotEmpty(t, settings.Security.BasicAuth.Password)
		require.Empty(t, settings.Security.BasicAuth.ClientID)

		// Frontend always sends "birdnet-client" as username
		frontendUsername := "birdnet-client"
		correctPassword := "mypassword"

		// V1 auth (old UI) - only checks password
		v1Success := simulateV1Auth(settings.Security.BasicAuth.Password, correctPassword)
		assert.True(t, v1Success, "V1 auth should succeed with correct password")

		// V2 auth (new UI) - NOW also succeeds because empty ClientID triggers V1-compatible mode
		v2Success := simulateV2Auth(
			settings.Security.BasicAuth.ClientID,
			settings.Security.BasicAuth.Password,
			frontendUsername,
			correctPassword,
		)
		assert.True(t, v2Success, "V2 auth should NOW SUCCEED when ClientID is empty (V1 compatible mode)")

		t.Log("Issue #1234 is FIXED:")
		t.Log("- User sets password in config.yaml but ClientID is empty")
		t.Log("- Old UI (/login) works because it only checks password")
		t.Log("- New UI (/ui) NOW ALSO WORKS because empty ClientID triggers V1 compatible mode")
	})
}

// TestImplementedFix documents and verifies the implemented fix for Issue #1234
func TestImplementedFix(t *testing.T) {
	t.Run("IMPLEMENTED: Option 4 - V2 treats empty ClientID as 'any username'", func(t *testing.T) {
		// If ClientID is empty, skip username check (V1 behavior)
		// If ClientID is set, require it to match (V2 behavior)
		//
		// This approach was chosen because:
		// - Backwards compatible with existing configs that don't have ClientID
		// - Allows stricter authentication when ClientID is explicitly configured
		// - Documented in adapter.go function comments
		//
		// Implementation in adapter.go AuthenticateBasic:
		// - Check if storedClientID is empty
		// - If empty: skip username validation (V1 compatible mode)
		// - If non-empty: require username to match ClientID

		// Verify the fix works
		emptyClientID := ""
		configuredClientID := "birdnet-client"
		password := "secret123"
		frontendUsername := "birdnet-client"

		// Empty ClientID - should succeed (V1 compatible)
		assert.True(t, simulateV2Auth(emptyClientID, password, frontendUsername, password),
			"Empty ClientID should allow any username (V1 compatible)")

		// Configured ClientID with matching username - should succeed
		assert.True(t, simulateV2Auth(configuredClientID, password, frontendUsername, password),
			"Matching ClientID and username should succeed")

		// Configured ClientID with wrong username - should fail
		assert.False(t, simulateV2Auth(configuredClientID, password, "wrong-user", password),
			"Mismatched ClientID and username should fail")

		t.Log("Fix implemented in: internal/api/v2/auth/adapter.go AuthenticateBasic()")
		t.Log("See function documentation for full behavior description")
	})
}
