package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/github"
	gothGoogle "github.com/markbates/goth/providers/google"
	"github.com/markbates/goth/providers/kakao"
	"github.com/markbates/goth/providers/line"
	"github.com/markbates/goth/providers/microsoftonline"
	"github.com/markbates/goth/providers/openidConnect"
	"golang.org/x/oauth2"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/security/proxytrust"
)

var (
	// oidcMu guards the variables below and every write this package makes to
	// goth's provider registry and to lastOIDCDiscoveryErr, so a registration
	// generation check and the write it protects are one step.
	oidcMu sync.Mutex

	// oidcRetriesDisabled blocks new OIDC discovery once application shutdown has
	// begun, so a late provider (re)registration cannot spawn a discovery
	// goroutine that outlives the server. Reset for each new server instance via
	// enableOIDCRetries. A provider reload does not set it.
	oidcRetriesDisabled bool

	// oidcEnableEpoch counts enableOIDCRetries calls, one per server instance. A
	// shutdown that belongs to an older epoch is ignored.
	oidcEnableEpoch uint64

	// oidcGeneration identifies the current provider registration. It is bumped by
	// every registration pass and by shutdown; a discovery attempt of an older
	// generation commits nothing.
	oidcGeneration uint64

	// oidcDiscoveryCancel cancels the discovery goroutine of the current
	// generation, nil when there is none.
	oidcDiscoveryCancel context.CancelFunc

	// oidcActive is the OIDC registration of the current generation, nil when
	// OIDC is not enabled. A pass whose OIDC registration equals it keeps the
	// generation instead of starting a new discovery.
	oidcActive *oidcRegistration

	// oidcProvider is the provider that discovery registered for oidcActive, nil
	// until discovery has succeeded.
	oidcProvider goth.Provider

	// oidcDiscoveryRunning reports whether the discovery goroutine of the current
	// generation is still running.
	oidcDiscoveryRunning bool

	// oidcDiscoveryGoroutines counts running OIDC discovery goroutines. It exists
	// so tests can wait for them to exit; shutdown does not wait on it, because a
	// discovery request in flight cannot be cancelled.
	oidcDiscoveryGoroutines sync.WaitGroup
)

// oidcRetryPolicy is the timing of background OIDC discovery.
type oidcRetryPolicy struct {
	initialBackoff time.Duration
	maxBackoff     time.Duration
	maxDuration    time.Duration
}

// defaultOIDCRetryPolicy returns the production discovery timing.
func defaultOIDCRetryPolicy() oidcRetryPolicy {
	return oidcRetryPolicy{
		initialBackoff: OIDCRetryInitialBackoff,
		maxBackoff:     OIDCRetryMaxBackoff,
		maxDuration:    OIDCRetryMaxDuration,
	}
}

// cancelOIDCDiscoveryLocked cancels and forgets the discovery goroutine of the
// current generation. The caller must hold oidcMu.
func cancelOIDCDiscoveryLocked() {
	if oidcDiscoveryCancel != nil {
		oidcDiscoveryCancel()
		oidcDiscoveryCancel = nil
	}
}

// shutdownOIDCRetriesFor shuts down OIDC discovery for the server instance that
// enableOIDCRetries returned epoch for: it supersedes the current registration
// generation, cancels its discovery and blocks new discovery for the remaining
// lifetime of the instance. It does nothing when a newer instance has enabled
// discovery since, so a late shutdown of an earlier server cannot tear down the
// registration or the discovery of the current one. Called when the server's
// context is cancelled (application shutdown), so discovery cannot be started
// after shutdown has begun (e.g. by a late provider reload).
func shutdownOIDCRetriesFor(epoch uint64) {
	oidcMu.Lock()
	defer oidcMu.Unlock()
	if epoch != oidcEnableEpoch {
		return
	}
	oidcRetriesDisabled = true
	oidcGeneration++
	cancelOIDCDiscoveryLocked()
	oidcActive, oidcProvider, oidcDiscoveryRunning = nil, nil, false
}

// enableOIDCRetries re-enables OIDC discovery for a fresh server instance,
// clearing any disabled state left by a previous instance's shutdown (relevant
// in tests that build servers sequentially). It returns the epoch of this
// instance, which shutdownOIDCRetriesFor compares.
func enableOIDCRetries() uint64 {
	oidcMu.Lock()
	defer oidcMu.Unlock()
	oidcRetriesDisabled = false
	oidcEnableEpoch++
	return oidcEnableEpoch
}

type AuthCode struct {
	Code      string
	ExpiresAt time.Time
}

type AccessToken struct {
	Token     string
	ExpiresAt time.Time
}

// providerAuthConfig holds the configuration for validating a provider's auth session.
// This allows checkProviderAuth to work generically with any OAuth provider.
type providerAuthConfig struct {
	providerName   string // Session key (e.g., ProviderGoogle, ProviderGitHub)
	enabled        bool   // Whether this provider is enabled
	allowedUserIds string // Comma-separated list of allowed user IDs
}

type OAuth2Server struct {
	// settings is the construction-time snapshot. It is unexported so external
	// callers cannot read it directly and observe a stale value: all runtime
	// reads must go through currentSettings() / CurrentSettings(), which resolve
	// the live atomic snapshot (GitHub #3370).
	settings     *conf.Settings
	authCodes    map[string]AuthCode
	accessTokens map[string]AccessToken
	mutex        sync.RWMutex

	GithubConfig *oauth2.Config
	GoogleConfig *oauth2.Config

	// Token persistence
	tokensFile    string
	persistTokens bool

	// Throttling
	throttledMessages map[string]time.Time

	// proxyTrust caches the parsed Security.TrustedProxies list for
	// AuthClientIP, rebuilt only when the list changes.
	proxyTrust proxytrust.Cache
}

// currentSettings returns the latest settings snapshot so security
// configuration changes take effect without recreating the OAuth2Server.
func (s *OAuth2Server) currentSettings() *conf.Settings {
	return conf.CurrentOrFallback(s.settings)
}

// CurrentSettings returns the latest settings snapshot for callers outside the
// security package (e.g. the API auth adapter) so they read live configuration
// instead of the construction-time pointer. See currentSettings.
func (s *OAuth2Server) CurrentSettings() *conf.Settings {
	return s.currentSettings()
}

// authClientIP returns the client address to use for authentication decisions,
// such as the allowed-subnet bypass, or nil when it cannot be verified (callers
// must then grant no address-based access). Unlike echo.Context.RealIP, which
// serves attribution and records a client that any private peer names
// consistently, it honors forwarded headers only from proxies listed in
// settings' Security.TrustedProxies; see
// proxytrust.Checker.AuthClientIP. Callers pass the snapshot they loaded once
// for the whole decision, so changes take effect on the next request without a
// restart and one decision never mixes two configurations. When the subnet
// bypass is enabled and an authentication provider is configured, it logs a
// throttled Info notice naming the peer when forwarded headers kept it from
// finding an address: one for an unlisted proxy that listing would fix
// (proxytrust.StatusUntrustedProxy: a loopback, link-local, private or CGNAT
// peer, never a public scanner), another for a listed proxy whose headers
// cannot be verified (proxytrust.StatusTrustedProxyUnverified, which a client
// may also cause by sending forged copies through a correctly configured
// proxy). Both notices take their decision from proxytrust.Checker.Classify,
// the classifier the settings connection check also uses; the request is
// classified once, and not at all while the notice it could earn is throttled. A nil
// request has no verifiable address.
func (s *OAuth2Server) authClientIP(settings *conf.Settings, r *http.Request) net.IP {
	if r == nil {
		return nil
	}
	checker := s.proxyTrust.ResolveSettings(settings)
	if ip := checker.AuthClientIP(r); ip != nil {
		return ip
	}
	if !bypassNoticeApplies(settings) {
		return nil
	}
	// A listed peer can only earn the listed-proxy notice and any other peer only
	// the unlisted-peer notice; when that one notice is inside its throttle
	// interval, skip classifying.
	noticeKey := unverifiedClientIPLogKey
	if peerIP, _ := proxytrust.PeerAddr(r); checker.IsConfiguredProxy(peerIP) {
		noticeKey = listedProxyUnverifiedLogKey
	}
	if s.noticeThrottled(noticeKey, unverifiedClientIPLogInterval) {
		return nil
	}
	conn := checker.Classify(r)
	switch conn.Status {
	case proxytrust.StatusUntrustedProxy:
		s.logThrottledInfo(unverifiedClientIPLogKey,
			"Subnet bypass skipped: forwarded client-IP headers came from a peer not listed in security.trustedproxies; if this peer is your reverse proxy, add it there",
			unverifiedClientIPLogInterval, logger.String("peer", conn.Peer.String()))
	case proxytrust.StatusTrustedProxyUnverified:
		s.logThrottledInfo(listedProxyUnverifiedLogKey,
			"Subnet bypass skipped: forwarded client-IP headers from a trusted proxy could not be verified: they disagree, repeat a single-value header, carry a malformed or loopback address or a Forwarded header, or every forwarded hop is itself listed in security.trustedproxies",
			unverifiedClientIPLogInterval, logger.String("peer", conn.Peer.String()))
	case proxytrust.StatusDirect, proxytrust.StatusTrustedProxy, proxytrust.StatusUntrustedPublicProxy,
		proxytrust.StatusUntrustedProxyUnverifiable:
		// No notice: nothing to fix, or advice that would not help.
	}
	return nil
}

// isLocalSubnetBypass reports whether the automatic local-network check grants
// r access: the subnet bypass is enabled in settings and the verified client
// address (never the attribution address, so a forged forwarded header cannot
// place a client on the host's network) is on a network of this host. Both are
// read from the one snapshot the caller passes. It also returns that address
// for logging.
func (s *OAuth2Server) isLocalSubnetBypass(settings *conf.Settings, r *http.Request) (net.IP, bool) {
	if !settings.Security.AllowSubnetBypass.Enabled {
		return nil, false
	}
	ip := s.authClientIP(settings, r)
	return ip, IsInLocalSubnet(ip)
}

// unverifiedClientIPLogKey throttles the unverified forwarded address notice.
// One key for all peers keeps the throttle map bounded.
const unverifiedClientIPLogKey = "subnet-bypass-unverified-client-ip"

// listedProxyUnverifiedLogKey throttles the notice for a listed proxy whose
// forwarded headers could not be verified, separately from the unlisted-peer
// notice so one cannot hide the other.
const listedProxyUnverifiedLogKey = "subnet-bypass-listed-proxy-unverified"

// unverifiedClientIPLogInterval is the minimum time between two unverified
// forwarded address notices.
const unverifiedClientIPLogInterval = 10 * time.Minute

// bypassNoticeApplies reports whether a subnet bypass notice can apply: the
// bypass is enabled and an authentication provider is configured (otherwise no
// login is asked for anyway).
func bypassNoticeApplies(settings *conf.Settings) bool {
	return settings != nil && settings.Security.AllowSubnetBypass.Enabled && settings.IsAuthProviderConfigured()
}

// For testing purposes
var testConfigPath string

// Pre-defined errors for token validation
var (
	ErrTokenNotFound = errors.NewStd("token not found")
	ErrTokenExpired  = errors.NewStd("token expired")
)

// NewOAuth2Server creates and initializes an OAuth2Server. The supplied context
// governs background goroutines owned by the server (currently the periodic
// expired-token cleanup); cancelling it stops them, giving the cleanup goroutine
// a proper shutdown path instead of running for the process lifetime.
//
// secureCookies controls the Secure attribute on the session/auth cookies,
// except for clients listed in Security.PlainHTTPSessionClients that connect
// directly over plain HTTP (see plainHTTPClientSessionStore). The caller decides
// it from the effective web-server TLS configuration (see
// api.Config.SessionCookiesSecure), since the security package cannot import
// internal/api.
func NewOAuth2Server(ctx context.Context, secureCookies bool) *OAuth2Server {
	// Re-enable OIDC discovery retries for this instance, clearing any disabled
	// state a previous instance's shutdown left behind (sequential tests).
	epoch := enableOIDCRetries()
	// Use the security logger from the start
	GetLogger().Info("Initializing OAuth2 server")
	settings := conf.GetSettings()

	server := &OAuth2Server{
		settings:     settings,
		authCodes:    make(map[string]AuthCode),
		accessTokens: make(map[string]AccessToken),
	}

	// Validate and potentially fix session secret
	validateSessionSecret(settings)

	// Initialize Gothic with the provided configuration
	InitializeGoth(settings, secureCookies)

	// Set up token persistence
	server.setupTokenPersistence()

	// Clean up expired tokens every hour. The caller's context terminates the
	// goroutine on application shutdown.
	server.StartAuthCleanup(ctx, time.Hour)

	// Stop in-flight OIDC discovery when the application shuts down and block
	// any new one. Discovery is package-global (goth's provider registry is
	// global), so a lightweight watcher on the lifecycle context drives
	// shutdownOIDCRetriesFor, rather than threading a context field through the
	// global provider-init path (which the fatcontext linter forbids). The epoch
	// ties the watcher to this instance: if a newer instance has started by the
	// time ctx ends, the shutdown is ignored.
	go func() {
		<-ctx.Done()
		shutdownOIDCRetriesFor(epoch)
	}()

	GetLogger().Info("OAuth2 server initialization complete")
	return server
}

// validateSessionSecret checks the session secret strength and generates a temporary one if needed
func validateSessionSecret(settings *conf.Settings) {
	if settings.Security.SessionSecret == "" {
		handleEmptySessionSecret(settings)
		return
	}
	if len(settings.Security.SessionSecret) < MinSessionSecretLength {
		handleWeakSessionSecret(settings)
	}
}

// handleEmptySessionSecret generates a temporary secret when none is configured
func handleEmptySessionSecret(settings *conf.Settings) {
	GetLogger().Error("CRITICAL SECURITY WARNING: SessionSecret is empty. A temporary secret will be generated, but this should be fixed in configuration.",
		logger.Bool("debug_mode", settings.WebServer.Debug),
		logger.String("recommendation", "BirdNET-Go will auto-generate a SessionSecret on next restart"))

	settings.Security.SessionSecret = conf.GenerateRandomSecret()
	GetLogger().Info("Generated temporary SessionSecret for this session")

	_ = errors.New(
		errors.NewStd("session secret is empty")).
		Component("security").
		Category(errors.CategoryConfiguration).
		Context("debug_mode", settings.WebServer.Debug).
		Context("action", "auto_generated_temporary_secret").
		Build()
}

// handleWeakSessionSecret logs a warning for weak session secrets
func handleWeakSessionSecret(settings *conf.Settings) {
	GetLogger().Warn("Security Recommendation: SessionSecret is potentially weak",
		logger.Int("current_length", len(settings.Security.SessionSecret)),
		logger.Int("recommended_length", MinSessionSecretLength),
		logger.Bool("debug_mode", settings.WebServer.Debug),
		logger.String("recommendation", "Consider regenerating with a stronger secret"))

	_ = errors.Newf("session secret is potentially weak: %d characters", len(settings.Security.SessionSecret)).
		Component("security").
		Category(errors.CategoryConfiguration).
		Context("current_length", len(settings.Security.SessionSecret)).
		Context("recommended_length", MinSessionSecretLength).
		Context("debug_mode", settings.WebServer.Debug).
		Build()
}

// parseBasicAuthRedirectURI parses and validates the Basic Auth redirect URI from
// the given settings snapshot. It is called per request from the basic-auth handlers
// so that changes to Security.BasicAuth.RedirectURI made via the web UI take effect
// without a restart (issue #3370). Returns nil on a missing or invalid configuration.
func parseBasicAuthRedirectURI(settings *conf.Settings) *url.URL {
	secLog := GetLogger().With(logger.String("uri", settings.Security.BasicAuth.RedirectURI))

	if settings.Security.BasicAuth.RedirectURI == "" {
		secLog.Warn("Basic Auth Redirect URI is not configured. Basic authentication might not function correctly.")
		return nil
	}

	parsedURI, err := url.Parse(settings.Security.BasicAuth.RedirectURI)
	if err != nil {
		secLog.Error("CRITICAL CONFIGURATION ERROR: Failed to parse BasicAuth.RedirectURI. Basic authentication will likely fail.",
			logger.Error(err))
		return nil
	}

	if parsedURI.RawQuery != "" || parsedURI.Fragment != "" {
		secLog.Error("CRITICAL CONFIGURATION ERROR: BasicAuth.RedirectURI must not contain query parameters or fragments.")
		return nil
	}

	secLog.Debug("Validated Basic Auth Redirect URI")
	return parsedURI
}

// setupTokenPersistence configures token persistence for the OAuth2 server
func (s *OAuth2Server) setupTokenPersistence() {
	secLog := GetLogger()

	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		secLog.Warn("Failed to get config paths for token persistence, persistence disabled", logger.Error(err))
		return
	}

	s.tokensFile = filepath.Join(configPaths[0], "tokens.json")
	s.persistTokens = true
	secLog = secLog.With(logger.String("file", s.tokensFile))
	secLog.Info("Token persistence configured")

	if err := os.MkdirAll(filepath.Dir(s.tokensFile), DirPermissions); err != nil {
		secLog.Error("Failed to create directory for token persistence, persistence disabled",
			logger.String("path", filepath.Dir(s.tokensFile)), logger.Error(err))
		s.persistTokens = false
		return
	}

	if err := s.loadTokens(context.Background()); err != nil {
		secLog.Warn("Failed to load persisted tokens", logger.Error(err))
	}
}

// InitializeGoth initializes social authentication providers. secureCookies sets
// the Secure attribute on the session store's cookies, except for configured
// plain-HTTP session clients (see plainHTTPClientSessionStore); the caller
// derives it from the effective web-server TLS configuration
// (api.Config.SessionCookiesSecure).
func InitializeGoth(settings *conf.Settings, secureCookies bool) {
	if settings == nil {
		// settings is a required dependency: setupSessionStore and
		// initializeProviders both dereference it. Fail fast with a clear message
		// instead of a confusing nil dereference deeper in initialization.
		panic("security.InitializeGoth: settings must not be nil")
	}
	GetLogger().Info("Initializing Goth providers")

	// Setup session store (filesystem or fallback to cookie-based)
	setupSessionStore(settings, secureCookies)

	// Initialize OAuth providers
	initializeProviders(settings)
}

// setupSessionStore configures the Gothic session store.
// It attempts to use a filesystem store, falling back to an in-memory cookie store on failure.
// secureCookies sets the Secure attribute on the session cookies, except for
// configured plain-HTTP session clients (see plainHTTPClientSessionStore); it is
// derived by the caller from the effective web-server TLS configuration.
func setupSessionStore(settings *conf.Settings, secureCookies bool) {
	secLog := GetLogger()

	maxAge := DefaultSessionMaxAgeSeconds
	if settings.Security.SessionDuration > 0 {
		maxAge = int(settings.Security.SessionDuration.Seconds())
	}

	// newCookieStoreFallback builds the in-memory fallback store with the same
	// cookie hardening (Secure, HttpOnly, SameSite) as the filesystem store, so a
	// fallback does not silently drop the Secure attribute the caller requested.
	newCookieStoreFallback := func() *sessions.CookieStore {
		store := sessions.NewCookieStore(createSessionKey(settings.Security.SessionSecret))
		store.Options = buildSessionOptions(secureCookies, maxAge)
		return store
	}

	sessionPath, ok := getSessionPath()
	if !ok {
		// Fallback to in-memory store if config paths can't be retrieved
		gothic.Store = plainHTTPClientSessionStore{Store: newCookieStoreFallback(), settings: settings}
		return
	}

	secLog = secLog.With(logger.String("path", sessionPath))

	// Ensure directory exists
	if err := os.MkdirAll(sessionPath, DirPermissions); err != nil {
		secLog.Error("Failed to create session directory, falling back to in-memory cookie store", logger.Error(err))
		gothic.Store = plainHTTPClientSessionStore{Store: newCookieStoreFallback(), settings: settings}
		return
	}

	// Create persistent session store with properly sized keys
	authKey := createSessionKey(settings.Security.SessionSecret)
	encKey := createSessionKey(settings.Security.SessionSecret + "encryption")

	store := sessions.NewFilesystemStore(
		sessionPath,
		authKey,
		encKey,
	)
	gothic.Store = plainHTTPClientSessionStore{Store: store, settings: settings}

	// Configure session store options
	store.Options = buildSessionOptions(secureCookies, maxAge)
	secLog.Info("Filesystem session store configured", logger.Int("max_age_seconds", maxAge), logger.Bool("secure", secureCookies))

	// Set reasonable values for session cookie storage
	store.MaxLength(MaxSessionSizeBytes)
	secLog.Debug("Set session store max length", logger.Int("max_bytes", MaxSessionSizeBytes))
}

// getSessionPath returns the path for session storage and whether it was successfully determined.
func getSessionPath() (string, bool) {
	secLog := GetLogger()

	if testConfigPath != "" {
		secLog.Info("Using test config path for session storage", logger.String("path", testConfigPath))
		return filepath.Join(testConfigPath, "sessions"), true
	}

	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		secLog.Warn("Failed to get config paths for session store, using in-memory cookie store", logger.Error(err))
		return "", false
	}

	sessionPath := filepath.Join(configPaths[0], "sessions")
	secLog.Info("Using filesystem session store", logger.String("path", sessionPath))
	return sessionPath, true
}

// oidcRegistration holds the inputs of one OIDC provider registration, copied out
// of the settings snapshot so the discovery goroutine never reads shared settings.
type oidcRegistration struct {
	issuerURL    string
	discoveryURL string
	clientID     string
	clientSecret string
	redirectURI  string
	scopes       []string
}

// buildGothProviders builds every enabled provider that needs no network call
// from the settings. The first enabled and configured OIDC entry is returned as
// a registration for background discovery instead, because goth's OpenID
// Connect constructor fetches the discovery document.
func buildGothProviders(settings *conf.Settings) (providers []goth.Provider, oidc *oidcRegistration) {
	secLog := GetLogger()
	providers = make([]goth.Provider, 0, InitialProviderCapacity)

	// Compute base URL for redirect URIs if not explicitly configured
	baseURL := settings.Security.OAuthOrigin()

	// Iterate over the OAuthProviders array
	for i := range settings.Security.OAuthProviders {
		providerConfig := &settings.Security.OAuthProviders[i]
		providerLog := secLog.With(logger.String("provider", providerConfig.Provider))

		if !providerConfig.Enabled || providerConfig.ClientID == "" || providerConfig.ClientSecret == "" {
			providerLog.Info("OAuth provider disabled or not configured")
			continue
		}

		// Build redirect URI using goth provider name (e.g., "microsoftonline" for Microsoft)
		redirectURI := providerConfig.RedirectURI
		if redirectURI == "" && baseURL != "" {
			gothProviderName := GetGothProviderName(providerConfig.Provider)
			redirectURI = baseURL + "/auth/" + gothProviderName + "/callback"
		}
		if redirectURI == "" {
			providerLog.Warn("OAuth provider enabled but redirect URI not configured. Set BaseURL or Host in security settings, or configure explicit RedirectURI.")
		}

		providerLog = providerLog.With(logger.String("redirect_uri", redirectURI))

		// Create the appropriate goth provider based on config provider ID
		// Note: Config uses "microsoft" but goth uses "microsoftonline"
		switch providerConfig.Provider {
		case ConfigGoogle:
			providerLog.Info("Enabling Google Auth provider")
			googleProvider := gothGoogle.New(
				providerConfig.ClientID,
				providerConfig.ClientSecret,
				redirectURI,
				"https://www.googleapis.com/auth/userinfo.email", // Scope for email
			)
			googleProvider.SetAccessType("offline")
			providers = append(providers, googleProvider)

		case ConfigGitHub:
			providerLog.Info("Enabling GitHub Auth provider")
			providers = append(providers, github.New(
				providerConfig.ClientID,
				providerConfig.ClientSecret,
				redirectURI,
				"user:email", // Scope for email
			))

		case ConfigMicrosoft:
			providerLog.Info("Enabling Microsoft Account Auth provider")
			providers = append(providers, microsoftonline.New(
				providerConfig.ClientID,
				providerConfig.ClientSecret,
				redirectURI,
				"user.read", // Scope for email/profile
			))

		case ConfigLine:
			providerLog.Info("Enabling LINE Auth provider")
			providers = append(providers, line.New(
				providerConfig.ClientID,
				providerConfig.ClientSecret,
				redirectURI,
				"profile", "openid", "email", // LINE login scopes
			))

		case ConfigKakao:
			providerLog.Info("Enabling Kakao Auth provider")
			// Kakao requires scopes to be configured in the Kakao Developer Console,
			// not passed via OAuth request. No scopes parameter needed here.
			providers = append(providers, kakao.New(
				providerConfig.ClientID,
				providerConfig.ClientSecret,
				redirectURI,
			))

		case ConfigOIDC:
			if oidc != nil {
				providerLog.Warn("Only one OIDC provider can be registered, skipping additional OIDC entry")
				continue
			}
			providerLog.Info("Enabling OIDC provider")
			scopes := slices.Clone(providerConfig.Scopes)
			if len(scopes) == 0 {
				scopes = []string{"openid", "profile", "email"}
			}
			// Ensure "openid" scope is always present (OIDC spec requirement)
			if !slices.Contains(scopes, "openid") {
				scopes = append([]string{"openid"}, scopes...)
			}
			oidc = &oidcRegistration{
				issuerURL:    providerConfig.IssuerURL,
				discoveryURL: oidcDiscoveryURL(providerConfig.IssuerURL),
				clientID:     providerConfig.ClientID,
				clientSecret: providerConfig.ClientSecret,
				redirectURI:  redirectURI,
				scopes:       scopes,
			}

		default:
			providerLog.Warn("Unknown OAuth provider type, skipping")
		}
	}

	return providers, oidc
}

// sameOIDCRegistration reports whether two OIDC registrations build the same
// provider: every input of discovery and of the provider is equal.
func sameOIDCRegistration(a, b *oidcRegistration) bool {
	return a != nil && b != nil &&
		a.issuerURL == b.issuerURL &&
		a.clientID == b.clientID &&
		a.clientSecret == b.clientSecret &&
		a.redirectURI == b.redirectURI &&
		slices.Equal(a.scopes, b.scopes)
}

// beginProviderRegistration runs one registration pass. Under oidcMu it replaces
// goth's whole registry with providers, so no provider built from older settings
// stays registered, and decides what happens to OIDC:
//   - oidc equals the registration of the current generation: the generation
//     stays. A provider that discovery already registered is carried into the new
//     registry, and a discovery still running is left running, so an edit of
//     another provider neither interrupts OIDC login nor repeats discovery.
//   - otherwise the previous generation is superseded (its discovery cancelled),
//     the recorded discovery failure is forgotten, and when oidc is set and
//     retries are not disabled the context and generation for a new discovery
//     goroutine are returned, with start true.
func beginProviderRegistration(providers []goth.Provider, oidc *oidcRegistration) (ctx context.Context, gen uint64, start bool) {
	oidcMu.Lock()
	defer oidcMu.Unlock()

	if sameOIDCRegistration(oidcActive, oidc) && (oidcProvider != nil || oidcDiscoveryRunning) {
		if oidcProvider != nil {
			providers = append(providers, oidcProvider)
		}
		goth.ReplaceProviders(providers...)
		return nil, oidcGeneration, false
	}

	oidcGeneration++
	gen = oidcGeneration
	cancelOIDCDiscoveryLocked()
	clearOIDCDiscoveryFailure()
	goth.ReplaceProviders(providers...)
	oidcActive, oidcProvider, oidcDiscoveryRunning = nil, nil, false

	if oidc == nil || oidcRetriesDisabled {
		return nil, gen, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	oidcDiscoveryCancel = cancel
	oidcActive = oidc
	oidcDiscoveryRunning = true
	return ctx, gen, true
}

// markOIDCDiscoveryStopped records that the discovery goroutine of generation
// gen has exited, so a later pass with the same OIDC registration starts
// discovery again instead of waiting for it.
func markOIDCDiscoveryStopped(gen uint64) {
	oidcMu.Lock()
	defer oidcMu.Unlock()
	if gen == oidcGeneration {
		oidcDiscoveryRunning = false
	}
}

// finishOIDCDiscovery commits the result of one discovery attempt of generation
// gen. It reports false, and writes nothing, when a newer registration pass or
// shutdown has superseded the generation. Otherwise it records the failure, or
// registers the provider and clears the recorded failure, and reports true.
func finishOIDCDiscovery(gen uint64, reg *oidcRegistration, provider *openidConnect.Provider, err error) bool {
	oidcMu.Lock()
	defer oidcMu.Unlock()

	if gen != oidcGeneration {
		return false
	}
	if err != nil {
		recordOIDCDiscoveryFailure(reg.discoveryURL, err)
		return true
	}
	registered := newPKCEOIDCProvider(provider)
	goth.UseProviders(registered)
	oidcProvider = registered
	// Register first, clear second: a reader that sees the provider
	// missing may still read the old failure, never the reverse.
	clearOIDCDiscoveryFailure()
	return true
}

// endOIDCDiscoveryWindow ends the discovery of generation gen at the end of its
// retry window, under oidcMu. The generation's discovery is marked stopped in
// the same step, so a pass that arrives afterwards starts discovery again
// instead of finding it "running". It reports whether gen is still the current
// generation.
func endOIDCDiscoveryWindow(gen uint64) (current bool) {
	oidcMu.Lock()
	defer oidcMu.Unlock()
	current = gen == oidcGeneration
	if current {
		oidcDiscoveryRunning = false
	}
	return current
}

// oidcHTTPClient is the one client for every request to the identity provider:
// discovery, token exchange, userinfo and token refresh. Its Timeout bounds
// each request. The nil Transport is http.DefaultTransport, which keeps the
// proxy environment variables and the system root store, including the
// SSL_CERT_FILE and SSL_CERT_DIR overrides, so trust is not changed.
var oidcHTTPClient = &http.Client{Timeout: OIDCHTTPTimeout}

// discoverOIDC fetches the discovery document and builds the goth OIDC provider
// with oidcHTTPClient. ctx bounds the discovery request together with the
// client timeout. It is the only place that calls goth's OpenID Connect
// constructor.
func discoverOIDC(ctx context.Context, reg *oidcRegistration) (*openidConnect.Provider, error) {
	return openidConnect.NewCustomisedHttpClientContext(
		ctx,
		oidcHTTPClient,
		"", // empty name keeps goth's default, ProviderOIDC
		reg.clientID,
		reg.clientSecret,
		reg.redirectURI,
		reg.discoveryURL,
		reg.scopes...,
	)
}

// startOIDCDiscovery starts the background OIDC discovery of registration
// generation gen. The first attempt runs at once; failed attempts are retried
// with exponential backoff until policy.maxDuration has passed. Cancelling ctx
// and the end of the retry window both stop the goroutine, also in the middle
// of a request to the identity provider. Results are committed through
// finishOIDCDiscovery, so an attempt that a newer generation has superseded
// writes nothing, and a cancelled attempt is not recorded as a failure.
// Returns a channel that is closed when the goroutine exits.
func startOIDCDiscovery(ctx context.Context, gen uint64, reg *oidcRegistration, policy oidcRetryPolicy) <-chan struct{} {
	secLog := GetLogger().With(logger.String("provider", ConfigOIDC))
	done := make(chan struct{})
	// windowCtx ends with ctx or when the retry window is over, whichever comes
	// first, so the window also cuts a request in flight.
	windowCtx, cancelWindow := context.WithTimeout(ctx, policy.maxDuration)

	oidcDiscoveryGoroutines.Go(func() {
		defer close(done)
		defer cancelWindow()
		defer markOIDCDiscoveryStopped(gen)
		backoff := policy.initialBackoff
		var lastErr error
		// A single reusable timer (Reset per iteration) instead of time.After
		// inside the loop, so an early ctx cancellation does not leave an
		// unexpired timer per backoff round waiting to be garbage collected.
		// It starts at zero so the first attempt runs at once.
		timer := time.NewTimer(0)
		defer timer.Stop()
		for attempt := 1; ; attempt++ {
			// Prioritize cancellation: a single select with both ctx.Done() and
			// timer.C ready picks pseudo-randomly, so on supersession or shutdown
			// the timer case could win and start one more (uncancellable)
			// discovery request. Check ctx first, then block on the timer.
			select {
			case <-ctx.Done():
				secLog.Info("OIDC discovery canceled")
				return
			default:
			}

			// The first attempt always runs: with a window that is already over
			// the select would otherwise pick at random between the window and
			// the zero timer, and could skip discovery without a recorded cause.
			windowOver := windowCtx.Done()
			if attempt == 1 {
				windowOver = nil
			}
			select {
			case <-windowOver:
				if ctx.Err() == nil {
					endOIDCRetryWindow(gen, reg, lastErr, policy)
					return
				}
				secLog.Info("OIDC discovery canceled")
				return
			case <-timer.C:
			}

			if attempt > 1 {
				secLog.Info("Retrying OIDC discovery",
					logger.CredentialURL("issuer_url", reg.issuerURL),
					logger.Duration("backoff", backoff))
			}

			oidcProvider, err := discoverOIDC(windowCtx, reg)
			// A cancelled attempt is not a failure: skip recording and logging.
			if ctx.Err() != nil {
				secLog.Info("OIDC discovery canceled")
				return
			}
			// The retry window ended during the attempt: the request was cut, so
			// its error is not a discovery failure. It is recorded only when no
			// real cause exists yet, and the window is reported as exhausted.
			if err != nil && windowCtx.Err() != nil {
				if lastErr == nil {
					lastErr = redactDiscoveryErr(err)
					finishOIDCDiscovery(gen, reg, nil, err)
				}
				endOIDCRetryWindow(gen, reg, lastErr, policy)
				return
			}
			if !finishOIDCDiscovery(gen, reg, oidcProvider, err) {
				secLog.Info("OIDC discovery result discarded, a newer provider registration or shutdown superseded it",
					logger.CredentialURL("issuer_url", reg.issuerURL))
				return
			}

			if err == nil {
				warnOnOIDCIssuerMismatch(reg.issuerURL, oidcProvider.OpenIDConfig.Issuer, reg.discoveryURL)
				if attempt == 1 {
					secLog.Info("OIDC provider registered",
						logger.CredentialURL("issuer_url", reg.issuerURL))
				} else {
					logOIDCEvent(logger.LogLevelInfo, "OIDC provider registered after background retry",
						logger.CredentialURL("issuer_url", reg.issuerURL),
						logger.CredentialURL("discovery_url", reg.discoveryURL))
				}
				return
			}

			lastErr = redactDiscoveryErr(err)
			if attempt == 1 {
				logOIDCEvent(logger.LogLevelError, "OIDC discovery failed, OIDC login is unavailable until a retry succeeds",
					logger.CredentialURL("issuer_url", reg.issuerURL),
					logger.CredentialURL("discovery_url", reg.discoveryURL),
					logger.Error(lastErr),
					logger.Duration("retry_for", policy.maxDuration))
			} else {
				secLog.Warn("OIDC discovery retry failed",
					logger.Error(lastErr),
					logger.Duration("next_backoff", backoff))
			}
			timer.Reset(backoff)
			backoff = min(backoff*OIDCRetryBackoffFactor, policy.maxBackoff)
		}
	})

	return done
}

// endOIDCRetryWindow ends the discovery of generation gen at the end of its
// retry window and, when gen is still current, logs the exhaustion with the last
// real cause.
func endOIDCRetryWindow(gen uint64, reg *oidcRegistration, lastErr error, policy oidcRetryPolicy) {
	if endOIDCDiscoveryWindow(gen) {
		logOIDCRetryExhausted(reg.issuerURL, reg.discoveryURL, lastErr, policy.maxDuration)
	}
}

// initializeProviders registers the OAuth providers (Google, GitHub, Microsoft,
// LINE, Kakao, OIDC) from the OAuthProviders array, which is populated either
// from new config or from migration of legacy provider fields. Every call
// replaces goth's whole registry, so providers that were disabled, removed or
// built from older settings are no longer served. OIDC discovery runs in a
// background goroutine of the new registration generation. It returns the done
// channel of a goroutine this call started. It returns nil when no goroutine was
// started: OIDC is not enabled, retries are disabled by shutdown, or the OIDC
// settings are unchanged and the provider is registered or its discovery is
// still running.
func initializeProviders(settings *conf.Settings) <-chan struct{} {
	secLog := GetLogger()
	secLog.Info("Configuring Goth providers")

	providers, oidc := buildGothProviders(settings)
	ctx, gen, start := beginProviderRegistration(providers, oidc)

	if len(providers) > 0 {
		secLog.Info("Goth providers initialized", logger.Int("count", len(providers)))
	} else if oidc == nil {
		secLog.Warn("No Goth providers enabled or configured")
	}

	if !start {
		return nil
	}
	return startOIDCDiscovery(ctx, gen, oidc, defaultOIDCRetryPolicy())
}

// ReloadOAuthProviders re-registers the OAuth providers from settings after a
// provider setting changed, so the change takes effect without a restart. It
// replaces goth's whole registry and, unless the OIDC settings are unchanged,
// restarts OIDC discovery in the background, so it performs no network I/O and
// does not wait for the identity provider.
// The session store is left untouched, so signed-in sessions survive. It does
// nothing but log when settings is nil.
func ReloadOAuthProviders(settings *conf.Settings) {
	if settings == nil {
		GetLogger().Warn("OAuth provider reload skipped: settings not available")
		return
	}
	GetLogger().Info("Reloading OAuth providers")
	initializeProviders(settings)
}

// createSessionKey creates a key of the proper length for AES encryption from a seed string
// AES requires keys of exactly 16, 24, or 32 bytes
func createSessionKey(seed string) []byte {
	// Create a SHA-256 hash of the seed (32 bytes, perfect for AES-256)
	hasher := sha256.New()
	hasher.Write([]byte(seed))
	return hasher.Sum(nil)
}

// SetTestConfigPath sets a test path for testing session persistence
// It should be called before InitializeGoth and reset after the test
func SetTestConfigPath(path string) {
	GetLogger().Debug("Setting test config path", logger.String("path", path))
	testConfigPath = path
}

// IsUserAuthenticated checks if the user is authenticated
func (s *OAuth2Server) IsUserAuthenticated(c echo.Context) bool {
	secLog := GetLogger().With(logger.String("client_ip", c.RealIP()))
	secLog.Debug("Checking user authentication status")

	if authIP, ok := s.isLocalSubnetBypass(s.currentSettings(), c.Request()); ok {
		secLog.Info("User authenticated: request from local subnet (subnet bypass enabled)",
			logger.String("auth_ip", authIP.String()))
		return true
	}

	if s.checkBasicAuthToken(c.Request(), secLog) {
		return true
	}

	if s.checkSocialAuthSessions(c.Request(), secLog) {
		return true
	}

	secLog.Info("User not authenticated")
	return false
}

// checkBasicAuthToken validates the basic auth access token from the session
func (s *OAuth2Server) checkBasicAuthToken(r *http.Request, log SecurityLogger) bool {
	token, err := gothic.GetFromSession("access_token", r)
	if err != nil || token == "" {
		return false
	}

	log.Debug("Found access_token in session, validating...")
	if s.ValidateAccessToken(token) == nil {
		log.Info("User authenticated: valid access_token found in session")
		return true
	}
	log.Warn("Invalid or expired access_token found in session")
	return false
}

// checkSocialAuthSessions checks for valid OAuth authentication sessions.
// It first tries a direct lookup using the stored auth_provider session key,
// then falls back to iterating all configured providers for backward compatibility.
func (s *OAuth2Server) checkSocialAuthSessions(r *http.Request, log SecurityLogger) bool {
	userId, err := gothic.GetFromSession("userId", r)
	if err != nil {
		log.Debug("No userId found in session")
	} else {
		log.Debug("Found userId in session, checking provider sessions")
	}

	// Try direct lookup via stored auth_provider session key first
	if s.checkSocialAuthDirect(r, userId, log) {
		return true
	}

	// Fall back to iterating all providers for backward compatibility
	// (existing sessions may not have the auth_provider key)
	return s.checkSocialAuthFallback(r, userId, log)
}

// checkSocialAuthDirect checks for a valid session using the stored auth_provider key.
// Returns false if the key is missing or the provider session is invalid.
func (s *OAuth2Server) checkSocialAuthDirect(r *http.Request, userId string, log SecurityLogger) bool {
	activeProvider, err := gothic.GetFromSession(SessionKeyAuthProvider, r)
	if err != nil || activeProvider == "" {
		log.Debug("No auth_provider key in session, will try fallback iteration")
		return false
	}

	log.Debug("Found active auth provider in session", logger.String("provider", activeProvider))

	// Look up the config provider for this goth provider name
	configProvider := gothToConfigProvider(activeProvider)
	provider := s.currentSettings().GetOAuthProvider(configProvider)
	if provider == nil || !provider.Enabled {
		log.Debug("Active auth provider not enabled or not found in config", logger.String("provider", activeProvider))
		return false
	}

	return s.checkProviderAuth(r, userId, log, providerAuthConfig{
		providerName:   activeProvider,
		enabled:        provider.Enabled,
		allowedUserIds: provider.UserID,
	})
}

// checkSocialAuthFallback iterates over all configured providers to find a valid session.
// This handles sessions created before the auth_provider key was introduced.
func (s *OAuth2Server) checkSocialAuthFallback(r *http.Request, userId string, log SecurityLogger) bool {
	settings := s.currentSettings()
	for configProvider, gothProvider := range ConfigToGothProvider {
		provider := settings.GetOAuthProvider(configProvider)
		if provider == nil || !provider.Enabled {
			continue
		}

		if s.checkProviderAuth(r, userId, log, providerAuthConfig{
			providerName:   gothProvider,
			enabled:        provider.Enabled,
			allowedUserIds: provider.UserID,
		}) {
			return true
		}
	}

	return false
}

// checkProviderAuth validates an OAuth provider session generically.
// This is the shared implementation used by all OAuth provider auth checks.
func (s *OAuth2Server) checkProviderAuth(r *http.Request, userId string, log SecurityLogger, cfg providerAuthConfig) bool {
	if !cfg.enabled {
		return false
	}

	sessionUser, err := gothic.GetFromSession(cfg.providerName, r)
	if err != nil || sessionUser == "" {
		return false
	}

	log.Debug("Found provider session key", logger.String("provider", cfg.providerName))
	// Check both the session userId (typically email) and the provider-specific session user
	// (typically the provider's UserID/sub claim). This handles cases where the admin configured
	// the allowed user list using either format.
	if isValidUserId(cfg.allowedUserIds, userId) || isValidUserId(cfg.allowedUserIds, sessionUser) {
		log.Info("User authenticated: valid session found for allowed user ID", logger.String("provider", cfg.providerName))
		return true
	}
	// The allowlist holds email addresses and user IDs, so it is never logged.
	log.Warn("Provider session found, but userId does not match allowed IDs", logger.String("provider", cfg.providerName))
	return false
}

func isValidUserId(configuredIds, providedId string) bool {
	if configuredIds == "" || providedId == "" {
		return false
	}

	// Trim whitespace from the ID we are checking
	trimmedProvidedId := strings.TrimSpace(providedId)
	if trimmedProvidedId == "" {
		return false // Don't match empty string after trimming
	}

	// Split configured IDs and trim spaces from each allowed ID once upfront
	allowedIdsRaw := strings.Split(configuredIds, ",")
	allowedIdsTrimmed := make([]string, 0, len(allowedIdsRaw))
	for _, allowedId := range allowedIdsRaw {
		trimmed := strings.TrimSpace(allowedId)
		if trimmed != "" { // Avoid adding empty strings to the comparison list
			allowedIdsTrimmed = append(allowedIdsTrimmed, trimmed)
		}
	}

	// Compare the provided ID against the pre-trimmed list using case-insensitive comparison
	for _, allowedId := range allowedIdsTrimmed {
		if strings.EqualFold(allowedId, trimmedProvidedId) {
			return true
		}
	}

	return false
}

// GenerateAuthCode generates a new authorization code
func (s *OAuth2Server) GenerateAuthCode() string {
	secLog := GetLogger()
	secLog.Debug("Generating new authorization code")

	code := make([]byte, AuthCodeByteLength)
	rand.Read(code)
	authCode := base64.URLEncoding.EncodeToString(code)

	s.mutex.Lock()
	defer s.mutex.Unlock()

	expiresAt := time.Now().Add(s.currentSettings().Security.BasicAuth.AuthCodeExp)
	s.authCodes[authCode] = AuthCode{
		Code:      authCode,
		ExpiresAt: expiresAt,
	}
	// Do not log the authCode itself
	secLog.Info("Generated and stored new authorization code", logger.Time("expires_at", expiresAt))
	go s.persistTokensIfEnabled() // Persist changes
	return authCode
}

// ExchangeAuthCode exchanges an authorization code for an access token
// It now accepts a context, although it's not directly used for internal operations yet.
func (s *OAuth2Server) ExchangeAuthCode(ctx context.Context, code string) (string, error) {
	// Do not log the code
	secLog := GetLogger().With(logger.String("operation", "ExchangeAuthCode"))

	// Fast-fail if the client already gave up
	if err := ctx.Err(); err != nil {
		secLog.Warn("Context cancelled before acquiring lock", logger.Error(err))
		return "", err
	}

	secLog.Debug("Attempting to exchange authorization code")
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Re-check context after acquiring the lock
	if err := ctx.Err(); err != nil {
		secLog.Warn("Context cancelled after acquiring lock", logger.Error(err))
		return "", err // Mutex is released by defer
	}

	authCode, ok := s.authCodes[code]
	if !ok {
		secLog.Warn("Authorization code not found")
		return "", errors.NewStd("authorization code not found")
	}

	if time.Now().After(authCode.ExpiresAt) {
		secLog.Warn("Authorization code expired", logger.Time("expired_at", authCode.ExpiresAt))
		delete(s.authCodes, code)     // Clean up expired code
		go s.persistTokensIfEnabled() // Persist removal
		return "", errors.NewStd("authorization code expired")
	}

	// Generate access token
	tokenBytes := make([]byte, AccessTokenByteLength)
	rand.Read(tokenBytes)
	accessToken := base64.URLEncoding.EncodeToString(tokenBytes)
	expiresAt := time.Now().Add(s.currentSettings().Security.BasicAuth.AccessTokenExp)

	s.accessTokens[accessToken] = AccessToken{
		Token:     accessToken,
		ExpiresAt: expiresAt,
	}

	// Invalidate the auth code after use
	delete(s.authCodes, code)

	// Do not log the accessToken
	secLog.Info("Exchanged authorization code for new access token", logger.Time("expires_at", expiresAt))
	go s.persistTokensIfEnabled() // Persist changes
	return accessToken, nil
}

// ValidateAccessToken checks if an access token is valid and returns an error if not.
func (s *OAuth2Server) ValidateAccessToken(token string) error {
	// Do not log the token
	secLog := GetLogger()
	secLog.Debug("Validating access token")

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	accessToken, ok := s.accessTokens[token]
	if !ok {
		secLog.Debug("Access token not found")
		return ErrTokenNotFound // Return specific error
	}

	if time.Now().After(accessToken.ExpiresAt) {
		secLog.Debug("Access token expired", logger.Time("expired_at", accessToken.ExpiresAt))
		// No need to delete here, cleanup routine handles it
		return ErrTokenExpired // Return specific error
	}

	secLog.Debug("Access token is valid")
	return nil // Return nil on success
}

// IsAuthenticationEnabled reports whether r must authenticate: an
// authentication provider is configured and r does not qualify for the
// allowed-subnet bypass. It takes the request rather than an address so the
// bypass is always decided on the verified client address (authClientIP), never
// on a caller-supplied one such as echo.Context.RealIP.
func (s *OAuth2Server) IsAuthenticationEnabled(r *http.Request) bool {
	settings := s.currentSettings()
	return s.isAuthenticationEnabledFor(settings, s.bypassClientIP(settings, r))
}

// isAuthenticationEnabledFor is IsAuthenticationEnabled for an already
// resolved client address ("" when there is none), decided on settings.
func (s *OAuth2Server) isAuthenticationEnabledFor(settings *conf.Settings, ip string) bool {
	authLog := GetLogger().With(logger.String("ip", ip))
	authLog.Debug("Checking if authentication is enabled for IP")
	if s.isAllowedSubnetIP(settings, ip) {
		authLog.Info("Authentication bypassed: request from allowed subnet")
		return false // Authentication not required for allowed subnets
	}

	// Both values are read for the log line only. The decision itself goes
	// through conf.Settings.IsAuthProviderConfigured so that this middleware and the
	// pprof gate cannot disagree about what "authentication is configured"
	// means: the gate falls back to its generated token when the predicate is
	// false, and no token is minted when it is true, so a third authentication
	// mechanism learned by only one copy would lock a legitimate admin out.
	basicEnabled := settings.Security.BasicAuth.Enabled
	enabledOAuthProviders := settings.GetEnabledOAuthProviders()

	if settings.IsAuthProviderConfigured() {
		authLog.Info("Authentication required: at least one provider enabled and IP not in allowed subnet",
			logger.Bool("basic_enabled", basicEnabled),
			logger.Any("oauth_providers", enabledOAuthProviders),
		)
		return true
	}
	authLog.Info("Authentication not enabled: no providers configured and IP not in allowed subnet")
	return false
}

// IsRequestFromAllowedSubnet reports whether r qualifies for the allowed-subnet
// bypass: the bypass is enabled and the verified client address (authClientIP)
// is loopback or inside a configured subnet. Like IsAuthenticationEnabled it
// takes the request so no caller can decide the bypass on an unverified address.
func (s *OAuth2Server) IsRequestFromAllowedSubnet(r *http.Request) bool {
	settings := s.currentSettings()
	return s.isAllowedSubnetIP(settings, s.bypassClientIP(settings, r))
}

// bypassClientIP returns the verified client address as a string for the
// allowed-subnet check, or "" when the bypass is disabled (the address is then
// never needed, so it is not resolved) or no address could be verified.
func (s *OAuth2Server) bypassClientIP(settings *conf.Settings, r *http.Request) string {
	if !settings.Security.AllowSubnetBypass.Enabled {
		return ""
	}
	if ip := s.authClientIP(settings, r); ip != nil {
		return ip.String()
	}
	return ""
}

// isAllowedSubnetIP checks whether an already resolved client address is
// within the allowed subnets of settings.
func (s *OAuth2Server) isAllowedSubnetIP(settings *conf.Settings, ipStr string) bool {
	authLog := GetLogger().With(logger.String("ip", ipStr))
	authLog.Debug("Checking if IP is in allowed subnet")

	// Check if subnet bypass is enabled first
	if !settings.Security.AllowSubnetBypass.Enabled {
		authLog.Debug("Allowed subnet check: subnet bypass is disabled in settings")
		return false
	}

	if ipStr == "" {
		authLog.Debug("Allowed subnet check: empty IP string")
		return false
	}

	ip := parseIPWithZone(ipStr)
	if ip == nil {
		authLog.Warn("Failed to parse IP address for allowed subnet check", logger.String("ip_string", ipStr))
		return false
	}

	// Check loopback explicitly - often implicitly allowed
	if ip.IsLoopback() {
		authLog.Debug("Allowed subnet check: IP is loopback")
		return true
	}

	// The allowedSubnets string is expected to be a comma-separated list of CIDR ranges.
	allowedSubnetsStr := settings.Security.AllowSubnetBypass.Subnet
	if allowedSubnetsStr == "" {
		authLog.Debug("Allowed subnet check: no allowed subnets configured (subnet string is empty)")
		return false
	}

	allowedSubnets := strings.Split(allowedSubnetsStr, ",")
	if len(allowedSubnets) == 0 {
		// This case should ideally not happen if the string is not empty, but check defensively
		authLog.Debug("Allowed subnet check: no allowed subnets configured (split result is empty)")
		return false
	}

	for _, cidr := range allowedSubnets {
		trimmedCIDR := strings.TrimSpace(cidr)
		if trimmedCIDR == "" {
			continue // Skip empty entries
		}
		_, subnet, err := net.ParseCIDR(trimmedCIDR)
		if err != nil {
			authLog.Warn("Failed to parse allowed subnet CIDR", logger.String("cidr", trimmedCIDR), logger.Error(err))
			continue
		}
		authLog.Debug("Checking against allowed subnet", logger.String("cidr", trimmedCIDR))
		if subnet.Contains(ip) {
			authLog.Debug("IP is within allowed subnet", logger.String("cidr", trimmedCIDR))
			return true
		}
	}

	authLog.Debug("IP is not in any allowed subnet")
	return false
}

// persistTokensIfEnabled saves tokens if persistence is enabled
func (s *OAuth2Server) persistTokensIfEnabled() {
	if s.persistTokens {
		// Use a short background context for saving, as it runs in a goroutine
		ctx, cancel := context.WithTimeout(context.Background(), TokenSaveTimeout)
		defer cancel()
		if err := s.saveTokens(ctx); err != nil {
			// Throttle logging for repeated save errors
			s.logThrottledError("save_tokens_error", "Failed to save tokens", err, ThrottleLogInterval)
		}
	}
}

// loadTokens loads tokens from the persistence file
func (s *OAuth2Server) loadTokens(ctx context.Context) error {
	secLog := GetLogger().With(logger.String("file", s.tokensFile))

	if !s.persistTokens {
		secLog.Debug("Token persistence is disabled, skipping load")
		return nil
	}

	secLog.Info("Attempting to load tokens from file")

	// Check context before potentially long file read
	select {
	case <-ctx.Done():
		secLog.Warn("Context cancelled before loading tokens", logger.Error(ctx.Err()))
		return ctx.Err()
	default:
	}

	// Read file outside the lock
	data, err := os.ReadFile(s.tokensFile)
	if err != nil {
		if os.IsNotExist(err) {
			secLog.Info("Token file does not exist, skipping load")
			return nil // Not an error if the file doesn't exist yet
		}
		secLog.Error("Failed to read token file", logger.Error(err))
		return fmt.Errorf("failed to read token file %s: %w", s.tokensFile, err)
	}

	if len(data) == 0 {
		secLog.Info("Token file is empty, skipping load")
		return nil // Empty file is fine
	}

	// Unmarshal data outside the lock
	var storedData struct {
		AuthCodes    map[string]AuthCode    `json:"auth_codes"`
		AccessTokens map[string]AccessToken `json:"access_tokens"`
	}

	if err := json.Unmarshal(data, &storedData); err != nil {
		secLog.Error("Failed to unmarshal token data from file", logger.Error(err))
		return fmt.Errorf("failed to unmarshal token data from %s: %w", s.tokensFile, err)
	}

	// Acquire lock only to update internal maps
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Restore non-expired tokens
	loadedAuthCodes := 0
	now := time.Now()
	for code, authCode := range storedData.AuthCodes {
		if now.Before(authCode.ExpiresAt) {
			s.authCodes[code] = authCode
			loadedAuthCodes++
		} else {
			secLog.Debug("Ignoring expired auth code during load", logger.Time("expired_at", authCode.ExpiresAt))
		}
	}

	loadedAccessTokens := 0
	for token, accessToken := range storedData.AccessTokens {
		if now.Before(accessToken.ExpiresAt) {
			s.accessTokens[token] = accessToken
			loadedAccessTokens++
		} else {
			secLog.Debug("Ignoring expired access token during load", logger.Time("expired_at", accessToken.ExpiresAt))
		}
	}

	secLog.Info("Successfully loaded tokens from file",
		logger.Int("auth_codes_loaded", loadedAuthCodes),
		logger.Int("access_tokens_loaded", loadedAccessTokens),
	)
	return nil
}

// saveTokens saves the current tokens to the persistence file
func (s *OAuth2Server) saveTokens(ctx context.Context) error {
	secLog := GetLogger().With(logger.String("file", s.tokensFile))

	if !s.persistTokens {
		secLog.Debug("Token persistence is disabled, skipping save")
		return nil
	}

	secLog.Debug("Attempting to save tokens to file")
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Create copies to avoid holding lock during marshaling/writing
	authCodesCopy := make(map[string]AuthCode, len(s.authCodes))
	maps.Copy(authCodesCopy, s.authCodes)
	accessTokensCopy := make(map[string]AccessToken, len(s.accessTokens))
	maps.Copy(accessTokensCopy, s.accessTokens)

	// Check context before marshaling/writing
	select {
	case <-ctx.Done():
		secLog.Warn("Context cancelled before saving tokens", logger.Error(ctx.Err()))
		return ctx.Err()
	default:
	}

	storedData := struct {
		AuthCodes    map[string]AuthCode    `json:"auth_codes"`
		AccessTokens map[string]AccessToken `json:"access_tokens"`
	}{
		AuthCodes:    authCodesCopy,
		AccessTokens: accessTokensCopy,
	}

	data, err := json.MarshalIndent(storedData, "", "  ")
	if err != nil {
		secLog.Error("Failed to marshal tokens for persistence", logger.Error(err))
		return fmt.Errorf("failed to marshal tokens: %w", err)
	}

	// Write atomically if possible (write to temp file, then rename)
	tempFile := s.tokensFile + ".tmp"
	if err := os.WriteFile(tempFile, data, FilePermissions); err != nil {
		secLog.Error("Failed to write tokens to temporary file", logger.String("temp_file", tempFile), logger.Error(err))
		return fmt.Errorf("failed to write tokens to temp file %s: %w", tempFile, err)
	}

	if err := os.Rename(tempFile, s.tokensFile); err != nil {
		secLog.Error("Failed to rename temporary token file to final destination", logger.String("temp_file", tempFile), logger.Error(err))
		// Attempt to clean up temp file
		_ = os.Remove(tempFile)
		return fmt.Errorf("failed to rename temp token file %s to %s: %w", tempFile, s.tokensFile, err)
	}

	secLog.Debug("Successfully saved tokens to file",
		logger.Int("auth_codes_saved", len(authCodesCopy)),
		logger.Int("access_tokens_saved", len(accessTokensCopy)),
	)
	return nil
}

// StartAuthCleanup starts a background goroutine to clean up expired codes and tokens.
// The cleanup goroutine will stop when the provided context is canceled,
// enabling graceful shutdown.
func (s *OAuth2Server) StartAuthCleanup(ctx context.Context, interval time.Duration) {
	GetLogger().Info("Starting periodic cleanup of expired tokens and codes", logger.Duration("interval", interval))
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				GetLogger().Info("Stopping auth cleanup goroutine", logger.Error(ctx.Err()))
				return
			case <-ticker.C:
				s.cleanupExpired()
			}
		}
	}()
}

// cleanupExpired removes expired codes and tokens
func (s *OAuth2Server) cleanupExpired() {
	secLog := GetLogger()
	secLog.Debug("Running cleanup for expired tokens and codes")

	s.mutex.Lock()
	defer s.mutex.Unlock()

	now := time.Now()
	authCodesExpired := 0
	accessTokensExpired := 0
	needsSave := false

	for code, authCode := range s.authCodes {
		if now.After(authCode.ExpiresAt) {
			delete(s.authCodes, code)
			authCodesExpired++
			needsSave = true
		}
	}

	for token, accessToken := range s.accessTokens {
		if now.After(accessToken.ExpiresAt) {
			delete(s.accessTokens, token)
			accessTokensExpired++
			needsSave = true
		}
	}

	if authCodesExpired > 0 || accessTokensExpired > 0 {
		secLog.Info("Cleaned up expired entries",
			logger.Int("auth_codes_removed", authCodesExpired),
			logger.Int("access_tokens_removed", accessTokensExpired),
		)
		if needsSave {
			go s.persistTokensIfEnabled() // Persist removals in background
		}
	} else {
		secLog.Debug("No expired entries found during cleanup")
	}
}

// logThrottledError logs an error message, but only once per specified interval for a given key
func (s *OAuth2Server) logThrottledError(key, msg string, err error, interval time.Duration) {
	if s.shouldLogThrottled(key, interval) {
		GetLogger().Error(msg, logger.String("key", key), logger.Error(err))
	}
}

// logThrottledInfo logs msg at Info level at most once per interval for key.
func (s *OAuth2Server) logThrottledInfo(key, msg string, interval time.Duration, fields ...logger.Field) {
	if s.shouldLogThrottled(key, interval) {
		GetLogger().Info(msg, fields...)
	}
}

// noticeThrottled reports whether key was logged within interval, under a read
// lock only. It does not record anything.
func (s *OAuth2Server) noticeThrottled(key string, interval time.Duration) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	last, exists := s.throttledMessages[key]
	return exists && time.Since(last) <= interval
}

// shouldLogThrottled reports whether a message for key may be logged now, and
// if so records the time. A read-locked check comes first so a throttled caller
// on a request path does not take the write lock that also guards tokens.
func (s *OAuth2Server) shouldLogThrottled(key string, interval time.Duration) bool {
	if s.noticeThrottled(key, interval) {
		return false
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.throttledMessages == nil {
		s.throttledMessages = make(map[string]time.Time)
	}
	// Re-check under the write lock so concurrent callers log once.
	lastLogTime, exists := s.throttledMessages[key]
	if exists && time.Since(lastLogTime) <= interval {
		return false
	}
	s.throttledMessages[key] = time.Now()
	return true
}
