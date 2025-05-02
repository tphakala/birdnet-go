# Security Package

The security package provides authentication and authorization mechanisms for the BirdNET-Go application, enabling secure access to protected resources through various authentication methods.

## Overview

This package implements a security layer that supports:

- Basic authentication with client ID/secret
- OAuth2 authentication with social providers (Google, GitHub)
- Local network authentication bypass for trusted subnets
- Persistent sessions across application restarts

## Authentication Flow

The security package follows a layered approach to authentication, determining if a user can access protected resources using the following sequence:

1. **Local Network Check**
   - If the request comes from a client IP in the same subnet as the server, and local subnet bypass is enabled, authentication is automatically granted
   - This is handled by the `IsInLocalSubnet()` function which:
     - Checks if the client IP is in the same /24 subnet as any of the server's network interfaces
     - Has special handling for containerized environments using `IsInHostSubnet()`

2. **Allowed Subnet Check**
   - If local subnet bypass is not applicable, the system checks if the client IP is in a list of explicitly allowed subnets
   - This is managed by `IsRequestFromAllowedSubnet()` which:
     - Checks if the AllowSubnetBypass feature is enabled in settings
     - Verifies if the client IP falls within any of the allowed CIDR ranges in the configuration

3. **Access Token Validation**
   - If local subnet checks fail, the system looks for a valid access token in the user's session
   - `ValidateAccessToken()` verifies that:
     - The token exists in the token store
     - The token has not expired

4. **Social Authentication Check**
   - If token validation fails, the system checks for valid social provider authentication
   - For Google and GitHub providers, it:
     - Verifies the provider is enabled in the configuration
     - Checks for a valid session from that provider
     - Confirms the user ID in the session matches allowed IDs in the configuration

The authentication flow is primarily managed by two key methods:

- `IsAuthenticationEnabled(ip string)`: Determines if authentication is required for a given IP address
- `IsUserAuthenticated(c echo.Context)`: Checks if the current request is from an authenticated user

### Authentication Decision Logic

```
IsAuthenticationEnabled(ip) -> false if:
  - IP is in an allowed subnet (configured via AllowSubnetBypass)
  - No authentication methods are enabled

IsUserAuthenticated(c) -> true if any of:
  - Request is from local subnet (same network as server)
  - Valid access token exists in session
  - Valid social provider session exists with matching user ID
```

### Integration with HTTP Controller

The HTTP controller uses this authentication system to protect routes by:

1. Using middleware that checks if authentication is enabled for the client IP
2. For protected routes, validating if the user is authenticated
3. Providing appropriate responses based on authentication status:
   - For API routes: Returning JSON with 401 Unauthorized
   - For web routes: Redirecting to login page with return URL

## Route Protection and Middleware

The application uses a dedicated `AuthMiddleware` to control access to protected resources:

1. **Protected Route Classification**
   - Routes are classified as protected based on their path prefixes:
     - Settings management routes (`/settings/`)
     - API endpoints for sensitive operations (`/api/v1/detections/delete`, `/api/v1/mqtt/`, etc.)
     - All API v2 endpoints (`/api/v2/`)
     - HLS streaming endpoints (`/api/v1/audio-stream-hls`)
     - Logout functionality (`/logout`)

2. **Public API Routes**
   - Some API routes are explicitly marked as public even in protected namespaces:
     - Detection listing endpoints (`/api/v2/detections`)
     - Analytics data endpoints (`/api/v2/analytics`)
     - Media access endpoints (`/api/v2/media/species-image`)
     - Spectrogram endpoints (`/api/v2/spectrogram`)
     - Audio file access endpoints (`/api/v2/audio`)

3. **Security Bypasses**
   - Local subnet clients are automatically granted access to protected routes
   - Authentication checks are skipped for non-protected routes
   - Rate limiting is applied to authentication endpoints to prevent brute force attacks

4. **Response Type Handling**
   - Different responses are provided based on request type:
     - API requests receive JSON 401 responses with descriptive error messages
     - HTMX requests receive `HX-Redirect` headers to the login page
     - Browser requests are redirected to the login page with a return URL

## Authentication Endpoints

The security package exposes several authentication endpoints that implement different authentication flows:

### OAuth2 Endpoints

- **`/api/v1/oauth2/authorize`**: Initiates the OAuth2 authorization flow
- **`/api/v1/oauth2/token`**: Exchanges an authorization code for an access token
- **`/api/v1/oauth2/callback`**: Completes the OAuth2 flow and establishes a session

### Social Authentication Endpoints

- **`/api/v1/auth/:provider`**: Initiates authentication with a social provider (Google, GitHub)
- **`/api/v1/auth/:provider/callback`**: Handles the callback from social providers

### Basic Authentication Endpoints

- **`/login`**: Renders the login page and handles credential validation
- **`/logout`**: Terminates all active sessions

### Authentication Process

1. **Login Flow**
   - User submits credentials through the login page
   - Credentials are validated (password for basic auth)
   - An authorization code is generated
   - User is redirected to the callback endpoint with the code
   - Code is exchanged for an access token
   - Session is established with the access token
   - User is redirected to the original destination or home page

2. **Logout Flow**
   - User session data is cleared for all providers
   - Access tokens are invalidated
   - User is redirected to the home page

## Components

### OAuth2Server

The core component that manages authentication and authorization flows:

```go
type OAuth2Server struct {
	Settings     *conf.Settings
	authCodes    map[string]AuthCode
	accessTokens map[string]AccessToken
	mutex        sync.RWMutex
	debug        bool

	GithubConfig *oauth2.Config
	GoogleConfig *oauth2.Config
	
	// Token persistence
	tokensFile     string
	persistTokens  bool
}
```

### Authentication Mechanisms

#### Basic Authentication

Enables username/password authentication with OAuth2 token issuance:

- `HandleBasicAuthorize`: Initiates the basic auth flow by generating an auth code
- `HandleBasicAuthToken`: Exchanges auth code for an access token
- `HandleBasicAuthCallback`: Handles the redirect after successful authentication

#### Social Authentication

Leverages third-party identity providers through the [Goth](https://github.com/markbates/goth) library:

- Google OAuth2 authentication
- GitHub OAuth2 authentication

#### Local Network Authentication

Allows bypassing authentication for requests from trusted local networks:

- `IsInLocalSubnet`: Determines if a client IP is in the same subnet as a local network interface
- `IsRequestFromAllowedSubnet`: Checks if a request comes from a configured allowed subnet

## Token Management

The package implements a secure token lifecycle:

- `GenerateAuthCode`: Generates time-limited authorization codes
- `ExchangeAuthCode`: Exchanges valid auth codes for access tokens
- `ValidateAccessToken`: Validates access tokens and handles expiration
- `StartAuthCleanup`: Background routine that cleans up expired tokens

## Session Persistence

Sessions and authentication state persist across application restarts:

- User sessions are stored on disk using `FilesystemStore` instead of in-memory
- Access tokens are serialized to JSON and saved to the configuration directory
- Tokens are automatically loaded when the application starts
- Expired tokens are cleaned up periodically
- Session files are stored in the application's configuration directory

## Configuration

Security settings are configured through the `conf.Settings` structure:

```go
type Security struct {
	Debug             bool
	Host              string
	AutoTLS           bool
	RedirectToHTTPS   bool
	AllowSubnetBypass AllowSubnetBypass
	BasicAuth         BasicAuth
	GoogleAuth        SocialProvider
	GithubAuth        SocialProvider
	SessionSecret     string
}
```

### Authentication Configuration

#### Basic Authentication

```go
type BasicAuth struct {
	Enabled        bool
	Password       string
	ClientID       string
	ClientSecret   string
	RedirectURI    string
	AuthCodeExp    time.Duration
	AccessTokenExp time.Duration
}
```

#### Social Provider Authentication

```go
type SocialProvider struct {
	Enabled      bool
	ClientID     string
	ClientSecret string
	RedirectURI  string
	UserId       string
}
```

#### Local Network Bypass

```go
type AllowSubnetBypass struct {
	Enabled bool
	Subnet  string // CIDR notation
}
```

## Usage

### Creating an OAuth2Server

```go
// Initialize the server
server := security.NewOAuth2Server()

// Use the server to check authentication
if server.IsUserAuthenticated(c) {
    // User is authenticated
    // Allow access to protected resources
} else {
    // Redirect to login or return unauthorized
}
```

### Checking Authentication Status

```go
// Check if authentication is enabled for this client
if server.IsAuthenticationEnabled(clientIP) {
    // Authentication is required
    if server.IsUserAuthenticated(c) {
        // Allow access
    } else {
        // Deny access
    }
} else {
    // Authentication is bypassed for this client
    // Allow access
}
```

## Cookie Security

The package provides context-aware cookie security:

- Secure cookies over HTTPS
- Special handling for local network connections

```go
// Configure cookie store for local network access
configureLocalNetworkCookieStore()
```

## Cross-Platform Compatibility

The security package is designed to work on:
- Linux
- macOS
- Windows

It properly handles different network interface naming conventions and subnet calculations across platforms.

## Best Practices

1. Always maintain secure values for client secrets and session keys
2. Use HTTPS in production environments 
3. Be cautious when enabling local network authentication bypass
4. Regularly rotate access tokens using appropriate expiration times
5. Verify the identity provider configurations (redirect URIs, client IDs, etc.)
6. Use the AllowSubnetBypass feature only for trusted networks
7. Ensure the application has write permissions to the configuration directory for session persistence

## Security Considerations

- The package implements proper token generation with cryptographic randomness
- Authorization codes and access tokens have configurable expiration times
- Concurrent access to token stores is protected by mutexes
- The package cleans up expired tokens to prevent memory leaks and unauthorized access
- Always verify that redirect URIs match the configured hosts to prevent open redirectors
- Session files are stored with strict permissions (0600) to prevent unauthorized access 

## Testing

The security package includes tests for session persistence:

- `TestTokenPersistence`: Tests saving and loading of access tokens to/from disk
- `TestFilesystemStore`: Tests correct initialization of persistent session storage
- `TestLocalNetworkCookieStore`: Tests configuration of cookie stores for local network access

For testing purposes, a helper function is available:

```go
// Set a custom path for session files during tests
security.SetTestConfigPath("/path/for/testing")

// Make sure to reset it after the test
defer security.SetTestConfigPath("")
``` 