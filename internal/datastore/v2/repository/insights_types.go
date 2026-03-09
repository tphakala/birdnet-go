package repository

// TimeRange represents a Unix timestamp range for index-friendly queries.
type TimeRange struct {
	Start int64
	End   int64
}

// ExpectedSpecies represents a species expected today based on historical data.
type ExpectedSpecies struct {
	LabelID        uint
	ScientificName string
	YearsSeen      int
	LastSeenDate   string // YYYY-MM-DD
}

// PhantomSpecies represents a species with frequent but low-confidence detections.
type PhantomSpecies struct {
	LabelID        uint
	ScientificName string
	DetectionCount int64
	AvgConfidence  float64
	MaxConfidence  float64
}

// DawnChorusRawEntry represents one species' earliest detection on a single day.
// Time-of-day averaging is done in Go for correct DST handling.
type DawnChorusRawEntry struct {
	LabelID        uint
	ScientificName string
	Date           string // YYYY-MM-DD
	EarliestAt     int64  // Unix timestamp of earliest detection that day
}

// NewArrival represents a species detected for the first time recently.
type NewArrival struct {
	LabelID        uint
	ScientificName string
	FirstDetected  int64 // Unix timestamp
	DetectionCount int64
}

// GoneQuietSpecies represents a previously regular species that has gone silent.
type GoneQuietSpecies struct {
	LabelID         uint
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
