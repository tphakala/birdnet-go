# Security Package

The `security` package holds the authentication building blocks of BirdNET-Go: the `OAuth2Server` that issues and validates tokens, social login through [goth](https://github.com/markbates/goth), the persistent session store, the subnet bypass checks, and the path and redirect validation helpers. It does not register HTTP routes itself. The routes and middleware that use it live in `internal/api`.

## Where the pieces live

| Concern                                               | Location                                                                         |
| ----------------------------------------------------- | -------------------------------------------------------------------------------- |
| `OAuth2Server`, tokens, sessions, providers, bypass   | `internal/security` (this package)                                               |
| Client address resolution for auth decisions          | `internal/security/proxytrust`                                                   |
| Basic-auth login, OAuth callback, logout, status      | `internal/api/v2/auth` (`/api/v2/auth/login`, `/callback`, `/logout`, `/status`) |
| Social login routes `/auth/:provider` and `/callback` | `registerOAuthRoutes` in `internal/api/server.go`                                |
| Auth middleware and the adapter around `OAuth2Server` | `internal/api/auth` (`Middleware`, `SecurityAdapter`)                            |
| Test helpers                                          | `internal/security/securitytest`                                                 |

API v1 no longer exists, so there are no `/api/v1/...` auth endpoints.

## OAuth2Server

`NewOAuth2Server(ctx, secureCookies)` (`oauth.go`) builds the server from the current settings. It validates the session secret (an empty secret is replaced by a temporary random one, a secret shorter than 32 characters only produces a warning), initializes goth (session store and providers), loads persisted tokens, and starts a cleanup goroutine that runs hourly until `ctx` is cancelled. `secureCookies` sets the `Secure` attribute on session cookies; the caller derives it from the effective web server TLS configuration.

All runtime reads go through `currentSettings()` / `CurrentSettings()`, which resolve the live settings snapshot. Changes to credentials, token lifetimes, subnets and trusted proxies therefore apply on the next request without a restart.

## Authentication flow

`IsUserAuthenticated(c)` returns true when any of these holds, checked in this order:

1. The request qualifies for the local subnet bypass (see below).
2. The session holds an `access_token` that `ValidateAccessToken` accepts (basic auth login).
3. The session holds a social login for an enabled provider whose user ID or email is in that provider's allowed user list (comparison is case-insensitive, entries are comma-separated). The active provider is read from the `auth_provider` session key, with a fallback that walks all configured providers.

`IsAuthenticationEnabled(r)` reports whether a request has to authenticate at all: it is false when the request passes the request gate below, or when no provider (basic auth or an enabled social provider) is configured. `internal/api/auth.Middleware` calls it first, then tries a `Bearer` token, then the session, and answers an unauthenticated request with a redirect to `/login` for browsers or a JSON 401 for API clients.

### Basic auth

Basic auth uses one fixed credential pair from `Security.BasicAuth` (`ClientID` as the user name, `Password`). The login handler in `internal/api/v2/auth` checks the credentials through `SecurityAdapter.AuthenticateBasic`, which calls `GenerateAuthCode`. The login response carries a redirect to `/api/v2/auth/callback?code=...`. The callback handler calls `ExchangeAuthCode` to turn the code into an access token, then `EstablishSession` clears any previous session (session fixation mitigation) and stores the token in a fresh one. Codes and tokens are random 32-byte values, their lifetimes are `BasicAuth.AuthCodeExp` and `BasicAuth.AccessTokenExp`, and a code is single use. The login endpoint is rate limited and adds a short random delay on failures.

`basic.go` also contains `HandleBasicAuthorize`, `HandleBasicAuthToken` and `HandleBasicAuthCallback`. The server does not register them as routes any more; the flow above replaced them.

### Social providers

Providers are configured in `Security.OAuthProviders` and registered with goth by `initializeProviders`. Config IDs and goth names are mapped in `constants.go` (`ConfigToGothProvider`):

| Config ID   | goth name         |
| ----------- | ----------------- |
| `google`    | `google`          |
| `github`    | `github`          |
| `microsoft` | `microsoftonline` |
| `line`      | `line`            |
| `kakao`     | `kakao`           |
| `oidc`      | `openid-connect`  |

A provider is skipped unless it is enabled and has a client ID and secret. When no redirect URI is configured, it defaults to `<BaseURL or Host>/auth/<goth name>/callback`. The generic OIDC provider uses discovery from `<issuer>/.well-known/openid-configuration`; if discovery fails, a background retry with backoff (`OIDCRetry*` constants) keeps trying until it succeeds, the retry window ends, or shutdown begins. `oidc_pkce.go` wraps goth's OpenID Connect provider so every login uses PKCE with the `S256` challenge, keeping the code verifier in the goth session.

The HTTP side is in `internal/api/server.go`: `GET /auth/:provider` begins the login (rejecting unknown or unconfigured providers) and `GET /auth/:provider/callback` completes it, checks the allowed user list, and stores `userId`, the provider session key and `auth_provider` in the session.

### Logout

`SecurityAdapter.Logout` clears `userId`, `access_token`, `id_token`, `auth_provider` and every provider session key, then calls `gothic.Logout`. For OIDC providers the adapter can also build the provider's end-session URL.

## Local subnet bypass

The bypass is controlled by `Security.AllowSubnetBypass` (`Enabled`, `Subnet` as a comma-separated CIDR list). Two separate checks use it, and both run on the verified client address rather than the address used for logging:

- **Request gate** (`isAllowedSubnetIP`, used by `IsAuthenticationEnabled` and `IsRequestFromAllowedSubnet`): when the bypass is enabled, loopback addresses and addresses inside a configured CIDR need no authentication at all.
- **Local network check** (`IsInLocalSubnet` in `basic.go`, used by `IsUserAuthenticated`): when the bypass is enabled, an address is treated as authenticated if it is IPv6 link-local, or on a network of one of this host's non-loopback interfaces. Inside a container it compares against the host subnet through `conf.IsInHostSubnet` instead.

Requests that pass only the second check still reach the login middleware, which then lets them through in `IsUserAuthenticated`.

### Client address and trusted proxies

`authClientIP` resolves the address through `proxytrust.Checker.AuthClientIP`, driven by `Security.TrustedProxies`. It returns the connection peer when the request has no forwarded client-IP header. When forwarded headers are present, it returns the forwarded address only if the peer is a configured proxy and the headers agree on one non-loopback client; otherwise it returns no address, and no bypass applies. The attribution address that logs and rate limiting use (`echo.Context.RealIP`) is more permissive and is never used for authentication decisions. `proxytrust` also provides `Classify`, which the settings connection check and the throttled log notices use so they match the authentication decision. See the package comment in `proxytrust/proxytrust.go` for the full rules, including the `cloudflare` preset.

## Sessions and token persistence

- **Session store** (`session_store.go`, `setupSessionStore` in `oauth.go`): a gorilla `FilesystemStore` under `<config dir>/sessions`, with the authentication and encryption keys derived from `Security.SessionSecret` by SHA-256. Sessions last `Security.SessionDuration`, defaulting to 7 days, and are capped at 1 MB. If the directory cannot be created, an in-memory cookie store with the same cookie options is used. The cookie options are `HttpOnly`, `SameSite=Lax` and `Secure` according to `secureCookies`. The store is wrapped so that clients listed in `Security.PlainHTTPSessionClients`, connecting directly over plain HTTP, get cookies without `Secure`.
- **Token persistence** (`setupTokenPersistence`, `saveTokens`, `loadTokens`): authorization codes and access tokens are written to `tokens.json` in the first config directory, with file mode `0600`, through a temporary file and rename. Expired entries are dropped on load, and `cleanupExpired` removes them every hour.

## Redirect and path validation (`util.go`)

- `IsSafePath` accepts only internal paths: it must start with `/` and stay under `MaxSafePathLength`, and rejects `//`, backslashes, `://`, `..`, null bytes and their URL-encoded, double-encoded and triple-encoded forms. The input is normalized to NFKC first so full-width look-alikes do not slip through.
- `IsValidRedirect` is the query-aware check used by the login middleware and the v2 login handler. The path part must pass `IsSafePath`; the query part, bounded by `MaxSafeRedirectLength`, is only checked for CR/LF, encoded nulls and control characters, so legitimate filter queries survive a login round trip.
- `ValidateAuthCallbackRedirect` sanitizes a redirect to a relative path, returning `/` for absolute, protocol-relative or backslash forms and CRLF injection. The v2 callback uses its own equivalent, `ValidateAndSanitizeRedirect`, in `internal/api/v2/auth`.
- `ValidateRedirectURI` compares a provided redirect URI with the configured basic auth one (scheme, host, normalized port, cleaned path, no query or fragment).

## Testing

`securitytest.NewOAuth2ServerForTesting(tb, settings)` builds an `OAuth2Server` around a settings object and restores the global settings snapshot when the test ends. It lives in its own package so `testing` stays out of the production build. `SetTestConfigPath(dir)` redirects the session store to a test directory; reset it with `SetTestConfigPath("")` afterwards. The package also has fuzz tests for the path and redirect helpers, and persistence tests for tokens and the session store.

Run the Go tests through `task test`, which adds the required build tags and CGO flags.
