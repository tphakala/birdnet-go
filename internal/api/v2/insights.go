package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Insights constants
const (
	insightsQueryTimeout        = 30 * time.Second
	phantomPeriodDays           = 30
	phantomMinDetections        = 3
	phantomMaxAvgConfidence     = 0.6
	dawnChorusPeriodDays        = 30
	dawnChorusStartHour         = 4
	dawnChorusEndHour           = 10
	dawnChorusMinDaysObserved   = 3
	migrationRecentDays         = 14
	migrationMinTotalDetections = 5
	expectedTodayWindowDays     = 3  // +/- days around today's DOY
	expectedTodayMaxYears       = 10 // how many previous years to scan
)

// --- Response types ---

// ExpectedTodayResponse is the response for GET /api/v2/insights/expected-today.
type ExpectedTodayResponse struct {
	Species     []ExpectedSpeciesItem `json:"species"`
	DayOfYear   int                   `json:"day_of_year"`
	YearsOfData int                   `json:"years_of_data"`
}

// ExpectedSpeciesItem is one species in the expected-today response.
type ExpectedSpeciesItem struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name"`
	YearsSeen      int    `json:"years_seen"`
	LastSeenDate   string `json:"last_seen_date"`
	ThumbnailURL   string `json:"thumbnail_url"`
}

// ExpectedTodayRegionalResponse is the response for GET /api/v2/insights/expected-today/regional.
type ExpectedTodayRegionalResponse struct {
	Species   []RegionalSpeciesItem `json:"species"`
	Available bool                  `json:"available"`
}

// RegionalSpeciesItem is one species from eBird regional data.
type RegionalSpeciesItem struct {
	ScientificName  string `json:"scientific_name"`
	CommonName      string `json:"common_name"`
	ObservationDate string `json:"observation_date"`
	LocationName    string `json:"location_name"`
}

// PhantomSpeciesResponse is the response for GET /api/v2/insights/phantom-species.
type PhantomSpeciesResponse struct {
	Species             []PhantomSpeciesItem `json:"species"`
	PeriodDays          int                  `json:"period_days"`
	ConfidenceThreshold float64              `json:"confidence_threshold"`
	MinDetections       int                  `json:"min_detections"`
}

// PhantomSpeciesItem is one species in the phantom-species response.
type PhantomSpeciesItem struct {
	ScientificName string  `json:"scientific_name"`
	CommonName     string  `json:"common_name"`
	DetectionCount int64   `json:"detection_count"`
	AvgConfidence  float64 `json:"avg_confidence"`
	MaxConfidence  float64 `json:"max_confidence"`
	ThumbnailURL   string  `json:"thumbnail_url"`
}

// DawnChorusResponse is the response for GET /api/v2/insights/dawn-chorus.
type DawnChorusResponse struct {
	Species    []DawnChorusItem `json:"species"`
	PeriodDays int              `json:"period_days"`
	StartHour  int              `json:"start_hour"`
	EndHour    int              `json:"end_hour"`
}

// DawnChorusItem is one species in the dawn-chorus response.
type DawnChorusItem struct {
	ScientificName    string `json:"scientific_name"`
	CommonName        string `json:"common_name"`
	AvgFirstDetection string `json:"avg_first_detection"` // HH:MM
	EarliestDetection string `json:"earliest_detection"`  // HH:MM
	DaysObserved      int    `json:"days_observed"`
	ThumbnailURL      string `json:"thumbnail_url"`
}

// MigrationResponse is the response for GET /api/v2/insights/migration.
type MigrationResponse struct {
	NewArrivals        []NewArrivalItem `json:"new_arrivals"`
	GoneQuiet          []GoneQuietItem  `json:"gone_quiet"`
	RecentDays         int              `json:"recent_days"`
	MinTotalDetections int              `json:"min_total_detections"`
}

// NewArrivalItem is one species in the new arrivals list.
type NewArrivalItem struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name"`
	FirstDetected  string `json:"first_detected"` // YYYY-MM-DD
	DetectionCount int64  `json:"detection_count"`
	ThumbnailURL   string `json:"thumbnail_url"`
}

// GoneQuietItem is one species in the gone quiet list.
type GoneQuietItem struct {
	ScientificName  string `json:"scientific_name"`
	CommonName      string `json:"common_name"`
	LastDetected    string `json:"last_detected"` // YYYY-MM-DD
	DaysSince       int    `json:"days_since"`
	TotalDetections int64  `json:"total_detections"`
	ThumbnailURL    string `json:"thumbnail_url"`
}

// DashboardKPIsResponse is the response for GET /api/v2/dashboard/kpis.
type DashboardKPIsResponse struct {
	LifetimeSpecies int64       `json:"lifetime_species"`
	TodayDetections int64       `json:"today_detections"`
	BestDay         BestDayInfo `json:"best_day"`
	DetectionStreak StreakInfo  `json:"detection_streak"`
}

// BestDayInfo contains the best single-day detection count.
type BestDayInfo struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

// StreakInfo contains consecutive detection day streak data.
type StreakInfo struct {
	Days      int    `json:"days"`
	StartDate string `json:"start_date"`
}

// --- Helper functions ---

// buildCommonNameMap creates a map from scientific name to common name
// using the BirdNET labels in Settings (format: "ScientificName_CommonName").
func buildCommonNameMap(labels []string) map[string]string {
	m := make(map[string]string, len(labels))
	for _, label := range labels {
		scientificName, commonName, found := strings.Cut(label, "_")
		if found {
			scientificName = strings.TrimSpace(scientificName)
			commonName = strings.TrimSpace(commonName)
			if scientificName != "" && commonName != "" {
				m[scientificName] = commonName
			}
		}
	}
	return m
}

// resolveCommonName looks up the common name for a scientific name.
// Returns the scientific name itself as fallback.
func resolveCommonName(nameMap map[string]string, scientificName string) string {
	if cn, ok := nameMap[scientificName]; ok {
		return cn
	}
	return scientificName
}

// buildThumbnailURL returns the proxy image URL for a species.
func buildThumbnailURL(scientificName string) string {
	return imageprovider.ProxyImageURL(scientificName)
}

// buildYearRanges computes index-friendly Unix timestamp ranges for the
// +/- windowDays around today's day-of-year in each previous year.
func buildYearRanges(now time.Time, windowDays int) []repository.TimeRange {
	loc := now.Location()
	currentYear := now.Year()
	doy := now.YearDay()

	firstYear := currentYear - expectedTodayMaxYears

	ranges := make([]repository.TimeRange, 0, currentYear-firstYear)
	for year := firstYear; year < currentYear; year++ {
		jan1 := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
		dec31 := time.Date(year, 12, 31, 23, 59, 59, 0, loc)
		daysInYear := dec31.YearDay()

		startDOY := doy - windowDays
		endDOY := doy + windowDays

		if startDOY < 1 && endDOY > daysInYear {
			ranges = append(ranges, repository.TimeRange{
				Start: jan1.Unix(),
				End:   dec31.Unix(),
			})
			continue
		}

		switch {
		case startDOY < 1:
			wrapStart := time.Date(year, 1, 1, 0, 0, 0, 0, loc).AddDate(0, 0, daysInYear+startDOY-1)
			ranges = append(ranges, repository.TimeRange{
				Start: wrapStart.Unix(),
				End:   dec31.Unix(),
			})
			wrapEnd := time.Date(year, 1, 1, 0, 0, 0, 0, loc).AddDate(0, 0, endDOY-1)
			ranges = append(ranges, repository.TimeRange{
				Start: jan1.Unix(),
				End:   time.Date(wrapEnd.Year(), wrapEnd.Month(), wrapEnd.Day(), 23, 59, 59, 0, loc).Unix(),
			})
		case endDOY > daysInYear:
			rangeStart := time.Date(year, 1, 1, 0, 0, 0, 0, loc).AddDate(0, 0, startDOY-1)
			ranges = append(ranges, repository.TimeRange{
				Start: rangeStart.Unix(),
				End:   dec31.Unix(),
			})
			if year+1 < currentYear {
				wrapEndDOY := endDOY - daysInYear
				wrapEnd := time.Date(year+1, 1, 1, 0, 0, 0, 0, loc).AddDate(0, 0, wrapEndDOY-1)
				ranges = append(ranges, repository.TimeRange{
					Start: time.Date(year+1, 1, 1, 0, 0, 0, 0, loc).Unix(),
					End:   time.Date(wrapEnd.Year(), wrapEnd.Month(), wrapEnd.Day(), 23, 59, 59, 0, loc).Unix(),
				})
			}
		default:
			rangeStart := time.Date(year, 1, 1, 0, 0, 0, 0, loc).AddDate(0, 0, startDOY-1)
			rangeEnd := time.Date(year, 1, 1, 0, 0, 0, 0, loc).AddDate(0, 0, endDOY-1)
			ranges = append(ranges, repository.TimeRange{
				Start: rangeStart.Unix(),
				End:   time.Date(rangeEnd.Year(), rangeEnd.Month(), rangeEnd.Day(), 23, 59, 59, 0, loc).Unix(),
			})
		}
	}

	return ranges
}

// calculateStreak counts consecutive days with detections, starting from today
// and working backwards through the sorted (descending) date list.
func calculateStreak(recentDates []string, today string) (days int, startDate string) {
	if len(recentDates) == 0 || recentDates[0] != today {
		return 0, ""
	}

	todayTime, err := time.Parse(time.DateOnly, today)
	if err != nil {
		return 0, ""
	}

	streakDays := 1
	lastMatched := todayTime
	expected := todayTime
	for i := 1; i < len(recentDates); i++ {
		expected = expected.AddDate(0, 0, -1)
		if recentDates[i] != expected.Format(time.DateOnly) {
			break
		}
		lastMatched = expected
		streakDays++
	}

	return streakDays, lastMatched.Format(time.DateOnly)
}

// secondsToTimeString converts seconds since midnight to "HH:MM" format.
func secondsToTimeString(seconds int) string {
	h := seconds / 3600
	m := (seconds % 3600) / 60
	return fmt.Sprintf("%02d:%02d", h, m)
}

// --- Route registration ---

// initInsightsRoutes lazily initializes the insights repository and registers
// insight API endpoints.
func (c *Controller) initInsightsRoutes() {
	if c.V2Manager == nil {
		return
	}
	db := c.V2Manager.DB()
	isMySQL := c.V2Manager.IsMySQL()
	var useV2Prefix bool
	if tp, ok := c.V2Manager.(interface{ TablePrefix() string }); ok {
		useV2Prefix = tp.TablePrefix() != ""
	}
	c.insightsRepo = repository.NewInsightsRepository(db, useV2Prefix, isMySQL)

	// Build common name map once and cache on Controller
	if c.Settings != nil {
		c.commonNameMap = buildCommonNameMap(c.Settings.BirdNET.Labels)
	}

	insightsGroup := c.Group.Group("/insights")
	insightsGroup.GET("/expected-today", c.GetExpectedToday)
	insightsGroup.GET("/expected-today/regional", c.GetExpectedTodayRegional)
	insightsGroup.GET("/phantom-species", c.GetPhantomSpecies)
	insightsGroup.GET("/dawn-chorus", c.GetDawnChorus)
	insightsGroup.GET("/migration", c.GetMigration)

	c.Group.GET("/dashboard/kpis", c.GetDashboardKPIs)
}

// --- Handlers ---

// GetExpectedToday returns species expected today based on historical day-of-year data.
func (c *Controller) GetExpectedToday(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}
	return c.getExpectedTodayImpl(ctx)
}

func (c *Controller) getExpectedTodayImpl(ctx echo.Context) error {
	now := time.Now()
	yearRanges := buildYearRanges(now, expectedTodayWindowDays)
	if len(yearRanges) == 0 {
		return ctx.JSON(http.StatusOK, ExpectedTodayResponse{
			Species:     []ExpectedSpeciesItem{},
			DayOfYear:   now.YearDay(),
			YearsOfData: 0,
		})
	}

	reqCtx, cancel := context.WithTimeout(ctx.Request().Context(), insightsQueryTimeout)
	defer cancel()

	results, err := c.insightsRepo.GetExpectedSpeciesToday(reqCtx, yearRanges, nil)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to query expected species", http.StatusInternalServerError)
	}

	nameMap := c.commonNameMap

	yearSet := make(map[int]struct{})
	for _, tr := range yearRanges {
		yearSet[time.Unix(tr.Start, 0).Year()] = struct{}{}
	}

	species := make([]ExpectedSpeciesItem, 0, len(results))
	for _, r := range results {
		species = append(species, ExpectedSpeciesItem{
			ScientificName: r.ScientificName,
			CommonName:     resolveCommonName(nameMap, r.ScientificName),
			YearsSeen:      r.YearsSeen,
			LastSeenDate:   r.LastSeenDate,
			ThumbnailURL:   buildThumbnailURL(r.ScientificName),
		})
	}

	return ctx.JSON(http.StatusOK, ExpectedTodayResponse{
		Species:     species,
		DayOfYear:   now.YearDay(),
		YearsOfData: len(yearSet),
	})
}

// GetExpectedTodayRegional returns regionally expected species from eBird.
func (c *Controller) GetExpectedTodayRegional(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}
	return c.getExpectedTodayRegionalImpl(ctx)
}

func (c *Controller) getExpectedTodayRegionalImpl(ctx echo.Context) error {
	if c.EBirdClient == nil {
		return ctx.JSON(http.StatusOK, ExpectedTodayRegionalResponse{
			Species:   []RegionalSpeciesItem{},
			Available: false,
		})
	}

	lat := c.Settings.BirdNET.Latitude
	lng := c.Settings.BirdNET.Longitude
	if lat == 0 && lng == 0 {
		return ctx.JSON(http.StatusOK, ExpectedTodayRegionalResponse{
			Species:   []RegionalSpeciesItem{},
			Available: false,
		})
	}

	reqCtx, cancel := context.WithTimeout(ctx.Request().Context(), insightsQueryTimeout)
	defer cancel()

	observations, err := c.EBirdClient.GetRecentObservations(reqCtx, lat, lng, 14)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to query eBird observations", http.StatusInternalServerError)
	}

	// Get local species to deduplicate against (best-effort — if this fails, show all eBird results)
	yearRanges := buildYearRanges(time.Now(), expectedTodayWindowDays)
	localSpecies, localErr := c.insightsRepo.GetExpectedSpeciesToday(reqCtx, yearRanges, nil)
	if localErr != nil {
		c.logAPIRequest(ctx, logger.LogLevelWarn, "Failed to query local species for deduplication",
			logger.Error(localErr))
	}

	localSet := make(map[string]struct{}, len(localSpecies))
	for _, sp := range localSpecies {
		localSet[sp.ScientificName] = struct{}{}
	}

	seen := make(map[string]struct{})
	items := make([]RegionalSpeciesItem, 0)
	for _, obs := range observations {
		if _, isLocal := localSet[obs.ScientificName]; isLocal {
			continue
		}
		if _, already := seen[obs.ScientificName]; already {
			continue
		}
		seen[obs.ScientificName] = struct{}{}
		items = append(items, RegionalSpeciesItem{
			ScientificName:  obs.ScientificName,
			CommonName:      obs.CommonName,
			ObservationDate: obs.ObservationDt,
			LocationName:    obs.LocationName,
		})
	}

	return ctx.JSON(http.StatusOK, ExpectedTodayRegionalResponse{
		Species:   items,
		Available: true,
	})
}

// GetPhantomSpecies returns species with frequent but low-confidence detections.
func (c *Controller) GetPhantomSpecies(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}
	return c.getPhantomSpeciesImpl(ctx)
}

func (c *Controller) getPhantomSpeciesImpl(ctx echo.Context) error {
	since := time.Now().AddDate(0, 0, -phantomPeriodDays).Unix()

	reqCtx, cancel := context.WithTimeout(ctx.Request().Context(), insightsQueryTimeout)
	defer cancel()

	results, err := c.insightsRepo.GetPhantomSpecies(reqCtx, since, phantomMinDetections, phantomMaxAvgConfidence, nil)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to query phantom species", http.StatusInternalServerError)
	}

	nameMap := c.commonNameMap

	species := make([]PhantomSpeciesItem, 0, len(results))
	for _, r := range results {
		species = append(species, PhantomSpeciesItem{
			ScientificName: r.ScientificName,
			CommonName:     resolveCommonName(nameMap, r.ScientificName),
			DetectionCount: r.DetectionCount,
			AvgConfidence:  r.AvgConfidence,
			MaxConfidence:  r.MaxConfidence,
			ThumbnailURL:   buildThumbnailURL(r.ScientificName),
		})
	}

	return ctx.JSON(http.StatusOK, PhantomSpeciesResponse{
		Species:             species,
		PeriodDays:          phantomPeriodDays,
		ConfidenceThreshold: phantomMaxAvgConfidence,
		MinDetections:       phantomMinDetections,
	})
}

// GetDawnChorus returns species ranked by average earliest detection time.
func (c *Controller) GetDawnChorus(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}
	return c.getDawnChorusImpl(ctx)
}

func (c *Controller) getDawnChorusImpl(ctx echo.Context) error {
	since := time.Now().AddDate(0, 0, -dawnChorusPeriodDays).Unix()

	reqCtx, cancel := context.WithTimeout(ctx.Request().Context(), insightsQueryTimeout)
	defer cancel()

	rawEntries, err := c.insightsRepo.GetDawnChorusRaw(reqCtx, since, dawnChorusStartHour, dawnChorusEndHour, nil)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to query dawn chorus", http.StatusInternalServerError)
	}

	// Group by species, compute DST-correct time-of-day averages
	type speciesData struct {
		scientificName  string
		secondsSum      int
		earliestSeconds int
		daysObserved    int
	}
	speciesMap := make(map[uint]*speciesData)
	loc := time.Now().Location()

	for _, entry := range rawEntries {
		sd, ok := speciesMap[entry.LabelID]
		if !ok {
			sd = &speciesData{
				scientificName:  entry.ScientificName,
				earliestSeconds: 24 * 3600, // sentinel max
			}
			speciesMap[entry.LabelID] = sd
		}

		t := time.Unix(entry.EarliestAt, 0).In(loc)
		secondsSinceMidnight := t.Hour()*3600 + t.Minute()*60 + t.Second()

		sd.secondsSum += secondsSinceMidnight
		sd.daysObserved++
		if secondsSinceMidnight < sd.earliestSeconds {
			sd.earliestSeconds = secondsSinceMidnight
		}
	}

	nameMap := c.commonNameMap

	items := make([]DawnChorusItem, 0, len(speciesMap))
	for _, sd := range speciesMap {
		if sd.daysObserved < dawnChorusMinDaysObserved {
			continue
		}
		avgSeconds := sd.secondsSum / sd.daysObserved
		items = append(items, DawnChorusItem{
			ScientificName:    sd.scientificName,
			CommonName:        resolveCommonName(nameMap, sd.scientificName),
			AvgFirstDetection: secondsToTimeString(avgSeconds),
			EarliestDetection: secondsToTimeString(sd.earliestSeconds),
			DaysObserved:      sd.daysObserved,
			ThumbnailURL:      buildThumbnailURL(sd.scientificName),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].AvgFirstDetection < items[j].AvgFirstDetection
	})

	return ctx.JSON(http.StatusOK, DawnChorusResponse{
		Species:    items,
		PeriodDays: dawnChorusPeriodDays,
		StartHour:  dawnChorusStartHour,
		EndHour:    dawnChorusEndHour,
	})
}

// GetMigration returns new arrivals and gone-quiet species.
func (c *Controller) GetMigration(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}
	return c.getMigrationImpl(ctx)
}

func (c *Controller) getMigrationImpl(ctx echo.Context) error {
	now := time.Now()
	recentSince := now.AddDate(0, 0, -migrationRecentDays).Unix()

	reqCtx, cancel := context.WithTimeout(ctx.Request().Context(), insightsQueryTimeout)
	defer cancel()

	arrivals, err := c.insightsRepo.GetNewArrivals(reqCtx, recentSince, nil)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to query new arrivals", http.StatusInternalServerError)
	}

	quiet, err := c.insightsRepo.GetGoneQuiet(reqCtx, recentSince, migrationMinTotalDetections, nil)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to query gone quiet species", http.StatusInternalServerError)
	}

	nameMap := c.commonNameMap

	arrivalItems := make([]NewArrivalItem, 0, len(arrivals))
	for _, a := range arrivals {
		arrivalItems = append(arrivalItems, NewArrivalItem{
			ScientificName: a.ScientificName,
			CommonName:     resolveCommonName(nameMap, a.ScientificName),
			FirstDetected:  time.Unix(a.FirstDetected, 0).In(now.Location()).Format(time.DateOnly),
			DetectionCount: a.DetectionCount,
			ThumbnailURL:   buildThumbnailURL(a.ScientificName),
		})
	}

	quietItems := make([]GoneQuietItem, 0, len(quiet))
	for _, q := range quiet {
		lastDetectedLocal := time.Unix(q.LastDetected, 0).In(now.Location())
		todayDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		lastDate := time.Date(lastDetectedLocal.Year(), lastDetectedLocal.Month(), lastDetectedLocal.Day(), 0, 0, 0, 0, now.Location())
		daysSince := int(todayDate.Sub(lastDate).Hours() / 24)
		quietItems = append(quietItems, GoneQuietItem{
			ScientificName:  q.ScientificName,
			CommonName:      resolveCommonName(nameMap, q.ScientificName),
			LastDetected:    lastDetectedLocal.Format(time.DateOnly),
			DaysSince:       daysSince,
			TotalDetections: q.TotalDetections,
			ThumbnailURL:    buildThumbnailURL(q.ScientificName),
		})
	}

	return ctx.JSON(http.StatusOK, MigrationResponse{
		NewArrivals:        arrivalItems,
		GoneQuiet:          quietItems,
		RecentDays:         migrationRecentDays,
		MinTotalDetections: migrationMinTotalDetections,
	})
}

// GetDashboardKPIs returns headline metrics for the dashboard.
func (c *Controller) GetDashboardKPIs(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}
	return c.getDashboardKPIsImpl(ctx)
}

func (c *Controller) getDashboardKPIsImpl(ctx echo.Context) error {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	reqCtx, cancel := context.WithTimeout(ctx.Request().Context(), insightsQueryTimeout)
	defer cancel()

	kpis, err := c.insightsRepo.GetDashboardKPIs(reqCtx, todayStart.Unix(), nil)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to query dashboard KPIs", http.StatusInternalServerError)
	}

	today := todayStart.Format(time.DateOnly)
	streakDays, streakStart := calculateStreak(kpis.RecentDates, today)

	return ctx.JSON(http.StatusOK, DashboardKPIsResponse{
		LifetimeSpecies: kpis.LifetimeSpecies,
		TodayDetections: kpis.TodayDetections,
		BestDay: BestDayInfo{
			Date:  kpis.BestDayDate,
			Count: kpis.BestDayCount,
		},
		DetectionStreak: StreakInfo{
			Days:      streakDays,
			StartDate: streakStart,
		},
	})
}
