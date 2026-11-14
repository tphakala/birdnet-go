//go:build integration

package v2only

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDetectionRepository_GetReadsReviewAndLockTimes_MySQL runs the review and lock
// time checks on MySQL and MariaDB with a server time zone that differs from the
// process zone. A lock time taken from the server's CURRENT_TIMESTAMP would read back
// as another instant.
func TestDetectionRepository_GetReadsReviewAndLockTimes_MySQL(t *testing.T) {
	ds := setupMySQLDatastoreWith(t, mysqlDatastoreOptions{serverZone: mysqlTestServerZone})

	var sessionZone string
	require.NoError(t, ds.manager.DB().Raw("SELECT @@session.time_zone").Scan(&sessionZone).Error)
	require.Equal(t, mysqlTestServerZone, sessionZone, "server time zone did not take effect")

	assertGetReviewAndLockTimes(t, ds)
}
