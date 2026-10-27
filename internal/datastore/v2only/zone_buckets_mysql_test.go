//go:build integration

package v2only

import "testing"

// TestV2OnlyDatastore_RangeAnalyticsWallClock_MySQL runs the range analytics wall-clock cases
// on MySQL and MariaDB, whose date expression (DATE_ADD with integer DIV) and per-row offset
// CASE in GROUP BY must agree with the Go reference the same way SQLite's do.
func TestV2OnlyDatastore_RangeAnalyticsWallClock_MySQL(t *testing.T) {
	assertRangeAnalyticsWallClock(t, setupMySQLDatastore(t))
}
