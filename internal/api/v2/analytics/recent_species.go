package analytics

import (
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

	recentSpeciesCandidateMultiplier = 200
	recentSpeciesMinCandidates       = 200
	recentSpeciesMaxCandidates       = 5000
	recentSpeciesCountScoreMax       = 6
	recentSpeciesSortByDateDesc      = "date_desc"

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
	now := time.Now()
	since := now.Add(-time.Duration(params.Hours) * time.Hour)
	candidateLimit := recentSpeciesCandidateLimit(params.Limit)

	c.LogDebugIfEnabled("Retrieving recent species activity",
		logger.Int("hours", params.Hours),
		logger.Int("limit", params.Limit),
		logger.Int("buckets", params.Buckets),
		logger.Float64("min_confidence", params.MinConfidence),
		logger.Int("candidate_limit", candidateLimit),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	notes, err := c.searchRecentSpeciesCandidateNotes(params, since, now, candidateLimit)
	if err != nil {
		c.LogErrorIfEnabled("Failed to get recent species activity",
			logger.Error(err),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		return c.HandleError(ctx, err, "Failed to get recent species activity", http.StatusInternalServerError)
	}

	result := c.buildRecentSpeciesActivity(notes, since, now, params)
	c.LogDebugIfEnabled("Recent species activity retrieved",
		logger.Int("species_count", len(result)),
		logger.Int("candidate_count", len(notes)),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, result)
}

func (c *Handler) searchRecentSpeciesCandidateNotes(params recentSpeciesActivityParams, since, now time.Time, candidateLimit int) ([]datastore.Note, error) {
	ranges := recentSpeciesSearchDateRanges(since, now)
	notes := make([]datastore.Note, 0, candidateLimit)

	for i := range ranges {
		dateRange := ranges[i]
		filters := &datastore.AdvancedSearchFilters{
			DateRange: &dateRange,
			SortBy:    recentSpeciesSortByDateDesc,
			Limit:     candidateLimit,
		}
		if params.MinConfidence > 0 {
			filters.Confidence = &datastore.ConfidenceFilter{
				Operator: ">=",
				Value:    params.MinConfidence,
			}
		}

		dateNotes, _, err := c.DS.SearchNotesAdvanced(filters)
		if err != nil {
			return nil, err
		}
		notes = append(notes, dateNotes...)
	}

	return notes, nil
}

func recentSpeciesSearchDateRanges(since, now time.Time) []datastore.DateRange {
	startDate := recentSpeciesDateStart(since)
	endDate := recentSpeciesDateStart(now)
	ranges := make([]datastore.DateRange, 0, 2)

	for date := endDate; !date.Before(startDate); date = date.AddDate(0, 0, -1) {
		ranges = append(ranges, datastore.DateRange{Start: date, End: date})
	}

	return ranges
}

func recentSpeciesDateStart(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
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

func recentSpeciesCandidateLimit(limit int) int {
	return clampRecentInt(
		limit*recentSpeciesCandidateMultiplier,
		recentSpeciesMinCandidates,
		recentSpeciesMaxCandidates,
	)
}

func (c *Handler) buildRecentSpeciesActivity(notes []datastore.Note, since, now time.Time, params recentSpeciesActivityParams) []RecentSpeciesActivity {
	if len(notes) == 0 {
		return []RecentSpeciesActivity{}
	}

	bucketDuration := time.Duration(params.Hours) * time.Hour / time.Duration(params.Buckets)
	bySpecies := make(map[string]*recentSpeciesAccumulator)

	for i := range notes {
		note := &notes[i]
		detectedAt, ok := parseNoteDetectedAt(note)
		if !ok || detectedAt.Before(since) || detectedAt.After(now) {
			continue
		}
		if note.Confidence < params.MinConfidence {
			continue
		}

		key := recentSpeciesKey(note)
		if key == "" {
			continue
		}
		acc := bySpecies[key]
		if acc == nil {
			acc = &recentSpeciesAccumulator{
				scientificName:       note.ScientificName,
				commonName:           note.CommonName,
				speciesCode:          note.SpeciesCode,
				bucketMaxConfidences: make([]float64, params.Buckets),
			}
			bySpecies[key] = acc
		}

		updateRecentSpeciesAccumulator(acc, note, detectedAt, since, bucketDuration, params.Buckets)
	}

	return c.recentSpeciesActivitiesFromAccumulators(bySpecies, since, now, params)
}

func updateRecentSpeciesAccumulator(acc *recentSpeciesAccumulator, note *datastore.Note, detectedAt, since time.Time, bucketDuration time.Duration, buckets int) {
	acc.count++
	acc.confidenceTotal += note.Confidence
	if note.Confidence > acc.maxConfidence {
		acc.maxConfidence = note.Confidence
	}
	if detectedAt.After(acc.latestHeardAt) || acc.latestHeardAt.IsZero() {
		acc.latestHeardAt = detectedAt
		acc.latestConfidence = note.Confidence
		acc.latestDetectionID = note.ID
	}
	if acc.speciesCode == "" && note.SpeciesCode != "" {
		acc.speciesCode = note.SpeciesCode
	}
	if acc.commonName == "" && note.CommonName != "" {
		acc.commonName = note.CommonName
	}
	if acc.scientificName == "" && note.ScientificName != "" {
		acc.scientificName = note.ScientificName
	}

	bucketIndex := int(detectedAt.Sub(since) / bucketDuration)
	if bucketIndex < 0 {
		bucketIndex = 0
	} else if bucketIndex >= buckets {
		bucketIndex = buckets - 1
	}
	if note.Confidence > acc.bucketMaxConfidences[bucketIndex] {
		acc.bucketMaxConfidences[bucketIndex] = note.Confidence
	}
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

func parseNoteDetectedAt(note *datastore.Note) (time.Time, bool) {
	detectedAt, err := time.ParseInLocation(time.DateTime, note.Date+" "+note.Time, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return detectedAt, true
}

func recentSpeciesKey(note *datastore.Note) string {
	if note.ScientificName != "" {
		return note.ScientificName
	}
	return note.CommonName
}

func recentSpeciesDisplayName(acc *recentSpeciesAccumulator) string {
	if acc.commonName != "" {
		return acc.commonName
	}
	return acc.scientificName
}

func clampRecentInt(value, minVal, maxVal int) int {
	if value < minVal {
		return minVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
