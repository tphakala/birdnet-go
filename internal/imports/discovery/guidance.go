package discovery

import (
	"strings"
	"sync"
)

// Guidance holds instructions for the user when a candidate has a known problem.
type Guidance struct {
	// Key is the unique identifier for this guidance (e.g., "invalid_schema").
	Key string
	// Message is the user-facing instruction (not localized at this layer).
	Message string
}

var (
	guidanceMu sync.RWMutex
	guidance   = map[string]Guidance{
		ReasonPermissionDenied: {
			Key:     ReasonPermissionDenied,
			Message: "BirdNET-Go does not have read permission for this file. Please check the file owner and permissions.",
		},
		ReasonInvalidSchema: {
			Key:     ReasonInvalidSchema,
			Message: "This database does not have the required BirdNET-Pi schema (missing columns or table).",
		},
		ReasonOpenFailed: {
			Key:     ReasonOpenFailed,
			Message: "The file is not a valid SQLite database or could not be opened.",
		},
	}
)

// RegisterGuidance adds or replaces guidance for a Reason.
// It is safe for concurrent use, primarily intended for init() registration
// of platform-specific guidance.
func RegisterGuidance(reason, message string) {
	guidanceMu.Lock()
	defer guidanceMu.Unlock()
	guidance[reason] = Guidance{
		Key:     reason,
		Message: strings.TrimSpace(message),
	}
}

// GetGuidance returns the guidance for a reason, or a fallback if unknown.
func GetGuidance(reason string) Guidance {
	guidanceMu.RLock()
	defer guidanceMu.RUnlock()
	if g, ok := guidance[reason]; ok {
		return g
	}
	return Guidance{
		Key:     reason,
		Message: "An unknown error prevented reading this database.",
	}
}
