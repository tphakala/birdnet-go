//go:build integration

package v2

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMySQL_PrivacyFilterReads runs the privacy filter read methods on MySQL
// under both table layouts, so the v2_ table names of the reads are checked
// against a real MySQL schema.
func TestMySQL_PrivacyFilterReads(t *testing.T) {
	for _, useV2Prefix := range []bool{true, false} {
		t.Run(fmt.Sprintf("useV2Prefix=%t", useV2Prefix), func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)

			mgr := newTestMySQLManager(t, useV2Prefix)
			t.Cleanup(func() { _ = mgr.Close() })
			require.NoError(t, mgr.Initialize())

			exercisePrivacyReads(t, mgr.DB(), useV2Prefix, true)

			require.NoError(t, mgr.Delete())
		})
	}
}
