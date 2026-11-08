//go:build integration

package repository_test

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// TestWallClockSQLMatchesGo_MySQL pins that the MySQL and MariaDB hour, date and year
// expressions equal Go's time package for every toolchain zone around the shifted epoch,
// including instants whose local time is before 1970. It runs on the server image in
// MYSQL_TEST_IMAGE.
func TestWallClockSQLMatchesGo_MySQL(t *testing.T) {
	repository.AssertWallClockSQLMatchesGo(t, testDB, true)
}
