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

// TestUpdateSectionSettingsRejectsSameIDOAuthProviders verifies PATCH rejects two
// entries with the same canonical ID, enabled or not, like PUT does.
func TestUpdateSectionSettingsRejectsSameIDOAuthProviders(t *testing.T) {
	tests := []struct {
		name           string
		secondDisabled bool
	}{
		{name: "both enabled"},
		{name: "second disabled", secondDisabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _, controller := setupTestEnvironment(t)
			controller.Settings.Load().Security.BaseURL = testOAuthBaseURL

			rec := sendSectionPatch(t, e, controller, "security", map[string]any{
				"oauthProviders": []any{
					oauthProviderPayload("google", nil),
					oauthProviderPayload("Google", map[string]any{"enabled": !tt.secondDisabled}),
				},
			})

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Empty(t, controller.Settings.Load().Security.OAuthProviders, "a rejected save must not change the stored providers")

			// The same payload with a different second provider is accepted, so the
			// duplicate ID is what the save was rejected for.
			patchSection(t, e, controller, "security", map[string]any{
				"oauthProviders": []any{
					oauthProviderPayload("google", nil),
					oauthProviderPayload("github", map[string]any{"enabled": !tt.secondDisabled}),
				},
			})
		})
	}
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

// sendFullSettings drives UpdateSettings (PUT) with a full snapshot and returns the
// response without asserting its status, for saves that are expected to be rejected.
func sendFullSettings(t *testing.T, e *echo.Echo, c *Controller, s *conf.Settings) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(s)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, c.UpdateSettings(e.NewContext(req, rec)))
	return rec
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
	rec := sendFullSettings(t, e, controller, s)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, controller.Settings.Load().Security.OAuthProviders, "a rejected save must not change the stored providers")

	// The same payload with a different second provider is accepted, so the
	// duplicate ID is what the save was rejected for.
	s.Security.OAuthProviders[1].Provider = "github"
	putFullSettings(t, e, controller, s)
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

// TestUpdateSettingsRestoresOAuthSecretByProviderID verifies a redacted secret is
// restored by provider ID when the list is reordered, and that an entry whose ID has
// no stored counterpart keeps the placeholder, so the save is rejected.
func TestUpdateSettingsRestoresOAuthSecretByProviderID(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	controller.Settings.Load().Security.BaseURL = testOAuthBaseURL
	controller.Settings.Load().Security.OAuthProviders = []conf.OAuthProviderConfig{
		{Provider: "google", Enabled: true, ClientID: "g", ClientSecret: testOAuthSecret, UserID: testOAuthUserAllowed},
		{Provider: "github", Enabled: true, ClientID: "h", ClientSecret: "github-secret", UserID: testOAuthUserAllowed},
	}

	s := conf.CloneSettings(controller.Settings.Load())
	s.Security.OAuthProviders = []conf.OAuthProviderConfig{
		{Provider: "github", Enabled: true, ClientID: "h", ClientSecret: redactedValue, UserID: testOAuthUserAllowed},
		{Provider: "google", Enabled: true, ClientID: "g", ClientSecret: redactedValue, UserID: testOAuthUserAllowed},
	}
	putFullSettings(t, e, controller, s)

	got := controller.Settings.Load().Security.OAuthProviders
	require.Len(t, got, 2)
	assert.Equal(t, "github-secret", got[0].ClientSecret)
	assert.Equal(t, testOAuthSecret, got[1].ClientSecret)

	s = conf.CloneSettings(controller.Settings.Load())
	s.Security.OAuthProviders = append(s.Security.OAuthProviders, conf.OAuthProviderConfig{
		Provider: "microsoft", Enabled: false, ClientID: "m", ClientSecret: redactedValue, UserID: testOAuthUserAllowed,
	})
	rec := sendFullSettings(t, e, controller, s)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
