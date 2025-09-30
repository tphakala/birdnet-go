// Package notification contains push provider interfaces and implementations after refactor
package notification

import "context"

// Provider defines a push delivery backend integrated into the notification package.
// Implementations must be safe for concurrent use.
type Provider interface {
	GetName() string
	ValidateConfig() error
	Send(ctx context.Context, n *Notification) error
	SupportsType(notifType Type) bool
	IsEnabled() bool
}
