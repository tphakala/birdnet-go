// auth_edge_cases_test.go: Edge case tests for password update handling
// Tests empty passwords, null values, and partial update payloads

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// TestEmptyPasswordUpdate tests various empty/null password scenarios
func TestEmptyPasswordUpdate(t *testing.T) {
	// Helper function to create a controller with hashed password
	createControllerWithPassword := func(t *testing.T, password string) (*Controller, string) {
		t.Helper()
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		require.NoError(t, err)
		
		settings := getTestSettings(t)
		settings.Security.BasicAuth.Enabled = true
		settings.Security.BasicAuth.ClientID = "testuser"
		settings.Security.BasicAuth.Password = string(hashedPassword)
		
		e := echo.New()
		controller := &Controller{
			Echo:                e,
			Settings:            settings,
			controlChan:         make(chan string, 10),
			DisableSaveSettings: true,
			logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
		}
		
		return controller, string(hashedPassword)
	}

	t.Run("Empty string password should preserve existing", func(t *testing.T) {
		controller, originalHash := createControllerWithPassword(t, "CurrentPassword123")
		
		// Send update with empty string password
		update := map[string]interface{}{
			"basicAuth": map[string]interface{}{
				"password": "",
			},
		}
		
		body, err := json.Marshal(update)
		require.NoError(t, err)
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := controller.Echo.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		err = controller.UpdateSectionSettings(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)
		
		// Password should remain unchanged
		assert.Equal(t, originalHash, controller.Settings.Security.BasicAuth.Password,
			"Empty string password should not change existing hash")
	})

	t.Run("Missing password field should preserve existing", func(t *testing.T) {
		controller, originalHash := createControllerWithPassword(t, "CurrentPassword456")
		
		// Send update WITHOUT password field at all
		update := map[string]interface{}{
			"basicAuth": map[string]interface{}{
				"enabled": true,
				// no password field
			},
		}
		
		body, err := json.Marshal(update)
		require.NoError(t, err)
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := controller.Echo.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		err = controller.UpdateSectionSettings(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)
		
		// Password should remain unchanged
		assert.Equal(t, originalHash, controller.Settings.Security.BasicAuth.Password,
			"Missing password field should preserve existing hash")
	})

	t.Run("Null password in JSON should preserve existing", func(t *testing.T) {
		controller, originalHash := createControllerWithPassword(t, "CurrentPassword789")
		
		// Send update with explicit null
		jsonStr := `{"basicAuth": {"password": null, "enabled": true}}`
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", strings.NewReader(jsonStr))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := controller.Echo.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		err := controller.UpdateSectionSettings(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)
		
		// Password should remain unchanged
		assert.Equal(t, originalHash, controller.Settings.Security.BasicAuth.Password,
			"Null password should preserve existing hash")
	})

	t.Run("Whitespace-only password should preserve existing", func(t *testing.T) {
		controller, _ := createControllerWithPassword(t, "CurrentPasswordXYZ")
		
		// Send update with whitespace-only password
		update := map[string]interface{}{
			"basicAuth": map[string]interface{}{
				"password": "   \t\n  ",
			},
		}
		
		body, err := json.Marshal(update)
		require.NoError(t, err)
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := controller.Echo.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		err = controller.UpdateSectionSettings(ctx)
		require.NoError(t, err)
		
		// Should get validation error for whitespace-only password
		assert.Equal(t, http.StatusBadRequest, rec.Code,
			"Whitespace-only password should be rejected")
		assert.Contains(t, rec.Body.String(), "password cannot be only whitespace")
	})

	t.Run("Update from plaintext to empty should preserve plaintext", func(t *testing.T) {
		// Start with plaintext password (legacy)
		settings := getTestSettings(t)
		settings.Security.BasicAuth.Enabled = true
		settings.Security.BasicAuth.ClientID = "legacyuser"
		settings.Security.BasicAuth.Password = "PlaintextPassword123" // Not hashed
		
		e := echo.New()
		controller := &Controller{
			Echo:                e,
			Settings:            settings,
			controlChan:         make(chan string, 10),
			DisableSaveSettings: true,
			logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
		}
		
		// Send update with empty password
		update := map[string]interface{}{
			"basicAuth": map[string]interface{}{
				"password": "",
			},
		}
		
		body, err := json.Marshal(update)
		require.NoError(t, err)
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		err = controller.UpdateSectionSettings(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)
		
		// Plaintext password should be preserved
		assert.Equal(t, "PlaintextPassword123", controller.Settings.Security.BasicAuth.Password,
			"Empty password update should preserve existing plaintext password")
	})
}

// TestPartialBasicAuthUpdate tests partial update payloads
func TestPartialBasicAuthUpdate(t *testing.T) {
	t.Run("Update only enabled flag preserves password", func(t *testing.T) {
		// Setup with hashed password
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte("SecurePass123"), bcrypt.DefaultCost)
		require.NoError(t, err)
		
		settings := getTestSettings(t)
		settings.Security.BasicAuth.Enabled = true
		settings.Security.BasicAuth.ClientID = "testuser"
		settings.Security.BasicAuth.ClientSecret = "testsecret"
		settings.Security.BasicAuth.Password = string(hashedPassword)
		
		e := echo.New()
		controller := &Controller{
			Echo:                e,
			Settings:            settings,
			controlChan:         make(chan string, 10),
			DisableSaveSettings: true,
			logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
		}
		
		// Update only enabled flag
		update := map[string]interface{}{
			"basicAuth": map[string]interface{}{
				"enabled": false,
				// No other fields
			},
		}
		
		body, err := json.Marshal(update)
		require.NoError(t, err)
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		err = controller.UpdateSectionSettings(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)
		
		// Check all fields
		assert.False(t, controller.Settings.Security.BasicAuth.Enabled, "Enabled should be updated")
		assert.Equal(t, string(hashedPassword), controller.Settings.Security.BasicAuth.Password,
			"Password hash should be preserved")
		assert.Equal(t, "testuser", controller.Settings.Security.BasicAuth.ClientID,
			"ClientID should be preserved")
		assert.Equal(t, "testsecret", controller.Settings.Security.BasicAuth.ClientSecret,
			"ClientSecret should be preserved")
	})

	t.Run("Update only clientId preserves password", func(t *testing.T) {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte("AnotherPass789"), bcrypt.DefaultCost)
		require.NoError(t, err)
		
		settings := getTestSettings(t)
		settings.Security.BasicAuth.Enabled = true
		settings.Security.BasicAuth.ClientID = "olduser"
		settings.Security.BasicAuth.Password = string(hashedPassword)
		
		e := echo.New()
		controller := &Controller{
			Echo:                e,
			Settings:            settings,
			controlChan:         make(chan string, 10),
			DisableSaveSettings: true,
			logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
		}
		
		// Update only clientId
		update := map[string]interface{}{
			"basicAuth": map[string]interface{}{
				"clientId": "newuser",
			},
		}
		
		body, err := json.Marshal(update)
		require.NoError(t, err)
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		err = controller.UpdateSectionSettings(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)
		
		// Verify updates
		assert.Equal(t, "newuser", controller.Settings.Security.BasicAuth.ClientID,
			"ClientID should be updated")
		assert.Equal(t, string(hashedPassword), controller.Settings.Security.BasicAuth.Password,
			"Password hash should be preserved")
		assert.True(t, controller.Settings.Security.BasicAuth.Enabled,
			"Enabled should be preserved")
	})

	t.Run("Empty basicAuth object preserves all fields", func(t *testing.T) {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte("TestPass456"), bcrypt.DefaultCost)
		require.NoError(t, err)
		
		settings := getTestSettings(t)
		settings.Security.BasicAuth.Enabled = true
		settings.Security.BasicAuth.ClientID = "testuser"
		settings.Security.BasicAuth.ClientSecret = "secret123"
		settings.Security.BasicAuth.Password = string(hashedPassword)
		
		e := echo.New()
		controller := &Controller{
			Echo:                e,
			Settings:            settings,
			controlChan:         make(chan string, 10),
			DisableSaveSettings: true,
			logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
		}
		
		// Send empty basicAuth object
		update := map[string]interface{}{
			"basicAuth": map[string]interface{}{
				// Completely empty
			},
		}
		
		body, err := json.Marshal(update)
		require.NoError(t, err)
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		err = controller.UpdateSectionSettings(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)
		
		// All fields should be preserved
		assert.True(t, controller.Settings.Security.BasicAuth.Enabled)
		assert.Equal(t, "testuser", controller.Settings.Security.BasicAuth.ClientID)
		assert.Equal(t, "secret123", controller.Settings.Security.BasicAuth.ClientSecret)
		assert.Equal(t, string(hashedPassword), controller.Settings.Security.BasicAuth.Password)
	})

	t.Run("Update other security settings preserves basicAuth", func(t *testing.T) {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte("KeepThisPass"), bcrypt.DefaultCost)
		require.NoError(t, err)
		
		settings := getTestSettings(t)
		settings.Security.BasicAuth.Enabled = true
		settings.Security.BasicAuth.ClientID = "keepuser"
		settings.Security.BasicAuth.Password = string(hashedPassword)
		settings.Security.GoogleAuth.Enabled = false
		
		e := echo.New()
		controller := &Controller{
			Echo:                e,
			Settings:            settings,
			controlChan:         make(chan string, 10),
			DisableSaveSettings: true,
			logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
		}
		
		// Update only Google auth settings
		update := map[string]interface{}{
			"googleAuth": map[string]interface{}{
				"enabled":  true,
				"clientId": "google-client-123",
				"clientSecret": "google-secret-123", // Need both for validation to pass
			},
			// No basicAuth in payload
		}
		
		body, err := json.Marshal(update)
		require.NoError(t, err)
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		err = controller.UpdateSectionSettings(ctx)
		if err != nil || rec.Code != http.StatusOK {
			t.Logf("Update failed: status=%d, body=%s, error=%v", rec.Code, rec.Body.String(), err)
		}
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)
		
		// BasicAuth should be completely unchanged
		assert.True(t, controller.Settings.Security.BasicAuth.Enabled)
		assert.Equal(t, "keepuser", controller.Settings.Security.BasicAuth.ClientID)
		assert.Equal(t, string(hashedPassword), controller.Settings.Security.BasicAuth.Password)
		
		// GoogleAuth should be updated
		assert.True(t, controller.Settings.Security.GoogleAuth.Enabled)
		assert.Equal(t, "google-client-123", controller.Settings.Security.GoogleAuth.ClientID)
	})

	t.Run("Changing password while disabled still hashes", func(t *testing.T) {
		settings := getTestSettings(t)
		settings.Security.BasicAuth.Enabled = false // Start disabled
		settings.Security.BasicAuth.Password = "oldplaintext"
		
		e := echo.New()
		controller := &Controller{
			Echo:                e,
			Settings:            settings,
			controlChan:         make(chan string, 10),
			DisableSaveSettings: true,
			logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
		}
		
		// Update password while auth is disabled
		update := map[string]interface{}{
			"basicAuth": map[string]interface{}{
				"password": "NewHashedPass123",
				// Note: not changing enabled flag
			},
		}
		
		body, err := json.Marshal(update)
		require.NoError(t, err)
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		err = controller.UpdateSectionSettings(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)
		
		// Password should be hashed even though auth is disabled
		assert.False(t, controller.Settings.Security.BasicAuth.Enabled,
			"Enabled flag should remain false")
		assert.True(t, strings.HasPrefix(controller.Settings.Security.BasicAuth.Password, "$2"),
			"Password should be hashed even when basicAuth is disabled")
		
		// Verify the hash is valid
		err = bcrypt.CompareHashAndPassword(
			[]byte(controller.Settings.Security.BasicAuth.Password),
			[]byte("NewHashedPass123"))
		require.NoError(t, err, "Hash should be valid")
	})
}

// TestPasswordUpdateWithComplexJSON tests complex/nested JSON scenarios
func TestPasswordUpdateWithComplexJSON(t *testing.T) {
	t.Run("Deeply nested null values", func(t *testing.T) {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte("KeepMe123"), bcrypt.DefaultCost)
		require.NoError(t, err)
		
		settings := getTestSettings(t)
		settings.Security.BasicAuth.Enabled = true
		settings.Security.BasicAuth.Password = string(hashedPassword)
		settings.Security.BasicAuth.ClientID = "user1"
		
		e := echo.New()
		controller := &Controller{
			Echo:                e,
			Settings:            settings,
			controlChan:         make(chan string, 10),
			DisableSaveSettings: true,
			logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
		}
		
		// Complex JSON with various null/empty combinations
		jsonStr := `{
			"basicAuth": {
				"enabled": true,
				"clientId": null,
				"clientSecret": "",
				"password": null
			},
			"googleAuth": null,
			"allowSubnetBypass": {}
		}`
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", strings.NewReader(jsonStr))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		err = controller.UpdateSectionSettings(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)
		
		// Password should be preserved
		assert.Equal(t, string(hashedPassword), controller.Settings.Security.BasicAuth.Password,
			"Password should be preserved with null value")
		// ClientID might be cleared by null (depending on implementation)
		// ClientSecret might be cleared by empty string
		assert.True(t, controller.Settings.Security.BasicAuth.Enabled)
	})

	t.Run("Array or invalid type for password field", func(t *testing.T) {
		settings := getTestSettings(t)
		settings.Security.BasicAuth.Enabled = true
		settings.Security.BasicAuth.Password = "keepthis"
		
		e := echo.New()
		controller := &Controller{
			Echo:                e,
			Settings:            settings,
			controlChan:         make(chan string, 10),
			DisableSaveSettings: true,
			logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
		}
		
		// Send array instead of string for password
		jsonStr := `{
			"basicAuth": {
				"password": ["not", "a", "string"]
			}
		}`
		
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", strings.NewReader(jsonStr))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("security")
		
		_ = controller.UpdateSectionSettings(ctx)
		// Should get an error for invalid type
		assert.Equal(t, http.StatusBadRequest, rec.Code,
			"Should reject array value for password field")
	})
}