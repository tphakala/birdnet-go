// internal/api/v2/analytics/review_stats.go
package analytics

import (
	"context"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// speciesReviewStatsDatastore is the optional datastore capability required to
// compute per-species review statistics. Datastores that do not implement it
// cause GetSpeciesReviewStats to return HTTP 501.
type speciesReviewStatsDatastore interface {
	GetSpeciesReviewStats(ctx context.Context) ([]datastore.SpeciesReviewStat, error)
}

// GetSpeciesReviewStats handles GET /api/v2/analytics/species/review-stats.
// It returns per-species total/verified/rejected counts across all time for the
// analytics "Manage" view. Returns HTTP 501 when the active datastore cannot
// provide review statistics.
func (c *Handler) GetSpeciesReviewStats(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path

	ds, ok := c.DS.(speciesReviewStatsDatastore)
	if !ok {
		return c.HandleError(ctx, fmt.Errorf("datastore does not support review stats"),
			"Review statistics are not supported by the active datastore", http.StatusNotImplemented)
	}

	queryCtx, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	stats, err := ds.GetSpeciesReviewStats(queryCtx)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Species review stats", "Failed to get species review stats",
			logger.String("ip", ip),
			logger.String("path", path),
		)
	}

	c.LogInfoIfEnabled("Species review stats retrieved",
		logger.Int("count", len(stats)),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	return ctx.JSON(http.StatusOK, stats)
}
