// Package analytics is the api/v2 analytics domain handler. It owns the
// /api/v2/analytics/* species/time/confidence/sun/sources endpoints, the
// geographic /api/v2/range/heatmap endpoint, the /api/v2/insights/* +
// /api/v2/dashboard/kpis endpoints, and the auth-protected
// /api/v2/system/database/overview endpoint.
//
// The Handler embeds *apicore.Core by pointer so the shared dependencies and
// helpers (DS, Repo, Settings, BirdImageCache, SunCalc, V2Manager, TaxonomyDB,
// DetectionRateCache, AuthMiddleware, the HandleError/logging helpers, the
// settings accessors, and GetBirdNETInstance) promote onto it.
//
// Three members are injected from the facade because they live on facade-owned
// subsystems that are not extracted into their own packages:
//   - isClientAuthenticated: the auth-service check, read per request so the
//     public /analytics/sources response anonymizes source display names for
//     unauthenticated callers.
//   - loadCommonNameMap / loadCommonToScientificMap: the cached BirdNET name maps
//     (facade name_maps.go), used to localize species names in insights responses
//     and to resolve a localized species query to its scientific name in the
//     analytics species filter (resolveSpeciesToScientific). The facade owns the
//     name-map plumbing (UpdateCommonNameMap/SetNameResolver) because it is shared
//     with detections, species, settings, and the external internal/analysis
//     callers; analytics only reads it.
package analytics

import (
	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// Handler serves the api/v2 analytics domain endpoints. It embeds the shared
// *apicore.Core (by pointer) and additionally holds the facade-injected auth
// check and cached name-map accessors, plus the lazily-initialized insights
// repository.
type Handler struct {
	*apicore.Core

	isClientAuthenticated     func(ctx echo.Context) bool
	loadCommonNameMap         func() map[string]string
	loadCommonToScientificMap func() map[string]string

	// insightsRepo is the enhanced (v2) database repository backing the
	// /insights/* and /dashboard/kpis endpoints. It is created lazily in
	// RegisterInsightsRoutes (only when the v2 manager is available).
	insightsRepo repository.InsightsRepository
}

// New constructs the analytics domain handler around the shared core and the
// facade-injected dependencies. The injected arguments are the facade's bound
// method values (read at call time so later option-set fields like the auth
// service are observed).
func New(
	core *apicore.Core,
	isClientAuthenticated func(ctx echo.Context) bool,
	loadCommonNameMap func() map[string]string,
	loadCommonToScientificMap func() map[string]string,
) *Handler {
	return &Handler{
		Core:                      core,
		isClientAuthenticated:     isClientAuthenticated,
		loadCommonNameMap:         loadCommonNameMap,
		loadCommonToScientificMap: loadCommonToScientificMap,
	}
}

// RegisterAnalyticsRoutes registers the /api/v2/analytics/* endpoints on the
// supplied group. It preserves the facade's previous initAnalyticsRoutes entry
// (all endpoints publicly accessible, in the same order).
func (c *Handler) RegisterAnalyticsRoutes(g *echo.Group) {
	// Create analytics API group - publicly accessible
	analyticsGroup := g.Group("/analytics")

	// Species analytics routes
	speciesGroup := analyticsGroup.Group("/species")
	speciesGroup.GET("/daily", c.GetDailySpeciesSummary)
	speciesGroup.GET("/daily/batch", c.GetBatchDailySpeciesSummary) // Batch daily summaries endpoint
	speciesGroup.GET("/summary", c.GetSpeciesSummary)
	speciesGroup.GET("/detections/new", c.GetNewSpeciesDetections) // Renamed endpoint
	speciesGroup.GET("/thumbnails", c.GetSpeciesThumbnails)        // Batch thumbnail endpoint
	speciesGroup.GET("/diversity", c.GetSpeciesDiversity)          // Species diversity over time
	speciesGroup.GET("/accumulation", c.GetSpeciesAccumulation)    // Species accumulation curve (biodiversity collector's curve)
	speciesGroup.GET("/phenology", c.GetSpeciesPhenology)          // Arrival/departure phenology (residency-bar Gantt)

	// Time analytics routes (can be implemented later)
	timeGroup := analyticsGroup.Group("/time")
	timeGroup.GET("/hourly", c.GetHourlyAnalytics)
	timeGroup.GET("/hourly/batch", c.GetBatchHourlySpeciesData) // Batch hourly data for multiple species
	timeGroup.GET("/daily", c.GetDailyAnalytics)
	timeGroup.GET("/daily/batch", c.GetBatchDailySpeciesData)              // Batch daily trends for multiple species
	timeGroup.GET("/distribution/hourly", c.GetTimeOfDayDistribution)      // Renamed endpoint for time-of-day distribution
	timeGroup.GET("/distribution/species", c.GetSpeciesHourlyDistribution) // Who-sings-when ridgeline (top-N species hour-of-day)
	timeGroup.GET("/heatmap", c.GetActivityHeatmap)                        // Seasonal density heatmap (date x intra-day slot)
	timeGroup.GET("/dawn-onset", c.GetDawnChorusOnset)                     // Dawn-chorus onset tracker (daily onset vs civil dawn)
	timeGroup.GET("/succession", c.GetAcousticSuccession)                  // Acoustic succession streamgraph (top-N species hour-of-day, stacked)
	timeGroup.GET("/year-over-year", c.GetYearOverYear)                    // Year-over-year tracker (this year-to-date vs same span last year, cumulative)

	// Confidence analytics routes
	confidenceGroup := analyticsGroup.Group("/confidence")
	confidenceGroup.GET("/distribution", c.GetConfidenceDistribution) // Confidence distribution per species (Review & Accuracy)

	// Sun times for the nocturnal activity clock's day/night shading. Additive: the clock's counts
	// come from the existing /time/distribution/hourly endpoint (unchanged); only this sun endpoint
	// is new (design spec section 6.4).
	analyticsGroup.GET("/sun", c.GetAnalyticsSun)

	// Audio sources that have detections in range, powering the analytics hub's source/mic filter.
	// Additive and read-only; names are anonymized for unauthenticated clients (the page is public).
	analyticsGroup.GET("/sources", c.GetAnalyticsSources)
}

// RegisterHeatmapRoutes registers the geographic /api/v2/range/heatmap endpoint
// and the range-filter reload hook on the supplied group. It preserves the
// facade's previous initHeatmapRoutes entry.
func (c *Handler) RegisterHeatmapRoutes(g *echo.Group) {
	g.GET("/range/heatmap", c.GetHeatmapGrid)

	classifier.OnRangeFilterReload(func() {
		InvalidateHeatmapCache()
		// Close the dedicated heatmap service so it gets re-created with the
		// new model on the next request (lazy init in GetHeatmapService).
		classifier.CloseHeatmapService()
	})
}

// RegisterInsightsRoutes lazily initializes the insights repository and registers
// the /api/v2/insights/* + /api/v2/dashboard/kpis endpoints on the supplied
// group. It preserves the facade's previous initInsightsRoutes registration. The
// facade seeds the name maps before calling this (the name-map plumbing is
// facade-owned); when the v2 manager is unavailable nothing is registered.
func (c *Handler) RegisterInsightsRoutes(g *echo.Group) {
	if c.V2Manager == nil {
		return
	}
	db := c.V2Manager.DB()
	isMySQL := c.V2Manager.IsMySQL()
	var useV2Prefix bool
	if tp, ok := c.V2Manager.(interface{ TablePrefix() string }); ok {
		useV2Prefix = tp.TablePrefix() != ""
	}
	c.insightsRepo = repository.NewInsightsRepository(db, useV2Prefix, isMySQL)

	insightsGroup := g.Group("/insights")
	insightsGroup.GET("/expected-today", c.GetExpectedToday)
	insightsGroup.GET("/expected-today/regional", c.GetExpectedTodayRegional)
	insightsGroup.GET("/phantom-species", c.GetPhantomSpecies)
	insightsGroup.GET("/dawn-chorus", c.GetDawnChorus)
	insightsGroup.GET("/migration", c.GetMigration)

	g.GET("/dashboard/kpis", c.GetDashboardKPIs)
}

// RegisterDatabaseOverviewRoutes registers the auth-protected
// /api/v2/system/database/overview endpoint on the supplied group. It preserves
// the facade's previous initDatabaseOverviewRoutes registration (called from the
// facade initSystemRoutes at the same position under the /system namespace).
func (c *Handler) RegisterDatabaseOverviewRoutes(g *echo.Group) {
	// Create a database group under system
	dbGroup := g.Group("/system/database")

	// Get the appropriate auth middleware
	authMiddleware := c.AuthMiddleware

	dbGroup.GET("/overview", c.GetDatabaseOverview, authMiddleware)

	c.LogInfoIfEnabled("Database overview route initialized")
}
