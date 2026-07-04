// Package api provides the HTTP server infrastructure for BirdNET-Go.
// This package contains the main server implementation while the JSON API
// endpoints are organized in the v2 subpackage.
package api

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetLogger returns the api package logger.
func GetLogger() logger.Logger {
	return logger.Global().Module("api")
}

// Default constants for the HTTP server.
const (
	DefaultReadTimeout     = 30 * time.Second
	DefaultWriteTimeout    = 30 * time.Second
	DefaultIdleTimeout     = 120 * time.Second
	DefaultShutdownTimeout = 10 * time.Second

	// DefaultLogPath is the default path for the server log file.
	DefaultLogPath = "logs/server.log"
)

// Config holds the HTTP server configuration.
// It consolidates settings from various sources into a single structure
// for easy server initialization.
type Config struct {
	// Server binding
	Host string // Host to bind to (empty for all interfaces)
	Port string // Port to listen on

	// TLS configuration
	TLSEnabled  bool   // Enable TLS
	AutoTLS     bool   // Use Let's Encrypt automatic TLS
	TLSCertFile string // Path to TLS certificate file (manual TLS)
	TLSKeyFile  string // Path to TLS key file (manual TLS)
	TLSPort     string // Port for HTTPS (when TLS is enabled)

	// Security settings
	RedirectToHTTPS   bool     // Redirect HTTP to HTTPS
	RedirectAuthority string   // External HTTPS authority (host[:port]) for HTTP->HTTPS redirects; empty falls back to the request host
	AllowedOrigins    []string // CORS allowed origins
	AllowEmbedding    bool     // Allow embedding in iframes (e.g., Home Assistant)

	// Timeouts
	ReadTimeout     time.Duration // Maximum duration for reading request
	WriteTimeout    time.Duration // Maximum duration for writing response
	IdleTimeout     time.Duration // Maximum time to wait for next request
	ShutdownTimeout time.Duration // Maximum time to wait for graceful shutdown

	// Limits
	BodyLimit string // Maximum request body size (e.g., "1M", "10M")

	// Logging
	Debug    bool            // Enable debug mode
	LogLevel logger.LogLevel // Logging level

	// Development mode
	DevMode bool // Enable development mode features
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Host:            "",
		Port:            "8080",
		TLSEnabled:      false,
		AutoTLS:         false,
		TLSPort:         defaultTLSPort,
		RedirectToHTTPS: false,
		AllowedOrigins:  []string{"*"},
		ReadTimeout:     DefaultReadTimeout,
		WriteTimeout:    DefaultWriteTimeout,
		IdleTimeout:     DefaultIdleTimeout,
		ShutdownTimeout: DefaultShutdownTimeout,
		BodyLimit:       "10M",
		Debug:           false,
		LogLevel:        logger.LogLevelInfo,
		DevMode:         false,
	}
}

// TLS listener port defaults used when a port is unset or collides with the HTTP port.
const (
	// defaultTLSPort is the HTTPS listener port used when TLS is enabled and no
	// port is configured.
	defaultTLSPort = "8443"
	// fallbackTLSPort is used when the configured TLS port equals the HTTP port.
	fallbackTLSPort = "8444"
)

// resolveTLSPort sets cfg.TLSPort, defaulting to defaultTLSPort when empty and
// resolving conflicts when TLSPort equals the HTTP port.
func resolveTLSPort(cfg *Config) {
	if cfg.TLSPort == "" {
		cfg.TLSPort = defaultTLSPort
	}
	if cfg.TLSPort == cfg.Port {
		fallback := defaultTLSPort
		if cfg.Port == defaultTLSPort {
			fallback = fallbackTLSPort
		}
		GetLogger().Warn("TLS port must differ from HTTP port",
			logger.String("http_port", cfg.Port),
			logger.String("configured_tls_port", cfg.TLSPort),
			logger.String("resolved_tls_port", fallback),
		)
		cfg.TLSPort = fallback
	}
}

// redirectExplicitlySet reports whether the user explicitly configured
// security.redirecttohttps via the config file or the environment, as opposed to
// relying on the built-in default. viper.IsSet cannot be used here: it returns
// true whenever a default is registered (defaults.go always registers one), so
// it can never distinguish an explicit user value from the default. InConfig
// checks only the config file, and the env var is probed directly. Declared as a
// variable so tests can drive the redirect-default policy deterministically;
// tests that swap it must not run in parallel, since ConfigFromSettings reads it.
var redirectExplicitlySet = func() bool {
	return viper.InConfig(conf.ConfigKeySecurityRedirect) || os.Getenv(conf.EnvVarSecurityRedirect) != ""
}

// ConfigFromSettings creates a Config from the application settings.
// This bridges the existing conf.Settings structure to the new server config.
func ConfigFromSettings(settings *conf.Settings) *Config {
	cfg := DefaultConfig()

	// Server binding - use port only, bind to all interfaces
	// Note: Security.Host is for external URLs (TLS certs, OAuth), not for socket binding
	if settings.WebServer.Port != "" {
		cfg.Port = settings.WebServer.Port
	}
	cfg.Host = "" // Bind to all interfaces (0.0.0.0)

	// TLS settings - map from TLSMode to server config
	switch settings.Security.TLSMode {
	case conf.TLSModeAutoTLS:
		cfg.AutoTLS = true
		cfg.TLSEnabled = true
		// Default to redirecting HTTP->HTTPS for AutoTLS; only honor an explicit
		// user value (config file or env var). The default holds because
		// redirectExplicitlySet ignores the built-in default (unlike viper.IsSet).
		cfg.RedirectToHTTPS = true
		if redirectExplicitlySet() {
			cfg.RedirectToHTTPS = settings.Security.RedirectToHTTPS
		}
		// Redirect to the externally advertised authority (base URL or host),
		// never the internal TLS port, which is unreachable behind container port
		// mappings such as host 443 -> container 8443. GetExternalHost falls back
		// to Host (no port), which AutoTLS validation requires to be set.
		cfg.RedirectAuthority = settings.Security.GetExternalHost()
		cfg.TLSPort = settings.Security.TLSPort
		resolveTLSPort(cfg)
	case conf.TLSModeManual, conf.TLSModeSelfSigned:
		tm := conf.GetTLSManager()
		certPath := tm.GetCertificatePath("webserver", conf.TLSCertTypeServerCert)
		keyPath := tm.GetCertificatePath("webserver", conf.TLSCertTypeServerKey)
		if tm.CertificateExists("webserver", conf.TLSCertTypeServerCert) &&
			tm.CertificateExists("webserver", conf.TLSCertTypeServerKey) {
			cfg.TLSEnabled = true
			cfg.TLSCertFile = certPath
			cfg.TLSKeyFile = keyPath
			cfg.RedirectToHTTPS = settings.Security.RedirectToHTTPS
			// Manual/self-signed TLS terminates directly on TLSPort with no
			// external port remapping, so the redirect keeps that port (the
			// request-host fallback in newHTTPRedirectServer). RedirectAuthority is
			// left empty here on purpose: overriding it from BaseURL would retarget
			// existing manual-TLS redirects and is out of scope for the AutoTLS fix.
			cfg.TLSPort = settings.Security.TLSPort
			resolveTLSPort(cfg)
		}
	default:
		// TLSModeNone — plain HTTP
	}

	// Embedding
	cfg.AllowEmbedding = settings.WebServer.AllowEmbedding

	// Debug mode
	cfg.Debug = settings.WebServer.Debug || settings.Debug
	if cfg.Debug {
		cfg.LogLevel = logger.LogLevelDebug
	}

	return cfg
}

// SessionCookiesSecure reports whether session and auth cookies should carry the
// Secure attribute, i.e. whether clients reach the app over HTTPS given the
// EFFECTIVE server configuration (not merely the persisted redirect toggle).
//
// AutoTLS with the HTTP->HTTPS redirect disabled is handled first: that mode
// deliberately serves the full app over plain HTTP on the ACME listener (see
// Server.startBlocking), so Secure must never be forced there, even when an
// https BaseURL is advertised. Forcing it would drop the session cookie (which
// also authenticates basic auth) and break login over that direct HTTP path.
//
// Otherwise it is true when any of the following hold:
//   - a reverse proxy terminates TLS and the canonical BaseURL uses https;
//   - the app redirects all HTTP traffic to HTTPS (RedirectToHTTPS), so every
//     client ends up on HTTPS;
//   - the app terminates TLS itself and serves no plain-HTTP app listener, i.e.
//     manual or self-signed TLS with certificates present (TLSEnabled without
//     AutoTLS). Manual/self-signed either redirects or listens HTTPS-only, so the
//     browser always reaches it over HTTPS.
//
// Missing manual/self-signed certs leave TLSEnabled false, correctly yielding a
// non-Secure cookie for the plain-HTTP fallback. The settings argument supplies
// BaseURL, which Config intentionally does not carry.
func (c *Config) SessionCookiesSecure(settings *conf.Settings) bool {
	if c == nil {
		return false
	}
	// AutoTLS without the redirect serves the app over plain HTTP directly, so
	// keep Secure off regardless of any advertised https BaseURL.
	if c.AutoTLS && !c.RedirectToHTTPS {
		return false
	}
	if settings != nil && settings.Security.IsHTTPSBaseURL() {
		return true
	}
	if c.RedirectToHTTPS {
		return true
	}
	return c.TLSEnabled && !c.AutoTLS
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if c.Port == "" {
		return fmt.Errorf("port is required")
	}

	// Validate TLS configuration
	if c.TLSEnabled && !c.AutoTLS {
		if c.TLSCertFile == "" || c.TLSKeyFile == "" {
			return fmt.Errorf("TLS enabled but certificate or key file not specified")
		}
	}

	// Validate timeouts
	if c.ReadTimeout <= 0 {
		return fmt.Errorf("read timeout must be positive")
	}
	if c.WriteTimeout <= 0 {
		return fmt.Errorf("write timeout must be positive")
	}

	return nil
}

// Address returns the full address string for the server to listen on.
func (c *Config) Address() string {
	if c.Host == "" {
		return ":" + c.Port
	}
	return c.Host + ":" + c.Port
}

// TLSAddress returns the full address string for the HTTPS server to listen on.
func (c *Config) TLSAddress() string {
	port := c.TLSPort
	if port == "" {
		port = "8443"
	}
	if c.Host == "" {
		return ":" + port
	}
	return c.Host + ":" + port
}

// String returns a human-readable representation of the config.
func (c *Config) String() string {
	tlsStatus := "disabled"
	if c.AutoTLS {
		tlsStatus = "auto (Let's Encrypt)"
	} else if c.TLSEnabled {
		tlsStatus = "manual"
	}

	return fmt.Sprintf("Server Config: address=%s, tls=%s, debug=%v",
		c.Address(), tlsStatus, c.Debug)
}
