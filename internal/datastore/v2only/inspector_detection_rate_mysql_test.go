//go:build integration

package v2only

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// mysqlTestServerZone is a fixed, non-whole-hour offset so the old
// FROM_UNIXTIME buckets differ from the UTC ones in both the hour and the day.
const mysqlTestServerZone = "+05:45"

// TestV2OnlyDatastore_DetectionRateLabelsUTC_MySQL runs the detection rate label
// checks on MySQL and MariaDB with the server time zone set to a non-UTC offset,
// for a fresh install (no table prefix) and for an install migrated from the
// legacy schema (v2_ table prefix).
func TestV2OnlyDatastore_DetectionRateLabelsUTC_MySQL(t *testing.T) {
	tests := []struct {
		name        string
		useV2Prefix bool
		wantPrefix  string
	}{
		{name: "fresh install, no table prefix", useV2Prefix: false, wantPrefix: ""},
		{name: "migrated install, v2_ table prefix", useV2Prefix: true, wantPrefix: "v2_"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds := setupMySQLDatastoreWith(t, mysqlDatastoreOptions{
				serverZone:  mysqlTestServerZone,
				useV2Prefix: tt.useV2Prefix,
			})
			require.Equal(t, tt.wantPrefix, ds.manager.TablePrefix(), "table prefix did not take effect")

			var sessionZone string
			require.NoError(t, ds.manager.DB().Raw("SELECT @@session.time_zone").Scan(&sessionZone).Error)
			require.Equal(t, mysqlTestServerZone, sessionZone, "server time zone did not take effect")

			assertDetectionRateLabelsUTC(t, ds)
		})
	}
}

// TestV2OnlyDatastore_DetectionRateDaily_MySQL_HugeDaysFails pins that a day
// count whose window in seconds does not fit in 64 bits is an error, not a
// wrapped cutoff that silently returns no rows.
func TestV2OnlyDatastore_DetectionRateDaily_MySQL_HugeDaysFails(t *testing.T) {
	ds := setupMySQLDatastore(t)
	_, err := ds.GetDetectionRateDaily(math.MaxInt)
	require.Error(t, err)
}
