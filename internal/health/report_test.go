// internal/health/report_test.go
package health

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewReport_BasicFields(t *testing.T) {
	t.Parallel()
	startedAt := time.Now().Add(-50 * time.Millisecond)
	results := []Result{
		{Name: "check-a", Category: CategorySystem, Status: StatusHealthy},
		{Name: "check-b", Category: CategoryAudio, Status: StatusWarning},
		{Name: "check-c", Category: CategorySystem, Status: StatusCritical},
	}

	report := NewReport("report-1", startedAt, results)

	assert.Equal(t, "report-1", report.ID)
	assert.Equal(t, 3, report.TotalChecks)
	assert.Equal(t, results, report.Results)
	assert.Equal(t, startedAt, report.StartedAt)
	assert.False(t, report.CompletedAt.IsZero())
	assert.GreaterOrEqual(t, report.DurationMS, 0.0)
}

func TestNewReport_OverallStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		statuses []Status
		want     Status
	}{
		{"all healthy", []Status{StatusHealthy, StatusHealthy}, StatusHealthy},
		{"warning present", []Status{StatusHealthy, StatusWarning}, StatusWarning},
		{"critical wins", []Status{StatusWarning, StatusCritical}, StatusCritical},
		{"empty results", []Status{}, StatusUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			results := make([]Result, len(tt.statuses))
			for i, s := range tt.statuses {
				results[i] = Result{Name: "c", Category: CategorySystem, Status: s}
			}
			report := NewReport("id", time.Now(), results)
			assert.Equal(t, tt.want, report.Status)
		})
	}
}

func TestNewReport_SummaryPerCategory(t *testing.T) {
	t.Parallel()
	results := []Result{
		{Name: "s1", Category: CategorySystem, Status: StatusHealthy},
		{Name: "s2", Category: CategorySystem, Status: StatusWarning},
		{Name: "a1", Category: CategoryAudio, Status: StatusCritical},
		{Name: "a2", Category: CategoryAudio, Status: StatusHealthy},
		{Name: "d1", Category: CategoryDatabase, Status: StatusHealthy},
	}

	report := NewReport("id", time.Now(), results)

	// System: worst of healthy + warning = warning
	assert.Equal(t, StatusWarning, report.Summary[CategorySystem])
	// Audio: worst of critical + healthy = critical
	assert.Equal(t, StatusCritical, report.Summary[CategoryAudio])
	// Database: only healthy
	assert.Equal(t, StatusHealthy, report.Summary[CategoryDatabase])
}

func TestNewReport_CountByStatus(t *testing.T) {
	t.Parallel()
	results := []Result{
		{Status: StatusHealthy},
		{Status: StatusHealthy},
		{Status: StatusWarning},
		{Status: StatusCritical},
	}

	report := NewReport("id", time.Now(), results)

	assert.Equal(t, 2, report.CountByStatus[StatusHealthy])
	assert.Equal(t, 1, report.CountByStatus[StatusWarning])
	assert.Equal(t, 1, report.CountByStatus[StatusCritical])
	assert.Equal(t, 0, report.CountByStatus[StatusUnknown])
}

func TestReportStore_SaveAndGet(t *testing.T) {
	t.Parallel()
	store := NewReportStore(10)

	report := NewReport("abc", time.Now(), nil)
	store.Save(report)

	got, ok := store.Get("abc")
	require.True(t, ok)
	assert.Equal(t, "abc", got.ID)
}

func TestReportStore_GetMissing(t *testing.T) {
	t.Parallel()
	store := NewReportStore(10)

	got, ok := store.Get("missing")
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestReportStore_Latest(t *testing.T) {
	t.Parallel()
	store := NewReportStore(10)

	base := time.Now()
	older := NewReport("older", base, nil)
	newer := NewReport("newer", base.Add(time.Second), nil)

	store.Save(older)
	store.Save(newer)

	latest := store.Latest()
	require.NotNil(t, latest)
	assert.Equal(t, "newer", latest.ID)
}

func TestReportStore_Latest_Empty(t *testing.T) {
	t.Parallel()
	store := NewReportStore(10)
	assert.Nil(t, store.Latest())
}

func TestReportStore_Eviction(t *testing.T) {
	t.Parallel()
	const maxSize = 3
	store := NewReportStore(maxSize)

	base := time.Now()
	// Fill the store.
	for i := range maxSize {
		id := string(rune('a' + i))
		r := NewReport(id, base.Add(time.Duration(i)*time.Second), nil)
		store.Save(r)
	}

	// Verify all 3 are present.
	_, ok := store.Get("a")
	require.True(t, ok)

	// Add a 4th report; the oldest ("a") should be evicted.
	newest := NewReport("d", base.Add(time.Duration(maxSize)*time.Second), nil)
	store.Save(newest)

	_, ok = store.Get("a")
	assert.False(t, ok, "oldest report should have been evicted")

	_, ok = store.Get("d")
	assert.True(t, ok, "new report should be present")
}

func TestNewReportStore_InvalidMaxSizeUsesDefault(t *testing.T) {
	t.Parallel()
	// A non-positive maxSize must fall back to the default rather than
	// evicting on every Save (which would mean the store never retains
	// anything). Both zero and negative inputs behave like the default size.
	for _, maxSize := range []int{0, -1} {
		store := NewReportStore(maxSize)
		base := time.Now()

		// Saving up to the default size must retain every report.
		for i := range DefaultReportStoreSize {
			id := string(rune('a' + i))
			store.Save(NewReport(id, base.Add(time.Duration(i)*time.Second), nil))
		}

		for i := range DefaultReportStoreSize {
			id := string(rune('a' + i))
			_, ok := store.Get(id)
			assert.Truef(t, ok, "report %q should be retained with maxSize=%d", id, maxSize)
		}
	}
}

func TestReportStore_UpdateWhileFullDoesNotEvict(t *testing.T) {
	t.Parallel()
	// Updating a report whose ID is already stored must not evict another
	// entry just because the store is at capacity. Otherwise an in-place
	// update would silently shrink the effective capacity by one.
	const size = 3
	store := NewReportStore(size)
	base := time.Now()

	for i := range size {
		id := string(rune('a' + i))
		store.Save(NewReport(id, base.Add(time.Duration(i)*time.Second), nil))
	}

	// Re-save an existing ID while full.
	store.Save(NewReport("a", base.Add(time.Duration(size)*time.Second), nil))

	for i := range size {
		id := string(rune('a' + i))
		_, ok := store.Get(id)
		assert.Truef(t, ok, "report %q must be retained after updating an existing report while full", id)
	}
}
