package analysis

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2only"
)

// TestInitializeV2OnlyMode_ComputesDawnChorusOnset pins that the enhanced-database (v2-only)
// datastore opened on restart is wired with a sun calculator for the station location. Without
// it civil dawn is never available, so GET /api/v2/analytics/time/dawn-onset reports a null onset
// for every day and the dawn-chorus chart stays empty.
func TestInitializeV2OnlyMode_ComputesDawnChorusOnset(t *testing.T) {
	datastoreV2.ResetDatabaseMode()
	defer datastoreV2.ResetDatabaseMode()

	const (
		// Mid-latitude station: civil dawn is defined on every day of the year.
		stationLatitude  = 50.0
		stationLongitude = 10.0
		onsetDate        = "2026-05-15"
		detectionCount   = 5 // matches the v2only minimum detections for a day to yield an onset
		minuteStep       = 5
	)

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(t.TempDir(), "birdnet.db")
	settings.BirdNET.Latitude = stationLatitude
	settings.BirdNET.Longitude = stationLongitude

	// Create the v2 schema at the configured path, then reopen it the way a restart does.
	fresh, err := v2only.InitializeFreshInstall(settings, nil, nil)
	require.NoError(t, err)
	require.NoError(t, fresh.Close())

	ds, err := initializeV2OnlyMode(settings)
	require.NoError(t, err)
	defer func() { _ = ds.Close() }()

	for i := range detectionCount {
		require.NoError(t, ds.Save(&datastore.Note{
			Date:           onsetDate,
			Time:           fmt.Sprintf("05:%02d:00", i*minuteStep),
			ScientificName: "Turdus merula",
			CommonName:     "Eurasian Blackbird",
			Confidence:     0.9,
		}, nil))
	}

	got, err := ds.GetDailyActivityOnset(t.Context(), onsetDate, onsetDate, "")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, detectionCount, got[0].DetectionCount)
	assert.NotNil(t, got[0].OnsetRelMinutes, "onset relative to civil dawn must be computed in v2-only mode")
}
