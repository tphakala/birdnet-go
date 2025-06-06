// Package serviceapi defines common interfaces for the application's components
// to avoid circular dependencies and enforce clear API boundaries.
package serviceapi

import (
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdnet"
)

// BirdNETProvider defines the minimal interface for accessing BirdNET functionality
type BirdNETProvider interface {
	// GetBirdNET returns the BirdNET service instance
	GetBirdNET() *birdnet.BirdNET
}

// ProcessorProvider defines the interface for accessing the analysis processor
type ProcessorProvider interface {
	// GetProcessor returns the analysis processor which implements BirdNETProvider
	GetProcessor() BirdNETProvider
}

// SecurityProvider defines the interface for authentication and access control
type SecurityProvider interface {
	// IsAccessAllowed determines if the current context has access
	IsAccessAllowed(ctx echo.Context) bool
}

// ServerFacade combines security and processor interfaces
// This interface is used by HTTP controllers to interact with the server
type ServerFacade interface {
	// IsAccessAllowed determines if the given context has access
	IsAccessAllowed(c echo.Context) bool
	// GetProcessor returns the analysis processor
	GetProcessor() BirdNETProvider
}
