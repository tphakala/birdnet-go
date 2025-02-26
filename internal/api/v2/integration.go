// internal/api/v2/integration.go
package api

import (
	"log"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// InitializeAPI sets up the JSON API endpoints in the provided Echo instance
func InitializeAPI(
	e *echo.Echo,
	ds datastore.Interface,
	settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache,
	sunCalc *suncalc.SunCalc,
	controlChan chan string,
	logger *log.Logger,
) *Controller {

	// Create new API controller
	apiController := New(e, ds, settings, birdImageCache, sunCalc, controlChan, logger)

	if logger != nil {
		logger.Printf("JSON API v2 initialized at /api/v2")
	}

	return apiController
}
