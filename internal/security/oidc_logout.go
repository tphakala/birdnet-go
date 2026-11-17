package security

import "github.com/tphakala/birdnet-go/internal/logger"

// LogOIDCLogoutEvent reports the outcome of OIDC sign-out at the identity
// provider to the security log and to the console and main log. It exists for
// internal/api/auth, which builds the end-session URL and would otherwise reach
// only the security log file.
func LogOIDCLogoutEvent(level logger.LogLevel, msg string, fields ...logger.Field) {
	logOIDCEvent(level, msg, fields...)
}
