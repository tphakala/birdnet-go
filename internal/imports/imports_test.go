package imports_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/imports"
	"github.com/tphakala/birdnet-go/internal/imports/birdnetpi"
)

// --- fixture helpers ---

type birdnetPiRow struct {
	Date       string
	Time       string
	SciName    string
	ComName    string
	Confidence float64
	Lat        float64
	Lon        float64
	Cutoff     float64
	Sens       float64
	FileName   string
}

func newFixtureDB(t *testing.T, rows []birdnetPiRow) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "birds.db")

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)

	require.NoError(t, db.Exec(`CREATE TABLE detections (
		Date DATE,
		Time TIME,
		Sci_Name VARCHAR(100) NOT NULL,
		Com_Name VARCHAR(100) NOT NULL,
		Confidence FLOAT,
		Lat FLOAT,
		Lon FLOAT,
		Cutoff FLOAT,
		Week INT,
		Sens FLOAT,
		Overlap FLOAT,
		File_Name VARCHAR(100) NOT NULL
	)`).Error)

	for _, r := range rows {
		require.NoError(t, db.Exec(
			`INSERT INTO detections (Date, Time, Sci_Name, Com_Name, Confidence, Lat, Lon, Cutoff, Week, Sens, Overlap, File_Name)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 0.0, ?)`,
			r.Date, r.Time, r.SciName, r.ComName, r.Confidence,
			r.Lat, r.Lon, r.Cutoff, r.Sens, r.FileName,
		).Error)
	}

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	return path
}

func newTestStore(t *testing.T) datastore.Interface {
	t.Helper()
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = tempDir + "/test.db"

	store := datastore.New(settings)
	require.NoError(t, store.Open(), "failed to open test datastore")

	t.Cleanup(func() {
		assert.NoError(t, store.Close(), "failed to close test datastore")
	})

	return store
}

// --- tests ---

func TestValidate_NoDetectionsTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.db")

	// Create an empty SQLite file with no tables.
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	src, err := birdnetpi.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, src.Close()) })

	err = src.Validate(t.Context())
	assert.Error(t, err, "Validate must fail when detections table is absent")
}

func TestCount(t *testing.T) {
	rows := []birdnetPiRow{
		{Date: "2025-03-25", Time: "14:27:32", SciName: "Dendrocopos major", ComName: "Great Spotted Woodpecker", Confidence: 0.74, Lat: 60.75, Lon: 24.77, Cutoff: 0.7, Sens: 1.25, FileName: "a.mp3"},
		{Date: "2025-03-25", Time: "15:00:00", SciName: "Parus major", ComName: "Great Tit", Confidence: 0.81, Lat: 60.75, Lon: 24.77, Cutoff: 0.5, Sens: 1.0, FileName: "b.mp3"},
	}
	path := newFixtureDB(t, rows)

	src, err := birdnetpi.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, src.Close()) })

	count, err := src.Count(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestSimpleSave(t *testing.T) {
	store := newTestStore(t)
	repo := datastore.NewDetectionRepository(store, time.UTC)

	now := time.Date(2025, 3, 25, 14, 27, 32, 0, time.UTC)
	result := &detection.Result{
		Timestamp:  now,
		SourceNode: "birdnet-pi",
		Species: detection.Species{
			ScientificName: "Dendrocopos major",
			CommonName:     "Great Spotted Woodpecker",
			Code:           "",
		},
		Confidence:  0.7357,
		Latitude:    60.7534,
		Longitude:   24.7709,
		Threshold:   0.7,
		Sensitivity: 1.25,
		Model: detection.ModelInfo{
			Name:    "BirdNET",
			Version: "birdnet-pi",
			Variant: "import",
		},
		ClipName: "",
	}

	ctx := t.Context()
	err := repo.Save(ctx, result, nil)
	require.NoError(t, err, "Save should not error")
	require.NotZero(t, result.ID, "ID should be assigned")

	t.Logf("Saved with ID: %d", result.ID)
}

func TestImport_FieldMapping(t *testing.T) {
	rows := []birdnetPiRow{
		{
			Date: "2025-03-25", Time: "14:27:32",
			SciName: "Dendrocopos major", ComName: "Great Spotted Woodpecker",
			Confidence: 0.7357, Lat: 60.7534, Lon: 24.7709, Cutoff: 0.7, Sens: 1.25,
			FileName: "woodpecker.mp3",
		},
	}
	path := newFixtureDB(t, rows)

	src, err := birdnetpi.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, src.Close()) })

	store := newTestStore(t)
	repo := datastore.NewDetectionRepository(store, time.UTC)
	engine := imports.NewEngine(repo)

	opts := imports.ImportOptions{
		SourceNode: imports.DefaultSourceNode,
		Location:   time.UTC,
		BatchSize:  100,
	}

	stats, err := engine.Run(t.Context(), src, opts, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Inserted)
	assert.Equal(t, 0, stats.Errors)

	results, _, err := repo.Search(t.Context(), &datastore.DetectionFilters{
		Location: []string{imports.DefaultSourceNode},
		Limit:    10,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)

	r := results[0]
	assert.Equal(t, "Dendrocopos major", r.Species.ScientificName)
	assert.Equal(t, "Great Spotted Woodpecker", r.Species.CommonName)
	assert.Empty(t, r.Species.Code)
	assert.InDelta(t, 0.7357, r.Confidence, 0.0001)
	assert.InDelta(t, 60.7534, r.Latitude, 0.0001)
	assert.InDelta(t, 24.7709, r.Longitude, 0.0001)
	assert.InDelta(t, 0.7, r.Threshold, 0.0001)
	assert.InDelta(t, 1.25, r.Sensitivity, 0.0001)
	assert.Equal(t, imports.DefaultSourceNode, r.SourceNode)
	// The synthetic Model marker (Version "birdnet-pi", Variant "import") is set at mapping
	// time, but the legacy datastore stores Note.Model as gorm:"-" (runtime-only), so only
	// Name survives the save+read round trip on this path. Assert just the Name here.
	assert.Equal(t, "BirdNET", r.Model.Name)
	assert.Empty(t, r.ClipName, "ClipName must be empty in DB-only mode")

	expectedTS := time.Date(2025, 3, 25, 14, 27, 32, 0, time.UTC)
	assert.Equal(t, expectedTS.UTC(), r.Timestamp.UTC())
}

func TestImport_SkipsUnparseableDate(t *testing.T) {
	rows := []birdnetPiRow{
		{Date: "2025-03-25", Time: "10:00:00", SciName: "Parus major", ComName: "Great Tit", Confidence: 0.85, Lat: 60.0, Lon: 24.0, Cutoff: 0.5, Sens: 1.0, FileName: "valid.mp3"},
		{Date: "not-a-date", Time: "08:00:00", SciName: "Erithacus rubecula", ComName: "European Robin", Confidence: 0.90, Lat: 60.0, Lon: 24.0, Cutoff: 0.5, Sens: 1.0, FileName: "invalid.mp3"},
	}
	path := newFixtureDB(t, rows)

	src, err := birdnetpi.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, src.Close()) })

	store := newTestStore(t)
	repo := datastore.NewDetectionRepository(store, time.UTC)
	engine := imports.NewEngine(repo)
	opts := imports.ImportOptions{SourceNode: imports.DefaultSourceNode, Location: time.UTC}

	stats, err := engine.Run(t.Context(), src, opts, nil)
	require.NoError(t, err)

	assert.Equal(t, 1, stats.Inserted, "only the row with valid date should be inserted")
	assert.Equal(t, 1, stats.Errors, "unparseable date row must be counted as error")
}

func TestImport_Idempotent(t *testing.T) {
	rows := []birdnetPiRow{
		{Date: "2025-04-01", Time: "09:00:00", SciName: "Turdus merula", ComName: "Common Blackbird", Confidence: 0.9200, Lat: 60.0, Lon: 24.0, Cutoff: 0.5, Sens: 1.0, FileName: "blackbird.mp3"},
	}
	path := newFixtureDB(t, rows)

	store := newTestStore(t)
	repo := datastore.NewDetectionRepository(store, time.UTC)
	opts := imports.ImportOptions{SourceNode: imports.DefaultSourceNode, Location: time.UTC}

	// First run.
	src1, err := birdnetpi.New(path)
	require.NoError(t, err)
	engine1 := imports.NewEngine(repo)
	stats1, err := engine1.Run(t.Context(), src1, opts, nil)
	require.NoError(t, err)
	require.NoError(t, src1.Close())
	assert.Equal(t, 1, stats1.Inserted)

	// Second run: fresh engine re-loads keys from DB.
	src2, err := birdnetpi.New(path)
	require.NoError(t, err)
	engine2 := imports.NewEngine(repo)
	stats2, err := engine2.Run(t.Context(), src2, opts, nil)
	require.NoError(t, err)
	require.NoError(t, src2.Close())
	assert.Equal(t, 0, stats2.Inserted, "re-run must insert zero rows")
	assert.Equal(t, 1, stats2.Skipped, "re-run must skip the already-imported row")
	assert.Equal(t, 0, stats2.Errors, "re-run must not produce any errors")
}

func TestImport_WithinSourceDuplicate(t *testing.T) {
	dupRows := []birdnetPiRow{
		{Date: "2025-04-02", Time: "11:00:00", SciName: "Fringilla coelebs", ComName: "Common Chaffinch", Confidence: 0.8800, Lat: 60.0, Lon: 24.0, Cutoff: 0.5, Sens: 1.0, FileName: "chaffinch.mp3"},
		{Date: "2025-04-02", Time: "11:00:00", SciName: "Fringilla coelebs", ComName: "Common Chaffinch", Confidence: 0.8800, Lat: 60.0, Lon: 24.0, Cutoff: 0.5, Sens: 1.0, FileName: "chaffinch.mp3"},
	}
	path := newFixtureDB(t, dupRows)

	store := newTestStore(t)
	repo := datastore.NewDetectionRepository(store, time.UTC)
	engine := imports.NewEngine(repo)
	opts := imports.ImportOptions{SourceNode: imports.DefaultSourceNode, Location: time.UTC}

	src, err := birdnetpi.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, src.Close()) })

	stats, err := engine.Run(t.Context(), src, opts, nil)
	require.NoError(t, err)

	assert.Equal(t, 1, stats.Inserted, "within-source duplicate must be inserted only once")
	assert.Equal(t, 1, stats.Skipped, "second copy of the duplicate must be skipped")
}

func TestImport_ContextCancellation(t *testing.T) {
	manyRows := make([]birdnetPiRow, 0, 200)
	for i := range 200 {
		manyRows = append(manyRows, birdnetPiRow{
			Date:       "2025-05-01",
			Time:       "10:00:00",
			SciName:    "Parus major",
			ComName:    "Great Tit",
			Confidence: float64(i+1) * 0.001,
			Lat:        60.0,
			Lon:        24.0,
			Cutoff:     0.5,
			Sens:       1.0,
			FileName:   "tit.mp3",
		})
	}
	path := newFixtureDB(t, manyRows)

	store := newTestStore(t)
	repo := datastore.NewDetectionRepository(store, time.UTC)
	engine := imports.NewEngine(repo)
	opts := imports.ImportOptions{
		SourceNode: imports.DefaultSourceNode,
		Location:   time.UTC,
		BatchSize:  10,
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately.

	src, err := birdnetpi.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, src.Close()) })

	_, err = engine.Run(ctx, src, opts, nil)
	assert.ErrorIs(t, err, context.Canceled, "cancelled import must return context error")
}

// TestImport_Idempotent_TimezoneMismatch verifies that idempotency holds even when
// opts.Location differs from the repository's timezone. The datastore reconstructs
// Timestamp from stored Date/Time wall-clock strings in its own timezone (UTC here),
// so a detectionKey based on ts.Unix() would differ when opts.Location is non-UTC.
// The wall-clock key (ts.Format) is timezone-independent and must deduplicate correctly.
func TestImport_Idempotent_TimezoneMismatch(t *testing.T) {
	rows := []birdnetPiRow{
		{
			Date: "2025-06-01", Time: "12:00:00",
			SciName: "Sylvia atricapilla", ComName: "Eurasian Blackcap",
			Confidence: 0.8800, Lat: 60.0, Lon: 24.0, Cutoff: 0.5, Sens: 1.0,
			FileName: "blackcap.mp3",
		},
	}
	path := newFixtureDB(t, rows)

	// Repository built with UTC (simulates the repo's read-back timezone).
	store := newTestStore(t)
	repo := datastore.NewDetectionRepository(store, time.UTC)

	// First run: import with a non-UTC location.
	nonUTCLoc := time.FixedZone("test", 3*3600)
	opts := imports.ImportOptions{
		SourceNode: imports.DefaultSourceNode,
		Location:   nonUTCLoc,
	}

	src1, err := birdnetpi.New(path)
	require.NoError(t, err)
	engine1 := imports.NewEngine(repo)
	stats1, err := engine1.Run(t.Context(), src1, opts, nil)
	require.NoError(t, err)
	require.NoError(t, src1.Close())
	require.Equal(t, 1, stats1.Inserted, "first run must insert the row")

	// Second run: same non-UTC location. Must skip the already-imported row.
	src2, err := birdnetpi.New(path)
	require.NoError(t, err)
	engine2 := imports.NewEngine(repo)
	stats2, err := engine2.Run(t.Context(), src2, opts, nil)
	require.NoError(t, err)
	require.NoError(t, src2.Close())
	assert.Equal(t, 0, stats2.Inserted, "re-run must insert zero rows even with non-UTC import location")
	assert.Equal(t, 1, stats2.Skipped, "re-run must skip the already-imported row")
	assert.Equal(t, 0, stats2.Errors, "re-run must not produce any errors")
}

// TestValidate_MissingColumn verifies that Validate rejects a database whose
// detections table is missing a required column.
func TestValidate_MissingColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad_schema.db")

	// Create a detections table that is missing Sci_Name and several other columns.
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE detections (
		Date DATE,
		Time TIME,
		Com_Name VARCHAR(100) NOT NULL,
		Confidence FLOAT
	)`).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	src, err := birdnetpi.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, src.Close()) })

	err = src.Validate(t.Context())
	assert.Error(t, err, "Validate must fail when required columns are missing")
}

// cancelOnFirstReport is a ProgressReporter that cancels a context on its first
// report call with stats.Inserted > 0, ensuring cancellation happens after the
// engine has inserted at least one row.
type cancelOnFirstReport struct {
	cancel context.CancelFunc
	once   bool
}

func (r *cancelOnFirstReport) Report(stats imports.ImportStats) {
	if !r.once && stats.Inserted > 0 {
		r.once = true
		r.cancel()
	}
}

// TestImport_CancelDuringSave verifies that a context cancellation after at least
// one batch has been saved causes Run to return context.Canceled without
// miscounting the cancelled row as a save failure (stats.Errors must stay 0).
func TestImport_CancelDuringSave(t *testing.T) {
	// Use enough rows that the import cannot complete in one batch.
	rows := make([]birdnetPiRow, 0, 500)
	for i := range 500 {
		rows = append(rows, birdnetPiRow{
			Date:       "2025-06-10",
			Time:       fmt.Sprintf("%02d:00:00", i%24),
			SciName:    "Phylloscopus trochilus",
			ComName:    "Willow Warbler",
			Confidence: float64(i+1) * 0.001,
			Lat:        60.0, Lon: 24.0, Cutoff: 0.5, Sens: 1.0,
			FileName: fmt.Sprintf("warbler_%d.mp3", i),
		})
	}
	path := newFixtureDB(t, rows)

	store := newTestStore(t)
	repo := datastore.NewDetectionRepository(store, time.UTC)

	src, err := birdnetpi.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, src.Close()) })

	engine := imports.NewEngine(repo)
	opts := imports.ImportOptions{
		SourceNode: imports.DefaultSourceNode,
		Location:   time.UTC,
		BatchSize:  10,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// reporter cancels the context on its first Report call, which the engine issues
	// at the end of the first batch. This guarantees at least one batch has been
	// processed before cancellation - no timing dependency.
	reporter := &cancelOnFirstReport{cancel: cancel}

	stats, err := engine.Run(ctx, src, opts, reporter)
	require.ErrorIs(t, err, context.Canceled, "mid-import cancel must return context.Canceled")
	assert.GreaterOrEqual(t, stats.Inserted, 1, "at least one row must have been inserted before cancel")
	assert.Equal(t, 0, stats.Errors, "cancelled row must not be counted as a save error")
}
