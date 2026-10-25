package repository

// TimeRange represents a Unix timestamp range for index-friendly queries.
type TimeRange struct {
	Start int64
	End   int64
}

// ExpectedSpecies represents a species expected today based on historical data.
type ExpectedSpecies struct {
	ScientificName string
	YearsSeen      int
	LastSeenDate   string // YYYY-MM-DD
}

// PhantomSpecies represents a species with frequent but low-confidence detections.
type PhantomSpecies struct {
	ScientificName string
	DetectionCount int64
	AvgConfidence  float64
	MaxConfidence  float64
}

// DawnChorusRawEntry represents one species' earliest detection on a single day.
// Time-of-day averaging is done in Go for correct DST handling.
type DawnChorusRawEntry struct {
	ScientificName string
	Date           string // YYYY-MM-DD
	EarliestAt     int64  // Unix timestamp of earliest detection that day
}

// NewArrival represents a species detected for the first time recently.
type NewArrival struct {
	ScientificName string
	FirstDetected  int64 // Unix timestamp
	DetectionCount int64
}

// GoneQuietSpecies represents a previously regular species that has gone silent.
type GoneQuietSpecies struct {
	ScientificName  string
	LastDetected    int64 // Unix timestamp
	TotalDetections int64
}

// DashboardKPIs holds headline metrics for the dashboard.
type DashboardKPIs struct {
	LifetimeSpecies int64
	TodayDetections int64
	BestDayDate     string // YYYY-MM-DD
	BestDayCount    int64
	RecentDates     []string // YYYY-MM-DD, descending, for streak calculation in caller
}

// ModelContributionStats summarizes how each AI model contributed to the
// detections in a half-open [start, end) detected_at range.
//
// Contribution rows are written best-effort, are absent for detections migrated
// from the legacy database, and may be missing when a model could not be
// resolved at save time, so every count derived from them is a lower bound.
// Rows without recorded confidence stats are counted but excluded from
// MinConfidence and MeanConfidence.
type ModelContributionStats struct {
	// TotalDetections is the number of detections in the range.
	TotalDetections int64
	// DetectionsWithContributions is the number of detections in the range with
	// at least one contribution row.
	DetectionsWithContributions int64
	// TotalHits is the sum of Models[i].Hits.
	TotalHits int64
	// Models holds one entry per contributing model, ordered by Detections
	// descending then ModelID ascending. Never nil.
	Models []ModelContributionStat
	// Agreements holds one entry per model pair that shares detections. Never nil.
	Agreements []ModelAgreement
}

// ModelContributionStat is one model's row of ModelContributionStats.
type ModelContributionStat struct {
	// ModelID is the ai_models id.
	ModelID uint
	// Name, Version, Variant and ModelType identify the model.
	Name      string
	Version   string
	Variant   string
	ModelType string
	// Detections is the number of detections this model contributed to.
	Detections int64
	// Hits is the sum of hit_count over those detections.
	Hits int64
	// HitShare is Hits divided by the range's total hits, 0 when that is 0.
	HitShare float64
	// SoleDetections counts detections where this model is the only contributor.
	SoleDetections int64
	// DetectionsWithStats counts contribution rows with recorded min and sum.
	DetectionsWithStats int64
	// MaxConfidence is the highest confidence over all rows.
	MaxConfidence float64
	// MinConfidence is the lowest confidence over rows with stats; nil when none.
	MinConfidence *float64
	// MeanConfidence is sum(confidence_sum) / sum(hit_count) over rows with
	// stats, clamped to [MinConfidence, MaxConfidence]; nil when none.
	MeanConfidence *float64
	// Correct, FalsePositive and Unreviewed partition Detections by review outcome.
	Correct       int64
	FalsePositive int64
	Unreviewed    int64
}

// ModelAgreement counts the detections two models both contributed to.
// ModelAID is always smaller than ModelBID.
type ModelAgreement struct {
	ModelAID         uint
	ModelBID         uint
	SharedDetections int64
}
