package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// mockInsightsRepo is a simple mock for handler tests.
type mockInsightsRepo struct {
	phantomSpecies  []repository.PhantomSpecies
	expectedSpecies []repository.ExpectedSpecies
	dawnChorusRaw   []repository.DawnChorusRawEntry
	newArrivals     []repository.NewArrival
	goneQuiet       []repository.GoneQuietSpecies
	dashboardKPIs   *repository.DashboardKPIs
}

func (m *mockInsightsRepo) GetExpectedSpeciesToday(_ context.Context, _ []repository.TimeRange, _ *uint) ([]repository.ExpectedSpecies, error) {
	return m.expectedSpecies, nil
}

func (m *mockInsightsRepo) GetPhantomSpecies(_ context.Context, _ int64, _ int, _ float64, _ *uint) ([]repository.PhantomSpecies, error) {
	return m.phantomSpecies, nil
}

func (m *mockInsightsRepo) GetDawnChorusRaw(_ context.Context, _ int64, _, _ int, _ *uint) ([]repository.DawnChorusRawEntry, error) {
	return m.dawnChorusRaw, nil
}

func (m *mockInsightsRepo) GetNewArrivals(_ context.Context, _ int64, _ *uint) ([]repository.NewArrival, error) {
	return m.newArrivals, nil
}

func (m *mockInsightsRepo) GetGoneQuiet(_ context.Context, _ int64, _ int, _ *uint) ([]repository.GoneQuietSpecies, error) {
	return m.goneQuiet, nil
}

func (m *mockInsightsRepo) GetDashboardKPIs(_ context.Context, _ int64, _ *uint) (*repository.DashboardKPIs, error) {
	return m.dashboardKPIs, nil
}

// setupInsightsTestController creates a minimal controller with a mock insights repo.
func setupInsightsTestController(t *testing.T, mock *mockInsightsRepo) (*echo.Echo, *Controller) {
	t.Helper()
	e := echo.New()
	controller := &Controller{
		Group: e.Group("/api/v2"),
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{
				Labels: []string{
					"Turdus merula_Eurasian Blackbird",
					"Parus major_Great Tit",
				},
			},
		},
		insightsRepo: mock,
		commonNameMap: buildCommonNameMap([]string{
			"Turdus merula_Eurasian Blackbird",
			"Parus major_Great Tit",
		}),
	}
	return e, controller
}

func TestGetPhantomSpecies_Handler(t *testing.T) {
	mockRepo := &mockInsightsRepo{
		phantomSpecies: []repository.PhantomSpecies{
			{LabelID: 1, ScientificName: "Parus major", DetectionCount: 5, AvgConfidence: 0.42, MaxConfidence: 0.58},
		},
	}
	e, controller := setupInsightsTestController(t, mockRepo)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/insights/phantom-species", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.getPhantomSpeciesImpl(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp PhantomSpeciesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Species, 1)
	assert.Equal(t, "Parus major", resp.Species[0].ScientificName)
	assert.Equal(t, "Great Tit", resp.Species[0].CommonName)
	assert.Equal(t, phantomPeriodDays, resp.PeriodDays)
}

func TestGetDashboardKPIs_Handler(t *testing.T) {
	today := time.Now().Format(time.DateOnly)
	yesterday := time.Now().AddDate(0, 0, -1).Format(time.DateOnly)
	mockRepo := &mockInsightsRepo{
		dashboardKPIs: &repository.DashboardKPIs{
			LifetimeSpecies: 87,
			TodayDetections: 42,
			BestDayDate:     "2026-05-15",
			BestDayCount:    234,
			RecentDates:     []string{today, yesterday},
		},
	}
	e, controller := setupInsightsTestController(t, mockRepo)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/dashboard/kpis", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.getDashboardKPIsImpl(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp DashboardKPIsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, int64(87), resp.LifetimeSpecies)
	assert.Equal(t, int64(42), resp.TodayDetections)
	assert.Equal(t, 2, resp.DetectionStreak.Days)
}

func TestGetMigration_Handler(t *testing.T) {
	now := time.Now()
	mockRepo := &mockInsightsRepo{
		newArrivals: []repository.NewArrival{
			{LabelID: 1, ScientificName: "Parus major", FirstDetected: now.AddDate(0, 0, -3).Unix(), DetectionCount: 5},
		},
		goneQuiet: []repository.GoneQuietSpecies{
			{LabelID: 2, ScientificName: "Turdus merula", LastDetected: now.AddDate(0, 0, -20).Unix(), TotalDetections: 15},
		},
	}
	e, controller := setupInsightsTestController(t, mockRepo)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/insights/migration", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.getMigrationImpl(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp MigrationResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.NewArrivals, 1)
	require.Len(t, resp.GoneQuiet, 1)
	assert.Equal(t, "Great Tit", resp.NewArrivals[0].CommonName)
	assert.Equal(t, "Eurasian Blackbird", resp.GoneQuiet[0].CommonName)
}

func TestCalculateStreak(t *testing.T) {
	tests := []struct {
		name      string
		dates     []string
		today     string
		wantDays  int
		wantStart string
	}{
		{
			name:      "consecutive 3 days",
			dates:     []string{"2026-03-09", "2026-03-08", "2026-03-07"},
			today:     "2026-03-09",
			wantDays:  3,
			wantStart: "2026-03-07",
		},
		{
			name:      "gap breaks streak",
			dates:     []string{"2026-03-09", "2026-03-07"},
			today:     "2026-03-09",
			wantDays:  1,
			wantStart: "2026-03-09",
		},
		{
			name:      "no detections today",
			dates:     []string{"2026-03-08", "2026-03-07"},
			today:     "2026-03-09",
			wantDays:  0,
			wantStart: "",
		},
		{
			name:      "empty dates",
			dates:     []string{},
			today:     "2026-03-09",
			wantDays:  0,
			wantStart: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days, start := calculateStreak(tt.dates, tt.today)
			assert.Equal(t, tt.wantDays, days)
			assert.Equal(t, tt.wantStart, start)
		})
	}
}

func TestBuildYearRanges(t *testing.T) {
	tests := []struct {
		name      string
		now       time.Time
		window    int
		checkFunc func(t *testing.T, ranges []repository.TimeRange)
	}{
		{
			name:   "mid-year date produces one range per year",
			now:    time.Date(2026, 3, 9, 12, 0, 0, 0, time.Local),
			window: 3,
			checkFunc: func(t *testing.T, ranges []repository.TimeRange) {
				t.Helper()
				require.NotEmpty(t, ranges)
				for _, r := range ranges {
					startTime := time.Unix(r.Start, 0)
					assert.Less(t, startTime.Year(), 2026, "range should be in a previous year")
					assert.Less(t, r.Start, r.End, "start should be before end")
				}
			},
		},
		{
			name:   "early January wraps around year boundary",
			now:    time.Date(2026, 1, 2, 12, 0, 0, 0, time.Local),
			window: 3,
			checkFunc: func(t *testing.T, ranges []repository.TimeRange) {
				t.Helper()
				require.NotEmpty(t, ranges)
				for _, r := range ranges {
					assert.Less(t, r.Start, r.End, "start should be before end")
				}
			},
		},
		{
			name:   "late December wraps around year boundary",
			now:    time.Date(2026, 12, 30, 12, 0, 0, 0, time.Local),
			window: 3,
			checkFunc: func(t *testing.T, ranges []repository.TimeRange) {
				t.Helper()
				require.NotEmpty(t, ranges)
				for _, r := range ranges {
					assert.Less(t, r.Start, r.End, "start should be before end")
				}
			},
		},
		{
			name:   "leap year Feb 29 handled correctly",
			now:    time.Date(2024, 2, 29, 12, 0, 0, 0, time.Local),
			window: 3,
			checkFunc: func(t *testing.T, ranges []repository.TimeRange) {
				t.Helper()
				require.NotEmpty(t, ranges)
				for _, r := range ranges {
					assert.Less(t, r.Start, r.End, "start should be before end")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ranges := buildYearRanges(tt.now, tt.window)
			tt.checkFunc(t, ranges)
		})
	}
}

func TestBuildCommonNameMap(t *testing.T) {
	labels := []string{
		"Turdus merula_Eurasian Blackbird",
		"Parus major_Great Tit",
		"Invalid Label Without Separator",
		"_EmptyScientificName",
	}

	m := buildCommonNameMap(labels)
	assert.Equal(t, "Eurasian Blackbird", m["Turdus merula"])
	assert.Equal(t, "Great Tit", m["Parus major"])
	assert.Len(t, m, 2) // invalid entries excluded

	// Test resolveCommonName fallback
	assert.Equal(t, "Eurasian Blackbird", resolveCommonName(m, "Turdus merula"))
	assert.Equal(t, "Unknown species", resolveCommonName(m, "Unknown species"))
}

func TestSecondsToTimeString(t *testing.T) {
	assert.Equal(t, "05:30", secondsToTimeString(5*3600+30*60))
	assert.Equal(t, "00:00", secondsToTimeString(0))
	assert.Equal(t, "23:59", secondsToTimeString(23*3600+59*60))
}
