// auth_integration_test.go: End-to-end integration test for password hashing flow
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

// TestPasswordHashingEndToEnd tests the complete flow:
// 1. User submits new password via settings API
// 2. Password is hashed before storage
// 3. Hashed password can be used for authentication
func TestPasswordHashingEndToEnd(t *testing.T) {
	// Setup initial settings with a plaintext password
	initialSettings := getTestSettings(t)
	initialSettings.Security.BasicAuth.Enabled = true
	initialSettings.Security.BasicAuth.Password = "oldPassword123"
	initialSettings.Security.BasicAuth.ClientID = "testuser"
	
	// Create controller
	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
		logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
	}
	
	// Step 1: Submit new password via API
	newPassword := "MyNewSecurePassword456!"
	updateRequest := map[string]interface{}{
		"basicAuth": map[string]interface{}{
			"enabled":  true,
			"password": newPassword,
		},
	}
	
	body, err := json.Marshal(updateRequest)
	require.NoError(t, err)
	
	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/security", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("security")
	
	// Execute the update
	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err, "Should successfully update security settings")
	assert.Equal(t, http.StatusOK, rec.Code)
	
	// Step 2: Verify password was hashed
	storedPassword := controller.Settings.Security.BasicAuth.Password
	assert.True(t, strings.HasPrefix(storedPassword, "$2"), "Password should be hashed with bcrypt")
	
	// Verify it's a valid bcrypt hash
	err = bcrypt.CompareHashAndPassword([]byte(storedPassword), []byte(newPassword))
	require.NoError(t, err, "Stored hash should match the new password")
	
	// Step 3: Verify old password no longer works
	err = bcrypt.CompareHashAndPassword([]byte(storedPassword), []byte("oldPassword123"))
	require.Error(t, err, "Old password should not match new hash")
	
	// Verify the response contains success message
	var response map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Contains(t, response["message"], "security settings updated successfully")
	
	// Additional verification: Ensure other security settings weren't affected
	assert.Equal(t, "testuser", controller.Settings.Security.BasicAuth.ClientID)
	assert.True(t, controller.Settings.Security.BasicAuth.Enabled)
}

// TestPasswordValidationIntegration tests password validation rules via the API
func TestPasswordValidationIntegration(t *testing.T) {
	testCases := []struct {
		name          string
		password      string
		expectedCode  int
		expectedError string
	}{
		{
			name:         "Valid 12 character password",
			password:     "ValidPass123",
			expectedCode: http.StatusOK,
		},
		{
			name:          "Too short (5 chars)",
			password:      "short",
			expectedCode:  http.StatusBadRequest,
			expectedError: "password must be at least 8 characters",
		},
		{
			name:          "Too long (73 chars)",
			password:      strings.Repeat("a", 73),
			expectedCode:  http.StatusBadRequest,
			expectedError: "password must not exceed 72 characters",
		},
		{
			name:         "Minimum length (8 chars)",
			password:     "12345678",
			expectedCode: http.StatusOK,
		},
		{
			name:         "Maximum length (72 chars)",
			password:     strings.Repeat("x", 72),
			expectedCode: http.StatusOK,
		},
		{
			name:         "Special characters and spaces",
			password:     "P@ss w0rd!#$",
			expectedCode: http.StatusOK,
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			settings := getTestSettings(t)
			settings.Security.BasicAuth.Enabled = true
			
			e := echo.New()
			controller := &Controller{
				Echo:                e,
				Settings:            settings,
				controlChan:         make(chan string, 10),
				DisableSaveSettings: true,
				logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
			}
			
			// Create request
			update := map[string]interface{}{
				"basicAuth": map[string]interface{}{
					"enabled":  true,
					"password": tc.password,
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
			
			// Execute
			_ = controller.UpdateSectionSettings(ctx)
			
			// Verify
			assert.Equal(t, tc.expectedCode, rec.Code, 
				"Expected status %d but got %d for password: %s", 
				tc.expectedCode, rec.Code, tc.password)
			
			if tc.expectedError != "" {
				assert.Contains(t, rec.Body.String(), tc.expectedError)
			}
			
			// If successful, verify password was hashed
			if tc.expectedCode == http.StatusOK {
				storedPassword := controller.Settings.Security.BasicAuth.Password
				assert.True(t, strings.HasPrefix(storedPassword, "$2"), 
					"Password should be hashed for: %s", tc.name)
			}
		})
	}
}

// TestMixedPasswordFormats tests handling of both hashed and plaintext passwords
func TestMixedPasswordFormats(t *testing.T) {
	t.Run("Update already hashed password", func(t *testing.T) {
		// Start with a pre-hashed password
		existingPassword := "ExistingPass123"
		existingHash, err := bcrypt.GenerateFromPassword([]byte(existingPassword), bcrypt.DefaultCost)
		require.NoError(t, err)
		
		settings := getTestSettings(t)
		settings.Security.BasicAuth.Enabled = true
		settings.Security.BasicAuth.Password = string(existingHash)
		
		e := echo.New()
		controller := &Controller{
			Echo:                e,
			Settings:            settings,
			controlChan:         make(chan string, 10),
			DisableSaveSettings: true,
			logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
		}
		
		// Try to update with the same hashed password (shouldn't re-hash)
		update := map[string]interface{}{
			"basicAuth": map[string]interface{}{
				"password": string(existingHash),
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
		
		// Password should remain the same hash (not double-hashed)
		assert.Equal(t, string(existingHash), controller.Settings.Security.BasicAuth.Password,
			"Already hashed password should not be re-hashed")
	})
	
	t.Run("Preserve other fields during password update", func(t *testing.T) {
		settings := getTestSettings(t)
		settings.Security.BasicAuth.Enabled = true
		settings.Security.BasicAuth.ClientID = "originaluser"
		settings.Security.BasicAuth.ClientSecret = "originalsecret"
		settings.Security.BasicAuth.Password = "originalpass"
		
		e := echo.New()
		controller := &Controller{
			Echo:                e,
			Settings:            settings,
			controlChan:         make(chan string, 10),
			DisableSaveSettings: true,
			logger:              log.New(io.Discard, "TEST: ", log.LstdFlags),
		}
		
		// Update only password
		update := map[string]interface{}{
			"basicAuth": map[string]interface{}{
				"password": "NewPassword789!",
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
		
		// Verify other fields preserved
		assert.Equal(t, "originaluser", controller.Settings.Security.BasicAuth.ClientID)
		assert.Equal(t, "originalsecret", controller.Settings.Security.BasicAuth.ClientSecret)
		assert.True(t, controller.Settings.Security.BasicAuth.Enabled)
		
		// Verify password was hashed
		assert.True(t, strings.HasPrefix(controller.Settings.Security.BasicAuth.Password, "$2"))
	})
}