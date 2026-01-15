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
	t.Parallel()

	t.Run("UTC weather matches local date - same timezone", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Weather stored at 12:00 UTC on 2024-01-15
		// In the system's local timezone, convert this to see what local date it falls on
		utcTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
		localTime := utcTime.In(time.Local)
		localDate := localTime.Format("2006-01-02")

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
		assert.Equal(t, 15.5, results[0].Temperature)
	})

	t.Run("UTC weather crosses midnight boundary - timezone ahead of UTC", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

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
		err := ds.DB.Create(weather).Error
		require.NoError(t, err)

		// Query for local date "2024-01-15"
		// This should match the weather record because in local time it's on 2024-01-15
		// The fix converts the local date to UTC range: 2024-01-14 11:00:00 to 2024-01-15 11:00:00
		results, err := ds.GetHourlyWeather("2024-01-15")
		require.NoError(t, err)
		assert.Len(t, results, 1, "Should find weather that falls within local date range")
		assert.Equal(t, 18.5, results[0].Temperature)
	})

	t.Run("multiple weather records across local day", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

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

		for i := range weatherRecords {
			err := ds.DB.Create(&weatherRecords[i]).Error
			require.NoError(t, err)
		}

		// Query for local date "2024-01-15"
		results, err := ds.GetHourlyWeather("2024-01-15")
		require.NoError(t, err)
		assert.Len(t, results, 5, "Should find all weather records within the local day")

		// Verify results are ordered by time
		assert.Equal(t, 12.0, results[0].Temperature, "First record should be start of local day")
		assert.Equal(t, 15.0, results[4].Temperature, "Last record should be end of local day")
	})

	t.Run("weather just outside local day boundary should not be included", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

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

		for i := range weatherRecords {
			err := ds.DB.Create(&weatherRecords[i]).Error
			require.NoError(t, err)
		}

		// Query for local date "2024-01-15"
		results, err := ds.GetHourlyWeather("2024-01-15")
		require.NoError(t, err)
		assert.Len(t, results, 2, "Should only include weather within the exact local day range")

		// Verify we got the correct records
		assert.Equal(t, 12.0, results[0].Temperature)
		assert.Equal(t, 18.0, results[1].Temperature)
	})

	t.Run("timezone behind UTC - negative offset", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// This test demonstrates behavior for timezones behind UTC
		// Note: The exact number of results depends on the system's actual timezone
		// Create a date in local time and add weather throughout that local day
		baseDate := time.Date(2024, 7, 15, 0, 0, 0, 0, time.Local)
		localDateStr := baseDate.Format("2006-01-02")

		weatherRecords := []HourlyWeather{
			{Time: baseDate.Add(2 * time.Hour).UTC(), Temperature: 10.0},
			{Time: baseDate.Add(8 * time.Hour).UTC(), Temperature: 15.0},
			{Time: baseDate.Add(18 * time.Hour).UTC(), Temperature: 12.0},
		}

		for i := range weatherRecords {
			err := ds.DB.Create(&weatherRecords[i]).Error
			require.NoError(t, err)
		}

		// Query for the local date
		results, err := ds.GetHourlyWeather(localDateStr)
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
		localDateStr := baseDate.Format("2006-01-02")

		// Create weather records within the local day
		weatherRecords := []HourlyWeather{
			{Time: baseDate.Add(10 * time.Hour).UTC(), Temperature: 20.0}, // 10:00 local
			{Time: baseDate.Add(5 * time.Hour).UTC(), Temperature: 12.0},  // 05:00 local
			{Time: baseDate.Add(15 * time.Hour).UTC(), Temperature: 22.0}, // 15:00 local
			{Time: baseDate.Add(1 * time.Hour).UTC(), Temperature: 10.0},  // 01:00 local
		}

		for i := range weatherRecords {
			err := ds.DB.Create(&weatherRecords[i]).Error
			require.NoError(t, err)
		}

		results, err := ds.GetHourlyWeather(localDateStr)
		require.NoError(t, err)
		require.Len(t, results, 4, "Should find all 4 weather records")

		// Verify ascending order by checking temperatures
		assert.Equal(t, 10.0, results[0].Temperature, "Should be ordered by time (earliest first)")
		assert.Equal(t, 12.0, results[1].Temperature)
		assert.Equal(t, 20.0, results[2].Temperature)
		assert.Equal(t, 22.0, results[3].Temperature, "Should be ordered by time (latest last)")
	})

	t.Run("daylight saving time boundary", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Test around DST boundary (using a fixed date for consistency)
		// Note: This test demonstrates the importance of timezone-aware queries
		// In real systems, time.Local will handle DST transitions automatically
		weatherRecords := []HourlyWeather{
			{Time: time.Date(2024, 3, 10, 6, 0, 0, 0, time.UTC), Temperature: 8.0},
			{Time: time.Date(2024, 3, 10, 12, 0, 0, 0, time.UTC), Temperature: 12.0},
			{Time: time.Date(2024, 3, 10, 18, 0, 0, 0, time.UTC), Temperature: 15.0},
		}

		for i := range weatherRecords {
			err := ds.DB.Create(&weatherRecords[i]).Error
			require.NoError(t, err)
		}

		results, err := ds.GetHourlyWeather("2024-03-10")
		require.NoError(t, err)
		assert.NotEmpty(t, results, "Should handle DST boundary dates")
	})
}

// TestGetHourlyWeather_EdgeCases tests edge cases and error conditions
func TestGetHourlyWeather_EdgeCases(t *testing.T) {
	t.Parallel()

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

		results, err := ds.GetHourlyWeather("2024-01-15")
		require.NoError(t, err)
		assert.Len(t, results, 1, "Should handle midnight UTC correctly")
	})

	t.Run("leap year date", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// February 29, 2024 (leap year) at 12:00 UTC
		utcTime := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
		localTime := utcTime.In(time.Local)
		localDate := localTime.Format("2006-01-02")

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

		// New Year's Eve and New Year's Day
		weatherRecords := []HourlyWeather{
			{Time: time.Date(2023, 12, 31, 23, 0, 0, 0, time.UTC), Temperature: 2.0},
			{Time: time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC), Temperature: 1.0},
		}

		for i := range weatherRecords {
			err := ds.DB.Create(&weatherRecords[i]).Error
			require.NoError(t, err)
		}

		// Query for New Year's Day
		results, err := ds.GetHourlyWeather("2024-01-01")
		require.NoError(t, err)
		assert.NotEmpty(t, results, "Should handle year boundary")

		// The exact count depends on timezone, but we should get at least the 01:00 record
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
		for i := 0; i < 100; i++ {
			weather := &HourlyWeather{
				Time:        baseTime.Add(time.Duration(i) * 15 * time.Minute),
				Temperature: 15.0 + float64(i)*0.1,
			}
			err := ds.DB.Create(weather).Error
			require.NoError(t, err)
		}

		start := time.Now()
		results, err := ds.GetHourlyWeather("2024-01-15")
		duration := time.Since(start)

		require.NoError(t, err)
		assert.NotEmpty(t, results)
		assert.Less(t, duration.Milliseconds(), int64(100), "Query should complete quickly even with many records")
	})
}

// TestGetHourlyWeather_RegressionTests tests for the original timezone bug
func TestGetHourlyWeather_RegressionTests(t *testing.T) {
	t.Parallel()

	t.Run("regression: morning detections show no weather (GMT+13)", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

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
		err := ds.DB.Create(weather).Error
		require.NoError(t, err)

		// Query for local date "2026-01-15"
		// The fix ensures this weather record is found
		results, err := ds.GetHourlyWeather("2026-01-15")
		require.NoError(t, err)
		assert.Len(t, results, 1, "REGRESSION: Should find weather for morning detections in GMT+13")
		assert.Equal(t, "partly-cloudy", results[0].WeatherIcon)
	})

	t.Run("regression: weather appears after UTC catches up", func(t *testing.T) {
		t.Parallel()
		ds := setupWeatherTestDB(t)

		// Weather from late in UTC day
		// For GMT+13, this appears on the next local day
		earlyWeather := &HourlyWeather{
			Time:        time.Date(2026, 1, 14, 20, 0, 0, 0, time.UTC), // 09:00 next day local
			Temperature: 15.0,
		}
		laterWeather := &HourlyWeather{
			Time:        time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC), // 23:00 same day local
			Temperature: 18.0,
		}

		err := ds.DB.Create(earlyWeather).Error
		require.NoError(t, err)
		err = ds.DB.Create(laterWeather).Error
		require.NoError(t, err)

		// Both should be found for local date 2026-01-15
		results, err := ds.GetHourlyWeather("2026-01-15")
		require.NoError(t, err)
		assert.Len(t, results, 2, "Should find both weather records that span the local day")
	})
}
