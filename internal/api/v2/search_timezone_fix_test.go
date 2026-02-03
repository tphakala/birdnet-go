// Package api_test contains tests for timezone handling fixes in search and detection APIs
// These tests demonstrate and validate the fix for issue #982 where database-stored local
// time strings were incorrectly parsed as UTC, causing timezone conversion bugs.
package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimezoneParsingBug demonstrates the timezone bug in search results
// This test shows how the current implementation incorrectly handles timezone conversion
func TestTimezoneParsingBug(t *testing.T) {
	// This test demonstrates the core problem:
	// Database stores "2024-01-15 14:30:00" as local time string
	// Current code parses this with time.Parse() which assumes UTC
	// This causes 12-hour offset for users in timezone UTC+12

	testCases := []struct {
		name               string
		dbDateString       string // What's stored in database (local time)
		dbTimeString       string // What's stored in database (local time)
		expectedDate       string // What date should be in API response
		expectedHour       int    // What hour should be in API response
		expectedMinute     int    // What minute should be in API response
		userTimezone       string // User's timezone (for context)
		problemDescription string
	}{
		{
			name:               "afternoon_detection_utc_plus_12",
			dbDateString:       "2024-01-15",
			dbTimeString:       "14:30:00", // 2:30 PM local time stored in DB
			expectedDate:       "2024-01-15",
			expectedHour:       14, // Should remain 14 (2 PM), not convert to 2 AM
			expectedMinute:     30,
			userTimezone:       "Pacific/Auckland", // UTC+12 during summer
			problemDescription: "User in Auckland sees 2:30 AM instead of 2:30 PM",
		},
		{
			name:               "morning_detection_utc_plus_12",
			dbDateString:       "2024-01-15",
			dbTimeString:       "08:15:00", // 8:15 AM local time stored in DB
			expectedDate:       "2024-01-15",
			expectedHour:       8, // Should remain 8 AM, not convert to 8 PM previous day
			expectedMinute:     15,
			userTimezone:       "Pacific/Auckland",
			problemDescription: "User in Auckland sees 8:15 PM on Jan 14 instead of 8:15 AM on Jan 15",
		},
		{
			name:               "evening_detection_utc_plus_12",
			dbDateString:       "2024-01-15",
			dbTimeString:       "22:45:00", // 10:45 PM local time stored in DB
			expectedDate:       "2024-01-15",
			expectedHour:       22, // Should remain 22 (10 PM), not convert to 10 AM
			expectedMinute:     45,
			userTimezone:       "Pacific/Auckland",
			problemDescription: "User in Auckland sees 10:45 AM instead of 10:45 PM",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate what currently happens in SearchDetections (interfaces.go:1882)
			// timestamp, err := time.Parse("2006-01-02 15:04:05", scanned.Date+" "+scanned.Time)

			dbTimestamp := tc.dbDateString + " " + tc.dbTimeString
			t.Logf("Database stores: %s (local time)", dbTimestamp)
			t.Logf("User timezone: %s", tc.userTimezone)
			t.Logf("Problem: %s", tc.problemDescription)

			// Current problematic parsing (assumes UTC)
			currentParsedTime, err := time.Parse(time.DateTime, dbTimestamp)
			require.NoError(t, err, "Should parse database timestamp")

			t.Logf("Current parsing treats as UTC: %s", currentParsedTime.Format(time.RFC3339))
			t.Logf("When JSON marshaled, becomes: %s", currentParsedTime.Format(time.RFC3339))

			// Load user's timezone for comparison
			userLoc, err := time.LoadLocation(tc.userTimezone)
			require.NoError(t, err, "Should load user timezone")

			// What the user sees after timezone conversion
			userSeesTime := currentParsedTime.In(userLoc)
			t.Logf("User in %s sees: %s", tc.userTimezone, userSeesTime.Format(time.DateTime))

			// THE BUG: User sees wrong time due to double timezone conversion
			// Database has local time -> Parse assumes UTC -> Convert to user timezone = wrong time

			// What SHOULD happen: preserve the local time from database
			expectedTime, err := time.ParseInLocation("2006-01-02 15:04:05", dbTimestamp, userLoc)
			require.NoError(t, err, "Should parse in user location")

			t.Logf("User SHOULD see: %s", expectedTime.Format(time.DateTime))

			// Validate the problem exists
			assert.NotEqual(t, expectedTime.Hour(), userSeesTime.Hour(),
				"Current implementation causes wrong hour due to timezone conversion")

			// Validate what the correct values should be
			assert.Equal(t, tc.expectedDate, expectedTime.Format(time.DateOnly),
				"Expected date should match database date")
			assert.Equal(t, tc.expectedHour, expectedTime.Hour(),
				"Expected hour should match database hour")
			assert.Equal(t, tc.expectedMinute, expectedTime.Minute(),
				"Expected minute should match database minute")
		})
	}
}

// TestTimezoneParsingFix validates the solution for timezone handling
// This test demonstrates that using ParseInLocation preserves local time correctly
func TestTimezoneParsingFix(t *testing.T) {
	// This test shows how to fix the timezone issue

	testCases := []struct {
		name         string
		dbDateString string
		dbTimeString string
		userTimezone string
	}{
		{
			name:         "fix_for_utc_plus_12",
			dbDateString: "2024-01-15",
			dbTimeString: "14:30:00",
			userTimezone: "Pacific/Auckland",
		},
		{
			name:         "fix_for_utc_minus_8",
			dbDateString: "2024-01-15",
			dbTimeString: "14:30:00",
			userTimezone: "America/Los_Angeles",
		},
		{
			name:         "fix_for_utc",
			dbDateString: "2024-01-15",
			dbTimeString: "14:30:00",
			userTimezone: "UTC",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dbTimestamp := tc.dbDateString + " " + tc.dbTimeString

			// Load the user's timezone
			userLoc, err := time.LoadLocation(tc.userTimezone)
			require.NoError(t, err)

			// PROPOSED FIX: Parse the database time string as if it's already in the user's local timezone
			// This preserves the local time from the database without conversion
			localTime, err := time.ParseInLocation("2006-01-02 15:04:05", dbTimestamp, userLoc)
			require.NoError(t, err)

			// Alternative fix: Parse as UTC but manually adjust to avoid double conversion
			// This would require knowing the server's timezone offset

			t.Logf("Database time: %s", dbTimestamp)
			t.Logf("Parsed as local time in %s: %s", tc.userTimezone, localTime.Format(time.RFC3339))

			// Validate that the fix preserves the database time components
			assert.Equal(t, "2024-01-15", localTime.Format(time.DateOnly), "Date should be preserved")
			assert.Equal(t, 14, localTime.Hour(), "Hour should be preserved")
			assert.Equal(t, 30, localTime.Minute(), "Minute should be preserved")

			// When this gets JSON marshaled and sent to frontend, it should preserve local time
			jsonTime := localTime.Format(time.RFC3339)
			t.Logf("JSON marshaled time: %s", jsonTime)

			// Parse it back to verify round-trip preservation
			parsedBack, err := time.Parse(time.RFC3339, jsonTime)
			require.NoError(t, err)

			// Convert back to user timezone for final verification
			finalTime := parsedBack.In(userLoc)
			assert.Equal(t, 14, finalTime.Hour(), "Hour should survive round-trip")
			assert.Equal(t, 30, finalTime.Minute(), "Minute should survive round-trip")
		})
	}
}

// TestTimezoneEdgeCases covers edge cases that commonly cause timezone bugs
// Including midnight boundaries, noon, and just before midnight
func TestTimezoneEdgeCases(t *testing.T) {
	edgeCases := []struct {
		name         string
		dbDate       string
		dbTime       string
		expectedHour int
		expectedDate string
		description  string
	}{
		{
			name:         "midnight_detection",
			dbDate:       "2024-01-16",
			dbTime:       "00:00:00",
			expectedHour: 0,
			expectedDate: "2024-01-16",
			description:  "Midnight detection should not shift to previous day",
		},
		{
			name:         "just_before_midnight",
			dbDate:       "2024-01-15",
			dbTime:       "23:59:59",
			expectedHour: 23,
			expectedDate: "2024-01-15",
			description:  "Late night detection should not shift to next day",
		},
		{
			name:         "noon_detection",
			dbDate:       "2024-01-15",
			dbTime:       "12:00:00",
			expectedHour: 12,
			expectedDate: "2024-01-15",
			description:  "Noon detection should remain at noon",
		},
	}

	// Test with timezone that commonly causes issues (UTC+12)
	aucklandTz, err := time.LoadLocation("Pacific/Auckland")
	require.NoError(t, err)

	for _, tc := range edgeCases {
		t.Run(tc.name, func(t *testing.T) {
			dbTimestamp := tc.dbDate + " " + tc.dbTime

			// Current problematic approach
			currentTime, err := time.Parse(time.DateTime, dbTimestamp)
			require.NoError(t, err)

			// What user sees (with timezone conversion)
			userTime := currentTime.In(aucklandTz)

			// Proposed fix approach
			fixedTime, err := time.ParseInLocation("2006-01-02 15:04:05", dbTimestamp, aucklandTz)
			require.NoError(t, err)

			t.Logf("Test case: %s", tc.description)
			t.Logf("Database: %s", dbTimestamp)
			t.Logf("Current (wrong): User sees %s", userTime.Format(time.DateTime))
			t.Logf("Fixed (correct): User sees %s", fixedTime.Format(time.DateTime))

			// Validate the fix preserves expected values
			assert.Equal(t, tc.expectedDate, fixedTime.Format(time.DateOnly), "Date should be preserved")
			assert.Equal(t, tc.expectedHour, fixedTime.Hour(), "Hour should be preserved")

			// Demonstrate the problem exists
			if tc.expectedHour != userTime.Hour() {
				t.Logf("BUG CONFIRMED: Current implementation shows hour %d instead of expected hour %d",
					userTime.Hour(), tc.expectedHour)
			}
		})
	}
}
