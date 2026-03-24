package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// settingsWithSecrets returns a Settings struct populated with realistic secret
// values in every sensitive field. Used as the input for sanitization tests.
func settingsWithSecrets(t *testing.T) *conf.Settings {
	t.Helper()

	s := &conf.Settings{}

	// Security
	s.Security.SessionSecret = "hmac-session-key-abc123"
	s.Security.BasicAuth.Enabled = true
	s.Security.BasicAuth.Password = "admin-password"
	s.Security.BasicAuth.ClientID = "oauth2-internal-id"
	s.Security.BasicAuth.ClientSecret = "oauth2-internal-secret"

	// Legacy OAuth providers
	s.Security.GoogleAuth.ClientSecret = "google-secret"
	s.Security.GithubAuth.ClientSecret = "github-secret"
	s.Security.MicrosoftAuth.ClientSecret = "microsoft-secret"

	// Array-based OAuth providers
	s.Security.OAuthProviders = []conf.OAuthProviderConfig{
		{Provider: "google", Enabled: true, ClientID: "goog-id", ClientSecret: "goog-secret"},
		{Provider: "github", Enabled: true, ClientID: "gh-id", ClientSecret: "gh-secret"},
	}

	// MQTT
	s.Realtime.MQTT.Enabled = true
	s.Realtime.MQTT.Broker = "mqtt.local"
	s.Realtime.MQTT.Username = "mqtt-user"
	s.Realtime.MQTT.Password = "mqtt-password"

	// MySQL
	s.Output.MySQL.Enabled = true
	s.Output.MySQL.Username = "dbuser"
	s.Output.MySQL.Password = "db-password"
	s.Output.MySQL.Host = "db.local"

	// Weather API keys
	s.Realtime.Weather.OpenWeather.APIKey = "ow-api-key-123"
	s.Realtime.Weather.Wunderground.APIKey = "wu-api-key-456"

	// eBird
	s.Realtime.EBird.APIKey = "ebird-api-key-789"

	// Backup encryption
	s.Backup.EncryptionKey = "base64-encryption-key"
	s.Backup.Targets = []conf.BackupTarget{
		{
			Type:    "ftp",
			Enabled: true,
			Settings: map[string]any{
				"host":     "ftp.local",
				"username": "ftpuser",
				"password": "ftp-password",
			},
		},
		{
			Type:    "s3",
			Enabled: true,
			Settings: map[string]any{
				"bucket":          "my-bucket",
				"accesskeyid":     "AKIAEXAMPLE",
				"secretaccesskey": "s3-secret-key",
			},
		},
	}

	// Webhook notification auth
	s.Notification.Push.Providers = []conf.PushProviderConfig{
		{
			Type:    "webhook",
			Enabled: true,
			Endpoints: []conf.WebhookEndpointConfig{
				{
					URL: "https://hooks.example.com/notify",
					Auth: conf.WebhookAuthConfig{
						Type:  "bearer",
						Token: "bearer-token-secret",
					},
				},
				{
					URL: "https://hooks.example.com/other",
					Auth: conf.WebhookAuthConfig{
						Type: "basic",
						Pass: "basic-auth-password",
					},
				},
			},
		},
	}

	return s
}

// TestSanitizeSettingsForAPI_RedactsAllSecrets verifies that every sensitive
// field is replaced with a redacted placeholder or empty string.
func TestSanitizeSettingsForAPI_RedactsAllSecrets(t *testing.T) {
	original := settingsWithSecrets(t)
	sanitized := sanitizeSettingsForAPI(original)

	// --- Security ---
	assert.Equal(t, redactedValue, sanitized.Security.SessionSecret, "sessionSecret must be redacted")
	assert.Equal(t, redactedValue, sanitized.Security.BasicAuth.Password, "basicAuth.password must be redacted")
	assert.Empty(t, sanitized.Security.BasicAuth.ClientID, "basicAuth.clientId must be empty")
	assert.Empty(t, sanitized.Security.BasicAuth.ClientSecret, "basicAuth.clientSecret must be empty")

	// Legacy OAuth
	assert.Equal(t, redactedValue, sanitized.Security.GoogleAuth.ClientSecret, "googleAuth.clientSecret must be redacted")
	assert.Equal(t, redactedValue, sanitized.Security.GithubAuth.ClientSecret, "githubAuth.clientSecret must be redacted")
	assert.Equal(t, redactedValue, sanitized.Security.MicrosoftAuth.ClientSecret, "microsoftAuth.clientSecret must be redacted")

	// Array-based OAuth providers
	require.Len(t, sanitized.Security.OAuthProviders, 2)
	for i, p := range sanitized.Security.OAuthProviders {
		assert.Equal(t, redactedValue, p.ClientSecret, "oauthProviders[%d].clientSecret must be redacted", i)
		// ClientID should NOT be redacted (it's not a secret in OAuth)
		assert.NotEmpty(t, p.ClientID, "oauthProviders[%d].clientId should be preserved", i)
	}

	// --- MQTT ---
	assert.Equal(t, redactedValue, sanitized.Realtime.MQTT.Password, "mqtt.password must be redacted")
	assert.Equal(t, "mqtt-user", sanitized.Realtime.MQTT.Username, "mqtt.username should be preserved")
	assert.Equal(t, "mqtt.local", sanitized.Realtime.MQTT.Broker, "mqtt.broker should be preserved")

	// --- MySQL ---
	assert.Equal(t, redactedValue, sanitized.Output.MySQL.Password, "mysql.password must be redacted")
	assert.Equal(t, "dbuser", sanitized.Output.MySQL.Username, "mysql.username should be preserved")

	// --- Weather API keys ---
	assert.Equal(t, redactedValue, sanitized.Realtime.Weather.OpenWeather.APIKey, "openWeather.apiKey must be redacted")
	assert.Equal(t, redactedValue, sanitized.Realtime.Weather.Wunderground.APIKey, "wunderground.apiKey must be redacted")

	// --- eBird ---
	assert.Equal(t, redactedValue, sanitized.Realtime.EBird.APIKey, "ebird.apiKey must be redacted")

	// --- Backup ---
	assert.Equal(t, redactedValue, sanitized.Backup.EncryptionKey, "backup.encryptionKey must be redacted")
	require.Len(t, sanitized.Backup.Targets, 2)
	assert.Equal(t, redactedValue, sanitized.Backup.Targets[0].Settings["password"], "ftp password must be redacted")
	assert.Equal(t, "ftp.local", sanitized.Backup.Targets[0].Settings["host"], "ftp host should be preserved")
	assert.Equal(t, redactedValue, sanitized.Backup.Targets[1].Settings["secretaccesskey"], "s3 secretaccesskey must be redacted")
	assert.Equal(t, "my-bucket", sanitized.Backup.Targets[1].Settings["bucket"], "s3 bucket should be preserved")

	// --- Webhook auth ---
	require.Len(t, sanitized.Notification.Push.Providers, 1)
	require.Len(t, sanitized.Notification.Push.Providers[0].Endpoints, 2)
	assert.Equal(t, redactedValue, sanitized.Notification.Push.Providers[0].Endpoints[0].Auth.Token, "webhook bearer token must be redacted")
	assert.Equal(t, redactedValue, sanitized.Notification.Push.Providers[0].Endpoints[1].Auth.Pass, "webhook basic auth password must be redacted")
}

// TestSanitizeSettingsForAPI_DoesNotMutateOriginal verifies that the original
// Settings struct is not modified by sanitization.
func TestSanitizeSettingsForAPI_DoesNotMutateOriginal(t *testing.T) {
	original := settingsWithSecrets(t)

	// Capture original secret values
	origSessionSecret := original.Security.SessionSecret
	origPassword := original.Security.BasicAuth.Password
	origMQTTPassword := original.Realtime.MQTT.Password
	origMySQLPassword := original.Output.MySQL.Password
	origOAuthSecret := original.Security.OAuthProviders[0].ClientSecret
	origWebhookToken := original.Notification.Push.Providers[0].Endpoints[0].Auth.Token
	origFTPPassword := original.Backup.Targets[0].Settings["password"]

	_ = sanitizeSettingsForAPI(original)

	// Verify originals are untouched
	assert.Equal(t, origSessionSecret, original.Security.SessionSecret, "original sessionSecret mutated")
	assert.Equal(t, origPassword, original.Security.BasicAuth.Password, "original basicAuth.password mutated")
	assert.Equal(t, origMQTTPassword, original.Realtime.MQTT.Password, "original mqtt.password mutated")
	assert.Equal(t, origMySQLPassword, original.Output.MySQL.Password, "original mysql.password mutated")
	assert.Equal(t, origOAuthSecret, original.Security.OAuthProviders[0].ClientSecret, "original oauthProviders[0].clientSecret mutated")
	assert.Equal(t, origWebhookToken, original.Notification.Push.Providers[0].Endpoints[0].Auth.Token, "original webhook token mutated")
	assert.Equal(t, origFTPPassword, original.Backup.Targets[0].Settings["password"], "original ftp password mutated")
}

// TestSanitizeSettingsForAPI_EmptySecretsStayEmpty verifies that fields that
// were never set (empty strings) remain empty rather than showing the redacted
// placeholder. This lets the frontend distinguish "configured" from "not set".
func TestSanitizeSettingsForAPI_EmptySecretsStayEmpty(t *testing.T) {
	s := &conf.Settings{}
	// Leave all secret fields at their zero values (empty strings)

	sanitized := sanitizeSettingsForAPI(s)

	// sessionSecret is always redacted (it's auto-generated, so always has a value at runtime,
	// but the sanitizer unconditionally redacts it for defense-in-depth)
	assert.Equal(t, redactedValue, sanitized.Security.SessionSecret)

	// All other fields should stay empty when not configured
	assert.Empty(t, sanitized.Security.BasicAuth.Password, "empty password should stay empty")
	assert.Empty(t, sanitized.Security.GoogleAuth.ClientSecret, "empty googleAuth secret should stay empty")
	assert.Empty(t, sanitized.Realtime.MQTT.Password, "empty mqtt password should stay empty")
	assert.Empty(t, sanitized.Output.MySQL.Password, "empty mysql password should stay empty")
	assert.Empty(t, sanitized.Realtime.Weather.OpenWeather.APIKey, "empty openweather key should stay empty")
	assert.Empty(t, sanitized.Realtime.EBird.APIKey, "empty ebird key should stay empty")
	assert.Empty(t, sanitized.Backup.EncryptionKey, "empty encryption key should stay empty")
}

// TestSanitizeSettingsForAPI_JSONOutputHasNoSecrets performs an end-to-end
// check: serialize the sanitized settings to JSON and verify that no known
// secret value appears anywhere in the output.
func TestSanitizeSettingsForAPI_JSONOutputHasNoSecrets(t *testing.T) {
	original := settingsWithSecrets(t)
	sanitized := sanitizeSettingsForAPI(original)

	data, err := json.Marshal(sanitized)
	require.NoError(t, err, "JSON marshal must succeed")

	jsonStr := string(data)

	// None of the original secret values should appear in the JSON
	secrets := []string{
		"hmac-session-key-abc123",
		"admin-password",
		"oauth2-internal-id",
		"oauth2-internal-secret",
		"google-secret",
		"github-secret",
		"microsoft-secret",
		"goog-secret",
		"gh-secret",
		"mqtt-password",
		"db-password",
		"ow-api-key-123",
		"wu-api-key-456",
		"ebird-api-key-789",
		"base64-encryption-key",
		"ftp-password",
		"s3-secret-key",
		"bearer-token-secret",
		"basic-auth-password",
	}

	for _, secret := range secrets {
		assert.NotContains(t, jsonStr, secret, "JSON output contains secret: %s", secret)
	}

	// Non-secret values should still be present
	assert.Contains(t, jsonStr, "mqtt.local", "non-secret mqtt.broker should be in JSON")
	assert.Contains(t, jsonStr, "dbuser", "non-secret mysql.username should be in JSON")
	assert.Contains(t, jsonStr, "ftp.local", "non-secret ftp.host should be in JSON")
}

// TestRedact verifies the redact helper function.
func TestRedact(t *testing.T) {
	assert.Equal(t, redactedValue, redact("some-secret"), "non-empty string should be redacted")
	assert.Empty(t, redact(""), "empty string should stay empty")
}

// TestRestoreRedactedSecrets_PreservesRealValues verifies that a PUT
// round-trip (GET sanitized → PUT back) does not overwrite secrets.
func TestRestoreRedactedSecrets_PreservesRealValues(t *testing.T) {
	current := settingsWithSecrets(t)

	// Simulate what the frontend sends back: all secrets are the redacted placeholder
	incoming := settingsWithSecrets(t)
	incoming.Security.SessionSecret = redactedValue
	incoming.Security.BasicAuth.Password = redactedValue
	incoming.Security.GoogleAuth.ClientSecret = redactedValue
	incoming.Realtime.MQTT.Password = redactedValue
	incoming.Output.MySQL.Password = redactedValue
	incoming.Realtime.Weather.OpenWeather.APIKey = redactedValue
	incoming.Realtime.EBird.APIKey = redactedValue
	incoming.Backup.EncryptionKey = redactedValue
	incoming.Security.OAuthProviders[0].ClientSecret = redactedValue
	incoming.Notification.Push.Providers[0].Endpoints[0].Auth.Token = redactedValue

	restoreRedactedSecrets(current, incoming)

	// After restore, incoming should have the original real values
	assert.Equal(t, "hmac-session-key-abc123", incoming.Security.SessionSecret)
	assert.Equal(t, "admin-password", incoming.Security.BasicAuth.Password)
	assert.Equal(t, "google-secret", incoming.Security.GoogleAuth.ClientSecret)
	assert.Equal(t, "mqtt-password", incoming.Realtime.MQTT.Password)
	assert.Equal(t, "db-password", incoming.Output.MySQL.Password)
	assert.Equal(t, "ow-api-key-123", incoming.Realtime.Weather.OpenWeather.APIKey)
	assert.Equal(t, "ebird-api-key-789", incoming.Realtime.EBird.APIKey)
	assert.Equal(t, "base64-encryption-key", incoming.Backup.EncryptionKey)
	assert.Equal(t, "goog-secret", incoming.Security.OAuthProviders[0].ClientSecret)
	assert.Equal(t, "bearer-token-secret", incoming.Notification.Push.Providers[0].Endpoints[0].Auth.Token)
}

// TestRestoreRedactedSecrets_MatchesByProviderName verifies that OAuth
// provider secrets are restored by provider name, not by index position.
// This handles the case where the frontend reorders providers.
func TestRestoreRedactedSecrets_MatchesByProviderName(t *testing.T) {
	current := settingsWithSecrets(t)

	// Incoming has providers in reversed order, both with redacted secrets
	incoming := settingsWithSecrets(t)
	incoming.Security.OAuthProviders = []conf.OAuthProviderConfig{
		{Provider: "github", Enabled: true, ClientID: "gh-id", ClientSecret: redactedValue},
		{Provider: "google", Enabled: true, ClientID: "goog-id", ClientSecret: redactedValue},
	}

	restoreRedactedSecrets(current, incoming)

	// Each provider should get its own secret back, not the other's
	assert.Equal(t, "gh-secret", incoming.Security.OAuthProviders[0].ClientSecret, "github should get github's secret")
	assert.Equal(t, "goog-secret", incoming.Security.OAuthProviders[1].ClientSecret, "google should get google's secret")
}

// TestRestoreRedactedSecrets_MatchesByBackupType verifies that backup target
// secrets are restored by target type, not by index position.
func TestRestoreRedactedSecrets_MatchesByBackupType(t *testing.T) {
	current := settingsWithSecrets(t)

	// Incoming has targets in reversed order
	incoming := settingsWithSecrets(t)
	incoming.Backup.Targets = []conf.BackupTarget{
		{
			Type:    "s3",
			Enabled: true,
			Settings: map[string]any{
				"bucket":          "my-bucket",
				"accesskeyid":     "AKIAEXAMPLE",
				"secretaccesskey": redactedValue,
			},
		},
		{
			Type:    "ftp",
			Enabled: true,
			Settings: map[string]any{
				"host":     "ftp.local",
				"username": "ftpuser",
				"password": redactedValue,
			},
		},
	}

	restoreRedactedSecrets(current, incoming)

	assert.Equal(t, "s3-secret-key", incoming.Backup.Targets[0].Settings["secretaccesskey"], "s3 target should get s3 secret")
	assert.Equal(t, "ftp-password", incoming.Backup.Targets[1].Settings["password"], "ftp target should get ftp secret")
}

// TestRestoreRedactedSecrets_NilSettingsMap verifies no panic when an
// incoming backup target has a nil Settings map.
func TestRestoreRedactedSecrets_NilSettingsMap(t *testing.T) {
	current := settingsWithSecrets(t)

	incoming := &conf.Settings{}
	incoming.Backup.Targets = []conf.BackupTarget{
		{Type: "ftp", Enabled: true, Settings: nil},
	}

	// Should not panic
	assert.NotPanics(t, func() {
		restoreRedactedSecrets(current, incoming)
	})
}

// TestRestoreRedactedSecrets_AllowsNewValues verifies that when the user
// actually changes a secret (sends a new non-placeholder value), it is kept.
func TestRestoreRedactedSecrets_AllowsNewValues(t *testing.T) {
	current := settingsWithSecrets(t)

	incoming := settingsWithSecrets(t)
	incoming.Security.BasicAuth.Password = "new-password-from-user"
	incoming.Realtime.MQTT.Password = "new-mqtt-password"

	restoreRedactedSecrets(current, incoming)

	assert.Equal(t, "new-password-from-user", incoming.Security.BasicAuth.Password)
	assert.Equal(t, "new-mqtt-password", incoming.Realtime.MQTT.Password)
}

// TestRestoreRedactedSecrets_SessionSecret verifies that SessionSecret is
// restored as defense-in-depth (it's also blocked by getBlockedFieldMap).
func TestRestoreRedactedSecrets_SessionSecret(t *testing.T) {
	current := settingsWithSecrets(t)

	incoming := settingsWithSecrets(t)
	incoming.Security.SessionSecret = redactedValue

	restoreRedactedSecrets(current, incoming)

	assert.Equal(t, "hmac-session-key-abc123", incoming.Security.SessionSecret)
}

// TestRoundTrip_SanitizeThenRestore verifies the full GET→PUT cycle:
// sanitize for GET, then restore when the frontend sends the redacted
// values back unchanged.
func TestRoundTrip_SanitizeThenRestore(t *testing.T) {
	original := settingsWithSecrets(t)

	// Step 1: Sanitize (what GET returns)
	sanitized := sanitizeSettingsForAPI(original)
	assert.Equal(t, redactedValue, sanitized.Security.BasicAuth.Password)
	assert.Equal(t, redactedValue, sanitized.Realtime.MQTT.Password)

	// Step 2: Frontend sends the sanitized values back (unchanged)
	incoming := *sanitized // simulate frontend round-trip

	// Step 3: Restore before applying the update
	restoreRedactedSecrets(original, &incoming)

	// The incoming struct should now have the original real values
	assert.Equal(t, "admin-password", incoming.Security.BasicAuth.Password)
	assert.Equal(t, "mqtt-password", incoming.Realtime.MQTT.Password)
	assert.Equal(t, "db-password", incoming.Output.MySQL.Password)
	assert.Equal(t, "ow-api-key-123", incoming.Realtime.Weather.OpenWeather.APIKey)

	// Original must not have been mutated
	assert.Equal(t, "admin-password", original.Security.BasicAuth.Password)
}

// TestRoundTrip_UserChangesPassword verifies that when a user enters a
// new password (different from the redacted placeholder), it is preserved.
func TestRoundTrip_UserChangesPassword(t *testing.T) {
	original := settingsWithSecrets(t)
	sanitized := sanitizeSettingsForAPI(original)

	// User changes the password field to a new value
	incoming := *sanitized
	incoming.Security.BasicAuth.Password = "brand-new-password"

	restoreRedactedSecrets(original, &incoming)

	// New password should be kept, not restored to old
	assert.Equal(t, "brand-new-password", incoming.Security.BasicAuth.Password)
}

// TestRoundTrip_UserClearsPassword verifies that clearing a password
// (setting it to empty string) works and is not treated as redacted.
func TestRoundTrip_UserClearsPassword(t *testing.T) {
	original := settingsWithSecrets(t)
	sanitized := sanitizeSettingsForAPI(original)

	// User clears the password
	incoming := *sanitized
	incoming.Security.BasicAuth.Password = ""

	restoreRedactedSecrets(original, &incoming)

	// Empty should stay empty (user intentionally cleared it)
	assert.Empty(t, incoming.Security.BasicAuth.Password)
}

// TestRestoreRedactedSecrets_PATCHArrayMerge verifies that redacted
// values inside arrays survive a JSON merge + restore cycle. This simulates
// the PATCH path where deepMergeMaps replaces the whole array.
func TestRestoreRedactedSecrets_PATCHArrayMerge(t *testing.T) {
	current := settingsWithSecrets(t)

	// Simulate what happens after a PATCH merges JSON into the struct:
	// the array elements have the redacted placeholder because the frontend
	// sent them back unchanged.
	incoming := settingsWithSecrets(t)
	incoming.Security.OAuthProviders = []conf.OAuthProviderConfig{
		{Provider: "google", Enabled: true, ClientID: "goog-id", ClientSecret: redactedValue},
		{Provider: "github", Enabled: true, ClientID: "gh-id", ClientSecret: redactedValue},
	}
	incoming.Realtime.MQTT.Password = redactedValue
	incoming.Output.MySQL.Password = redactedValue

	restoreRedactedSecrets(current, incoming)

	// Secrets inside arrays should be restored by provider name
	assert.Equal(t, "goog-secret", incoming.Security.OAuthProviders[0].ClientSecret)
	assert.Equal(t, "gh-secret", incoming.Security.OAuthProviders[1].ClientSecret)
	// Scalar secrets should also be restored
	assert.Equal(t, "mqtt-password", incoming.Realtime.MQTT.Password)
	assert.Equal(t, "db-password", incoming.Output.MySQL.Password)
}
