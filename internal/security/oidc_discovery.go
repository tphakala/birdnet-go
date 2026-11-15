package security

import (
	"fmt"
	"regexp"
	"sync/atomic"

	"github.com/markbates/goth"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// oidcMainLogModule is the logger module that mirrors the three OIDC discovery
// outcomes (first failure, late registration, retry exhaustion) to the console
// and the main log. The name must stay out of the per-module output defaults
// (defaultModuleOutputs in internal/logger/config.go and the module defaults in
// internal/conf/defaults.go): a module without its own output entry writes
// through the base handler. The logger must be taken with
// logger.Global().Module(oidcMainLogModule); GetLogger().Module("oidc") shares
// the security file handler and never reaches the console.
const oidcMainLogModule = "security.oidc"

// errProviderNotRegistered is returned by ProviderRegistrationError for a
// provider goth does not know and for which no discovery failure is recorded.
var errProviderNotRegistered = errors.NewStd("provider is not registered: it was not enabled at startup or its settings changed since, restart BirdNET-Go to apply")

// lastOIDCDiscoveryErr holds the most recent OIDC discovery failure, or nil
// when discovery has not failed or the provider has registered since.
var lastOIDCDiscoveryErr atomic.Pointer[error]

// redactedError carries an error text with credentials removed and still
// unwraps to the original error.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// urlUserinfo matches the userinfo of a URL with a scheme inside an error text.
var urlUserinfo = regexp.MustCompile(`(?i)(\w+://)[^/@\s"]+@`)

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

// logOIDCEvent writes an OIDC discovery event to the security module log and to
// the base handler (console and main log).
func logOIDCEvent(level logger.LogLevel, msg string, fields ...logger.Field) {
	fields = append(fields, logger.String("provider", ConfigOIDC))
	GetLogger().Log(level, msg, fields...)
	logger.Global().Module(oidcMainLogModule).Log(level, msg, fields...)
}

// logOIDCRetryExhausted reports that the background discovery retry gave up.
// lastErr may be nil.
func logOIDCRetryExhausted(issuerURL string, lastErr error) {
	logOIDCEvent(logger.LogLevelError,
		"OIDC discovery retries exhausted, restart BirdNET-Go after fixing the issuer URL or the connection to the identity provider",
		logger.CredentialURL("issuer_url", issuerURL),
		logger.Error(lastErr),
		logger.Duration("retried_for", OIDCRetryMaxDuration))
}
