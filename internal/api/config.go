// Package api provides the HTTP server infrastructure for BirdNET-Go.
// This package contains the main server implementation while the JSON API
// endpoints are organized in the v2 subpackage.
package api

import (
	"fmt"
	"time"

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

	// Security settings
	RedirectToHTTPS bool     // Redirect HTTP to HTTPS
	AllowedOrigins  []string // CORS allowed origins

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

// ConfigFromSettings creates a Config from the application settings.
// This bridges the existing conf.Settings structure to the new server config.
func ConfigFromSettings(settings *conf.Settings) *Config {
	cfg := DefaultConfig()

	// Server binding - use port only, bind to all interfaces
	// Note: Security.Host is for external URLs (TLS certs, OAuth), not for socket binding
	cfg.Port = settings.WebServer.Port
	cfg.Host = "" // Bind to all interfaces (0.0.0.0)

	// TLS settings
	cfg.AutoTLS = settings.Security.AutoTLS
	cfg.TLSEnabled = settings.Security.AutoTLS // AutoTLS implies TLS enabled
	cfg.RedirectToHTTPS = settings.Security.RedirectToHTTPS

	// Debug mode
	cfg.Debug = settings.WebServer.Debug || settings.Debug
	if cfg.Debug {
		cfg.LogLevel = logger.LogLevelDebug
	}

	return cfg
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
