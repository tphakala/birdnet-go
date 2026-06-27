// Package detections is the api/v2 detections domain handler. It owns the
// /api/v2/detections/* CRUD, review, lock, ignore, and batch endpoints plus the
// POST /api/v2/search endpoint (detection search lives in this domain because it
// queries the same datastore and shares the detection response types).
//
// The Handler embeds *apicore.Core by pointer so the shared dependencies and
// helpers (DS, Processor, SunCalc, BirdImageCache, DetectionCache, SFS,
// AuthMiddleware, the HandleError/logging helpers, and the settings accessors)
// promote onto it.
//
// Seven members are injected from the facade because they live on facade-owned
// subsystems that have not been extracted into their own domains yet:
//   - settingsMutex / getSettingsOrFallback / publishAndSaveSettings /
//     handleSettingsChanges: the facade settings-save machinery, used by the
//     review/ignore exclude-list mutation (toggleSpeciesInIgnoredList,
//     addSpeciesToIgnoredList). settingsMutex MUST be the same *sync.RWMutex the
//     settings update handlers lock so these writes serialise against them (the
//     integrations/tls precedent).
//   - isClientAuthenticated: the auth-service check (audio_level.go), read per
//     request so public detection/search responses can strip source metadata for
//     unauthenticated callers.
//   - loadCommonNameMap / loadCommonToScientificMap: the cached BirdNET name maps
//     (insights.go) used to resolve and localize species names in responses and
//     in the search resolver (the species precedent injects loadCommonNameMap).
package detections

import (
	"fmt"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// queryValueTrue is the canonical "true" query-parameter value parsed by the
// detection list handlers. The facade keeps its own QueryValueTrue constant for
// the other (not-yet-extracted) consumers (audio_hls); this local copy avoids
// rippling that shared constant into apicore for a single domain.
const queryValueTrue = "true"

// queryValueAny is the "any" status sentinel used by the search request
// normalization. It is detection/search-owned (the only consumer).
const queryValueAny = "any"

// weatherUnitMetric is the metric weather-unit identifier returned by
// getWeatherUnits. It is detection-owned (the only consumer after the weather
// provider identifiers moved to the integrations domain).
const weatherUnitMetric = "metric"

// Verification status constants for detections.
const (
	VerificationStatusCorrect       = "correct"
	VerificationStatusFalsePositive = "false_positive"
	VerificationStatusUnverified    = "unverified"
)

// Handler serves the api/v2 detections + search domain endpoints. It embeds the
// shared *apicore.Core (by pointer) and additionally holds the facade-injected
// settings-save machinery, the auth check, and the cached name-map accessors.
type Handler struct {
	*apicore.Core

	settingsMutex             *sync.RWMutex
	getSettingsOrFallback     func() *conf.Settings
	publishAndSaveSettings    func(current, updated *conf.Settings) error
	handleSettingsChanges     func(oldSettings, currentSettings *conf.Settings) error
	isClientAuthenticated     func(ctx echo.Context) bool
	loadCommonNameMap         func() map[string]string
	loadCommonToScientificMap func() map[string]string
}

// New constructs the detections domain handler around the shared core and the
// facade-injected dependencies. settingsMutex is passed by pointer so the
// exclude-list mutation serialises against the facade settings update handlers;
// the remaining arguments are the facade's bound method values (read at call time
// so later option-set fields like authService are observed). c is fully
// constructed when the facade calls this (before the functional options loop),
// and these deps are stable for its lifetime.
func New(
	core *apicore.Core,
	settingsMutex *sync.RWMutex,
	getSettingsOrFallback func() *conf.Settings,
	publishAndSaveSettings func(current, updated *conf.Settings) error,
	handleSettingsChanges func(oldSettings, currentSettings *conf.Settings) error,
	isClientAuthenticated func(ctx echo.Context) bool,
	loadCommonNameMap func() map[string]string,
	loadCommonToScientificMap func() map[string]string,
) *Handler {
	return &Handler{
		Core:                      core,
		settingsMutex:             settingsMutex,
		getSettingsOrFallback:     getSettingsOrFallback,
		publishAndSaveSettings:    publishAndSaveSettings,
		handleSettingsChanges:     handleSettingsChanges,
		isClientAuthenticated:     isClientAuthenticated,
		loadCommonNameMap:         loadCommonNameMap,
		loadCommonToScientificMap: loadCommonToScientificMap,
	}
}

// RegisterSearchRoutes registers the search-related routes on the supplied group.
// Search is registered as its own ordered initializer (preceding the detection
// routes), mirroring the facade's previous initSearchRoutes entry.
func (c *Handler) RegisterSearchRoutes(g *echo.Group) {
	c.LogInfoIfEnabled("Initializing search routes")

	// Search endpoints - publicly accessible
	g.POST("/search", c.HandleSearch)

	c.LogInfoIfEnabled("Search routes initialized successfully")
}

// RegisterDetectionRoutes registers all detection-related API endpoints on the
// supplied group. It preserves the facade's previous initDetectionRoutes entry,
// including the datastore-disabled guard.
func (c *Handler) RegisterDetectionRoutes(g *echo.Group) {
	// Detection handlers dereference c.DS. Honor the constructor's "datastore disabled"
	// mode (NewWithOptions permits a nil datastore) by not registering this route group
	// when there is no datastore, instead of registering handlers that would panic.
	if c.DS == nil {
		c.LogWarnIfEnabled("Skipping detection routes: datastore is not available")
		return
	}

	// DetectionCache is already initialized by the constructor (NewWithOptions); do not
	// re-create it here, which would orphan the constructor's cache (and its janitor).

	// Detection endpoints - publicly accessible
	//
	// Note: Detection data is decoupled from weather data by design.
	// To get weather information for a specific detection, use the
	// /api/v2/weather/detection/:id endpoint after fetching the detection.
	g.GET("/detections", c.GetDetections)
	g.GET("/detections/:id", c.GetDetection)
	g.GET("/detections/recent", c.GetRecentDetections)
	g.GET("/detections/:id/time-of-day", c.GetDetectionTimeOfDay)

	// Protected detection management endpoints
	detectionGroup := g.Group("/detections", c.AuthMiddleware)
	detectionGroup.DELETE("/:id", c.DeleteDetection)
	detectionGroup.POST("/:id/review", c.ReviewDetection)
	detectionGroup.POST("/:id/lock", c.LockDetection)
	detectionGroup.POST("/ignore", c.IgnoreSpecies)
	detectionGroup.GET("/ignored", c.GetExcludedSpecies)

	// Batch operation endpoints
	batchGroup := detectionGroup.Group("/batch")
	batchGroup.POST("/delete", c.BatchDeleteDetections)
	batchGroup.POST("/review", c.BatchReviewDetections)
	batchGroup.POST("/lock", c.BatchLockDetections)
	batchGroup.POST("/resolve", c.BatchResolveDetections)
}

// validateDateOrder validates that start date is not after end date.
// Returns a descriptive error if invalid, nil if valid or if either date is empty.
// Detection-owned (used by the detection list and search handlers).
func validateDateOrder(startDate, endDate string) error {
	if startDate == "" || endDate == "" {
		return nil
	}
	start, _ := time.Parse(time.DateOnly, startDate)
	end, _ := time.Parse(time.DateOnly, endDate)
	if start.After(end) {
		return fmt.Errorf("start date (%s) must be earlier than or equal to end date (%s)", startDate, endDate)
	}
	return nil
}

// validateDateFormat validates a date string is in YYYY-MM-DD format.
// Returns a descriptive error if invalid, nil if valid or empty.
// Detection-owned (used by the search request validation).
func validateDateFormat(dateStr, paramName string) error {
	if dateStr == "" {
		return nil
	}
	if _, err := time.Parse(time.DateOnly, dateStr); err != nil {
		return fmt.Errorf("invalid %s format '%s', use YYYY-MM-DD", paramName, dateStr)
	}
	return nil
}
