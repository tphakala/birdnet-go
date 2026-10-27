package repository

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/testutil"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

const (
	// oracleTable is the per-connection temporary table the oracle fills with instants and offsets.
	oracleTable = "wall_clock_oracle"
	// oracleInsertBatch is the number of rows per INSERT statement of the oracle.
	oracleInsertBatch = 400
	// oracleMaxReported caps the mismatch lines the oracle prints, so a regression stays readable.
	oracleMaxReported = 8
	// oracleRecentInstant is one ordinary 2026 instant (2026-06-15 12:00:00 UTC) that every zone
	// also gets, so rows after the epoch are covered next to the ones before it.
	oracleRecentInstant = 1781524800
)

// oracleRow is one instant with the offset the SQL applies and the wall time Go derives from them.
type oracleRow struct {
	unix   int64
	offset int
	at     time.Time // the instant in a fixed zone of offset seconds
}

// oracleRows returns, for every toolchain zone, instants whose local time lies around the shifted
// epoch (1970-01-01 00:00 at the zone's own offset there) and around day and hour boundaries
// below and above it, plus fixed instants before the epoch and one recent instant.
func oracleRows(t *testing.T) []oracleRow {
	t.Helper()
	zones := testutil.ToolchainZones(t)
	names := slices.Sorted(maps.Keys(zones))

	shifted := []int64{
		-wallClockSecondsPerDay - 1, -wallClockSecondsPerDay, -wallClockSecondsPerDay + 1,
		-wallClockSecondsPerHour - 1, -wallClockSecondsPerHour, -wallClockSecondsPerHour + 1,
		-1, 0, 1,
		wallClockSecondsPerHour - 1, wallClockSecondsPerHour,
		wallClockSecondsPerDay - 1, wallClockSecondsPerDay,
	}
	fixed := []int64{-365*wallClockSecondsPerDay - 1, -1, 0, 1, oracleRecentInstant}

	rows := make([]oracleRow, 0, len(names)*(len(shifted)+len(fixed)))
	for _, name := range names {
		loc := zones[name]
		epochOffset := int64(wallclock.OffsetAt(loc, time.Unix(0, 0)))
		instants := make([]int64, 0, len(shifted)+len(fixed))
		for _, x := range shifted {
			instants = append(instants, x-epochOffset)
		}
		instants = append(instants, fixed...)
		for _, c := range instants {
			_, off := time.Unix(c, 0).In(loc).Zone()
			rows = append(rows, oracleRow{unix: c, offset: off, at: time.Unix(c, 0).In(time.FixedZone("", off))})
		}
	}
	return rows
}

// AssertWallClockSQLMatchesGo inserts instants and offsets for every toolchain zone into a
// temporary table of db and checks that the hour, date and year SQL of the repository builders
// equals Go's time package for each row. isMySQL selects the dialect. It lives in this test file
// so the MySQL suite (package repository_test, integration tag) can call it too.
func AssertWallClockSQLMatchesGo(t *testing.T, db *sql.DB, isMySQL bool) {
	t.Helper()
	ctx := context.Background() //nolint:gocritic // helper shared by suites; the caller's t.Context is not required here

	// A temporary table is private to its connection, so pin one.
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	// Closing returns the connection to the pool with its temporary table, so drop it first.
	t.Cleanup(func() {
		_, _ = conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+oracleTable)
		_ = conn.Close()
	})

	_, err = conn.ExecContext(ctx, "CREATE TEMPORARY TABLE "+oracleTable+" (unix_s BIGINT NOT NULL, utc_offset INTEGER NOT NULL)")
	require.NoError(t, err)

	rows := oracleRows(t)
	for batch := range slices.Chunk(rows, oracleInsertBatch) {
		placeholders := make([]string, len(batch))
		args := make([]any, 0, len(batch)*2)
		for i, r := range batch {
			placeholders[i] = "(?, ?)"
			args = append(args, r.unix, r.offset)
		}
		_, err = conn.ExecContext(ctx, "INSERT INTO "+oracleTable+" (unix_s, utc_offset) VALUES "+strings.Join(placeholders, ", "), args...)
		require.NoError(t, err)
	}

	query := fmt.Sprintf("SELECT unix_s, utc_offset, %s, %s, %s FROM %s",
		wallClockHourSQL("unix_s", "utc_offset", isMySQL),
		wallClockDateSQL("unix_s", "utc_offset", isMySQL),
		wallClockYearSQL("unix_s", "utc_offset", isMySQL),
		oracleTable)
	result, err := conn.QueryContext(ctx, query)
	require.NoError(t, err)
	defer func() { _ = result.Close() }()

	want := make(map[[2]int64]oracleRow, len(rows))
	for _, r := range rows {
		want[[2]int64{r.unix, int64(r.offset)}] = r
	}

	var seen, mismatches int
	var reported []string
	for result.Next() {
		var unix, offset, hour, year int64
		var date string
		require.NoError(t, result.Scan(&unix, &offset, &hour, &date, &year))
		seen++
		r, ok := want[[2]int64{unix, offset}]
		require.True(t, ok, "unexpected row unix=%d offset=%d", unix, offset)
		wantDate := wallclock.DateOf(r.at).String()
		if hour != int64(r.at.Hour()) || date != wantDate || year != int64(r.at.Year()) {
			mismatches++
			if len(reported) < oracleMaxReported {
				reported = append(reported, fmt.Sprintf("unix=%d offset=%d: sql hour=%d date=%s year=%d, go hour=%d date=%s year=%d",
					unix, offset, hour, date, year, r.at.Hour(), wantDate, r.at.Year()))
			}
		}
	}
	require.NoError(t, result.Err())
	assert.Equal(t, len(rows), seen, "row count")
	assert.Zero(t, mismatches, "%d of %d rows differ from Go, first %d:\n%s", mismatches, seen, len(reported), strings.Join(reported, "\n"))
}

// TestWallClockSQLMatchesGo_SQLite pins that the SQLite hour, date and year expressions equal
// Go's time package for every toolchain zone around the shifted epoch, including instants whose
// local time is before 1970.
func TestWallClockSQLMatchesGo_SQLite(t *testing.T) {
	db := setupDetectionTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	AssertWallClockSQLMatchesGo(t, sqlDB, false)
}
