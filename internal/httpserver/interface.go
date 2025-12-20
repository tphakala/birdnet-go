// Package httpserver provides a common interface for HTTP servers used in BirdNET-Go.
// This allows switching between different server implementations (legacy httpcontroller
// and new api server) at runtime based on configuration.
package httpserver

import (
	api "github.com/tphakala/birdnet-go/internal/api/v2"
)

// Server defines the interface for HTTP servers in BirdNET-Go.
// Both the legacy httpcontroller.Server and the new api.Server implement this interface.
type Server interface {
	// Start begins serving HTTP requests in a background goroutine.
	// Both implementations start the server asynchronously and return immediately.
	// Use Shutdown() to stop the server.
	Start()

	// Shutdown gracefully stops the server and releases resources.
	Shutdown() error

	// APIController returns the v2 API controller for SSE broadcasting and other features.
	// Returns nil if the API controller is not initialized.
	APIController() *api.Controller
}
