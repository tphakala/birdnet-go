// Package app provides the top-level application lifecycle management.
package app

import "context"

// Service represents a managed subsystem with a lifecycle.
type Service interface {
	// Name returns a human-readable identifier for logging and diagnostics.
	Name() string
	// Start initializes and starts the service. The context carries startup deadline.
	Start(ctx context.Context) error
	// Stop gracefully shuts down the service. The context carries shutdown deadline.
	Stop(ctx context.Context) error
}

// ShutdownTier controls the order and timeout budget during shutdown.
type ShutdownTier int

const (
	// TierNetwork is for services with external connections (API server, monitors).
	// Stopped first with the majority of the shutdown budget.
	TierNetwork ShutdownTier = iota
	// TierCore is for critical data services (database, WAL checkpoint).
	// Stopped last with a guaranteed independent timeout budget.
	TierCore
)

// TieredService is optionally implemented by services that need a specific shutdown tier.
// Services that don't implement this default to TierNetwork.
type TieredService interface {
	ShutdownTier() ShutdownTier
}
