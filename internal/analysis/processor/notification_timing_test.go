// notification_timing_test.go: Tests for GitHub issue #1702
// Validates that notification timing uses detection timestamp (Note.BeginTime)
// rather than processing timestamp (time.Now()).
//
// ROOT CAUSE: The notification system was using time.Now() instead of Note.BeginTime
// when checking and recording species detections. This caused:
//   - "First of year" notifications to be delayed by ~24 hours
//   - Species tracking to use processing time, not detection time
//   - Year boundaries to be evaluated at wrong times
//
// SCENARIO: Detection at 11:55 PM Dec 31 processed at 12:05 AM Jan 1
// Expected: Detection is for year Dec 31 (old year)
// Bug: Detection was counted for Jan 1 (new year) due to time.Now()
package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// setupMockDatastore creates a mock datastore with standard expectations for species tracker tests.
// This reduces duplication across notification timing tests.
func setupMockDatastore(t *testing.T) *mocks.MockInterface {
	t.Helper()
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	mockDS.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	mockDS.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	mockDS.On("Save", mock.AnythingOfType("*datastore.Note"), mock.Anything).
		Return(nil).Maybe()
	mockDS.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	return mockDS
}

// TestNotificationTiming_CrossMidnight validates that detections occurring
// before midnight are correctly attributed to the correct day even when
// processed after midnight.
//
// This is the core test for GitHub issue #1702.
func TestNotificationTiming_CrossMidnight(t *testing.T) {
	t.Parallel()

	mockDS := setupMockDatastore(t)

	// Create species tracker with yearly tracking enabled
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := species.NewTrackerFromSettings(mockDS, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	// Simulate scenario: Detection at 11:55 PM on Dec 31, 2025
	// Processing happens at 12:05 AM on Jan 1, 2026
	detectionTime := time.Date(2025, 12, 31, 23, 55, 0, 0, time.UTC) // BeginTime
	processingTime := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)   // Would be time.Now() during processing

	testSpecies := "Parus major" // Great Tit

	// First, record a detection for this species in 2025 (before midnight)
	// This simulates having seen the species earlier in 2025
	previousDetection := time.Date(2025, 12, 15, 10, 0, 0, 0, time.UTC)
	// daysSinceFirstSeen is intentionally ignored - we only need to establish prior detection
	isNew, _ := tracker.CheckAndUpdateSpecies(testSpecies, previousDetection)
	assert.True(t, isNew, "First detection in 2025 should be new")

	// Now simulate the cross-midnight scenario
	// The detection occurred at 11:55 PM Dec 31 (detectionTime)
	// If we use BeginTime (correct): species was already seen in 2025, not new
	// If we used time.Now() (bug): would check against Jan 1, 2026, might be "new for year"

	// Verify using detection time (correct behavior after fix)
	statusWithDetectionTime := tracker.GetSpeciesStatus(testSpecies, detectionTime)
	t.Logf("Status with detection time (Dec 31 23:55): IsNew=%v, DaysSinceFirst=%d",
		statusWithDetectionTime.IsNew, statusWithDetectionTime.DaysSinceFirst)

	// Species should NOT be new since it was detected on Dec 15
	assert.False(t, statusWithDetectionTime.IsNew,
		"Species should not be 'new' when checking with detection time (Dec 31)")
	assert.Positive(t, statusWithDetectionTime.DaysSinceFirst,
		"Should have been seen before (Dec 15)")

	// Verify what would happen with processing time (the bug scenario)
	// After year reset on Jan 1, yearly tracking would consider it "new for this year"
	statusWithProcessingTime := tracker.GetSpeciesStatus(testSpecies, processingTime)
	t.Logf("Status with processing time (Jan 1 00:05): IsNew=%v, DaysSinceFirst=%d, IsNewThisYear=%v",
		statusWithProcessingTime.IsNew, statusWithProcessingTime.DaysSinceFirst, statusWithProcessingTime.IsNewThisYear)

	// With processing time (Jan 1), the species would be incorrectly marked as "first of year"
	// because the yearly reset happened on Jan 1 and this is the first check in 2026.
	// This demonstrates why using detection time (BeginTime) is important.
	assert.True(t, statusWithProcessingTime.IsNewThisYear,
		"Using processing time (Jan 1) should incorrectly mark species as new for the year")

	// The key point: using detection time (Dec 31) gives correct behavior,
	// while processing time (Jan 1) gives incorrect behavior
}

// TestNotificationTiming_BeginTimeUsed validates that DatabaseAction.Execute
// uses Note.BeginTime for species tracking, not time.Now().
func TestNotificationTiming_BeginTimeUsed(t *testing.T) {
	t.Parallel()

	mockDS := setupMockDatastore(t)

	// Create species tracker
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
		},
	}

	tracker := species.NewTrackerFromSettings(mockDS, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	// Create a Result with BeginTime set to a specific time
	// This simulates the detection time being different from processing time
	beginTime := time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC)

	testResult := detection.Result{
		Timestamp: beginTime,
		BeginTime: beginTime,
		Species: detection.Species{
			CommonName:     "Great Tit",
			ScientificName: "Parus major",
		},
		Confidence: 0.85,
		AudioSource: detection.AudioSource{
			ID:          "test-source",
			SafeString:  "test-source",
			DisplayName: "Test Source",
		},
	}

	// Create a simple event tracker that allows all events (0 interval = no throttling)
	eventTracker := NewEventTracker(0)

	// Create the DatabaseAction
	action := &DatabaseAction{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{
						Enabled: false, // Disable audio export for this test
					},
				},
			},
		},
		Ds:                mockDS,
		Result:            testResult,
		Results:           nil, // No secondary predictions needed for this test
		EventTracker:      eventTracker,
		NewSpeciesTracker: tracker,
		Description:       "Test Database Action",
		CorrelationID:     "test-correlation-id",
	}

	// Execute the action
	err := action.Execute(nil)
	require.NoError(t, err, "DatabaseAction.Execute should not return error")

	// Verify the species was tracked with the correct time (BeginTime)
	// by checking the species status at BeginTime
	status := tracker.GetSpeciesStatus("Parus major", beginTime)

	// The species should have been recorded as first seen at BeginTime
	assert.True(t, status.IsNew, "Species should be marked as new based on BeginTime")
	assert.Equal(t, 0, status.DaysSinceFirst, "DaysSinceFirst should be 0 for a new detection")

	t.Logf("Species status: IsNew=%v, DaysSinceFirst=%d, IsNewThisYear=%v",
		status.IsNew, status.DaysSinceFirst, status.IsNewThisYear)
}

// TestNotificationTiming_SuppressionWindowUseBeginTime validates that
// notification suppression checks use BeginTime, not time.Now().
func TestNotificationTiming_SuppressionWindowUsesBeginTime(t *testing.T) {
	t.Parallel()

	mockDS := setupMockDatastore(t)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         7,
		SyncIntervalMinutes:          60,
		NotificationSuppressionHours: 1, // 1 hour suppression window
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
		},
	}

	tracker := species.NewTrackerFromSettings(mockDS, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	testSpecies := "Parus major"

	// Record first notification at 10:00 AM
	firstNotificationTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	tracker.RecordNotificationSent(testSpecies, firstNotificationTime)

	// Scenario 1: Detection at 10:30 AM (within suppression window)
	// Should be suppressed
	detection1Time := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	shouldSuppress1 := tracker.ShouldSuppressNotification(testSpecies, detection1Time)
	assert.True(t, shouldSuppress1, "Detection at 10:30 (30 min after first) should be suppressed")

	// Scenario 2: Detection at 11:30 AM (outside suppression window)
	// Should NOT be suppressed
	detection2Time := time.Date(2025, 6, 15, 11, 30, 0, 0, time.UTC)
	shouldSuppress2 := tracker.ShouldSuppressNotification(testSpecies, detection2Time)
	assert.False(t, shouldSuppress2, "Detection at 11:30 (90 min after first) should not be suppressed")

	// Scenario 3: Cross-midnight scenario
	// First notification at 11:30 PM
	lateNotificationTime := time.Date(2025, 6, 15, 23, 30, 0, 0, time.UTC)
	tracker.RecordNotificationSent(testSpecies, lateNotificationTime)

	// Detection at 12:15 AM next day (within 1 hour window)
	crossMidnightTime := time.Date(2025, 6, 16, 0, 15, 0, 0, time.UTC)
	shouldSuppress3 := tracker.ShouldSuppressNotification(testSpecies, crossMidnightTime)
	assert.True(t, shouldSuppress3, "Detection 45 min after late notification should be suppressed even across midnight")

	t.Logf("Suppression tests: 30min=%v, 90min=%v, cross-midnight=%v",
		shouldSuppress1, shouldSuppress2, shouldSuppress3)
}
