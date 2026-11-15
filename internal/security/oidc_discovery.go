package security

import (
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

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

// urlParseErrorMarker starts the text of a net/url parse failure. It also
// appears inside the text of another error that quotes such a failure, for
// example net/http reporting a Location header it could not parse.
const urlParseErrorMarker = `parse "`

// errOIDCDiscoveryCauseUnknown stands in for a discovery failure whose cause was
// not passed on.
var errOIDCDiscoveryCauseUnknown = errors.NewStd("no discovery error was recorded")

// errOIDCDiscoveryURLInvalid replaces the text of an error that contains a URL
// parse failure, in the discovery URL or in a redirect Location the identity
// provider sent. That text quotes the URL and a fragment of its userinfo, and
// the discovery URL is logged separately, redacted, in the discovery_url field.
var errOIDCDiscoveryURLInvalid = errors.NewStd("a URL in the discovery request or its redirect is not a valid URL")

// redactDiscoveryErr removes URL userinfo from the text of err. net/http masks
// the password in a transport error but keeps the username, and prints a URL it
// cannot parse unchanged, so the text is rewritten before it is logged or
// recorded. An error whose text contains a URL parse failure is replaced by
// errOIDCDiscoveryURLInvalid. An error without userinfo in its text passes
// through unchanged. A nil err is reported as errOIDCDiscoveryCauseUnknown.
func redactDiscoveryErr(err error) error {
	if err == nil {
		return errOIDCDiscoveryCauseUnknown
	}
	msg := err.Error()
	if strings.Contains(msg, urlParseErrorMarker) {
		// The reason of a parse failure can quote a fragment of the password,
		// and the quoted URL can be cut before the "@".
		msg = errOIDCDiscoveryURLInvalid.Error()
	}
	msg = privacy.ScrubURLUserinfoInText(msg)
	if msg == err.Error() {
		return err
	}
	return &redactedError{msg: msg, err: err}
}

// errProviderNotRegistered is returned by ProviderRegistrationError for a
// provider goth does not know and for which no discovery failure is recorded.
// The provider is not enabled, or its registration after startup or a settings
// change has not completed yet.
var errProviderNotRegistered = errors.NewStd("provider is not registered: it is not enabled, or its registration after startup or a settings change has not completed")

// lastOIDCDiscoveryErr holds the most recent OIDC discovery failure of the
// current registration generation, or nil when discovery has not failed or the
// provider has registered since. A new registration pass clears it.
var lastOIDCDiscoveryErr atomic.Pointer[error]

// recordOIDCDiscoveryFailure remembers why OIDC discovery failed, with any
// userinfo in the discovery URL redacted. A nil err is recorded as
// errOIDCDiscoveryCauseUnknown. Production callers hold oidcMu.
func recordOIDCDiscoveryFailure(discoveryURL string, err error) {
	wrapped := fmt.Errorf("OIDC discovery request to %s failed: %w", privacy.ScrubCredentialURL(discoveryURL), redactDiscoveryErr(err))
	lastOIDCDiscoveryErr.Store(&wrapped)
}

// clearOIDCDiscoveryFailure forgets the recorded discovery failure. Production
// callers hold oidcMu.
func clearOIDCDiscoveryFailure() {
	lastOIDCDiscoveryErr.Store(nil)
}

// ProviderRegistrationError reports why a login with the given goth provider
// name cannot start. It returns nil when goth has the provider registered.
// Otherwise it returns the recorded OIDC discovery failure when gothProvider is
// ProviderOIDC and one is recorded, and a generic "not registered" error in
// every other case. The recorded failure belongs to the current registration
// generation, so a failure of superseded settings is never reported. It is
// safe for concurrent use: it reads the registry and the recorded failure under
// oidcMu, the lock a discovery attempt holds while it registers the provider
// and clears the failure, so it never reports the failure of an attempt that
// has just registered. The error is meant for the server log and must not be
// sent to a client.
func ProviderRegistrationError(gothProvider string) error {
	oidcMu.Lock()
	defer oidcMu.Unlock()
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
// recent discovery error and may be nil, in which case errOIDCDiscoveryCauseUnknown
// is logged. Userinfo in lastErr is redacted before it is logged. retriedFor is
// the length of the retry window.
func logOIDCRetryExhausted(issuerURL, discoveryURL string, lastErr error, retriedFor time.Duration) {
	logOIDCEvent(logger.LogLevelError,
		"OIDC discovery retries exhausted. If the issuer URL is wrong, correct it in the authentication settings and save, which starts discovery again. If the settings are correct, restart BirdNET-Go once the identity provider is reachable",
		logger.CredentialURL("issuer_url", issuerURL),
		logger.CredentialURL("discovery_url", discoveryURL),
		logger.Error(redactDiscoveryErr(lastErr)),
		logger.Duration("retried_for", retriedFor))
}
