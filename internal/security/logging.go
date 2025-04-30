package security

import (
	"fmt"
	"log/slog"

	"github.com/tphakala/birdnet-go/internal/logging"
)

// Package-level logger for security related events
var (
	securityLogger *slog.Logger
	// securityLogCloser func() error // Optional closer func
	// TODO: Call securityLogCloser during graceful shutdown if needed
)

func init() {
	var err error
	// Default level is Info. Security events might warrant Debug in some cases,
	// but Info is a safer default to avoid overly verbose logs.
	securityLogger, _, err = logging.NewFileLogger("logs/security.log", "security", slog.LevelInfo)
	if err != nil {
		logging.Error("Failed to initialize security file logger", "error", err)
		// Fallback to the default structured logger
		securityLogger = logging.Structured().With("service", "security")
		if securityLogger == nil {
			panic(fmt.Sprintf("Failed to initialize any logger for security service: %v", err))
		}
		logging.Warn("Security service falling back to default logger due to file logger initialization error.")
	} else {
		logging.Info("Security file logger initialized successfully", "path", "logs/security.log")
	}
	// securityLogCloser = closer
}
