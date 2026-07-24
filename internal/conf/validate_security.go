// conf/validate_security.go

package conf

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Provider IDs used in security.oauthProviders, and by the deprecated
// googleAuth/githubAuth/microsoftAuth blocks that MigrateOAuthConfig converts into
// it. These must stay equal to the values internal/security dispatches on
// (ConfigGoogle, ConfigGitHub, ConfigMicrosoft, ConfigOIDC in
// internal/security/constants.go). They are duplicated rather than shared because
// security imports conf, so the dependency cannot run the other way.
const (
	providerGoogle    = "google"
	providerGitHub    = "github"
	providerMicrosoft = "microsoft"
	providerOIDC      = "oidc"
)

// validateSecuritySettings validates the security-specific settings.
//
// A provider that is enabled but merely unfinished no longer blocks startup:
// normalizeIncompleteFeatures disables the ones the runtime already ignores,
// because the alternative is a server that will not boot and therefore serves no
// UI in which to fix the provider. The exception is a provider that can still
// force authentication on while being unable to complete a sign-in; see
// validateOAuthRedirects and validateOIDCProviders.
func validateSecuritySettings(settings *Security) error {
	// TLS mode validation
	if err := validateTLSMode(settings); err != nil {
		return err
	}

	// Validate the subnet bypass setting against the allowed pattern.
	// Empty entries (from trailing commas, double commas, or all-whitespace tokens)
	// are skipped so that a config like "10.0.0.0/8, ,192.168.0.0/24" is accepted
	// with the same semantics as oauth.go's allowlist check.
	if settings.AllowSubnetBypass.Enabled {
		subnets := strings.SplitSeq(settings.AllowSubnetBypass.Subnet, ",")
		for subnet := range subnets {
			trimmedSubnet := strings.TrimSpace(subnet)
			if trimmedSubnet == "" {
				continue // Skip empty entries (e.g. trailing or embedded commas)
			}
			_, _, err := net.ParseCIDR(trimmedSubnet)
			if err != nil {
				return errors.New(err).
					Category(errors.CategoryValidation).
					Context("validation_type", "security-subnet-format").
					Context("subnet", trimmedSubnet).
					Build()
			}
		}
	}

	// Validate trusted-proxy entries. Each must be a valid CIDR, a bare IP (a
	// single host), or the reserved "cloudflare" preset token. Empty entries
	// (blank list items) are skipped, matching the lenient parsing used by the
	// subnet bypass check above.
	for _, entry := range settings.TrustedProxies {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" || strings.EqualFold(trimmed, TrustedProxyCloudflarePreset) {
			continue
		}
		if net.ParseIP(trimmed) != nil {
			continue // bare IP, treated as a single-host /32 or /128
		}
		if _, _, err := net.ParseCIDR(trimmed); err != nil {
			return errors.New(err).
				Category(errors.CategoryValidation).
				Context("validation_type", "security-trustedproxies-format").
				Context("entry", trimmed).
				Build()
		}
	}

	// Normalize session duration: viper nested defaults can be lost when the
	// parent key exists in the config file but sessionduration is absent.
	if settings.SessionDuration <= 0 {
		settings.SessionDuration = DefaultSessionDuration // 7 days, matches viper default
	}

	// Reject providers that would force authentication on without being able to
	// complete a sign-in. Runs before validateOIDCProviders so that the reason an
	// operator cannot get in is reported ahead of the OIDC-only issuer rules.
	if err := validateOAuthRedirects(settings); err != nil {
		return err
	}

	// Validate OIDC provider configuration
	if err := validateOIDCProviders(settings); err != nil {
		return err
	}

	return nil
}

// oauthRedirectProblem describes why a provider has no usable redirect URL, or
// returns "" when it has one. It mirrors what initializeProviders actually
// computes: an explicit redirectUri is used verbatim, and only an absent one is
// built from security.host or security.baseUrl.
//
// Testing merely for an empty redirectUri would miss the common case. The
// deprecated googleAuth and githubAuth blocks default redirecturi to "/settings"
// (see defaults.go), and MigrateOAuthConfig copies that into the array as-is, so a
// migrated provider usually carries a non-empty but relative value that no OAuth
// provider can redirect back to.
//
// originProblem describes the shared fallback, and is what a provider without its
// own redirectUri inherits; see oauthOriginProblem.
func oauthRedirectProblem(p *OAuthProviderConfig, originProblem string) string {
	if p.RedirectURI == "" {
		return originProblem
	}
	parsed, err := url.Parse(p.RedirectURI)
	if err != nil || parsed.Host == "" || (parsed.Scheme != SchemeHTTPS && parsed.Scheme != SchemeHTTP) {
		return "its redirectUri is not an absolute http(s) URL, so the provider has nowhere to send the user back to"
	}
	return ""
}

// oauthOriginProblem describes why security.host and security.baseUrl cannot
// produce a redirect origin, or returns "" when they can.
//
// Presence is not enough: computeBaseURL returns baseUrl verbatim, so a value that
// is not an absolute http(s) URL yields a callback the identity provider will
// reject, which is the same lock-out as having no origin at all. baseUrl also wins
// outright when set, so a valid host cannot rescue a malformed one.
//
// This mirrors computeBaseURL in internal/security/oauth.go, including its rule
// that a host without a scheme is assumed to be https. The logic is duplicated
// rather than shared for the same reason as the provider name constants above:
// security imports conf, so the dependency cannot run the other way.
//
// A malformed baseUrl is only reported through this path, when a credentialed
// provider actually depends on it. Rejecting one that nothing consumes would be the
// kind of inert-config failure this pass exists to remove.
func oauthOriginProblem(settings *Security) string {
	var origin string
	switch {
	case settings.BaseURL != "":
		origin = strings.TrimSuffix(settings.BaseURL, "/")
	case settings.Host != "":
		host := strings.TrimSuffix(settings.Host, "/")
		if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
			origin = host
		} else {
			origin = "https://" + host
		}
	default:
		return "no redirect URL can be built; set security.host or security.baseUrl, or security.oauthProviders[].redirectUri"
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || (parsed.Scheme != SchemeHTTPS && parsed.Scheme != SchemeHTTP) {
		field := "security.host"
		if settings.BaseURL != "" {
			field = "security.baseUrl"
		}
		return fmt.Sprintf("%s does not form an absolute http(s) URL (%q), so no redirect URL can be built from it; fix it or set security.oauthProviders[].redirectUri", field, origin)
	}
	return ""
}

// validateOAuthRedirects rejects an enabled OAuth provider that has credentials but
// no usable redirect URL, for every provider kind.
//
// This is the one shape that the rest of this change cannot afford to downgrade to
// a warning. Credentials are what make a provider count towards
// IsAuthenticationEnabled (see GetEnabledOAuthProviders), so such an entry makes
// authentication mandatory, while initializeProviders still registers it with a
// redirect the identity provider will reject. The result is an instance nobody can
// sign in to, and the notification that reports the problem lives behind the very
// login that cannot complete. Refusing to boot at least names the field.
//
// The credentials guard is what keeps this consistent with the rest of the pass: a
// provider merely toggled on cannot sign anyone in either, but it also does not
// make authentication required, so it is inert and normalizeOAuthProviders just
// disables it.
//
// This deliberately does not restore the deleted security-oauth-host rule, which
// demanded security.host or security.baseUrl even from a provider carrying a
// perfectly good absolute redirectUri. Only the absence of any usable redirect is
// fatal here.
func validateOAuthRedirects(settings *Security) error {
	originProblem := oauthOriginProblem(settings)
	for i := range settings.OAuthProviders {
		provider := &settings.OAuthProviders[i]
		if !provider.Enabled || provider.ClientID == "" || provider.ClientSecret == "" {
			continue
		}
		if reason := oauthRedirectProblem(provider, originProblem); reason != "" {
			return errors.Newf("security.oauthProviders: provider %q is enabled but %s", provider.Provider, reason).
				Category(errors.CategoryValidation).
				Context("validation_type", "security-oauth-redirect-missing").
				Context("provider", provider.Provider).
				Build()
		}
	}
	return nil
}

// validateTLSMode validates TLS certificate management mode settings.
func validateTLSMode(settings *Security) error {
	switch settings.TLSMode {
	case TLSModeAutoTLS:
		hostname := settings.GetHostnameForCertificates()
		if hostname == "" {
			return errors.Newf("security.host (or hostname in security.baseUrl) must be set when TLS mode is autotls").
				Category(errors.CategoryValidation).
				Context("validation_type", "security-autotls-host").
				Build()
		}
		if err := validateAutoTLSHostname(hostname); err != nil {
			return err
		}
		if RunningInContainer() {
			GetLogger().Warn("AutoTLS requires host ports 80 and 443 to be exposed",
				logger.String("ports", "80:8080 (ACME HTTP-01), 443:8443 (HTTPS)"),
				logger.String("hint", "Consider using docker-compose.autotls.yml for proper AutoTLS configuration"))
		}

	case TLSModeManual:
		tm := GetTLSManager()
		if !tm.CertificateExists("webserver", TLSCertTypeServerCert) {
			GetLogger().Warn("TLS mode is 'manual' but no server certificate is installed",
				logger.String("hint", "Upload a server certificate via the settings page or API"))
		}

	case TLSModeSelfSigned:
		tm := GetTLSManager()
		if !tm.CertificateExists("webserver", TLSCertTypeServerCert) {
			GetLogger().Info("TLS mode is 'selfsigned' - a self-signed certificate will be generated on startup")
		}

	case TLSModeNone:
		// No TLS validation needed

	default:
		return errors.Newf("security.tlsMode has invalid value %q (valid: autotls, manual, selfsigned, or empty)", settings.TLSMode).
			Category(errors.CategoryValidation).
			Context("validation_type", "security-tlsmode-invalid").
			Context("tls_mode", string(settings.TLSMode)).
			Build()
	}
	return nil
}

// validateOIDCProviders validates OIDC-specific provider configuration.
//
// Only a duplicate among ENABLED entries is fatal on that count: two of those make
// it ambiguous which one a sign-in would use, because goth registers providers by
// name and the second silently replaces the first. A disabled second entry never
// reaches goth (GetEnabledOAuthProviders and initializeProviders both skip it), so
// refusing to start over a leftover is the failure this whole change exists to
// remove.
//
// An unusable issuer URL stays fatal. That is worth spelling out because the rest
// of this change moves in the other direction. Enabling OIDC is the setting that
// can lock an operator out of their own instance: the provider counts towards
// IsAuthenticationEnabled as soon as it has credentials, so accepting a broken
// issuer means authentication becomes required while no sign-in can complete. The
// settings API validates without normalizing, so keeping this rule here is what
// answers that save with an error instead of a 200 and a locked door.
//
// The matching "no way back from the identity provider" rule used to live here too.
// It now applies to every provider kind in validateOAuthRedirects, because the
// lock-out it prevents was never specific to OIDC.
//
// The load path pays for that: an OIDC provider with credentials and a broken
// issuer still refuses to boot. The trade is deliberate. A provider merely toggled
// on has no credentials, so normalizeIncompleteFeatures disables it before this
// runs and the common case never reaches here; what remains is a provider someone
// deliberately configured, where booting into a locked door is worse than saying
// which field is wrong.
func validateOIDCProviders(settings *Security) error {
	oidcCount := 0
	for i := range settings.OAuthProviders {
		provider := &settings.OAuthProviders[i]
		if provider.Provider != providerOIDC || !provider.Enabled {
			continue
		}
		oidcCount++
		if oidcCount > 1 {
			return errors.Newf("only one enabled OIDC provider (provider: %q) is allowed in security.oauthProviders, found duplicate entry", providerOIDC).
				Category(errors.CategoryValidation).
				Context("validation_type", "security-oidc-duplicate").
				Build()
		}
		if provider.IssuerURL == "" {
			return errors.Newf("security.oauthProviders: issuerUrl is required when provider is %q and enabled", providerOIDC).
				Category(errors.CategoryValidation).
				Context("validation_type", "security-oidc-issuer-missing").
				Build()
		}
		parsed, err := url.Parse(provider.IssuerURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != SchemeHTTPS && parsed.Scheme != SchemeHTTP) {
			return errors.Newf("security.oauthProviders: issuerUrl %q is not a valid http(s) URL", provider.IssuerURL).
				Category(errors.CategoryValidation).
				Context("validation_type", "security-oidc-issuer-invalid").
				Context("issuer_url", provider.IssuerURL).
				Build()
		}
		if parsed.Scheme == SchemeHTTP {
			GetLogger().Warn("OIDC issuerUrl uses HTTP instead of HTTPS — acceptable for local development only",
				logger.String("issuer_url", provider.IssuerURL))
		}
	}
	return nil
}

// privateTLDs lists TLD suffixes that are not publicly resolvable
// and therefore cannot be used with Let's Encrypt.
var privateTLDs = []string{
	".local",
	".internal",
	".lan",
	".home",
	".localdomain",
	".localhost",
	".test",
	".example",
	".invalid",
}

// validateAutoTLSHostname checks that a hostname is suitable for Let's Encrypt.
// Let's Encrypt requires a publicly resolvable FQDN — not an IP, not a private
// name, and not a bare hostname without dots.
func validateAutoTLSHostname(hostname string) error {
	// Must not be an IP address
	if net.ParseIP(hostname) != nil {
		return errors.Newf("Let's Encrypt requires a domain name, not an IP address (%s)", hostname).
			Category(errors.CategoryValidation).
			Context("validation_type", "security-autotls-hostname").
			Context("hostname", hostname).
			Build()
	}

	// Must not be localhost (check before dot check since "localhost" has no dots)
	if strings.EqualFold(hostname, "localhost") {
		return errors.Newf("Let's Encrypt cannot issue certificates for localhost").
			Category(errors.CategoryValidation).
			Context("validation_type", "security-autotls-hostname").
			Context("hostname", hostname).
			Build()
	}

	// Must contain at least one dot (FQDN)
	if !strings.Contains(hostname, ".") {
		return errors.Newf("Let's Encrypt requires a fully qualified domain name (e.g., birds.example.com), not a bare hostname (%s)", hostname).
			Category(errors.CategoryValidation).
			Context("validation_type", "security-autotls-hostname").
			Context("hostname", hostname).
			Build()
	}

	// Must not use a private/non-routable TLD
	lower := strings.ToLower(hostname)
	for _, suffix := range privateTLDs {
		if strings.HasSuffix(lower, suffix) {
			return errors.Newf("Let's Encrypt cannot issue certificates for private domain %q (TLD %s is not publicly resolvable)", hostname, suffix).
				Category(errors.CategoryValidation).
				Context("validation_type", "security-autotls-hostname").
				Context("hostname", hostname).
				Context("tld", suffix).
				Build()
		}
	}

	return nil
}
