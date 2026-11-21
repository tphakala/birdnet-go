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

All runtime reads go through `currentSettings()` / `CurrentSettings()`, which resolve the live settings snapshot. Changes to credentials, token lifetimes, subnets and trusted proxies therefore apply on the next request without a restart. Provider registration is rebuilt by `ReloadOAuthProviders` when provider settings change (see below), and it leaves the session store alone, so signed-in sessions survive.

## Authentication flow

`IsUserAuthenticated(c)` returns true when any of these holds, checked in this order:

1. The request qualifies for the local subnet bypass (see below).
2. The `birdnet_session` cookie names a live session record (`checkSession`, the only session check). A password record counts while basic auth is enabled. A social record counts while its own provider is enabled and its user ID or email is in that provider's allowed user list (comparison is case-insensitive, entries are comma-separated); no other provider's list is consulted.

`IsAuthenticationEnabled(r)` reports whether a request has to authenticate at all: it is false when the request passes the request gate below, or when no provider (basic auth or an enabled social provider) is configured. `internal/api/auth.Middleware` calls it first, then tries a `Bearer` token, then the session, and answers an unauthenticated request with a redirect to `/login` for browsers or a JSON 401 for API clients.

### Basic auth

Basic auth uses one fixed credential pair from `Security.BasicAuth` (`ClientID` as the user name, `Password`). The login handler in `internal/api/v2/auth` checks the credentials through `SecurityAdapter.AuthenticateBasic`, which calls `GenerateAuthCode` with the settings snapshot it checked the password against; a code is refused when the credentials have changed since. The login response carries a redirect to `/api/v2/auth/callback?code=...`. The login request may carry `rememberBrowser`; the choice is stored with the code. The callback handler calls `ExchangeAuthCode` to turn the code into a session record, then `EstablishSession` revokes the record the browser's cookie named and sends the new `birdnet_session` cookie. Codes and tokens are random 32-byte values, a code lives `BasicAuth.AuthCodeExp` and is single use. How long the session lasts does not come from `BasicAuth.AccessTokenExp` (see Sessions and token persistence). The login endpoint is rate limited and adds a short random delay on failures.

`basic.go` also contains `HandleBasicAuthorize`. The server does not register it as a route any more; the flow above replaced it.

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

A provider is skipped unless it is enabled and has a client ID and secret. When no redirect URI is configured, it defaults to `<BaseURL or Host>/auth/<goth name>/callback`. Every registration pass (startup, and `ReloadOAuthProviders` after a provider, `Host` or `BaseURL` change) replaces goth's whole registry, so a disabled, removed or changed provider is no longer served. When the OIDC settings are unchanged, the registered OIDC provider is carried into the new registry and a discovery still running is left running, so editing another provider does not interrupt OIDC login. The generic OIDC provider uses discovery from `<issuer>/.well-known/openid-configuration` (a configured issuer that already ends in that path is accepted as is). A discovery document that reports a different issuer than the configured URL is logged as a warning and does not block sign-in. Discovery runs in a background goroutine tagged with a registration generation: it tries at once and retries with backoff (`OIDCRetry*` constants) until it succeeds, the retry window ends, a pass with changed OIDC settings supersedes it, or shutdown begins. Discovery, token exchange, userinfo and token refresh use one client (`oidcHTTPClient`) bounded by `OIDCHTTPTimeout`, and discovery is cancelled with its registration generation and at the end of the retry window, also in the middle of a request. A superseded or cancelled attempt registers nothing and records no failure, and OIDC login answers 503 until discovery has succeeded. The last discovery failure is recorded, and the first failure, a late registration and retry exhaustion are also logged on the `security.oidc` module (console and main log), in addition to the security log. `oidc_pkce.go` wraps goth's OpenID Connect provider so every login uses PKCE with the `S256` challenge, keeping the code verifier in the goth session.

The HTTP side is in `internal/api/server.go`: `GET /auth/:provider` begins the login (rejecting unknown or unconfigured providers, and answering 503 for an enabled provider that is not registered with goth, for example while OIDC discovery is pending or after it failed) and `GET /auth/:provider/callback` completes it, checks the allowed user list, and starts a session through `StartProviderSession`. The begin step records the "Remember this browser" choice (`?remember=1`) in a short-lived cookie named for the provider (`birdnet_remember_browser_<provider>`, the goth name) that the callback for the same provider reads and clears, so a callback applies only the choice recorded for its own provider.

### Logout

`SecurityAdapter.Logout` calls `EndSession`, which deletes the session record and persists the deletion before it returns and clears the cookie, then calls `gothic.Logout` to drop a leftover handshake session. For OIDC providers the adapter can also build the provider's end-session URL from the ID token kept in the record.

## Local subnet bypass

The bypass is controlled by `Security.AllowSubnetBypass` (`Enabled`, `Subnet` as a comma-separated CIDR list). Two separate checks use it, and both run on the verified client address rather than the address used for logging:

- **Request gate** (`isAllowedSubnetIP`, used by `IsAuthenticationEnabled` and `IsRequestFromAllowedSubnet`): when the bypass is enabled, loopback addresses and addresses inside a configured CIDR need no authentication at all.
- **Local network check** (`IsInLocalSubnet` in `basic.go`, used by `IsUserAuthenticated`): when the bypass is enabled, an address is treated as authenticated if it is IPv6 link-local, or on a network of one of this host's non-loopback interfaces. Inside a container it compares against the host subnet through `conf.IsInHostSubnet` instead.

Requests that pass only the second check still reach the login middleware, which then lets them through in `IsUserAuthenticated`.

### Client address and trusted proxies

`authClientIP` resolves the address through `proxytrust.Checker.AuthClientIP`, driven by `Security.TrustedProxies`. It returns the connection peer when the request has no forwarded client-IP header. When forwarded headers are present, it returns the forwarded address only if the peer is a configured proxy and the headers agree on one non-loopback client; otherwise it returns no address, and no bypass applies. The attribution address that logs and the tunnel label use (`echo.Context.RealIP`) comes from the same `RateLimitClient` resolution as the rate limiters (`apicore.Core.attributionIP`), so forwarded headers that disagree, or that a listed proxy cannot verify, are not recorded: such a request is logged as its peer. A client that an unlisted loopback, link-local or private host names consistently (a lone `CF-Connecting-IP`, `X-Forwarded-For` or `X-Real-IP` with a usable address included) is recorded as named, so such a host can choose the address in the logs. It is still for attribution only and is never used for authentication decisions. The api/v2 rate limiters do not read it: they key each request through `apicore.Core.RateLimitKey`, which uses `proxytrust.Checker.RateLimitClient`. That resolver has four tiers. A peer the limiter cannot believe (public, CGNAT or unparseable) is the client and its headers are ignored. A configured proxy gives the client `AuthClientIP` verifies, and a configured proxy whose headers do not verify is not re-judged by the looser rules: all its unverifiable requests share one bucket. An unlisted loopback, link-local or private proxy gives a client only when its client-IP headers agree (Cloudflare edge hops and private hops are looked past only when `CF-Connecting-IP` is present (a hop inside a range listed in `security.trustedproxies`, such as the `cloudflare` preset, is always looked past), so without the preset a caller inside Cloudflare's ranges that omits it is the client; `True-Client-IP` can only veto), and the key is namespaced under the peer, so such a claim cannot name a direct client's bucket or another proxy's. Anything else is the peer's shared unverified bucket. The login and import elevation routes also limit the connection peer as a whole, which never reads a forwarded header. They skip that peer limit (`apicore.Core.SkipPeerRateLimit`, decided per request from the live list) for a direct request, whose client limiter already keys on the peer, and for a client a proxy verified when that proxy is listed by its own address (`proxytrust.Checker.IsSingleHostProxy`: a bare IP, a /32 or a /128, not inside any wider listed range) and no Cloudflare edge address appears in the peer, `X-Real-IP` or `X-Forwarded-For`. Clients an unlisted proxy named, unverifiable requests, clients a listed range verified and clients a listed proxy verified behind a Cloudflare edge hop keep it. The api/v2 limiter stores (`apicore.NewRateLimiterStore`) cap their entries, per key namespace and overall. At a cap a new key replaces an entry only when that entry's bucket has refilled; otherwise it shares an overflow bucket, so minting keys cannot grow a store without bound or reset a throttled bucket. `proxytrust` also provides `Classify`, which the settings connection check and the throttled log notices use so they match the authentication decision. See the package comment in `proxytrust/proxytrust.go` for the full rules, including the `cloudflare` preset.

## Sessions and token persistence

- **Session store** (`session_store.go`, `setupSessionStore` in `oauth.go`): a gorilla `FilesystemStore` under `<config dir>/sessions`, with the authentication and encryption keys derived from `Security.SessionSecret` by SHA-256. It carries only the OAuth handshake (state and PKCE verifier) of a social sign-in, not the signed-in state. Its cookie lasts `Security.SessionDuration`, defaulting to 7 days, and the files are capped at 1 MB. If the directory cannot be created, an in-memory cookie store with the same cookie options is used. The cookie options are `HttpOnly`, `SameSite=Lax` and `Secure` according to `secureCookies`. The store is wrapped so that clients listed in `Security.PlainHTTPSessionClients`, connecting directly over plain HTTP, get cookies without `Secure`.
- **Browser sessions** (`session_lifetime.go`): every sign-in gets one record, found through the opaque `birdnet_session` cookie. The record holds the remember choice, the expiry and, for a social sign-in, the provider, the identity and the ID token. A remembered session (the "Remember this browser" box ticked) has a persistent cookie and lasts 30 days; an unticked one has a browser-session cookie and an 8 hour idle limit. The cookie is written once, at sign-in, and never re-sent: a remembered cookie gets the longest Max-Age browsers keep (400 days) and the server record, not the cookie, decides when the session ends. The expiry of the record slides: an authenticated request extends it, at most once per 24 hours (remembered) or 15 minutes (unticked). Renewal touches only the server record, so it works on every response, including a WebSocket upgrade. The choice is fixed at sign-in. Every sign-in mints a new token and revokes the browser's previous record. Changing the basic-auth client ID or password ends every session, password and social, including the browser that made the change. `Security.SessionDuration` and `BasicAuth.AccessTokenExp` no longer govern sign-in; the server logs a warning at startup when either differs from its old default.
- **Token persistence** (`setupTokenPersistence`, `saveTokens`, `loadTokens`): authorization codes and session records are written to `tokens.json` in the first config directory (keys `auth_codes`, `sessions` and `credentials_id`), with file mode `0600`, through a temporary file and rename. Saves are serialised and take their snapshot inside the save lock. Records are stored under the SHA-256 of their token, so the file cannot be used to sign in; it does hold the signed-in identity and the OIDC ID token. Expired entries are dropped on load, and `cleanupExpired` removes them every hour. A `tokens.json` from an older build loads with no sessions, so each browser signs in once after an upgrade.

## Redirect and path validation (`util.go`)

- `IsSafePath` accepts only internal paths: it must start with `/` and stay under `MaxSafePathLength`, and rejects `//`, backslashes, `://`, `..`, null bytes and their URL-encoded, double-encoded and triple-encoded forms. The input is normalized to NFKC first so full-width look-alikes do not slip through.
- `IsValidRedirect` is the query-aware check used by the login middleware and the v2 login handler. The path part must pass `IsSafePath`; the query part, bounded by `MaxSafeRedirectLength`, is only checked for CR/LF, encoded nulls and control characters, so legitimate filter queries survive a login round trip.
- `ValidateAuthCallbackRedirect` sanitizes a redirect to a relative path, returning `/` for absolute, protocol-relative or backslash forms and CRLF injection. The v2 callback uses its own equivalent, `ValidateAndSanitizeRedirect`, in `internal/api/v2/auth`.
- `ValidateRedirectURI` compares a provided redirect URI with the configured basic auth one (scheme, host, normalized port, cleaned path, no query or fragment).

## Testing

`securitytest.NewOAuth2ServerForTesting(tb, settings)` builds an `OAuth2Server` around a settings object and restores the global settings snapshot when the test ends. It lives in its own package so `testing` stays out of the production build. `SetTestConfigPath(dir)` redirects the session store to a test directory; reset it with `SetTestConfigPath("")` afterwards. The package also has fuzz tests for the path and redirect helpers, and persistence tests for tokens and the session store.

Run the Go tests through `task test`, which adds the required build tags and CGO flags.
