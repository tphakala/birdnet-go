package security

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/markbates/goth"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// oidcMainLogModule is the logger module that mirrors OIDC events an
// administrator needs to see (the discovery outcomes and skipped sign-out at
// the identity provider) to the console and the main log. The name must stay out of the per-module output defaults
// (defaultModuleOutputs in internal/logger/config.go and the module defaults in
// internal/conf/defaults.go): a module without its own output entry writes
// through the base handler. The logger must be taken with
// logger.Global().Module(oidcMainLogModule); GetLogger().Module("oidc") shares
// the security file handler and never reaches the console.
const oidcMainLogModule = "security.oidc"

// oidcDiscoveryPath is appended to the issuer URL to get the discovery document.
const oidcDiscoveryPath = "/.well-known/openid-configuration"

// oidcIssuerBase returns the issuer URL without trailing slashes. It accepts
// the issuer itself or the full discovery document URL and strips a trailing
// oidcDiscoveryPath, so a pasted discovery URL reaches the same document as the
// issuer it belongs to. The value must not carry a query; see
// splitOIDCIssuerQuery.
func oidcIssuerBase(issuerURL string) string {
	base := strings.TrimRight(issuerURL, "/")
	base = strings.TrimSuffix(base, oidcDiscoveryPath)
	return strings.TrimRight(base, "/")
}

// splitOIDCIssuerQuery returns the configured issuer URL without its query and
// fragment, plus the raw query. Some identity providers show the discovery URL
// with a query (for example ?appid=<client id>), and a user may paste that form.
// A value that does not parse, or has no query or fragment, is returned as is
// with an empty query.
func splitOIDCIssuerQuery(issuerURL string) (withoutQuery, rawQuery string) {
	u, err := url.Parse(issuerURL)
	if err != nil || !strings.ContainsAny(issuerURL, "?#") {
		return issuerURL, ""
	}
	rawQuery = u.RawQuery
	u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
	return u.String(), rawQuery
}

// oidcDiscoveryURL builds the OIDC discovery document URL: the document is at
// {issuer}/.well-known/openid-configuration (OpenID Connect Discovery 1.0,
// section 4). The configured value may already be the discovery URL; see
// oidcIssuerBase. A query on the configured value is kept on the discovery URL
// and a fragment is dropped.
func oidcDiscoveryURL(issuerURL string) string {
	withoutQuery, rawQuery := splitOIDCIssuerQuery(issuerURL)
	discoveryURL := oidcIssuerBase(withoutQuery) + oidcDiscoveryPath
	if rawQuery != "" {
		discoveryURL += "?" + rawQuery
	}
	return discoveryURL
}

// warnOnOIDCIssuerMismatch logs a warning when the issuer reported by the
// discovery document differs from the configured issuer URL, ignoring trailing
// slashes, a pasted discovery path and a query. Registration continues either
// way: goth checks ID tokens against the discovered issuer, and a provider
// reached through an internal host name keeps working.
func warnOnOIDCIssuerMismatch(configuredIssuer, discoveredIssuer, discoveryURL string) {
	withoutQuery, _ := splitOIDCIssuerQuery(configuredIssuer)
	if oidcIssuerBase(withoutQuery) == strings.TrimRight(discoveredIssuer, "/") {
		return
	}
	logOIDCEvent(logger.LogLevelWarn, "OIDC discovery document reports a different issuer than the configured issuer URL, sign-in continues and ID tokens are checked against the discovered issuer",
		logger.CredentialURL("issuer_url", configuredIssuer),
		logger.CredentialURL("discovered_issuer", discoveredIssuer),
		logger.CredentialURL("discovery_url", discoveryURL))
}

// redactedError carries an error text with credentials removed and still
// unwraps to the original error.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// urlUserinfo matches the userinfo of a URL with a scheme inside an error text.
// The class excludes the slash that ends the authority, so the match runs to the
// last at sign of the authority and a raw at sign in a password stays inside it.
var urlUserinfo = regexp.MustCompile(`(?i)(\w+://)[^/\s"]+@`)

// redactDiscoveryErr removes URL userinfo from the text of err. net/http masks
// the password in a transport error but keeps the username, and prints a URL it
// cannot parse unchanged, so the text is rewritten before it is logged or
// recorded. An error without userinfo in its text passes through unchanged. err
// must not be nil.
func redactDiscoveryErr(err error) error {
	msg := urlUserinfo.ReplaceAllString(err.Error(), "${1}"+privacy.RedactedMarker+"@")
	if msg == err.Error() {
		return err
	}
	return &redactedError{msg: msg, err: err}
}

// errProviderNotRegistered is returned by ProviderRegistrationError for a
// provider goth does not know and for which no discovery failure is recorded.
var errProviderNotRegistered = errors.NewStd("provider is not registered: it was not enabled at startup or its settings changed since, restart BirdNET-Go to apply")

// lastOIDCDiscoveryErr holds the most recent OIDC discovery failure, or nil
// when discovery has not failed or the provider has registered since.
var lastOIDCDiscoveryErr atomic.Pointer[error]

// recordOIDCDiscoveryFailure remembers why OIDC discovery failed, with any
// userinfo in the discovery URL redacted.
func recordOIDCDiscoveryFailure(discoveryURL string, err error) {
	wrapped := fmt.Errorf("OIDC discovery request to %s failed: %w", privacy.ScrubCredentialURL(discoveryURL), redactDiscoveryErr(err))
	lastOIDCDiscoveryErr.Store(&wrapped)
}

// clearOIDCDiscoveryFailure forgets the recorded discovery failure.
func clearOIDCDiscoveryFailure() {
	lastOIDCDiscoveryErr.Store(nil)
}

// ProviderRegistrationError reports why a login with the given goth provider
// name cannot start. It returns nil when goth has the provider registered.
// Otherwise it returns the recorded OIDC discovery failure when gothProvider is
// ProviderOIDC and one is recorded, and a generic "not registered" error in
// every other case. It is safe for concurrent use. The error is meant for the
// server log and must not be sent to a client.
func ProviderRegistrationError(gothProvider string) error {
	if _, err := goth.GetProvider(gothProvider); err == nil {
		return nil
	}
	if gothProvider == ProviderOIDC {
		if recorded := lastOIDCDiscoveryErr.Load(); recorded != nil {
			return *recorded
		}
	}
	return errProviderNotRegistered
}

// logOIDCEvent writes an OIDC event (discovery or sign-out) to the security
// module log and to the base handler (console and main log).
func logOIDCEvent(level logger.LogLevel, msg string, fields ...logger.Field) {
	fields = append(fields, logger.String("provider", ConfigOIDC))
	GetLogger().Log(level, msg, fields...)
	logger.Global().Module(oidcMainLogModule).Log(level, msg, fields...)
}

// logOIDCRetryExhausted reports that the background discovery retry gave up.
// discoveryURL is the discovery document URL that was retried. lastErr is the most
// recent discovery error and may be nil.
func logOIDCRetryExhausted(issuerURL, discoveryURL string, lastErr error) {
	logOIDCEvent(logger.LogLevelError,
		"OIDC discovery retries exhausted, restart BirdNET-Go after fixing the issuer URL or the connection to the identity provider",
		logger.CredentialURL("issuer_url", issuerURL),
		logger.CredentialURL("discovery_url", discoveryURL),
		logger.Error(lastErr),
		logger.Duration("retried_for", OIDCRetryMaxDuration))
}
