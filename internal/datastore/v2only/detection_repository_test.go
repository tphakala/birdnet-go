package v2only

import (
	"testing"
	"time"
	_ "time/tzdata" // zone data for the named locations, independent of the host

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
)

// TestDetectionRepository_SaveStoresTimestampInRepeatedHour verifies that the
// stored detected_at is the detection's own instant for both occurrences of a
// wall-clock hour that repeats at the end of DST. Formatting the instant as wall
// strings and parsing them back stores one occurrence of each pair shifted.
func TestDetectionRepository_SaveStoresTimestampInRepeatedHour(t *testing.T) {
	t.Parallel()

	// Each zone lists the two UTC instants at which its clock reads the same
	// wall time (first the DST occurrence, then the standard time one).
	tests := []struct {
		name     string
		zone     string
		wallTime string
		first    time.Time
		second   time.Time
	}{
		{
			name: "New York", zone: "America/New_York", wallTime: "2026-11-01 01:30",
			first:  time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC),
			second: time.Date(2026, time.November, 1, 6, 30, 0, 0, time.UTC),
		},
		{
			name: "Helsinki", zone: "Europe/Helsinki", wallTime: "2026-10-25 03:30",
			first:  time.Date(2026, time.October, 25, 0, 30, 0, 0, time.UTC),
			second: time.Date(2026, time.October, 25, 1, 30, 0, 0, time.UTC),
		},
		{
			// A 30 minute shift: an hour-only shortcut would not pass.
			name: "Lord Howe", zone: "Australia/Lord_Howe", wallTime: "2026-04-05 01:50",
			first:  time.Date(2026, time.April, 4, 14, 50, 0, 0, time.UTC),
			second: time.Date(2026, time.April, 4, 15, 20, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			loc, err := time.LoadLocation(tt.zone)
			require.NoError(t, err)

			cfg, cleanup := buildTestConfig(t, nil)
			defer cleanup()
			cfg.Timezone = loc
			ds, err := New(cfg)
			require.NoError(t, err)
			defer func() { assert.NoError(t, ds.Close()) }()

			repo := NewDetectionRepository(ds)
			ctx := t.Context()

			for i, utc := range []time.Time{tt.first, tt.second} {
				at := utc.In(loc)
				require.Equal(t, tt.wallTime, at.Format("2006-01-02 15:04"), "occurrence %d wall time", i+1)

				result := &detection.Result{
					Timestamp: at,
					Species:   detection.Species{ScientificName: "Parus major", CommonName: "Great Tit"},
					Model:     detection.DefaultModelInfo(),
				}
				require.NoError(t, repo.Save(ctx, result, nil), "occurrence %d", i+1)
				require.NotZero(t, result.ID, "Save must propagate the new row ID (occurrence %d)", i+1)

				stored, err := ds.detection.Get(ctx, result.ID)
				require.NoError(t, err)
				assert.Equal(t, at.Unix(), stored.DetectedAt, "occurrence %d must keep its own instant", i+1)
			}
		})
	}
}

// TestDetectionRepository_SaveMapsResultFields verifies that every field of a
// detection.Result and its additional results reaches the stored rows, including
// the raw labels that only show as non-bird label types.
func TestDetectionRepository_SaveMapsResultFields(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ctx := t.Context()
	repo := NewDetectionRepository(ds)

	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	begin := at.Add(-3 * time.Second)
	end := at.Add(3 * time.Second)
	// More digits than a float32 holds: the v2 path stores the full float64.
	const predConfidence = 0.123456789012345

	result := &detection.Result{
		Timestamp:      at,
		SourceNode:     "garage-node",
		AudioSource:    detection.AudioSource{SafeString: "rtsp_garage", DisplayName: "Garage"},
		Species:        detection.Species{ScientificName: "power", CommonName: "Power Tool"},
		RawLabel:       "power_tool",
		Confidence:     0.91,
		Unlikely:       true,
		Latitude:       60.17,
		Longitude:      24.94,
		ClipName:       "clips/power.wav",
		ClipSampleRate: 48000,
		BeginTime:      begin,
		EndTime:        end,
		ProcessingTime: 150 * time.Millisecond,
		Model:          perchModelInfo(),
	}
	additional := []detection.AdditionalResult{{
		Species:    detection.Species{ScientificName: "speech", CommonName: "Speech"},
		Confidence: predConfidence,
		RawLabel:   "speech",
	}}

	require.NoError(t, repo.Save(ctx, result, additional))
	require.NotZero(t, result.ID)

	var det entities.Detection
	require.NoError(t, ds.manager.DB().Preload("Label").Preload("Source").First(&det, result.ID).Error)

	assert.Equal(t, at.Unix(), det.DetectedAt)
	assert.InDelta(t, 0.91, det.Confidence, 1e-9)
	assert.True(t, det.Unlikely)
	require.NotNil(t, det.Latitude)
	assert.InDelta(t, 60.17, *det.Latitude, 1e-9)
	require.NotNil(t, det.Longitude)
	assert.InDelta(t, 24.94, *det.Longitude, 1e-9)
	require.NotNil(t, det.ClipName)
	assert.Equal(t, "clips/power.wav", *det.ClipName)
	require.NotNil(t, det.ClipSampleRate)
	assert.Equal(t, 48000, *det.ClipSampleRate)
	require.NotNil(t, det.BeginTime)
	assert.Equal(t, begin.UnixMilli(), *det.BeginTime)
	require.NotNil(t, det.EndTime)
	assert.Equal(t, end.UnixMilli(), *det.EndTime)
	require.NotNil(t, det.ProcessingTimeMs)
	assert.Equal(t, int64(150), *det.ProcessingTimeMs)

	require.NotNil(t, det.Source, "the audio source must be stored")
	assert.Equal(t, "rtsp_garage", det.Source.SourceURI)
	assert.Equal(t, "garage-node", det.Source.NodeName)
	require.NotNil(t, det.Source.DisplayName)
	assert.Equal(t, "Garage", *det.Source.DisplayName)

	// A dropped RawLabel falls back to the species label type.
	wantMechanical := ds.nonBirdLabelTypeIDs[nonbird.CategoryMechanical]
	require.NotZero(t, wantMechanical)
	require.NotNil(t, det.Label)
	assert.Equal(t, "power", det.Label.ScientificName)
	assert.Equal(t, wantMechanical, det.Label.LabelTypeID, "primary raw label must classify the label")

	var preds []entities.DetectionPrediction
	require.NoError(t, ds.manager.DB().Preload("Label").Where("detection_id = ?", result.ID).Find(&preds).Error)
	require.Len(t, preds, 1)
	assert.InDelta(t, predConfidence, preds[0].Confidence, 1e-15, "prediction confidence must not be narrowed to float32")
	require.NotNil(t, preds[0].Label)
	assert.Equal(t, "speech", preds[0].Label.ScientificName)
	wantHuman := ds.nonBirdLabelTypeIDs[nonbird.CategoryHuman]
	require.NotZero(t, wantHuman)
	assert.Equal(t, wantHuman, preds[0].Label.LabelTypeID, "prediction raw label must classify the label")
}
