// Package apicore holds the shared substrate of the API v2 controller: the
// cross-cutting dependencies, settings accessors, error/logging helpers, shared
// middleware, the SSE hub and broadcasters, and the name-map plumbing. Domain
// handler packages embed *Core so these members promote onto each handler type
// without re-wiring. apicore depends only on leaf packages; it never imports a
// domain package or the api facade, so there is no import cycle.
package apicore

import (
	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetLogger returns a logger instance for the API v2 package.
// This provides consistent logging with module identification.
func GetLogger() logger.Logger {
	return logger.Global().Module("api")
}

// CurrentSettings returns the latest settings snapshot so UI changes
// take effect in API responses without restarting the service.
//
// It resolves the lock-free global atomic snapshot first and only falls back
// to this controller's own snapshot when no global snapshot has been published
// (standalone unit-test controllers). Both reads are lock-free (the per-controller
// fallback is an atomic Load), so the accessor is race-free against the
// Settings.Store the update handlers perform under the settings mutex.
func (c *Core) CurrentSettings() *conf.Settings {
	if latest := conf.GetSettings(); latest != nil {
		return latest
	}
	return c.Settings.Load()
}

// ControllerSettings returns this controller's own settings snapshot, read
// lock-free from the atomic Settings pointer that the update handlers publish on
// every save. Unlike CurrentSettings(), it deliberately does NOT consult the
// process-global atomic snapshot: use it for reads whose result is asserted
// per-controller (e.g. debug-gated response verbosity), where the shared global
// snapshot would couple otherwise-independent parallel tests.
//
// Loading the atomic pointer (rather than reading a plain field under a mutex)
// is what makes this safe to call from newErrorResponse, which is reached from
// HandleError while UpdateSettings already holds the settings write lock: a
// non-reentrant RLock there would deadlock. The snapshot is published under that
// same write lock, so the read sees a consistent value. The returned snapshot is
// immutable (copy-on-write), so callers may dereference its fields freely.
// Returns nil only on a controller that never stored settings (standalone tests);
// callers that may hit that path nil-check or fall back.
func (c *Core) ControllerSettings() *conf.Settings {
	return c.Settings.Load()
}

// LogAPIRequest is a helper to log API requests with common context fields.
func (c *Core) LogAPIRequest(ctx echo.Context, level logger.LogLevel, msg string, fields ...logger.Field) {
	if c.APILogger == nil {
		return // Do nothing if logger isn't initialized
	}

	// Extract common context info
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Create base fields with preallocated capacity
	baseFields := make([]logger.Field, 0, 2+len(fields))
	baseFields = append(baseFields,
		logger.String("path", path),
		logger.String("ip", ip),
	)

	// Append specific fields to base fields
	baseFields = append(baseFields, fields...)

	// Log at the specified level
	c.APILogger.Log(level, msg, baseFields...)
}

// LogInfoIfEnabled logs info message if APILogger is enabled
func (c *Core) LogInfoIfEnabled(msg string, fields ...logger.Field) {
	if c.APILogger != nil {
		c.APILogger.Info(msg, fields...)
	}
}

// LogErrorIfEnabled logs error message if APILogger is enabled
func (c *Core) LogErrorIfEnabled(msg string, fields ...logger.Field) {
	if c.APILogger != nil {
		c.APILogger.Error(msg, fields...)
	}
}

// LogWarnIfEnabled logs warning message if APILogger is enabled
func (c *Core) LogWarnIfEnabled(msg string, fields ...logger.Field) {
	if c.APILogger != nil {
		c.APILogger.Warn(msg, fields...)
	}
}

// LogDebugIfEnabled logs debug message if APILogger is enabled
func (c *Core) LogDebugIfEnabled(msg string, fields ...logger.Field) {
	if c.APILogger != nil {
		c.APILogger.Debug(msg, fields...)
	}
}

// The LogSecurity* helpers mirror the api-module helpers above but write to the
// "security" module logger, so authentication events (form login/logout, OAuth
// callback) are co-located with the OAuth and provider-init logging where
// admins look when debugging auth.

// LogSecurityInfoIfEnabled logs an info message to the security module if enabled.
func (c *Core) LogSecurityInfoIfEnabled(msg string, fields ...logger.Field) {
	if c.securityLogger != nil {
		c.securityLogger.Info(msg, fields...)
	}
}

// LogSecurityWarnIfEnabled logs a warning message to the security module if enabled.
func (c *Core) LogSecurityWarnIfEnabled(msg string, fields ...logger.Field) {
	if c.securityLogger != nil {
		c.securityLogger.Warn(msg, fields...)
	}
}

// LogSecurityErrorIfEnabled logs an error message to the security module if enabled.
func (c *Core) LogSecurityErrorIfEnabled(msg string, fields ...logger.Field) {
	if c.securityLogger != nil {
		c.securityLogger.Error(msg, fields...)
	}
}

// LogSecurityDebugIfEnabled logs a debug message to the security module if enabled.
func (c *Core) LogSecurityDebugIfEnabled(msg string, fields ...logger.Field) {
	if c.securityLogger != nil {
		c.securityLogger.Debug(msg, fields...)
	}
}
