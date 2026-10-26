//go:build integration

package v2only

import "testing"

// TestV2OnlyDatastore_HourFilterMatchesDashboardOnDSTDays_MySQL runs the DST day hour filter
// checks on MySQL, whose hour expression ((col + CASE ... END) DIV 3600) % 24 must agree with
// the dashboard cells the same way SQLite's does.
func TestV2OnlyDatastore_HourFilterMatchesDashboardOnDSTDays_MySQL(t *testing.T) {
	assertHourFilterMatchesDashboardOnDSTDays(t, setupMySQLDatastore(t))
}
