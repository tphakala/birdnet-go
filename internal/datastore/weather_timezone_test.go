// weather_timezone_test.go: Tests for timezone-aware weather queries
package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupWeatherTestDB creates a test database with HourlyWeather table
func setupWeatherTestDB(t *testing.T) *DataStore {
	t.Helper()

	ds := setupTestDB(t)

	// Create the HourlyWeather table schema
	err := ds.DB.AutoMigrate(&HourlyWeather{})
	require.NoError(t, err, "Failed to create HourlyWeather table")

	// Create the DailyEvents table schema
	err = ds.DB.AutoMigrate(&DailyEvents{})
	require.NoError(t, err, "Failed to create DailyEvents table")

	return ds
}

// TestGetHourlyWeather_TimezoneHandling tests that weather queries work correctly across timezones
func TestGetHourlyWeather_TimezoneHandling(t *testing.T) {
	t.Run("UTC weather matches local date - same timezone", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Weather stored at 12:00 UTC on 2024-01-15
		// In the system's local timezone, convert this to see what local date it falls on
		utcTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
		localTime := utcTime.In(time.Local)
		localDate := localTime.Format(time.DateOnly)

		weather := &HourlyWeather{
			Time:        utcTime,
			Temperature: 15.5,
			Humidity:    65,
			WeatherIcon: "partly-cloudy",
		}
		err := ds.DB.Create(weather).Error
		require.NoError(t, err)

		// Query for the local date that this UTC time falls on
		results, err := ds.GetHourlyWeather(localDate)
		require.NoError(t, err)
		assert.Len(t, results, 1, "Should find weather for the local date")
		assert.InEpsilon(t, 15.5, results[0].Temperature, 0.0001)
	})

	t.Run("UTC weather crosses midnight boundary - timezone ahead of UTC", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Load Pacific/Auckland timezone (GMT+13 during NZDT)
		auckland, err := time.LoadLocation("Pacific/Auckland")
		require.NoError(t, err, "Failed to load Pacific/Auckland timezone")

		// Simulate user in GMT+13 timezone
		// Weather stored at 2024-01-14 20:30:00 UTC
		// In GMT+13, this is 2024-01-15 09:30:00 local
		utcTime := time.Date(2024, 1, 14, 20, 30, 0, 0, time.UTC)
		weather := &HourlyWeather{
			Time:        utcTime,
			Temperature: 18.5,
			Humidity:    70,
			WeatherIcon: "sunny",
		}
		err = ds.DB.Create(weather).Error
		require.NoError(t, err)

		// Query for local date "2024-01-15" in Auckland timezone
		// This should match the weather record because in Auckland time it's on 2024-01-15
		// The fix converts the local date to UTC range: 2024-01-14 11:00:00 to 2024-01-15 11:00:00
		results, err := ds.GetHourlyWeatherInLocation("2024-01-15", auckland)
		require.NoError(t, err)
		require.Len(t, results, 1, "Should find weather that falls within local date range")
		assert.InEpsilon(t, 18.5, results[0].Temperature, 0.0001)
	})

	t.Run("multiple weather records across local day", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Load Pacific/Auckland timezone
		auckland, err := time.LoadLocation("Pacific/Auckland")
		require.NoError(t, err, "Failed to load Pacific/Auckland timezone")

		// Insert weather records throughout a local day in GMT+13
		// Local day 2024-01-15 spans UTC: 2024-01-14 11:00:00 to 2024-01-15 11:00:00
		weatherRecords := []HourlyWeather{
			// Start of local day (2024-01-15 00:00 local = 2024-01-14 11:00 UTC)
			{Time: time.Date(2024, 1, 14, 11, 0, 0, 0, time.UTC), Temperature: 12.0},
			// Morning (2024-01-15 06:00 local = 2024-01-14 17:00 UTC)
			{Time: time.Date(2024, 1, 14, 17, 0, 0, 0, time.UTC), Temperature: 14.0},
			// Afternoon (2024-01-15 14:00 local = 2024-01-15 01:00 UTC)
			{Time: time.Date(2024, 1, 15, 1, 0, 0, 0, time.UTC), Temperature: 20.0},
			// Evening (2024-01-15 20:00 local = 2024-01-15 07:00 UTC)
			{Time: time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC), Temperature: 18.0},
			// Just before midnight local (2024-01-15 23:00 local = 2024-01-15 10:00 UTC)
			{Time: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Temperature: 15.0},
		}

		err = ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		// Query for local date "2024-01-15" in Auckland timezone
		results, err := ds.GetHourlyWeatherInLocation("2024-01-15", auckland)
		require.NoError(t, err)
		assert.Len(t, results, 5, "Should find all weather records within the local day")

		// Verify results are ordered by time
		assert.InEpsilon(t, 12.0, results[0].Temperature, 0.0001, "First record should be start of local day")
		assert.InEpsilon(t, 15.0, results[4].Temperature, 0.0001, "Last record should be end of local day")
	})

	t.Run("weather just outside local day boundary should not be included", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Load Pacific/Auckland timezone
		auckland, err := time.LoadLocation("Pacific/Auckland")
		require.NoError(t, err, "Failed to load Pacific/Auckland timezone")

		// For GMT+13, local day 2024-01-15 spans UTC: 2024-01-14 11:00:00 to 2024-01-15 11:00:00
		weatherRecords := []HourlyWeather{
			// Just before start (2024-01-14 10:59 UTC = 2024-01-14 23:59 local)
			{Time: time.Date(2024, 1, 14, 10, 59, 0, 0, time.UTC), Temperature: 10.0},
			// Within range (2024-01-14 11:00 UTC = 2024-01-15 00:00 local)
			{Time: time.Date(2024, 1, 14, 11, 0, 0, 0, time.UTC), Temperature: 12.0},
			// Within range (2024-01-15 10:59 UTC = 2024-01-15 23:59 local)
			{Time: time.Date(2024, 1, 15, 10, 59, 0, 0, time.UTC), Temperature: 18.0},
			// Just after end (2024-01-15 11:00 UTC = 2024-01-16 00:00 local)
			{Time: time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC), Temperature: 20.0},
		}

		err = ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		// Query for local date "2024-01-15" in Auckland timezone
		results, err := ds.GetHourlyWeatherInLocation("2024-01-15", auckland)
		require.NoError(t, err)
		assert.Len(t, results, 2, "Should only include weather within the exact local day range")

		// Verify we got the correct records
		assert.InEpsilon(t, 12.0, results[0].Temperature, 0.0001)
		assert.InEpsilon(t, 18.0, results[1].Temperature, 0.0001)
	})

	t.Run("timezone behind UTC - negative offset", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Load America/New_York timezone (UTC-5 or UTC-4 depending on DST)
		newYork, err := time.LoadLocation("America/New_York")
		require.NoError(t, err, "Failed to load America/New_York timezone")

		// For July 15, 2024, New York is UTC-4 (EDT)
		// Local day 2024-07-15 spans UTC: 2024-07-15 04:00:00 to 2024-07-16 04:00:00
		weatherRecords := []HourlyWeather{
			// 2024-07-15 02:00 local = 2024-07-15 06:00 UTC
			{Time: time.Date(2024, 7, 15, 6, 0, 0, 0, time.UTC), Temperature: 10.0},
			// 2024-07-15 08:00 local = 2024-07-15 12:00 UTC
			{Time: time.Date(2024, 7, 15, 12, 0, 0, 0, time.UTC), Temperature: 15.0},
			// 2024-07-15 18:00 local = 2024-07-15 22:00 UTC
			{Time: time.Date(2024, 7, 15, 22, 0, 0, 0, time.UTC), Temperature: 12.0},
		}

		err = ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		// Query for the local date in New York timezone
		results, err := ds.GetHourlyWeatherInLocation("2024-07-15", newYork)
		require.NoError(t, err)
		assert.Len(t, results, 3, "Should find all weather within the local day")
	})

	t.Run("empty results for date with no weather", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Insert weather for a different day
		weather := &HourlyWeather{
			Time:        time.Date(2024, 1, 20, 12, 0, 0, 0, time.UTC),
			Temperature: 15.5,
		}
		err := ds.DB.Create(weather).Error
		require.NoError(t, err)

		// Query for a day with no weather
		results, err := ds.GetHourlyWeather("2024-01-15")
		require.NoError(t, err)
		assert.Empty(t, results, "Should return empty results for date with no weather")
	})

	t.Run("invalid date format returns error", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Test with invalid date format
		_, err := ds.GetHourlyWeather("invalid-date")
		require.Error(t, err, "Should return error for invalid date format")
		assert.Contains(t, err.Error(), "parsing time", "Error should mention parsing time")
	})

	t.Run("results ordered by time ascending", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Create a date that will work in any timezone
		// Use times that span a single local day regardless of timezone offset
		baseDate := time.Date(2024, 6, 15, 0, 0, 0, 0, time.Local)
		localDateStr := baseDate.Format(time.DateOnly)

		// Create weather records within the local day
		weatherRecords := []HourlyWeather{
			{Time: baseDate.Add(10 * time.Hour).UTC(), Temperature: 20.0}, // 10:00 local
			{Time: baseDate.Add(5 * time.Hour).UTC(), Temperature: 12.0},  // 05:00 local
			{Time: baseDate.Add(15 * time.Hour).UTC(), Temperature: 22.0}, // 15:00 local
			{Time: baseDate.Add(1 * time.Hour).UTC(), Temperature: 10.0},  // 01:00 local
		}

		err := ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		results, err := ds.GetHourlyWeather(localDateStr)
		require.NoError(t, err)
		require.Len(t, results, 4, "Should find all 4 weather records")

		// Verify ascending order by checking temperatures
		assert.InEpsilon(t, 10.0, results[0].Temperature, 0.0001, "Should be ordered by time (earliest first)")
		assert.InEpsilon(t, 12.0, results[1].Temperature, 0.0001)
		assert.InEpsilon(t, 20.0, results[2].Temperature, 0.0001)
		assert.InEpsilon(t, 22.0, results[3].Temperature, 0.0001, "Should be ordered by time (latest last)")
	})

	t.Run("DST spring forward - 23 hour day boundary", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Load America/New_York timezone for DST testing
		newYork, err := time.LoadLocation("America/New_York")
		require.NoError(t, err, "Failed to load America/New_York timezone")

		// March 10, 2024 - Spring Forward in US (23-hour day)
		// Clocks jump from 2:00 AM to 3:00 AM EST -> EDT
		// Before DST: EST (UTC-5), After DST: EDT (UTC-4)
		// Local March 10 spans: 2024-03-10 05:00 UTC to 2024-03-11 04:00 UTC (23 hours)

		weatherRecords := []HourlyWeather{
			// Start of day (2024-03-10 00:00 local = 2024-03-10 05:00 UTC)
			{Time: time.Date(2024, 3, 10, 5, 0, 0, 0, time.UTC), Temperature: 1.0},
			// Mid-day (2024-03-10 12:00 local = 2024-03-10 16:00 UTC, now EDT)
			{Time: time.Date(2024, 3, 10, 16, 0, 0, 0, time.UTC), Temperature: 2.0},
			// End of day (2024-03-10 23:30 local = 2024-03-11 03:30 UTC)
			{Time: time.Date(2024, 3, 11, 3, 30, 0, 0, time.UTC), Temperature: 3.0},
			// NEXT day start (2024-03-11 00:30 local = 2024-03-11 04:30 UTC) - should NOT be included
			{Time: time.Date(2024, 3, 11, 4, 30, 0, 0, time.UTC), Temperature: 99.0},
		}

		err = ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		results, err := ds.GetHourlyWeatherInLocation("2024-03-10", newYork)
		require.NoError(t, err)
		assert.Len(t, results, 3, "Should find exactly 3 records within the 23-hour DST day")

		// Verify the next day's record is NOT included
		for _, r := range results {
			assert.NotEqual(t, 99.0, r.Temperature, "Should NOT include weather from next day")
		}
	})

	t.Run("DST fall back - 25 hour day boundary", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Load America/New_York timezone for DST testing
		newYork, err := time.LoadLocation("America/New_York")
		require.NoError(t, err, "Failed to load America/New_York timezone")

		// November 3, 2024 - Fall Back in US (25-hour day)
		// Clocks fall back from 2:00 AM to 1:00 AM EDT -> EST
		// Before DST: EDT (UTC-4), After DST: EST (UTC-5)
		// Local Nov 3 spans: 2024-11-03 04:00 UTC to 2024-11-04 05:00 UTC (25 hours)

		weatherRecords := []HourlyWeather{
			// Start of day (2024-11-03 00:00 local = 2024-11-03 04:00 UTC)
			{Time: time.Date(2024, 11, 3, 4, 0, 0, 0, time.UTC), Temperature: 1.0},
			// Mid-day (2024-11-03 12:00 local = 2024-11-03 16:00 UTC, still EDT)
			{Time: time.Date(2024, 11, 3, 16, 0, 0, 0, time.UTC), Temperature: 2.0},
			// Late night in the "extra" hour (2024-11-03 23:30 local = 2024-11-04 04:30 UTC, now EST)
			{Time: time.Date(2024, 11, 4, 4, 30, 0, 0, time.UTC), Temperature: 3.0},
			// NEXT day start (2024-11-04 00:30 local = 2024-11-04 05:30 UTC) - should NOT be included
			{Time: time.Date(2024, 11, 4, 5, 30, 0, 0, time.UTC), Temperature: 99.0},
		}

		err = ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		results, err := ds.GetHourlyWeatherInLocation("2024-11-03", newYork)
		require.NoError(t, err)
		assert.Len(t, results, 3, "Should find exactly 3 records within the 25-hour DST day")

		// Verify the next day's record is NOT included
		for _, r := range results {
			assert.NotEqual(t, 99.0, r.Temperature, "Should NOT include weather from next day")
		}

		// Verify the late-night record from the 25th hour IS included
		hasLateRecord := false
		for _, r := range results {
			if r.Temperature == 3.0 {
				hasLateRecord = true
				break
			}
		}
		assert.True(t, hasLateRecord, "Should include weather from the extra hour on fall back day")
	})
}

// TestGetHourlyWeather_EdgeCases tests edge cases and error conditions
func TestGetHourlyWeather_EdgeCases(t *testing.T) {
	t.Run("midnight UTC", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Weather at exactly midnight UTC
		weather := &HourlyWeather{
			Time:        time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Temperature: 10.0,
		}
		err := ds.DB.Create(weather).Error
		require.NoError(t, err)

		// Query using UTC timezone to test midnight handling
		results, err := ds.GetHourlyWeatherInLocation("2024-01-15", time.UTC)
		require.NoError(t, err)
		assert.Len(t, results, 1, "Should handle midnight UTC correctly")
	})

	t.Run("leap year date", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// February 29, 2024 (leap year) at 12:00 UTC
		utcTime := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
		localTime := utcTime.In(time.Local)
		localDate := localTime.Format(time.DateOnly)

		weather := &HourlyWeather{
			Time:        utcTime,
			Temperature: 5.0,
		}
		err := ds.DB.Create(weather).Error
		require.NoError(t, err)

		results, err := ds.GetHourlyWeather(localDate)
		require.NoError(t, err)
		assert.Len(t, results, 1, "Should handle leap year dates correctly")
	})

	t.Run("year boundary", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Load America/New_York timezone
		newYork, err := time.LoadLocation("America/New_York")
		require.NoError(t, err, "Failed to load America/New_York timezone")

		// New Year's Day in New York (EST = UTC-5)
		// Local Jan 1 spans UTC: 2024-01-01 05:00:00 to 2024-01-02 05:00:00
		weatherRecords := []HourlyWeather{
			// 2024-01-01 06:00 UTC = 2024-01-01 01:00 local (New Year's Day)
			{Time: time.Date(2024, 1, 1, 6, 0, 0, 0, time.UTC), Temperature: 1.0},
			// 2024-01-01 18:00 UTC = 2024-01-01 13:00 local (New Year's Day)
			{Time: time.Date(2024, 1, 1, 18, 0, 0, 0, time.UTC), Temperature: 2.0},
		}

		err = ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		// Query for New Year's Day in New York timezone
		results, err := ds.GetHourlyWeatherInLocation("2024-01-01", newYork)
		require.NoError(t, err)
		assert.Len(t, results, 2, "Should find both weather records on New Year's Day")

		// Verify we got the correct records
		hasNewYearRecord := false
		for _, r := range results {
			if r.Temperature == 1.0 {
				hasNewYearRecord = true
				break
			}
		}
		assert.True(t, hasNewYearRecord, "Should include weather from new year")
	})

	t.Run("very large dataset performance", func(t *testing.T) {
		if testing.Short() {
			t.Skip("Skipping performance test in short mode")
		}
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Insert many weather records for a single day
		baseTime := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
		weatherRecords := make([]HourlyWeather, 100)
		for i := range 100 {
			weatherRecords[i] = HourlyWeather{
				Time:        baseTime.Add(time.Duration(i) * 15 * time.Minute),
				Temperature: 15.0 + float64(i)*0.1,
			}
		}
		err := ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		start := time.Now()
		results, err := ds.GetHourlyWeatherInLocation("2024-01-15", time.UTC)
		duration := time.Since(start)

		require.NoError(t, err)
		assert.NotEmpty(t, results)
		assert.Less(t, duration.Milliseconds(), int64(100), "Query should complete quickly even with many records")
	})
}

// TestGetHourlyWeather_RegressionTests tests for the original timezone bug
func TestGetHourlyWeather_RegressionTests(t *testing.T) {
	t.Run("regression: morning detections show no weather (GMT+13)", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Load Pacific/Auckland timezone
		auckland, err := time.LoadLocation("Pacific/Auckland")
		require.NoError(t, err, "Failed to load Pacific/Auckland timezone")

		// This reproduces the original bug scenario:
		// User in GMT+13 timezone at 9:00 AM local on 2026-01-15
		// Last weather record: 2026-01-14 20:35:20 UTC
		// In local time: 2026-01-15 09:35:20
		// Detection at: 2026-01-15 09:00:00 local

		// Weather from "yesterday" UTC but "today" local
		weather := &HourlyWeather{
			Time:        time.Date(2026, 1, 14, 20, 35, 20, 0, time.UTC),
			Temperature: 18.5,
			Humidity:    70,
			WeatherIcon: "partly-cloudy",
		}
		err = ds.DB.Create(weather).Error
		require.NoError(t, err)

		// Query for local date "2026-01-15" in Auckland timezone
		// The fix ensures this weather record is found
		results, err := ds.GetHourlyWeatherInLocation("2026-01-15", auckland)
		require.NoError(t, err)
		require.Len(t, results, 1, "REGRESSION: Should find weather for morning detections in GMT+13")
		assert.Equal(t, "partly-cloudy", results[0].WeatherIcon)
	})

	t.Run("regression: weather appears after UTC catches up", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Load Pacific/Auckland timezone
		auckland, err := time.LoadLocation("Pacific/Auckland")
		require.NoError(t, err, "Failed to load Pacific/Auckland timezone")

		// Weather from late in UTC day
		// For GMT+13, this appears on the next local day
		weatherRecords := []HourlyWeather{
			{Time: time.Date(2026, 1, 14, 20, 0, 0, 0, time.UTC), Temperature: 15.0}, // 09:00 next day local
			{Time: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC), Temperature: 18.0}, // 23:00 same day local
		}

		err = ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		// Both should be found for local date 2026-01-15 in Auckland timezone
		results, err := ds.GetHourlyWeatherInLocation("2026-01-15", auckland)
		require.NoError(t, err)
		assert.Len(t, results, 2, "Should find both weather records that span the local day")
	})
}

// TestGetHourlyWeather_NonUTCStorageOffset tests queries against records stored
// with non-UTC timezone offsets. This reproduces the original bug where
// mattn/go-sqlite3 preserves the timezone offset in the stored string
// (e.g., "2024-01-15 11:00:00+13:00") and SQLite's lexicographic string
// comparison produces incorrect results when comparing against UTC-formatted
// query boundaries (e.g., "2024-01-14 22:00:00+00:00").
func TestGetHourlyWeather_NonUTCStorageOffset(t *testing.T) {
	t.Run("records stored with local offset are found correctly", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		auckland, err := time.LoadLocation("Pacific/Auckland")
		require.NoError(t, err)

		// Store records using Auckland local time (GMT+13).
		// mattn/go-sqlite3 serialises the offset, so the DB contains strings like
		// "2024-01-15 06:00:00+13:00" rather than the UTC equivalent.
		weatherRecords := []HourlyWeather{
			// 2024-01-15 06:00 NZDT = 2024-01-14 17:00 UTC
			{Time: time.Date(2024, 1, 15, 6, 0, 0, 0, auckland), Temperature: 14.0},
			// 2024-01-15 12:00 NZDT = 2024-01-14 23:00 UTC
			{Time: time.Date(2024, 1, 15, 12, 0, 0, 0, auckland), Temperature: 20.0},
			// 2024-01-15 18:00 NZDT = 2024-01-15 05:00 UTC
			{Time: time.Date(2024, 1, 15, 18, 0, 0, 0, auckland), Temperature: 17.0},
		}

		err = ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		// Query for 2024-01-15 in Auckland timezone.
		// UTC range: 2024-01-14 11:00 to 2024-01-15 11:00.
		// All three records fall within this range.
		results, err := ds.GetHourlyWeatherInLocation("2024-01-15", auckland)
		require.NoError(t, err)
		require.Len(t, results, 3, "All records stored with +13:00 offset should be found")

		assert.InEpsilon(t, 14.0, results[0].Temperature, 0.0001)
		assert.InEpsilon(t, 20.0, results[1].Temperature, 0.0001)
		assert.InEpsilon(t, 17.0, results[2].Temperature, 0.0001)
	})

	t.Run("yesterday records with local offset must not leak into today", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		auckland, err := time.LoadLocation("Pacific/Auckland")
		require.NoError(t, err)

		// Store records for YESTERDAY (2024-01-14 local) using Auckland offset.
		// These are stored with +13:00 offset in the DB.
		yesterdayRecords := []HourlyWeather{
			// 2024-01-14 12:00 NZDT = 2024-01-13 23:00 UTC
			{Time: time.Date(2024, 1, 14, 12, 0, 0, 0, auckland), Temperature: 50.0},
			// 2024-01-14 18:00 NZDT = 2024-01-14 05:00 UTC
			{Time: time.Date(2024, 1, 14, 18, 0, 0, 0, auckland), Temperature: 51.0},
			// 2024-01-14 23:00 NZDT = 2024-01-14 10:00 UTC
			{Time: time.Date(2024, 1, 14, 23, 0, 0, 0, auckland), Temperature: 52.0},
		}

		// Store records for TODAY (2024-01-15 local) using Auckland offset.
		todayRecords := []HourlyWeather{
			// 2024-01-15 08:00 NZDT = 2024-01-14 19:00 UTC
			{Time: time.Date(2024, 1, 15, 8, 0, 0, 0, auckland), Temperature: 15.0},
			// 2024-01-15 11:00 NZDT = 2024-01-14 22:00 UTC
			{Time: time.Date(2024, 1, 15, 11, 0, 0, 0, auckland), Temperature: 18.0},
		}

		allRecords := make([]HourlyWeather, 0, len(yesterdayRecords)+len(todayRecords))
		allRecords = append(allRecords, yesterdayRecords...)
		allRecords = append(allRecords, todayRecords...)
		err = ds.DB.Create(&allRecords).Error
		require.NoError(t, err)

		// Query for today (2024-01-15) in Auckland timezone.
		// UTC range: 2024-01-14 11:00 to 2024-01-15 11:00.
		// Yesterday's records (UTC: 23:00 Jan 13, 05:00 Jan 14, 10:00 Jan 14)
		// should all fall OUTSIDE this range.
		results, err := ds.GetHourlyWeatherInLocation("2024-01-15", auckland)
		require.NoError(t, err)
		require.Len(t, results, 2, "Only today's records should be returned, not yesterday's")

		// Verify none of yesterday's sentinel temperatures leaked in.
		for _, r := range results {
			assert.Less(t, r.Temperature, 50.0,
				"Yesterday's records (temp >= 50) must not appear in today's query")
		}
	})

	t.Run("mixed UTC and local offset records coexist correctly", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		auckland, err := time.LoadLocation("Pacific/Auckland")
		require.NoError(t, err)

		// Simulate the transition period: some records stored with local offset
		// (legacy) and some with UTC (new code).
		// All represent times on 2024-01-15 in Auckland.
		weatherRecords := []HourlyWeather{
			// Legacy record stored with +13:00 offset.
			// 2024-01-15 09:00 NZDT = 2024-01-14 20:00 UTC
			{Time: time.Date(2024, 1, 15, 9, 0, 0, 0, auckland), Temperature: 16.0},
			// New record stored in UTC.
			// 2024-01-15 01:00 UTC = 2024-01-15 14:00 NZDT
			{Time: time.Date(2024, 1, 15, 1, 0, 0, 0, time.UTC), Temperature: 21.0},
			// Legacy record stored with +13:00 offset.
			// 2024-01-15 22:00 NZDT = 2024-01-15 09:00 UTC
			{Time: time.Date(2024, 1, 15, 22, 0, 0, 0, auckland), Temperature: 13.0},
		}

		err = ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		// All three records fall within 2024-01-15 Auckland time
		// (UTC range: 2024-01-14 11:00 to 2024-01-15 11:00).
		results, err := ds.GetHourlyWeatherInLocation("2024-01-15", auckland)
		require.NoError(t, err)
		require.Len(t, results, 3, "Should find all records regardless of stored timezone offset")
	})

	t.Run("records with negative offset are found correctly", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		newYork, err := time.LoadLocation("America/New_York")
		require.NoError(t, err)

		// Store records using New York local time (EDT = UTC-4 in July).
		// mattn/go-sqlite3 serialises as e.g. "2024-07-15 10:00:00-04:00".
		weatherRecords := []HourlyWeather{
			// 2024-07-15 10:00 EDT = 2024-07-15 14:00 UTC
			{Time: time.Date(2024, 7, 15, 10, 0, 0, 0, newYork), Temperature: 28.0},
			// 2024-07-15 22:00 EDT = 2024-07-16 02:00 UTC
			{Time: time.Date(2024, 7, 15, 22, 0, 0, 0, newYork), Temperature: 24.0},
		}

		err = ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		// Query for 2024-07-15 in New York timezone.
		// UTC range: 2024-07-15 04:00 to 2024-07-16 04:00.
		results, err := ds.GetHourlyWeatherInLocation("2024-07-15", newYork)
		require.NoError(t, err)
		require.Len(t, results, 2, "Records with negative UTC offset should be found correctly")

		assert.InEpsilon(t, 28.0, results[0].Temperature, 0.0001)
		assert.InEpsilon(t, 24.0, results[1].Temperature, 0.0001)
	})

	t.Run("boundary exclusion with local offset is exact", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		auckland, err := time.LoadLocation("Pacific/Auckland")
		require.NoError(t, err)

		// For GMT+13, local day 2024-01-15 spans UTC: 2024-01-14 11:00 to 2024-01-15 11:00.
		// Store boundary records with +13:00 offset to verify exact inclusion/exclusion.
		weatherRecords := []HourlyWeather{
			// Just before start: 2024-01-14 23:59 NZDT = 2024-01-14 10:59 UTC → OUTSIDE
			{Time: time.Date(2024, 1, 14, 23, 59, 0, 0, auckland), Temperature: 99.0},
			// Exact start: 2024-01-15 00:00 NZDT = 2024-01-14 11:00 UTC → INSIDE
			{Time: time.Date(2024, 1, 15, 0, 0, 0, 0, auckland), Temperature: 10.0},
			// Exact end: 2024-01-16 00:00 NZDT = 2024-01-15 11:00 UTC → OUTSIDE (< not <=)
			{Time: time.Date(2024, 1, 16, 0, 0, 0, 0, auckland), Temperature: 98.0},
			// Just before end: 2024-01-15 23:59 NZDT = 2024-01-15 10:59 UTC → INSIDE
			{Time: time.Date(2024, 1, 15, 23, 59, 0, 0, auckland), Temperature: 20.0},
		}

		err = ds.DB.Create(&weatherRecords).Error
		require.NoError(t, err)

		results, err := ds.GetHourlyWeatherInLocation("2024-01-15", auckland)
		require.NoError(t, err)
		require.Len(t, results, 2, "Only records within [start, end) should be included")

		assert.InEpsilon(t, 10.0, results[0].Temperature, 0.0001, "Start-of-day record should be included")
		assert.InEpsilon(t, 20.0, results[1].Temperature, 0.0001, "End-of-day record should be included")
	})
}
