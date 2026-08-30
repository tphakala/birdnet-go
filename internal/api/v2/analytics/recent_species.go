package analytics

import (
	"context"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	defaultRecentSpeciesHours   = 4
	minRecentSpeciesHours       = 1
	maxRecentSpeciesHours       = 24
	defaultRecentSpeciesLimit   = 8
	maxRecentSpeciesLimit       = 20
	recentSpeciesBucketsPerHour = 4
	minRecentSpeciesBuckets     = 4
	maxRecentSpeciesBuckets     = maxRecentSpeciesHours * recentSpeciesBucketsPerHour

	recentSpeciesCountScoreMax = 6

	recentSpeciesRecencyWeight    = 0.45
	recentSpeciesConfidenceWeight = 0.45
	recentSpeciesCountWeight      = 0.10
)

// RecentSpeciesActivity represents a species heard in the recent dashboard window.
type RecentSpeciesActivity struct {
	ScientificName    string    `json:"scientific_name"`
	CommonName        string    `json:"common_name"`
	SpeciesCode       string    `json:"species_code,omitempty"`
	Count             int       `json:"count"`
	LatestHeardAt     string    `json:"latest_heard_at"`
	LatestConfidence  float64   `json:"latest_confidence"`
	MaxConfidence     float64   `json:"max_confidence"`
	AvgConfidence     float64   `json:"avg_confidence"`
	ConfidenceTrend   []float64 `json:"confidence_trend"`
	TrendStart        string    `json:"trend_start"`
	TrendHours        int       `json:"trend_hours"`
	Score             float64   `json:"score"`
	LatestDetectionID uint      `json:"latest_detection_id"`
	ThumbnailURL      string    `json:"thumbnail_url,omitempty"`
}

type recentSpeciesActivityParams struct {
	Hours         int
	Limit         int
	Buckets       int
	MinConfidence float64
}

type recentSpeciesAccumulator struct {
	scientificName       string
	commonName           string
	speciesCode          string
	count                int
	latestHeardAt        time.Time
	latestConfidence     float64
	latestDetectionID    uint
	maxConfidence        float64
	confidenceTotal      float64
	bucketMaxConfidences []float64
}

// GetRecentSpeciesActivity handles GET /api/v2/analytics/species/recent.
func (c *Handler) GetRecentSpeciesActivity(ctx echo.Context) error {
	params := c.parseRecentSpeciesActivityParams(ctx)
	now := c.recentSpeciesNow().Truncate(time.Second)
	since := now.Add(-time.Duration(params.Hours) * time.Hour)

	c.LogDebugIfEnabled("Retrieving recent species activity",
		logger.Int("hours", params.Hours),
		logger.Int("limit", params.Limit),
		logger.Int("buckets", params.Buckets),
		logger.Float64("min_confidence", params.MinConfidence),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	queryCtx, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	result, detectionCount, err := c.loadRecentSpeciesActivity(queryCtx, params, since, now)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Recent species activity", "Failed to get recent species activity",
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	c.LogDebugIfEnabled("Recent species activity retrieved",
		logger.Int("species_count", len(result)),
		logger.Int("detection_count", detectionCount),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, result)
}

func (c *Handler) recentSpeciesNow() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Handler) loadRecentSpeciesActivity(ctx context.Context, params recentSpeciesActivityParams, since, now time.Time) ([]RecentSpeciesActivity, int, error) {
	rows, err := c.DS.GetRecentSpeciesData(ctx, since, now, params.MinConfidence, params.Buckets)
	if err != nil {
		return nil, 0, err
	}

	bySpecies := make(map[string]*recentSpeciesAccumulator)
	totalDetections := aggregateRecentSpeciesRows(bySpecies, rows, params.Buckets)

	return c.recentSpeciesActivitiesFromAccumulators(bySpecies, since, now, params), totalDetections, nil
}

func (c *Handler) parseRecentSpeciesActivityParams(ctx echo.Context) recentSpeciesActivityParams {
	hours := clampRecentInt(
		c.parseOptionalPositiveInt(ctx, "hours", defaultRecentSpeciesHours),
		minRecentSpeciesHours,
		maxRecentSpeciesHours,
	)
	limit := clampRecentInt(
		c.parseOptionalPositiveInt(ctx, "limit", defaultRecentSpeciesLimit),
		1,
		maxRecentSpeciesLimit,
	)
	buckets := clampRecentInt(
		c.parseOptionalPositiveInt(ctx, "buckets", hours*recentSpeciesBucketsPerHour),
		minRecentSpeciesBuckets,
		maxRecentSpeciesBuckets,
	)
	minConfidence := clamp01(c.parseOptionalFloat(ctx, "min_confidence", 0.0, apicore.PercentageMultiplier))

	return recentSpeciesActivityParams{
		Hours:         hours,
		Limit:         limit,
		Buckets:       buckets,
		MinConfidence: minConfidence,
	}
}

func aggregateRecentSpeciesRows(bySpecies map[string]*recentSpeciesAccumulator, rows []datastore.RecentSpeciesData, buckets int) int {
	totalDetections := 0
	for i := range rows {
		row := &rows[i]
		if row.Count <= 0 || row.Bucket < 0 || row.Bucket >= buckets {
			continue
		}

		key := row.ScientificName
		if key == "" {
			key = row.CommonName
		}
		if key == "" {
			continue
		}

		acc := bySpecies[key]
		if acc == nil {
			acc = &recentSpeciesAccumulator{
				scientificName:       row.ScientificName,
				commonName:           row.CommonName,
				speciesCode:          row.SpeciesCode,
				bucketMaxConfidences: make([]float64, buckets),
			}
			bySpecies[key] = acc
		}

		acc.count += row.Count
		acc.confidenceTotal += row.ConfidenceTotal
		if row.BucketMaxConfidence > acc.maxConfidence {
			acc.maxConfidence = row.BucketMaxConfidence
		}
		if row.BucketMaxConfidence > acc.bucketMaxConfidences[row.Bucket] {
			acc.bucketMaxConfidences[row.Bucket] = row.BucketMaxConfidence
		}
		if !row.LatestDetectedAt.IsZero() && (acc.latestHeardAt.IsZero() ||
			row.LatestDetectedAt.After(acc.latestHeardAt) ||
			(row.LatestDetectedAt.Equal(acc.latestHeardAt) && row.LatestDetectionID > acc.latestDetectionID)) {
			acc.latestHeardAt = row.LatestDetectedAt
			acc.latestConfidence = row.LatestConfidence
			acc.latestDetectionID = row.LatestDetectionID
		}
		if acc.speciesCode == "" {
			acc.speciesCode = row.SpeciesCode
		}
		if acc.commonName == "" {
			acc.commonName = row.CommonName
		}
		if acc.scientificName == "" {
			acc.scientificName = row.ScientificName
		}
		totalDetections += row.Count
	}
	return totalDetections
}

func (c *Handler) recentSpeciesActivitiesFromAccumulators(bySpecies map[string]*recentSpeciesAccumulator, since, now time.Time, params recentSpeciesActivityParams) []RecentSpeciesActivity {
	if len(bySpecies) == 0 {
		return []RecentSpeciesActivity{}
	}

	result := make([]RecentSpeciesActivity, 0, len(bySpecies))
	for _, acc := range bySpecies {
		if acc.count == 0 || acc.latestHeardAt.IsZero() {
			continue
		}
		result = append(result, buildRecentSpeciesActivityItem(acc, since, now, params.Hours))
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		if result[i].LatestHeardAt != result[j].LatestHeardAt {
			return result[i].LatestHeardAt > result[j].LatestHeardAt
		}
		return result[i].CommonName < result[j].CommonName
	})

	if params.Limit < len(result) {
		return result[:params.Limit]
	}
	return result
}

func buildRecentSpeciesActivityItem(acc *recentSpeciesAccumulator, since, now time.Time, hours int) RecentSpeciesActivity {
	return RecentSpeciesActivity{
		ScientificName:    acc.scientificName,
		CommonName:        recentSpeciesDisplayName(acc),
		SpeciesCode:       acc.speciesCode,
		Count:             acc.count,
		LatestHeardAt:     acc.latestHeardAt.Format(time.RFC3339),
		LatestConfidence:  acc.latestConfidence,
		MaxConfidence:     acc.maxConfidence,
		AvgConfidence:     acc.confidenceTotal / float64(acc.count),
		ConfidenceTrend:   buildRecentSpeciesTrend(acc),
		TrendStart:        since.Format(time.RFC3339),
		TrendHours:        hours,
		Score:             recentSpeciesScore(acc, now, hours),
		LatestDetectionID: acc.latestDetectionID,
		ThumbnailURL:      buildThumbnailURL(acc.scientificName),
	}
}

func buildRecentSpeciesTrend(acc *recentSpeciesAccumulator) []float64 {
	trend := make([]float64, len(acc.bucketMaxConfidences))
	copy(trend, acc.bucketMaxConfidences)
	return trend
}

func recentSpeciesScore(acc *recentSpeciesAccumulator, now time.Time, hours int) float64 {
	window := time.Duration(hours) * time.Hour
	recencyScore := 1 - clamp01(float64(now.Sub(acc.latestHeardAt))/float64(window))
	confidenceScore := (acc.latestConfidence + acc.maxConfidence) / 2
	countScore := clamp01(float64(acc.count) / recentSpeciesCountScoreMax)

	return recencyScore*recentSpeciesRecencyWeight +
		confidenceScore*recentSpeciesConfidenceWeight +
		countScore*recentSpeciesCountWeight
}

func recentSpeciesDisplayName(acc *recentSpeciesAccumulator) string {
	if acc.commonName != "" {
		return acc.commonName
	}
	return acc.scientificName
}

func clampRecentInt(value, minVal, maxVal int) int {
	return min(max(value, minVal), maxVal)
}

func clamp01(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	return min(max(value, 0), 1)
}
