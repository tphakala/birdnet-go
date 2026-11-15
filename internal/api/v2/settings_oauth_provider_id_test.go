package api

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	testOAuthIssuerURL   = "https://idp.example.com"
	testOAuthBaseURL     = "https://birds.example.com"
	testOAuthSecret      = "stored-secret"
	testOAuthUserAllowed = "user@example.com"
)

func oauthProviderPayload(provider string, extra map[string]any) map[string]any {
	entry := map[string]any{
		"provider":     provider,
		"enabled":      true,
		"clientId":     "id",
		"clientSecret": testOAuthSecret,
		"userId":       testOAuthUserAllowed,
	}
	maps.Copy(entry, extra)
	return entry
}

// TestUpdateSettingsNormalizesOAuthProviderID verifies the full-settings PUT path
// stores the provider ID in canonical form, so the OIDC rules and the goth
// registration see it.
func TestUpdateSettingsNormalizesOAuthProviderID(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)

	s := conf.CloneSettings(controller.Settings.Load())
	s.Security.BaseURL = testOAuthBaseURL
	s.Security.OAuthProviders = []conf.OAuthProviderConfig{{
		Provider: "OIDC", Enabled: true, ClientID: "id", ClientSecret: testOAuthSecret,
		UserID: testOAuthUserAllowed, IssuerURL: testOAuthIssuerURL,
	}}
	putFullSettings(t, e, controller, s)

	got := controller.Settings.Load().Security.OAuthProviders
	require.Len(t, got, 1)
	assert.Equal(t, "oidc", got[0].Provider)
}

// TestUpdateSectionSettingsNormalizesOAuthProviderID verifies PATCH accepts the
// oidc provider (missing from the valid list before) in any case and stores the
// canonical ID.
func TestUpdateSectionSettingsNormalizesOAuthProviderID(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	controller.Settings.Load().Security.BaseURL = testOAuthBaseURL

	patchSection(t, e, controller, "security", map[string]any{
		"oauthProviders": []any{oauthProviderPayload("OIDC", map[string]any{"issuerUrl": testOAuthIssuerURL})},
	})

	got := controller.Settings.Load().Security.OAuthProviders
	require.Len(t, got, 1)
	assert.Equal(t, "oidc", got[0].Provider)
}

// TestValidateOAuthProvidersArrayRejectsBlankProviderAfterTrim verifies a
// whitespace-only provider reports the empty-string error, not an invalid provider.
func TestValidateOAuthProvidersArrayRejectsBlankProviderAfterTrim(t *testing.T) {
	t.Parallel()

	err := validateOAuthProvidersArray(map[string]any{
		"oauthProviders": []any{oauthProviderPayload("   ", nil)},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a non-empty string")
}

// TestUpdateSectionSettingsOAuthProviderDuplicates verifies PATCH applies the same
// duplicate rule as load and PUT: two enabled entries with the same canonical ID
// are rejected, an enabled entry with a disabled leftover is not.
func TestUpdateSectionSettingsOAuthProviderDuplicates(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	controller.Settings.Load().Security.BaseURL = testOAuthBaseURL

	rejected := sendSectionPatch(t, e, controller, "security", map[string]any{
		"oauthProviders": []any{oauthProviderPayload("google", nil), oauthProviderPayload("Google", nil)},
	})
	assert.Equal(t, http.StatusBadRequest, rejected.Code)

	patchSection(t, e, controller, "security", map[string]any{
		"oauthProviders": []any{
			oauthProviderPayload("google", map[string]any{"enabled": false}),
			oauthProviderPayload("Google", nil),
		},
	})
	got := controller.Settings.Load().Security.OAuthProviders
	require.Len(t, got, 2)
	assert.False(t, got[0].Enabled)
	assert.True(t, got[1].Enabled)
}

// TestUpdateSettingsKeepsOAuthSecretWhenProviderIDCaseChanges verifies the ID is
// canonicalized before redacted secrets are restored, so a case-only change of the
// ID while the secret is hidden keeps the stored secret.
func TestUpdateSettingsKeepsOAuthSecretWhenProviderIDCaseChanges(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	controller.Settings.Load().Security.BaseURL = testOAuthBaseURL
	controller.Settings.Load().Security.OAuthProviders = []conf.OAuthProviderConfig{{
		Provider: "google", Enabled: true, ClientID: "id", ClientSecret: testOAuthSecret, UserID: testOAuthUserAllowed,
	}}

	s := conf.CloneSettings(controller.Settings.Load())
	s.Security.OAuthProviders[0].Provider = "Google"
	s.Security.OAuthProviders[0].ClientSecret = redactedValue
	putFullSettings(t, e, controller, s)

	got := controller.Settings.Load().Security.OAuthProviders
	require.Len(t, got, 1)
	assert.Equal(t, "google", got[0].Provider)
	assert.Equal(t, testOAuthSecret, got[0].ClientSecret)
}

// TestUpdateSectionSettingsKeepsOAuthSecretWhenProviderIDCaseChanges verifies the
// section PATCH canonicalizes the ID before redacted secrets are restored, so a
// case-only change of the ID while the secret is hidden keeps the stored secret.
func TestUpdateSectionSettingsKeepsOAuthSecretWhenProviderIDCaseChanges(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	controller.Settings.Load().Security.BaseURL = testOAuthBaseURL
	controller.Settings.Load().Security.OAuthProviders = []conf.OAuthProviderConfig{{
		Provider: "google", Enabled: true, ClientID: "id", ClientSecret: testOAuthSecret, UserID: testOAuthUserAllowed,
	}}

	patchSection(t, e, controller, "security", map[string]any{
		"oauthProviders": []any{oauthProviderPayload("Google", map[string]any{"clientSecret": redactedValue})},
	})

	got := controller.Settings.Load().Security.OAuthProviders
	require.Len(t, got, 1)
	assert.Equal(t, "google", got[0].Provider)
	assert.Equal(t, testOAuthSecret, got[0].ClientSecret)
}

// TestUpdateSettingsRejectsCaseVariantOAuthProviderDuplicates verifies the full
// settings PUT rejects two enabled entries whose IDs differ only in case, which
// canonicalisation turns into the same provider.
func TestUpdateSettingsRejectsCaseVariantOAuthProviderDuplicates(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)

	s := conf.CloneSettings(controller.Settings.Load())
	s.Security.BaseURL = testOAuthBaseURL
	s.Security.OAuthProviders = []conf.OAuthProviderConfig{
		{Provider: "google", Enabled: true, ClientID: "id1", ClientSecret: testOAuthSecret, UserID: testOAuthUserAllowed},
		{Provider: "Google", Enabled: true, ClientID: "id2", ClientSecret: testOAuthSecret, UserID: testOAuthUserAllowed},
	}
	body, err := json.Marshal(s)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, controller.UpdateSettings(e.NewContext(req, rec)))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestUpdateSettingsRestoresSecretOfMatchingOAuthEntry verifies the redacted secret
// is restored from the stored entry with the same ID and enabled state, so a
// disabled leftover listed first does not donate its secret to the enabled entry,
// including when both entries share a client ID.
func TestUpdateSettingsRestoresSecretOfMatchingOAuthEntry(t *testing.T) {
	tests := []struct {
		name                         string
		leftoverClientID, liveClient string
	}{
		{name: "different client IDs", leftoverClientID: "leftover", liveClient: "live"},
		{name: "same client ID", leftoverClientID: "same", liveClient: "same"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _, controller := setupTestEnvironment(t)
			const leftoverSecret = "leftover-secret"
			controller.Settings.Load().Security.BaseURL = testOAuthBaseURL
			controller.Settings.Load().Security.OAuthProviders = []conf.OAuthProviderConfig{
				{Provider: "google", Enabled: false, ClientID: tt.leftoverClientID, ClientSecret: leftoverSecret, UserID: testOAuthUserAllowed},
				{Provider: "google", Enabled: true, ClientID: tt.liveClient, ClientSecret: testOAuthSecret, UserID: testOAuthUserAllowed},
			}

			s := conf.CloneSettings(controller.Settings.Load())
			s.Security.OAuthProviders[0].ClientSecret = redactedValue
			s.Security.OAuthProviders[1].ClientSecret = redactedValue
			putFullSettings(t, e, controller, s)

			got := controller.Settings.Load().Security.OAuthProviders
			require.Len(t, got, 2)
			assert.Equal(t, leftoverSecret, got[0].ClientSecret)
			assert.Equal(t, testOAuthSecret, got[1].ClientSecret)
		})
	}
}

// TestUpdateSettingsRestoresSecretWhenEnabledFlagsSwap verifies each redacted
// secret goes back to the entry with the same client ID even when the enabled
// flags of two same-ID entries change in one save.
func TestUpdateSettingsRestoresSecretWhenEnabledFlagsSwap(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	const leftoverSecret = "leftover-secret"
	controller.Settings.Load().Security.BaseURL = testOAuthBaseURL
	controller.Settings.Load().Security.OAuthProviders = []conf.OAuthProviderConfig{
		{Provider: "google", Enabled: false, ClientID: "leftover", ClientSecret: leftoverSecret, UserID: testOAuthUserAllowed},
		{Provider: "google", Enabled: true, ClientID: "live", ClientSecret: testOAuthSecret, UserID: testOAuthUserAllowed},
	}

	s := conf.CloneSettings(controller.Settings.Load())
	s.Security.OAuthProviders[0].Enabled = true
	s.Security.OAuthProviders[0].ClientSecret = redactedValue
	s.Security.OAuthProviders[1].Enabled = false
	s.Security.OAuthProviders[1].ClientSecret = redactedValue
	putFullSettings(t, e, controller, s)

	got := controller.Settings.Load().Security.OAuthProviders
	require.Len(t, got, 2)
	assert.Equal(t, leftoverSecret, got[0].ClientSecret)
	assert.Equal(t, testOAuthSecret, got[1].ClientSecret)
}

// TestUpdateSettingsRestoresSecretWhenClientIDChanges verifies a redacted secret is
// still restored from the stored entry with the same provider ID when only the
// client ID was edited.
func TestUpdateSettingsRestoresSecretWhenClientIDChanges(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	controller.Settings.Load().Security.BaseURL = testOAuthBaseURL
	controller.Settings.Load().Security.OAuthProviders = []conf.OAuthProviderConfig{
		{Provider: "google", Enabled: true, ClientID: "old", ClientSecret: testOAuthSecret, UserID: testOAuthUserAllowed},
	}

	s := conf.CloneSettings(controller.Settings.Load())
	s.Security.OAuthProviders[0].ClientID = "new"
	s.Security.OAuthProviders[0].ClientSecret = redactedValue
	putFullSettings(t, e, controller, s)

	got := controller.Settings.Load().Security.OAuthProviders
	require.Len(t, got, 1)
	assert.Equal(t, "new", got[0].ClientID)
	assert.Equal(t, testOAuthSecret, got[0].ClientSecret)
}
