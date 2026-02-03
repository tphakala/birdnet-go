// season.go - Season calculation and validation functions

package species

import (
	"maps"
	"slices"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// initializeDefaultSeasons sets up the default Northern Hemisphere seasons
func (t *SpeciesTracker) initializeDefaultSeasons() {
	t.seasons["spring"] = seasonDates{month: monthMarch, day: daySpringEquinox}     // March 20
	t.seasons["summer"] = seasonDates{month: monthJune, day: daySummerSolstice}     // June 21
	t.seasons["fall"] = seasonDates{month: monthSeptember, day: dayFallEquinox}     // September 22
	t.seasons["winter"] = seasonDates{month: monthDecember, day: dayWinterSolstice} // December 21
}

// validateSeasonOrder checks if all seasons in the given order exist in the tracker's seasons map.
// Returns true if all seasons are present, false otherwise.
func (t *SpeciesTracker) validateSeasonOrder(seasonOrder []string, seasonType string) bool {
	for _, required := range seasonOrder {
		if _, exists := t.seasons[required]; !exists {
			getLog().Warn("Missing "+seasonType+" season in configuration",
				logger.String("missing_season", required),
				logger.Any("available_seasons", t.seasons))
			return false
		}
	}
	return true
}

// initializeSeasonOrder builds the cached season order based on configured seasons
// This is called once at initialization to avoid rebuilding on every computeCurrentSeason() call
func (t *SpeciesTracker) initializeSeasonOrder() {
	// Check if we have traditional seasons (Northern/Southern Hemisphere)
	if _, hasWinter := t.seasons["winter"]; hasWinter {
		seasonOrder := []string{"winter", "spring", "summer", "fall"}
		if t.validateSeasonOrder(seasonOrder, "traditional") {
			t.cachedSeasonOrder = seasonOrder
			return
		}
	} else if _, hasWet1 := t.seasons["wet1"]; hasWet1 {
		// Equatorial seasons: dry2, wet1, dry1, wet2 (in chronological order within a year)
		seasonOrder := []string{"dry2", "wet1", "dry1", "wet2"}
		if t.validateSeasonOrder(seasonOrder, "equatorial") {
			t.cachedSeasonOrder = seasonOrder
			return
		}
	}

	// Fall back to using all available seasons if non-standard configuration
	t.cachedSeasonOrder = slices.Collect(maps.Keys(t.seasons))

	getLog().Debug("Initialized season order cache",
		logger.Any("order", t.cachedSeasonOrder),
		logger.Int("count", len(t.cachedSeasonOrder)))
}

// validateSeasonDate validates that a month/day combination is valid
func validateSeasonDate(month, day int) error {
	if month < 1 || month > 12 {
		return errors.Newf("invalid month: %d (must be 1-12)", month).
			Component("species-tracking").
			Category(errors.CategoryValidation).
			Build()
	}

	maxDays := daysInMonth[month-1]
	// Special case for February - accept 29 for leap years
	if month == monthFebruary {
		maxDays = 29 // Accept Feb 29 since seasons are year-agnostic
	}

	if day < 1 || day > maxDays {
		return errors.Newf("invalid day %d for month %d (must be 1-%d)", day, month, maxDays).
			Component("species-tracking").
			Category(errors.CategoryValidation).
			Build()
	}

	return nil
}

// isInEarlyWinterMonths checks if the current month is in the early winter period
// (the 2 months after winter starts). Returns false if winter season is not configured.
func (t *SpeciesTracker) isInEarlyWinterMonths(currentMonth time.Month) bool {
	winterSeason, hasWinter := t.seasons["winter"]
	if !hasWinter {
		return false
	}
	// Early winter months are the 2 months after winter starts, in the next year
	// Northern: Winter starts Dec, so Jan/Feb are early winter
	// Southern: Winter starts Jun, so Jul/Aug are early winter
	winterStartMonth := time.Month(winterSeason.month)
	earlyWinterMonth1 := (winterStartMonth % monthsPerYear) + 1       // Month after winter start
	earlyWinterMonth2 := ((winterStartMonth + 1) % monthsPerYear) + 1 // 2 months after winter start
	return currentMonth == earlyWinterMonth1 || currentMonth == earlyWinterMonth2
}

// shouldAdjustFallSeasonYear determines if fall season year should be adjusted to previous year.
// Returns true if we're in early winter months (when fall has ended).
func (t *SpeciesTracker) shouldAdjustFallSeasonYear(now time.Time, seasonMonth time.Month) bool {
	fallSeason, hasFall := t.seasons["fall"]
	if !hasFall || int(seasonMonth) != fallSeason.month {
		return false
	}
	// For fall season, only adjust to previous year if we're in early winter months
	return t.isInEarlyWinterMonths(now.Month())
}

// shouldAdjustYearForSeason determines if a season's year should be adjusted backward
// based on the current time and the use case (detection vs range calculation).
//
// For year-crossing seasons (Oct-Dec), adjusts to previous year when in early months (Jan-May).
// For range calculations, also handles fall season (Sep) when queried during winter months.
//
// Parameters:
//   - now: The current time to base the adjustment on
//   - seasonMonth: The month when the season starts
//   - isRangeCalculation: true when calculating date ranges (e.g., getSeasonDateRange),
//     false when detecting current season (e.g., computeCurrentSeason)
func (t *SpeciesTracker) shouldAdjustYearForSeason(now time.Time, seasonMonth time.Month, isRangeCalculation bool) bool {
	// Core logic: Year-crossing seasons (Oct-Dec) in early months of the year
	// These seasons span year boundaries (e.g., Northern winter: Dec-Feb, Southern summer: Dec-Feb)
	if seasonMonth >= time.October && now.Month() < yearCrossingCutoffMonth {
		return true
	}

	// Additional logic for range calculations only:
	// Handle fall season - return current year's fall unless we're in early winter months.
	if isRangeCalculation && t.shouldAdjustFallSeasonYear(now, seasonMonth) {
		return true
	}

	// For other seasons (spring, summer), don't adjust to previous year
	return false
}

// getCurrentSeason determines which season we're currently in with intelligent caching
func (t *SpeciesTracker) getCurrentSeason(currentTime time.Time) string {
	// Check cache first - if valid entry exists and the input time is reasonably close to cached time
	if t.cachedSeason != "" &&
		t.isSameSeasonPeriod(currentTime, t.seasonCacheForTime) &&
		time.Since(t.seasonCacheTime) < t.seasonCacheTTL {
		// Cache hit - return cached season directly
		return t.cachedSeason
	}

	// Cache miss or expired - compute fresh season
	season := t.computeCurrentSeason(currentTime)

	// Cache the computed result for future requests
	t.cachedSeason = season
	t.seasonCacheTime = time.Now()     // Cache time is when we computed it
	t.seasonCacheForTime = currentTime // Input time for which we computed

	return season
}

// isSameSeasonPeriod checks if two times are likely in the same season period
// This helps avoid cache misses for times that are very close together
func (t *SpeciesTracker) isSameSeasonPeriod(time1, time2 time.Time) bool {
	// If times are in different years, they could be in different seasons
	if time1.Year() != time2.Year() {
		return false
	}

	// If times are within the same day, they're definitely in the same season
	if time1.YearDay() == time2.YearDay() {
		return true
	}

	// Check if any season boundary falls between the two times
	// If so, they might be in different seasons even if close together
	day1 := time1.YearDay()
	day2 := time2.YearDay()
	minDay := min(day1, day2)
	maxDay := max(day1, day2)

	for _, season := range t.seasons {
		boundaryDate := time.Date(time1.Year(), time.Month(season.month), season.day, 0, 0, 0, 0, time1.Location())
		boundaryDay := boundaryDate.YearDay()

		// If a season boundary falls between the two times (inclusive of boundary day),
		// they could be in different seasons
		if boundaryDay >= minDay && boundaryDay <= maxDay {
			return false
		}
	}

	// If no boundaries between times and within buffer, consider same period
	// (seasons typically last ~90 days, so seasonBufferDays is a safe buffer)
	timeDiff := time1.Sub(time2)
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}
	return timeDiff <= seasonBufferDuration
}

// calculateSeasonStartDate calculates the start date for a season, adjusting for year boundaries.
func (t *SpeciesTracker) calculateSeasonStartDate(seasonName string, seasonStart seasonDates, currentTime time.Time) time.Time {
	seasonDate := time.Date(currentTime.Year(), time.Month(seasonStart.month), seasonStart.day, 0, 0, 0, 0, currentTime.Location())

	// Handle seasons that might cross year boundaries
	if t.shouldAdjustYearForSeason(currentTime, time.Month(seasonStart.month), false) {
		seasonDate = time.Date(currentTime.Year()-1, time.Month(seasonStart.month), seasonStart.day, 0, 0, 0, 0, currentTime.Location())
		getLog().Debug("Adjusting season to previous year",
			logger.String("season", seasonName),
			logger.String("adjusted_date", seasonDate.Format(time.DateOnly)))
	}
	return seasonDate
}

// computeCurrentSeason performs the actual season calculation
func (t *SpeciesTracker) computeCurrentSeason(currentTime time.Time) string {
	getLog().Debug("Computing current season",
		logger.String("input_time", currentTime.Format(time.DateTime)),
		logger.Int("current_month", int(currentTime.Month())),
		logger.Int("current_day", currentTime.Day()),
		logger.Int("current_year", currentTime.Year()))

	// Use cached season order for efficiency (built once at initialization)
	seasonOrder := t.cachedSeasonOrder
	if len(seasonOrder) == 0 {
		getLog().Warn("Season order cache was empty, rebuilding", logger.Any("seasons", t.seasons))
		t.initializeSeasonOrder()
		seasonOrder = t.cachedSeasonOrder
	}

	// Find the most recent season start date
	var currentSeason string
	var latestDate time.Time

	for _, seasonName := range seasonOrder {
		seasonStart, exists := t.seasons[seasonName]
		if !exists {
			continue
		}

		seasonDate := t.calculateSeasonStartDate(seasonName, seasonStart, currentTime)

		// Check if current date is on or after this season's start and it's the most recent
		isOnOrAfter := !currentTime.Before(seasonDate)
		isMoreRecent := currentSeason == "" || seasonDate.After(latestDate)
		if isOnOrAfter && isMoreRecent {
			currentSeason = seasonName
			latestDate = seasonDate
		}
	}

	// Default to winter if we couldn't determine the season
	if currentSeason == "" {
		currentSeason = "winter"
		getLog().Debug("Defaulting to winter season - no match found")
	}

	getLog().Debug("Computed season result",
		logger.String("season", currentSeason),
		logger.String("season_start_date", latestDate.Format(time.DateOnly)))

	return currentSeason
}

// checkAndResetPeriods checks if we need to reset yearly or seasonal tracking
func (t *SpeciesTracker) checkAndResetPeriods(currentTime time.Time) {
	// Check for yearly reset
	if t.yearlyEnabled && t.shouldResetYear(currentTime) {
		oldYear := t.currentYear
		t.speciesThisYear = make(map[string]time.Time)
		t.currentYear = t.getTrackingYear(currentTime) // Use tracking year, not calendar year
		// Clear status cache when year resets to ensure fresh calculations
		t.statusCache = make(map[string]cachedSpeciesStatus)
		getLog().Debug("Reset yearly tracking",
			logger.Int("old_year", oldYear),
			logger.Int("new_year", t.currentYear),
			logger.String("check_time", currentTime.Format(time.DateTime)))
	}

	// Check for seasonal reset
	if t.seasonalEnabled {
		newSeason := t.getCurrentSeason(currentTime)
		if newSeason != t.currentSeason {
			t.currentSeason = newSeason
			// Initialize season map if it doesn't exist
			if t.speciesBySeason[newSeason] == nil {
				t.speciesBySeason[newSeason] = make(map[string]time.Time)
			}
		}
	}
}

// shouldResetYear determines if we should reset yearly tracking
func (t *SpeciesTracker) shouldResetYear(currentTime time.Time) bool {
	// If we've never reset before (currentYear is 0), we need to reset
	if t.currentYear == 0 {
		return true
	}

	currentCalendarYear := currentTime.Year()

	// Handle standard January 1st resets
	if t.resetMonth == 1 && t.resetDay == 1 {
		// Standard calendar year - reset if we're in a later year
		return currentCalendarYear > t.currentYear
	}

	// Handle custom reset dates
	resetDate := time.Date(currentCalendarYear, time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, currentTime.Location())

	// If we're in a later calendar year, definitely reset
	if currentCalendarYear > t.currentYear {
		return true
	}

	// If we're in an earlier calendar year (shouldn't happen normally), don't reset
	if currentCalendarYear < t.currentYear {
		return false
	}

	// Same calendar year - reset only on the exact reset day
	// This handles the case where we reach the reset day but not necessarily at midnight
	if currentCalendarYear == t.currentYear &&
		currentTime.Month() == resetDate.Month() &&
		currentTime.Day() == resetDate.Day() {
		return true
	}

	return false
}

// getTrackingYear determines which tracking year a given time falls into
// This handles custom reset dates (e.g., tracking years starting July 1st)
func (t *SpeciesTracker) getTrackingYear(now time.Time) int {
	currentYear := now.Year()

	// If current time is before this year's reset date, we're still in the previous tracking year
	currentYearResetDate := time.Date(currentYear, time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, now.Location())

	if now.Before(currentYearResetDate) {
		// We haven't reached this year's reset date yet, so we're still in the previous tracking year
		return currentYear - 1
	}
	// We've passed this year's reset date, so we're in the current tracking year
	return currentYear
}

// getYearDateRange calculates the start and end dates for yearly tracking
func (t *SpeciesTracker) getYearDateRange(now time.Time) (startDate, endDate string) {
	// Use t.currentYear if explicitly set for testing, otherwise use the provided time's year
	currentYear := now.Year()
	useOverride := t.currentYear != 0 && t.currentYear != time.Now().Year()

	if useOverride {
		// Only use t.currentYear if it was explicitly set for testing (different from real current year)
		currentYear = t.currentYear
	}

	// Determine the tracking year based on reset date
	var trackingYear int

	if useOverride {
		// When year is overridden for testing, use it directly as the tracking year
		trackingYear = currentYear
	} else {
		// Normal operation: determine based on reset date
		// If current time is before this year's reset date, we're still in the previous tracking year
		currentYearResetDate := time.Date(currentYear, time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, now.Location())

		if now.Before(currentYearResetDate) {
			// We haven't reached this year's reset date yet, so we're still in the previous tracking year
			trackingYear = currentYear - 1
		} else {
			// We've passed this year's reset date, so we're in the current tracking year
			trackingYear = currentYear
		}
	}

	// Calculate the tracking period: from reset date of trackingYear to day before reset date of next year
	yearStart := time.Date(trackingYear, time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, now.Location())
	nextYearReset := time.Date(trackingYear+1, time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, now.Location())
	yearEnd := nextYearReset.AddDate(0, 0, -1)

	startDate = yearStart.Format(time.DateOnly)
	endDate = yearEnd.Format(time.DateOnly)

	return startDate, endDate
}

// getSeasonDateRange calculates the start and end dates for a specific season
func (t *SpeciesTracker) getSeasonDateRange(seasonName string, now time.Time) (startDate, endDate string) {
	season, exists := t.seasons[seasonName]
	if !exists || season.month <= 0 || season.day <= 0 {
		// Return empty strings for unknown or invalid season
		return "", ""
	}

	// Use test year override if set, otherwise use now's year
	currentYear := now.Year()
	if t.currentYear != 0 && t.currentYear != time.Now().Year() {
		currentYear = t.currentYear
	}

	// Calculate season start date
	seasonStart := time.Date(currentYear, time.Month(season.month), season.day, 0, 0, 0, 0, now.Location())

	// Handle seasons that might need adjustment to previous year
	if t.shouldAdjustYearForSeason(now, time.Month(season.month), true) {
		seasonStart = time.Date(currentYear-1, time.Month(season.month), season.day, 0, 0, 0, 0, now.Location())
	}

	// Calculate season end date - seasons last monthsPerSeason months
	// Add monthsPerSeason months, then subtract 1 day to get the last day of the final month
	seasonEnd := seasonStart.AddDate(0, monthsPerSeason, 0).AddDate(0, 0, -1)

	startDate = seasonStart.Format(time.DateOnly)
	endDate = seasonEnd.Format(time.DateOnly)

	return startDate, endDate
}

// isWithinCurrentYear checks if a detection time falls within the current tracking year
func (t *SpeciesTracker) isWithinCurrentYear(detectionTime time.Time) bool {
	// Handle uninitialized currentYear (0) - use detection time's year
	if t.currentYear == 0 {
		// When currentYear is not set, any detection is considered within the current year
		return true
	}

	// Standard calendar year case (reset on Jan 1)
	if t.resetMonth == 1 && t.resetDay == 1 {
		return detectionTime.Year() == t.currentYear
	}

	// Custom tracking year case - use getTrackingYear for consistent logic
	// For example, with July 1 reset and currentYear=2024:
	// - June 30, 2024 → getTrackingYear returns 2023 → FALSE (previous tracking year)
	// - July 1, 2024 → getTrackingYear returns 2024 → TRUE (current tracking year)
	return t.getTrackingYear(detectionTime) == t.currentYear
}
