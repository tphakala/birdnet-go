package species

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// newCanonicalTestTracker builds a tracker whose lifetime history is seeded from the
// given rows (as returned by GetNewSpeciesDetections), with yearly/seasonal tracking
// and notification suppression disabled to keep the canonical-name behavior isolated.
func newCanonicalTestTracker(t *testing.T, lifetime []datastore.NewSpeciesData) *SpeciesTracker {
	t.Helper()

	mockDS := mocks.NewMockInterface(t)
	// Required (not .Maybe()): InitFromDatabase must load lifetime history, which is
	// the premise of every test built on this helper. Asserting the call catches a
	// regression where the lifetime load stops firing.
	mockDS.EXPECT().
		GetNewSpeciesDetections(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(lifetime, nil).Once()
	mockDS.EXPECT().
		GetActiveNotificationHistory(mock.Anything, mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	mockDS.EXPECT().
		GetSpeciesFirstDetectionInPeriod(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
	}
	tracker := NewTrackerFromSettings(mockDS, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())
	return tracker
}

// TestSpeciesTracker_CanonicalName_LegacyHistoryNotNew verifies that a species whose
// history is stored under a legacy scientific name is not reported as "new" when the
// same taxon is later detected under its canonical name. The tracker canonicalizes
// both the loaded history and the live lookup so they collapse to one identity.
func TestSpeciesTracker_CanonicalName_LegacyHistoryNotNew(t *testing.T) {
	t.Parallel()

	tracker := newCanonicalTestTracker(t, []datastore.NewSpeciesData{
		{ScientificName: "Streptopelia senegalensis", FirstSeenDate: "2020-01-01", LastSeenDate: "2020-01-02"},
	})

	isNew, _ := tracker.CheckAndUpdateSpecies("Spilopelia senegalensis", time.Now())
	assert.False(t, isNew,
		"canonical detection of a species seen under its legacy name must not be new")
}

// TestSpeciesTracker_CanonicalName_IsNewSpeciesAliasAware verifies the IsNewSpecies
// lookup path is also alias-aware.
func TestSpeciesTracker_CanonicalName_IsNewSpeciesAliasAware(t *testing.T) {
	t.Parallel()

	tracker := newCanonicalTestTracker(t, []datastore.NewSpeciesData{
		{ScientificName: "Streptopelia senegalensis", FirstSeenDate: "2020-01-01", LastSeenDate: "2020-01-02"},
	})

	assert.False(t, tracker.IsNewSpecies("Spilopelia senegalensis"),
		"IsNewSpecies must recognize the canonical name of a species seen under its legacy name")
}

// TestSpeciesTracker_CanonicalName_MergeKeepsEarliestFirstSeen verifies that when a
// legacy-named and a canonical-named lifetime row for one taxon collapse onto the
// canonical key, the merge keeps the EARLIEST first-seen. The canonical (later) row
// is listed last so a last-write-wins regression would store the later date and fail
// this test.
func TestSpeciesTracker_CanonicalName_MergeKeepsEarliestFirstSeen(t *testing.T) {
	t.Parallel()

	tracker := newCanonicalTestTracker(t, []datastore.NewSpeciesData{
		{ScientificName: "Streptopelia senegalensis", FirstSeenDate: "2019-06-01", LastSeenDate: "2019-06-02"},
		{ScientificName: "Spilopelia senegalensis", FirstSeenDate: "2021-03-01", LastSeenDate: "2021-03-02"},
	})

	status := tracker.GetSpeciesStatus("Spilopelia senegalensis", time.Now())
	assert.Equal(t, "2019-06-01", status.FirstSeenTime.Format(time.DateOnly),
		"merge must keep the earliest first-seen when aliased rows collapse onto one key")
}

// TestSpeciesTracker_CanonicalName_GenuinelyNewSpeciesStillNew guards that
// canonicalization does not suppress real new-species detections.
func TestSpeciesTracker_CanonicalName_GenuinelyNewSpeciesStillNew(t *testing.T) {
	t.Parallel()

	tracker := newCanonicalTestTracker(t, []datastore.NewSpeciesData{
		{ScientificName: "Streptopelia senegalensis", FirstSeenDate: "2020-01-01", LastSeenDate: "2020-01-02"},
	})

	isNew, _ := tracker.CheckAndUpdateSpecies("Turdus merula", time.Now())
	assert.True(t, isNew, "a species absent from history must still be reported as new")
}
