package api

import (
	"maps"
	"testing"

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

// TestValidateOAuthProvidersArrayRejectsCaseDuplicate verifies two entries that
// differ only in case are reported as a duplicate, not as an invalid provider.
func TestValidateOAuthProvidersArrayRejectsCaseDuplicate(t *testing.T) {
	t.Parallel()

	err := validateOAuthProvidersArray(map[string]any{
		"oauthProviders": []any{
			oauthProviderPayload("oidc", nil),
			oauthProviderPayload("OIDC", nil),
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate provider")
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
