// internal/api/v2/integration.go
package api

import (
	"log"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// InitializeAPI sets up the JSON API endpoints in the provided Echo instance
// The returned Controller has a Shutdown method that should be called during application shutdown
// to properly clean up resources and stop background goroutines
func InitializeAPI(
	e *echo.Echo,
	ds datastore.Interface,
	settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache,
	sunCalc *suncalc.SunCalc,
	controlChan chan string,
	logger *log.Logger,
	proc *processor.Processor,
) *Controller {

	// Create new API controller
	apiController, err := New(e, ds, settings, birdImageCache, sunCalc, controlChan, logger)
	if err != nil {
		// Handle the error appropriately, perhaps log and panic as API init failure is critical
		logger.Fatalf("Failed to initialize API controller: %v", err)
	}

	// Set the processor
	apiController.Processor = proc

	if logger != nil {
		logger.Printf("JSON API v2 initialized at /api/v2")
	}

	return apiController
}
