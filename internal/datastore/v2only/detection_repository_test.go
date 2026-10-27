package v2only

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
	_ "time/tzdata" // zone data for the named locations, independent of the host

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
)

// repeatedHourZone names a zone and the two UTC instants at which its clock reads
// the same wall time (first the DST occurrence, then the standard time one).
type repeatedHourZone struct {
	name     string
	zone     string
	wallTime string
	hour     string // the repeated wall hour, as the hour filters take it
	first    time.Time
	second   time.Time
}

// repeatedHourZones are the zones the repeated-hour tests run in.
var repeatedHourZones = []repeatedHourZone{
	{
		name: "New York", zone: "America/New_York", wallTime: "2026-11-01 01:30", hour: "01",
		first:  time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC),
		second: time.Date(2026, time.November, 1, 6, 30, 0, 0, time.UTC),
	},
	{
		name: "Helsinki", zone: "Europe/Helsinki", wallTime: "2026-10-25 03:30", hour: "03",
		first:  time.Date(2026, time.October, 25, 0, 30, 0, 0, time.UTC),
		second: time.Date(2026, time.October, 25, 1, 30, 0, 0, time.UTC),
	},
	{
		// A 30 minute shift: an hour-only shortcut would not pass.
		name: "Lord Howe", zone: "Australia/Lord_Howe", wallTime: "2026-04-05 01:50", hour: "01",
		first:  time.Date(2026, time.April, 4, 14, 50, 0, 0, time.UTC),
		second: time.Date(2026, time.April, 4, 15, 20, 0, 0, time.UTC),
	},
}

// TestDetectionRepository_SaveStoresTimestampInRepeatedHour verifies that the
// stored detected_at is the detection's own instant for both occurrences of a
// wall-clock hour that repeats at the end of DST. Formatting the instant as wall
// strings and parsing them back stores one occurrence of each pair shifted.
func TestDetectionRepository_SaveStoresTimestampInRepeatedHour(t *testing.T) {
	t.Parallel()

	for _, tt := range repeatedHourZones {
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

// TestDetectionRepository_SearchMinIDCursorVisitsAllRows verifies that cursor
// pagination (WithMinID) pages by id, so every row is visited even when ids do
// not follow detection time, as after an import of older recordings.
func TestDetectionRepository_SearchMinIDCursorVisitsAllRows(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ctx := t.Context()

	// The insert order differs from the time order: every other row is older
	// than the one inserted before it, so a time-sorted page has a larger last
	// id than the rows that follow it.
	const (
		rowCount = 10
		pageSize = 3
		maxPages = 20 // guard against an endless loop on a regression
	)
	base := time.Date(2024, 1, 10, 10, 0, 0, 0, time.UTC)
	wantIDs := make(map[uint]bool, rowCount)
	for i := range rowCount {
		at := base.AddDate(0, 0, i)
		if i%2 == 1 {
			at = base.AddDate(0, 0, -5-i) // older than every even row
		}
		wantIDs[seedDetection(t, ds, "Parus major", at).ID] = true
	}

	repo := NewDetectionRepository(ds)
	visited := make(map[uint]bool, rowCount)
	var last uint
	for range maxPages {
		results, _, err := repo.Search(ctx, datastore.NewDetectionFilters().WithMinID(last).WithLimit(pageSize))
		require.NoError(t, err)
		if len(results) == 0 {
			break
		}
		for _, r := range results {
			assert.Greater(t, r.ID, last, "page must continue after the cursor")
			visited[r.ID] = true
			last = r.ID
		}
	}

	assert.Equal(t, wantIDs, visited, "every row must be visited once by cursor pagination")
}

// readCall is one read method of the DetectionRepository with its arguments bound.
type readCall struct {
	name string
	call func(ctx context.Context, repo datastore.DetectionRepository) ([]*detection.Result, error)
}

// withoutTotal adapts a read that returns no total to a readCall.
func withoutTotal(read func(ctx context.Context, repo datastore.DetectionRepository) ([]*detection.Result, int64, error)) func(ctx context.Context, repo datastore.DetectionRepository) ([]*detection.Result, error) {
	return func(ctx context.Context, repo datastore.DetectionRepository) ([]*detection.Result, error) {
		results, _, err := read(ctx, repo)
		return results, err
	}
}

// listReadCalls returns the list read methods bound to a date and hour. Get is not a
// list read and is exercised per ID by the callers.
func listReadCalls(date, hour string) []readCall {
	const listLimit = 10
	return []readCall{
		{"GetRecent", func(ctx context.Context, repo datastore.DetectionRepository) ([]*detection.Result, error) {
			return repo.GetRecent(ctx, listLimit)
		}},
		{"Search", withoutTotal(func(ctx context.Context, repo datastore.DetectionRepository) ([]*detection.Result, int64, error) {
			return repo.Search(ctx, datastore.NewDetectionFilters().WithLimit(listLimit))
		})},
		{"SearchAdvanced", withoutTotal(func(ctx context.Context, repo datastore.DetectionRepository) ([]*detection.Result, int64, error) {
			return repo.SearchAdvanced(ctx, &datastore.AdvancedSearchFilters{Limit: listLimit})
		})},
		{"GetBySpecies", withoutTotal(func(ctx context.Context, repo datastore.DetectionRepository) ([]*detection.Result, int64, error) {
			return repo.GetBySpecies(ctx, "Parus major", date, hour, 1, listLimit, 0)
		})},
		{"GetByDateRange", withoutTotal(func(ctx context.Context, repo datastore.DetectionRepository) ([]*detection.Result, int64, error) {
			return repo.GetByDateRange(ctx, date, date, listLimit, 0)
		})},
		{"GetHourly", withoutTotal(func(ctx context.Context, repo datastore.DetectionRepository) ([]*detection.Result, int64, error) {
			return repo.GetHourly(ctx, date, hour, 1, listLimit, 0)
		})},
	}
}

// TestDetectionRepository_ReadsReturnStoredInstantInRepeatedHour verifies that every
// read method returns each detection's stored instant, in the station zone, for both
// occurrences of a wall-clock hour that repeats at the end of DST. Reading through the
// formatted Date and Time text parses one occurrence of each pair back shifted.
func TestDetectionRepository_ReadsReturnStoredInstantInRepeatedHour(t *testing.T) {
	t.Parallel()

	for _, tt := range repeatedHourZones {
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

			want := make(map[uint]time.Time)
			for _, utc := range []time.Time{tt.first, tt.second} {
				at := utc.In(loc)
				result := &detection.Result{
					Timestamp: at,
					Species:   detection.Species{ScientificName: "Parus major", CommonName: "Great Tit"},
					Model:     detection.DefaultModelInfo(),
				}
				require.NoError(t, repo.Save(ctx, result, nil))
				want[result.ID] = at
			}
			date := tt.wallTime[:len("2006-01-02")]

			assertInstants := func(t *testing.T, got []*detection.Result) {
				t.Helper()
				require.Len(t, got, len(want))
				for _, r := range got {
					at, ok := want[r.ID]
					require.True(t, ok, "unexpected detection %d", r.ID)
					assert.True(t, at.Equal(r.Timestamp), "detection %d: want instant %s, got %s", r.ID, at, r.Timestamp)
					assert.Equal(t, at.Format(time.RFC3339), r.Timestamp.Format(time.RFC3339), "detection %d offset", r.ID)
					assert.Same(t, loc, r.Timestamp.Location(), "detection %d zone", r.ID)
				}
			}

			t.Run("Get", func(t *testing.T) {
				got := make([]*detection.Result, 0, len(want))
				for id := range want {
					r, err := repo.Get(ctx, strconv.FormatUint(uint64(id), 10))
					require.NoError(t, err)
					got = append(got, r)
				}
				assertInstants(t, got)
			})
			for _, rc := range listReadCalls(date, tt.hour) {
				t.Run(rc.name, func(t *testing.T) {
					got, err := rc.call(ctx, repo)
					require.NoError(t, err)
					assertInstants(t, got)
				})
			}
		})
	}
}

// TestDetectionRepository_ReadsMatchBridge verifies that the v2 repository returns the
// same values as the datastore.Interface bridge over the same Datastore for every read
// method: results, totals, errors, and nil versus empty slices.
func TestDetectionRepository_ReadsMatchBridge(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ds.timezone = time.UTC
	ctx := t.Context()

	v2 := NewDetectionRepository(ds)
	bridge := datastore.NewDetectionRepository(ds, ds.timezone)

	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	full := &detection.Result{
		Timestamp:      at,
		AudioSource:    detection.AudioSource{SafeString: "rtsp_garage", DisplayName: "Garage"},
		Species:        detection.Species{ScientificName: "Parus major", CommonName: "Great Tit"},
		Confidence:     0.91,
		Unlikely:       true,
		Latitude:       60.17,
		Longitude:      24.94,
		ClipName:       "clips/parus.wav",
		ClipSampleRate: 48000,
		BeginTime:      at.Add(-3 * time.Second),
		EndTime:        at.Add(3 * time.Second),
		ProcessingTime: 150 * time.Millisecond,
		Model:          perchModelInfo(),
	}
	require.NoError(t, v2.Save(ctx, full, nil))
	id := strconv.FormatUint(uint64(full.ID), 10)
	require.NoError(t, ds.SaveNoteReview(&datastore.NoteReview{NoteID: full.ID, Verified: "correct"}))
	require.NoError(t, ds.LockNote(id))
	require.NoError(t, ds.SaveNoteComment(&datastore.NoteComment{NoteID: full.ID, Entry: "a comment", CreatedAt: at}))
	bare := seedDetection(t, ds, "Parus major", at.Add(time.Hour))
	bareID := strconv.FormatUint(uint64(bare.ID), 10)
	// A detection whose audio retention removed while its spectrogram was kept.
	const spectrogramClip = "2026/06/blue-tit.wav"
	spectrogramOnly := seedDetection(t, ds, "Cyanistes caeruleus", at.AddDate(0, 0, 1))
	require.NoError(t, ds.manager.DB().Model(&entities.Detection{}).
		Where("id = ?", spectrogramOnly.ID).Update("clip_name", spectrogramClip).Error)
	retained, err := ds.RetainNoteSpectrogramsByClipNames([]string{spectrogramClip})
	require.NoError(t, err)
	require.Equal(t, int64(1), retained)
	spectrogramOnlyID := strconv.FormatUint(uint64(spectrogramOnly.ID), 10)

	// readOutcome is everything one read returns. Errors compare by text.
	type readOutcome struct {
		Results []*detection.Result
		Total   int64
		Err     string
	}
	outcome := func(results []*detection.Result, total int64, err error) readOutcome {
		o := readOutcome{Results: results, Total: total}
		if err != nil {
			o.Err = err.Error()
		}
		return o
	}

	const listLimit = 10
	reads := []struct {
		name string
		read func(repo datastore.DetectionRepository) readOutcome
	}{
		{"Get full", func(r datastore.DetectionRepository) readOutcome {
			res, err := r.Get(ctx, id)
			return outcome([]*detection.Result{res}, 0, err)
		}},
		{"Get bare", func(r datastore.DetectionRepository) readOutcome {
			res, err := r.Get(ctx, bareID)
			return outcome([]*detection.Result{res}, 0, err)
		}},
		{"Get spectrogram only", func(r datastore.DetectionRepository) readOutcome {
			res, err := r.Get(ctx, spectrogramOnlyID)
			require.NoError(t, err)
			assert.Equal(t, spectrogramClip, res.SpectrogramClipName)
			assert.True(t, res.IsSpectrogramOnly())
			return outcome([]*detection.Result{res}, 0, err)
		}},
		{"Get missing", func(r datastore.DetectionRepository) readOutcome {
			res, err := r.Get(ctx, "9999")
			return outcome([]*detection.Result{res}, 0, err)
		}},
		{"Get invalid ID", func(r datastore.DetectionRepository) readOutcome {
			res, err := r.Get(ctx, "x")
			return outcome([]*detection.Result{res}, 0, err)
		}},
		{"GetRecent", func(r datastore.DetectionRepository) readOutcome {
			res, err := r.GetRecent(ctx, listLimit)
			return outcome(res, 0, err)
		}},
		{"GetRecent limit 1", func(r datastore.DetectionRepository) readOutcome {
			res, err := r.GetRecent(ctx, 1)
			return outcome(res, 0, err)
		}},
		{"Search nil filters", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.Search(ctx, nil)
			return outcome(res, total, err)
		}},
		{"Search species and dates", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.Search(ctx, datastore.NewDetectionFilters().
				WithSpecies("Parus major").WithDateRange("2026-06-01", "2026-06-01").WithLimit(1))
			return outcome(res, total, err)
		}},
		{"Search bad date", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.Search(ctx, datastore.NewDetectionFilters().WithDateRange("not-a-date", ""))
			return outcome(res, total, err)
		}},
		{"Search no match", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.Search(ctx, datastore.NewDetectionFilters().WithSpecies("Turdus merula"))
			return outcome(res, total, err)
		}},
		{"SearchAdvanced empty filters", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.SearchAdvanced(ctx, &datastore.AdvancedSearchFilters{Limit: listLimit})
			return outcome(res, total, err)
		}},
		{"SearchAdvanced species confidence sorted", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.SearchAdvanced(ctx, &datastore.AdvancedSearchFilters{
				Species:    []string{"Parus major", "Cyanistes caeruleus"},
				Confidence: &datastore.ConfidenceFilter{Operator: ">=", Value: 0.5},
				SortBy:     "species_asc",
				Limit:      listLimit,
			})
			return outcome(res, total, err)
		}},
		{"SearchAdvanced scientific names", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.SearchAdvanced(ctx, &datastore.AdvancedSearchFilters{
				SpeciesScientific: []string{"Cyanistes caeruleus"}, Limit: listLimit,
			})
			return outcome(res, total, err)
		}},
		{"SearchAdvanced source", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.SearchAdvanced(ctx, &datastore.AdvancedSearchFilters{
				Source: []string{"rtsp_garage"}, Limit: listLimit,
			})
			return outcome(res, total, err)
		}},
		{"SearchAdvanced nil filters", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.SearchAdvanced(ctx, nil)
			require.ErrorIs(t, err, datastore.ErrNilAdvancedSearchFilters)
			return outcome(res, total, err)
		}},
		{"SearchAdvanced second page", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.SearchAdvanced(ctx, &datastore.AdvancedSearchFilters{Limit: 1, Offset: 1})
			return outcome(res, total, err)
		}},
		{"GetBySpecies known", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetBySpecies(ctx, "Parus major", "", "", 1, 1, 1)
			return outcome(res, total, err)
		}},
		{"GetBySpecies default limit", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetBySpecies(ctx, "Parus major", "", "", 1, 0, 0)
			return outcome(res, total, err)
		}},
		{"GetBySpecies unknown", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetBySpecies(ctx, "Unknown species", "", "", 1, 0, 0)
			return outcome(res, total, err)
		}},
		{"GetBySpecies date", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetBySpecies(ctx, "Parus major", "2026-06-01", "", 1, listLimit, 0)
			return outcome(res, total, err)
		}},
		{"GetBySpecies hour and duration", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetBySpecies(ctx, "Parus major", "2026-06-01", "12", 2, listLimit, 0)
			return outcome(res, total, err)
		}},
		{"GetBySpecies hour without match", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetBySpecies(ctx, "Parus major", "2026-06-01", "03", 1, listLimit, 0)
			return outcome(res, total, err)
		}},
		{"GetByDateRange dates", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetByDateRange(ctx, "2026-06-01", "2026-06-02", listLimit, 0)
			return outcome(res, total, err)
		}},
		{"GetByDateRange empty dates", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetByDateRange(ctx, "", "", listLimit, 0)
			return outcome(res, total, err)
		}},
		{"GetByDateRange bad date", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetByDateRange(ctx, "not-a-date", "", listLimit, 0)
			return outcome(res, total, err)
		}},
		{"GetHourly", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetHourly(ctx, "2026-06-01", "12", 1, listLimit, 0)
			return outcome(res, total, err)
		}},
		{"GetHourly empty hour", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetHourly(ctx, "2026-06-01", "03", 1, listLimit, 0)
			return outcome(res, total, err)
		}},
		{"GetHourly bad hour", func(r datastore.DetectionRepository) readOutcome {
			res, total, err := r.GetHourly(ctx, "2026-06-01", "x", 1, listLimit, 0)
			return outcome(res, total, err)
		}},
	}

	// wantTotals pins the new reads to rows, so two empty outcomes cannot match vacuously.
	wantTotals := map[string]int64{
		"SearchAdvanced empty filters":             3,
		"SearchAdvanced species confidence sorted": 3,
		"SearchAdvanced scientific names":          1,
		"SearchAdvanced source":                    1,
		"GetBySpecies date":                        2,
		"GetBySpecies hour and duration":           2,
		"GetBySpecies hour without match":          0,
	}

	for _, rd := range reads {
		t.Run(rd.name, func(t *testing.T) {
			want := rd.read(bridge)
			got := rd.read(v2)
			if total, ok := wantTotals[rd.name]; ok {
				assert.Equal(t, total, got.Total, "total")
			}
			// Whole values compare equal: both paths build times from Unix seconds or a
			// wall time parsed in the same *time.Location, with no monotonic reading.
			assert.Equal(t, want, got)
		})
	}
}

// TestDetectionRepository_ReadsHonourCancelledContext verifies that the v2 reads pass
// the caller's context to their queries.
func TestDetectionRepository_ReadsHonourCancelledContext(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	det := seedDetection(t, ds, "Parus major", time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	repo := NewDetectionRepository(ds)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := repo.Get(ctx, strconv.FormatUint(uint64(det.ID), 10))
	require.Error(t, err, "Get")
	require.ErrorIs(t, err, context.Canceled, "Get")

	for _, rc := range listReadCalls("2026-06-01", "12") {
		_, err := rc.call(ctx, repo)
		require.Error(t, err, rc.name)
		assert.ErrorIs(t, err, context.Canceled, rc.name)
	}
}

// cancelAfterChecks is a context that cancels itself once Err has been called more than
// limit times, so a test can end the context at every point of a read.
type cancelAfterChecks struct {
	context.Context
	cancel context.CancelFunc
	limit  int64
	checks atomic.Int64
}

func (c *cancelAfterChecks) Err() error {
	if c.checks.Add(1) > c.limit {
		c.cancel()
	}
	return c.Context.Err()
}

// TestDetectionRepository_ListReadsNeverReturnUnresolvedSpecies verifies that a list read
// whose context ends part way returns an error, not rows whose species were not loaded.
func TestDetectionRepository_ListReadsNeverReturnUnresolvedSpecies(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ds.timezone = time.UTC
	const (
		species  = "Parus major"
		rowCount = 3
		// maxCancelPoint is far above the number of context checks one read makes.
		maxCancelPoint = 500
	)
	for i := range rowCount {
		seedDetection(t, ds, species, time.Date(2026, 6, 1, 12+i, 0, 0, 0, time.UTC))
	}
	repo := NewDetectionRepository(ds)

	for _, rc := range listReadCalls("2026-06-01", "12") {
		t.Run(rc.name, func(t *testing.T) {
			completed := false
			for limit := range maxCancelPoint {
				base, cancel := context.WithCancel(t.Context())
				ctx := &cancelAfterChecks{Context: base, cancel: cancel, limit: int64(limit)}
				results, err := rc.call(ctx, repo)
				cancel()
				if err != nil {
					continue
				}
				require.NotEmpty(t, results, "context ended after %d checks", limit)
				for _, r := range results {
					require.Equal(t, species, r.Species.ScientificName, "context ended after %d checks", limit)
				}
				completed = true
			}
			assert.True(t, completed, "no cancel point let the read finish")
		})
	}
}

// TestSearchAdvanced_DefaultSortMatchesSearchNotes verifies that the repository's
// SearchAdvanced with SortBySearchDefault returns the same IDs, in the same order, with
// the same total as SearchNotes, for the plain text queries the detection list API
// serves, at the first and a second page.
func TestSearchAdvanced_DefaultSortMatchesSearchNotes(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastoreWithLabels(t, []string{
		"Corvus corone_Corneille noire",
		"Corvus frugilegus_Corbeau freux",
		"Erithacus rubecula_Rougegorge familier",
	})
	defer cleanup()
	ds.timezone = time.UTC
	ctx := t.Context()
	repo := NewDetectionRepository(ds)

	// Later rows are older, so an id-ordered result differs from a time-ordered one.
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	names := []string{
		"Corvus corone", "Erithacus rubecula", "Corvus frugilegus",
		"Corvus corone", "Erithacus rubecula", "Corvus corone",
	}
	for i, name := range names {
		seedDetection(t, ds, name, base.Add(-time.Duration(i)*time.Hour))
	}

	const pageSize = 2
	queries := []struct {
		name  string
		query string
		want  int64
	}{
		{"empty query", "", 6},
		{"scientific substring", "Corvus", 4},
		{"common name substring", "Rougegorge", 2},
		{"no match", "Pinson", 0},
	}
	for _, q := range queries {
		for _, offset := range []int{0, pageSize} {
			t.Run(q.name+" offset "+strconv.Itoa(offset), func(t *testing.T) {
				notes, noteTotal, err := ds.SearchNotes(q.query, false, pageSize, offset)
				require.NoError(t, err)
				require.Equal(t, q.want, noteTotal, "SearchNotes total")

				results, total, err := repo.SearchAdvanced(ctx, &datastore.AdvancedSearchFilters{
					TextQuery: q.query,
					SortBy:    datastore.SortBySearchDefault,
					Limit:     pageSize,
					Offset:    offset,
				})
				require.NoError(t, err)

				assert.Equal(t, noteTotal, total, "total")
				wantIDs := make([]uint, len(notes))
				for i := range notes {
					wantIDs[i] = notes[i].ID
				}
				gotIDs := make([]uint, len(results))
				for i := range results {
					gotIDs[i] = results[i].ID
				}
				assert.Equal(t, wantIDs, gotIDs, "IDs in order")
			})
		}
	}
}

// TestDetectionRepository_GetBySpeciesDefaultLimit verifies that a limit of zero or less
// returns DefaultDetectionLimit rows while the total counts every match.
func TestDetectionRepository_GetBySpeciesDefaultLimit(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ds.timezone = time.UTC
	const extraRows = 1
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := range datastore.DefaultDetectionLimit + extraRows {
		seedDetection(t, ds, "Parus major", base.Add(time.Duration(i)*time.Minute))
	}
	repo := NewDetectionRepository(ds)
	bridge := datastore.NewDetectionRepository(ds, ds.timezone)

	for name, r := range map[string]datastore.DetectionRepository{"v2": repo, "bridge": bridge} {
		for _, limit := range []int{0, -1} {
			results, total, err := r.GetBySpecies(t.Context(), "Parus major", "", "", 1, limit, 0)
			require.NoError(t, err, name)
			assert.Len(t, results, datastore.DefaultDetectionLimit, "%s limit %d", name, limit)
			assert.Equal(t, int64(datastore.DefaultDetectionLimit+extraRows), total, "%s limit %d total", name, limit)
		}
	}
}
