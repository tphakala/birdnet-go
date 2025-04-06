# Security Package

The security package provides authentication and authorization mechanisms for the BirdNET-Go application, enabling secure access to protected resources through various authentication methods.

## Overview

This package implements a security layer that supports:

- Basic authentication with client ID/secret
- OAuth2 authentication with social providers (Google, GitHub)
- Local network authentication bypass for trusted subnets
- Persistent sessions across application restarts

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