//go:build integration

package v2only

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIntegrityResult_MySQLReportsOK verifies the non-SQLite branch: PRAGMA
// quick_check is SQLite-specific, so on MySQL the accessor must report "ok"
// (healthy) rather than "" or a "skipped"-style sentinel. DatabaseIntegrityCheck.Run
// treats "" as StatusUnknown and any non-"ok", non-empty result as corruption, so
// only "ok" avoids both a permanent Unknown and a false corruption alarm on MySQL.
func TestIntegrityResult_MySQLReportsOK(t *testing.T) {
	ds := setupMySQLDatastore(t)

	result, corrupted := ds.IntegrityResult()
	assert.Equal(t, "ok", result, "MySQL has no PRAGMA quick_check; report healthy so the check reads neither Unknown nor corrupted")
	assert.False(t, corrupted)
}
