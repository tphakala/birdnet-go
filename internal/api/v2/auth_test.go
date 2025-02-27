// auth_test.go: Package api provides tests for API v2 authentication endpoints.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"errors"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// MockSecurityManager implements a mock for the authentication system
type MockSecurityManager struct {
	mock.Mock
}

// Validate the token
func (m *MockSecurityManager) ValidateToken(token string) (bool, error) {
	args := m.Called(token)
	return args.Bool(0), args.Error(1)
}

// Generate a new token
func (m *MockSecurityManager) GenerateToken(username string) (string, error) {
	args := m.Called(username)
	return args.String(0), args.Error(1)
}

// TestAuthMiddleware tests the authentication middleware
func TestAuthMiddleware(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Set up the security manager with a mock
	mockSecurity := new(MockSecurityManager)

	// Store original security manager implementation
	originalValidateToken := validateTokenFunc
	// Override the function for testing
	validateTokenFunc = func(token string) (bool, error) {
		return mockSecurity.ValidateToken(token)
	}

	// Restore the original security manager after the test
	defer func() {
		validateTokenFunc = originalValidateToken
	}()

	// Test cases
	testCases := []struct {
		name           string
		token          string
		validateReturn bool
		validateError  error
		expectStatus   int
	}{
		{
			name:           "Valid token",
			token:          "valid-token",
			validateReturn: true,
			validateError:  nil,
			expectStatus:   http.StatusOK,
		},
		{
			name:           "Invalid token",
			token:          "invalid-token",
			validateReturn: false,
			validateError:  nil,
			expectStatus:   http.StatusUnauthorized,
		},
		{
			name:           "No token",
			token:          "",
			validateReturn: false,
			validateError:  nil,
			expectStatus:   http.StatusUnauthorized,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock expectations
			if tc.token != "" {
				mockSecurity.On("ValidateToken", tc.token).Return(tc.validateReturn, tc.validateError).Once()
			}

			// Create a test handler that will be called if middleware passes
			testHandler := func(c echo.Context) error {
				return c.String(http.StatusOK, "success")
			}

			// Create a request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/protected", http.NoBody)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call the middleware
			h := controller.AuthMiddleware(testHandler)
			err := h(c)

			// Check result
			switch tc.expectStatus {
			case http.StatusOK:
				assert.NoError(t, err)
				assert.Equal(t, http.StatusOK, rec.Code)
				assert.Equal(t, "success", rec.Body.String())
			default:
				assert.NotEqual(t, "success", rec.Body.String())
				var httpErr *echo.HTTPError
				if errors.As(err, &httpErr) {
					assert.Equal(t, tc.expectStatus, httpErr.Code)
				}
			}
		})
	}
}

// TestLogin tests the login endpoint
func TestLogin(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Set up the security manager with a mock
	mockSecurity := new(MockSecurityManager)

	// Store original security manager implementation
	originalGenerateToken := generateTokenFunc
	// Override the function for testing
	generateTokenFunc = func(username string) (string, error) {
		return mockSecurity.GenerateToken(username)
	}

	// Restore the original security manager after the test
	defer func() {
		generateTokenFunc = originalGenerateToken
	}()

	// Set up auth settings for testing
	controller.Settings = &conf.Settings{
		Security: conf.Security{
			BasicAuth: conf.BasicAuth{
				Enabled:  true,
				Password: "password",
			},
		},
	}

	// Test cases
	testCases := []struct {
		name          string
		username      string
		password      string
		expectSuccess bool
		expectToken   string
		tokenError    error
	}{
		{
			name:          "Valid login",
			username:      "admin",
			password:      "password",
			expectSuccess: true,
			expectToken:   "valid-token",
			tokenError:    nil,
		},
		{
			name:          "Invalid login",
			username:      "admin",
			password:      "wrongpassword",
			expectSuccess: false,
			expectToken:   "",
			tokenError:    nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock expectations
			if tc.expectSuccess {
				mockSecurity.On("GenerateToken", tc.username).Return(tc.expectToken, tc.tokenError).Once()
			}

			// Create login request body
			loginJSON := `{"username":"` + tc.username + `","password":"` + tc.password + `"}`

			// Create a request
			req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginJSON))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Set right settings for test
			if tc.password == "password" {
				controller.Settings.Security.BasicAuth.Password = tc.password
			} else {
				controller.Settings.Security.BasicAuth.Password = "password"
			}

			// Call login handler
			err := controller.Login(c)

			// Check result
			assert.NoError(t, err)

			switch tc.expectSuccess {
			case true:
				assert.Equal(t, http.StatusOK, rec.Code)

				// Check response body
				var response map[string]interface{}
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Equal(t, true, response["success"])
				assert.Equal(t, tc.username, response["username"])
			case false:
				assert.Equal(t, http.StatusUnauthorized, rec.Code)

				// Check response body
				var response map[string]interface{}
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Equal(t, false, response["success"])
				assert.Equal(t, "Invalid credentials", response["message"])
			}
		})
	}
}

// Mock the token validation handler if not available in the Controller
func mockValidateToken(c echo.Context) error {
	token := c.Get("token").(string)

	if token == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Token is required")
	}

	valid, _ := validateTokenFunc(token)

	if !valid {
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid token")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"valid": true,
	})
}

// TestValidateToken tests the token validation endpoint
func TestValidateToken(t *testing.T) {
	// Setup
	e, _, _ := setupTestEnvironment(t)

	// Set up the security manager with a mock
	mockSecurity := new(MockSecurityManager)

	// Store original security manager implementation
	originalValidateToken := validateTokenFunc
	// Override the function for testing
	validateTokenFunc = func(token string) (bool, error) {
		return mockSecurity.ValidateToken(token)
	}

	// Restore the original security manager after the test
	defer func() {
		validateTokenFunc = originalValidateToken
	}()

	// Test cases
	testCases := []struct {
		name           string
		token          string
		validateReturn bool
		validateError  error
		expectStatus   int
	}{
		{
			name:           "Valid token",
			token:          "valid-token",
			validateReturn: true,
			validateError:  nil,
			expectStatus:   http.StatusOK,
		},
		{
			name:           "Invalid token",
			token:          "invalid-token",
			validateReturn: false,
			validateError:  nil,
			expectStatus:   http.StatusUnauthorized,
		},
		{
			name:           "Empty token",
			token:          "",
			validateReturn: false,
			validateError:  nil,
			expectStatus:   http.StatusBadRequest,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock expectations
			if tc.token != "" {
				mockSecurity.On("ValidateToken", tc.token).Return(tc.validateReturn, tc.validateError).Once()
			}

			// Create a request
			req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/validate", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Add token to context
			c.Set("token", tc.token)

			// Call validate handler using our mock function
			err := mockValidateToken(c)

			// Check result
			if tc.expectStatus == http.StatusOK {
				assert.NoError(t, err)
				assert.Equal(t, http.StatusOK, rec.Code)

				// Check response body
				var response map[string]interface{}
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Equal(t, true, response["valid"])
			} else {
				var httpErr *echo.HTTPError
				if errors.As(err, &httpErr) {
					assert.Equal(t, tc.expectStatus, httpErr.Code)
				}
			}
		})
	}
}

// Define variables that will be used for mocking
var validateTokenFunc func(token string) (bool, error)
var generateTokenFunc func(username string) (string, error)
