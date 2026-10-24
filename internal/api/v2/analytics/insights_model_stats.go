package analytics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// modelStatsMaxRangeDays caps the inclusive length of a model contribution
// statistics range (a leap year). SQLite serializes all access through one
// connection (internal/datastore/v2/manager.go), so the cap bounds how long one
// request holds it; multi-year ranges on small hardware would approach
// insightsQueryTimeout.
const modelStatsMaxRangeDays = 366

// modelStatsOperation labels log lines and parameter errors of the endpoint.
const modelStatsOperation = "model contribution stats"

var (
	// ErrModelStatsRangeTooLong is returned by modelStatsRange when the inclusive
	// day count exceeds modelStatsMaxRangeDays.
	ErrModelStatsRangeTooLong = errors.NewStd("model contribution stats range too long")
	// ErrModelStatsInvalidDate is returned by modelStatsRange when a date does not
	// parse or the end lies before the start.
	ErrModelStatsInvalidDate = errors.NewStd("model contribution stats date invalid")
)

// ModelContributionStatsResponse is the response for GET /api/v2/insights/model-contributions.
// Counts come from contribution rows, which are best-effort and absent for
// detections migrated from the legacy database, so they are lower bounds.
type ModelContributionStatsResponse struct {
	StartDate                   string                  `json:"start_date"` // YYYY-MM-DD, inclusive
	EndDate                     string                  `json:"end_date"`   // YYYY-MM-DD, inclusive, after the default is applied
	TotalDetections             int64                   `json:"total_detections"`
	DetectionsWithContributions int64                   `json:"detections_with_contributions"`
	TotalHits                   int64                   `json:"total_hits"`
	Models                      []ModelContributionItem `json:"models"`
	Agreements                  []ModelAgreementItem    `json:"agreements"`
}

// ModelContributionItem is one model in the model contribution statistics response.
type ModelContributionItem struct {
	ModelID        uint                   `json:"model_id"`
	Name           string                 `json:"name"`
	Version        string                 `json:"version"`
	Variant        string                 `json:"variant"`
	ModelType      string                 `json:"model_type"`
	Detections     int64                  `json:"detections"`
	Hits           int64                  `json:"hits"`
	HitShare       float64                `json:"hit_share"`
	SoleDetections int64                  `json:"sole_detections"`
	Confidence     ModelConfidenceSummary `json:"confidence"`
	Outcomes       ModelOutcomeCounts     `json:"outcomes"`
}

// ModelConfidenceSummary summarizes a model's confidence over the range. Min and
// Mean are null when no contribution row in the range has recorded stats.
type ModelConfidenceSummary struct {
	Min                 *float64 `json:"min"`
	Mean                *float64 `json:"mean"`
	Max                 float64  `json:"max"`
	DetectionsWithStats int64    `json:"detections_with_stats"`
}

// ModelOutcomeCounts partitions a model's detections by review outcome.
type ModelOutcomeCounts struct {
	Correct       int64 `json:"correct"`
	FalsePositive int64 `json:"false_positive"`
	Unreviewed    int64 `json:"unreviewed"`
}

// ModelAgreementItem counts the detections two models both contributed to.
type ModelAgreementItem struct {
	ModelAID         uint  `json:"model_a_id"`
	ModelBID         uint  `json:"model_b_id"`
	SharedDetections int64 `json:"shared_detections"`
}

// modelStatsRange converts inclusive YYYY-MM-DD dates to the half-open Unix
// seconds range [start, end) of local midnights in loc, the way every other
// analytics range is built. An empty endDate defaults to startDate plus
// defaultAnalyticsDays. endDateOut is the inclusive end date after defaulting.
func modelStatsRange(startDate, endDate string, loc *time.Location) (start, end int64, endDateOut string, err error) {
	startDay, err := time.ParseInLocation(time.DateOnly, startDate, loc)
	if err != nil {
		return 0, 0, "", fmt.Errorf("%w: start_date: %w", ErrModelStatsInvalidDate, err)
	}
	var endDay time.Time
	if endDate == "" {
		endDay = startDay.AddDate(0, 0, defaultAnalyticsDays)
	} else {
		endDay, err = time.ParseInLocation(time.DateOnly, endDate, loc)
		if err != nil {
			return 0, 0, "", fmt.Errorf("%w: end_date: %w", ErrModelStatsInvalidDate, err)
		}
	}

	// Count days on the UTC calendar so DST changes cannot skew the cap.
	startUTC := time.Date(startDay.Year(), startDay.Month(), startDay.Day(), 0, 0, 0, 0, time.UTC)
	endUTC := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 0, 0, 0, 0, time.UTC)
	days := int(endUTC.Sub(startUTC).Hours()/apicore.HoursPerDay) + 1
	if days < 1 {
		return 0, 0, "", fmt.Errorf("%w: end_date before start_date", ErrModelStatsInvalidDate)
	}
	if days > modelStatsMaxRangeDays {
		return 0, 0, "", fmt.Errorf("%w: %d days", ErrModelStatsRangeTooLong, days)
	}

	// AddDate, never +86400: a DST day is not 24 hours long.
	return startDay.Unix(), endDay.AddDate(0, 0, 1).Unix(), endDay.Format(time.DateOnly), nil
}

// GetModelContributionStats returns per-model contribution statistics for
// GET /api/v2/insights/model-contributions?start_date=&end_date=. It answers 409
// unless the enhanced (v2) database is in use.
func (c *Handler) GetModelContributionStats(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}
	return c.getModelContributionStatsImpl(ctx)
}

func (c *Handler) getModelContributionStatsImpl(ctx echo.Context) error {
	// Each validator has already written the 400 when it returns non-nil.
	if err := c.requireQueryParam(ctx, "start_date", modelStatsOperation); err != nil {
		return nil
	}
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")
	if err := c.validateDateFormatStrictWithResponse(ctx, startDate, "start_date", modelStatsOperation); err != nil {
		return nil
	}
	if err := c.validateDateFormatStrictWithResponse(ctx, endDate, "end_date", modelStatsOperation); err != nil {
		return nil
	}
	if err := c.validateDateRangeWithResponse(ctx, startDate, endDate, modelStatsOperation); err != nil {
		return nil
	}

	start, end, endDateOut, err := modelStatsRange(startDate, endDate, time.Local)
	switch {
	case errors.Is(err, ErrModelStatsRangeTooLong):
		return c.HandleError(ctx, err, fmt.Sprintf("Date range too long. Maximum: %d days", modelStatsMaxRangeDays), http.StatusBadRequest)
	case err != nil:
		return c.HandleError(ctx, err, "Invalid date parameters", http.StatusBadRequest)
	}

	reqCtx, cancel := context.WithTimeout(ctx.Request().Context(), insightsQueryTimeout)
	defer cancel()

	stats, err := c.insightsRepo.GetModelContributionStats(reqCtx, start, end)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Model contribution stats", "Failed to query model contribution stats")
	}

	resp := ModelContributionStatsResponse{
		StartDate:                   startDate,
		EndDate:                     endDateOut,
		TotalDetections:             stats.TotalDetections,
		DetectionsWithContributions: stats.DetectionsWithContributions,
		TotalHits:                   stats.TotalHits,
		Models:                      make([]ModelContributionItem, 0, len(stats.Models)),
		Agreements:                  make([]ModelAgreementItem, 0, len(stats.Agreements)),
	}
	for i := range stats.Models {
		m := &stats.Models[i]
		resp.Models = append(resp.Models, ModelContributionItem{
			ModelID:        m.ModelID,
			Name:           m.Name,
			Version:        m.Version,
			Variant:        m.Variant,
			ModelType:      m.ModelType,
			Detections:     m.Detections,
			Hits:           m.Hits,
			HitShare:       m.HitShare,
			SoleDetections: m.SoleDetections,
			Confidence: ModelConfidenceSummary{
				Min:                 m.MinConfidence,
				Mean:                m.MeanConfidence,
				Max:                 m.MaxConfidence,
				DetectionsWithStats: m.DetectionsWithStats,
			},
			Outcomes: ModelOutcomeCounts{
				Correct:       m.Correct,
				FalsePositive: m.FalsePositive,
				Unreviewed:    m.Unreviewed,
			},
		})
	}
	for _, a := range stats.Agreements {
		resp.Agreements = append(resp.Agreements, ModelAgreementItem{
			ModelAID:         a.ModelAID,
			ModelBID:         a.ModelBID,
			SharedDetections: a.SharedDetections,
		})
	}
	return ctx.JSON(http.StatusOK, resp)
}
