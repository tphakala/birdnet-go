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
	"github.com/tphakala/birdnet-go/internal/labels/soundevent"
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
	wantMechanical := ds.nonBirdLabelTypeIDs[soundevent.CategoryMechanical]
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
	wantHuman := ds.nonBirdLabelTypeIDs[soundevent.CategoryHuman]
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
			// Only the v2 Get fills the review and lock times, which this detection has;
			// TestDetectionRepository_GetReadsReviewAndLockTimes pins them. Every other
			// read, lists included, still compares whole.
			if res != nil {
				res.FirstReviewedAt, res.LastReviewedAt, res.LockedAt = time.Time{}, time.Time{}, time.Time{}
			}
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

	_, err = repo.GetAdditionalResults(ctx, strconv.FormatUint(uint64(det.ID), 10))
	require.Error(t, err, "GetAdditionalResults")
	require.ErrorIs(t, err, context.Canceled, "GetAdditionalResults")

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

// TestDetectionRepository_GetAdditionalResultsResolvesNames verifies that the v2
// repository resolves the alternates' common names and species codes like a
// detection's own species, strips a stored "Scientific_Common" label to the bare
// scientific name, and returns the full float64 confidence. The bridge it replaces
// returns the scientific name as the common name, no code, and a float32. It also
// checks that a label without a scientific name is skipped and a non-numeric id is
// rejected.
func TestDetectionRepository_GetAdditionalResultsResolvesNames(t *testing.T) {
	t.Parallel()

	const (
		primarySci    = "Turdus merula"
		ravenSci      = "Corvus corax"
		ravenCommon   = "Common Raven"
		ravenCode     = "comrav"
		sparrowSci    = "Passer domesticus"
		sparrowCommon = "House Sparrow"
		sparrowCode   = "houspa"
		// More digits than a float32 holds.
		ravenConfidence = 0.123456789012345
		sparrowConf     = 0.05
		ghostConf       = 0.5
		delta           = 1e-15
	)
	cfg, cfgCleanup := buildTestConfig(t, []string{
		primarySci + "_Eurasian Blackbird",
		ravenSci + "_" + ravenCommon,
		sparrowSci + "_" + sparrowCommon,
	})
	cfg.SpeciesCodeMap = map[string]string{ravenSci: ravenCode, sparrowSci: sparrowCode}
	ds, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, ds.Close()); cfgCleanup() }) // nolint:testifylint // assert not require: cfgCleanup must still run if Close errors; require would Goexit and skip it
	ctx := t.Context()
	repo := NewDetectionRepository(ds)

	save := func(additional []detection.AdditionalResult) *detection.Result {
		t.Helper()
		result := &detection.Result{
			Timestamp:   time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
			AudioSource: detection.AudioSource{SafeString: "rtsp_garage", DisplayName: "Garage"},
			Species:     detection.Species{ScientificName: primarySci, CommonName: "Eurasian Blackbird"},
			Confidence:  0.9,
			Model:       birdModelInfo(),
		}
		require.NoError(t, repo.Save(ctx, result, additional))
		require.NotZero(t, result.ID)
		return result
	}
	idString := func(r *detection.Result) string { return strconv.FormatUint(uint64(r.ID), 10) }

	withAlternates := save([]detection.AdditionalResult{{
		Species:    detection.Species{ScientificName: ravenSci},
		Confidence: ravenConfidence,
	}})

	// A stored "Scientific_Common" label. SavePredictionsBatch keeps the existing
	// rows; SavePredictions would delete them first.
	label, err := ds.label.GetOrCreate(ctx, sparrowSci+"_"+sparrowCommon, ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
	require.NoError(t, err)
	require.NoError(t, ds.detection.SavePredictionsBatch(ctx, []*entities.DetectionPrediction{{
		DetectionID: withAlternates.ID, LabelID: label.ID, Confidence: sparrowConf, Rank: 2,
	}}))

	// A stored label with no scientific part names no species and is skipped.
	ghost, err := ds.label.GetOrCreate(ctx, "_Unnamed", ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
	require.NoError(t, err)
	require.NoError(t, ds.detection.SavePredictionsBatch(ctx, []*entities.DetectionPrediction{{
		DetectionID: withAlternates.ID, LabelID: ghost.ID, Confidence: ghostConf, Rank: 3,
	}}))

	got, err := repo.GetAdditionalResults(ctx, idString(withAlternates))
	require.NoError(t, err)
	require.Len(t, got, 2, "the label without a scientific name must not be returned")
	bySci := make(map[string]detection.AdditionalResult, len(got))
	for _, ar := range got {
		bySci[ar.Species.ScientificName] = ar
	}

	raven, ok := bySci[ravenSci]
	require.True(t, ok, "stored alternate must be returned")
	assert.Equal(t, ravenCommon, raven.Species.CommonName)
	assert.Equal(t, ravenCode, raven.Species.Code)
	assert.InDelta(t, ravenConfidence, raven.Confidence, delta, "confidence must not be narrowed to float32")
	assert.Empty(t, raven.RawLabel, "RawLabel is runtime only and stays empty on read")

	sparrow, ok := bySci[sparrowSci]
	require.True(t, ok, "a stored Scientific_Common label must be returned as the bare scientific name")
	assert.Equal(t, sparrowCommon, sparrow.Species.CommonName)
	assert.Equal(t, sparrowCode, sparrow.Species.Code)
	assert.InDelta(t, sparrowConf, sparrow.Confidence, delta)

	without := save(nil)
	none, err := repo.GetAdditionalResults(ctx, idString(without))
	require.NoError(t, err)
	assert.Empty(t, none)

	_, err = repo.GetAdditionalResults(ctx, "not-an-id")
	require.Error(t, err, "a non-numeric id must be rejected")
}

// TestDetectionRepository_ReadsCarryOriginalSpecies verifies that every read path
// that builds a detection.Result from the v2 store fills OriginalSpecies for a
// detection whose species a reviewer changed, keeps it after the species is changed
// back, and leaves it zero for an untouched detection in the same result set.
func TestDetectionRepository_ReadsCarryOriginalSpecies(t *testing.T) {
	t.Parallel()

	const (
		originalSci    = "Turdus merula"
		originalCommon = "Eurasian Blackbird"
		originalCode   = "eurbla"
		currentSci     = "Parus major"
		currentCommon  = "Great Tit"
		ghostSci       = "Corvus corax"
		date           = "2026-06-01"
		hour           = "12"
	)
	cfg, cfgCleanup := buildTestConfig(t, []string{
		originalSci + "_" + originalCommon,
		currentSci + "_" + currentCommon,
	})
	cfg.SpeciesCodeMap = map[string]string{originalSci: originalCode}
	ds, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, ds.Close()); cfgCleanup() }) // nolint:testifylint // assert not require: cfgCleanup must still run if Close errors; require would Goexit and skip it
	ds.timezone = time.UTC
	ctx := t.Context()
	repo := NewDetectionRepository(ds)

	at := func(minute int) time.Time { return time.Date(2026, 6, 1, 12, minute, 0, 0, time.UTC) }
	changed := seedDetection(t, ds, originalSci, at(0))
	untouched := seedDetection(t, ds, currentSci, at(10))
	require.NoError(t, ds.detection.ChangeSpecies(ctx, changed.ID, currentSci))

	want := detection.Species{ScientificName: originalSci, CommonName: originalCommon, Code: originalCode}
	find := func(results []*detection.Result, id uint) *detection.Result {
		for _, r := range results {
			if r.ID == id {
				return r
			}
		}
		return nil
	}
	idString := func(id uint) string { return strconv.FormatUint(uint64(id), 10) }

	checkLists := func(t *testing.T, wantOriginal detection.Species, skipSpeciesList bool) {
		t.Helper()
		for _, rc := range listReadCalls(date, hour) {
			if skipSpeciesList && rc.name == "GetBySpecies" {
				// The listed species is the one the detection no longer has.
				continue
			}
			results, err := rc.call(ctx, repo)
			require.NoError(t, err, rc.name)
			got := find(results, changed.ID)
			require.NotNil(t, got, "%s did not return the changed detection", rc.name)
			assert.Equal(t, wantOriginal, got.OriginalSpecies, rc.name)
			other := find(results, untouched.ID)
			require.NotNil(t, other, "%s did not return the untouched detection", rc.name)
			assert.Zero(t, other.OriginalSpecies, rc.name)
		}
	}

	t.Run("after a change", func(t *testing.T) {
		got, err := repo.Get(ctx, idString(changed.ID))
		require.NoError(t, err)
		assert.Equal(t, currentSci, got.Species.ScientificName)
		assert.Equal(t, want, got.OriginalSpecies)
		other, err := repo.Get(ctx, idString(untouched.ID))
		require.NoError(t, err)
		assert.Zero(t, other.OriginalSpecies)

		checkLists(t, want, false)
	})

	t.Run("after a change back", func(t *testing.T) {
		require.NoError(t, ds.detection.ChangeSpecies(ctx, changed.ID, originalSci))

		got, err := repo.Get(ctx, idString(changed.ID))
		require.NoError(t, err)
		assert.Equal(t, originalSci, got.Species.ScientificName)
		assert.Equal(t, want, got.OriginalSpecies)

		checkLists(t, want, true)
	})

	t.Run("original label row deleted", func(t *testing.T) {
		ghost := seedDetection(t, ds, ghostSci, at(20))
		require.NoError(t, ds.detection.ChangeSpecies(ctx, ghost.ID, currentSci))
		ghostLabel, err := ds.label.GetOrCreate(ctx, ghostSci, ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
		require.NoError(t, err)
		require.NoError(t, ds.label.Delete(ctx, ghostLabel.ID))

		got, err := repo.Get(ctx, idString(ghost.ID))
		require.NoError(t, err)
		assert.Equal(t, currentSci, got.Species.ScientificName)
		assert.Zero(t, got.OriginalSpecies)

		// The list reads batch the labels: a missing original row is no error there either.
		for _, rc := range listReadCalls(date, hour) {
			results, err := rc.call(ctx, repo)
			require.NoError(t, err, rc.name)
			listed := find(results, ghost.ID)
			require.NotNil(t, listed, "%s did not return the detection with the deleted original label", rc.name)
			assert.Equal(t, currentSci, listed.Species.ScientificName, rc.name)
			assert.Zero(t, listed.OriginalSpecies, rc.name)
		}
	})
}

// TestDetectionRepository_OriginalSpeciesCommonNameFallback pins what the read
// mapping puts in OriginalSpecies.CommonName: the resolved common name for a species
// the name maps know, and the scientific name itself for a legacy alias they lack,
// which the predictions merge treats as unresolved.
func TestDetectionRepository_OriginalSpeciesCommonNameFallback(t *testing.T) {
	t.Parallel()

	const (
		aliasSci     = "Streptopelia senegalensis" // legacy alias, not in the name maps
		canonicalSci = "Spilopelia senegalensis"
		canonicalCom = "Laughing Dove"
		currentSci   = "Parus major"
	)
	ds, cleanup := setupTestDatastoreWithLabels(t, []string{canonicalSci + "_" + canonicalCom, currentSci + "_Great Tit"})
	defer cleanup()
	ds.timezone = time.UTC
	ctx := t.Context()
	repo := NewDetectionRepository(ds)
	seedDetection(t, ds, currentSci, time.Date(2026, 6, 1, 12, 20, 0, 0, time.UTC))

	tests := []struct {
		name string
		sci  string
		want detection.Species
	}{
		{"alias falls back to its scientific name", aliasSci, detection.Species{ScientificName: aliasSci, CommonName: aliasSci}},
		{"canonical species resolves", canonicalSci, detection.Species{ScientificName: canonicalSci, CommonName: canonicalCom}},
	}
	for i, tt := range tests {
		changed := seedDetection(t, ds, tt.sci, time.Date(2026, 6, 1, 12, i, 0, 0, time.UTC))
		require.NoError(t, ds.detection.ChangeSpecies(ctx, changed.ID, currentSci), tt.name)

		got, err := repo.Get(ctx, strconv.FormatUint(uint64(changed.ID), 10))
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.want, got.OriginalSpecies, tt.name)
	}
}

// reviewLockTimeTolerance bounds how far a review or lock time read back may be from
// the moment the test wrote it.
const reviewLockTimeTolerance = time.Minute

// assertNoReviewLockTimes checks that res carries none of the review and lock times.
func assertNoReviewLockTimes(t *testing.T, res *detection.Result, what string) {
	t.Helper()
	assert.True(t, res.FirstReviewedAt.IsZero(), what)
	assert.True(t, res.LastReviewedAt.IsZero(), what)
	assert.True(t, res.LockedAt.IsZero(), what)
}

// assertGetReviewAndLockTimes drives one detection through review, lock, unlock and
// relock with the methods the handlers call and checks the times Get returns after
// each step. It is shared by the SQLite test and the MySQL/MariaDB integration test,
// which runs it with a server time zone that differs from the process zone. It returns
// the detection, left reviewed and locked.
func assertGetReviewAndLockTimes(t *testing.T, ds *Datastore) *entities.Detection {
	t.Helper()
	ctx := t.Context()

	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	ds.timezone = helsinki

	det := seedDetection(t, ds, "Parus major", time.Now().Add(-time.Hour))
	id := strconv.FormatUint(uint64(det.ID), 10)
	repo := NewDetectionRepository(ds)
	get := func() *detection.Result {
		t.Helper()
		res, err := repo.Get(ctx, id)
		require.NoError(t, err)
		return res
	}
	assertNear := func(got time.Time, what string) {
		t.Helper()
		assert.WithinDuration(t, time.Now(), got, reviewLockTimeTolerance, what)
		assert.Same(t, helsinki, got.Location(), what+" must be in the station zone")
	}
	reviewColumns := func(created, updated time.Time) {
		t.Helper()
		// UpdateColumns bypasses GORM's update-time tracking, so the values stay as given.
		require.NoError(t, ds.manager.DB().Model(&entities.DetectionReview{}).
			Where("detection_id = ?", det.ID).
			UpdateColumns(map[string]any{"created_at": created, "updated_at": updated}).Error)
	}

	// A fresh detection has no review and no lock.
	res := get()
	assertNoReviewLockTimes(t, res, "fresh detection")

	// Reviewed once and locked: every time is now, and a review saved once has equal times.
	require.NoError(t, ds.SaveNoteReview(&datastore.NoteReview{NoteID: det.ID, Verified: "correct"}))
	require.NoError(t, ds.LockNote(id))
	res = get()
	assertNear(res.FirstReviewedAt, "firstReviewedAt")
	assertNear(res.LastReviewedAt, "lastReviewedAt")
	assertNear(res.LockedAt, "lockedAt")
	assert.True(t, res.LastReviewedAt.Equal(res.FirstReviewedAt), "a review saved once has equal times")

	// Saved again with the same status: the first time stays, the last moves.
	fixed := time.Date(2025, 3, 3, 9, 30, 15, 0, time.UTC)
	reviewColumns(fixed, fixed)
	require.NoError(t, ds.SaveNoteReview(&datastore.NoteReview{NoteID: det.ID, Verified: "correct"}))
	res = get()
	assert.True(t, res.FirstReviewedAt.Equal(fixed), "firstReviewedAt must keep the first save, got %s", res.FirstReviewedAt)
	assertNear(res.LastReviewedAt, "lastReviewedAt")

	// A review stored with updated_at just before created_at (saved once by an earlier
	// version) never reports the last time before the first.
	reviewColumns(fixed, fixed.Add(-time.Second))
	res = get()
	assert.True(t, res.LastReviewedAt.Equal(res.FirstReviewedAt), "lastReviewedAt must not precede firstReviewedAt")

	// Locking an already locked detection keeps the original lock time.
	require.NoError(t, ds.manager.DB().Model(&entities.DetectionLock{}).
		Where("detection_id = ?", det.ID).UpdateColumns(map[string]any{"locked_at": fixed}).Error)
	require.NoError(t, ds.LockNote(id))
	assert.True(t, get().LockedAt.Equal(fixed), "a second lock must keep the original lockedAt")

	// Unlocking removes the lock time and keeps the review times; locking again sets it.
	require.NoError(t, ds.UnlockNote(id))
	res = get()
	assert.True(t, res.LockedAt.IsZero())
	assert.False(t, res.FirstReviewedAt.IsZero())
	require.NoError(t, ds.LockNote(id))
	assertNear(get().LockedAt, "lockedAt after relock")

	return det
}

// TestDetectionRepository_GetReadsReviewAndLockTimes pins that Get returns the review
// and lock times in the station zone, that no list read does, and that the legacy
// bridge returns none.
func TestDetectionRepository_GetReadsReviewAndLockTimes(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	det := assertGetReviewAndLockTimes(t, ds)
	ctx := t.Context()

	at := time.Unix(det.DetectedAt, 0).In(ds.timezone)
	date, hour := at.Format(time.DateOnly), at.Format("15")
	for _, call := range listReadCalls(date, hour) {
		results, err := call.call(ctx, NewDetectionRepository(ds))
		require.NoError(t, err, call.name)
		var found *detection.Result
		for _, r := range results {
			if r.ID == det.ID {
				found = r
			}
		}
		require.NotNil(t, found, "%s must contain the detection", call.name)
		assertNoReviewLockTimes(t, found, call.name)
	}

	bridged, err := datastore.NewDetectionRepository(ds, ds.timezone).Get(ctx, strconv.FormatUint(uint64(det.ID), 10))
	require.NoError(t, err)
	assertNoReviewLockTimes(t, bridged, "bridge Get")
}
