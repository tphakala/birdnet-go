package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const settingsSectionSecurity = "security"

// trustedProxiesFixture mixes a bare IPv4, an IPv4 CIDR, an IPv6 CIDR and the
// preset token in a deliberately unsorted order that must be stored verbatim.
var trustedProxiesFixture = []string{"172.18.0.1", "10.0.0.0/8", "2001:db8::/32", "Cloudflare"}

// getSecuritySection returns the decoded security section through the GET
// handler.
func getSecuritySection(t *testing.T, e *echo.Echo, c *Controller) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/"+settingsSectionSecurity, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues(settingsSectionSecurity)
	require.NoError(t, c.GetSectionSettings(ctx))
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

// TestSettingsRoundTripTrustedProxies pins that security.trustedProxies is
// saved and read back in order through PATCH, GET and a full PUT, and cleared
// by an empty list.
func TestSettingsRoundTripTrustedProxies(t *testing.T) {
	e, _, c := setupTestEnvironment(t)

	patchSection(t, e, c, settingsSectionSecurity, map[string]any{"trustedProxies": trustedProxiesFixture})
	assert.Equal(t, trustedProxiesFixture, c.Settings.Load().Security.TrustedProxies, "PATCH must store the list in order")

	got := getSecuritySection(t, e, c)
	assert.Equal(t, []any{"172.18.0.1", "10.0.0.0/8", "2001:db8::/32", "Cloudflare"}, got["trustedProxies"], "GET must return the stored list")

	putFullSettings(t, e, c, c.Settings.Load())
	assert.Equal(t, trustedProxiesFixture, c.Settings.Load().Security.TrustedProxies, "a full PUT must keep the list")

	patchSection(t, e, c, settingsSectionSecurity, map[string]any{"trustedProxies": []string{}})
	assert.Empty(t, c.Settings.Load().Security.TrustedProxies, "an empty list must clear it")
}

// TestSettingsRejectInvalidTrustedProxies pins that an invalid entry is
// rejected on both write paths, also while the subnet bypass is disabled, and
// leaves the stored list unchanged.
func TestSettingsRejectInvalidTrustedProxies(t *testing.T) {
	e, _, c := setupTestEnvironment(t)

	patchSection(t, e, c, settingsSectionSecurity, map[string]any{"trustedProxies": trustedProxiesFixture})
	require.False(t, c.Settings.Load().Security.AllowSubnetBypass.Enabled, "the bypass must be disabled for this test")

	rec := sendSectionPatch(t, e, c, settingsSectionSecurity, map[string]any{"trustedProxies": []string{"not-an-ip"}})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "PATCH with an invalid entry must be rejected")
	assert.Equal(t, trustedProxiesFixture, c.Settings.Load().Security.TrustedProxies, "a rejected PATCH must not change the list")

	invalid := *c.Settings.Load()
	invalid.Security.TrustedProxies = []string{"not-an-ip"}
	body, err := json.Marshal(&invalid)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	putRec := httptest.NewRecorder()
	require.NoError(t, c.UpdateSettings(e.NewContext(req, putRec)))
	assert.Equal(t, http.StatusBadRequest, putRec.Code, "PUT with an invalid entry must be rejected")
	assert.Equal(t, trustedProxiesFixture, c.Settings.Load().Security.TrustedProxies, "a rejected PUT must not change the list")
}
