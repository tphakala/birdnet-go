package v2only

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// benchDBEnv names the environment variable pointing at an existing v2 SQLite
// database for the large-database benchmarks. They skip when it is unset, so they
// never run in CI; point it at a copy, since opening the database may seed lookup
// rows and checkpoints its WAL on close. The benchmarks query fixed dates between
// 2024-05-16 and 2026-06-01, so the database needs detections across that range.
const benchDBEnv = "BIRDNET_BENCH_DB"

// benchTZEnv names the environment variable holding the IANA timezone the
// benchmark datastore buckets dates in; benchDefaultTZ applies when it is unset.
const (
	benchTZEnv     = "BIRDNET_BENCH_TZ"
	benchDefaultTZ = "Europe/Helsinki"
)

// openBenchDatastore opens the database named by BIRDNET_BENCH_DB with the
// production SQLite manager settings and wires a Datastore over it, skipping the
// calling test or benchmark when the variable is unset.
func openBenchDatastore(tb testing.TB) *Datastore {
	tb.Helper()

	path := os.Getenv(benchDBEnv)
	if path == "" {
		tb.Skipf("%s not set; skipping large-database benchmark", benchDBEnv)
	}
	tzName := os.Getenv(benchTZEnv)
	if tzName == "" {
		tzName = benchDefaultTZ
	}
	loc, err := time.LoadLocation(tzName)
	require.NoError(tb, err)

	manager, err := v2.NewSQLiteManager(v2.Config{DirectPath: path})
	require.NoError(tb, err)

	db := manager.DB()
	ds, err := New(&Config{
		Manager:   manager,
		Detection: repository.NewDetectionRepository(db, nil, false, false),
		Label:     repository.NewLabelRepository(db, nil, false, false),
		Model:     repository.NewModelRepository(db, nil, false, false),
		Timezone:  loc,
	})
	require.NoError(tb, err)
	tb.Cleanup(func() { assert.NoError(tb, ds.Close()) })
	return ds
}

// benchSpecies holds scientific names picked from the benchmark database by
// detection volume, so the benchmarks need no knowledge of its species list.
type benchSpecies struct {
	dominant string // most detections
	common   string // benchCommonRank-th most detections
	rare     string // fewest detections
}

// benchCommonRank is the volume rank (0-based) used as the "common" species.
const benchCommonRank = 9

// pickBenchSpecies ranks the detected labels by detection count.
func pickBenchSpecies(tb testing.TB, ds *Datastore) benchSpecies {
	tb.Helper()
	var names []string
	prefix := ds.manager.TablePrefix()
	require.NoError(tb, ds.manager.DB().Raw(
		"SELECT l.scientific_name FROM "+prefix+"detections d JOIN "+prefix+"labels l ON l.id = d.label_id"+
			" GROUP BY l.id, l.scientific_name ORDER BY COUNT(*) DESC, l.id").
		Scan(&names).Error)
	require.NotEmpty(tb, names, "benchmark database has no detections")
	return benchSpecies{
		dominant: names[0],
		common:   names[min(benchCommonRank, len(names)-1)],
		rare:     names[len(names)-1],
	}
}
