// internal/api/v2/detections/species_stats.go
package detections

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	detectionPkg "github.com/tphakala/birdnet-go/internal/detection"
)

// Species statistics query parameter, messages and log text (file-local).
const (
	speciesStatsNameParam       = "scientific_name"
	speciesStatsV2RequiredMsg   = "Species statistics require the enhanced (v2) database"
	speciesStatsNameMissingMsg  = "The scientific_name parameter is required"
	speciesStatsNameInvalidMsg  = "Invalid scientific name"
	speciesStatsLoadFailedMsg   = "Failed to load species statistics"
	speciesStatsRequestEndedLog = "species statistics request ended before the read finished"
	speciesStatsV2RequiredLog   = "species statistics requested without the enhanced database"
)

// speciesStatsReader reads the statistics of one species. The enhanced (v2)
// detection repository implements it; the shared detection repository interface
// does not, because the legacy store behind it has no such operation.
type speciesStatsReader interface {
	GetSpeciesStats(ctx context.Context, scientificName string) (*repository.SpeciesStats, error)
}

// SpeciesStatsResponse is the body of GET /species/stats. Every key is always
// present; a value that does not exist is null.
type SpeciesStatsResponse struct {
	// ScientificName is the species the statistics belong to.
	ScientificName string `json:"scientificName"`
	// Count is the number of detections not reviewed as false positive.
	Count int64 `json:"count"`
	// FirstHeard is the earliest such detection, RFC3339 in the station zone.
	FirstHeard *string `json:"firstHeard"`
	// LastHeard is the latest such detection, RFC3339 in the station zone.
	LastHeard *string `json:"lastHeard"`
	// AvgConfidence is the mean confidence of such detections, 0 to 1.
	AvgConfidence *float64 `json:"avgConfidence"`
	// MaxConfidence is the highest confidence of such detections, 0 to 1.
	MaxConfidence *float64 `json:"maxConfidence"`
	// Reviews counts the detections of the species by review verdict.
	Reviews SpeciesReviewCounts `json:"reviews"`
}

// SpeciesReviewCounts is how reviewers judged the detections of a species.
type SpeciesReviewCounts struct {
	// Correct is the number of detections reviewed as correct.
	Correct int64 `json:"correct"`
	// FalsePositive is the number of detections reviewed as false positive.
	FalsePositive int64 `json:"falsePositive"`
}

// GetSpeciesStats handles GET /api/v2/species/stats?scientific_name=<name>. It
// answers 200 with the all-time statistics of the species, zeros and nulls
// included for a name no detection was stored under, 409 unless the enhanced (v2)
// database is in use, 400 for a missing name or a name without a species part and
// 500 for a database error.
func (c *Handler) GetSpeciesStats(ctx echo.Context) error {
	if c.speciesStats == nil || !datastoreV2.IsEnhancedDatabase() {
		return c.respondEnhancedDatabaseRequired(ctx, speciesStatsV2RequiredMsg, speciesStatsV2RequiredLog)
	}

	name := ctx.QueryParam(speciesStatsNameParam)
	if strings.TrimSpace(name) == "" {
		return c.HandleError(ctx, nil, speciesStatsNameMissingMsg, http.StatusBadRequest)
	}
	species := detectionPkg.ExtractScientificName(name)
	if species == "" {
		return c.HandleError(ctx, nil, speciesStatsNameInvalidMsg, http.StatusBadRequest)
	}

	// The repository extracts the species itself, so it gets the name unchanged.
	stats, err := c.speciesStats.GetSpeciesStats(ctx.Request().Context(), name)
	if err != nil {
		if handled, respErr := c.respondIfRequestEnded(ctx, err, speciesStatsRequestEndedLog); handled {
			return respErr
		}
		return c.HandleError(ctx, err, speciesStatsLoadFailedMsg, http.StatusInternalServerError)
	}

	resp := SpeciesStatsResponse{
		ScientificName: species,
		Count:          stats.Detections,
		Reviews: SpeciesReviewCounts{
			Correct:       stats.CorrectReviews,
			FalsePositive: stats.FalsePositiveReviews,
		},
	}
	if stats.Detections > 0 {
		resp.FirstHeard = stationTime(stats.FirstDetectedAt)
		resp.LastHeard = stationTime(stats.LastDetectedAt)
		resp.AvgConfidence = &stats.AvgConfidence
		resp.MaxConfidence = &stats.MaxConfidence
	}
	return ctx.JSON(http.StatusOK, resp)
}

// stationTime renders Unix seconds as RFC3339 in the station zone, like a
// detection's timestamp. Any epoch is rendered as is, including 0 and instants
// before 1970; the caller leaves the time out when there is no detection.
func stationTime(epoch int64) *string {
	formatted := time.Unix(epoch, 0).In(datastore.Timezone()).Format(time.RFC3339)
	return &formatted
}
