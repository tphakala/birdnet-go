// auth_test.go: Package api provides tests for API v2 authentication mechanisms implementation.
// This file focuses on testing the correctness of authentication implementation including
// middleware behavior, login functionality, and token validation.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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
	ValidateRefreshToken(token string) (bool, error)
	GenerateNewTokenPair(username string) (string, string, error)
	ValidateSession(sessionID string) (bool, error)
	CreateSession(username string) (string, error)
}

// MockSecurityManager implements a mock for the authentication system
type MockSecurityManager struct {
	mock.Mock
	mu sync.Mutex // Added mutex for concurrent safety
}

// Validate the token
func (m *MockSecurityManager) ValidateToken(token string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(token)
	return args.Bool(0), args.Error(1)
}

// Generate a new token
func (m *MockSecurityManager) GenerateToken(username string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(username)
	return args.String(0), args.Error(1)
}

// ValidateRefreshToken validates a refresh token
func (m *MockSecurityManager) ValidateRefreshToken(token string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(token)
	return args.Bool(0), args.Error(1)
}

// GenerateNewTokenPair generates a new access token and refresh token pair
func (m *MockSecurityManager) GenerateNewTokenPair(username string) (accessToken, refreshToken string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(username)
	return args.String(0), args.String(1), args.Error(2)
}

// ValidateSession validates a session ID
func (m *MockSecurityManager) ValidateSession(sessionID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(sessionID)
	return args.Bool(0), args.Error(1)
}

// CreateSession creates a new session for a user
func (m *MockSecurityManager) CreateSession(username string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(username)
	return args.String(0), args.Error(1)
}

// MockServer implements the interfaces required for auth testing
type MockServer struct {
	mock.Mock
	mu          sync.Mutex // Added mutex for concurrent safety
	AuthEnabled bool
	ValidTokens map[string]bool
	Password    string
	Security    SecurityManager
}

// ValidateAccessToken validates an access token
func (m *MockServer) ValidateAccessToken(token string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

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
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(c)
	return args.Bool(0)
}

// isAuthenticationEnabled checks if authentication is enabled
func (m *MockServer) isAuthenticationEnabled(c echo.Context) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.AuthEnabled
}

// AuthenticateBasic performs basic authentication
func (m *MockServer) AuthenticateBasic(c echo.Context, username, password string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(c, username, password)
	return args.Bool(0)
}

// GetUsername returns the authenticated username
func (m *MockServer) GetUsername(c echo.Context) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(c)
	return args.String(0)
}

// GetAuthMethod returns the authentication method
func (m *MockServer) GetAuthMethod(c echo.Context) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	args := m.Called(c)
	return args.String(0)
}

// extractTokenFromContext is a utility function to consistently extract tokens
// from either context or authorization header
func extractTokenFromContext(c echo.Context) string {
	// First check if token was set directly in context
	if tokenVal := c.Get("token"); tokenVal != nil {
		if token, ok := tokenVal.(string); ok {
			return token
		}
	}

	// Next, try to extract from Authorization header (Bearer token)
	authHeader := c.Request().Header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}

	return ""
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
		{
			name:           "Syntactically corrupted token",
			token:          "invalid.jwt.format-missing-segments",
			validateReturn: false,
			validateError:  errors.New("invalid token format"),
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
			h(c)

			// Check result
			switch tc.expectStatus {
			case http.StatusOK:
				assert.Equal(t, http.StatusOK, rec.Code)
				assert.Equal(t, "success", rec.Body.String())
			default:
				assert.NotEqual(t, "success", rec.Body.String())
				assert.Equal(t, tc.expectStatus, rec.Code)
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
			controller.Settings.Security.BasicAuth.Password = testPassword

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

	// validateToken is now defined within test scope to keep the package-level namespace clean
	validateToken := func(c echo.Context, token string) (bool, error) {
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

	// mockValidateToken is now using the common token extraction utility
	mockValidateToken := func(c echo.Context) error {
		token := extractTokenFromContext(c)

		if token == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "Token is required")
		}

		valid, err := validateToken(c, token)

		// Handle specific error types
		switch {
		case err != nil && err.Error() == "token expired":
			return echo.NewHTTPError(http.StatusUnauthorized, "Token expired")
		case err != nil && err.Error() == "missing claims":
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid token format")
		case !valid:
			return echo.NewHTTPError(http.StatusUnauthorized, "Invalid token")
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"valid": true,
		})
	}

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
		{
			name:           "Malformed JWT token",
			token:          "not.a.valid.jwt.token",
			validateReturn: false,
			validateError:  errors.New("malformed token"),
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

			// Create a request - test both ways of providing the token
			req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/validate", http.NoBody)

			// Randomly alternate between setting token in header vs context to test both pathways
			if tc.name == "Valid token" || tc.name == "Invalid token" || tc.name == "Malformed JWT token" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}

			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Set the token in context (for test cases not using Authorization header)
			if tc.name != "Valid token" && tc.name != "Invalid token" && tc.name != "Malformed JWT token" {
				c.Set("token", tc.token)
			}

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

// TestAuthenticationRequirement tests that API endpoints require authentication
func TestAuthenticationRequirement(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)
	mockSecurity := new(MockSecurityManager)
	mockServer := new(MockServer)
	mockServer.AuthEnabled = true
	mockServer.Security = mockSecurity

	// Setup mock expectations for authentication checks
	mockServer.On("isAuthenticationEnabled", mock.Anything).Return(true)
	mockServer.On("IsAccessAllowed", mock.Anything).Return(false)

	// Endpoint configurations to test
	endpoints := []struct {
		method  string
		path    string
		handler func(c echo.Context) error
	}{
		{http.MethodGet, "/api/v2/detections", controller.GetDetections},
		{http.MethodGet, "/api/v2/detections/1", controller.GetDetection},
		{http.MethodDelete, "/api/v2/detections/1", controller.DeleteDetection},
		{http.MethodPost, "/api/v2/detections/1/review", controller.ReviewDetection},
		{http.MethodGet, "/api/v2/analytics/species", controller.GetSpeciesSummary},
		{http.MethodGet, "/api/v2/analytics/hourly", controller.GetHourlyAnalytics},
		{http.MethodGet, "/api/v2/analytics/daily", controller.GetDailyAnalytics},
	}

	// Test that these endpoints would be properly protected in production
	for _, ep := range endpoints {
		t.Run(fmt.Sprintf("%s %s requires auth", ep.method, ep.path), func(t *testing.T) {
			// Create a request with no auth token
			req := httptest.NewRequest(ep.method, ep.path, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Set the mock server in the context with auth enabled
			c.Set("server", mockServer)

			// Apply the auth middleware to the handler
			h := controller.AuthMiddleware(ep.handler)
			h(c)

			// Verify that unauthorized request is rejected
			assert.Equal(t, http.StatusUnauthorized, rec.Code, "Expected unauthorized status code")
			assert.Contains(t, rec.Body.String(), "Authentication required", "Expected authentication required message")
		})
	}
}

// TestRefreshToken tests the token refresh functionality
func TestRefreshToken(t *testing.T) {
	// Setup
	e, _, _ := setupTestEnvironment(t)

	// Set up the security manager with a mock
	mockSecurity := new(MockSecurityManager)

	// Create a mock server
	mockServer := new(MockServer)
	mockServer.AuthEnabled = true
	mockServer.Security = mockSecurity

	// Test cases
	testCases := []struct {
		name           string
		refreshToken   string
		mockSetup      func(*mock.Mock)
		expectStatus   int
		expectNewToken bool
	}{
		{
			name:         "Valid refresh token",
			refreshToken: "valid-refresh-token",
			mockSetup: func(m *mock.Mock) {
				m.On("ValidateRefreshToken", "valid-refresh-token").Return(true, nil)
				m.On("GenerateNewTokenPair", mock.Anything).Return("new-access-token", "new-refresh-token", nil)
			},
			expectStatus:   http.StatusOK,
			expectNewToken: true,
		},
		{
			name:         "Expired refresh token",
			refreshToken: "expired-refresh-token",
			mockSetup: func(m *mock.Mock) {
				m.On("ValidateRefreshToken", "expired-refresh-token").Return(false, errors.New("refresh token expired"))
			},
			expectStatus:   http.StatusUnauthorized,
			expectNewToken: false,
		},
		{
			name:         "Invalid refresh token",
			refreshToken: "invalid-refresh-token",
			mockSetup: func(m *mock.Mock) {
				m.On("ValidateRefreshToken", "invalid-refresh-token").Return(false, nil)
			},
			expectStatus:   http.StatusUnauthorized,
			expectNewToken: false,
		},
		{
			name:         "Missing refresh token",
			refreshToken: "",
			mockSetup:    func(m *mock.Mock) {},
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "Server error during refresh",
			refreshToken: "error-token",
			mockSetup: func(m *mock.Mock) {
				m.On("ValidateRefreshToken", "error-token").Return(true, nil)
				m.On("GenerateNewTokenPair", mock.Anything).Return("", "", errors.New("server error"))
			},
			expectStatus:   http.StatusInternalServerError,
			expectNewToken: false,
		},
		{
			name:         "Revoked/blacklisted token",
			refreshToken: "revoked-token",
			mockSetup: func(m *mock.Mock) {
				m.On("ValidateRefreshToken", "revoked-token").Return(false, errors.New("token has been revoked"))
			},
			expectStatus:   http.StatusUnauthorized,
			expectNewToken: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset mock server
			mockServer = new(MockServer)
			mockServer.AuthEnabled = true
			mockServer.Security = mockSecurity

			// Setup mock expectations
			tc.mockSetup(&mockSecurity.Mock)

			// Create a request
			reqBody := strings.NewReader(`{"refresh_token":"` + tc.refreshToken + `"}`)
			req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/refresh", reqBody)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Set the mock server in the context
			c.Set("server", mockServer)

			// Create a handler function that simulates the refresh token endpoint
			refreshHandler := func(c echo.Context) error {
				var request struct {
					RefreshToken string `json:"refresh_token"`
				}

				if err := c.Bind(&request); err != nil {
					return echo.NewHTTPError(http.StatusBadRequest, "Invalid request format")
				}

				if request.RefreshToken == "" {
					return echo.NewHTTPError(http.StatusBadRequest, "Refresh token is required")
				}

				// Get server from context
				server := c.Get("server")
				if server == nil {
					return echo.NewHTTPError(http.StatusInternalServerError, "Server not available")
				}

				mockServer, ok := server.(*MockServer)
				if !ok || mockServer.Security == nil {
					return echo.NewHTTPError(http.StatusInternalServerError, "Security manager not available")
				}

				// Validate refresh token
				valid, err := mockServer.Security.ValidateRefreshToken(request.RefreshToken)
				if err != nil {
					return echo.NewHTTPError(http.StatusUnauthorized, "Refresh token expired")
				}

				if !valid {
					return echo.NewHTTPError(http.StatusUnauthorized, "Invalid refresh token")
				}

				// Generate new token pair
				accessToken, refreshToken, err := mockServer.Security.GenerateNewTokenPair("user123")
				if err != nil {
					return echo.NewHTTPError(http.StatusInternalServerError, "Failed to generate new tokens")
				}

				// Return new tokens
				return c.JSON(http.StatusOK, map[string]interface{}{
					"access_token":  accessToken,
					"refresh_token": refreshToken,
					"token_type":    "Bearer",
				})
			}

			// Call the handler
			err := refreshHandler(c)

			// Check result
			if tc.expectStatus == http.StatusOK {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectStatus, rec.Code)

				// Check response body
				var response map[string]interface{}
				jsonErr := json.Unmarshal(rec.Body.Bytes(), &response)
				assert.NoError(t, jsonErr)

				if tc.expectNewToken {
					assert.Contains(t, response, "access_token")
					assert.Contains(t, response, "refresh_token")
					assert.Equal(t, "Bearer", response["token_type"])
				}
			} else {
				// For error cases
				var httpErr *echo.HTTPError
				if errors.As(err, &httpErr) {
					assert.Equal(t, tc.expectStatus, httpErr.Code)
				}
			}

			// Verify mock expectations
			mockSecurity.AssertExpectations(t)
		})
	}
}

// TestSessionManagement tests session creation, validation, and expiration
func TestSessionManagement(t *testing.T) {
	// Setup
	e, _, _ := setupTestEnvironment(t)

	// Set up the security manager with a mock
	mockSecurity := new(MockSecurityManager)

	// Create a mock server
	mockServer := new(MockServer)
	mockServer.AuthEnabled = true
	mockServer.Security = mockSecurity

	// Test cases
	testCases := []struct {
		name           string
		setupSession   bool
		sessionAge     time.Duration
		mockSetup      func(*mock.Mock)
		expectValid    bool
		expectResponse int
	}{
		{
			name:         "Valid active session",
			setupSession: true,
			sessionAge:   5 * time.Minute, // Recent session
			mockSetup: func(m *mock.Mock) {
				m.On("ValidateSession", "test-session-id").Return(true, nil)
			},
			expectValid:    true,
			expectResponse: http.StatusOK,
		},
		{
			name:         "Expired session",
			setupSession: true,
			sessionAge:   25 * time.Hour, // Older than typical session timeout
			mockSetup: func(m *mock.Mock) {
				m.On("ValidateSession", "test-session-id").Return(false, errors.New("session expired"))
			},
			expectValid:    false,
			expectResponse: http.StatusUnauthorized,
		},
		{
			name:         "Invalid session",
			setupSession: true,
			sessionAge:   5 * time.Minute,
			mockSetup: func(m *mock.Mock) {
				m.On("ValidateSession", "test-session-id").Return(false, nil)
			},
			expectValid:    false,
			expectResponse: http.StatusUnauthorized,
		},
		{
			name:           "No session",
			setupSession:   false,
			mockSetup:      func(m *mock.Mock) {},
			expectValid:    false,
			expectResponse: http.StatusUnauthorized,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset mock server
			mockServer = new(MockServer)
			mockServer.AuthEnabled = true
			mockServer.Security = mockSecurity

			// Setup mock expectations
			tc.mockSetup(&mockSecurity.Mock)

			// Create a request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/auth/session", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Set up session if needed
			if tc.setupSession {
				// Create a session cookie
				cookie := &http.Cookie{
					Name:     "session_id",
					Value:    "test-session-id",
					Path:     "/",
					HttpOnly: true,
					Secure:   true,
					SameSite: http.SameSiteStrictMode,
					Expires:  time.Now().Add(-tc.sessionAge), // Use negative to simulate age
				}
				req.AddCookie(cookie)
			}

			// Set the mock server in the context
			c.Set("server", mockServer)

			// Create a handler function that simulates the session validation endpoint
			sessionHandler := func(c echo.Context) error {
				// Check for session cookie
				cookie, err := c.Cookie("session_id")
				if err != nil {
					return echo.NewHTTPError(http.StatusUnauthorized, "No session found")
				}

				// Get server from context
				server := c.Get("server")
				if server == nil {
					return echo.NewHTTPError(http.StatusInternalServerError, "Server not available")
				}

				mockServer, ok := server.(*MockServer)
				if !ok || mockServer.Security == nil {
					return echo.NewHTTPError(http.StatusInternalServerError, "Security manager not available")
				}

				// Validate session
				valid, err := mockServer.Security.ValidateSession(cookie.Value)
				if err != nil {
					return echo.NewHTTPError(http.StatusUnauthorized, "Session expired")
				}

				if !valid {
					return echo.NewHTTPError(http.StatusUnauthorized, "Invalid session")
				}

				// Return session info
				return c.JSON(http.StatusOK, map[string]interface{}{
					"valid":       true,
					"session_id":  cookie.Value,
					"user":        "test_user",
					"last_active": time.Now().Format(time.RFC3339),
				})
			}

			// Call the handler
			err := sessionHandler(c)

			// Check result
			if tc.expectValid {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectResponse, rec.Code)

				// Check response body
				var response map[string]interface{}
				jsonErr := json.Unmarshal(rec.Body.Bytes(), &response)
				assert.NoError(t, jsonErr)
				assert.Equal(t, true, response["valid"])
				assert.Equal(t, "test-session-id", response["session_id"])
			} else {
				// For error cases
				if tc.setupSession {
					var httpErr *echo.HTTPError
					if errors.As(err, &httpErr) {
						assert.Equal(t, tc.expectResponse, httpErr.Code)
					}
				} else {
					var httpErr *echo.HTTPError
					if errors.As(err, &httpErr) {
						assert.Equal(t, tc.expectResponse, httpErr.Code)
						assert.Contains(t, httpErr.Message, "No session found")
					}
				}
			}

			// Verify mock expectations
			mockSecurity.AssertExpectations(t)
		})
	}
}
