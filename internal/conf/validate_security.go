// conf/validate_security.go

package conf

import (
	"net"
	"net/url"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// validateSecuritySettings validates the security-specific settings
func validateSecuritySettings(settings *Security) error {
	// Check if any OAuth provider is enabled (OAuth providers require host or baseUrl for redirect URLs)
	// Note: BasicAuth doesn't require host as it doesn't use OAuth redirects
	if (settings.GoogleAuth.Enabled || settings.GithubAuth.Enabled || settings.MicrosoftAuth.Enabled) && settings.Host == "" && settings.BaseURL == "" {
		return errors.Newf("security.host or security.baseUrl must be set when using OAuth authentication providers (Google, GitHub, or Microsoft)").
			Category(errors.CategoryValidation).
			Context("validation_type", "security-oauth-host").
			Context("google_enabled", settings.GoogleAuth.Enabled).
			Context("github_enabled", settings.GithubAuth.Enabled).
			Context("microsoft_enabled", settings.MicrosoftAuth.Enabled).
			Build()
	}

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

	// Validate session duration
	if settings.SessionDuration <= 0 {
		return errors.Newf("security.sessionduration must be a positive duration").
			Category(errors.CategoryValidation).
			Context("validation_type", "security-session-duration").
			Build()
	}

	// Validate OIDC provider configuration
	if err := validateOIDCProviders(settings); err != nil {
		return err
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
			GetLogger().Warn("AutoTLS requires ports 80 and 443 to be exposed",
				logger.String("ports", "80:80 (ACME HTTP-01), 443:443 (HTTPS)"),
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

// validateOIDCProviders validates OIDC-specific provider configuration:
// at most one OIDC entry, valid issuer URL when enabled, callback URL source required, HTTPS warning.
func validateOIDCProviders(settings *Security) error {
	oidcCount := 0
	for i := range settings.OAuthProviders {
		provider := &settings.OAuthProviders[i]
		if provider.Provider != "oidc" {
			continue
		}
		oidcCount++
		if oidcCount > 1 {
			return errors.Newf("only one OIDC provider (provider: \"oidc\") is allowed in security.oauthProviders, found duplicate entry").
				Category(errors.CategoryValidation).
				Context("validation_type", "security-oidc-duplicate").
				Build()
		}
		if !provider.Enabled {
			continue
		}
		// Require a callback URL source — without host/baseUrl/redirectUri, the OAuth flow will fail
		if provider.RedirectURI == "" && settings.Host == "" && settings.BaseURL == "" {
			return errors.Newf("security.host or security.baseUrl must be set, or redirectUri must be configured, when OIDC provider is enabled").
				Category(errors.CategoryValidation).
				Context("validation_type", "security-oidc-redirect-missing").
				Context("issuer_url", provider.IssuerURL).
				Build()
		}
		if provider.IssuerURL == "" {
			return errors.Newf("security.oauthProviders: issuerUrl is required when provider is \"oidc\" and enabled").
				Category(errors.CategoryValidation).
				Context("validation_type", "security-oidc-issuer-missing").
				Build()
		}
		parsed, err := url.Parse(provider.IssuerURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != SchemeHTTPS && parsed.Scheme != SchemeHTTP) {
			return errors.Newf("security.oauthProviders: issuerUrl %q is not a valid URL", provider.IssuerURL).
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
