//go:build test
// +build test

// Package api provides test utilities for API tests
package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// HandleMalformedJSON is a test handler for testing malformed JSON payloads.
// It is tagged to be excluded from production builds.
func (c *Controller) HandleMalformedJSON(ctx echo.Context) error {
	// Simulate a binding error
	return echo.NewHTTPError(http.StatusBadRequest, "Simulated JSON binding error")
}
