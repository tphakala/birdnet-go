package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// TestNightFilterExcludesSunriseSunsetWindows verifies that the "Night" time-of-day
// filter properly excludes the ±30 minute windows around sunrise and sunset.
// This test demonstrates issue #961 where sunrise/sunset detections were incorrectly
// included in "Night Only" searches.
//
// The test is timezone-agnostic and works in any timezone (UTC, local, etc.) by
// dynamically calculating test times based on actual sunrise/sunset for the test date.
func TestNightFilterExcludesSunriseSunsetWindows(t *testing.T) {
	// Setup test date and location
	// Use time.Local to match whatever timezone the test is running in
	// Use coordinates near the prime meridian to avoid sunset crossing midnight in UTC
	testDate := time.Date(2025, 7, 15, 0, 0, 0, 0, time.Local)
	latitude := 51.5074  // London, UK - near UTC meridian
	longitude := -0.1278 // Avoids sunset crossing midnight in UTC timezone

	// Create test database with location settings
	settings := &conf.Settings{}
	settings.BirdNET.Latitude = latitude
	settings.BirdNET.Longitude = longitude

	ds := createDatabase(t, settings)

	// Calculate actual sunrise/sunset times for this location and date
	sc := suncalc.NewSunCalc(latitude, longitude)
	sunTimes, err := sc.GetSunEventTimes(testDate)
	require.NoError(t, err, "Failed to calculate sun times")

	// Log the calculated times for debugging
	t.Logf("Test date: %s (timezone: %s)", testDate.Format(time.DateOnly), testDate.Location())
	t.Logf("Calculated sunrise: %s", sunTimes.Sunrise.Format(time.TimeOnly))
	t.Logf("Calculated sunset: %s", sunTimes.Sunset.Format(time.TimeOnly))

	// Define the 30-minute window
	window := 30 * time.Minute

	// Log the windows for debugging
	t.Logf("Sunrise window: %s to %s",
		sunTimes.Sunrise.Add(-window).Format(time.TimeOnly),
		sunTimes.Sunrise.Add(window).Format(time.TimeOnly))
	t.Logf("Sunset window: %s to %s",
		sunTimes.Sunset.Add(-window).Format(time.TimeOnly),
		sunTimes.Sunset.Add(window).Format(time.TimeOnly))

	// Dynamically generate test times based on calculated sunrise/sunset
	// This makes the test work in any timezone
	testCases := []struct {
		name          string
		timeOffset    time.Duration // Offset from a reference time
		reference     time.Time     // Reference time (sunrise or sunset)
		shouldBeNight bool
		description   string
	}{
		// Deep night cases - well outside any transition windows
		{
			name:          "Deep Night - 2h before sunrise",
			timeOffset:    -2 * time.Hour,
			reference:     sunTimes.Sunrise,
			shouldBeNight: true,
			description:   "Well before sunrise window, clearly night",
		},
		{
			name:          "Deep Night - 2h after sunset",
			timeOffset:    2 * time.Hour,
			reference:     sunTimes.Sunset,
			shouldBeNight: true,
			description:   "Well after sunset window, clearly night",
		},

		// Just before sunrise window
		{
			name:          "Before Sunrise Window - 31min before sunrise",
			timeOffset:    -31 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: true,
			description:   "Just before sunrise window begins, should still be night",
		},

		// Within sunrise window - should NOT be night
		{
			name:          "Sunrise Window Start - 30min before sunrise",
			timeOffset:    -30 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "At start of sunrise window, should NOT be night",
		},
		{
			name:          "Sunrise Window - 15min before sunrise",
			timeOffset:    -15 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "Within sunrise window, should NOT be night",
		},
		{
			name:          "Sunrise Exact",
			timeOffset:    0,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "Exact sunrise time, should NOT be night",
		},
		{
			name:          "Sunrise Window - 15min after sunrise",
			timeOffset:    15 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "Within sunrise window, should NOT be night",
		},
		{
			name:          "Sunrise Window End - 30min after sunrise",
			timeOffset:    30 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "At end of sunrise window, should NOT be night",
		},

		// After sunrise window - day time
		{
			name:          "Early Day - 31min after sunrise",
			timeOffset:    31 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "After sunrise window, clearly day",
		},

		// Midday - definitely day
		{
			name:          "Midday",
			timeOffset:    0,
			reference:     sunTimes.Sunrise.Add(sunTimes.Sunset.Sub(sunTimes.Sunrise) / 2),
			shouldBeNight: false,
			description:   "Middle of the day, clearly not night",
		},

		// Before sunset window - still day
		{
			name:          "Late Day - 31min before sunset",
			timeOffset:    -31 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "Before sunset window, still day",
		},

		// Within sunset window - should NOT be night
		{
			name:          "Sunset Window Start - 30min before sunset",
			timeOffset:    -30 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "At start of sunset window, should NOT be night",
		},
		{
			name:          "Sunset Window - 15min before sunset",
			timeOffset:    -15 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "Within sunset window, should NOT be night",
		},
		{
			name:          "Sunset Exact",
			timeOffset:    0,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "Exact sunset time, should NOT be night",
		},
		{
			name:          "Sunset Window - 15min after sunset",
			timeOffset:    15 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "Within sunset window, should NOT be night",
		},
		{
			name:          "Sunset Window End - 30min after sunset",
			timeOffset:    30 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "At end of sunset window, should NOT be night",
		},

		// After sunset window - night
		{
			name:          "Early Night - 31min after sunset",
			timeOffset:    31 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: true,
			description:   "After sunset window ends, should be night",
		},
	}

	// Insert test detections with dynamically calculated times
	dateStr := testDate.Format(time.DateOnly)
	insertedTimes := make(map[string]bool) // Track what we inserted

	for _, tc := range testCases {
		testTime := tc.reference.Add(tc.timeOffset)
		timeStr := testTime.Format(time.TimeOnly)

		// Skip if we've already inserted this exact time (avoid duplicates)
		if insertedTimes[timeStr] {
			t.Logf("Skipping duplicate time: %s for test case: %s", timeStr, tc.name)
			continue
		}
		insertedTimes[timeStr] = true

		note := &Note{
			Date:           dateStr,
			Time:           timeStr,
			ScientificName: "Strix varia", // Barred Owl - a nocturnal species
			CommonName:     "Barred Owl",
			Confidence:     0.9,
		}
		err := ds.Save(note, []Results{})
		require.NoError(t, err, "Failed to insert test note for %s at %s", tc.name, timeStr)
		t.Logf("Inserted detection: %s at %s (should be night: %v)", tc.name, timeStr, tc.shouldBeNight)
	}

	// Test the Night filter using SearchDetections
	filters := &SearchFilters{
		TimeOfDay: TimeOfDayNight,
		DateStart: dateStr,
		DateEnd:   dateStr,
	}

	results, _, err := ds.SearchDetections(filters)
	require.NoError(t, err, "Failed to execute search with Night filter")

	// Create a map of returned timestamps for easy lookup
	returnedTimes := make(map[string]bool)
	for _, detection := range results {
		timeStr := detection.Timestamp.Format(time.TimeOnly)
		returnedTimes[timeStr] = true
		t.Logf("Night filter returned: %s", timeStr)
	}

	// Verify each test case
	expectedNightCount := 0
	for _, tc := range testCases {
		if tc.shouldBeNight {
			expectedNightCount++
		}
	}

	// Run subtests to verify Night filter behavior
	for _, tc := range testCases {
		testTime := tc.reference.Add(tc.timeOffset)
		timeStr := testTime.Format(time.TimeOnly)

		// Skip duplicate time checks
		if !insertedTimes[timeStr] {
			continue
		}

		t.Run(tc.name, func(t *testing.T) {
			wasReturned := returnedTimes[timeStr]

			if tc.shouldBeNight {
				assert.True(t, wasReturned,
					"%s (%s): Expected to be included in Night filter but was excluded. %s",
					tc.name, timeStr, tc.description)
			} else {
				assert.False(t, wasReturned,
					"%s (%s): Expected to be excluded from Night filter but was included. %s (THIS IS THE BUG FROM ISSUE #961)",
					tc.name, timeStr, tc.description)
			}
		})
	}

	// Log summary for context
	t.Logf("\n=== Test Summary ===")
	t.Logf("Night filter returned %d detections out of %d unique times inserted", len(results), len(insertedTimes))
	t.Logf("Expected %d night detections (2 deep night + 2 edge cases)", expectedNightCount)
	t.Log("\nThis test verifies issue #961 fix:")
	t.Log("The Night filter must exclude the ±30 minute windows around sunrise and sunset")
	t.Log("Before fix: Incorrectly included sunrise/sunset/day detections")
	t.Log("After fix: Correctly returns only true night detections")
}
