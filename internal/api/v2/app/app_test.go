// app_test.go: Package app provides tests for the app config endpoint.
// This file tests /api/v2/app/config which returns security configuration,
// CSRF tokens, and version information to the frontend SPA.

package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/branding"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/security/securitytest"
	"github.com/tphakala/birdnet-go/internal/speciesdict"
)

// =============================================================================
// Test Setup Helpers
// =============================================================================

// newAppMockDS builds the default mock datastore for app config tests. The
// PruneAppEvents expectation is Maybe because the apicore startup goroutine
// (launched by apitest.NewCore) may or may not call it before the test ends.
func newAppMockDS(t *testing.T) *mocks.MockInterface {
	t.Helper()
	mockDS := mocks.NewMockInterface(t)
	mockDS.EXPECT().PruneAppEvents(mock.Anything, mock.Anything).Return(int64(0), nil).Maybe()
	return mockDS
}

// newAppHandler builds an app Handler around a fully wired *apicore.Core for the
// given settings (no auth/notification service injected). apitest.NewCore
// publishes the settings to the process-global snapshot (restored on cleanup) so
// GetAppConfig's CurrentSettings() observes them.
func newAppHandler(t *testing.T, e *echo.Echo, settings *conf.Settings) *Handler {
	t.Helper()
	core := apitest.NewCore(t,
		apitest.WithEcho(e),
		apitest.WithDatastore(newAppMockDS(t)),
		apitest.WithSettings(settings),
	)
	return New(core, nil, nil)
}

// setupAppConfigTest creates a test environment for app config tests.
func setupAppConfigTest(t *testing.T, securityConfig *conf.Security) (*echo.Echo, *Handler) {
	t.Helper()

	e := echo.New()

	// Use empty security config if nil
	secCfg := conf.Security{}
	if securityConfig != nil {
		secCfg = *securityConfig
	}

	settings := &conf.Settings{
		Version: "1.0.0-test",
		WebServer: conf.WebServerSettings{
			Debug: true,
		},
		Security: secCfg,
	}

	return e, newAppHandler(t, e, settings)
}

// setupAppConfigTestWithAuth creates a test environment with full auth support.
func setupAppConfigTestWithAuth(t *testing.T, securityConfig *conf.Security) (*echo.Echo, *Handler) {
	t.Helper()

	e := echo.New()

	// Use empty security config if nil
	secCfg := conf.Security{}
	if securityConfig != nil {
		secCfg = *securityConfig
	}

	settings := &conf.Settings{
		Version: "1.0.0-test",
		WebServer: conf.WebServerSettings{
			Debug: true,
		},
		Security: secCfg,
	}

	// Create OAuth2Server for auth before publishing settings so any session
	// secret it defaults is captured in the published snapshot. Initialize the
	// gothic session store the same way the monolithic setup did.
	oauth2Server := securitytest.NewOAuth2ServerForTesting(t, settings)
	authService := auth.NewSecurityAdapter(oauth2Server)
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))

	core := apitest.NewCore(t,
		apitest.WithEcho(e),
		apitest.WithDatastore(newAppMockDS(t)),
		apitest.WithSettings(settings),
	)

	return e, New(core, authService, nil)
}

// =============================================================================
// Integration Tests
// =============================================================================

// TestGetAppConfig_NoSecurity tests the endpoint when no security is configured.
func TestGetAppConfig_NoSecurity(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// When no security is configured, accessAllowed should be true
	assert.False(t, response.Security.Enabled, "Security should be disabled")
	assert.True(t, response.Security.AccessAllowed, "Access should be allowed when security is disabled")
	assert.Equal(t, "1.0.0-test", response.Version)

	// All auth methods should be disabled
	assert.False(t, response.Security.AuthConfig.BasicEnabled)
	assert.Empty(t, response.Security.AuthConfig.EnabledProviders, "No OAuth providers should be enabled")
}

// TestGetAppConfig_AudioExportEnabled verifies the response surfaces
// Realtime.Audio.Export.Enabled so the frontend can hide per-detection
// spectrogram/audio UI when clip export is disabled.
func TestGetAppConfig_AudioExportEnabled(t *testing.T) {
	tests := []struct {
		name          string
		exportEnabled bool
	}{
		{name: "export enabled", exportEnabled: true},
		{name: "export disabled", exportEnabled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			settings := &conf.Settings{
				Version: "1.0.0-test",
			}
			settings.Realtime.Audio.Export.Enabled = tt.exportEnabled
			controller := newAppHandler(t, e, settings)

			req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/app/config")

			err := controller.GetAppConfig(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)

			var response AppConfigResponse
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.Equal(t, tt.exportEnabled, response.AudioExportEnabled,
				"AudioExportEnabled should mirror Realtime.Audio.Export.Enabled")

			// Also assert against the raw JSON payload, pinning the full wire
			// contract independent of the struct's json tag. The typed unmarshal
			// above cannot distinguish a present-but-false value from an omitted
			// key (both decode to false), so an accidental `omitempty` on the field
			// would silently drop the key when export is disabled and leave the
			// frontend guessing. Comparing the raw value catches both an omitted
			// key (decodes to nil, != false) and a wrong value.
			var raw map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			require.Contains(t, raw, "audioExportEnabled",
				"response must always include the audioExportEnabled key, even when false")
			assert.Equal(t, tt.exportEnabled, raw["audioExportEnabled"],
				"raw audioExportEnabled must carry the correct boolean value")
		})
	}
}

// TestGetAppConfig_ProjectLinks verifies the response always carries the
// project identity/links (independent of telemetry), defaulting to the upstream
// values with correctly derived issue URLs when nothing is overridden.
func TestGetAppConfig_ProjectLinks(t *testing.T) {
	// Branding resolution gives BIRDNET_GO_PROJECT_* env vars highest precedence;
	// clear them so this default-links assertion is deterministic regardless of
	// the caller's environment.
	for _, key := range []string{
		"BIRDNET_GO_PROJECT_NAME", "BIRDNET_GO_PROJECT_REPO_URL", "BIRDNET_GO_PROJECT_ISSUES_URL",
		"BIRDNET_GO_PROJECT_NEW_ISSUE_URL", "BIRDNET_GO_PROJECT_SUPPORT_URL",
		"BIRDNET_GO_PROJECT_DISCUSSIONS_URL", "BIRDNET_GO_PROJECT_RELEASES_URL",
		"BIRDNET_GO_PROJECT_COMMUNITY_URL",
	} {
		t.Setenv(key, "")
	}

	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Assert the endpoint serves exactly what the branding package resolves
	// (the exact default values are locked in branding's own TestDefaults). With
	// the env cleared above and no ldflags baked into the test binary, these
	// resolve to the upstream defaults; asserting against the getters also keeps
	// the test correct for ldflags-built binaries, which t.Setenv cannot clear.
	links := response.ProjectLinks
	assert.Equal(t, branding.Name(), links.Name)
	assert.Equal(t, branding.RepoURL(), links.RepoURL)
	assert.Equal(t, branding.IssuesURL(), links.IssuesURL)
	assert.Equal(t, branding.NewIssueURL(), links.NewIssueURL)
	assert.Equal(t, branding.SupportURL(), links.SupportURL)
	assert.Equal(t, branding.DiscussionsURL(), links.DiscussionsURL)
	assert.Equal(t, branding.ReleasesURL(), links.ReleasesURL)
	assert.Equal(t, branding.CommunityURL(), links.CommunityURL)
}

// requestAppConfig sends GET /api/v2/app/config to h and returns the recorder
// once the status is known to be 200.
func requestAppConfig(t *testing.T, e *echo.Echo, h *Handler) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	require.NoError(t, h.GetAppConfig(c))
	require.Equal(t, http.StatusOK, rec.Code)
	return rec
}

// securityTestHost is the configured security host. It must never be served. It
// is a reserved .invalid name so that no project link, which a deployment may
// override, can contain it.
const securityTestHost = "security-host.invalid"

// enabledOAuthProviders builds enabled OAuth provider settings with credentials
// for the given provider IDs.
func enabledOAuthProviders(ids ...string) []conf.OAuthProviderConfig {
	providers := make([]conf.OAuthProviderConfig, 0, len(ids))
	for _, id := range ids {
		providers = append(providers, conf.OAuthProviderConfig{
			Provider:     id,
			Enabled:      true,
			ClientID:     id + "-client-id",
			ClientSecret: id + "-secret",
		})
	}
	return providers
}

// TestGetAppConfig_SecurityConfig tests that the security block follows the
// configured authentication methods: security is enabled when any method is
// configured, basic auth and each OAuth provider are reported on their own, and
// the configured security host is never served. These values come from the
// settings alone, so no auth service is wired.
func TestGetAppConfig_SecurityConfig(t *testing.T) {
	tests := []struct {
		name      string
		basic     bool
		providers []string
	}{
		{name: "nothing configured"},
		{name: "basic auth only", basic: true},
		{name: "google only", providers: []string{security.ConfigGoogle}},
		{name: "github only", providers: []string{security.ConfigGitHub}},
		{name: "microsoft only", providers: []string{security.ConfigMicrosoft}},
		{name: "basic auth and every provider", basic: true, providers: []string{security.ConfigGoogle, security.ConfigGitHub, security.ConfigMicrosoft}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: setupAppConfigTest publishes settings to the
			// process-global snapshot that GetAppConfig reads.
			e, controller := setupAppConfigTest(t, &conf.Security{
				Host:           securityTestHost,
				BasicAuth:      conf.BasicAuth{Enabled: tt.basic},
				OAuthProviders: enabledOAuthProviders(tt.providers...),
			})

			rec := requestAppConfig(t, e, controller)

			var response AppConfigResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))

			assert.Equal(t, tt.basic || len(tt.providers) > 0, response.Security.Enabled)
			assert.Equal(t, tt.basic, response.Security.AuthConfig.BasicEnabled)
			assert.ElementsMatch(t, tt.providers, response.Security.AuthConfig.EnabledProviders)
			assert.NotContains(t, rec.Body.String(), securityTestHost, "the security host must not be served")
			if len(tt.providers) == 0 {
				// A typed decode cannot tell an empty array from null, and the
				// frontend iterates this field.
				assert.Contains(t, rec.Body.String(), `"enabledProviders":[]`)
			}
		})
	}
}

// TestGetAppConfig_CSRFTokenFromContext tests CSRF token extraction from context.
func TestGetAppConfig_CSRFTokenFromContext(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	// Simulate CSRF middleware setting the token
	expectedToken := "test-csrf-token-abc123"
	c.Set("csrf", expectedToken)

	err := controller.GetAppConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, expectedToken, response.CSRFToken, "CSRF token should match context value")
}

// TestGetAppConfig_NoCSRFToken tests that EnsureCSRFToken generates a token
// when none exists in context or cookie (Echo v4.15.0 compatibility fix).
func TestGetAppConfig_NoCSRFToken(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")
	// Don't set csrf in context - EnsureCSRFToken will generate one

	err := controller.GetAppConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// EnsureCSRFToken now generates a token when none exists
	assert.NotEmpty(t, response.CSRFToken, "CSRF token should be generated when not in context")

	// Verify the token was also set as a cookie
	cookies := rec.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "csrf" {
			csrfCookie = c
			break
		}
	}
	require.NotNil(t, csrfCookie, "CSRF cookie should be set")
	assert.Equal(t, response.CSRFToken, csrfCookie.Value, "Cookie and response token should match")
}

// TestGetAppConfig_ResponseShape pins the wire shape of the response: the JSON
// content type, the exact key set at each level (a renamed or extra key fails)
// and the species dictionary version. The keys tagged omitempty on
// AppConfigResponse are not part of the default response.
func TestGetAppConfig_ResponseShape(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	rec := requestAppConfig(t, e, controller)

	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json", "Response should be JSON")

	var raw map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw), "Response should be valid JSON")

	keys := func(m map[string]any) []string { return slices.Collect(maps.Keys(m)) }
	object := func(m map[string]any, key string) map[string]any {
		t.Helper()
		sub, ok := m[key].(map[string]any)
		require.True(t, ok, "%s should be an object", key)
		return sub
	}

	assert.ElementsMatch(t, []string{
		"csrfToken", "security", "version", "speciesDictVersion", "basePath",
		"liveSpectrogram", "audioExportEnabled", "freshInstall", "newVersion", "projectLinks",
	}, keys(raw))

	securityObj := object(raw, "security")
	assert.ElementsMatch(t, []string{"enabled", "accessAllowed", "authConfig", "publicAccess", "privateMode"}, keys(securityObj))
	assert.ElementsMatch(t, []string{"basicEnabled", "enabledProviders"}, keys(object(securityObj, "authConfig")))
	assert.ElementsMatch(t, []string{"liveAudio"}, keys(object(securityObj, "publicAccess")))
	assert.ElementsMatch(t, []string{
		"name", "repoUrl", "issuesUrl", "newIssueUrl", "supportUrl", "discussionsUrl", "releasesUrl", "communityUrl",
	}, keys(object(raw, "projectLinks")))

	var response AppConfigResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.NotEmpty(t, response.SpeciesDictVersion, "speciesDictVersion must not be empty")
	assert.Equal(t, speciesdict.Version(), response.SpeciesDictVersion, "speciesDictVersion must match speciesdict.Version()")
}

// =============================================================================
// Security Tests
// =============================================================================

// TestGetAppConfig_NoSensitiveDataExposed ensures passwords/secrets are not leaked.
func TestGetAppConfig_NoSensitiveDataExposed(t *testing.T) {
	securityConfig := conf.Security{
		SessionSecret: "super-secret-session-key-do-not-leak",
		BasicAuth: conf.BasicAuth{
			Enabled:        true,
			Password:       "super-secret-password-do-not-leak",
			ClientID:       "test-client",
			AuthCodeExp:    5 * time.Minute,
			AccessTokenExp: 24 * time.Hour,
		},
		GoogleAuth: conf.SocialProvider{
			Enabled:      true,
			ClientID:     "google-client-id",
			ClientSecret: "google-super-secret-do-not-leak",
		},
	}

	e, controller := setupAppConfigTestWithAuth(t, &securityConfig)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err)

	body := rec.Body.String()

	// Ensure no secrets are exposed
	assert.NotContains(t, body, "super-secret-session-key-do-not-leak", "Session secret should not be exposed")
	assert.NotContains(t, body, "super-secret-password-do-not-leak", "Password should not be exposed")
	assert.NotContains(t, body, "google-super-secret-do-not-leak", "OAuth secrets should not be exposed")
	assert.NotContains(t, body, "google-client-id", "OAuth client IDs should not be exposed")
	assert.NotContains(t, body, "ClientSecret", "ClientSecret field should not exist")
	assert.NotContains(t, body, "Password", "Password field should not exist")
}

// TestGetAppConfig_UnusableCSRFContextValueIsNotServed tests that a CSRF
// context value that cannot be used as a token (a non-string, or an empty
// string) is not served as the token, and that a non-empty token is returned.
func TestGetAppConfig_UnusableCSRFContextValueIsNotServed(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "non-string value", value: 12345},
		{name: "empty string", value: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller := setupAppConfigTest(t, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/app/config")
			c.Set("csrf", tt.value)

			require.NoError(t, controller.GetAppConfig(c))
			assert.Equal(t, http.StatusOK, rec.Code)

			var response AppConfigResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))

			assert.NotEmpty(t, response.CSRFToken, "a token should still be returned")
			assert.NotEqual(t, fmt.Sprint(tt.value), response.CSRFToken, "the unusable context value must not be served")
		})
	}
}

// TestGetAppConfig_NilAuthService tests behavior when auth service is nil.
func TestGetAppConfig_NilAuthService(t *testing.T) {
	// Create controller without auth service
	securityConfig := conf.Security{
		BasicAuth: conf.BasicAuth{
			Enabled:  true,
			Password: "test",
		},
	}

	e, controller := setupAppConfigTest(t, &securityConfig)
	// Note: setupAppConfigTest doesn't set authService, so it's nil

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err, "Should handle nil auth service gracefully")
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// With nil auth service, should fail closed (deny access)
	assert.True(t, response.Security.Enabled)
	assert.False(t, response.Security.AccessAllowed, "Should deny access when auth service is nil")
}

// =============================================================================
// determineAccessAllowed Tests
// =============================================================================

// TestDetermineAccessAllowed_SecurityDisabled tests access when security is off.
func TestDetermineAccessAllowed_SecurityDisabled(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	allowed := controller.determineAccessAllowed(c, false)
	assert.True(t, allowed, "Should allow access when security is disabled")
}

// TestDetermineAccessAllowed_SecurityEnabledNoAuthService tests fail-closed behavior.
func TestDetermineAccessAllowed_SecurityEnabledNoAuthService(t *testing.T) {
	e, controller := setupAppConfigTest(t, &conf.Security{
		BasicAuth: conf.BasicAuth{
			Enabled:  true,
			Password: "test",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	allowed := controller.determineAccessAllowed(c, true)
	assert.False(t, allowed, "Should deny access when auth service is nil (fail closed)")
}

// TestGetAppConfig_SettingFlags tests that the public live audio and live
// spectrogram flags follow their settings, in both directions.
func TestGetAppConfig_SettingFlags(t *testing.T) {
	flags := []struct {
		name string
		set  func(s *conf.Settings, v bool)
		got  func(r *AppConfigResponse) bool
	}{
		{
			name: "public live audio",
			set:  func(s *conf.Settings, v bool) { s.Security.PublicAccess.LiveAudio = v },
			got:  func(r *AppConfigResponse) bool { return r.Security.PublicAccess.LiveAudio },
		},
		{
			name: "live spectrogram",
			set:  func(s *conf.Settings, v bool) { s.Realtime.Dashboard.LiveSpectrogram = v },
			got:  func(r *AppConfigResponse) bool { return r.LiveSpectrogram },
		},
	}

	for _, flag := range flags {
		for _, value := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s %t", flag.name, value), func(t *testing.T) {
				// Not parallel: setupAppConfigTest publishes settings to the
				// process-global snapshot that GetAppConfig reads via currentSettings.
				e, controller := setupAppConfigTest(t, nil)
				flag.set(controller.Settings.Load(), value)

				rec := requestAppConfig(t, e, controller)

				var response AppConfigResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
				assert.Equal(t, value, flag.got(&response))
			})
		}
	}
}

// TestGetAppConfig_SentryConfigWhenEnabled tests that Sentry config is included when telemetry is enabled.
func TestGetAppConfig_SentryConfigWhenEnabled(t *testing.T) {
	// Not parallel: publishes settings to the process-global snapshot that
	// GetAppConfig reads via currentSettings.
	_, controller := setupAppConfigTest(t, nil)

	// The Sentry DSN is no longer hardcoded; it resolves from the
	// BIRDNET_GO_SENTRY_DSN env var (or an ldflags-baked value). Inject one so
	// the frontend config is populated, mirroring an official build with a
	// baked-in DSN. Without it, from-source/test builds resolve an empty DSN
	// and the Sentry block is intentionally omitted.
	t.Setenv("BIRDNET_GO_SENTRY_DSN", "https://dummy@example.ingest.sentry.io/1")

	// Enable Sentry in settings
	controller.Settings.Load().Sentry.Enabled = true
	controller.Settings.Load().SystemID = "test-system-id-123"

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetAppConfig(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	require.NotNil(t, response.Sentry, "Sentry config should be present when enabled")
	assert.True(t, response.Sentry.Enabled)
	assert.NotEmpty(t, response.Sentry.DSN, "DSN should not be empty")
	assert.Equal(t, "test-system-id-123", response.Sentry.SystemID)
}

// TestGetAppConfig_SentryConfigOmittedForOldBuild tests that Sentry config is omitted
// for a build past its telemetry age even when telemetry is enabled.
func TestGetAppConfig_SentryConfigOmittedForOldBuild(t *testing.T) {
	// Not parallel: publishes settings to the process-global snapshot that
	// GetAppConfig reads via currentSettings.
	_, controller := setupAppConfigTest(t, nil)

	t.Setenv("BIRDNET_GO_SENTRY_DSN", "https://dummy@example.ingest.sentry.io/1")

	controller.Settings.Load().Sentry.Enabled = true
	controller.Settings.Load().BuildDate = time.Now().AddDate(-1, 0, 0).Format(time.RFC3339)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, controller.GetAppConfig(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Nil(t, response.Sentry, "Sentry config must be omitted for an expired build")
}

// TestGetAppConfig_SentryConfigWhenDisabled tests that Sentry config is omitted when telemetry is disabled.
func TestGetAppConfig_SentryConfigWhenDisabled(t *testing.T) {
	// Not parallel: publishes settings to the process-global snapshot that
	// GetAppConfig reads via currentSettings.
	_, controller := setupAppConfigTest(t, nil)

	// A usable DSN and a current build, so the only reason to omit the config
	// is that telemetry is off.
	t.Setenv("BIRDNET_GO_SENTRY_DSN", "https://dummy@example.ingest.sentry.io/1")
	controller.Settings.Load().Sentry.Enabled = false

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetAppConfig(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Nil(t, response.Sentry, "Sentry config should be nil when disabled")
}

// allowAllAuth is an auth.Service stub that reports every request as
// authenticated. The embedded nil interface is never dereferenced because
// GetAppConfig calls only IsAuthenticated.
type allowAllAuth struct{ auth.Service }

func (allowAllAuth) IsAuthenticated(echo.Context) bool { return true }

// TestGetAppConfig_WizardStateRequiresAccess pins that wizard state is not
// reported to a visitor without access.
func TestGetAppConfig_WizardStateRequiresAccess(t *testing.T) {
	authConfig := &conf.Security{
		BasicAuth: conf.BasicAuth{Enabled: true, Password: "test"},
	}
	onboardingPending := map[string]string{appMetadataKeyOnboardingPending: onboardingPendingValue}
	lastSeenOld := map[string]string{appMetadataKeyLastSeenVersion: "0.9.0"}

	tests := []struct {
		name            string
		security        *conf.Security
		authSvc         auth.Service
		metadata        map[string]string
		wantFresh       bool
		wantNewVersion  bool
		wantPrevVersion string
	}{
		{name: "visitor without access is not offered onboarding", security: authConfig, metadata: onboardingPending},
		{name: "visitor with access is offered onboarding", security: authConfig, authSvc: allowAllAuth{}, metadata: onboardingPending, wantFresh: true},
		{name: "visitor without access is not offered what's new", security: authConfig, metadata: lastSeenOld},
		{name: "visitor with access is offered what's new", security: authConfig, authSvc: allowAllAuth{}, metadata: lastSeenOld, wantNewVersion: true, wantPrevVersion: "0.9.0"},
		{name: "onboarding without auth configured is unchanged", metadata: onboardingPending, wantFresh: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller := setupAppConfigTest(t, tt.security)
			controller.authService = tt.authSvc
			repo := newMockAppMetadataRepo()
			maps.Copy(repo.store, tt.metadata)
			controller.appMetadataRepo = repo

			req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/app/config")

			require.NoError(t, controller.GetAppConfig(c))
			require.Equal(t, http.StatusOK, rec.Code)

			var response AppConfigResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			// Precondition: each row grants or denies access as its name says.
			require.Equal(t, tt.security == nil || tt.authSvc != nil, response.Security.AccessAllowed)
			assert.Equal(t, tt.wantFresh, response.FreshInstall)
			assert.Equal(t, tt.wantNewVersion, response.NewVersion)
			assert.Equal(t, tt.wantPrevVersion, response.PreviousVersion)
		})
	}
}
