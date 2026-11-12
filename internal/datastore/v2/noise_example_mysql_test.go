//go:build integration

package v2

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMySQL_NoiseExampleRepositories runs the noise example statements on MySQL
// and MariaDB under both table layouts. It catches a review reason expression the
// server reads differently, a BLOB column that does not round trip, a foreign key
// that does not clear the link on delete, a unique index that treats NULL links as
// equal, and a drop list that orders the table after detections.
func TestMySQL_NoiseExampleRepositories(t *testing.T) {
	for _, useV2Prefix := range []bool{true, false} {
		t.Run(fmt.Sprintf("useV2Prefix=%t", useV2Prefix), func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)

			mgr := newTestMySQLManager(t, useV2Prefix)
			t.Cleanup(func() { _ = mgr.Close() })
			require.NoError(t, mgr.Initialize())

			exerciseNoiseExampleRepositories(t, mgr.DB(), useV2Prefix, true)

			require.NoError(t, mgr.Delete())
		})
	}
}
