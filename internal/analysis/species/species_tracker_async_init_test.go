// species_tracker_async_init_test.go
//
// Tests for asynchronous species-tracker initialization (startup-speed fix).
// On startup the historical load runs in the background so it does not block
// the HTTP server. While the load is in flight the tracker is "warming" and
// must suppress new-species status so it never fires spurious notifications,
// then resume normal behavior once the load completes.

package species

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// asyncTestSettings returns lifetime-only tracking settings to keep the mock
// surface small (yearly/seasonal loads are exercised elsewhere).
func asyncTestSettings() *conf.SpeciesTrackingSettings {
	return &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking:       conf.YearlyTrackingSettings{Enabled: false, WindowDays: 7},
		SeasonalTracking:     conf.SeasonalTrackingSettings{Enabled: false},
	}
}

// TestNewTrackerIsNotWarming verifies a directly-constructed tracker behaves
// normally (not warming) so existing synchronous tests are unaffected.
func TestNewTrackerIsNotWarming(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	tracker := NewTrackerFromSettings(ds, asyncTestSettings())

	assert.False(t, tracker.IsWarming(), "freshly constructed tracker must not be warming")
}

// TestInitFromDatabaseAsyncSuppressesNewSpeciesWhileWarming validates the core
// behavior: while the background load is in flight, the tracker reports no new
// species and records nothing, then serves the loaded historical state once done.
func TestInitFromDatabaseAsyncSuppressesNewSpeciesWhileWarming(t *testing.T) {
	t.Parallel()

	unblock := make(chan struct{})
	ds := mocks.NewMockInterface(t)
	// Block the first historical query to hold the tracker in the warming state.
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ mock.Arguments) { <-unblock }).
		Return([]datastore.NewSpeciesData{
			{ScientificName: "Branta canadensis", FirstSeenDate: "2024-10-01"}, // 20 days before detection
		}, nil).Maybe()
	ds.On("GetActiveNotificationHistory", mock.Anything, mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	ds.On("GetSpeciesDetectionDatesInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.SpeciesDetectionDate{}, nil).Maybe()

	tracker := NewTrackerFromSettings(ds, asyncTestSettings())
	tracker.InitFromDatabaseAsync()

	require.True(t, tracker.IsWarming(), "tracker must be warming immediately after async init starts")

	detectionTime := time.Date(2024, 10, 21, 10, 0, 0, 0, time.UTC)

	// While warming: a species that IS in the database must not be flagged new,
	// and the detection must not be recorded into the (still empty) maps.
	isNew, _ := tracker.CheckAndUpdateSpecies("Branta canadensis", detectionTime)
	assert.False(t, isNew, "new-species status must be suppressed while warming")
	assert.Equal(t, 0, tracker.GetSpeciesCount(), "detections must not be recorded while warming")

	status := tracker.GetSpeciesStatus("Branta canadensis", detectionTime)
	assert.False(t, status.IsNew, "GetSpeciesStatus must report IsNew=false while warming")
	// FirstSeenTime must be the zero value while warming: the SSE feed and the
	// detections REST handler derive "new species" from
	// !FirstSeenTime.IsZero() && note.Date == FirstSeenTime, so a today-dated
	// FirstSeenTime here would flag every live detection as new during warm-up.
	assert.True(t, status.FirstSeenTime.IsZero(), "warming status must leave FirstSeenTime zero to avoid spurious new-species in SSE/detections")

	// Let the background load complete.
	close(unblock)
	require.Eventually(t, func() bool { return !tracker.IsWarming() }, 2*time.Second, 10*time.Millisecond,
		"tracker must leave the warming state after the load completes")

	// After load: the species loaded from the database (first seen 20 days before
	// the detection, outside the 14-day window) is known, not new.
	assert.Equal(t, 1, tracker.GetSpeciesCount(), "loaded historical species must be present after warm-up")
	isNew, days := tracker.CheckAndUpdateSpecies("Branta canadensis", detectionTime)
	assert.False(t, isNew, "known species must not be flagged new after the load completes")
	assert.Equal(t, 20, days, "days-since-first must reflect the loaded historical first-seen date")
}

// TestInitFromDatabaseAsyncGoesLiveOnError verifies the tracker leaves the
// warming state (goes live) even when the historical load fails, so detections
// are processed normally afterwards rather than being suppressed forever.
func TestInitFromDatabaseAsyncGoesLiveOnError(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData(nil), assert.AnError).Maybe()
	ds.On("GetActiveNotificationHistory", mock.Anything, mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()

	tracker := NewTrackerFromSettings(ds, asyncTestSettings())
	tracker.InitFromDatabaseAsync()

	require.Eventually(t, func() bool { return !tracker.IsWarming() }, 2*time.Second, 10*time.Millisecond,
		"tracker must leave the warming state even when the load fails")

	// Live again: a never-before-seen species is now correctly flagged new.
	isNew, _ := tracker.CheckAndUpdateSpecies("Turdus merula", time.Now())
	assert.True(t, isNew, "tracker must process detections normally after a failed load")
}

// TestInitFromDatabaseAsyncCloseCancelsLoad verifies Close cancels the in-flight
// background load via context rather than blocking on it until the DB scan ends,
// so a shutdown landing in the warm-up window returns promptly.
func TestInitFromDatabaseAsyncCloseCancelsLoad(t *testing.T) {
	t.Parallel()

	released := make(chan struct{})
	ds := mocks.NewMockInterface(t)
	// The load blocks until either the context is cancelled (shutdown) or the
	// test explicitly releases it, mirroring a long DB scan that honors ctx.
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ctx, _ := args.Get(0).(context.Context)
			select {
			case <-ctx.Done():
			case <-released:
			}
		}).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	ds.On("GetActiveNotificationHistory", mock.Anything, mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesDetectionDatesInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.SpeciesDetectionDate{}, nil).Maybe()

	tracker := NewTrackerFromSettings(ds, asyncTestSettings())
	tracker.InitFromDatabaseAsync()
	require.True(t, tracker.IsWarming())

	// Close must cancel the load's context and return promptly, not block until
	// the (otherwise indefinite) scan finishes.
	done := make(chan error, 1)
	go func() { done <- tracker.Close() }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		close(released) // unblock the goroutine so the test can exit cleanly
		t.Fatal("Close did not return promptly; the background load was not cancelled")
	}

	assert.False(t, tracker.IsWarming(), "tracker must leave the warming state after Close cancels the load")
}

// TestInitFromDatabaseAsyncNoRaceUnderConcurrentDetections drives detections
// concurrently with the background load to surface data races under -race.
func TestInitFromDatabaseAsyncNoRaceUnderConcurrentDetections(t *testing.T) {
	t.Parallel()

	unblock := make(chan struct{})
	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ mock.Arguments) { <-unblock }).
		Return([]datastore.NewSpeciesData{
			{ScientificName: "Branta canadensis", FirstSeenDate: "2024-10-01"},
		}, nil).Maybe()
	ds.On("GetActiveNotificationHistory", mock.Anything, mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	ds.On("GetSpeciesDetectionDatesInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.SpeciesDetectionDate{}, nil).Maybe()

	tracker := NewTrackerFromSettings(ds, asyncTestSettings())
	tracker.InitFromDatabaseAsync()

	done := make(chan struct{})
	go func() {
		defer close(done)
		now := time.Now()
		for range 200 {
			tracker.CheckAndUpdateSpecies("Branta canadensis", now)
			_ = tracker.GetSpeciesStatus("Branta canadensis", now)
			_ = tracker.GetSpeciesCount()
			_ = tracker.IsNewSpecies("Branta canadensis")
		}
	}()

	close(unblock)
	<-done
	require.Eventually(t, func() bool { return !tracker.IsWarming() }, 2*time.Second, 10*time.Millisecond)

	// Final state reflects the loaded historical data.
	assert.GreaterOrEqual(t, tracker.GetSpeciesCount(), 1)
}
