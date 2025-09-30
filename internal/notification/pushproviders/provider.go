package pushproviders

import (
	"context"
	"time"
)

// Payload is the provider-facing notification data
// (decoupled from internal Notification to avoid import cycles)
type Payload struct {
	ID        string
	Type      string
	Priority  string
	Title     string
	Message   string
	Component string
	Timestamp time.Time
	Metadata  map[string]any
}

// Provider defines a push delivery backend
// Implementations must be safe for concurrent use.
type Provider interface {
	GetName() string
	ValidateConfig() error
	Send(ctx context.Context, payload *Payload) error
	SupportsType(notifType string) bool
	IsEnabled() bool
}
