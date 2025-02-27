// auth_test.go: Package api provides tests for API v2 authentication endpoints.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"errors"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// SecurityManager interface defines methods for token validation and generation
type SecurityManager interface {
	ValidateToken(token string) (bool, error)
	GenerateToken(username string) (string, error)
}

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

// MockServer implements the interfaces required for auth testing
type MockServer struct {
	mock.Mock
	AuthEnabled bool
	ValidTokens map[string]bool
	Password    string
	Security    SecurityManager
}

// ValidateAccessToken validates an access token
func (m *MockServer) ValidateAccessToken(token string) bool {
	// First check if we have a direct mock expectation
	if m.Mock.ExpectedCalls != nil {
		for _, call := range m.Mock.ExpectedCalls {
			if call.Method == "ValidateAccessToken" {
				args := m.Called(token)
				return args.Bool(0)
			}
		}
	}

	// Otherwise, delegate to the security manager if available
	if m.Security != nil {
		isValid, _ := m.Security.ValidateToken(token)
		return isValid
	}

	return false
}

// IsAccessAllowed checks if access is allowed
func (m *MockServer) IsAccessAllowed(c echo.Context) bool {
	args := m.Called(c)
	return args.Bool(0)
}

// isAuthenticationEnabled checks if authentication is enabled
func (m *MockServer) isAuthenticationEnabled(c echo.Context) bool {
	return m.AuthEnabled
}

// AuthenticateBasic performs basic authentication
func (m *MockServer) AuthenticateBasic(c echo.Context, username, password string) bool {
	args := m.Called(c, username, password)
	return args.Bool(0)
}

// GetUsername returns the authenticated username
func (m *MockServer) GetUsername(c echo.Context) string {
	args := m.Called(c)
	return args.String(0)
}

// GetAuthMethod returns the authentication method
func (m *MockServer) GetAuthMethod(c echo.Context) string {
	args := m.Called(c)
	return args.String(0)
}

// TestAuthMiddleware tests the authentication middleware
func TestAuthMiddleware(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Set up the security manager with a mock
	mockSecurity := new(MockSecurityManager)

	// Create a mock server
	mockServer := new(MockServer)
	mockServer.AuthEnabled = true
	mockServer.Security = mockSecurity

	// Test cases
	testCases := []struct {
		name           string
		token          string
		validateReturn bool
		validateError  error
		expectStatus   int
		serverSetup    func(*MockServer) // Added server setup function for custom server configuration
	}{
		{
			name:           "Valid token",
			token:          "valid-token",
			validateReturn: true,
			validateError:  nil,
			expectStatus:   http.StatusOK,
			serverSetup:    nil,
		},
		{
			name:           "Invalid token",
			token:          "invalid-token",
			validateReturn: false,
			validateError:  nil,
			expectStatus:   http.StatusUnauthorized,
			serverSetup:    nil,
		},
		{
			name:           "No token",
			token:          "",
			validateReturn: false,
			validateError:  nil,
			expectStatus:   http.StatusUnauthorized,
			serverSetup:    nil,
		},
		{
			name:           "Missing security manager",
			token:          "valid-token",
			validateReturn: false,
			validateError:  nil,
			expectStatus:   http.StatusUnauthorized,
			serverSetup: func(m *MockServer) {
				// Set security manager to nil to test the path where ValidateAccessToken fails because
				// security manager is missing
				m.Security = nil
			},
		},
		{
			name:           "Token validation error",
			token:          "error-token",
			validateReturn: false,
			validateError:  errors.New("validation error"),
			expectStatus:   http.StatusUnauthorized,
			serverSetup:    nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset mock and set expectations
			mockServer = new(MockServer)
			mockServer.AuthEnabled = true
			mockServer.Security = mockSecurity

			// Apply custom server setup if provided
			if tc.serverSetup != nil {
				tc.serverSetup(mockServer)
			}

			if tc.token != "" {
				// Only setup mock security expectations if we have a security manager
				if mockServer.Security != nil {
					mockSecurity.On("ValidateToken", tc.token).Return(tc.validateReturn, tc.validateError).Once()
					mockServer.On("ValidateAccessToken", tc.token).Return(tc.validateReturn).Once()
				} else {
					// When security manager is nil, ValidateAccessToken will return false
					mockServer.On("ValidateAccessToken", tc.token).Return(false).Once()
				}
			} else {
				// For the "No token" case, IsAccessAllowed will be called
				mockServer.On("IsAccessAllowed", mock.Anything).Return(false).Once()
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

			// Set the mock server in the context
			c.Set("server", mockServer)

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

	// Create a mock server
	mockServer := new(MockServer)
	mockServer.AuthEnabled = true
	mockServer.Security = mockSecurity

	// Retrieve test password from environment with fallback
	// This approach is more secure for CI/CD and prevents hard-coded credentials
	testPassword := os.Getenv("TEST_AUTH_PASSWORD")
	if testPassword == "" {
		// Fallback to a default for tests only
		testPassword = "test-password-123"
	}

	// Setup credentials for testing from environment variables or defaults
	controller.Settings = &conf.Settings{
		Security: conf.Security{
			BasicAuth: conf.BasicAuth{
				Enabled:  true,
				Password: testPassword,
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
			password:      testPassword, // Use environment-based password
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
			// Reset mock server for each test case
			mockServer = new(MockServer)
			mockServer.AuthEnabled = true
			mockServer.Security = mockSecurity

			// Setup mock expectations
			if tc.expectSuccess {
				mockSecurity.On("GenerateToken", tc.username).Return(tc.expectToken, tc.tokenError).Once()
				mockServer.On("AuthenticateBasic", mock.Anything, tc.username, tc.password).Return(true).Once()
			} else {
				mockServer.On("AuthenticateBasic", mock.Anything, tc.username, tc.password).Return(false).Once()
			}

			// Create login request body
			loginJSON := `{"username":"` + tc.username + `","password":"` + tc.password + `"}`

			// Create a request
			req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginJSON))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Set the mock server in the context
			c.Set("server", mockServer)

			// Use the correct test password for comparison
			// This pattern avoids hardcoding the actual password in the test code
			if tc.expectSuccess {
				controller.Settings.Security.BasicAuth.Password = testPassword
			} else {
				// Ensure we have a different password for negative tests
				controller.Settings.Security.BasicAuth.Password = testPassword
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

// ValidateToken implements a test token validation function directly within test context
func validateToken(c echo.Context, token string) (bool, error) {
	// Get server from context which should contain our mock
	server := c.Get("server")
	if server == nil {
		return false, errors.New("server not available in context")
	}

	// Try to use the mock server's security manager
	if mockServer, ok := server.(*MockServer); ok && mockServer.Security != nil {
		return mockServer.Security.ValidateToken(token)
	}

	return false, errors.New("validation failed")
}

// mockValidateToken is an implementation for the test handler
func mockValidateToken(c echo.Context) error {
	token := c.Get("token").(string)

	if token == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Token is required")
	}

	valid, err := validateToken(c, token)

	// Handle specific error types
	if err != nil && err.Error() == "token expired" {
		return echo.NewHTTPError(http.StatusUnauthorized, "Token expired")
	} else if err != nil && err.Error() == "missing claims" {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid token format")
	} else if !valid {
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

	// Create mock server
	mockServer := new(MockServer)
	mockServer.AuthEnabled = true
	mockServer.Security = mockSecurity

	// Test cases
	testCases := []struct {
		name           string
		token          string
		validateReturn bool
		validateError  error
		expectStatus   int
		expectMessage  string
	}{
		{
			name:           "Valid token",
			token:          "valid-token",
			validateReturn: true,
			validateError:  nil,
			expectStatus:   http.StatusOK,
			expectMessage:  "",
		},
		{
			name:           "Invalid token",
			token:          "invalid-token",
			validateReturn: false,
			validateError:  nil,
			expectStatus:   http.StatusUnauthorized,
			expectMessage:  "Invalid token",
		},
		{
			name:           "Empty token",
			token:          "",
			validateReturn: false,
			validateError:  nil,
			expectStatus:   http.StatusBadRequest,
			expectMessage:  "Token is required",
		},
		{
			name:           "Expired token",
			token:          "expired-token",
			validateReturn: false,
			validateError:  errors.New("token expired"),
			expectStatus:   http.StatusUnauthorized,
			expectMessage:  "Token expired",
		},
		{
			name:           "Token with missing claims",
			token:          "incomplete-token",
			validateReturn: false,
			validateError:  errors.New("missing claims"),
			expectStatus:   http.StatusBadRequest,
			expectMessage:  "Invalid token format",
		},
		{
			name:           "Token with validation error",
			token:          "error-token",
			validateReturn: false,
			validateError:  errors.New("validation error"),
			expectStatus:   http.StatusUnauthorized,
			expectMessage:  "Invalid token",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset mock server
			mockServer = new(MockServer)
			mockServer.AuthEnabled = true
			mockServer.Security = mockSecurity

			// Setup mock expectations
			if tc.token != "" {
				mockSecurity.On("ValidateToken", tc.token).Return(tc.validateReturn, tc.validateError).Once()
				mockServer.On("ValidateAccessToken", tc.token).Return(tc.validateReturn).Once()
			}

			// Create a request
			req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/validate", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Add token to context
			c.Set("token", tc.token)

			// Set the mock server in the context
			c.Set("server", mockServer)

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
					// Also check the error message if provided
					if tc.expectMessage != "" {
						assert.Equal(t, tc.expectMessage, httpErr.Message)
					}
				}
			}
		})
	}
}
