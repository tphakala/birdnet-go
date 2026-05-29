// internal/health/report.go
package health

import (
	"sync"
	"time"
)

// DiagnosticsReport is the full output of a diagnostic run.
type DiagnosticsReport struct {
	ID            string              `json:"id"`
	Status        Status              `json:"status"`
	StartedAt     time.Time           `json:"started_at"`
	CompletedAt   time.Time           `json:"completed_at"`
	DurationMS    float64             `json:"duration_ms"`
	TotalChecks   int                 `json:"total_checks"`
	Results       []Result            `json:"results"`
	Summary       map[Category]Status `json:"summary"`
	CountByStatus map[Status]int      `json:"count_by_status"`
}

// NewReport creates a DiagnosticsReport from a slice of results.
func NewReport(id string, startedAt time.Time, results []Result) *DiagnosticsReport {
	completedAt := time.Now()
	summary := make(map[Category]Status)
	countByStatus := make(map[Status]int)

	catResults := make(map[Category][]Result)
	for _, r := range results {
		catResults[r.Category] = append(catResults[r.Category], r)
		countByStatus[r.Status]++
	}
	for cat, rs := range catResults {
		summary[cat] = WorstStatus(rs)
	}

	return &DiagnosticsReport{
		ID:            id,
		Status:        WorstStatus(results),
		StartedAt:     startedAt,
		CompletedAt:   completedAt,
		DurationMS:    float64(completedAt.Sub(startedAt).Microseconds()) / 1000.0,
		TotalChecks:   len(results),
		Results:       results,
		Summary:       summary,
		CountByStatus: countByStatus,
	}
}

// DefaultReportStoreSize is the number of reports a ReportStore retains when
// NewReportStore is given a non-positive maxSize.
const DefaultReportStoreSize = 10

// ReportStore keeps recent reports in memory.
type ReportStore struct {
	mu      sync.RWMutex
	reports map[string]*DiagnosticsReport
	maxSize int
}

// NewReportStore creates a store that keeps up to maxSize reports.
// If maxSize is less than or equal to zero, it falls back to
// DefaultReportStoreSize. Without this guard a non-positive maxSize would make
// Save evict on every call, so the store would never retain any report.
func NewReportStore(maxSize int) *ReportStore {
	if maxSize <= 0 {
		maxSize = DefaultReportStoreSize
	}
	return &ReportStore{
		reports: make(map[string]*DiagnosticsReport),
		maxSize: maxSize,
	}
}

// Save stores a report. If the store is full, the oldest report is evicted.
// Nil reports are silently ignored.
// Save stores a report. If the store is full, the oldest report is evicted.
// Nil reports are silently ignored.
func (s *ReportStore) Save(report *DiagnosticsReport) {
	if report == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Only evict when inserting a new ID. Updating an existing report in place
	// must not evict another entry, which would otherwise shrink the effective
	// capacity by one on every update of an already-stored report.
	if _, exists := s.reports[report.ID]; !exists && len(s.reports) >= s.maxSize {
		var oldestID string
		var oldestTime time.Time
		for id, r := range s.reports {
			if oldestID == "" || r.StartedAt.Before(oldestTime) {
				oldestID = id
				oldestTime = r.StartedAt
			}
		}
		if oldestID != "" {
			delete(s.reports, oldestID)
		}
	}
	s.reports[report.ID] = report
}

// Get retrieves a report by ID.
func (s *ReportStore) Get(id string) (*DiagnosticsReport, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.reports[id]
	return r, ok
}

// Latest returns the most recent report, or nil if empty.
func (s *ReportStore) Latest() *DiagnosticsReport {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest *DiagnosticsReport
	for _, r := range s.reports {
		if latest == nil || r.StartedAt.After(latest.StartedAt) {
			latest = r
		}
	}
	return latest
}
